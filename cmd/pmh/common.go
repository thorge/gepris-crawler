package main

import "time"

// grantDocument holds the union set of all fields of the different grant types
// (personal, institutional, umbrella).  Theoretically, the set is then limited
// to the fields relevant for MARCXML output but I haven’t done this yet.  The
// field names must match those in MongoDB but the first letter needs to be
// capitalised to allow reflection and thus unmarshalling of the MongoDB
// driver.
type grantDocument struct {
	Identifier         string
	Title              string
	ShortTitle         string
	Disciplines        []string
	StartYear, EndYear int
	Process            string
	Description        string
	Type               string
	Applicants         []string
	Speakers           []string
	Institutions       []string
	Parent             string
	ParentTitle        string
	LastModification   time.Time
}
