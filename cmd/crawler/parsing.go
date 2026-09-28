package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var whitespaceRegexp = regexp.MustCompile(`[\p{Z}\n\r\t]+`)

// normalizeString replaces any whitespace characters sequence with a single
// space.  Moreover, it removes leading and trailing whitespace.
func normalizeString(s string) (result string) {
	result = whitespaceRegexp.ReplaceAllString(s, " ")
	result = strings.TrimSpace(result)
	return result
}

// mustAtoi converts a string to an int and panics if something goes wrong.  It
// is used for converting regexp submatched that I know for sure to be
// integers.
func mustAtoi(s string) (i int) {
	i, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return
}

var timespanRegex = regexp.MustCompile(`^von (\d\d\d\d) bis (\d\d\d\d)$|` +
	`^seit (\d\d\d\d)$|^in (\d\d\d\d)$|^Bis (\d\d\d\d)$`)

// parseTimespan returns start and end year for a project from the given GEPRIS
// funding period string (the “PRJ_FOERDERZEITRAUM” data field).  If any of
// start or end cannot be found in the string, 0 is used as the value.
func parseTimespan(timespan string) (start int, end int, err error) {
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
		return 0, 0, fmt.Errorf("FATAL: %v could not be matched", timespan)
	}
	return
}
