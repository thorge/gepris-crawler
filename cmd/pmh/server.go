/*
pmh offers a web service so that GEPRIS data can be retrieved using OAI-PMH.
It takes its data from a MogoDB.

See the README file for further information.
*/
package main

import (
	"os"
	"strconv"

	tbr_errors "gitlab.com/bronger/tools/errors"
	"go.mongodb.org/mongo-driver/bson"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/options"
	"jugit.fz-juelich.de/fdm/pmh-server"
)

func main() {
	pmh.Serve(pmh.Options{
		MongoOpts:  options.MongoOpts,
		Name:       "GEPRIS – project database of the Deutsche Forschungsgemeinschaft (DFG); UNOFFICIAL",
		URL:        "https://gepris.its.kfa-juelich.de",
		AdminEmail: "juser@fz-juelich.de",
		Filter: bson.D{{"$or", bson.A{
			bson.D{{"endYear", bson.D{{"$gte", sinceEndYear}}}},
			bson.D{{"endYear", bson.D{{"$eq", 0}}}},
			bson.D{{"latestChildEndYear", bson.D{{"$gte", sinceEndYear}}}},
		}}},
		MetadataFormats: []pmh.MetadataFormat{
			{
				Prefix:    "oai_dc",
				Schema:    "http://www.openarchives.org/OAI/2.0/oai_dc.xsd",
				Namespace: "http://www.openarchives.org/OAI/2.0/oai_dc/",
				BuildFunc: buildGrantDC,
			},
			{
				Prefix:    "marcxml",
				Schema:    "https://www.loc.gov/standards/marcxml/schema/MARC21slim.xsd",
				Namespace: "http://www.loc.gov/MARC21/slim",
				BuildFunc: buildGrant,
			},
		},
	})
}

var sinceEndYear int

func init() {
	if endYearRaw := os.Getenv("GEPRIS_PMH_SINCE_END_YEAR"); endYearRaw != "" {
		var err error
		sinceEndYear, err = strconv.Atoi(endYearRaw)
		tbr_errors.ExitOnExpectedError(err, "Invalid value of GEPRIS_PMH_SINCE_END_YEAR", 10)
	}
}
