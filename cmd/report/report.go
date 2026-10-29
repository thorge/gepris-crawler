/*
report creates CSV files from the MongoDB filled by the crawler.

Every report is defined by a YAML file in the reports directory, containing a
MongoDB aggregation pipeline and the columns of the CSV file.  See the README
file for further information.
*/
package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/yaml.v3"
)

// parametersFilename is the name of the file in the reports directory that
// holds parameters shared by all reports, e.g. lists of universities.
const parametersFilename = "parameters.yaml"

// column is a column of the CSV output.  Field is the name of the field in the
// documents returned by the pipeline; nested fields can be addressed with
// dots.  If Decimals is set, numbers are rounded to this number of decimal
// places.
type column struct {
	Field    string `yaml:"field"`
	Header   string `yaml:"header"`
	Decimals *int   `yaml:"decimals"`
}

// report is the definition of a report as read from its YAML file.  Pipeline
// is a MongoDB aggregation pipeline in JSON, which may contain placeholders
// for the parameters in Go template syntax, e.g. “{{.year}}”.
type report struct {
	Title       string         `yaml:"title"`
	Description string         `yaml:"description"`
	Collection  string         `yaml:"collection"`
	Parameters  map[string]any `yaml:"parameters"`
	Pipeline    string         `yaml:"pipeline"`
	Columns     []column       `yaml:"columns"`
}

// loadYAML reads the given YAML file into “target”.
func loadYAML(filename string, target any) error {
	content, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(content, target); err != nil {
		return fmt.Errorf("invalid YAML in %v: %v", filename, err)
	}
	return nil
}

// templateFuncs are the functions available in pipeline templates.  “json”
// returns its argument as JSON.  “ids” takes a list of entries with an “id”
// item (as in the shared parameters) and returns the IDs as a JSON array.
var templateFuncs = template.FuncMap{
	"json": func(value any) (string, error) {
		result, err := json.Marshal(value)
		return string(result), err
	},
	"ids": func(entries []any) (string, error) {
		ids := make([]any, 0, len(entries))
		for _, entry := range entries {
			item, ok := entry.(map[string]any)
			if !ok || item["id"] == nil {
				return "", fmt.Errorf("entry without “id”: %v", entry)
			}
			ids = append(ids, item["id"])
		}
		result, err := json.Marshal(ids)
		return string(result), err
	},
}

// buildPipeline fills in the parameters into the pipeline template and
// returns the pipeline ready for MongoDB.
func buildPipeline(pipelineTemplate string, parameters map[string]any) (bson.A, error) {
	tmpl, err := template.New("pipeline").Funcs(templateFuncs).Option("missingkey=error").
		Parse(pipelineTemplate)
	if err != nil {
		return nil, err
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, parameters); err != nil {
		return nil, err
	}
	// Extended JSON must be a document at top level.
	var wrapper struct {
		Pipeline bson.A `bson:"pipeline"`
	}
	if err := bson.UnmarshalExtJSON([]byte(`{"pipeline": `+rendered.String()+`}`), false, &wrapper); err != nil {
		return nil, fmt.Errorf("invalid pipeline JSON: %v\n%v", err, rendered.String())
	}
	return wrapper.Pipeline, nil
}

// getField returns the value of the given field of the given document.  Dots
// in the field name address nested documents.
func getField(document any, field string) any {
	for _, part := range strings.Split(field, ".") {
		switch d := document.(type) {
		case bson.M:
			document = d[part]
		case map[string]any:
			document = d[part]
		case bson.D:
			document = d.Map()[part]
		default:
			return nil
		}
	}
	return document
}

// formatValue returns the given value as a string suitable for a CSV file
// opened with a German Excel, i.e. with a decimal comma.
func formatValue(value any, decimals *int) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "ja"
		}
		return "nein"
	case int32, int64, int:
		return fmt.Sprint(v)
	case float64:
		precision := -1
		if decimals != nil {
			precision = *decimals
		}
		return strings.Replace(strconv.FormatFloat(v, 'f', precision, 64), ".", ",", 1)
	case primitive.DateTime:
		return v.Time().Format("2006-01-02")
	case bson.A:
		items := make([]string, 0, len(v))
		for _, item := range v {
			items = append(items, formatValue(item, decimals))
		}
		return strings.Join(items, ", ")
	default:
		return fmt.Sprint(v)
	}
}

// writeCSV writes the given documents as CSV to “w”.  The format is suitable
// for a German Excel: semicolons as separators, UTF-8 with byte order mark.
func writeCSV(w io.Writer, columns []column, documents []bson.M) error {
	if _, err := w.Write([]byte("\xef\xbb\xbf")); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.Comma = ';'
	writer.UseCRLF = true
	headers := make([]string, len(columns))
	for i, col := range columns {
		headers[i] = col.Header
	}
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, document := range documents {
		row := make([]string, len(columns))
		for i, col := range columns {
			row[i] = formatValue(getField(document, col.Field), col.Decimals)
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// dataState returns a description of how current the data in the database
// is, based on the crawler runs recorded in the “crawls” collection.
func dataState(db *mongodb.Database) string {
	coll := db.Collection("crawls")
	var last, lastFull struct {
		End time.Time `bson:"end"`
	}
	latest := mongoopts.FindOne().SetSort(bson.D{{"end", -1}})
	result := "kein Crawler-Lauf verzeichnet"
	if err := coll.FindOne(context.Background(), bson.M{}, latest).Decode(&last); err == nil {
		result = "letzter Crawler-Lauf am " + last.End.Format("2006-01-02")
	}
	if err := coll.FindOne(context.Background(), bson.M{"full": true}, latest).Decode(&lastFull); err == nil {
		result += ", letzter vollständiger Lauf am " + lastFull.End.Format("2006-01-02")
	} else {
		result += ", bisher kein vollständiger Lauf (ältere Projekte können fehlen)"
	}
	return result
}

// formatParameters returns the given parameters as human-readable lines,
// sorted by name.  Only parameters used in the given pipeline template are
// included.
func formatParameters(parameters map[string]any, pipelineTemplate string) string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		if regexp.MustCompile(`\.` + regexp.QuoteMeta(name) + `\b`).MatchString(pipelineTemplate) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var lines []string
	for _, name := range names {
		value, _ := json.Marshal(parameters[name])
		lines = append(lines, fmt.Sprintf("  %v: %s", name, value))
	}
	return strings.Join(lines, "\n")
}

// runReport executes the given report and writes “<name>.csv” and
// “<name>.txt” (with title, description, parameters, and data state) into
// the output directory.
func runReport(db *mongodb.Database, name string, definition report, parameters map[string]any,
	outputDir string) (int, error) {
	pipeline, err := buildPipeline(definition.Pipeline, parameters)
	if err != nil {
		return 0, err
	}
	collection := definition.Collection
	if collection == "" {
		collection = "projects"
	}
	cursor, err := db.Collection(collection).Aggregate(context.Background(), pipeline)
	if err != nil {
		return 0, err
	}
	var documents []bson.M
	if err := cursor.All(context.Background(), &documents); err != nil {
		return 0, err
	}
	var csvContent bytes.Buffer
	if err := writeCSV(&csvContent, definition.Columns, documents); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, name+".csv"), csvContent.Bytes(), 0666); err != nil {
		return 0, err
	}
	info := fmt.Sprintf("%v\n\n%v\n\nParameter:\n%v\n\nDatenstand: %v\nErstellt: %v\n"+
		"Quelle: GEPRIS (https://gepris.dfg.de), Deutsche Forschungsgemeinschaft\n",
		definition.Title, strings.TrimSpace(definition.Description), formatParameters(parameters, definition.Pipeline),
		dataState(db), time.Now().Format("2006-01-02 15:04"))
	err = os.WriteFile(filepath.Join(outputDir, name+".txt"), []byte(strings.ReplaceAll(info, "\n", "\r\n")), 0666)
	return len(documents), err
}

// reportNames returns the names of all reports in the given directory, i.e.
// the names of the YAML files without extension, except for the shared
// parameters file.
func reportNames(reportsDir string) ([]string, error) {
	filenames, err := filepath.Glob(filepath.Join(reportsDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, filename := range filenames {
		if filepath.Base(filename) != parametersFilename {
			names = append(names, strings.TrimSuffix(filepath.Base(filename), ".yaml"))
		}
	}
	if len(names) == 0 {
		return nil, errors.New("no reports found in " + reportsDir)
	}
	return names, nil
}

// mergeParameters returns the parameters for a report: the shared ones,
// overridden by the report’s own, overridden by those given on the command
// line.
func mergeParameters(layers ...map[string]any) map[string]any {
	result := make(map[string]any)
	for _, layer := range layers {
		for key, value := range layer {
			result[key] = value
		}
	}
	return result
}
