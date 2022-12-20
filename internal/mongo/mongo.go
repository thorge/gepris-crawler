package mongo

import (
	"context"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var MongoOpts = options.Replace().SetUpsert(true)

var indexModel = mongo.IndexModel{
	Keys: bson.D{{"number", 1}},
}

var mongoURI string

func init() {
	mongoURI = os.Getenv("MONGODB_HOST")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost"
	}
}

// InitializeMongo makes the connection to the MongoDB, creates the database
// “gepris” with the collection “projects” and the index for the “number”
// field.  Everything of this that already exists is left as is.
func InitializeMongo(ctx context.Context) (*mongo.Client, *mongo.Collection, error) {
	if client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI)); err != nil {
		return nil, nil, err
	} else {
		coll := client.Database("gepris").Collection("projects")
		_, err := coll.Indexes().CreateOne(ctx, indexModel)
		check(err)
		return client, coll, nil
	}
}

func UpdateLastModificationTimestamp(update bson.M) {
	update["lastModification"] = primitive.NewDateTimeFromTime(time.Now())
}
