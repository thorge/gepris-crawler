package main

import (
	"fmt"
	"regexp"

	"github.com/PuerkitoBio/goquery"
	"go.mongodb.org/mongo-driver/bson"
)

// projectBase contains the data included in every DFG project.  This is an
// abstract type that is only embedded into others.
type projectBase struct {
	number             string
	title              string
	process            string
	startYear, endYear int
	disciplines        []string
	url                string
	description        string
}

// projectPerson contains the data of a project the applicant of which is a
// person.
type projectPerson struct {
	projectBase
	applicant  []string
	partPeople []string
}

// projectInstitution contains the data of a project the applicant of which is
// an institution.
type projectInstitution struct {
	projectBase
	institution      string
	coInstitutions   []string
	partInstitutions []string
	speaker          string
	coSpeakers       []string
	partPeople       []string
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectBase) updateMongo() (bson.D, bson.D) {
	filter := bson.D{{"number", project.number}}
	update := bson.D{{"$set", bson.M{
		"number":      project.number,
		"title":       project.title,
		"process":     project.process,
		"startYear":   project.startYear,
		"endYear":     project.endYear,
		"disciplines": project.disciplines,
		"url":         project.url,
		"description": project.description,
	}}}
	return filter, update
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectPerson) updateMongo() (bson.D, bson.D) {
	filter, update := project.projectBase.updateMongo()
	document := update[0].Value.(bson.M)
	document["type"] = "personal"
	document["applicant"] = project.applicant
	document["partPeople"] = project.partPeople
	return filter, update
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectInstitution) updateMongo() (bson.D, bson.D) {
	filter, update := project.projectBase.updateMongo()
	document := update[0].Value.(bson.M)
	document["type"] = "institutional"
	document["institution"] = project.institution
	document["coInstitutions"] = project.coInstitutions
	document["partInstitutions"] = project.partInstitutions
	document["speaker"] = project.speaker
	document["coSpeakers"] = project.coSpeakers
	document["partPeople"] = project.partPeople
	return filter, update
}

// Types of the projecter interface are able to take data from a project HTML
// page and fill their internal data structure with it.  Then, they
type projecter interface {
	scanDocument(doc *goquery.Document) error
	updateMongo() (bson.D, bson.D)
}

// parseDisciplines returns all disciplines (“Fachbereiche”) found in doc.
func parseDisciplines(doc *goquery.Document) (disciplines []string) {
	doc.Find(`span.name:contains("Fachliche Zuordnung")`).Next().FilterFunction(func(i int, s *goquery.Selection) bool {
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

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectBase) scanDocument(doc *goquery.Document) error {
	var err error
	if project.title, err = getField(doc.Find("div.details > h1")); err != nil {
		return err
	}
	if project.title == "" {
		return fmt.Errorf("project has no title")
	}
	project.process, err = getField(doc.Find(`span.name:contains("DFG-Verfahren")`).Next())
	check(err)
	if project.process == "" {
		return fmt.Errorf("project has no DFG process")
	}
	project.disciplines = parseDisciplines(doc)
	if len(project.disciplines) == 0 {
		return fmt.Errorf("project has no discipline")
	}
	project.startYear, project.endYear, err = parseTimespan(doc)
	check(err)
	project.description, err = getField(doc.Find("div#projekttext"))
	check(err)
	if project.description == "" {
		return fmt.Errorf("WARNING: project has no description (not even empty)")
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
	if results := parseLinkList(doc.Find(`span.name:contains("Antragsteller"), span.name:contains("Antragstellerin")`).
		Next()); len(results) == 0 {
		return fmt.Errorf("no applicant found")
	} else {
		project.applicant = results
	}
	project.partPeople = parseLinkList(doc.Find(`span.name:contains("Beteiligte Personen")`).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}

var leiterRegexp = regexp.MustCompile(`^\s*(?:Teilprojektleiter|Sprecher|Leiter)(?:in)?\s*$`)

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
	if results := parseLinkList(doc.Find(`span.name:contains("Antragstellende Institution")`).Next()); len(results) != 1 {
		return fmt.Errorf("number of applying institutions was not 1 but %v", len(results))
	} else {
		project.institution = results[0]
	}
	project.coInstitutions = parseLinkList(doc.Find(`span.name:contains("Mitantragstellende Institution")`).Next())
	if len(project.coInstitutions) != 0 {
		logger.Printf("DEBUG: found %v co-applying institutions", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(doc.Find(`span.name:contains("Beteiligte Einrichtung")`).Next())
	if len(project.partInstitutions) != 0 {
		logger.Printf("DEBUG: found %v participating institutions", len(project.partInstitutions))
	}
	if results := parseLinkList(doc.Find(`span.name`).FilterFunction(filterByRegexp(leiterRegexp)).Next()); len(results) != 1 {
		return fmt.Errorf("number of speakers was not 1 but %v", len(results))
	} else {
		project.speaker = results[0]
	}
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name:contains("Co-Sprecherinnen / Co-Sprecher")`).Next())
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name:contains("Beteiligte Personen")`).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}
