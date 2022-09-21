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
		logger.Panic(e)
	}
}

// setupLogging sets up logging into a file.  The file is called
// “mail2web.log”, and the directory is taken from the environment variable
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

var ticker = make(chan time.Time, 50)

// https://gepris.dfg.de/gepris/OCTOPUS?context=projekt&findButton=historyCall&hitsPerPage=50&index=50&task=doKatalog

var ErrNoFurtherResults = errors.New("no further search results")
var searchResultRegexp = regexp.MustCompile(`<a class="intern" href="/gepris/projekt/(?P<projectNumber>\d+)">mehr</a>`)
var client http.Client

func fetchPage(url string) (string, error) {
	logger.Printf("fetching %v", url)
	var body string
	triesLeft := triesLeft
	for {
		<-ticker
		resp, err := client.Get(url)
		if err == nil {
			body_raw, err := ioutil.ReadAll(resp.Body)
			check(err)
			must.Close(resp.Body)
			body = string(body_raw)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				break
			} else {
				logger.Printf("The HTTP request for %v failed with status code %v: %v",
					url, resp.StatusCode, body)
				if resp.StatusCode < 500 || resp.StatusCode >= 600 {
					return "", fmt.Errorf("Giving up on %v.", url)
				}
			}
		} else {
			logger.Printf("The HTTP request for %v failed without status code: %v", url, err)
		}
		triesLeft--
		if triesLeft == 0 {
			return "", fmt.Errorf("Giving up on %v.", url)
		}
		time.Sleep(time.Duration(rand.Int31n(maxWaitMillisec)) * time.Millisecond)
	}
	return body, nil
}

func getSearchResults(index int, projectNumbers chan<- string) error {
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/OCTOPUS?context=projekt&findButton=historyCall&"+
		"hitsPerPage=%v&index=%v&task=doKatalog", resultsPerPage, index)
	if body, err := fetchPage(url); err != nil {
		return err
	} else {
		if strings.Contains(body, `<p class="warning">Ihre Suche ergab leider keine Treffer.</p>`) {
			return ErrNoFurtherResults
		} else {
			for _, matches := range searchResultRegexp.FindAllStringSubmatch(body, -1) {
				projectNumbers <- matches[1]
			}
		}
	}
	return nil
}

func init() {
	var transport = &http.Transport{
		// Dial is the original function, plus a line in the log.  This way,
		// one can see how many HTTP connections are established.
		Dial: func(network, addr string) (net.Conn, error) {
			logger.Println("Dial!")
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
				logger.Println("Workers slower than rate limiter")
			}
		}
	}()
}

type projectBase struct {
	number             string
	title              string
	startYear, endYear int
	discipline         string
	url                string
	description        string
}

type projectPerson struct {
	projectBase
	applicant    string
	coApplicants []string
}

type projectInstitution struct {
	projectBase
	institution      string
	coInstitutions   []string
	partInstitutions []string
	speaker          string
	coSpeakers       []string
	participants     []string
}

func (project projectBase) updateMongo() (bson.D, bson.D, error) {
	filter := bson.D{{"number", project.number}}
	update := bson.D{{"$set", bson.M{"number": project.number}}}
	return filter, update, nil
}

func (project projectPerson) updateMongo() (bson.D, bson.D, error) {
	filter, update, err := project.projectBase.updateMongo()
	if err != nil {
		return nil, nil, err
	}
	document := update[0].Value.(bson.M)
	document["applicant"] = project.applicant
	document["coApplicants"] = project.coApplicants
	return filter, update, nil
}

func (project projectInstitution) updateMongo() (bson.D, bson.D, error) {
	filter, update, err := project.projectBase.updateMongo()
	if err != nil {
		return nil, nil, err
	}
	document := update[0].Value.(bson.M)
	document["institution"] = project.institution
	document["coInstitutions"] = project.coInstitutions
	document["partInstitutions"] = project.partInstitutions
	document["speaker"] = project.speaker
	document["coSpeakers"] = project.coSpeakers
	document["participants"] = project.participants
	return filter, update, nil
}

type projecter interface {
	updateMongo() (bson.D, bson.D, error)
	scanDocument(doc *goquery.Document) error
}

func (project *projectBase) scanDocument(doc *goquery.Document) error {
	var err error
	project.title, err = getField(doc.Find("div.details > h1"))
	check(err)
	if project.title == "" {
		return fmt.Errorf("project has no title")
	}
	project.discipline, err = getField(doc.Find(`span.name:contains("Fachliche Zuordnung")`).Next())
	check(err)
	if project.discipline == "" {
		return fmt.Errorf("project has no discipline")
	}
	project.startYear, project.endYear, err = parseTimespan(doc)
	check(err)
	project.description, err = getField(doc.Find("div#projekttext"))
	check(err)
	if project.description == "" {
		return fmt.Errorf("project has no description")
	}
	return nil
}

func (project *projectPerson) scanDocument(doc *goquery.Document) error {
	project.projectBase.scanDocument(doc)
	return fmt.Errorf("NotImplementedError")
}

func (project *projectInstitution) scanDocument(doc *goquery.Document) error {
	project.projectBase.scanDocument(doc)
	return fmt.Errorf("NotImplementedError")
}

var whitespaceRegexp = regexp.MustCompile(`[\p{Z}\n\r\t]+`)

func normalizeString(s string) (result string) {
	result = whitespaceRegexp.ReplaceAllString(s, " ")
	result = strings.TrimSpace(result)
	return result
}

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
	`Förderung seit (\d\d\d\d)|Förderung in (\d\d\d\d)`)

func mustAtoi(s string) (i int) {
	i, err := strconv.Atoi(s)
	if err != nil {
		logger.Panic(err)
	}
	return
}

func parseTimespan(doc *goquery.Document) (start int, end int, err error) {
	timespan, err := getField(doc.Find(`span.name:contains("Förderung")`).Next())
	if err != nil {
		return start, end, err
	}
	matches := timespanRegex.FindStringSubmatch(timespan)
	if len(matches) != 0 {
		if matches[4] != "" {
			start = mustAtoi(matches[4])
			end = start
		} else if matches[3] != "" {
			start = mustAtoi(matches[3])
		} else {
			start = mustAtoi(matches[1])
			end = mustAtoi(matches[2])
		}
	} else {
		logger.Panic(timespan, "could not be matched")
	}
	return
}

func processProject(projectNumber string) (result projecter, err error) {
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/projekt/%v", projectNumber)
	var body string
	if body, err = fetchPage(url); err != nil {
		return nil, err
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

func initializeMongo() (*mongo.Collection, error) {
	if client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI("mongodb://localhost")); err != nil {
		return nil, err
	} else {
		return client.Database("gepris").Collection("projects"), nil
	}
}

func main() {
	coll, err := initializeMongo()
	check(err)
	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string)
	projects := make(chan projecter)
	for i := 0; i < numberProjectWorkers; i++ {
		projectWorkersWaitGroup.Add(1)
		go func() {
			defer projectWorkersWaitGroup.Done()
			for projectNumber := range projectNumbers {
				if project, err := processProject(projectNumber); err != nil {
					logger.Printf("Error while processing #%v: %v", projectNumber, err)
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
			filter, update, err := project.updateMongo()
			check(err)
			result, err := coll.UpdateOne(context.TODO(), filter, update, mongoOpts)
			check(err)
			if result.UpsertedCount != 1 {
				logger.Printf("project was not written properly to MongoDB")
			}
		}
	}()
	index := 3000
	for {
		err := getSearchResults(index, projectNumbers)
		if errors.Is(err, ErrNoFurtherResults) {
			break
		} else if err != nil {
			logger.Println(err)
		}
		index += resultsPerPage
		break
	}
	close(projectNumbers)
	projectWorkersWaitGroup.Wait()
	close(projects)
	projectStorageWaitGroup.Wait()
}
