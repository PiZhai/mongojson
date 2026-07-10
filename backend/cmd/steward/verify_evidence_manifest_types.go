package main

import (
	"encoding/json"
	"time"
)

type evidenceManifestOptions struct {
	Dir                                     string        `json:"dir"`
	Output                                  string        `json:"output,omitempty"`
	Preset                                  string        `json:"preset,omitempty"`
	PresetApplied                           bool          `json:"-"`
	RequirePassing                          bool          `json:"require_passing"`
	RequireKinds                            []string      `json:"require_kinds,omitempty"`
	RequirePlatforms                        []string      `json:"require_platforms,omitempty"`
	RequireAgentIDs                         []string      `json:"require_agent_ids,omitempty"`
	RequireKindPlatforms                    []string      `json:"require_kind_platforms,omitempty"`
	RequirePlatformAgents                   []string      `json:"require_platform_agents,omitempty"`
	RequireKindPlatformAgents               []string      `json:"require_kind_platform_agents,omitempty"`
	RequireServiceScopes                    []string      `json:"require_service_scopes,omitempty"`
	RequirePlatformServiceScopes            []string      `json:"require_platform_service_scopes,omitempty"`
	RequireKindPlatformServiceScopes        []string      `json:"require_kind_platform_service_scopes,omitempty"`
	RequireServiceNames                     []string      `json:"require_service_names,omitempty"`
	RequirePlatformServiceNames             []string      `json:"require_platform_service_names,omitempty"`
	RequireKindPlatformServiceNames         []string      `json:"require_kind_platform_service_names,omitempty"`
	RequireAdvisorProviders                 []string      `json:"require_advisor_providers,omitempty"`
	RequirePlatformAdvisorProviders         []string      `json:"require_platform_advisor_providers,omitempty"`
	RequireKindPlatformAdvisorProviders     []string      `json:"require_kind_platform_advisor_providers,omitempty"`
	RequireAdvisorModels                    []string      `json:"require_advisor_models,omitempty"`
	RequirePlatformAdvisorModels            []string      `json:"require_platform_advisor_models,omitempty"`
	RequireKindPlatformAdvisorModels        []string      `json:"require_kind_platform_advisor_models,omitempty"`
	RequireAdvisorMaxDataLevels             []string      `json:"require_advisor_max_data_levels,omitempty"`
	RequirePlatformAdvisorMaxDataLevels     []string      `json:"require_platform_advisor_max_data_levels,omitempty"`
	RequireKindPlatformAdvisorMaxDataLevels []string      `json:"require_kind_platform_advisor_max_data_levels,omitempty"`
	RequireChecks                           []string      `json:"require_checks,omitempty"`
	RequireCheckPlatforms                   []string      `json:"require_check_platforms,omitempty"`
	RequireKindCheckPlatforms               []string      `json:"require_kind_check_platforms,omitempty"`
	LatestPerKind                           bool          `json:"latest_per_kind"`
	MinWatchDuration                        time.Duration `json:"min_watch_duration"`
	MinWatchDurationPerPlatform             bool          `json:"min_watch_duration_per_platform"`
}

const (
	evidenceManifestPresetS3S4Final       = "s3s4-final"
	evidenceManifestPresetS3S4FinalSystem = "s3s4-final-system"
	s3s4FinalMinWatchDuration             = 24 * time.Hour
)

type verificationEvidenceManifest struct {
	OK          bool                              `json:"ok"`
	Directory   string                            `json:"directory"`
	GeneratedAt time.Time                         `json:"generated_at"`
	Options     evidenceManifestOptions           `json:"options"`
	Coverage    verificationEvidenceCoverage      `json:"coverage"`
	Files       []verificationEvidenceFileSummary `json:"files"`
	Checks      []runtimeVerificationCheck        `json:"checks"`
}

type verificationEvidenceCoverage struct {
	TotalFiles                       int               `json:"total_files"`
	PassingFiles                     int               `json:"passing_files"`
	FailingFiles                     int               `json:"failing_files"`
	Kinds                            []string          `json:"kinds"`
	Platforms                        []string          `json:"platforms"`
	AgentIDs                         []string          `json:"agent_ids,omitempty"`
	ServiceScopes                    []string          `json:"service_scopes,omitempty"`
	ServiceNames                     []string          `json:"service_names,omitempty"`
	KindPlatforms                    []string          `json:"kind_platforms"`
	PlatformAgents                   []string          `json:"platform_agents,omitempty"`
	KindPlatformAgents               []string          `json:"kind_platform_agents,omitempty"`
	PlatformServiceScopes            []string          `json:"platform_service_scopes,omitempty"`
	KindPlatformServiceScopes        []string          `json:"kind_platform_service_scopes,omitempty"`
	PlatformServiceNames             []string          `json:"platform_service_names,omitempty"`
	KindPlatformServiceNames         []string          `json:"kind_platform_service_names,omitempty"`
	AdvisorProviders                 []string          `json:"advisor_providers,omitempty"`
	PlatformAdvisorProviders         []string          `json:"platform_advisor_providers,omitempty"`
	KindPlatformAdvisorProviders     []string          `json:"kind_platform_advisor_providers,omitempty"`
	AdvisorModels                    []string          `json:"advisor_models,omitempty"`
	PlatformAdvisorModels            []string          `json:"platform_advisor_models,omitempty"`
	KindPlatformAdvisorModels        []string          `json:"kind_platform_advisor_models,omitempty"`
	AdvisorMaxDataLevels             []string          `json:"advisor_max_data_levels,omitempty"`
	PlatformAdvisorMaxDataLevels     []string          `json:"platform_advisor_max_data_levels,omitempty"`
	KindPlatformAdvisorMaxDataLevels []string          `json:"kind_platform_advisor_max_data_levels,omitempty"`
	Checks                           []string          `json:"checks"`
	PassingChecks                    []string          `json:"passing_checks"`
	FailingChecks                    []string          `json:"failing_checks"`
	CheckPlatforms                   []string          `json:"check_platforms,omitempty"`
	PassingCheckPlatforms            []string          `json:"passing_check_platforms,omitempty"`
	FailingCheckPlatforms            []string          `json:"failing_check_platforms,omitempty"`
	KindCheckPlatforms               []string          `json:"kind_check_platforms,omitempty"`
	PassingKindCheckPlatforms        []string          `json:"passing_kind_check_platforms,omitempty"`
	FailingKindCheckPlatforms        []string          `json:"failing_kind_check_platforms,omitempty"`
	MaxWatchSamples                  int               `json:"max_watch_samples"`
	MaxWatchSpan                     string            `json:"max_watch_span"`
	MaxWatchSpanMillis               int64             `json:"max_watch_span_ms"`
	PlatformMaxWatchSpans            map[string]string `json:"platform_max_watch_spans,omitempty"`
	PlatformMaxWatchSpanMillis       map[string]int64  `json:"platform_max_watch_span_ms,omitempty"`
}

type verificationEvidenceFileSummary struct {
	Path                             string    `json:"path"`
	Kind                             string    `json:"kind,omitempty"`
	OK                               bool      `json:"ok"`
	CreatedAt                        time.Time `json:"created_at,omitempty"`
	Command                          []string  `json:"command,omitempty"`
	AgentIDs                         []string  `json:"agent_ids,omitempty"`
	ServiceScopes                    []string  `json:"service_scopes,omitempty"`
	ServiceNames                     []string  `json:"service_names,omitempty"`
	Platforms                        []string  `json:"platforms,omitempty"`
	PlatformAgents                   []string  `json:"platform_agents,omitempty"`
	KindPlatformAgents               []string  `json:"kind_platform_agents,omitempty"`
	PlatformServiceScopes            []string  `json:"platform_service_scopes,omitempty"`
	KindPlatformServiceScopes        []string  `json:"kind_platform_service_scopes,omitempty"`
	PlatformServiceNames             []string  `json:"platform_service_names,omitempty"`
	KindPlatformServiceNames         []string  `json:"kind_platform_service_names,omitempty"`
	AdvisorProviders                 []string  `json:"advisor_providers,omitempty"`
	PlatformAdvisorProviders         []string  `json:"platform_advisor_providers,omitempty"`
	KindPlatformAdvisorProviders     []string  `json:"kind_platform_advisor_providers,omitempty"`
	AdvisorModels                    []string  `json:"advisor_models,omitempty"`
	PlatformAdvisorModels            []string  `json:"platform_advisor_models,omitempty"`
	KindPlatformAdvisorModels        []string  `json:"kind_platform_advisor_models,omitempty"`
	AdvisorMaxDataLevels             []string  `json:"advisor_max_data_levels,omitempty"`
	PlatformAdvisorMaxDataLevels     []string  `json:"platform_advisor_max_data_levels,omitempty"`
	KindPlatformAdvisorMaxDataLevels []string  `json:"kind_platform_advisor_max_data_levels,omitempty"`
	Checks                           []string  `json:"checks,omitempty"`
	PassingChecks                    []string  `json:"passing_checks,omitempty"`
	FailingChecks                    []string  `json:"failing_checks,omitempty"`
	CheckPlatforms                   []string  `json:"check_platforms,omitempty"`
	PassingCheckPlatforms            []string  `json:"passing_check_platforms,omitempty"`
	FailingCheckPlatforms            []string  `json:"failing_check_platforms,omitempty"`
	KindCheckPlatforms               []string  `json:"kind_check_platforms,omitempty"`
	PassingKindCheckPlatforms        []string  `json:"passing_kind_check_platforms,omitempty"`
	FailingKindCheckPlatforms        []string  `json:"failing_kind_check_platforms,omitempty"`
	WatchSamples                     int       `json:"watch_samples,omitempty"`
	WatchSpan                        string    `json:"watch_span,omitempty"`
	WatchSpanMillis                  int64     `json:"watch_span_ms,omitempty"`
	Error                            string    `json:"error,omitempty"`
}

type rawVerificationEvidenceEnvelope struct {
	Kind      string          `json:"kind"`
	OK        bool            `json:"ok"`
	Command   []string        `json:"command,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}
