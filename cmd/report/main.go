package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tbr_errors "gitlab.com/bronger/tools/errors"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/yaml.v3"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/options"
)

// parameterFlags collects the “-param name=value” command line options.  The
// value is parsed as YAML, so that numbers and lists can be given.
type parameterFlags map[string]any

func (p parameterFlags) String() string {
	return fmt.Sprint(map[string]any(p))
}

func (p parameterFlags) Set(raw string) error {
	name, rawValue, found := strings.Cut(raw, "=")
	if !found || name == "" {
		return fmt.Errorf("expected name=value, got %q", raw)
	}
	var value any
	if err := yaml.Unmarshal([]byte(rawValue), &value); err != nil {
		return fmt.Errorf("invalid value for %v: %v", name, err)
	}
	p[name] = value
	return nil
}

func main() {
	reportsDir := flag.String("reports", "reports", "directory with the report definitions")
	outputDir := flag.String("output", "report-output", "directory for the CSV files")
	cliParameters := parameterFlags{}
	flag.Var(cliParameters, "param", "override a parameter, e.g. -param year=2024 (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %v [options] [report name …]\n\nWithout report names, all reports are created.\n\n",
			os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	sharedParameters := map[string]any{}
	if err := loadYAML(filepath.Join(*reportsDir, parametersFilename), &sharedParameters); err != nil &&
		!os.IsNotExist(err) {
		tbr_errors.ExitWithExpectedError("Could not read shared parameters", 10, "error", err)
	}
	names := flag.Args()
	if len(names) == 0 {
		var err error
		names, err = reportNames(*reportsDir)
		tbr_errors.ExitOnExpectedError(err, "Could not find reports", 10)
	}
	err := os.MkdirAll(*outputDir, 0777)
	tbr_errors.ExitOnExpectedError(err, "Could not create output directory", 10)

	client, err := mongodb.Connect(context.Background(), mongoopts.Client().ApplyURI(options.MongoOpts.Host))
	tbr_errors.ExitOnExpectedError(err, "Could not connect to MongoDB", 10)
	defer func() {
		err := client.Disconnect(context.Background())
		tbr_errors.ExitOnExpectedError(err, "Could not disconnect from MongoDB", 10)
	}()
	db := client.Database(options.MongoOpts.DBName)

	failed := false
	for _, name := range names {
		var definition report
		if err := loadYAML(filepath.Join(*reportsDir, name+".yaml"), &definition); err != nil {
			slog.Error("Could not read report", "report", name, "error", err)
			failed = true
			continue
		}
		parameters := mergeParameters(sharedParameters, definition.Parameters, cliParameters)
		if rows, err := runReport(db, name, definition, parameters, *outputDir); err != nil {
			slog.Error("Could not create report", "report", name, "error", err)
			failed = true
		} else {
			slog.Info("Created report", "report", name, "rows", rows,
				"file", filepath.Join(*outputDir, name+".csv"))
		}
	}
	if failed {
		os.Exit(1)
	}
}
