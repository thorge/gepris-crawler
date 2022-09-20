package main

import (
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

type project struct {
	number             string
	title              string
	startYear, endYear int
	discipline         string
	url                string
	description        string
	institution        string
	coInstitutions     []string
	speaker            string
	coSpeakers         []string
	participants       []string
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

func processProject(projectNumber string) (project, error) {
	var project project
	project.number = projectNumber
	url := fmt.Sprintf("https://gepris.dfg.de/gepris/projekt/%v", projectNumber)
	if body, err := fetchPage(url); err == nil {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		check(err)
		project.title, err = getField(doc.Find("div.details > h1"))
		check(err)
		if project.title == "" {
			return project, fmt.Errorf("%v has no title", projectNumber)
		}
		project.discipline, err = getField(doc.Find(`span.name:contains("Fachliche Zuordnung")`).Next())
		check(err)
		if project.discipline == "" {
			return project, fmt.Errorf("%v has no discipline", projectNumber)
		}
		project.startYear, project.endYear, err = parseTimespan(doc)
		check(err)
		project.description, err = getField(doc.Find("div#projekttext"))
		check(err)
		if project.description == "" {
			return project, fmt.Errorf("%v has no description", projectNumber)
		}
		logger.Println(project.title, "---", project.description)
	}
	return project, nil
}

func storeProject(project project) error {
	logger.Printf("Storage: Received project #%v", project.number)
	return nil
}

func main() {
	var projectWorkersWaitGroup sync.WaitGroup
	projectNumbers := make(chan string)
	projects := make(chan project)
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
			err := storeProject(project)
			check(err)
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
