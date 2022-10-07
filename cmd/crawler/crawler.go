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
	numberSearchWorkers  = 4
	numberProjectWorkers = 4
	triesLeft            = 10
	maxWaitMillisec      = 10000
	maxRequestsPerSec    = 50
	resultsPerPage       = 50
)

var subProjectListRegex = regexp.MustCompile("^.*Projekte.*$")

// processProject takes the DFG project number and returns a projecter object
// already filled with all data.  The caller will then send this object to the
// storage (MongoDB).  This function decides which concrete type will be used
// for the projecter.
func processProject(projectNumber string) (result projecter, err error) {
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/projekt/%v", projectNumber)
	var body string
	if bodyRaw, err := os.ReadFile(fmt.Sprintf("project-pages/%v.html", projectNumber)); err != nil {
		if body, err = fetchPage(url); err != nil {
			return nil, err
		}
		err = os.WriteFile(fmt.Sprintf("project-pages/%v.html", projectNumber), []byte(body), 0666)
		check(err)
	} else {
		logger.Printf("INFO: Could take page from cache")
		body = string(bodyRaw)
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
// resultsPerPage in subsequent calls.  The return value may be
// ErrNoFurtherResults, which means that the index was larger than the number
// of search results.
func getSearchResults(index int, projectNumbers chan<- string) error {
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/OCTOPUS?context=projekt&findButton=historyCall&"+
		"hitsPerPage=%v&index=%v&task=doKatalog", resultsPerPage, index)
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

type grantDocument struct {
	Number      string
	Title       string
	Parent      string
	ParentTitle string
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
	if mongoClient, coll_, err := mongo.InitializeMongo(context.Background()); err != nil {
		check(err)
	} else {
		defer func() { err := mongoClient.Disconnect(context.Background()); check(err) }()
		coll = coll_
	}
	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string, 10*numberProjectWorkers)
	projects := make(chan projecter)
	for i := 0; i < numberProjectWorkers; i++ {
		projectWorkersWaitGroup.Add(1)
		go func() {
			defer projectWorkersWaitGroup.Done()
			for projectNumber := range projectNumbers {
				if project, err := processProject(projectNumber); err != nil {
					logger.Printf("ERROR: While processing <https://gepris.dfg.de/gepris/projekt/%v>: %v",
						projectNumber, err)
				} else {
					projects <- project
				}
			}
		}()
	}
	var projectStorageWaitGroup sync.WaitGroup
	projectStorageWaitGroup.Add(1)
	go func() {
		defer projectStorageWaitGroup.Done()
		for project := range projects {
			filter, update := project.updateMongo()
			mongo.UpdateLastModificationTimestamp(update)
			result, err := coll.UpdateOne(context.Background(), filter, update, mongo.MongoOpts)
			check(err)
			if result.UpsertedCount+result.ModifiedCount != 1 {
				logger.Println("ERROR: project was not written properly to MongoDB")
			}
		}
	}()
	index := 0
	for {
		err := getSearchResults(index, projectNumbers)
		if errors.Is(err, ErrNoFurtherResults) {
			break
		} else if err != nil {
			logger.Println("ERROR:", err)
		}
		index += resultsPerPage
	}
	close(projectNumbers)
	projectWorkersWaitGroup.Wait()
	close(projects)
	projectStorageWaitGroup.Wait()
	logger.Println("INFO: updating all parent project titles …")
	if cursor, err := coll.Find(context.Background(), bson.D{}); err != nil {
		logger.Panic("could not access MongoDB")
	} else {
		defer func() { err = cursor.Close(context.Background()) }()
		titles := make(map[string]string)
		for cursor.Next(context.Background()) {
			var document grantDocument
			if err := cursor.Decode(&document); err != nil {
				logger.Panicf("invalid record in MongoDB: %v", err)
			}
			if document.Parent == "" {
				continue
			}
			// titles[document.Number] = document.Title
			var title string
			if foundTitle, ok := titles[document.Parent]; !ok {
				result := coll.FindOne(context.Background(), bson.D{{"number", document.Parent}})
				var parentDocument grantDocument
				if err := result.Decode(&parentDocument); err != nil {
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
