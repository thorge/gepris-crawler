package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
)

// lookupEntry is a single entry of one of the GEPRIS classifications, e.g. a
// review board or a research site.  ParentKey is the key of the entry one
// level higher in the hierarchy, if any.
type lookupEntry struct {
	Name      string
	NameEn    string
	ParentKey string
}

// lookupTables holds the GEPRIS classifications (DFG processes, subject
// classification, research sites) with their names and hierarchy.  The keys
// of the maps are the GEPRIS keys.
type lookupTables struct {
	// processes are the main DFG processes, e.g. “SFB”.  This is what the
	// “PRJ_VERFAHREN_KEY” data field of a project contains.
	processes map[string]lookupEntry
	// subProcesses are the variants of the main processes, e.g. “TRR”.
	// ParentKey is the main process.
	subProcesses map[string]lookupEntry
	// disciplines are the four scientific disciplines
	// (“Wissenschaftsbereiche”), e.g. “2” for life sciences.
	disciplines map[string]lookupEntry
	// researchAreas are the “Fachgebiete”, e.g. “22” for medicine.
	researchAreas map[string]lookupEntry
	// reviewBoards are the “Fachkollegien”, e.g. “2.22”.  ParentKey is
	// the research area.
	reviewBoards map[string]lookupEntry
	// subjectAreas are the “Fächer”, e.g. “2.22-14”.  ParentKey is the
	// review board.
	subjectAreas map[string]lookupEntry
	// regions are the German federal states (e.g. “SH”) and the countries
	// (e.g. “AUT”) of research sites.  ParentKey is “BL” for German states
	// and “LND” for countries.
	regions map[string]lookupEntry
	// researchSites are the institutions where research takes place, i.e.
	// universities and other top-level institutions.  The keys are
	// institution IDs; ParentKey is the region.
	researchSites map[string]lookupEntry
	// loaded is false if the tables could not be downloaded.  Then, all
	// maps are empty, and names are missing in the projects.
	loaded bool
}

// disciplines are not part of the GEPRIS classification tables.  Their key
// is the first digit of the research area.
var disciplines = map[string]lookupEntry{
	"1": {Name: "Geistes- und Sozialwissenschaften", NameEn: "Humanities and Social Sciences"},
	"2": {Name: "Lebenswissenschaften", NameEn: "Life Sciences"},
	"3": {Name: "Naturwissenschaften", NameEn: "Natural Sciences"},
	"4": {Name: "Ingenieurwissenschaften", NameEn: "Engineering Sciences"},
}

// lookups is set by the main function before any project is processed and
// read-only afterwards.  If it cannot be loaded, it stays empty, so that the
// crawler can continue to work for PMH.
var lookups = &lookupTables{}

// apiTranslation is an entry of the GEPRIS “general/translations” API
// response.
type apiTranslation struct {
	Key     *string `json:"key"`
	German  *string `json:"german"`
	English *string `json:"english"`
}

// apiHierarchyMeta maps a key of the GEPRIS “general/dropdowns” API response
// to the keys of its ancestors.
type apiHierarchyMeta map[string]struct {
	First  string `json:"first"`
	Second string `json:"second"`
}

// apiHierarchy is a hierarchy of the GEPRIS “general/dropdowns” API
// response.
type apiHierarchy struct {
	SecondMeta apiHierarchyMeta `json:"secondMeta"`
	ThirdMeta  apiHierarchyMeta `json:"thirdMeta"`
}

// buildLookupTable creates a lookup table from the given GEPRIS translations.
// “parent” returns the parent key for a given key, it may be nil.
func buildLookupTable(entries []apiTranslation, parent func(key string) string) map[string]lookupEntry {
	result := make(map[string]lookupEntry, len(entries))
	for _, entry := range entries {
		if entry.Key == nil || *entry.Key == "" {
			continue
		}
		var lookup lookupEntry
		if entry.German != nil {
			lookup.Name = normalizeString(*entry.German)
		}
		if entry.English != nil {
			lookup.NameEn = normalizeString(*entry.English)
		}
		if parent != nil {
			lookup.ParentKey = parent(*entry.Key)
		}
		result[*entry.Key] = lookup
	}
	return result
}

// parseLookups creates the lookup tables from the raw GEPRIS
// “general/translations” and “general/dropdowns” API responses.
func parseLookups(translationsRaw, dropdownsRaw []byte) (*lookupTables, error) {
	var translations map[string]json.RawMessage
	if err := json.Unmarshal(translationsRaw, &translations); err != nil {
		return nil, fmt.Errorf("could not parse translations: %v", err)
	}
	var dropdowns map[string]json.RawMessage
	if err := json.Unmarshal(dropdownsRaw, &dropdowns); err != nil {
		return nil, fmt.Errorf("could not parse dropdowns: %v", err)
	}
	getTranslations := func(name string) ([]apiTranslation, error) {
		var result []apiTranslation
		if err := json.Unmarshal(translations[name], &result); err != nil {
			return nil, fmt.Errorf("could not parse translations “%v”: %v", name, err)
		}
		return result, nil
	}
	getHierarchy := func(name string) (result apiHierarchy, err error) {
		if err = json.Unmarshal(dropdowns[name], &result); err != nil {
			err = fmt.Errorf("could not parse hierarchy “%v”: %v", name, err)
		}
		return
	}
	processHierarchy, err := getHierarchy("dfg_verfahren_hierarchy")
	if err != nil {
		return nil, err
	}
	subjectHierarchy, err := getHierarchy("fach_hierarchy")
	if err != nil {
		return nil, err
	}
	researchHierarchy, err := getHierarchy("research_hierarchy")
	if err != nil {
		return nil, err
	}
	result := lookupTables{disciplines: disciplines, loaded: true}
	for _, table := range []struct {
		target      *map[string]lookupEntry
		translation string
		parent      func(key string) string
	}{
		{&result.processes, "dfg_verfahren_hierarchy_first_level", nil},
		{&result.subProcesses, "dfg_verfahren_hierarchy_second_level",
			func(key string) string { return processHierarchy.SecondMeta[key].First }},
		{&result.researchAreas, "fach_hierarchy_first_level", nil},
		{&result.reviewBoards, "fach_hierarchy_second_level",
			func(key string) string { return subjectHierarchy.SecondMeta[key].First }},
		{&result.subjectAreas, "fach_hierarchy_third_level",
			func(key string) string { return subjectHierarchy.ThirdMeta[key].Second }},
		{&result.regions, "research_hierarchy_second_level",
			func(key string) string { return researchHierarchy.SecondMeta[key].First }},
		{&result.researchSites, "research_hierarchy_third_level",
			func(key string) string { return researchHierarchy.ThirdMeta[key].Second }},
	} {
		entries, err := getTranslations(table.translation)
		if err != nil {
			return nil, err
		}
		*table.target = buildLookupTable(entries, table.parent)
	}
	return &result, nil
}

// loadLookups downloads the GEPRIS classifications and returns them as lookup
// tables.
func loadLookups() (*lookupTables, error) {
	translationsRaw, err := fetchJSONCached("https://gepris.dfg.de/backend/general/translations",
		"lookups/translations.json")
	if err != nil {
		return nil, err
	}
	dropdownsRaw, err := fetchJSONCached("https://gepris.dfg.de/backend/general/dropdowns",
		"lookups/dropdowns.json")
	if err != nil {
		return nil, err
	}
	return parseLookups(translationsRaw, dropdownsRaw)
}

// storeLookups writes the lookup tables into the MongoDB collection
// “lookups”, so that names of keys can be looked up in queries.  Every
// document has the fields “table”, “key”, “name”, “nameEn”, and “parentKey”.
func storeLookups(db *mongodb.Database, tables *lookupTables) error {
	coll := db.Collection("lookups")
	if _, err := coll.Indexes().CreateOne(context.Background(), mongodb.IndexModel{
		Keys:    bson.D{{"table", 1}, {"key", 1}},
		Options: mongoopts.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	var models []mongodb.WriteModel
	for name, table := range map[string]map[string]lookupEntry{
		"processes":     tables.processes,
		"subProcesses":  tables.subProcesses,
		"disciplines":   tables.disciplines,
		"researchAreas": tables.researchAreas,
		"reviewBoards":  tables.reviewBoards,
		"subjectAreas":  tables.subjectAreas,
		"regions":       tables.regions,
		"researchSites": tables.researchSites,
	} {
		for key, entry := range table {
			filter := bson.M{"table": name, "key": key}
			models = append(models, mongodb.NewReplaceOneModel().SetFilter(filter).SetUpsert(true).
				SetReplacement(bson.M{"table": name, "key": key, "name": entry.Name,
					"nameEn": entry.NameEn, "parentKey": entry.ParentKey}))
		}
	}
	_, err := coll.BulkWrite(context.Background(), models, mongoopts.BulkWrite().SetOrdered(false))
	if err == nil {
		slog.Info("stored lookup tables", "entries", len(models))
	}
	return err
}

// subProcessKey returns the key of the sub-process (e.g. “TRR”) of a project
// with the given main process key (e.g. “SFB”) and process name (the
// “PRJ_VERFAHREN” data field, e.g. “Sonderforschungsbereiche Transregios”).
// GEPRIS does not deliver the sub-process key, so it is derived from the
// name, which consists of the names of main process and sub-process.  If the
// name is only the main process name, the main process key is returned if it
// is also a sub-process key (as e.g. for “SFB”), and an empty string
// otherwise.  The same is returned if the name cannot be resolved.
func (tables *lookupTables) subProcessKey(mainKey, name string) string {
	main, ok := tables.processes[mainKey]
	if !ok {
		return ""
	}
	name = normalizeString(name)
	if name == main.Name {
		if sub, ok := tables.subProcesses[mainKey]; ok && sub.ParentKey == mainKey {
			return mainKey
		}
		return ""
	}
	if rest, found := strings.CutPrefix(name, main.Name+" "); found {
		for key, sub := range tables.subProcesses {
			if sub.ParentKey == mainKey && sub.Name == rest {
				return key
			}
		}
	}
	return ""
}
