package mongo

import (
	"context"
	"os"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var MongoOpts = options.Replace().SetUpsert(true)

var indexModel = mongo.IndexModel{
	Keys:    bson.D{{"number", 1}},
	Options: options.Index().SetUnique(true),
}

var indexTokenModel = mongo.IndexModel{
	Keys:    bson.D{{"token", 1}},
	Options: options.Index().SetUnique(true),
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
		for _, fieldName := range []string{"endYear", "lastModification"} {
			_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{fieldName, 1}}})
			check(err)
		}
		return client, coll, nil
	}
}

// GetTokenColl returns the resumption tokens collection.  It also creates
// (idempotently) the index on the “tokens” field in that collection.
func GetTokenColl(ctx context.Context, client *mongo.Client) (coll *mongo.Collection, err error) {
	coll = client.Database("gepris").Collection("tokens")
	_, err = coll.Indexes().CreateOne(ctx, indexTokenModel)
	return
}
