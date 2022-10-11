/*
crawler downloads project data from GEPRIS.
It stores the data in a MongoDB.

Environment variables:

GEPRIS_CRAWLER_LOG_PATH – path to the directory to which the log file is
written.  If not given, the log is sent to stderr.
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
	"go.mongodb.org/mongo-driver/bson"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/mongo"
)

// check panics if its argument is non-nil.  It marks locations that should be
// re-visited for possibly better error management.  If it is clear that a
// panic is the only appropriate reaction, this function should not be used.
func check(e error) {
	if e != nil {
		logger.Panicln("FATAL:", e)
	}
}

// setupLogging sets up logging into a file.  The file is called
// “gepris-crawler”, and the directory is taken from the environment variable
// GEPRIS_CRAWLER_LOG_PATH.  If this is not set, Go’s default logger ist used.
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
	if cachePages {
		if bodyRaw, err := os.ReadFile(fmt.Sprintf("project-pages/%v.html", projectNumber)); err == nil {
			logger.Printf("INFO: Could take page from cache")
			body = string(bodyRaw)
		}
	}
	if body == "" {
		if body, err = fetchPage(url); err != nil {
			return nil, err
		}
		if cachePages {
			err = os.WriteFile(fmt.Sprintf("project-pages/%v.html", projectNumber), []byte(body), 0666)
			check(err)
		}
	}
	doc, err := getDocument(body)
	check(err)
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
	filename := fmt.Sprintf("search-results/%v_50.html", index)
	if bodyRaw, err := os.ReadFile(filename); err != nil {
		if body, err = fetchPage(url); err != nil {
			return err
		}
		err = os.WriteFile(filename, []byte(body), 0666)
		check(err)
	} else {
		logger.Printf("INFO: Could take search results from cache")
		body = string(bodyRaw)
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

func getYears() []int {
	if os.Getenv("CRAWLER_FULL") == "1" {
		return []int{0}
	} else {
		currentYear := time.Now().Year()
		return []int{currentYear - 1, currentYear}
	}
}

type grantDocument struct {
	Number      string
	Title       string
	Parent      string
	ParentTitle string
}

func updateParentTitles(coll *mongodb.Collection) {
	logger.Println("INFO: updating all parent project titles …")
	if cursor, err := coll.Find(context.Background(), bson.D{}); err != nil {
		logger.Panic("could not access MongoDB")
	} else {
		defer func() { err := cursor.Close(context.Background()); check(err) }()
		titles := make(map[string]string)
		for cursor.Next(context.Background()) {
			var document grantDocument
			if err := cursor.Decode(&document); err != nil {
				logger.Panicf("invalid record in MongoDB: %v", err)
			}
			if document.Parent == "" {
				continue
			}
			titles[document.Number] = document.Title
			var title string
			if foundTitle, ok := titles[document.Parent]; !ok {
				var parentDocument grantDocument
				if err := coll.FindOne(context.Background(), bson.D{{"number", document.Parent}}).
					Decode(&parentDocument); err != nil {
					logger.Printf("ERROR: MongoDB result invalid for %v: %v", document.Parent, err)
				} else {
					title = parentDocument.Title
					if title == "" {
						logger.Printf("ERROR: no title of %v found", document.Parent)
						continue
					}
					titles[parentDocument.Number] = parentDocument.Title
				}
			} else {
				title = foundTitle
			}
			if result, err := coll.UpdateOne(context.Background(), bson.D{{"number", document.Number}},
				bson.D{{"$set", bson.M{"parentTitle": title}}}); err != nil {
				logger.Panicf("MongoDB error while updating %v: %v", document.Number, err)
			} else {
				if result.UpsertedCount+result.MatchedCount != 1 {
					logger.Println("ERROR: parent title was not written properly to MongoDB:", result)
				} else {
					logger.Printf("DEBUG: updated parent title of %v to “%v”", document.Number, title)
				}
			}
		}
	}
	logger.Println("INFO: finished updating all parent project titles")
}

func processProjectNumbers(projectNumbers <-chan string, projects chan<- projecter, wg *sync.WaitGroup,
	allProjectNumbers map[string]bool, m *sync.RWMutex) {
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
				logger.Println("DEBUG: project is new")
			} else {
				logger.Println("DEBUG: project has changed")
			}
			filter, update := project.updateMongo()
			mongo.UpdateLastModificationTimestamp(update)
			result, err := coll.ReplaceOne(context.Background(), filter, update, mongo.MongoOpts)
			check(err)
			if result.UpsertedCount+result.ModifiedCount != 1 {
				logger.Println("ERROR: project was not written properly to MongoDB")
			}
		} else {
			logger.Println("DEBUG: project is unchanged")
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
		check(err)
		defer func() { err := mongoClient.Disconnect(context.Background()); check(err) }()
	}

	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string, 10*numberProjectWorkers)
	projects := make(chan projecter)
	allProjectNumbers := make(map[string]bool)
	var allProjectNumbersMutex sync.RWMutex
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
