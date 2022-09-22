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
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go4.org/must"
	"golang.org/x/net/publicsuffix"
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

const (
	numberSearchWorkers  = 4
	numberProjectWorkers = 4
	triesLeft            = 3
	maxWaitMillisec      = 5000
	maxRequestsPerSec    = 50
	resultsPerPage       = 50
)

var ticker = make(chan time.Time, maxRequestsPerSec)

var ErrNoFurtherResults = errors.New("no further search results")
var searchResultRegexp = regexp.MustCompile(`<a class="intern" href="/gepris/projekt/(?P<projectNumber>\d+)">mehr</a>`)
var client http.Client

// fetchPage downloads the given URL end returns the HTML.  If the server
// answers with a 5xx status, the request is retried up to triesLeft times.
func fetchPage(url string) (string, error) {
	logger.Printf("INFO: fetching %v", url)
	var body string
	triesLeft := triesLeft
	for {
		<-ticker
		resp, err := client.Get(url)
		if err == nil {
			bodyRaw, err := ioutil.ReadAll(resp.Body)
			check(err)
			must.Close(resp.Body)
			body = string(bodyRaw)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			} else {
				logger.Printf("WARNING: The HTTP request for %v failed with status code %v: %v",
					url, resp.StatusCode, body)
				if resp.StatusCode < 500 || resp.StatusCode >= 600 {
					return "", fmt.Errorf("Giving up on %v.", url)
				}
			}
		} else {
			logger.Printf("WARNING: The HTTP request for %v failed without status code: %v", url, err)
		}
		triesLeft--
		if triesLeft == 0 {
			return "", fmt.Errorf("Giving up on %v.", url)
		}
		time.Sleep(time.Duration(rand.Int31n(maxWaitMillisec)) * time.Millisecond)
	}
	return body, nil
}

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
	if strings.Contains(body, `<p class="warning">Ihre Suche ergab leider keine Treffer.</p>`) {
		return ErrNoFurtherResults
	} else {
		for _, matches := range searchResultRegexp.FindAllStringSubmatch(body, -1) {
			projectNumbers <- matches[1]
		}
	}
	return nil
}

func init() {
	var transport = &http.Transport{
		// Dial is the original function, plus a line in the log.  This way,
		// one can see how many HTTP connections are established.
		Dial: func(network, addr string) (net.Conn, error) {
			logger.Println("DEBUG: Dial!")
			return net.Dial(network, addr)
		},
		MaxIdleConnsPerHost: numberSearchWorkers + numberProjectWorkers,
	}
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	check(err)
	client = http.Client{Transport: transport, Jar: jar}
	go func() {
		for t := range time.Tick(time.Second / maxRequestsPerSec) {
			select {
			case ticker <- t:
			default:
				logger.Println("DEBUG: Workers slower than rate limiter")
			}
		}
	}()
}

// projectBase contains the data included in every DFG project.  This is an
// abstract type that is only embedded into others.
type projectBase struct {
	number             string
	title              string
	process            string
	startYear, endYear int
	disciplines        []string
	url                string
	description        string
}

// projectPerson contains the data of a project the applicant of which is a
// person.
type projectPerson struct {
	projectBase
	applicant  string
	partPeople []string
}

// projectInstitution contains the data of a project the applicant of which is
// an institution.
type projectInstitution struct {
	projectBase
	institution      string
	coInstitutions   []string
	partInstitutions []string
	speaker          string
	coSpeakers       []string
	partPeople       []string
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectBase) updateMongo() (bson.D, bson.D) {
	filter := bson.D{{"number", project.number}}
	update := bson.D{{"$set", bson.M{
		"number":      project.number,
		"title":       project.title,
		"process":     project.process,
		"startYear":   project.startYear,
		"endYear":     project.endYear,
		"disciplines": project.disciplines,
		"url":         project.url,
		"description": project.description,
	}}}
	return filter, update
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectPerson) updateMongo() (bson.D, bson.D) {
	filter, update := project.projectBase.updateMongo()
	document := update[0].Value.(bson.M)
	document["type"] = "personal"
	document["applicant"] = project.applicant
	document["partPeople"] = project.partPeople
	return filter, update
}

// updateMongo returns the “filter” and “update” parameters ready to be used
// for “mongo.Collection.UpdateOne”.
func (project projectInstitution) updateMongo() (bson.D, bson.D) {
	filter, update := project.projectBase.updateMongo()
	document := update[0].Value.(bson.M)
	document["type"] = "institutional"
	document["institution"] = project.institution
	document["coInstitutions"] = project.coInstitutions
	document["partInstitutions"] = project.partInstitutions
	document["speaker"] = project.speaker
	document["coSpeakers"] = project.coSpeakers
	document["partPeople"] = project.partPeople
	return filter, update
}

// Types of the projecter interface are able to take data from a project HTML
// page and fill their internal data structure with it.  Then, they
type projecter interface {
	scanDocument(doc *goquery.Document) error
	updateMongo() (bson.D, bson.D)
}

// parseDisciplines returns all disciplines (“Fachbereiche”) found in doc.
func parseDisciplines(doc *goquery.Document) (disciplines []string) {
	doc.Find(`span.name:contains("Fachliche Zuordnung")`).Next().FilterFunction(func(i int, s *goquery.Selection) bool {
		return s.Nodes[0].Type == 3
	}).Each(func(i int, s *goquery.Selection) {
		disciplines = append(disciplines, normalizeString(s.Text()))
	})
	return
}

// parseLinkList returns all items found in the given subset of HTML elements.
// More precisely, the items are the text content in the “<a class="intern"
// href="…">…</a>” elements.
func parseLinkList(s *goquery.Selection) (elements []string) {
	s.Find("a.intern").Each(func(i int, s *goquery.Selection) {
		elements = append(elements, normalizeString(s.Text()))
	})
	return
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectBase) scanDocument(doc *goquery.Document) error {
	var err error
	if project.title, err = getField(doc.Find("div.details > h1")); err != nil {
		return err
	}
	if project.title == "" {
		return fmt.Errorf("project has no title")
	}
	project.process, err = getField(doc.Find(`span.name:contains("DFG-Verfahren")`).Next())
	check(err)
	if project.process == "" {
		return fmt.Errorf("project has no DFG process")
	}
	project.disciplines = parseDisciplines(doc)
	if len(project.disciplines) == 0 {
		return fmt.Errorf("project has no discipline")
	}
	project.startYear, project.endYear, err = parseTimespan(doc)
	check(err)
	project.description, err = getField(doc.Find("div#projekttext"))
	check(err)
	if project.description == "" {
		return fmt.Errorf("WARNING: project has no description (not even empty)")
	} else if project.description == "Keine Zusammenfassung vorhanden" {
		project.description = ""
	}
	return nil
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectPerson) scanDocument(doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(doc); err != nil {
		return err
	}
	if results := parseLinkList(doc.Find(`span.name:contains("Antragsteller"), span.name:contains("Antragstellerin")`).
		Next()); len(results) != 1 {
		return fmt.Errorf("number of applicants was not 1 but %v", len(results))
	} else {
		project.applicant = results[0]
	}
	project.partPeople = parseLinkList(doc.Find(`span.name:contains("Beteiligte Personen")`).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}

// scanDocument fills the internal data fields of the project with the data
// found in the given HTML document.
func (project *projectInstitution) scanDocument(doc *goquery.Document) error {
	if err := project.projectBase.scanDocument(doc); err != nil {
		return err
	}
	if results := parseLinkList(doc.Find(`span.name:contains("Antragstellende Institution")`).Next()); len(results) != 1 {
		return fmt.Errorf("number of applying institutions was not 1 but %v", len(results))
	} else {
		project.institution = results[0]
	}
	project.coInstitutions = parseLinkList(doc.Find(`span.name:contains("Mitantragstellende Institution")`).Next())
	if len(project.coInstitutions) != 0 {
		logger.Printf("DEBUG: found %v co-applying institutions", len(project.coInstitutions))
	}
	project.partInstitutions = parseLinkList(doc.Find(`span.name:contains("Beteiligte Einrichtung")`).Next())
	if len(project.partInstitutions) != 0 {
		logger.Printf("DEBUG: found %v participating institutions", len(project.partInstitutions))
	}
	if results := parseLinkList(doc.Find(`span.name:contains("Sprecher"), span.name:contains("Sprecherin")` +
		`span.name:contains("Leiter"), span.name:contains("Leiterin")`).Next()); len(results) != 1 {
		return fmt.Errorf("number of speakers was not 1 but %v", len(results))
	} else {
		project.speaker = results[0]
	}
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.coSpeakers = parseLinkList(doc.Find(`span.name:contains("Co-Sprecherinnen / Co-Sprecher")`).Next())
	if len(project.coSpeakers) != 0 {
		logger.Printf("DEBUG: found %v co-speakers", len(project.coSpeakers))
	}
	project.partPeople = parseLinkList(doc.Find(`span.name:contains("Beteiligte Personen")`).Next())
	if len(project.partPeople) != 0 {
		logger.Printf("DEBUG: found %v participating people", len(project.partPeople))
	}
	return nil
}

var whitespaceRegexp = regexp.MustCompile(`[\p{Z}\n\r\t]+`)

// normalizeString replaces any whitespace characters sequence with a single
// space.  Moreover, it removed leading and trailing whitespace.
func normalizeString(s string) (result string) {
	result = whitespaceRegexp.ReplaceAllString(s, " ")
	result = strings.TrimSpace(result)
	return result
}

// getField returns the normalised text content of the given document node(s).
// It returns an error if the field was empty or ambiguous.
func getField(s *goquery.Selection) (value string, err error) {
	s.EachWithBreak(func(i int, s *goquery.Selection) bool {
		if i == 0 {
			value = normalizeString(s.Text())
			if value == "" {
				err = fmt.Errorf(`First occurence of field was empty`)
				return false
			} else {
				return true
			}
		} else {
			err = fmt.Errorf(`Field was not unique`)
			return false
		}
	})
	return value, err
}

var timespanRegex = regexp.MustCompile(`Förderung von (\d\d\d\d) bis (\d\d\d\d)|` +
	`Förderung seit (\d\d\d\d)|Förderung in (\d\d\d\d)|Förderung: Bis (\d\d\d\d)|` +
	`Befindet sich in der laufenden Förderung.`)

// mustAtoi converts a string to an int and panics if something goes wrong.  It
// is used for converting regexp submatched that I know for sure to be
// integers.
func mustAtoi(s string) (i int) {
	i, err := strconv.Atoi(s)
	if err != nil {
		logger.Panicln("FATAL:", err)
	}
	return
}

// parseTimespan returns start and end year for a project.  It gets the HTML
// document of the project as input.  If any of start or end cannot be found in
// the document, 0 is used as the value.
func parseTimespan(doc *goquery.Document) (start int, end int, err error) {
	timespan, err := getField(doc.Find(`span.name:contains("Förderung")`).Next())
	if err != nil {
		return start, end, err
	}
	matches := timespanRegex.FindStringSubmatch(timespan)
	if len(matches) != 0 {
		if matches[5] != "" {
			end = mustAtoi(matches[5])
		} else if matches[4] != "" {
			start = mustAtoi(matches[4])
			end = start
		} else if matches[3] != "" {
			start = mustAtoi(matches[3])
		} else if matches[1] != "" && matches[2] != "" {
			start = mustAtoi(matches[1])
			end = mustAtoi(matches[2])
		}
	} else {
		logger.Panicln("FATAL:", timespan, "could not be matched")
	}
	return
}

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
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	check(err)
	if strings.Contains(body, "Antragstellende Institution") {
		result = &projectInstitution{projectBase: projectBase{number: projectNumber}}
	} else {
		result = &projectPerson{projectBase: projectBase{number: projectNumber}}
	}
	err = result.scanDocument(doc)
	return result, err
}

var mongoOpts = options.Update().SetUpsert(true)

var indexModel = mongo.IndexModel{
	Keys: bson.D{{"number", 1}},
}

// initializeMongo makes the connection to the MongoDB, creates the database
// “gepris” with the collection “projects” and the index for the “number”
// field.  Everything of this that already exists is left as is.
func initializeMongo() (*mongo.Client, *mongo.Collection, error) {
	if client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI("mongodb://localhost")); err != nil {
		return nil, nil, err
	} else {
		coll := client.Database("gepris").Collection("projects")
		name, err := coll.Indexes().CreateOne(context.TODO(), indexModel)
		check(err)
		logger.Printf("INFO: Created index %v", name)
		return client, coll, nil
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
	mongoClient, coll, err := initializeMongo()
	check(err)
	defer func() { err := mongoClient.Disconnect(context.TODO()); check(err) }()
	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string, 10*numberProjectWorkers)
	projects := make(chan projecter)
	for i := 0; i < numberProjectWorkers; i++ {
		projectWorkersWaitGroup.Add(1)
		go func() {
			defer projectWorkersWaitGroup.Done()
			for projectNumber := range projectNumbers {
				if project, err := processProject(projectNumber); err != nil {
					logger.Printf("ERROR: While processing #%v: %v", projectNumber, err)
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
			update[0].Value.(bson.M)["lastModification"] = primitive.Timestamp{T: uint32(time.Now().Unix())}
			result, err := coll.UpdateOne(context.TODO(), filter, update, mongoOpts)
			check(err)
			if result.UpsertedCount != 1 {
				logger.Printf("ERROR: project was not written properly to MongoDB")
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
}
