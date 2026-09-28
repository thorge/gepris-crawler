package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tbr_logging "gitlab.com/bronger/tools/logging"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

// Types of the projecter interface are able to scan data from a project's
// GEPRIS JSON record and fill their internal data structure with it.  Then,
// they can prepare the data structures needed to write it to the MongoDB
// database.  This abstraction is useful because GEPRIS projects fall into some
// categories with different datafield requirements.
type projecter interface {
	fillData(logger tbr_logging.Logger, project *apiProject) error
	updateMongo() (bson.M, bson.M)
	isDifferent(oldProject map[string]any) bool
	getIdentifier() string
}

// apiProject is the subset of the GEPRIS “project/get” API response needed by
// the crawler.
type apiProject struct {
	ID           int            `json:"id"`
	Data         map[string]any `json:"data"`
	Per          apiGroups      `json:"per"`
	Ins          apiGroups      `json:"ins"`
	Teilprojekte apiGroups      `json:"teilprojekte"`
}

// apiGroups is a GEPRIS API field like “per”, “ins”, or “teilprojekte”: an
// object holding the sequence of the group keys in the “order” item and one
// item per group holding the entries of that group.
type apiGroups struct {
	Order  []string
	Groups map[string][]apiEntry
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (groups *apiGroups) UnmarshalJSON(raw []byte) error {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return err
	}
	groups.Groups = make(map[string][]apiEntry, len(outer))
	for key, value := range outer {
		if key == "order" {
			if err := json.Unmarshal(value, &groups.Order); err != nil {
				return err
			}
			continue
		}
		var entries []apiEntry
		if err := json.Unmarshal(value, &entries); err != nil {
			return err
		}
		groups.Groups[key] = entries
	}
	return nil
}

// apiEntry is a single entry (person, institution, or sub-project) of an
// apiGroups field.
type apiEntry struct {
	ID           int            `json:"id"`
	Data         map[string]any `json:"data"`
	SortCriteria string         `json:"sortCriteria"`
}

// stringFromMap returns the string value of the given key of the given map, or
// an empty string if the key is not present or does not hold a string.
func stringFromMap(m map[string]any, key string) string {
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

// intFromMap returns the integer value of the given key of the given map, or 0
// if the key is not present or does not hold a number.
func intFromMap(m map[string]any, key string) int {
	switch value := m[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

// hasPersonGroup returns whether the given apiGroups field contains at least
// one entry with the given group label.
func hasPersonGroup(groups apiGroups, label string) bool {
	for _, entries := range groups.Groups {
		for _, entry := range entries {
			if stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP") == label {
				return true
			}
		}
	}
	return false
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

// fillData fills the internal data fields of the project with the data found
// in the given GEPRIS JSON record.
func (project *projectBase) fillData(logger tbr_logging.Logger, data *apiProject) error {
	project.title = normalizeString(stringFromMap(data.Data, "PRJ_TITEL"))
	if project.title == "" {
		whitelist := []string{"G:(GEPRIS)61027617", "G:(GEPRIS)142091898", "G:(GEPRIS)221781256"}
		if slices.Contains(whitelist, project.identifier) {
			project.title = fmt.Sprintf("DFG project %v (no proper title known)", project.identifier)
		} else {
			return fmt.Errorf("project has no title")
		}
	}
	project.process = normalizeString(stringFromMap(data.Data, "PRJ_VERFAHREN"))
	if project.process == "" {
		return fmt.Errorf("project has no DFG process")
	}
	if rahPrjID := intFromMap(data.Data, "RAH_PRJ_ID"); rahPrjID != 0 && rahPrjID != data.ID {
		logger.Debug("Found parent project")
		project.parent = "G:(GEPRIS)" + strconv.Itoa(rahPrjID)
	}
	if fkl := normalizeString(stringFromMap(data.Data, "FKL_BEZEICHNUNG")); fkl == "" {
		logger.Warn("Project has no discipline")
	} else {
		for _, discipline := range strings.Split(fkl, " · ") {
			project.disciplines = append(project.disciplines, normalizeString(discipline))
		}
	}
	var err error
	project.startYear, project.endYear, err = parseTimespan(
		normalizeString(stringFromMap(data.Data, "PRJ_FOERDERZEITRAUM")))
	if err != nil {
		return err
	}
	project.description = normalizeString(stringFromMap(data.Data, "PRJ_ABSTRACT"))
	if project.description == "Keine Zusammenfassung vorhanden" {
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
// string → any because this is the lowest common denominator for all project
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

// fillData fills the internal data fields of the project with the data found
// in the given GEPRIS JSON record.
func (project *projectPerson) fillData(logger tbr_logging.Logger, data *apiProject) error {
	if err := project.projectBase.fillData(logger, data); err != nil {
		return err
	}
	for _, key := range data.Per.Order {
		for _, entry := range data.Per.Groups[key] {
			label := stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP")
			name := normalizeString(stringFromMap(entry.Data, "bt_partner_name_komplett"))
			if name == "" {
				logger.Warn("Person entry has no name", "group", key, "label", label, "entry", entry.ID)
				continue
			}
			switch label {
			case "Antragsteller/-in":
				project.applicants = append(project.applicants, name)
			case "Weitere antragsbeteiligte Personen":
				project.partPeople = append(project.partPeople, name)
			default:
				logger.Warn("Unknown person group", "group", key, "label", label)
			}
		}
	}
	if len(project.applicants) == 0 {
		whitelist := []string{"G:(GEPRIS)5336466", "G:(GEPRIS)5382838", "G:(GEPRIS)5395416",
			"G:(GEPRIS)5449328", "G:(GEPRIS)5449334", "G:(GEPRIS)5449348", "G:(GEPRIS)29528568",
			"G:(GEPRIS)463409137", "G:(GEPRIS)495365311"}
		if slices.Contains(whitelist, project.identifier) {
			logger.Debug("Ignoring missing applicant for white-listed project")
		} else {
			return fmt.Errorf("no applicant found")
		}
	}
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
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectPerson) isDifferent(oldProject map[string]any) bool {
	return project.projectBase.isDifferent(oldProject) ||
		oldProject["type"].(string) != "personal" ||
		!equal(project.applicants, oldProject["applicants"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
}

// projectParties contains the data of a project the applicant of which is an
// institution.  This is an abstract type that is only embedded into others
// (projectInstitution and projectUmbrella).  It adds the person and
// institution data fields to the ones of projectBase.
type projectParties struct {
	projectBase
	institutions     []string
	coInstitutions   []string
	partInstitutions []string
	speakers         []string
	coSpeakers       []string
	partPeople       []string
}

// fillParties fills the person and institution data fields from the given
// GEPRIS JSON record.
func (project *projectParties) fillParties(logger tbr_logging.Logger, data *apiProject) {
	for _, key := range data.Ins.Order {
		for _, entry := range data.Ins.Groups[key] {
			label := stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP")
			name := normalizeString(stringFromMap(entry.Data, "INS_NAME"))
			if name == "" {
				logger.Warn("Institution entry has no name", "group", key, "label", label, "entry", entry.ID)
				continue
			}
			switch label {
			case "Antragstellende Institution":
				project.institutions = append(project.institutions, name)
			case "Mitantragstellende Institution":
				project.coInstitutions = append(project.coInstitutions, name)
			case "Weitere antragsbeteiligte Institutionen":
				project.partInstitutions = append(project.partInstitutions, name)
			default:
				logger.Warn("Unknown institution group", "group", key, "label", label)
			}
		}
	}
	for _, key := range data.Per.Order {
		for _, entry := range data.Per.Groups[key] {
			label := stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP")
			name := normalizeString(stringFromMap(entry.Data, "bt_partner_name_komplett"))
			if name == "" {
				logger.Warn("Person entry has no name", "group", key, "label", label, "entry", entry.ID)
				continue
			}
			switch label {
			case "Sprecher/-in", "Teilprojektleiter/-in", "Projektverantwortliche Personen":
				project.speakers = append(project.speakers, name)
			case "Co-Sprecher/-in":
				project.coSpeakers = append(project.coSpeakers, name)
			case "Weitere antragsbeteiligte Personen":
				project.partPeople = append(project.partPeople, name)
			default:
				logger.Warn("Unknown person group", "group", key, "label", label)
			}
		}
	}
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectParties) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectBase.updateMongo()
	update["institutions"] = project.institutions
	update["coInstitutions"] = project.coInstitutions
	update["partInstitutions"] = project.partInstitutions
	update["speakers"] = project.speakers
	update["coSpeakers"] = project.coSpeakers
	update["partPeople"] = project.partPeople
	return filter, update
}

// isDifferentParties returns whether the person and institution data of the
// given project is different from the one this method is called on.
func (project projectParties) isDifferentParties(oldProject map[string]any) bool {
	return !equal(project.institutions, oldProject["institutions"]) ||
		!equal(project.coInstitutions, oldProject["coInstitutions"]) ||
		!equal(project.partInstitutions, oldProject["partInstitutions"]) ||
		!equal(project.speakers, oldProject["speakers"]) ||
		!equal(project.coSpeakers, oldProject["coSpeakers"]) ||
		!equal(project.partPeople, oldProject["partPeople"])
}

// projectInstitution contains the data of a project the applicant of which is
// an institution.
type projectInstitution struct {
	projectParties
}

// fillData fills the internal data fields of the project with the data found
// in the given GEPRIS JSON record.
func (project *projectInstitution) fillData(logger tbr_logging.Logger, data *apiProject) error {
	if err := project.projectBase.fillData(logger, data); err != nil {
		return err
	}
	project.fillParties(logger, data)
	if len(project.institutions) == 0 {
		logger.Warn("No applying institution found")
	}
	if len(project.coInstitutions) != 0 {
		logger.Debug("Found co-applying institutions", "number", len(project.coInstitutions))
	}
	if len(project.partInstitutions) != 0 {
		logger.Debug("Found participating institutions", "number", len(project.partInstitutions))
	}
	if len(project.speakers) == 0 {
		logger.Warn("No speaker found")
	}
	if len(project.coSpeakers) != 0 {
		logger.Debug("Found co-speakers", "number", len(project.coSpeakers))
	}
	if len(project.partPeople) != 0 {
		logger.Debug("Found participating people", "number", len(project.partPeople))
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectInstitution) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectParties.updateMongo()
	update["type"] = "institutional"
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectInstitution) isDifferent(oldProject map[string]any) bool {
	return project.projectParties.isDifferent(oldProject) ||
		oldProject["type"].(string) != "institutional" ||
		project.projectParties.isDifferentParties(oldProject)
}

// projectUmbrella represents a DFG project with sub projects.
type projectUmbrella struct {
	projectParties
}

// fillData fills the internal data fields of the project with the data found
// in the given GEPRIS JSON record.
func (project *projectUmbrella) fillData(logger tbr_logging.Logger, data *apiProject) error {
	if err := project.projectBase.fillData(logger, data); err != nil {
		return err
	}
	project.fillParties(logger, data)
	if len(project.institutions) == 0 {
		logger.Debug("No applying institution found")
	}
	if len(project.coInstitutions) != 0 {
		logger.Debug("Found co-applying institutions", "number", len(project.coInstitutions))
	}
	if len(project.partInstitutions) != 0 {
		logger.Debug("Found participating institutions", "number", len(project.partInstitutions))
	}
	if len(project.speakers) == 0 {
		logger.Warn("No speaker found")
	}
	if len(project.coSpeakers) != 0 {
		logger.Debug("Found co-speakers", "number", len(project.coSpeakers))
	}
	if len(project.partPeople) != 0 {
		logger.Debug("Found participating people", "number", len(project.partPeople))
	}
	return nil
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectUmbrella) updateMongo() (bson.M, bson.M) {
	filter, update := project.projectParties.updateMongo()
	update["type"] = "umbrella"
	return filter, update
}

// isDifferent returns whether the given project “oldProject” is different from
// the one this method is called on.  “oldProject” is usually taken from the
// MongoDB database, and we want to know whether it needs to be updated (with a
// touched lastModification field).  Note that “oldProject” is a mapping
// string → any because this is the lowest common denominator for all project
// types (person, institution, and umbrella).
func (project projectUmbrella) isDifferent(oldProject map[string]any) bool {
	return project.projectParties.isDifferent(oldProject) ||
		oldProject["type"].(string) != "umbrella" ||
		project.projectParties.isDifferentParties(oldProject)
}

// equal returns whether the given slices of strings are equal, i.e. have the
// same length and have identical items at each position.  “b” is expected to
// be a bson.A array (or nil, which is considered an empty array).
func equal(a []string, b any) bool {
	if b == nil {
		return len(a) == 0
	}
	bAsArray, ok := b.(bson.A)
	if !ok {
		return false
	}
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
