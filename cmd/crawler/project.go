package main

import (
	"fmt"
	"regexp"

	"github.com/PuerkitoBio/goquery"
	tbr_errors "gitlab.com/bronger/tools/errors"
	tbr_logging "gitlab.com/bronger/tools/logging"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

// Types of the projecter interface are able to scan data from a project HTML
// page and fill their internal data structure with it.  Then, they can prepare
// the data structures needed to write it to the MongoDB database.  This
// abstraction is useful because GEPRIS projects fall into some categories with
// different HTML parsing and datafield requirements.
type projecter interface {
	scanDocument(logger tbr_logging.Logger, doc *goquery.Document) error
	updateMongo() (bson.M, bson.M)
	isDifferent(oldProject map[string]any) bool
	getIdentifier() string
}

// projectBase contains the data included in every DFG project.  This is an
// abstract type that is only embedded into others.  It must have an
// identifier, a title, and a process.
type projectBase struct {
	identifier         string
	title              string
	shortTitle         string
	process            string
	startYear, endYear int
	disciplines        []string
	url                string
	description        string
	parent             string
	parentTitle        string
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectBase) scanDocument(logger tbr_logging.Logger, doc *goquery.Document) error {
	var err error
	if project.title, err = getField(doc.Find("div.details > h1")); err != nil {
		whitelist := []string{"G:(GEPRIS)61027617", "G:(GEPRIS)142091898", "G:(GEPRIS)221781256"}
		if slices.Contains(whitelist, project.identifier) {
			project.title = fmt.Sprintf("DFG project %v (no proper title known)", project.identifier)
		} else {
			return err
		}
	}
	if project.title == "" {
		return fmt.Errorf("project has no title")
	}
	project.process, err = getField(doc.Find(`span.name`).FilterFunction(filterByRegexp(processRegexp)).Next())
	tbr_errors.ExitOnExpectedError(err, "HTML document from GEPRIS has unexpected format", 10)
	if project.process == "" {
		return fmt.Errorf("project has no DFG process")
	}
	if parent, err := getParent(doc); err != nil {
		return err
	} else {
		if parent != "" {
			logger.Debug("Found parent project")
			project.parent = parent
		}
	}
	project.disciplines = parseDisciplines(doc)
	if len(project.disciplines) == 0 {
		logger.Warn("Project has no discipline")
	}
	project.startYear, project.endYear, err = parseTimespan(doc)
	tbr_errors.ExitOnExpectedError(err, "HTML document from GEPRIS has unexpected format", 10)
	project.description, err = getField(doc.Find("div#projekttext"))
	tbr_errors.ExitOnExpectedError(err, "HTML document from GEPRIS has unexpected format", 10)
	if project.description == "" {
		return fmt.Errorf("project has no description (not even empty)")
	} else if project.description == "Keine Zusammenfassung vorhanden" {
		project.description = ""
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectBase) updateMongo() (bson.M, bson.M) {
	filter := bson.M{"identifier": project.identifier}
	update := bson.M{
		"identifier":  project.identifier,
		"title":       project.title,
		"shortTitle":  project.shortTitle,
		"process":     project.process,
		"startYear":   project.startYear,
		"endYear":     project.endYear,
		"disciplines": project.disciplines,
		"url":         project.url,
		"description": project.description,
		"parent":      project.parent,
		"parentTitle": project.parentTitle,
	}
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectBase) isDifferent(oldProject map[string]any) bool {
	if project.identifier != oldProject["identifier"].(string) {
		panic("Comparison with project with different project identifier does not make sense")
	}
	return project.title != oldProject["title"].(string) ||
		project.process != oldProject["process"].(string) ||
		project.startYear != int(oldProject["startYear"].(int32)) ||
		project.endYear != int(oldProject["endYear"].(int32)) ||
		!equal(project.disciplines, oldProject["disciplines"]) ||
		project.url != oldProject["url"].(string) ||
		project.description != oldProject["description"].(string) ||
		project.parent != oldProject["parent"].(string)
}

// getIdentifier returns the project identifier.
func (project projectBase) getIdentifier() string {
	return project.identifier
}

// projectPerson contains the data of a project the applicant of which is a
// person.  Additionally to the requirements of projectBase, it must contain at
// least one applicant.
type projectPerson struct {
	projectBase
	applicants []string
	partPeople []string
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectPerson) scanDocument(logger tbr_logging.Logger, doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(logger, doc); err != nil {
		return err
	}
	if results := parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(applicantRegexp)).
		Next()); len(results) == 0 {
		whitelist := []string{"5336466", "5382838", "5395416", "5449328", "5449334", "5449348", "29528568",
			"463409137", "495365311"}
		if slices.Contains(whitelist, project.identifier) {
			logger.Debug("Ignoring missing applicant for white-listed project")
		} else {
			return fmt.Errorf("no applicant found")
		}
	} else {
		project.applicants = results
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Debug("Found participating people", "number", len(project.partPeople))
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectPerson) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectBase.updateMongo()
	update["type"] = "personal"
	update["applicants"] = project.applicants
	update["partPeople"] = project.partPeople
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectPerson) isDifferent(oldProject map[string]any) bool {
	return project.projectBase.isDifferent(oldProject) ||
		oldProject["type"].(string) != "personal" ||
		!equal(project.applicants, oldProject["applicants"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
}

// projectInstitution contains the data of a project the applicant of which is
// an institution.  Additionally to the requirements of projectBase, at least
// one item in “institutions” is a requirement.
type projectInstitution struct {
	projectBase
	institutions     []string
	coInstitutions   []string
	partInstitutions []string
	speakers         []string
	coSpeakers       []string
	partPeople       []string
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectInstitution) scanDocument(logger tbr_logging.Logger, doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(logger, doc); err != nil {
		return err
	}
	if results := parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(institutionsRegexp)).Next()); len(results) == 0 {
		return fmt.Errorf("no applying institution found")
	} else {
		project.institutions = results
	}
	project.coInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(coInstitutionsRegexp)).Next())
	if len(project.coInstitutions) != 0 {
		logger.Debug("Found co-applying institutions", "number", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(partInstitutionsRegexp)).Next())
	if len(project.partInstitutions) != 0 {
		logger.Debug("Found participating institutions", "number", len(project.partInstitutions))
	}
	project.speakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(leiterRegexp)).Next())
	if len(project.speakers) == 0 {
		logger.Warn("No speaker found")
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(coSpeakersRegexp)).Next())
	if len(project.coSpeakers) != 0 {
		logger.Debug("Found co-speakers", "number", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Debug("Found participating people", "number", len(project.partPeople))
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectInstitution) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectBase.updateMongo()
	update["type"] = "institutional"
	update["institutions"] = project.institutions
	update["coInstitutions"] = project.coInstitutions
	update["partInstitutions"] = project.partInstitutions
	update["speakers"] = project.speakers
	update["coSpeakers"] = project.coSpeakers
	update["partPeople"] = project.partPeople
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectInstitution) isDifferent(oldProject map[string]any) bool {
	return project.projectBase.isDifferent(oldProject) ||
		oldProject["type"].(string) != "institutional" ||
		!equal(project.institutions, oldProject["institutions"]) ||
		!equal(project.coInstitutions, oldProject["coInstitutions"]) ||
		!equal(project.partInstitutions, oldProject["partInstitutions"]) ||
		!equal(project.speakers, oldProject["speakers"]) ||
		!equal(project.coSpeakers, oldProject["coSpeakers"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
}

// projectUmbrella represents a DFG project with sub projects.  It inherits the
// requirements of projectBase and adds no own.
type projectUmbrella struct {
	projectBase
	institutions     []string
	coInstitutions   []string
	partInstitutions []string
	speakers         []string
	coSpeakers       []string
	partPeople       []string
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectUmbrella) scanDocument(logger tbr_logging.Logger, doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(logger, doc); err != nil {
		return err
	}
	if results := parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(institutionsRegexp)).Next()); len(results) == 0 {
		logger.Debug("No applying institution found")
	} else {
		project.institutions = results
	}
	project.coInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(coInstitutionsRegexp)).Next())
	if len(project.coInstitutions) != 0 {
		logger.Debug("Found co-applying institutions", "number", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(partInstitutionsRegexp)).Next())
	if len(project.partInstitutions) != 0 {
		logger.Debug("Found participating institutions", "number", len(project.partInstitutions))
	}
	project.speakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(leiterRegexp)).Next())
	if len(project.speakers) == 0 {
		logger.Warn("No speaker found")
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(coSpeakersRegexp)).Next())
	if len(project.coSpeakers) != 0 {
		logger.Debug("Found co-speakers", "number", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Debug("Found participating people", "number", len(project.partPeople))
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectUmbrella) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectBase.updateMongo()
	update["type"] = "umbrella"
	update["institutions"] = project.institutions
	update["coInstitutions"] = project.coInstitutions
	update["partInstitutions"] = project.partInstitutions
	update["speakers"] = project.speakers
	update["coSpeakers"] = project.coSpeakers
	update["partPeople"] = project.partPeople
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectUmbrella) isDifferent(oldProject map[string]any) bool {
	return project.projectBase.isDifferent(oldProject) ||
		oldProject["type"].(string) != "umbrella" ||
		!equal(project.institutions, oldProject["institutions"]) ||
		!equal(project.coInstitutions, oldProject["coInstitutions"]) ||
		!equal(project.partInstitutions, oldProject["partInstitutions"]) ||
		!equal(project.speakers, oldProject["speakers"]) ||
		!equal(project.coSpeakers, oldProject["coSpeakers"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
}

// equal returns whether the given slices of strings are equal, i.e. have the
// same length and have identical items at each position.  “b” is expected to
// be a bson.A array (or nil, which is considered an empty array).
func equal(a []string, b any) bool {
	if b == nil {
		return len(a) == 0
	}
	bAsArray := b.(bson.A)
	if len(a) != len(bAsArray) {
		return false
	}
	for i, v := range a {
		if v != bAsArray[i].(string) {
			return false
		}
	}
	return true
}

// parseDisciplines returns all disciplines (“Fachbereiche”) found in doc.
func parseDisciplines(doc *goquery.Document) (disciplines []string) {
	doc.Find(`span.name`).FilterFunction(filterByRegexp(disciplineRegexp)).Next().
		FilterFunction(func(i int, s *goquery.Selection) bool {
			return s.Nodes[0].Type == 3
		}).Each(func(i int, s *goquery.Selection) {
		disciplines = append(disciplines, normalizeString(s.Text()))
	})
	return
}

// parseLinkList returns all items found in the given subset of HTML elements.
// More precisely, the items are the text content in the “<a class="intern"
// href="…">…</a>” elements.
func parseLinkList(s *goquery.Selection) (elements []string) {
	s.Find("a.intern").Each(func(i int, s *goquery.Selection) {
		elements = append(elements, normalizeString(s.Text()))
	})
	return
}

var urlRegexp = regexp.MustCompile(`^/gepris/projekt/(\d+)$`)

// getParent returns the project identifier of the parent project.  The “doc”
// argument contains the parsed HTML page for the project.  If it does not have
// a parent, an empty string is returned.
func getParent(doc *goquery.Document) (string, error) {
	fieldNode := doc.Find(`span.name`).FilterFunction(filterByRegexp(parentRegexp)).Next()
	if fieldNode.Length() == 0 {
		return "", nil
	} else if fieldNode.Length() > 1 {
		return "", fmt.Errorf("ambiguous “Teilprojekt zu” field")
	}
	linkNode := fieldNode.Find("a.intern")
	if linkNode.Length() != 1 {
		return "", fmt.Errorf("not exactly one link in “Teilprojekt zu” field")
	}
	var url string
	if value, exists := linkNode.Attr("href"); !exists {
		return "", fmt.Errorf("no URL to parent project found in “Teilprojekt zu”")
	} else {
		url = value
	}
	if match := urlRegexp.FindStringSubmatch(url); match == nil {
		return "", fmt.Errorf("invalid URL found in “Teilprojekt zu”")
	} else {
		return "G:(GEPRIS)" + match[1], nil
	}
}

// Helper regexps to be used when scanning DOMs in the scanDocument methods.
var leiterRegexp = regexp.MustCompile(`^\s*(Teilprojektleiter|Sprecher|Leiter)(in(nen)?)?` +
	`( / (Teilprojektleiter|Sprecher|Leiter))?\s*$`)
var disciplineRegexp = regexp.MustCompile(`^\s*Fachliche Zuordnung\s*$`)
var processRegexp = regexp.MustCompile(`^\s*DFG-Verfahren\s*$`)
var parentRegexp = regexp.MustCompile(`^\s*Teilprojekt zu\s*$`)
var applicantRegexp = regexp.MustCompile(`^\s*Antragsteller(in(nen)?)?( / Antragsteller)?\s*$`)
var partPeopleRegexp = regexp.MustCompile(`^\s*Beteiligte Person(en)?\s*$`)
var institutionsRegexp = regexp.MustCompile(`^\s*Antragstellende Institution\s*$`)
var coInstitutionsRegexp = regexp.MustCompile(`^\s*Mitantragstellende Institution\s*$`)
var partInstitutionsRegexp = regexp.MustCompile(`^\s*Beteiligte Einrichtung\s*$`)
var coSpeakersRegexp = regexp.MustCompile(`^\s*Co-Sprecher(in(nen)?)?( / Co-Sprecher)?\s*$`)
var fundingRegexp = regexp.MustCompile(`^\s*Förderung\s*$`)

// filterByRegexp returns a function that is ready to be used as an argument to
// goquery.FilterFunction.  Eventually, this reduces the nodes set to those
// matching the given regexp.
func filterByRegexp(re *regexp.Regexp) func(int, *goquery.Selection) bool {
	return func(_ int, s *goquery.Selection) bool {
		return re.MatchString(s.Text())
	}
}
