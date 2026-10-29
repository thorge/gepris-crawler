package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	mongodb "go.mongodb.org/mongo-driver/mongo"
	mongoopts "go.mongodb.org/mongo-driver/mongo/options"
)

// entityMaxAge is the time after which a person or institution is downloaded
// again from GEPRIS.  Before that, it is considered up to date.  This limits
// the burden on GEPRIS because persons and institutions occur in many
// projects, and their data rarely changes.
const entityMaxAge = 30 * 24 * time.Hour

// apiEntity is the subset of the GEPRIS “person/get” and “institution/get”
// API responses needed by the crawler.
type apiEntity struct {
	ID     int            `json:"id"`
	Data   map[string]any `json:"data"`
	Prj    apiGroups      `json:"prj"`
	Preise apiGroups      `json:"preise"`
}

// entityKind describes how a certain kind of GEPRIS entity other than
// projects (i.e. persons and institutions) is downloaded and stored.
type entityKind struct {
	// name is used for logging.
	name string
	// collName is the name of the MongoDB collection.
	collName string
	// idField is the name of the field in MongoDB holding the GEPRIS ID.
	idField string
	// urlFormat is the URL of the GEPRIS API with a placeholder for the ID.
	urlFormat string
	// cacheDir is the directory used for caching if CRAWLER_DEBUG is set.
	cacheDir string
	// fields returns the data fields extracted from the given API response.
	fields func(entity *apiEntity) bson.M
}

// projectRef is a project associated with a person or institution as listed
// by GEPRIS.  This list is complete, i.e. it also contains projects not (yet)
// crawled.  For persons, “role” is the role in the project.
type projectRef struct {
	ProjectID         int    `bson:"projectId"`
	Role              string `bson:"role,omitempty"`
	RoleLabel         string `bson:"roleLabel,omitempty"`
	ProcessKey        string `bson:"processKey"`
	Status            string `bson:"status"`
	StartYear         int    `bson:"startYear"`
	EndYear           int    `bson:"endYear"`
	IsTransferProject bool   `bson:"isTransferProject"`
}

// projectRefs returns all projects listed in the “prj” field of the given API
// response.
func projectRefs(entity *apiEntity) (result []projectRef) {
	for _, key := range entity.Prj.Order {
		for _, entry := range entity.Prj.Groups[key] {
			ref := projectRef{
				ProjectID:         entry.ID,
				Role:              stringFromMap(entry.Data, "BTR_KEY_GRUP"),
				RoleLabel:         stringFromMap(entry.Data, "BTR_BEZEICHNUNG"),
				ProcessKey:        stringFromMap(entry.Data, "PRJ_VERFAHREN_KEY"),
				Status:            stringFromMap(entry.Data, "PRJ_STATUS"),
				IsTransferProject: boolFromMap(entry.Data, "IS_TRANSFERPROJEKT"),
			}
			if ref.ProjectID == 0 {
				ref.ProjectID = intFromMap(entry.Data, "PRJ_ID")
			}
			ref.StartYear, ref.EndYear = parseYears(stringFromMap(entry.Data, "PRJ_FOERDERZEITRAUM"))
			result = append(result, ref)
		}
	}
	return result
}

// prize is a prize awarded by the DFG to a person, e.g. the Leibniz Prize.
type prize struct {
	PrizeKey string `bson:"prizeKey"`
	Name     string `bson:"name"`
	NameEn   string `bson:"nameEn"`
	Year     int    `bson:"year"`
}

// prizes returns all prizes listed in the “preise” field of the given API
// response.
func prizes(entity *apiEntity) (result []prize) {
	for _, key := range entity.Preise.Order {
		for _, entry := range entity.Preise.Groups[key] {
			year, _ := strconv.Atoi(fmt.Sprint(entry.Data["JAHR"]))
			result = append(result, prize{
				PrizeKey: stringFromMap(entry.Data, "PREIS_KEY"),
				Name:     normalizeString(stringFromMap(entry.Data, "PREIS_BEZEICHNUNG")),
				NameEn:   normalizeString(stringFromMap(entry.Data, "PREIS_BEZEICHNUNG_EN")),
				Year:     year,
			})
		}
	}
	return result
}

var personKind = entityKind{
	name:      "person",
	collName:  "persons",
	idField:   "personId",
	urlFormat: "https://gepris.dfg.de/backend/person/get?id=%v",
	cacheDir:  "person-pages",
	fields: func(entity *apiEntity) bson.M {
		return bson.M{
			"name":                  normalizeString(stringFromMap(entity.Data, "PER_NAME_KOMPLETT")),
			"deceased":              boolFromMap(entity.Data, "PER_VERSTORBEN"),
			"institutionId":         intFromMap(entity.Data, "INS_ID"),
			"institutionName":       normalizeString(stringFromMap(entity.Data, "INS_NAMEDEUTSCH")),
			"institutionNameEn":     normalizeString(stringFromMap(entity.Data, "INS_NAMEENGLISCH")),
			"rootInstitutionId":     intFromMap(entity.Data, "ROOT_ID"),
			"rootInstitutionName":   normalizeString(stringFromMap(entity.Data, "ROOT_NAMEDEUTSCH")),
			"rootInstitutionNameEn": normalizeString(stringFromMap(entity.Data, "ROOT_NAMEENGLISCH")),
			"country":               stringFromMap(entity.Data, "ANS_LAND"),
			"countryEn":             stringFromMap(entity.Data, "ANS_LAND_EN"),
			"prizes":                prizes(entity),
			"projects":              projectRefs(entity),
		}
	},
}

var institutionKind = entityKind{
	name:      "institution",
	collName:  "institutions",
	idField:   "institutionId",
	urlFormat: "https://gepris.dfg.de/backend/institution/get?id=%v",
	cacheDir:  "institution-pages",
	fields: func(entity *apiEntity) bson.M {
		return bson.M{
			"name":                  normalizeString(stringFromMap(entity.Data, "INS_NAMEDEUTSCH")),
			"nameEn":                normalizeString(stringFromMap(entity.Data, "INS_NAMEENGLISCH")),
			"postalName":            normalizeString(stringFromMap(entity.Data, "INS_NAMEPOSTANSCHRIFT")),
			"rootInstitutionId":     intFromMap(entity.Data, "ROOT_ID"),
			"rootInstitutionName":   normalizeString(stringFromMap(entity.Data, "ROOT_NAMEDEUTSCH")),
			"rootInstitutionNameEn": normalizeString(stringFromMap(entity.Data, "ROOT_NAMEENGLISCH")),
			"type":                  stringFromMap(entity.Data, "IA_TER_BEZEICHNUNG"),
			"typeEn":                stringFromMap(entity.Data, "IA_TER_BEZEICHNUNG_EN"),
			"typeKey":               stringFromMap(entity.Data, "IA_TER_KEY"),
			"sector":                stringFromMap(entity.Data, "IA_SKT_BEZEICHNUNG"),
			"sectorEn":              stringFromMap(entity.Data, "IA_SKT_BEZEICHNUNG_EN"),
			"sectorKey":             stringFromMap(entity.Data, "IA_SKT_KEY"),
			"street":                normalizeString(stringFromMap(entity.Data, "ANS_STRASSE")),
			"postalCodeCity":        normalizeString(stringFromMap(entity.Data, "ANS_PLZ_ORT")),
			"city":                  stringFromMap(entity.Data, "ANS_ORT"),
			"country":               stringFromMap(entity.Data, "ANS_LAND"),
			"countryEn":             stringFromMap(entity.Data, "ANS_LAND_EN"),
			"dissolved":             boolFromMap(entity.Data, "INS_AUFGELOEST"),
			"inGerit":               boolFromMap(entity.Data, "INS_GERIT"),
			"projects":              projectRefs(entity),
		}
	},
}

// initializeEntityCollection returns the MongoDB collection for the given
// entity kind and creates (idempotently) its indices.
func initializeEntityCollection(db *mongodb.Database, kind entityKind) (*mongodb.Collection, error) {
	coll := db.Collection(kind.collName)
	_, err := coll.Indexes().CreateMany(context.Background(), []mongodb.IndexModel{
		{Keys: bson.D{{kind.idField, 1}}, Options: mongoopts.Index().SetUnique(true)},
		{Keys: bson.D{{"projects.projectId", 1}}},
		{Keys: bson.D{{"rootInstitutionId", 1}}},
	})
	return coll, err
}

// entityReferences collects the GEPRIS IDs of persons and institutions found
// in the crawled projects.  It is not safe for concurrent use.
type entityReferences struct {
	personIDs, institutionIDs map[int]bool
}

func newEntityReferences() *entityReferences {
	return &entityReferences{personIDs: make(map[int]bool), institutionIDs: make(map[int]bool)}
}

// add adds the given IDs to the collection.
func (references *entityReferences) add(personIDs, institutionIDs []int) {
	for _, id := range personIDs {
		references.personIDs[id] = true
	}
	for _, id := range institutionIDs {
		references.institutionIDs[id] = true
	}
}

// crawlEntities downloads all entities of the given kind with the given IDs
// from GEPRIS and writes them to MongoDB.  Entities that have been downloaded
// within entityMaxAge are skipped.  It uses numberProjectWorkers parallel Go
// routines and blocks until all of them are finished.
func crawlEntities(coll *mongodb.Collection, kind entityKind, ids map[int]bool) {
	slog.Info("crawling referenced entities …", "kind", kind.name, "number", len(ids))
	var wg sync.WaitGroup
	idsChannel := make(chan int, 10*numberProjectWorkers)
	for range numberProjectWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range idsChannel {
				if err := processEntity(coll, kind, id); err != nil {
					slog.Error("Could not process entity", "kind", kind.name, "id", id, "error", err)
				}
			}
		}()
	}
	for id := range ids {
		idsChannel <- id
	}
	close(idsChannel)
	wg.Wait()
	slog.Info("finished crawling referenced entities", "kind", kind.name)
}

// processEntity downloads the entity of the given kind and ID from GEPRIS and
// writes it to MongoDB, unless it is still up to date (see entityMaxAge).  The
// “lastModification” field is only touched if the GEPRIS record has changed,
// whereas “lastFetched” is always updated.
func processEntity(coll *mongodb.Collection, kind entityKind, id int) error {
	var oldEntity struct {
		SourceHash  string    `bson:"sourceHash"`
		LastFetched time.Time `bson:"lastFetched"`
	}
	filter := bson.M{kind.idField: id}
	err := coll.FindOne(context.Background(), filter,
		mongoopts.FindOne().SetProjection(bson.M{"sourceHash": 1, "lastFetched": 1})).Decode(&oldEntity)
	if err == nil && time.Since(oldEntity.LastFetched) < entityMaxAge {
		return nil
	} else if err != nil && !errors.Is(err, mongodb.ErrNoDocuments) {
		return err
	}
	raw, err := fetchJSONCached(fmt.Sprintf(kind.urlFormat, id),
		fmt.Sprintf("%v/%v.json", kind.cacheDir, id))
	if err != nil {
		return err
	}
	var entity apiEntity
	if err := json.Unmarshal(raw, &entity); err != nil {
		return fmt.Errorf("could not parse JSON from GEPRIS: %v", err)
	}
	now := time.Now()
	sourceHash := hashRaw(raw)
	if sourceHash == oldEntity.SourceHash {
		_, err = coll.UpdateOne(context.Background(), filter, bson.M{"$set": bson.M{"lastFetched": now}})
		return err
	}
	document := kind.fields(&entity)
	document[kind.idField] = id
	document["sourceHash"] = sourceHash
	document["lastFetched"] = now
	document["lastModification"] = now
	_, err = coll.ReplaceOne(context.Background(), filter, document, optUpsert)
	return err
}
