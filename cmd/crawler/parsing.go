package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func getDocument(body string) (doc *goquery.Document, err error) {
	doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
	return
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
