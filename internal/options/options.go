package options

import (
	"jugit.fz-juelich.de/fdm/pmh-server/mongo"
)

var MongoOpts = mongo.Options{
	DBName:     "gepris",
	CollName:   "projects",
	AddIndices: []string{"endYear"},
}
