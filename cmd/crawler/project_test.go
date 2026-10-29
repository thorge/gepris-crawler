package main

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

// testProjectJSON is a shortened, anonymised GEPRIS “project/get” response of
// an umbrella project.
const testProjectJSON = `{
  "id": 100,
  "data": {
    "PRJ_ID": 100, "RAH_PRJ_ID": 50,
    "PRJ_TITEL": "SFB 1 Testprojekt", "PRJ_TITEL_EN": "SFB 1 Test project",
    "PRJ_FOERDERZEITRAUM": "von 2019 bis 2027",
    "FKL_BEZEICHNUNG": "4.51 Bauwesen und Architektur · 4.43 Informatik",
    "FKL_BEZEICHNUNG_EN": "4.43 Computer Science · 4.51 Construction Engineering and  Architecture",
    "PRJ_VERFAHREN_KEY": "SFB", "PRJ_VERFAHREN": "Sonderforschungsbereiche  Transregios",
    "PRJ_ABSTRACT": "Zusammenfassung", "PRJ_ABSTRACT_EN": "Summary",
    "PRJ_ABABSTRACT": "Abschlussbericht",
    "PRJ_WEBSEITE": "<a href=\"https://example.org/\" title=\"Webseite\" target=\"_blank\">Zur Homepage</a>",
    "INT_BEZUG": "Schweiz", "IS_TRANSFERPROJEKT": true
  },
  "per": {
    "order": ["PSK", "PVP"],
    "PSK": [{"id": 1, "data": {"per_id": 1, "BTR_KEY_GRUP": "PSK", "BTR_BEZEICHNUNG_GRUP": "Sprecher/-in",
             "bt_partner_name_komplett": "Professorin Dr. Erika Muster", "PER_VERSTORBEN": false,
             "beteiligungen": "Test U, Institut", "beteiligungen_en": "Test U, Institute"}}],
    "PVP": [{"id": 2, "data": {"per_id": 2, "BTR_KEY_GRUP": "PVP",
             "BTR_BEZEICHNUNG_GRUP": "Projektverantwortliche Personen",
             "bt_partner_name_komplett": "Dr. Max Muster", "PER_VERSTORBEN": true}}]
  },
  "ins": {
    "order": ["IAN"],
    "IAN": [{"id": 10, "data": {"BTR_KEY_GRUP": "IAN", "BTR_BEZEICHNUNG_GRUP": "Antragstellende Institution",
             "INS_NAME": "Test-Universität", "INS_NAMEENGLISCH": "Test University", "INS_ID": 10,
             "INS_AUFGELOEST": false}}]
  },
  "publikation": [{"id": 500, "data": {"PRJ_ID": 100, "PUB_ID": 500, "PUB_AUTOR_HRSG": "E. Muster",
                   "PUB_TITEL": "Ein  Buch", "PUB_ERSCHEINUNGSJAHR": 2021, "PUB_DOI": "10.1234/abc"}}],
  "teilprojekte": {
    "order": ["101", "102"],
    "101": [{"id": 101, "data": {"RAH_PRJ_ID": 100, "TP_PRJ_ID": 101, "TP_PRJ_KURZTITEL": "A01",
             "TP_PRJ_TITEL": "Teilprojekt", "TP_PRJ_TITEL_EN": "Sub-project", "PER_ID": 1,
             "PRJ_FOERDERZEITRAUM": "von 2019 bis 2023", "PRJ_STATUS": "abgeschlossen"}},
            {"id": 101, "data": {"RAH_PRJ_ID": 100, "TP_PRJ_ID": 101, "TP_PRJ_KURZTITEL": "A01",
             "TP_PRJ_TITEL": "Teilprojekt", "TP_PRJ_TITEL_EN": "Sub-project", "PER_ID": 3,
             "PRJ_FOERDERZEITRAUM": "von 2019 bis 2023", "PRJ_STATUS": "abgeschlossen"}}],
    "102": [{"id": 102, "data": {"RAH_PRJ_ID": 100, "TP_PRJ_ID": 102, "TP_PRJ_KURZTITEL": "Z",
             "PRJ_FOERDERZEITRAUM": "seit 2019"}}]
  },
  "rahmen_projekt": null
}`

// testSearchHitJSON is a shortened GEPRIS “project/search” hit belonging to
// testProjectJSON.
const testSearchHitJSON = `{"prj_id": 100,
  "prj_fachklass_json": [{"FAS_KEY": "4.43", "WSG_KEY": "44", "DFA_KEY": "4.43-01"},
                         {"FAS_KEY": null, "WSG_KEY": "45", "DFA_KEY": null}],
  "ins_land_json": [{"ROOT_ID": "10", "EINR_LND_KEY": "SH", "EINR_TYP_KEY": "BL"}]}`

// testTranslationsJSON and testDropdownsJSON are shortened GEPRIS
// “general/translations” and “general/dropdowns” responses.
const testTranslationsJSON = `{
  "dfg_verfahren_hierarchy_first_level": [
    {"key": "SFB", "german": "Sonderforschungsbereiche", "english": "Collaborative Research Centres"},
    {"key": "SBH", "german": "Sachbeihilfen", "english": "Research Grants"},
    {"key": "FOR", "german": "Forschungsgruppen", "english": "Research Units"}],
  "dfg_verfahren_hierarchy_second_level": [
    {"key": null, "german": null, "english": null},
    {"key": "SFB", "german": "Sonderforschungsbereiche", "english": "Collaborative Research Centres"},
    {"key": "TRR", "german": "Transregios", "english": "CRC/Transregios"},
    {"key": "FOR", "german": "Forschungsgruppen", "english": "Research Units"},
    {"key": "KFO", "german": "Klinische Forschungsgruppen", "english": "Clinical Research Units"}],
  "fach_hierarchy_first_level": [
    {"key": "44", "german": "Informatik, System- und Elektrotechnik", "english": "Computer Science"},
    {"key": "45", "german": "Bauwesen und Architektur", "english": "Construction Engineering"}],
  "fach_hierarchy_second_level": [{"key": "4.43", "german": "Informatik", "english": "Computer Science"}],
  "fach_hierarchy_third_level": [
    {"key": "4.43-01", "german": "Theoretische Informatik", "english": "Theoretical Computer Science"}],
  "research_hierarchy_second_level": [{"key": "SH", "german": "Schleswig-Holstein", "english": "Schleswig-Holstein"}],
  "research_hierarchy_third_level": [{"key": "10", "german": "Test-Universität", "english": "Test University"}]
}`
const testDropdownsJSON = `{
  "dfg_verfahren_hierarchy": {"secondMeta": {
    "SFB": {"first": "SFB", "second": "SFB"}, "TRR": {"first": "SFB", "second": "TRR"},
    "FOR": {"first": "FOR", "second": "FOR"}, "KFO": {"first": "FOR", "second": "KFO"}}},
  "fach_hierarchy": {"secondMeta": {"4.43": {"first": "44", "second": "4.43"}},
                     "thirdMeta": {"4.43-01": {"first": "44", "second": "4.43"}}},
  "research_hierarchy": {"secondMeta": {"SH": {"first": "BL", "second": "SH"}},
                         "thirdMeta": {"10": {"first": "BL", "second": "SH"}}}
}`

// useTestLookups sets the global lookup tables to the test data for the
// duration of the test.
func useTestLookups(t *testing.T) {
	t.Helper()
	tables, err := parseLookups([]byte(testTranslationsJSON), []byte(testDropdownsJSON))
	if err != nil {
		t.Fatal(err)
	}
	oldLookups := lookups
	lookups = tables
	t.Cleanup(func() { lookups = oldLookups })
}

func parseTestProject(t *testing.T) *apiProject {
	t.Helper()
	var project apiProject
	if err := json.Unmarshal([]byte(testProjectJSON), &project); err != nil {
		t.Fatal(err)
	}
	project.raw = []byte(testProjectJSON)
	if err := json.Unmarshal([]byte(testSearchHitJSON), &project.hit); err != nil {
		t.Fatal(err)
	}
	return &project
}

func TestFillDataUmbrella(t *testing.T) {
	useTestLookups(t)
	project := &projectUmbrella{projectParties: projectParties{
		projectBase: projectBase{identifier: "G:(GEPRIS)100"}}}
	if err := project.fillData(slog.Default(), parseTestProject(t)); err != nil {
		t.Fatal(err)
	}
	if project.projectID != 100 || project.parentID != 50 || project.parent != "G:(GEPRIS)50" {
		t.Errorf("wrong IDs: %v, %v, %v", project.projectID, project.parentID, project.parent)
	}
	if project.titleEn != "SFB 1 Test project" || project.processKey != "SFB" ||
		project.descriptionEn != "Summary" {
		t.Errorf("wrong data fields: %q, %q, %q", project.titleEn, project.processKey, project.descriptionEn)
	}
	if project.mainProcess != "Sonderforschungsbereiche" || project.subProcessKey != "TRR" ||
		project.subProcess != "Transregios" {
		t.Errorf("wrong process: %q, %q, %q", project.mainProcess, project.subProcessKey, project.subProcess)
	}
	expectedClassifications := []classification{
		{DisciplineKey: "4", Discipline: "Ingenieurwissenschaften",
			ResearchAreaKey: "44", ResearchArea: "Informatik, System- und Elektrotechnik",
			ReviewBoardKey: "4.43", ReviewBoard: "Informatik",
			SubjectAreaKey: "4.43-01", SubjectArea: "Theoretische Informatik"},
		{DisciplineKey: "4", Discipline: "Ingenieurwissenschaften",
			ResearchAreaKey: "45", ResearchArea: "Bauwesen und Architektur"},
	}
	if !reflect.DeepEqual(project.classifications, expectedClassifications) {
		t.Errorf("wrong classifications: %+v", project.classifications)
	}
	expectedSites := []researchSite{{InstitutionID: 10, Name: "Test-Universität", RegionKey: "SH",
		Region: "Schleswig-Holstein", InGermany: true}}
	if !reflect.DeepEqual(project.researchSites, expectedSites) {
		t.Errorf("wrong research sites: %+v", project.researchSites)
	}
	expectedPersons := []personRole{
		{PersonID: 1, Name: "Professorin Dr. Erika Muster", Role: "PSK", RoleLabel: "Sprecher/-in",
			Affiliation: "Test U, Institut", AffiliationEn: "Test U, Institute"},
		{PersonID: 2, Name: "Dr. Max Muster", Role: "PVP", RoleLabel: "Projektverantwortliche Personen",
			Deceased: true},
	}
	if !reflect.DeepEqual(project.personRoles, expectedPersons) {
		t.Errorf("wrong person roles: %+v", project.personRoles)
	}
	expectedInstitutions := []institutionRole{{InstitutionID: 10, Name: "Test-Universität",
		NameEn: "Test University", Role: "IAN", RoleLabel: "Antragstellende Institution"}}
	if !reflect.DeepEqual(project.institutionRoles, expectedInstitutions) {
		t.Errorf("wrong institution roles: %+v", project.institutionRoles)
	}
	expectedSubprojects := []subproject{
		{ProjectID: 101, ShortTitle: "A01", Title: "Teilprojekt", TitleEn: "Sub-project", Status: "abgeschlossen",
			StartYear: 2019, EndYear: 2023, LeaderIDs: []int{1, 3}},
		{ProjectID: 102, ShortTitle: "Z", StartYear: 2019},
	}
	if !reflect.DeepEqual(project.subprojects, expectedSubprojects) {
		t.Errorf("wrong sub-projects: %+v", project.subprojects)
	}
	expectedPublications := []publication{{PublicationID: 500, Authors: "E. Muster", Title: "Ein Buch",
		Year: 2021, DOI: "10.1234/abc"}}
	if !reflect.DeepEqual(project.publications, expectedPublications) {
		t.Errorf("wrong publications: %+v", project.publications)
	}
	if project.finalReportSummary != "Abschlussbericht" || project.website != "https://example.org/" ||
		project.internationalRelation != "Schweiz" || !project.isTransferProject {
		t.Errorf("wrong data fields: %q, %q, %q, %v", project.finalReportSummary, project.website,
			project.internationalRelation, project.isTransferProject)
	}
	if !reflect.DeepEqual(project.speakers, []string{"Professorin Dr. Erika Muster", "Dr. Max Muster"}) {
		t.Errorf("wrong speakers: %v", project.speakers)
	}
	personIDs, institutionIDs := project.references()
	if !reflect.DeepEqual(personIDs, []int{1, 2}) || !reflect.DeepEqual(institutionIDs, []int{10, 10}) {
		t.Errorf("wrong references: %v, %v", personIDs, institutionIDs)
	}
}

func TestStoredDocument(t *testing.T) {
	useTestLookups(t)
	project := &projectUmbrella{projectParties: projectParties{
		projectBase: projectBase{identifier: "G:(GEPRIS)100"}}}
	if err := project.fillData(slog.Default(), parseTestProject(t)); err != nil {
		t.Fatal(err)
	}
	_, update := project.updateMongo()
	// Round trip through BSON, as it happens when writing to and reading from
	// MongoDB.
	encoded, err := bson.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := bson.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored["raw"]; ok {
		t.Error("raw API response must not be stored")
	}
	if intFromAny(stored["projectId"]) != 100 {
		t.Errorf("wrong project ID: %v", stored["projectId"])
	}
	personRoles := stored["personRoles"].(bson.A)
	if intFromAny(personRoles[0].(map[string]any)["personId"]) != 1 {
		t.Errorf("wrong person ID: %v", personRoles[0])
	}
	if project.isDifferent(stored) {
		t.Error("project is different from itself")
	}
	if project.isOutdated(stored) {
		t.Error("project is outdated compared to itself")
	}
	stored["sourceHash"] = "something else"
	if !project.isOutdated(stored) {
		t.Error("changed source hash not detected")
	}
	delete(stored, "schemaVersion")
	stored["sourceHash"] = project.sourceHash
	if !project.isOutdated(stored) {
		t.Error("missing schema version not detected")
	}
}

func TestSearchHitChangesSourceHash(t *testing.T) {
	useTestLookups(t)
	data := parseTestProject(t)
	project := &projectUmbrella{projectParties: projectParties{
		projectBase: projectBase{identifier: "G:(GEPRIS)100"}}}
	if err := project.fillData(slog.Default(), data); err != nil {
		t.Fatal(err)
	}
	data.hit.ResearchSites = json.RawMessage(`[]`)
	changedProject := &projectUmbrella{projectParties: projectParties{
		projectBase: projectBase{identifier: "G:(GEPRIS)100"}}}
	if err := changedProject.fillData(slog.Default(), data); err != nil {
		t.Fatal(err)
	}
	if project.sourceHash == changedProject.sourceHash {
		t.Error("changed search hit does not change the source hash")
	}
}

func TestSubProcessKey(t *testing.T) {
	useTestLookups(t)
	for _, testCase := range []struct{ mainKey, name, expected string }{
		{"SFB", "Sonderforschungsbereiche Transregios", "TRR"},
		{"SFB", "Sonderforschungsbereiche", "SFB"},
		{"FOR", "Forschungsgruppen Klinische Forschungsgruppen", "KFO"},
		{"FOR", "Forschungsgruppen Unbekannte Variante", ""},
		{"SBH", "Sachbeihilfen", ""},
		{"XYZ", "Unbekanntes Verfahren", ""},
	} {
		if key := lookups.subProcessKey(testCase.mainKey, testCase.name); key != testCase.expected {
			t.Errorf("%v / %q: expected %q, got %q", testCase.mainKey, testCase.name, testCase.expected, key)
		}
	}
}

func TestParseLookups(t *testing.T) {
	useTestLookups(t)
	if entry := lookups.subProcesses["TRR"]; entry.ParentKey != "SFB" || entry.NameEn != "CRC/Transregios" {
		t.Errorf("wrong sub-process: %+v", entry)
	}
	if entry := lookups.subjectAreas["4.43-01"]; entry.ParentKey != "4.43" {
		t.Errorf("wrong subject area: %+v", entry)
	}
	if entry := lookups.researchSites["10"]; entry.ParentKey != "SH" || entry.Name != "Test-Universität" {
		t.Errorf("wrong research site: %+v", entry)
	}
	if _, ok := lookups.subProcesses[""]; ok {
		t.Error("empty keys must be skipped")
	}
}

func TestEntityFields(t *testing.T) {
	const personJSON = `{"id": 1, "data": {"PER_ID": 1, "PER_NAME_KOMPLETT": "Dr. Erika Muster",
      "PER_VERSTORBEN": false, "ROOT_ID": 10, "ROOT_NAMEDEUTSCH": "Test-Universität", "INS_ID": 11,
      "INS_NAMEDEUTSCH": "Institut für Tests", "ANS_LAND": "Deutschland"},
    "prj": {"order": ["PAN", "PSK"],
      "PAN": [{"id": 200, "data": {"PRJ_ID": 200, "BTR_KEY_GRUP": "PAN", "BTR_BEZEICHNUNG": "Antragsteller",
               "PRJ_VERFAHREN_KEY": "SBH", "PRJ_FOERDERZEITRAUM": "seit 2022", "PRJ_STATUS": "laufend"}},
              {"id": 201, "data": {"PRJ_ID": 201, "BTR_KEY_GRUP": "PAN", "PRJ_VERFAHREN_KEY": "SBH",
               "PRJ_FOERDERZEITRAUM": "von 2010 bis 2013"}}],
      "PSK": [{"id": 100, "data": {"PRJ_ID": 100, "BTR_KEY_GRUP": "PSK", "PRJ_VERFAHREN_KEY": "SFB"}}]},
    "preise": {"order": ["1"], "1": [{"id": 7, "data": {"PREIS_KEY": "GWL",
      "PREIS_BEZEICHNUNG": "Gottfried Wilhelm Leibniz-Programm", "JAHR": "2004"}}]}}`
	var entity apiEntity
	if err := json.Unmarshal([]byte(personJSON), &entity); err != nil {
		t.Fatal(err)
	}
	fields := personKind.fields(&entity)
	if fields["name"] != "Dr. Erika Muster" || fields["rootInstitutionId"] != 10 ||
		fields["institutionId"] != 11 {
		t.Errorf("wrong person fields: %v", fields)
	}
	expected := []projectRef{
		{ProjectID: 200, Role: "PAN", RoleLabel: "Antragsteller", ProcessKey: "SBH", Status: "laufend",
			StartYear: 2022},
		{ProjectID: 201, Role: "PAN", ProcessKey: "SBH", StartYear: 2010, EndYear: 2013},
		{ProjectID: 100, Role: "PSK", ProcessKey: "SFB"},
	}
	if !reflect.DeepEqual(fields["projects"], expected) {
		t.Errorf("wrong project references: %+v", fields["projects"])
	}
	expectedPrizes := []prize{{PrizeKey: "GWL", Name: "Gottfried Wilhelm Leibniz-Programm", Year: 2004}}
	if !reflect.DeepEqual(fields["prizes"], expectedPrizes) {
		t.Errorf("wrong prizes: %+v", fields["prizes"])
	}
}
