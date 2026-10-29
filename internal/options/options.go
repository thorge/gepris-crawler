package options

import (
	"os"

	"jugit.fz-juelich.de/fdm/pmh-server/mongo"
)

var MongoOpts mongo.Options

// analysisIndices are the fields of the projects collection that are used in
// typical analysis queries, e.g. “all projects of a person”.
var analysisIndices = []string{"startYear", "projectId", "parentId", "processKey", "subProcessKey",
	"personRoles.personId", "institutionRoles.institutionId", "researchSites.institutionId",
	"classifications.researchAreaKey", "classifications.reviewBoardKey", "classifications.subjectAreaKey"}

func init() {
	mongoURI := os.Getenv("MONGODB_HOST")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost"
	}
	MongoOpts = mongo.Options{
		Host:       mongoURI,
		DBName:     "gepris-new",
		CollName:   "projects",
		AddIndices: append([]string{"endYear"}, analysisIndices...),
	}
}
