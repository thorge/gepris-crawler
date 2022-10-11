package main

import (
	"fmt"
	"regexp"

	"github.com/PuerkitoBio/goquery"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

// projectBase contains the data included in every DFG project.  This is an
// abstract type that is only embedded into others.
type projectBase struct {
	number             string
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

// projectPerson contains the data of a project the applicant of which is a
// person.
type projectPerson struct {
	projectBase
	applicants []string
	partPeople []string
}

// projectInstitution contains the data of a project the applicant of which is
// an institution.
type projectInstitution struct {
	projectBase
	institutions     []string
	coInstitutions   []string
	partInstitutions []string
	speakers         []string
	coSpeakers       []string
	partPeople       []string
}

type projectUmbrella struct {
	projectBase
	institutions     []string
	coInstitutions   []string
	partInstitutions []string
	speakers         []string
	coSpeakers       []string
	partPeople       []string
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectBase) updateMongo() (bson.M, bson.M) {
	filter := bson.M{"number": project.number}
	update := bson.M{
		"number":      project.number,
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

func (project projectBase) getNumber() string {
	return project.number
}

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

func (project projectBase) isDifferent(oldProject map[string]any) bool {
	if project.number != oldProject["number"].(string) {
		logger.Panic("comparison with project with different project number does not make sense")
	}
	return project.title != oldProject["title"].(string) ||
		project.shortTitle != oldProject["shortTitle"].(string) ||
		project.process != oldProject["process"].(string) ||
		project.startYear != int(oldProject["startYear"].(int32)) ||
		project.endYear != int(oldProject["endYear"].(int32)) ||
		!equal(project.disciplines, oldProject["disciplines"]) ||
		project.url != oldProject["url"].(string) ||
		project.description != oldProject["description"].(string) ||
		project.parent != oldProject["parent"].(string)
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

func (project projectPerson) isDifferent(oldProject map[string]any) bool {
	return project.projectBase.isDifferent(oldProject) ||
		oldProject["type"].(string) != "personal" ||
		!equal(project.applicants, oldProject["applicants"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
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

// Types of the projecter interface are able to take data from a project HTML
// page and fill their internal data structure with it.  Then, they
type projecter interface {
	scanDocument(doc *goquery.Document) error
	updateMongo() (bson.M, bson.M)
	getNumber() string
	isDifferent(oldProject map[string]any) bool
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
		return match[1], nil
	}
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectBase) scanDocument(doc *goquery.Document) error {
	var err error
	if project.title, err = getField(doc.Find("div.details > h1")); err != nil {
		whitelist := []string{"61027617", "142091898", "221781256"}
		if slices.Contains(whitelist, project.number) {
			project.title = fmt.Sprintf("DFG project %v (no proper title known)", project.number)
		} else {
			return err
		}
	}
	if project.title == "" {
		return fmt.Errorf("project has no title")
	}
	project.shortTitle = fmt.Sprintf("DFG-Projekt %v", project.number)
	project.process, err = getField(doc.Find(`span.name`).FilterFunction(filterByRegexp(processRegexp)).Next())
	check(err)
	if project.process == "" {
		return fmt.Errorf("project has no DFG process")
	}
	if parent, err := getParent(doc); err != nil {
		return err
	} else {
		if parent != "" {
			logger.Println("DEBUG: found parent project")
			project.parent = parent
		}
	}
	project.disciplines = parseDisciplines(doc)
	if len(project.disciplines) == 0 {
		logger.Println("WARNING: project has no discipline")
	}
	project.startYear, project.endYear, err = parseTimespan(doc)
	check(err)
	project.description, err = getField(doc.Find("div#projekttext"))
	check(err)
	if project.description == "" {
		return fmt.Errorf("project has no description (not even empty)")
	} else if project.description == "Keine Zusammenfassung vorhanden" {
		project.description = ""
	}
	return nil
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectPerson) scanDocument(doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(doc); err != nil {
		return err
	}
	if results := parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(applicantRegexp)).
		Next()); len(results) == 0 {
		whitelist := []string{"5336466", "5382838", "5395416", "5449328", "5449334", "5449348", "29528568",
			"463409137", "495365311"}
		if slices.Contains(whitelist, project.number) {
			logger.Printf("DEBUG: ignoring missing applicant for white-listed project")
		} else {
			return fmt.Errorf("no applicant found")
		}
	} else {
		project.applicants = results
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}

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

func filterByRegexp(re *regexp.Regexp) func(int, *goquery.Selection) bool {
	return func(_ int, s *goquery.Selection) bool {
		return re.MatchString(s.Text())
	}
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectInstitution) scanDocument(doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(doc); err != nil {
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
		logger.Printf("DEBUG: found %v co-applying institutions", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(partInstitutionsRegexp)).Next())
	if len(project.partInstitutions) != 0 {
		logger.Printf("DEBUG: found %v participating institutions", len(project.partInstitutions))
	}
	project.speakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(leiterRegexp)).Next())
	if len(project.speakers) == 0 {
		logger.Printf("WARNING: no speaker found")
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(coSpeakersRegexp)).Next())
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectUmbrella) scanDocument(doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(doc); err != nil {
		return err
	}
	if results := parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(institutionsRegexp)).Next()); len(results) == 0 {
		logger.Println("DEBUG: no applying institution found")
	} else {
		project.institutions = results
	}
	project.coInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(coInstitutionsRegexp)).Next())
	if len(project.coInstitutions) != 0 {
		logger.Printf("DEBUG: found %v co-applying institutions", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(
		doc.Find(`span.name`).FilterFunction(filterByRegexp(partInstitutionsRegexp)).Next())
	if len(project.partInstitutions) != 0 {
		logger.Printf("DEBUG: found %v participating institutions", len(project.partInstitutions))
	}
	project.speakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(leiterRegexp)).Next())
	if len(project.speakers) == 0 {
		logger.Printf("WARNING: no speaker found")
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(coSpeakersRegexp)).Next())
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(partPeopleRegexp)).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}
