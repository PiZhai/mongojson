package mongoreview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCredentialCipherRoundTrip(t *testing.T) {
	cipher, err := newCredentialCipher("test-only-secret")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("mongodb://readonly:password@localhost:27017")
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == "mongodb://readonly:password@localhost:27017" {
		t.Fatal("credential was stored as plaintext")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "mongodb://readonly:password@localhost:27017" {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestEnvironmentConnectionTestValidatesDraftInputBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}

	err := service.TestEnvironment(context.Background(), "unknown", EnvironmentInput{})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsupported environment error = %v", err)
	}

	err = service.TestEnvironment(context.Background(), "test", EnvironmentInput{
		ConnectionURI: "mongodb://127.0.0.1:27017",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("partial draft error = %v", err)
	}
}

func TestValidateMongoQueryRejectsExecutableOperators(t *testing.T) {
	for _, operator := range []string{"$where", "$function", "$accumulator", "$expr"} {
		t.Run(operator, func(t *testing.T) {
			err := validateMongoQuery(bson.M{operator: "unsafe"}, 0)
			if err == nil {
				t.Fatalf("%s should be rejected", operator)
			}
		})
	}
	if err := validateMongoQuery(bson.M{
		"enabled": true,
		"$or": bson.A{
			bson.M{"count": bson.M{"$gte": 2}},
			bson.M{"code": bson.M{"$in": bson.A{"A", "B"}}},
		},
	}, 0); err != nil {
		t.Fatalf("safe query rejected: %v", err)
	}
}

func TestBuildRuleFilterUsesCompositeFields(t *testing.T) {
	document := json.RawMessage(`{"code":"A","tenant":{"id":{"$numberInt":"7"}}}`)
	filter, err := buildRuleFilter(document, QueryRule{
		Name: "activity key", Collection: "activity",
		FieldMappings: []FieldMapping{
			{DocumentPath: "code", QueryField: "code"},
			{DocumentPath: "tenant.id", QueryField: "tenantId"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filter["code"] != "A" {
		t.Fatalf("code = %#v", filter["code"])
	}
	if value, ok := filter["tenantId"].(int32); !ok || value != 7 {
		t.Fatalf("tenantId = %#v", filter["tenantId"])
	}
}

func TestCompareDocumentsPreservesBSONTypeDifferences(t *testing.T) {
	differences := compareDocuments(
		json.RawMessage(`{"count":{"$numberInt":"1"}}`),
		json.RawMessage(`{"count":{"$numberLong":"1"}}`),
	)
	if len(differences) != 1 || differences[0].Path != "$.count" {
		t.Fatalf("differences = %#v", differences)
	}
}

func TestRepositoryReadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("db.getCollection(\"x\").deleteMany({})"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.js")); err != nil {
		t.Fatal(err)
	}
	service := &Service{repositoryRoot: root}
	if _, err := service.ReadRepositoryFile("escape.js"); err == nil {
		t.Fatal("symlink escape should be rejected")
	}
}

func TestRepositoryIndexGroupsTaskPrefixAcrossProjects(t *testing.T) {
	root := t.TempDir()
	for _, relative := range []string{
		"modules/mastercard_b_plus/MCC-15165@cmecvp/prod/a.js",
		"modules/super-common/MCC-15165@cmecvp/prod/b.js",
		"modules/bonus/MCC-15094@hainan/test/c.js",
	} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`db.getCollection("x").insertOne({code:"A"})`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{repositoryRoot: root}
	projects, tasks, err := service.ListRepositoryIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 3 {
		t.Fatalf("projects = %#v", projects)
	}
	var grouped *RepositoryTaskSummary
	for index := range tasks {
		if tasks[index].Key == "MCC-15165" {
			grouped = &tasks[index]
			break
		}
	}
	if grouped == nil || grouped.FileCount != 2 || len(grouped.Locations) != 2 {
		t.Fatalf("grouped task = %#v", grouped)
	}
}

func TestCreateRepositoryFileIsScopedAndDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "modules", "bonus"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := &Service{repositoryRoot: root}
	input := CreateRepositoryFileInput{
		Project: "bonus", TaskFolder: "MCC-15200@new", FilePath: "test/activity.js",
		Source: `db.getCollection("activity").insertOne({code:"A"})`,
	}
	created, err := service.CreateRepositoryFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != "modules/bonus/MCC-15200@new/test/activity.js" {
		t.Fatalf("created path = %q", created.Path)
	}
	if _, err := service.CreateRepositoryFile(input); err == nil {
		t.Fatal("existing repository file should not be overwritten")
	}
	input.FilePath = "../escape.js"
	if _, err := service.CreateRepositoryFile(input); err == nil {
		t.Fatal("path traversal should be rejected")
	}
}

func TestGetRepositoryTaskReturnsIndependentStatements(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "modules", "bonus", "MCC-15200@new", "prod", "activity.js")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("first\nsecond"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/parse" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"operations":[
			{"id":"op-0","type":"insertOne","collection":"activity","queryable":true,"description":"","source":"first","contextSource":"const code=\"A\"\n\nfirst","range":{},"arguments":[],"unresolvedPaths":[],"diagnostics":[]},
			{"id":"op-6","type":"deleteOne","collection":"activity","queryable":true,"description":"","source":"second","range":{},"arguments":[],"unresolvedPaths":[],"diagnostics":[]}
		],"diagnostics":[]}`))
	}))
	defer server.Close()
	service := &Service{repositoryRoot: root, analyzer: NewAnalyzerClient(server.URL)}
	task, err := service.GetRepositoryTask(context.Background(), "mcc-15200")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Files) != 1 || len(task.Files[0].Statements) != 2 {
		t.Fatalf("task = %#v", task)
	}
	if !strings.Contains(task.Files[0].Statements[0].Source, "const code") {
		t.Fatalf("statement source = %q", task.Files[0].Statements[0].Source)
	}
}
