package options

import (
	"os"

	"jugit.fz-juelich.de/fdm/pmh-server/mongo"
)

var MongoOpts mongo.Options

func init() {
	mongoURI := os.Getenv("MONGODB_HOST")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost"
	}
	MongoOpts = mongo.Options{
		Host:       mongoURI,
		DBName:     "gepris",
		CollName:   "projects",
		AddIndices: []string{"endYear"},
	}
}
