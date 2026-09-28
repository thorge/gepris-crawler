/*
crawler downloads project data from GEPRIS.
It stores the data in a MongoDB.

See the README file for further information.
*/
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	tbr_errors "gitlab.com/bronger/tools/errors"
	tbr_logging "gitlab.com/bronger/tools/logging"
	"go.mongodb.org/mongo-driver/bson"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/options"
	"jugit.fz-juelich.de/fdm/pmh-server/mongo"
)

// init sets up logging into a file.  The file is called “gepris-crawler.log”,
// and the directory is taken from the environment variable
// GEPRIS_CRAWLER_LOG_PATH.  If this is not set, Go’s default logger ist used.
func init() {
	var logfile io.Writer
	logPath := os.Getenv("GEPRIS_CRAWLER_LOG_PATH")
	if logPath == "" {
		logfile = os.Stderr
	} else {
		logFilename := filepath.Join(logPath, "gepris-crawler.log")
		var err error
		logfile, err = os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		tbr_errors.ExitOnExpectedError(err, "Could not open logfile", 10, "path", logFilename)
	}
	tbr_logging.Init(logfile, slog.LevelDebug)
}

const (
	numberProjectWorkers = 4
	triesLeft            = 10
	maxWaitMillisec      = 10000
	maxRequestsPerSec    = 50
	resultsPerPage       = 50
)

var cachePages = os.Getenv("CRAWLER_DEBUG") == "1"

// processProject takes the DFG project number and returns a projecter object
// already filled with all data.  The caller will then send this object to the
// storage (MongoDB).  This function decides which concrete type will be used
// for the projecter.
func processProject(projectNumber string) (result projecter, err error) {
	url := fmt.Sprintf("https://gepris.dfg.de/backend/project/get?id=%v&ranking=0", projectNumber)
	var raw []byte
	filename := fmt.Sprintf("project-pages/%v.json", projectNumber)
	if cachePages {
		if bodyRaw, err := os.ReadFile(filename); err == nil {
			slog.Info("Could take page from cache")
			raw = bodyRaw
		}
	}
	if raw == nil {
		if raw, err = fetchJSON(slog.Default(), url); err != nil {
			return nil, err
		}
		if cachePages {
			err = os.WriteFile(filename, raw, 0666)
			tbr_errors.ExitOnExpectedError(err, "Could not write file", 10, "filename", filename)
		}
	}
	var project apiProject
	if err := json.Unmarshal(raw, &project); err != nil {
		return nil, fmt.Errorf("could not parse JSON from GEPRIS: %v", err)
	}
	identifier := "G:(GEPRIS)" + projectNumber
	switch {
	case len(project.Teilprojekte.Groups) > 0:
		result = &projectUmbrella{projectParties: projectParties{
			projectBase: projectBase{identifier: identifier}}}
	case hasPersonGroup(project.Per, "Antragsteller/-in"):
		result = &projectPerson{projectBase: projectBase{identifier: identifier}}
	default:
		result = &projectInstitution{projectParties: projectParties{
			projectBase: projectBase{identifier: identifier}}}
	}
	if err = result.fillData(slog.Default(), &project); err != nil {
		return nil, err
	}
	return result, nil
}

var ErrNoFurtherResults = errors.New("no further search results")

// searchResponse is the part of the GEPRIS search API response that the
// crawler needs.
type searchResponse struct {
	Hits struct {
		Total int `json:"total"`
		Hits  []struct {
			PrjID int `json:"prj_id"`
		} `json:"hits"`
	} `json:"hits"`
}

// getSearchResults downloads one page of GEPRIS search results with
// resultsPerPage entries and sends the project numbers to the channel
// projectNumbers.  The given index is the starting index in the search
// results.  Thus, the caller should start with 0 and increment it by
// resultsPerPage in subsequent calls.  The given year finds all projects
// funded in that year.  Pass 0 if you want to have all projects.  The return
// value may be ErrNoFurtherResults, which means that the index was larger than
// the number of search results.
func getSearchResults(year int, index int, projectNumbers chan<- string) error {
	payload := map[string]any{
		"size": resultsPerPage,
		"from": index,
		"sort": "prj_beginn_jahr:asc",
	}
	if year != 0 {
		payload["foerderungszeitraum_von"] = strconv.Itoa(year)
		payload["foerderungszeitraum_bis"] = strconv.Itoa(year)
	}
	var raw []byte
	filename := fmt.Sprintf("search-results/%v_%v_%v.json", year, index, resultsPerPage)
	if cachePages {
		if bodyRaw, err := os.ReadFile(filename); err == nil {
			slog.Info("Could take search results from cache")
			raw = bodyRaw
		}
	}
	if raw == nil {
		var err error
		if raw, err = postJSON(slog.Default(), "https://gepris.dfg.de/backend/project/search", payload); err != nil {
			return err
		}
		if cachePages {
			err = os.WriteFile(filename, raw, 0666)
			tbr_errors.ExitOnExpectedError(err, "Could not write file", 10, "filename", filename)
		}
	}
	var response searchResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("could not parse JSON from GEPRIS: %v", err)
	}
	if index >= response.Hits.Total {
		return ErrNoFurtherResults
	}
	for _, hit := range response.Hits.Hits {
		projectNumbers <- strconv.Itoa(hit.PrjID)
	}
	return nil
}

// getYears returns the years of funding to be searched by the crawler.  If all
// projects should be found, it returns a single 0.
func getYears() []int {
	if os.Getenv("CRAWLER_FULL") == "1" {
		slog.Info("Will crawl all years")
		return []int{0}
	} else {
		currentYear := time.Now().Year()
		years := []int{currentYear - 1, currentYear}
		slog.Info("Will crawl only certain years", "years", years)
		return years
	}
}

// grantDocument is used by updateParentTitles to hold MongoDB results.
// Therefore, it contains only the subset of fields of grants that are needed
// here.
type grantDocument struct {
	Identifier         string
	Title              string
	ShortTitle         string
	EndYear            int
	Parent             string
	ParentTitle        string
	latestChildEndYear int
}

var sfbRegexp = regexp.MustCompile(`^SFB ([0-9]+)`)
var subProjectRegexp = regexp.MustCompile(`\s+\(([^)]*?)[*#]*\)$`)
var grkRegexp = regexp.MustCompile(`^(GRK [0-9]+)`)

// findShortTitle uses heuristics to return a good abbreviated title for the
// given project.  If none can be reliably created, an empty string is
// returned.  Currently, SFB sub-projects (not the parent project, because they
// should not not appear in selection lists) and GRKs are abbreviated.
func findShortTitle(doc grantDocument) string {
	if parentMatches := sfbRegexp.FindStringSubmatch(doc.ParentTitle); parentMatches != nil {
		if matches := subProjectRegexp.FindStringSubmatch(doc.Title); matches != nil {
			return fmt.Sprintf("SFB %v %v", parentMatches[1], matches[1])
		}
	}
	if matches := grkRegexp.FindStringSubmatch(doc.Title); matches != nil {
		return matches[1]
	}
	return fmt.Sprintf("DFG project %v", doc.Identifier)
}

// denormalizeParentRelations walks through all projects in the MongoDB
// database and sets the parent titles according to the parent identifiers.
// When fetching the projects from the GEPRIS, we only get the *identifier* of
// the parent project.  But when returning results in PMH requests, we also
// need the title.  In order to avoid having to query for them then, we use
// denormalisation by adding the parent’s title here.
//
// Moreover, this function sets the latestChildEndYear field of all parents.
// This way, the PMH server can offer parent projects for harvesting even if
// they are too old, as long as one of their respective children is new enough.
func denormalizeParentRelations(coll *mongodb.Collection) {
	if cursor, err := coll.Find(context.Background(), bson.D{}); err != nil {
		tbr_errors.ExitWithExpectedError("Could not access MongoDB", 10)
	} else {
		titles := make(map[string]string)
		latestChildEndYears := make(map[string]int)
		oldLatestChildEndYears := make(map[string]int)
		slog.Info("updating all parent project titles …")
		for cursor.Next(context.Background()) {
			var document grantDocument
			if err := cursor.Decode(&document); err != nil {
				tbr_errors.ExitWithExpectedError("Invalid record in MongoDB", 10)
			}
			titles[document.Identifier] = document.Title
			oldParentTitle := document.ParentTitle
			if document.Parent != "" {
				if latestChildEndYears[document.Parent] < document.EndYear {
					latestChildEndYears[document.Parent] = document.EndYear
				}
				if foundParentTitle, ok := titles[document.Parent]; !ok {
					var parentDocument grantDocument
					if err := coll.FindOne(context.Background(), bson.D{{"identifier", document.Parent}}).
						Decode(&parentDocument); err != nil {
						slog.Error("MongoDB result invalid",
							"document", document.Parent, "error", err)
					} else {
						oldLatestChildEndYears[parentDocument.Identifier] =
							parentDocument.latestChildEndYear

						document.ParentTitle = parentDocument.Title
						if document.ParentTitle == "" {
							slog.Error("no title found", "document", document.Parent)
							continue
						}
						titles[parentDocument.Identifier] = parentDocument.Title
					}
				} else {
					document.ParentTitle = foundParentTitle
				}
			}
			oldShortTitle := document.ShortTitle
			document.ShortTitle = findShortTitle(document)
			if oldParentTitle != document.ParentTitle || oldShortTitle != document.ShortTitle {
				if result, err := coll.UpdateOne(
					context.Background(), bson.D{{"identifier", document.Identifier}},
					bson.D{{"$set", bson.M{
						"parentTitle":      document.ParentTitle,
						"shortTitle":       document.ShortTitle,
						"lastModification": time.Now()}}}); err != nil {
					tbr_errors.ExitWithExpectedError("MongoDB error while updating", 10,
						"document identifier", document.Identifier)
				} else {
					if result.UpsertedCount+result.MatchedCount != 1 {
						slog.Error("parent title / short title "+
							"was not written properly to MongoDB", "result", result)
					} else {
						if oldParentTitle != document.ParentTitle {
							slog.Debug("updated parent title",
								"document identifier", document.Identifier,
								"old parent title", oldParentTitle,
								"new parent title", document.ParentTitle)
						}
						if oldShortTitle != document.ShortTitle {
							slog.Debug("updated short title",
								"document identifier", document.Identifier,
								"old short title", oldShortTitle,
								"new short title", document.ShortTitle)
						}
					}
				}
			}
		}
		tbr_errors.ExitOnExpectedError(cursor.Err(), "Could not iterate though MongoDB documents", 10)
		slog.Info("finished updating all parent project titles")

		slog.Info("updating all parents’ “end year of latest child” …")
		for identifier, latestChildEndYear := range latestChildEndYears {
			if latestChildEndYear == oldLatestChildEndYears[identifier] {
				continue
			}
			if result, err := coll.UpdateOne(
				context.Background(), bson.D{{"identifier", identifier}},
				bson.D{{"$set", bson.M{
					"latestChildEndYear": latestChildEndYear,
					"lastModification":   time.Now()}}}); err != nil {
				tbr_errors.ExitWithExpectedError("MongoDB error while updating", 10,
					"document identifier", identifier)
			} else {
				if result.UpsertedCount+result.MatchedCount != 1 {
					slog.Error("end year of latest child was not written properly to MongoDB",
						"result", result)
				} else {
					slog.Debug("updated end year of latest child",
						"parent identifier", identifier,
						"end year of latest child", latestChildEndYear)
				}
			}
		}
		slog.Info("finished updating all parents’ “end year of latest child”")
	}
}

// processProjectNumbers is meant to run in multiple instances parallelly.  It
// reads project numbers from a channel “projectNumbers”, fetches the projects’
// data from the GEPRIS backend API, and writes the results back into another
// channel “projects”.  It finishes when projectNumbers is closed and reports
// this back in the given wait group.  “allProjectNumbers” holds a map created
// by the caller that contains all process numbers already processed because
// some may occur multiple times in “projectNumbers”.
func processProjectNumbers(projectNumbers <-chan string, projects chan<- projecter, wg *sync.WaitGroup,
	allProjectNumbers map[string]bool, m *sync.Mutex) {
	defer wg.Done()
	for projectNumber := range projectNumbers {
		m.Lock()
		alreadyProcessed := allProjectNumbers[projectNumber]
		if !alreadyProcessed {
			allProjectNumbers[projectNumber] = true
		}
		m.Unlock()
		if !alreadyProcessed {
			if project, err := processProject(projectNumber); err != nil {
				slog.Error("Could not process project",
					"url", fmt.Sprintf("https://gepris.dfg.de/backend/project/get?id=%v", projectNumber),
					"error", err)
			} else {
				projects <- project
			}
		}
	}
}

var optUpsert = mongoopts.Replace().SetUpsert(true)

// storeProjects reads projects from “projects” and writes them to MongoDB.  It
// must not be run with multiple instances.  It reports back on the given wait
// group when it’s finished.
func storeProjects(projects <-chan projecter, coll *mongodb.Collection, wg *sync.WaitGroup) {
	defer wg.Done()
	for project := range projects {
		var oldProject map[string]any
		if err := coll.FindOne(context.Background(), bson.M{"identifier": project.getIdentifier()}).
			Decode(&oldProject); err != nil && !errors.Is(err, mongodb.ErrNoDocuments) {
			tbr_errors.ExitWithExpectedError("Could not retrieve document from MongoDB", 10)
		}
		if oldProject == nil || project.isDifferent(oldProject) {
			if oldProject == nil {
				slog.Debug("project is new", "identifier", project.getIdentifier())
			} else {
				slog.Debug("project has changed", "identifier", project.getIdentifier())
			}
			filter, update := project.updateMongo()
			update["lastModification"] = time.Now()
			result, err := coll.ReplaceOne(context.Background(), filter, update, optUpsert)
			tbr_errors.ExitOnExpectedError(err, "MongoDB error when replacing document", 10)
			if result.UpsertedCount+result.ModifiedCount != 1 {
				slog.Error("project was not written properly to MongoDB",
					"identifier", project.getIdentifier())
			}
		} else {
			slog.Debug("project is unchanged", "identifier", project.getIdentifier())
		}
	}
}

// The main routine has three operations that run in parallel: (1) The search
// results of GEPRIS are downloaded and searched for project numbers.  (2) The
// project numbers are used to download the project details, which are searched
// for the content of the project data fields.  (3) The project instances are
// written to MongoDB.  Here, (2) and (3) are Go routines, (2) is even a set of
// Go routines.  In contrast, (1) is a synchronous top-level loop in the main
// function.
//
// All three are connected with channels: “projectNumbers” between (1) and (2),
// and “projects” between (2) and (3).
func main() {
	var coll *mongodb.Collection
	{
		var mongoClient *mongodb.Client
		var err error
		mongoClient, coll, err = mongo.InitializeMongo(context.Background(), options.MongoOpts)
		tbr_errors.ExitOnExpectedError(err, "Could not initialise connection to MongoDB", 10)
		defer func() {
			err := mongoClient.Disconnect(context.Background())
			tbr_errors.ExitOnExpectedError(err, "Could not disconnect from MongoDB", 10)
		}()
	}

	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string, 10*numberProjectWorkers)
	projects := make(chan projecter)
	allProjectNumbers := make(map[string]bool)
	var allProjectNumbersMutex sync.Mutex
	for range numberProjectWorkers {
		projectWorkersWaitGroup.Add(1)
		go processProjectNumbers(
			projectNumbers, projects, &projectWorkersWaitGroup, allProjectNumbers, &allProjectNumbersMutex)
	}

	var projectStorageWaitGroup sync.WaitGroup
	projectStorageWaitGroup.Add(1)
	go storeProjects(projects, coll, &projectStorageWaitGroup)

	ctxTicker, cancelTicker := context.WithCancel(context.Background())
	go enableTicker(ctxTicker, slog.Default())
	for _, year := range getYears() {
		index := 0
		for {
			err := getSearchResults(year, index, projectNumbers)
			if errors.Is(err, ErrNoFurtherResults) {
				break
			} else if err != nil {
				slog.Error("Could not get search results from GEPRIS", "error", err)
			}
			index += resultsPerPage
		}
	}

	close(projectNumbers)
	projectWorkersWaitGroup.Wait()
	cancelTicker()
	close(projects)
	projectStorageWaitGroup.Wait()

	denormalizeParentRelations(coll)
}
