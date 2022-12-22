/*
expirerer deletes resumtion tokens that are older than their expiration date.
It should run regularly, e.g. once a day.

See the README file for further information.
*/
package main

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go4.org/must"
	"jugit.fz-juelich.de/fdm/gepris-crawler/internal/mongo"
)

func main() {
	mongoClient, _, err := mongo.InitializeMongo(context.Background())
	if err != nil {
		return
	}
	tokenColl, err := mongo.GetTokenColl(context.Background(), mongoClient)
	if err != nil {
		return
	}
	defer must.Do(func() error { return mongoClient.Disconnect(context.Background()) })
	if result, err := tokenColl.DeleteMany(context.Background(),
		bson.D{{"expirationDate", bson.D{{"$lte", time.Now()}}}}); err != nil {
		panic(err)
	} else {
		fmt.Println(result.DeletedCount, "tokens expired")
	}
}
