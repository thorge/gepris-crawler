package main

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var mongoOpts = options.Update().SetUpsert(true)

var indexModel = mongo.IndexModel{
	Keys: bson.D{{"number", 1}},
}

// initializeMongo makes the connection to the MongoDB, creates the database
// “gepris” with the collection “projects” and the index for the “number”
// field.  Everything of this that already exists is left as is.
func initializeMongo() (*mongo.Client, *mongo.Collection, error) {
	if client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI("mongodb://localhost")); err != nil {
		return nil, nil, err
	} else {
		coll := client.Database("gepris").Collection("projects")
		name, err := coll.Indexes().CreateOne(context.TODO(), indexModel)
		check(err)
		logger.Printf("INFO: Created index %v", name)
		return client, coll, nil
	}
}

func updateLastModificationTimestamp(update bson.D) {
	update[0].Value.(bson.M)["lastModification"] = primitive.Timestamp{T: uint32(time.Now().Unix())}
}
