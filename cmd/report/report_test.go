package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestBuildPipeline(t *testing.T) {
	parameters := map[string]any{
		"year":  2025,
		"sites": []any{map[string]any{"id": 10337, "name": "Kiel"}, map[string]any{"id": 10345}},
	}
	pipeline, err := buildPipeline(
		`[{"$match": {"startYear": {{.year}}, "site": {"$in": {{ids .sites}}}}}]`, parameters)
	if err != nil {
		t.Fatal(err)
	}
	match := pipeline[0].(bson.D).Map()["$match"].(bson.D).Map()
	if match["startYear"] != int32(2025) {
		t.Errorf("wrong year: %#v", match["startYear"])
	}
	if sites := match["site"].(bson.D).Map()["$in"].(bson.A); len(sites) != 2 || sites[0] != int32(10337) {
		t.Errorf("wrong sites: %#v", sites)
	}
	if _, err := buildPipeline(`[{"$match": {"x": {{.missing}}}}]`, parameters); err == nil {
		t.Error("missing parameter not detected")
	}
	if _, err := buildPipeline(`[{"$match": {{ids .year}}}]`, parameters); err == nil {
		t.Error("invalid argument of “ids” not detected")
	}
}

func TestFormatValue(t *testing.T) {
	one := 1
	for _, testCase := range []struct {
		value    any
		decimals *int
		expected string
	}{
		{nil, nil, ""},
		{"Kiel", nil, "Kiel"},
		{int32(42), nil, "42"},
		{27.95930, &one, "28,0"},
		{2.5, nil, "2,5"},
		{true, nil, "ja"},
		{bson.A{"Kiel", "Lübeck"}, nil, "Kiel, Lübeck"},
	} {
		if result := formatValue(testCase.value, testCase.decimals); result != testCase.expected {
			t.Errorf("%#v: expected %q, got %q", testCase.value, testCase.expected, result)
		}
	}
}

func TestWriteCSV(t *testing.T) {
	columns := []column{{Field: "site", Header: "Forschungsstelle"}, {Field: "_id.area", Header: "Fachgebiet"},
		{Field: "n", Header: "Anzahl"}}
	documents := []bson.M{{"site": "Universität; Kiel", "_id": bson.M{"area": "Medizin"}, "n": int32(7)}}
	var output bytes.Buffer
	if err := writeCSV(&output, columns, documents); err != nil {
		t.Fatal(err)
	}
	expected := "\xef\xbb\xbfForschungsstelle;Fachgebiet;Anzahl\r\n\"Universität; Kiel\";Medizin;7\r\n"
	if output.String() != expected {
		t.Errorf("wrong CSV:\n%q\nexpected:\n%q", output.String(), expected)
	}
}

func TestFormatParameters(t *testing.T) {
	parameters := map[string]any{"year": 2025, "years": 3, "sites": []any{map[string]any{"id": 1}}}
	result := formatParameters(parameters, `[{"$match": {"y": {{.year}}, "s": {{ids .sites}}}}]`)
	if result != "  sites: [{\"id\":1}]\n  year: 2025" {
		t.Errorf("wrong parameters: %q", result)
	}
}

// TestReportDefinitions checks that all report definitions in the repository
// can be read and their pipelines built with the shared parameters.  It is
// skipped if there is no reports directory.
func TestReportDefinitions(t *testing.T) {
	const reportsDir = "../../reports"
	if _, err := os.Stat(reportsDir); os.IsNotExist(err) {
		t.Skip("no reports directory")
	}
	sharedParameters := map[string]any{}
	if err := loadYAML(filepath.Join(reportsDir, parametersFilename), &sharedParameters); err != nil &&
		!os.IsNotExist(err) {
		t.Fatal(err)
	}
	names, err := reportNames(reportsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		var definition report
		if err := loadYAML(filepath.Join(reportsDir, name+".yaml"), &definition); err != nil {
			t.Errorf("%v: %v", name, err)
			continue
		}
		if definition.Title == "" || len(definition.Columns) == 0 {
			t.Errorf("%v: title or columns missing", name)
		}
		if _, err := buildPipeline(definition.Pipeline,
			mergeParameters(sharedParameters, definition.Parameters)); err != nil {
			t.Errorf("%v: %v", name, err)
		}
	}
}
