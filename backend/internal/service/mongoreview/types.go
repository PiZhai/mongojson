package mongoreview

import (
	"encoding/json"
	"time"
)

const (
	MaxScriptBytes = 4 << 20
	MaxDetailDocs  = 50
	MaxResultBytes = 2 << 20
	QueryTimeout   = 3 * time.Second
)

var ValidEnvironments = map[string]bool{
	"demo": true,
	"test": true,
	"stag": true,
	"prod": true,
}

type Environment struct {
	Environment  string    `json:"environment"`
	DatabaseName string    `json:"database_name"`
	Configured   bool      `json:"configured"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type EnvironmentInput struct {
	ConnectionURI string `json:"connection_uri"`
	DatabaseName  string `json:"database_name"`
}

type FieldMapping struct {
	DocumentPath string `json:"document_path"`
	QueryField   string `json:"query_field"`
}

type QueryRule struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Collection    string         `json:"collection"`
	FieldMappings []FieldMapping `json:"field_mappings"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type Script struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	Source                string            `json:"source"`
	OriginPath            *string           `json:"origin_path,omitempty"`
	Operations            []ParsedOperation `json:"operations,omitempty"`
	OperationDescriptions map[string]string `json:"operation_descriptions,omitempty"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

type Diagnostic struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
	Offset   int    `json:"offset,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

type SourceRange struct {
	Start       int `json:"start"`
	End         int `json:"end"`
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

type BulkChild struct {
	ID              string            `json:"id"`
	Index           int               `json:"index"`
	Type            string            `json:"type"`
	Arguments       []json.RawMessage `json:"arguments"`
	UnresolvedPaths []string          `json:"unresolvedPaths"`
	Queryable       bool              `json:"queryable"`
	Diagnostics     []Diagnostic      `json:"diagnostics"`
}

type ParsedOperation struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	Collection      string            `json:"collection"`
	Queryable       bool              `json:"queryable"`
	Description     string            `json:"description"`
	Source          string            `json:"source"`
	ContextSource   string            `json:"contextSource,omitempty"`
	Range           SourceRange       `json:"range"`
	Arguments       []json.RawMessage `json:"arguments"`
	UnresolvedPaths []string          `json:"unresolvedPaths"`
	Diagnostics     []Diagnostic      `json:"diagnostics"`
	BulkOrdered     bool              `json:"bulkOrdered,omitempty"`
	Children        []BulkChild       `json:"children,omitempty"`
}

type ParseResult struct {
	Operations  []ParsedOperation `json:"operations"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
}

type ReviewRequest struct {
	ScriptID     string            `json:"script_id"`
	Source       string            `json:"source"`
	Environments []string          `json:"environments"`
	RuleIDs      map[string]string `json:"rule_ids"`
}

type FieldDifference struct {
	Path     string          `json:"path"`
	Script   json.RawMessage `json:"script,omitempty"`
	Database json.RawMessage `json:"database,omitempty"`
	Kind     string          `json:"kind"`
}

type DocumentResult struct {
	Before         json.RawMessage   `json:"before,omitempty"`
	After          json.RawMessage   `json:"after,omitempty"`
	Differences    []FieldDifference `json:"differences,omitempty"`
	ModifiedPaths  []string          `json:"modified_paths,omitempty"`
	UncertainPaths []string          `json:"uncertain_paths,omitempty"`
}

type OperationResult struct {
	OperationID string           `json:"operation_id"`
	Environment string           `json:"environment"`
	Status      string           `json:"status"`
	Message     string           `json:"message,omitempty"`
	MatchCount  int64            `json:"match_count"`
	Truncated   bool             `json:"truncated"`
	Documents   []DocumentResult `json:"documents,omitempty"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
}

type Review struct {
	ID         string            `json:"id"`
	ScriptID   string            `json:"script_id,omitempty"`
	Status     string            `json:"status"`
	Parse      ParseResult       `json:"parse"`
	Results    []OperationResult `json:"results"`
	Error      string            `json:"error,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
}

type ReviewEvent struct {
	Sequence int64  `json:"sequence"`
	Type     string `json:"type"`
	Review   Review `json:"review"`
}

type RepositoryFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type RepositoryProject struct {
	Name        string   `json:"name"`
	TaskFolders []string `json:"task_folders"`
}

type RepositoryTaskLocation struct {
	Project    string `json:"project"`
	TaskFolder string `json:"task_folder"`
	FileCount  int    `json:"file_count"`
}

type RepositoryTaskSummary struct {
	Key       string                   `json:"key"`
	Locations []RepositoryTaskLocation `json:"locations"`
	FileCount int                      `json:"file_count"`
}

type RepositoryStatement struct {
	ID         string          `json:"id"`
	Index      int             `json:"index"`
	Source     string          `json:"source"`
	Operation  ParsedOperation `json:"operation"`
	Project    string          `json:"project"`
	TaskFolder string          `json:"task_folder"`
	FilePath   string          `json:"file_path"`
}

type RepositoryTaskFile struct {
	Path        string                `json:"path"`
	Project     string                `json:"project"`
	TaskFolder  string                `json:"task_folder"`
	Size        int64                 `json:"size"`
	ModifiedAt  time.Time             `json:"modified_at"`
	Statements  []RepositoryStatement `json:"statements"`
	Diagnostics []Diagnostic          `json:"diagnostics"`
}

type RepositoryTask struct {
	Key       string                   `json:"key"`
	Locations []RepositoryTaskLocation `json:"locations"`
	Files     []RepositoryTaskFile     `json:"files"`
}

type CreateRepositoryFileInput struct {
	Project    string `json:"project"`
	TaskFolder string `json:"task_folder"`
	FilePath   string `json:"file_path"`
	Source     string `json:"source"`
}

type SimulationResult struct {
	Before         json.RawMessage `json:"before"`
	After          json.RawMessage `json:"after"`
	ModifiedPaths  []string        `json:"modifiedPaths"`
	UncertainPaths []string        `json:"uncertainPaths"`
	Diagnostics    []Diagnostic    `json:"diagnostics"`
}
