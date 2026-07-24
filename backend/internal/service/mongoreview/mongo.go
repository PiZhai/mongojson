package mongoreview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type readOnlyMongo struct {
	client   *mongo.Client
	database string
}

func connectReadOnlyMongo(ctx context.Context, uri, databaseName string) (*readOnlyMongo, error) {
	client, err := mongo.Connect(
		options.Client().
			ApplyURI(uri).
			SetConnectTimeout(QueryTimeout).
			SetServerSelectionTimeout(QueryTimeout),
	)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.PrimaryPreferred()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return &readOnlyMongo{client: client, database: databaseName}, nil
}

func (m *readOnlyMongo) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}

func decodeFilter(raw json.RawMessage) (bson.M, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("filter is empty")
	}
	var filter bson.M
	if err := bson.UnmarshalExtJSON(raw, true, &filter); err != nil {
		return nil, fmt.Errorf("invalid MongoDB filter: %w", err)
	}
	if err := validateMongoQuery(filter, 0); err != nil {
		return nil, err
	}
	return filter, nil
}

var allowedQueryOperators = map[string]bool{
	"$and": true, "$or": true, "$nor": true, "$not": true,
	"$eq": true, "$ne": true, "$gt": true, "$gte": true, "$lt": true, "$lte": true,
	"$in": true, "$nin": true, "$exists": true, "$type": true,
	"$all": true, "$elemMatch": true, "$size": true, "$regex": true, "$options": true,
	"$mod": true, "$bitsAllClear": true, "$bitsAllSet": true, "$bitsAnyClear": true, "$bitsAnySet": true,
}

func validateMongoQuery(value any, depth int) error {
	if depth > 16 {
		return fmt.Errorf("query nesting exceeds 16 levels")
	}
	switch typed := value.(type) {
	case bson.M:
		for key, child := range typed {
			if strings.HasPrefix(key, "$") && !allowedQueryOperators[key] {
				return fmt.Errorf("query operator %s is not allowed", key)
			}
			if !strings.HasPrefix(key, "$") && !validFieldPath(key) {
				return fmt.Errorf("invalid query field %q", key)
			}
			if err := validateMongoQuery(child, depth+1); err != nil {
				return err
			}
		}
	case bson.D:
		for _, element := range typed {
			if strings.HasPrefix(element.Key, "$") && !allowedQueryOperators[element.Key] {
				return fmt.Errorf("query operator %s is not allowed", element.Key)
			}
			if err := validateMongoQuery(element.Value, depth+1); err != nil {
				return err
			}
		}
	case bson.A:
		for _, child := range typed {
			if err := validateMongoQuery(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateMongoQuery(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validFieldPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "$") || strings.Contains(path, "\x00") {
		return false
	}
	parts := strings.Split(path, ".")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, "$") {
			return false
		}
	}
	return true
}

func (m *readOnlyMongo) FindAndCount(
	ctx context.Context,
	collection string,
	filter bson.M,
	limit int64,
) (int64, []json.RawMessage, bool, error) {
	if !validFieldPath(collection) {
		return 0, nil, false, fmt.Errorf("invalid collection name")
	}
	queryCtx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	coll := m.client.Database(m.database).Collection(collection)
	count, err := coll.CountDocuments(queryCtx, filter)
	if err != nil {
		return 0, nil, false, err
	}
	if count == 0 || limit == 0 {
		return count, nil, false, nil
	}
	cursor, err := coll.Find(
		queryCtx,
		filter,
		options.Find().SetLimit(limit),
	)
	if err != nil {
		return 0, nil, false, err
	}
	defer cursor.Close(queryCtx)
	var documents []json.RawMessage
	totalBytes := 0
	for cursor.Next(queryCtx) {
		var document bson.M
		if err := cursor.Decode(&document); err != nil {
			return 0, nil, false, err
		}
		encoded, err := bson.MarshalExtJSON(document, true, false)
		if err != nil {
			return 0, nil, false, err
		}
		if totalBytes+len(encoded) > MaxResultBytes {
			return count, documents, true, nil
		}
		totalBytes += len(encoded)
		documents = append(documents, encoded)
	}
	if err := cursor.Err(); err != nil {
		return 0, nil, false, err
	}
	return count, documents, count > int64(len(documents)), nil
}

func buildRuleFilter(document json.RawMessage, rule QueryRule) (bson.M, error) {
	var decoded bson.M
	if err := bson.UnmarshalExtJSON(document, true, &decoded); err != nil {
		return nil, fmt.Errorf("invalid insert document: %w", err)
	}
	filter := bson.M{}
	for _, mapping := range rule.FieldMappings {
		value, ok := lookupDocumentPath(decoded, mapping.DocumentPath)
		if !ok {
			return nil, fmt.Errorf("document does not contain query field %s", mapping.DocumentPath)
		}
		filter[mapping.QueryField] = value
	}
	return filter, validateMongoQuery(filter, 0)
}

func lookupDocumentPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		switch typed := current.(type) {
		case bson.M:
			current, _ = typed[part]
			if current == nil {
				_, exists := typed[part]
				if !exists {
					return nil, false
				}
			}
		case bson.D:
			found := false
			for _, element := range typed {
				if element.Key == part {
					current = element.Value
					found = true
					break
				}
			}
			if !found {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return current, true
}

func closeMongo(client *readOnlyMongo) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = client.Close(ctx)
}
