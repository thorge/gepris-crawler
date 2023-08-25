/*
crawler downloads project data from GEPRIS.
It stores the data in a MongoDB.

See the README file for further information.
*/
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	tbr_errors "gitlab.com/bronger/tools/errors"
	"go.mongodb.org/mongo-driver/bson"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/mongo"
)

// setupLogging sets up logging into a file.  The file is called
// “gepris-crawler.log”, and the directory is taken from the environment
// variable GEPRIS_CRAWLER_LOG_PATH.  If this is not set, Go’s default logger
// ist used.
func setupLogging() *log.Logger {
	logPath := os.Getenv("GEPRIS_CRAWLER_LOG_PATH")
	if logPath == "" {
		return log.Default()
	}
	logFilename := filepath.Join(logPath, "gepris-crawler.log")
	logfile, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Panic(err)
	}
	return log.New(logfile, "", log.Lshortfile|log.LstdFlags)
}

var logger = setupLogging()

func init() {
	mongo.Logger = logger
}

const (
	numberProjectWorkers = 4
	triesLeft            = 10
	maxWaitMillisec      = 10000
	maxRequestsPerSec    = 50
	resultsPerPage       = 50
)

var subProjectListRegex = regexp.MustCompile("^.*Projekte.*$")
var cachePages = os.Getenv("CRAWLER_DEBUG") == "1"

// processProject takes the DFG project number and returns a projecter object
// already filled with all data.  The caller will then send this object to the
// storage (MongoDB).  This function decides which concrete type will be used
// for the projecter.
func processProject(projectNumber string) (result projecter, err error) {
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/projekt/%v", projectNumber)
	var body string
	filename := fmt.Sprintf("project-pages/%v.html", projectNumber)
	if cachePages {
		if bodyRaw, err := os.ReadFile(filename); err == nil {
			logger.Printf("INFO: Could take page from cache")
			body = string(bodyRaw)
		}
	}
	if body == "" {
		if body, err = fetchPage(url); err != nil {
			return nil, err
		}
		if cachePages {
			err = os.WriteFile(filename, []byte(body), 0666)
			tbr_errors.ExitOnExpectedError(err, "Could not write file", 10, "filename", filename)
		}
	}
	doc, err := getDocument(body)
	tbr_errors.ExitOnExpectedError(err, "Could not parse HTML", 10, "body", body)
	if doc.Find("h2.facelift").FilterFunction(filterByRegexp(subProjectListRegex)).Length() > 0 {
		result = &projectUmbrella{projectBase: projectBase{number: projectNumber}}
	} else if doc.Find(`span.name:contains("Antragstellende Institution")`).Length() > 0 {
		result = &projectInstitution{projectBase: projectBase{number: projectNumber}}
	} else {
		result = &projectPerson{projectBase: projectBase{number: projectNumber}}
	}
	err = result.scanDocument(doc)
	return result, err
}

var ErrNoFurtherResults = errors.New("no further search results")
var searchResultRegexp = regexp.MustCompile(`<a class="intern" href="/gepris/projekt/(?P<projectNumber>\d+)">mehr</a>`)

// getSearchResults downloads the GEPRIS results page with resultsPerPage
// entries, scans for the project numbers and sends them to the channel
// projectNumbers.  The given index is the starting index in the search
// results.  Thus, the caller should start with 0 and increment it by
// resultsPerPage in subsequent calls.  The given year finds all projects
// funded in that year.  Pass 0 if you want to have all projects.  The return
// value may be ErrNoFurtherResults, which means that the index was larger than
// the number of search results.
func getSearchResults(year int, index int, projectNumbers chan<- string) error {
	var fundedIn string
	if year != 0 {
		fundedIn = fmt.Sprintf("gefoerdertIn=%v&", year)
	}
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/OCTOPUS?context=projekt&findButton=historyCall&"+
		fundedIn+"hitsPerPage=%v&index=%v&task=doKatalog", resultsPerPage, index)
	var body string
	filename := fmt.Sprintf("search-results/%v_%v_%v.html", year, index, resultsPerPage)
	if cachePages {
		if bodyRaw, err := os.ReadFile(filename); err == nil {
			logger.Printf("INFO: Could take search results from cache")
			body = string(bodyRaw)
		}
	}
	if body == "" {
		var err error
		if body, err = fetchPage(url); err != nil {
			return err
		}
		if cachePages {
			err = os.WriteFile(filename, []byte(body), 0666)
			tbr_errors.ExitOnExpectedError(err, "Could not write file", 10, "filename", filename)
		}
	}
	if doc, err := getDocument(body); err != nil {
		return err
	} else {
		if doc.Find(`p.warning:contains("Ihre Suche ergab leider keine Treffer")`).Length() != 0 {
			return ErrNoFurtherResults
		} else {
			var err error
			doc.Find("h2 > a").EachWithBreak(func(_ int, s *goquery.Selection) bool {
				if url, exists := s.Attr("href"); exists {
					if match := urlRegexp.FindStringSubmatch(url); match == nil {
						err = fmt.Errorf("invalid URL “%v” found on search page", url)
						return false
					} else {
						projectNumbers <- match[1]
					}
				} else {
					err = fmt.Errorf("h2 > a element on search page had no “href” attribute")
					return false
				}
				return true
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// getYears returns the years of funding to be searched by the crawler.  If all
// projects should be found, it returns a single 0.
func getYears() []int {
	if os.Getenv("CRAWLER_FULL") == "1" {
		logger.Printf("INFO: Will crawl all years")
		return []int{0}
	} else {
		currentYear := time.Now().Year()
		years := []int{currentYear - 1, currentYear}
		logger.Printf("INFO: Will crawl only the years %v", years)
		return years
	}
}

// grantDocument is used by updateParentTitles to hold MongoDB results.
// Therefore, it contains only the subset of fields of grants that are needed
// here.
type grantDocument struct {
	Number      string
	Title       string
	ShortTitle  string
	Parent      string
	ParentTitle string
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
	return fmt.Sprintf("DFG project %v", doc.Number)
}

// updateParentTitles walks through all projects in the MongoDB database and
// sets the parent titles according to the parent numbers.  When fetching the
// projects from the GEPRIS, we only get the *number* of the parent project.
// But when returning results in PMH requests, we also need the title.  In
// order to avoid having to query for them then, we use denormalisation by
// adding the parent’s title here.
func updateParentTitles(coll *mongodb.Collection) {
	logger.Println("INFO: updating all parent project titles …")
	if cursor, err := coll.Find(context.Background(), bson.D{}); err != nil {
		logger.Panic("could not access MongoDB")
	} else {
		titles := make(map[string]string)
		for cursor.Next(context.Background()) {
			var document grantDocument
			if err := cursor.Decode(&document); err != nil {
				logger.Panicf("invalid record in MongoDB: %v", err)
			}
			titles[document.Number] = document.Title
			oldParentTitle := document.ParentTitle
			if document.Parent != "" {
				if foundParentTitle, ok := titles[document.Parent]; !ok {
					var parentDocument grantDocument
					if err := coll.FindOne(context.Background(), bson.D{{"number", document.Parent}}).
						Decode(&parentDocument); err != nil {
						logger.Printf("ERROR: MongoDB result invalid for %v: %v",
							document.Parent, err)
					} else {
						document.ParentTitle = parentDocument.Title
						if document.ParentTitle == "" {
							logger.Printf("ERROR: no title of %v found", document.Parent)
							continue
						}
						titles[parentDocument.Number] = parentDocument.Title
					}
				} else {
					document.ParentTitle = foundParentTitle
				}
			}
			oldShortTitle := document.ShortTitle
			document.ShortTitle = findShortTitle(document)
			if oldParentTitle != document.ParentTitle || oldShortTitle != document.ShortTitle {
				if result, err := coll.UpdateOne(context.Background(), bson.D{{"number", document.Number}},
					bson.D{{"$set", bson.M{
						"parentTitle":      document.ParentTitle,
						"shortTitle":       document.ShortTitle,
						"lastModification": time.Now()}}}); err != nil {
					logger.Panicf("MongoDB error while updating %v: %v", document.Number, err)
				} else {
					if result.UpsertedCount+result.MatchedCount != 1 {
						logger.Println("ERROR: parent title / short title "+
							"was not written properly to MongoDB:", result)
					} else {
						if oldParentTitle != document.ParentTitle {
							logger.Printf("DEBUG: updated parent title of %v from “%v” to “%v”",
								document.Number, oldParentTitle, document.ParentTitle)
						}
						if oldShortTitle != document.ShortTitle {
							logger.Printf("DEBUG: updated short title of %v from “%v” to “%v”",
								document.Number, oldShortTitle, document.ShortTitle)
						}
					}
				}
			}
		}
		tbr_errors.ExitOnExpectedError(cursor.Err(), "Could not iterate though MongoDB documents", 10)
	}
	logger.Println("INFO: finished updating all parent project titles")
}

// processProjectNumbers is meant to run in multiple instances parallelly.  It
// reads project numbers from a channel “projectNumbers”, scrapes the projects’
// data from the GEPRIS website, and writes the results back into another
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
				logger.Printf("ERROR: While processing "+
					"<https://gepris.dfg.de/gepris/projekt/%v>: %v", projectNumber, err)
			} else {
				projects <- project
			}
		}
	}
}

// storeProjects reads projects from “projects” and writes them to MongoDB.  It
// must not be run with multiple instances.  It reports back on the given wait
// groups when it’s finished.
func storeProjects(projects <-chan projecter, coll *mongodb.Collection, wg *sync.WaitGroup) {
	defer wg.Done()
	for project := range projects {
		var oldProject map[string]any
		if err := coll.FindOne(context.Background(), bson.M{"number": project.getNumber()}).
			Decode(&oldProject); err != nil && !errors.Is(err, mongodb.ErrNoDocuments) {
			logger.Panic(err)
		}
		if oldProject == nil || project.isDifferent(oldProject) {
			if oldProject == nil {
				logger.Printf("DEBUG: project %v is new", project.getNumber())
			} else {
				logger.Printf("DEBUG: project %v has changed", project.getNumber())
			}
			filter, update := project.updateMongo()
			update["lastModification"] = time.Now()
			result, err := coll.ReplaceOne(context.Background(), filter, update, mongo.MongoOpts)
			tbr_errors.ExitOnExpectedError(err, "MongoDB error when replacing document", 10)
			if result.UpsertedCount+result.ModifiedCount != 1 {
				logger.Printf("ERROR: project %v was not written properly to MongoDB", project.getNumber())
			}
		} else {
			logger.Printf("DEBUG: project %v is unchanged", project.getNumber())
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
		mongoClient, coll, err = mongo.InitializeMongo(context.Background())
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
	for i := 0; i < numberProjectWorkers; i++ {
		projectWorkersWaitGroup.Add(1)
		go processProjectNumbers(
			projectNumbers, projects, &projectWorkersWaitGroup, allProjectNumbers, &allProjectNumbersMutex)
	}

	var projectStorageWaitGroup sync.WaitGroup
	projectStorageWaitGroup.Add(1)
	go storeProjects(projects, coll, &projectStorageWaitGroup)

	ctxTicker, cancelTicker := context.WithCancel(context.Background())
	go enableTicker(ctxTicker)
	for _, year := range getYears() {
		index := 0
		for {
			err := getSearchResults(year, index, projectNumbers)
			if errors.Is(err, ErrNoFurtherResults) {
				break
			} else if err != nil {
				logger.Println("ERROR:", err)
			}
			index += resultsPerPage
		}
	}

	close(projectNumbers)
	projectWorkersWaitGroup.Wait()
	cancelTicker()
	close(projects)
	projectStorageWaitGroup.Wait()

	updateParentTitles(coll)
}
