package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
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
	extendedFields() bson.M
	isDifferent(oldProject map[string]any) bool
	isOutdated(oldProject map[string]any) bool
	getIdentifier() string
	references() (personIDs, institutionIDs []int)
}

// schemaVersion is stored with every project.  Increment it whenever the set
// or the computation of the extended fields (see extendedFields) changes, so
// that all projects get rewritten during the next crawl.
const schemaVersion = 1

// apiProject is the subset of the GEPRIS “project/get” API response needed by
// the crawler.  Additionally, raw holds the complete response body, which is
// used for detecting changes, and hit holds the search hit by which the
// project was found.  “Publikation” is parsed lazily (see fillExtended) so
// that an unexpected format does not prevent storing the project.
type apiProject struct {
	ID           int             `json:"id"`
	Data         map[string]any  `json:"data"`
	Per          apiGroups       `json:"per"`
	Ins          apiGroups       `json:"ins"`
	Teilprojekte apiGroups       `json:"teilprojekte"`
	Publikation  json.RawMessage `json:"publikation"`
	raw          []byte
	hit          apiSearchHit
}

// apiSearchHit is the subset of a hit of the GEPRIS “project/search” API
// response needed by the crawler.  Only the search hits contain the
// structured subject classification and the research sites of a project.
// They are parsed lazily (see fillExtended) so that an unexpected format does
// not prevent storing the project.
type apiSearchHit struct {
	PrjID         int             `json:"prj_id"`
	Fachklass     json.RawMessage `json:"prj_fachklass_json"`
	ResearchSites json.RawMessage `json:"ins_land_json"`
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

// boolFromMap returns the boolean value of the given key of the given map, or
// false if the key is not present or does not hold a boolean.
func boolFromMap(m map[string]any, key string) bool {
	value, _ := m[key].(bool)
	return value
}

// intFromAny converts an integer as decoded by the MongoDB driver to an int.
// It returns 0 for anything that is not a number.
func intFromAny(v any) int {
	switch value := v.(type) {
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

// hashRaw returns the SHA-256 hash of the given raw API response parts as a
// hexadecimal string.  It is used to detect changes in GEPRIS records.
func hashRaw(parts ...[]byte) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// parseYears returns start and end year of the given GEPRIS funding period
// string, or zeros if it cannot be parsed.
func parseYears(timespan string) (start, end int) {
	start, end, _ = parseTimespan(normalizeString(timespan))
	return
}

var hrefRegexp = regexp.MustCompile(`href="([^"]*)"`)

// extractHref returns the link target of the given HTML snippet (GEPRIS
// delivers the project website as an <a> element), or an empty string.
func extractHref(html string) string {
	if matches := hrefRegexp.FindStringSubmatch(html); matches != nil {
		return matches[1]
	}
	return ""
}

// classification is a DFG subject classification of a project on all
// levels of the DFG hierarchy (see lookupTables).  Coordinated programmes are
// often only classified down to the research area, so that the lower levels
// are empty.
type classification struct {
	DisciplineKey   string `bson:"disciplineKey"`
	Discipline      string `bson:"discipline"`
	ResearchAreaKey string `bson:"researchAreaKey"`
	ResearchArea    string `bson:"researchArea"`
	ReviewBoardKey  string `bson:"reviewBoardKey"`
	ReviewBoard     string `bson:"reviewBoard"`
	SubjectAreaKey  string `bson:"subjectAreaKey"`
	SubjectArea     string `bson:"subjectArea"`
}

// researchSite is an institution (e.g. a university) where the research of
// a project takes place.  This is how GEPRIS attributes projects to
// institutions in its search, also for individual grants which have no
// participating institutions.
type researchSite struct {
	InstitutionID int    `bson:"institutionId"`
	Name          string `bson:"name"`
	RegionKey     string `bson:"regionKey"`
	Region        string `bson:"region"`
	InGermany     bool   `bson:"inGermany"`
}

// parseClassifications returns the subject classifications of the given
// “prj_fachklass_json” field of a search hit.
func parseClassifications(logger tbr_logging.Logger, raw json.RawMessage) (result []classification) {
	var entries []map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			logger.Warn("Could not parse subject classification", "error", err)
		}
	}
	for _, entry := range entries {
		c := classification{
			ResearchAreaKey: stringFromMap(entry, "WSG_KEY"),
			ReviewBoardKey:  stringFromMap(entry, "FAS_KEY"),
			SubjectAreaKey:  stringFromMap(entry, "DFA_KEY"),
		}
		if c.ResearchAreaKey != "" {
			c.DisciplineKey = c.ResearchAreaKey[:1]
		}
		c.Discipline = lookups.disciplines[c.DisciplineKey].Name
		c.ResearchArea = lookups.researchAreas[c.ResearchAreaKey].Name
		c.ReviewBoard = lookups.reviewBoards[c.ReviewBoardKey].Name
		c.SubjectArea = lookups.subjectAreas[c.SubjectAreaKey].Name
		result = append(result, c)
	}
	return result
}

// parseResearchSites returns the research sites of the given “ins_land_json”
// field of a search hit.
func parseResearchSites(logger tbr_logging.Logger, raw json.RawMessage) (result []researchSite) {
	var entries []map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &entries); err != nil {
			logger.Warn("Could not parse research sites", "error", err)
		}
	}
	for _, entry := range entries {
		key := fmt.Sprint(entry["ROOT_ID"])
		id, err := strconv.Atoi(key)
		if err != nil {
			logger.Warn("Invalid research site", "id", entry["ROOT_ID"])
			continue
		}
		site := researchSite{
			InstitutionID: id,
			Name:          lookups.researchSites[key].Name,
			RegionKey:     stringFromMap(entry, "EINR_LND_KEY"),
			InGermany:     stringFromMap(entry, "EINR_TYP_KEY") == "BL",
		}
		site.Region = lookups.regions[site.RegionKey].Name
		result = append(result, site)
	}
	return result
}

// personRole is a person participating in a project in a certain role.  A
// person may occur multiple times in a project with different roles.
type personRole struct {
	PersonID      int    `bson:"personId"`
	Name          string `bson:"name"`
	Role          string `bson:"role"`
	RoleLabel     string `bson:"roleLabel"`
	Affiliation   string `bson:"affiliation"`
	AffiliationEn string `bson:"affiliationEn"`
	Deceased      bool   `bson:"deceased"`
}

// institutionRole is an institution participating in a project in a certain
// role.
type institutionRole struct {
	InstitutionID int    `bson:"institutionId"`
	Name          string `bson:"name"`
	NameEn        string `bson:"nameEn"`
	Role          string `bson:"role"`
	RoleLabel     string `bson:"roleLabel"`
	Dissolved     bool   `bson:"dissolved"`
}

// subproject is a sub-project as listed in its umbrella project.  The short
// title (e.g. “A01”) is only available here.
type subproject struct {
	ProjectID  int    `bson:"projectId"`
	ShortTitle string `bson:"shortTitle"`
	Title      string `bson:"title"`
	TitleEn    string `bson:"titleEn"`
	Status     string `bson:"status"`
	StartYear  int    `bson:"startYear"`
	EndYear    int    `bson:"endYear"`
	LeaderIDs  []int  `bson:"leaderIds"`
}

// publication is a publication listed as a result of a project.
type publication struct {
	PublicationID int    `bson:"publicationId"`
	Authors       string `bson:"authors"`
	Title         string `bson:"title"`
	Year          int    `bson:"year"`
	DOI           string `bson:"doi"`
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

	// The following fields are not needed for PMH but make the database
	// useful for analyses.  See extendedFields.
	projectID             int
	parentID              int
	titleEn               string
	descriptionEn         string
	finalReportSummary    string
	finalReportSummaryEn  string
	processKey            string
	processEn             string
	mainProcess           string
	subProcessKey         string
	subProcess            string
	website               string
	internationalRelation string
	isTransferProject     bool
	classifications       []classification
	researchSites         []researchSite
	personRoles           []personRole
	institutionRoles      []institutionRole
	subprojects           []subproject
	publications          []publication
	sourceHash            string
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
	project.fillExtended(logger, data)
	return nil
}

// fillExtended fills the data fields of the project that are returned by
// extendedFields.
func (project *projectBase) fillExtended(logger tbr_logging.Logger, data *apiProject) {
	project.projectID = data.ID
	if rahPrjID := intFromMap(data.Data, "RAH_PRJ_ID"); rahPrjID != data.ID {
		project.parentID = rahPrjID
	}
	project.titleEn = normalizeString(stringFromMap(data.Data, "PRJ_TITEL_EN"))
	project.descriptionEn = normalizeString(stringFromMap(data.Data, "PRJ_ABSTRACT_EN"))
	project.finalReportSummary = normalizeString(stringFromMap(data.Data, "PRJ_ABABSTRACT"))
	project.finalReportSummaryEn = normalizeString(stringFromMap(data.Data, "PRJ_ABABSTRACT_EN"))
	project.processKey = stringFromMap(data.Data, "PRJ_VERFAHREN_KEY")
	project.processEn = normalizeString(stringFromMap(data.Data, "PRJ_VERFAHREN_EN"))
	project.mainProcess = lookups.processes[project.processKey].Name
	project.subProcessKey = lookups.subProcessKey(project.processKey, stringFromMap(data.Data, "PRJ_VERFAHREN"))
	project.subProcess = lookups.subProcesses[project.subProcessKey].Name
	project.website = extractHref(stringFromMap(data.Data, "PRJ_WEBSEITE"))
	project.internationalRelation = normalizeString(stringFromMap(data.Data, "INT_BEZUG"))
	project.isTransferProject = boolFromMap(data.Data, "IS_TRANSFERPROJEKT")
	project.classifications = parseClassifications(logger, data.hit.Fachklass)
	project.researchSites = parseResearchSites(logger, data.hit.ResearchSites)
	for _, key := range data.Per.Order {
		for _, entry := range data.Per.Groups[key] {
			id := entry.ID
			if id == 0 {
				id = intFromMap(entry.Data, "per_id")
			}
			project.personRoles = append(project.personRoles, personRole{
				PersonID:      id,
				Name:          normalizeString(stringFromMap(entry.Data, "bt_partner_name_komplett")),
				Role:          stringFromMap(entry.Data, "BTR_KEY_GRUP"),
				RoleLabel:     stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP"),
				Affiliation:   normalizeString(stringFromMap(entry.Data, "beteiligungen")),
				AffiliationEn: normalizeString(stringFromMap(entry.Data, "beteiligungen_en")),
				Deceased:      boolFromMap(entry.Data, "PER_VERSTORBEN"),
			})
		}
	}
	for _, key := range data.Ins.Order {
		for _, entry := range data.Ins.Groups[key] {
			id := entry.ID
			if id == 0 {
				id = intFromMap(entry.Data, "INS_ID")
			}
			project.institutionRoles = append(project.institutionRoles, institutionRole{
				InstitutionID: id,
				Name:          normalizeString(stringFromMap(entry.Data, "INS_NAME")),
				NameEn:        normalizeString(stringFromMap(entry.Data, "INS_NAMEENGLISCH")),
				Role:          stringFromMap(entry.Data, "BTR_KEY_GRUP"),
				RoleLabel:     stringFromMap(entry.Data, "BTR_BEZEICHNUNG_GRUP"),
				Dissolved:     boolFromMap(entry.Data, "INS_AUFGELOEST"),
			})
		}
	}
	for _, key := range data.Teilprojekte.Order {
		entries := data.Teilprojekte.Groups[key]
		id, err := strconv.Atoi(key)
		if err != nil || len(entries) == 0 {
			logger.Warn("Invalid sub-project", "key", key)
			continue
		}
		// There is one entry per sub-project leader; the project data is
		// the same in all of them.
		first := entries[0].Data
		sub := subproject{
			ProjectID:  id,
			ShortTitle: normalizeString(stringFromMap(first, "TP_PRJ_KURZTITEL")),
			Title:      normalizeString(stringFromMap(first, "TP_PRJ_TITEL")),
			TitleEn:    normalizeString(stringFromMap(first, "TP_PRJ_TITEL_EN")),
			Status:     stringFromMap(first, "PRJ_STATUS"),
		}
		sub.StartYear, sub.EndYear = parseYears(stringFromMap(first, "PRJ_FOERDERZEITRAUM"))
		for _, entry := range entries {
			if leaderID := intFromMap(entry.Data, "PER_ID"); leaderID != 0 {
				sub.LeaderIDs = append(sub.LeaderIDs, leaderID)
			}
		}
		project.subprojects = append(project.subprojects, sub)
	}
	var publications []apiEntry
	if len(data.Publikation) > 0 {
		if err := json.Unmarshal(data.Publikation, &publications); err != nil {
			logger.Warn("Could not parse publications", "error", err)
		}
	}
	for _, entry := range publications {
		id := entry.ID
		if id == 0 {
			id = intFromMap(entry.Data, "PUB_ID")
		}
		project.publications = append(project.publications, publication{
			PublicationID: id,
			Authors:       normalizeString(stringFromMap(entry.Data, "PUB_AUTOR_HRSG")),
			Title:         normalizeString(stringFromMap(entry.Data, "PUB_TITEL")),
			Year:          intFromMap(entry.Data, "PUB_ERSCHEINUNGSJAHR"),
			DOI:           stringFromMap(entry.Data, "PUB_DOI"),
		})
	}
	// Whether the lookup tables were available is part of the hash, so that
	// names missing due to a failed download are added in the next crawl.
	project.sourceHash = hashRaw(data.raw, data.hit.Fachklass, data.hit.ResearchSites,
		[]byte(strconv.FormatBool(lookups.loaded)))
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
	for key, value := range project.extendedFields() {
		update[key] = value
	}
	return filter, update
}

// extendedFields returns the data fields that are not needed for PMH.  They
// can be updated without touching the “lastModification” field.
// “sourceHash” is the hash of the GEPRIS API response, used to detect
// changes.
func (project projectBase) extendedFields() bson.M {
	return bson.M{
		"projectId":             project.projectID,
		"parentId":              project.parentID,
		"titleEn":               project.titleEn,
		"descriptionEn":         project.descriptionEn,
		"finalReportSummary":    project.finalReportSummary,
		"finalReportSummaryEn":  project.finalReportSummaryEn,
		"processKey":            project.processKey,
		"processEn":             project.processEn,
		"mainProcess":           project.mainProcess,
		"subProcessKey":         project.subProcessKey,
		"subProcess":            project.subProcess,
		"website":               project.website,
		"internationalRelation": project.internationalRelation,
		"isTransferProject":     project.isTransferProject,
		"classifications":       project.classifications,
		"researchSites":         project.researchSites,
		"personRoles":           project.personRoles,
		"institutionRoles":      project.institutionRoles,
		"subprojects":           project.subprojects,
		"publications":          project.publications,
		"sourceHash":            project.sourceHash,
		"schemaVersion":         schemaVersion,
	}
}

// isOutdated returns whether the extended fields of “oldProject” need to be
// updated, i.e. whether the GEPRIS record or the schema version has changed.
func (project projectBase) isOutdated(oldProject map[string]any) bool {
	oldSourceHash, _ := oldProject["sourceHash"].(string)
	return project.sourceHash != oldSourceHash || intFromAny(oldProject["schemaVersion"]) != schemaVersion
}

// references returns the GEPRIS IDs of all persons and institutions
// participating in this project, including its research sites.
func (project projectBase) references() (personIDs, institutionIDs []int) {
	for _, role := range project.personRoles {
		if role.PersonID != 0 {
			personIDs = append(personIDs, role.PersonID)
		}
	}
	for _, role := range project.institutionRoles {
		if role.InstitutionID != 0 {
			institutionIDs = append(institutionIDs, role.InstitutionID)
		}
	}
	for _, site := range project.researchSites {
		institutionIDs = append(institutionIDs, site.InstitutionID)
	}
	return
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
