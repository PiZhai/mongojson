package domain

import "time"

type StewardToolHostStatus struct {
	Name      string    `json:"name"`
	Target    string    `json:"target"`
	Transport string    `json:"transport"`
	Online    bool      `json:"online"`
	Summary   string    `json:"summary,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type StewardTool struct {
	Name               string                  `json:"name"`
	Title              string                  `json:"title"`
	Description        string                  `json:"description"`
	Origin             string                  `json:"origin"`
	Enabled            bool                    `json:"enabled"`
	ActiveVersion      string                  `json:"active_version"`
	ExecutionTarget    string                  `json:"execution_target"`
	HealthStatus       string                  `json:"health_status"`
	HealthSummary      string                  `json:"health_summary,omitempty"`
	CatalogGeneration  int64                   `json:"catalog_generation"`
	CreatedByEpisodeID string                  `json:"created_by_episode_id,omitempty"`
	CreatedByTurnID    string                  `json:"created_by_turn_id,omitempty"`
	CreatedByModel     string                  `json:"created_by_model,omitempty"`
	InvocationCount    int64                   `json:"invocation_count"`
	LastUsedAt         *time.Time              `json:"last_used_at,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
	Active             *StewardToolVersion     `json:"active,omitempty"`
	Versions           []StewardToolVersion    `json:"versions,omitempty"`
	RecentTests        []StewardToolTestRun    `json:"recent_tests,omitempty"`
	DependencyChanges  []StewardToolDependency `json:"dependency_changes,omitempty"`
}

type StewardToolVersion struct {
	ToolName          string         `json:"tool_name"`
	Version           string         `json:"version"`
	Runtime           string         `json:"runtime"`
	Status            string         `json:"status"`
	Manifest          map[string]any `json:"manifest"`
	PackagePath       string         `json:"package_path,omitempty"`
	ContentSHA256     string         `json:"content_sha256"`
	SBOM              map[string]any `json:"sbom"`
	Provenance        map[string]any `json:"provenance"`
	ValidationSummary string         `json:"validation_summary,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	ValidatedAt       *time.Time     `json:"validated_at,omitempty"`
}

type StewardToolTestRun struct {
	ID           string           `json:"id"`
	ToolName     string           `json:"tool_name"`
	ToolVersion  string           `json:"tool_version"`
	TestName     string           `json:"test_name"`
	Status       string           `json:"status"`
	Input        map[string]any   `json:"input"`
	Output       map[string]any   `json:"output"`
	ErrorSummary string           `json:"error_summary,omitempty"`
	Evidence     []map[string]any `json:"evidence"`
	StartedAt    time.Time        `json:"started_at"`
	CompletedAt  *time.Time       `json:"completed_at,omitempty"`
}

type StewardToolDependency struct {
	ID               string         `json:"id"`
	ToolName         string         `json:"tool_name"`
	ToolVersion      string         `json:"tool_version"`
	Ecosystem        string         `json:"ecosystem"`
	PackageName      string         `json:"package_name"`
	RequestedVersion string         `json:"requested_version,omitempty"`
	ResolvedVersion  string         `json:"resolved_version,omitempty"`
	InstallScope     string         `json:"install_scope"`
	Status           string         `json:"status"`
	Preexisting      bool           `json:"preexisting"`
	PreviousVersion  string         `json:"previous_version,omitempty"`
	InstallCommand   string         `json:"install_command,omitempty"`
	RollbackCommand  string         `json:"rollback_command,omitempty"`
	Evidence         map[string]any `json:"evidence"`
	CreatedAt        time.Time      `json:"created_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
}
