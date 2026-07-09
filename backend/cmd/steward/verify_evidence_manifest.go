package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func buildVerificationEvidenceManifest(options evidenceManifestOptions) verificationEvidenceManifest {
	expandedOptions, presetErr := applyEvidenceManifestPreset(options)
	manifest := verificationEvidenceManifest{
		OK:          true,
		GeneratedAt: time.Now().UTC(),
		Options:     normalizeEvidenceManifestOptions(expandedOptions),
		Files:       []verificationEvidenceFileSummary{},
		Checks:      []runtimeVerificationCheck{},
	}
	if presetErr != nil {
		manifest.OK = false
		manifest.Directory = manifest.Options.Dir
		manifest.Checks = append(manifest.Checks, runtimeVerificationCheck{ID: "evidence.preset", Status: "error", Message: presetErr.Error()})
		return manifest
	}
	absDir, err := filepath.Abs(manifest.Options.Dir)
	if err != nil {
		manifest.OK = false
		manifest.Directory = manifest.Options.Dir
		manifest.Checks = append(manifest.Checks, runtimeVerificationCheck{ID: "evidence.dir", Status: "error", Message: err.Error()})
		return manifest
	}
	manifest.Directory = absDir
	paths, err := verificationEvidenceFiles(absDir)
	if err != nil {
		manifest.OK = false
		manifest.Checks = append(manifest.Checks, runtimeVerificationCheck{ID: "evidence.dir", Status: "error", Message: err.Error()})
		return manifest
	}
	for _, path := range paths {
		summary := summarizeVerificationEvidenceFile(path)
		if summary.Error != "" {
			manifest.OK = false
		}
		manifest.Files = append(manifest.Files, summary)
	}
	if manifest.Options.LatestPerKind {
		manifest.Files = latestVerificationEvidenceFilesPerKind(manifest.Files)
	}
	manifest.Coverage = verificationEvidenceCoverageFromFiles(manifest.Files)
	addEvidenceManifestChecks(&manifest)
	return manifest
}

func latestVerificationEvidenceFilesPerKind(files []verificationEvidenceFileSummary) []verificationEvidenceFileSummary {
	latestByKind := map[string]verificationEvidenceFileSummary{}
	passthrough := []verificationEvidenceFileSummary{}
	for _, file := range files {
		if file.Error != "" || strings.TrimSpace(file.Kind) == "" {
			passthrough = append(passthrough, file)
			continue
		}
		current, ok := latestByKind[file.Kind]
		if !ok || evidenceFileIsNewer(file, current) {
			latestByKind[file.Kind] = file
		}
	}
	latest := make([]verificationEvidenceFileSummary, 0, len(latestByKind))
	for _, file := range latestByKind {
		latest = append(latest, file)
	}
	sort.Slice(latest, func(i, j int) bool {
		if latest[i].CreatedAt.Equal(latest[j].CreatedAt) {
			return latest[i].Path < latest[j].Path
		}
		return latest[i].CreatedAt.Before(latest[j].CreatedAt)
	})
	out := append([]verificationEvidenceFileSummary{}, passthrough...)
	out = append(out, latest...)
	return out
}

func evidenceFileIsNewer(candidate verificationEvidenceFileSummary, current verificationEvidenceFileSummary) bool {
	if candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.Path > current.Path
	}
	if current.CreatedAt.IsZero() {
		return true
	}
	if candidate.CreatedAt.IsZero() {
		return false
	}
	return candidate.CreatedAt.After(current.CreatedAt)
}

func normalizeEvidenceManifestOptions(options evidenceManifestOptions) evidenceManifestOptions {
	options.Dir = strings.TrimSpace(options.Dir)
	if options.Dir == "" {
		options.Dir = "."
	}
	options.Output = strings.TrimSpace(options.Output)
	options.Preset = strings.ToLower(strings.TrimSpace(options.Preset))
	options.RequireKinds = normalizeStringSet(options.RequireKinds)
	options.RequirePlatforms = normalizeStringSet(options.RequirePlatforms)
	options.RequireAgentIDs = normalizeStringSet(options.RequireAgentIDs)
	options.RequireKindPlatforms = normalizeKindPlatformRequirements(options.RequireKindPlatforms)
	options.RequirePlatformAgents = normalizePlatformAgentRequirements(options.RequirePlatformAgents)
	options.RequireKindPlatformAgents = normalizeKindPlatformAgentRequirements(options.RequireKindPlatformAgents)
	options.RequireServiceScopes = normalizeStringSet(options.RequireServiceScopes)
	options.RequirePlatformServiceScopes = normalizePlatformServiceScopeRequirements(options.RequirePlatformServiceScopes)
	options.RequireKindPlatformServiceScopes = normalizeKindPlatformServiceScopeRequirements(options.RequireKindPlatformServiceScopes)
	options.RequireServiceNames = normalizeStringSet(options.RequireServiceNames)
	options.RequirePlatformServiceNames = normalizePlatformServiceNameRequirements(options.RequirePlatformServiceNames)
	options.RequireKindPlatformServiceNames = normalizeKindPlatformServiceNameRequirements(options.RequireKindPlatformServiceNames)
	options.RequireAdvisorProviders = normalizeStringSet(options.RequireAdvisorProviders)
	options.RequirePlatformAdvisorProviders = normalizePlatformAdvisorValueRequirements(options.RequirePlatformAdvisorProviders)
	options.RequireKindPlatformAdvisorProviders = normalizeKindPlatformAdvisorValueRequirements(options.RequireKindPlatformAdvisorProviders)
	options.RequireAdvisorModels = normalizeStringSet(options.RequireAdvisorModels)
	options.RequirePlatformAdvisorModels = normalizePlatformAdvisorValueRequirements(options.RequirePlatformAdvisorModels)
	options.RequireKindPlatformAdvisorModels = normalizeKindPlatformAdvisorValueRequirements(options.RequireKindPlatformAdvisorModels)
	options.RequireAdvisorMaxDataLevels = normalizeStringSet(options.RequireAdvisorMaxDataLevels)
	options.RequirePlatformAdvisorMaxDataLevels = normalizePlatformAdvisorValueRequirements(options.RequirePlatformAdvisorMaxDataLevels)
	options.RequireKindPlatformAdvisorMaxDataLevels = normalizeKindPlatformAdvisorValueRequirements(options.RequireKindPlatformAdvisorMaxDataLevels)
	options.RequireChecks = normalizeStringSet(options.RequireChecks)
	options.RequireCheckPlatforms = normalizeCheckPlatformRequirements(options.RequireCheckPlatforms)
	options.RequireKindCheckPlatforms = normalizeKindCheckPlatformRequirements(options.RequireKindCheckPlatforms)
	if options.MinWatchDuration < 0 {
		options.MinWatchDuration = 0
	}
	return options
}

func applyEvidenceManifestPreset(options evidenceManifestOptions) (evidenceManifestOptions, error) {
	preset := strings.ToLower(strings.TrimSpace(options.Preset))
	if preset == "" {
		return options, nil
	}
	options.Preset = preset
	if options.PresetApplied {
		return options, nil
	}
	switch preset {
	case evidenceManifestPresetS3S4Final:
		options = applyS3S4FinalEvidencePreset(options)
		options.PresetApplied = true
		return options, nil
	case evidenceManifestPresetS3S4FinalSystem:
		options = applyS3S4FinalEvidencePreset(options)
		options = applyS3S4FinalSystemServiceScopePreset(options)
		options.PresetApplied = true
		return options, nil
	default:
		return evidenceManifestOptions{}, fmt.Errorf("unknown evidence preset %q", preset)
	}
}

func applyS3S4FinalEvidencePreset(options evidenceManifestOptions) evidenceManifestOptions {
	platforms := []string{"windows", "darwin", "linux"}
	options.RequirePassing = true
	options.RequireKinds = append(options.RequireKinds, "service", "mesh", "s3s4-final-host")
	options.RequirePlatforms = append(options.RequirePlatforms, platforms...)
	for _, platform := range platforms {
		options.RequireKindPlatforms = append(options.RequireKindPlatforms, "service:"+platform, "mesh:"+platform, "s3s4-final-host:"+platform)
		for _, check := range s3s4FinalServiceChecks() {
			options.RequireKindCheckPlatforms = append(options.RequireKindCheckPlatforms, "service:"+check+":"+platform)
		}
		for _, check := range s3s4FinalMeshChecks() {
			options.RequireKindCheckPlatforms = append(options.RequireKindCheckPlatforms, "mesh:"+check+":"+platform)
		}
		for _, check := range s3s4FinalHostChecks() {
			options.RequireKindCheckPlatforms = append(options.RequireKindCheckPlatforms, "s3s4-final-host:"+check+":"+platform)
		}
	}
	if options.MinWatchDuration < s3s4FinalMinWatchDuration {
		options.MinWatchDuration = s3s4FinalMinWatchDuration
	}
	options.MinWatchDurationPerPlatform = true
	return options
}

func applyS3S4FinalSystemServiceScopePreset(options evidenceManifestOptions) evidenceManifestOptions {
	for _, platform := range []string{"windows", "darwin", "linux"} {
		options.RequireKindPlatformServiceScopes = append(options.RequireKindPlatformServiceScopes, "service:"+platform+":system")
	}
	return options
}

func s3s4FinalHostChecks() []string {
	return []string{
		"s3s4_final_host.binary",
		"s3s4_final_host.service",
		"s3s4_final_host.mesh",
		"s3s4_final_host.local_manifest",
	}
}

func s3s4FinalServiceChecks() []string {
	return []string{
		"service.status",
		"service.runtime",
		"service.watch",
		"service.watch.heartbeat",
		"s3.sync.security.strict",
		"s3.sync.security.expected_sync_key",
		"s3.sync.security.expected_local_key",
		"s4.autonomy.status",
		"s4.advisor.probe",
		"s4.advisor.privacy_probe",
	}
}

func s3s4FinalMeshChecks() []string {
	return []string{
		"mesh.watch",
		"mesh.watch.heartbeat",
		"s3.peers.present",
		"s3.peers.status",
		"s3.sync.security.strict",
		"s3.peer_probe.task",
		"s3.peer_probe.source_ref",
		"s3.peer_probe.data_tag",
		"s3.peer_probe.entity_tag",
		"s3.peer_probe.event",
		"s3.peer_probe.timeline_segment",
		"s3.peer_probe.relations",
		"s3.sync.security.expected_sync_key",
		"s3.sync.security.expected_local_key",
		"s4.autonomy.status",
		"s4.advisor.probe",
		"s4.advisor.privacy_probe",
	}
}

func verificationEvidenceFiles(root string) ([]string, error) {
	paths := []string{}
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "steward-verify-") && strings.HasSuffix(strings.ToLower(name), ".json") {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func summarizeVerificationEvidenceFile(path string) verificationEvidenceFileSummary {
	summary := verificationEvidenceFileSummary{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		summary.Error = err.Error()
		return summary
	}
	var envelope rawVerificationEvidenceEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		summary.Error = err.Error()
		return summary
	}
	if strings.TrimSpace(envelope.Kind) == "" || len(envelope.Payload) == 0 {
		summary.Error = "file is not a steward verification evidence envelope"
		return summary
	}
	var payload any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		summary.Error = err.Error()
		return summary
	}
	watchSamples, watchSpan := verificationEvidenceWatchStats(payload)
	checks, passingChecks, failingChecks := verificationEvidenceCheckStats(payload)
	checkPlatforms, passingCheckPlatforms, failingCheckPlatforms := verificationEvidenceCheckPlatformStats(payload)
	summary.Kind = sanitizeEvidenceName(envelope.Kind)
	summary.OK = envelope.OK
	summary.CreatedAt = envelope.CreatedAt
	summary.Command = envelope.Command
	summary.AgentIDs = normalizeStringSet(collectEvidenceStringValues(payload, "agent_id"))
	summary.Platforms = normalizeStringSet(collectEvidenceStringValues(payload, "platform"))
	summary.PlatformAgents = verificationEvidencePlatformAgentStats(payload)
	summary.KindPlatformAgents = withEvidenceKindPrefix(summary.Kind, summary.PlatformAgents)
	summary.ServiceScopes = verificationEvidenceServiceScopeStats(payload)
	summary.PlatformServiceScopes = verificationEvidencePlatformServiceScopeStats(payload)
	summary.KindPlatformServiceScopes = withEvidenceKindPlatformServiceScopePrefix(summary.Kind, summary.PlatformServiceScopes)
	summary.ServiceNames = verificationEvidenceServiceNameStats(payload)
	summary.PlatformServiceNames = verificationEvidencePlatformServiceNameStats(payload)
	summary.KindPlatformServiceNames = withEvidenceKindPlatformServiceNamePrefix(summary.Kind, summary.PlatformServiceNames)
	summary.AdvisorProviders = verificationEvidenceAdvisorFieldStats(payload, "provider")
	summary.PlatformAdvisorProviders = verificationEvidencePlatformAdvisorFieldStats(payload, "provider")
	summary.KindPlatformAdvisorProviders = withEvidenceKindPlatformAdvisorValuePrefix(summary.Kind, summary.PlatformAdvisorProviders)
	summary.AdvisorModels = verificationEvidenceAdvisorFieldStats(payload, "model")
	summary.PlatformAdvisorModels = verificationEvidencePlatformAdvisorFieldStats(payload, "model")
	summary.KindPlatformAdvisorModels = withEvidenceKindPlatformAdvisorValuePrefix(summary.Kind, summary.PlatformAdvisorModels)
	summary.AdvisorMaxDataLevels = verificationEvidenceAdvisorFieldStats(payload, "max_data_level")
	summary.PlatformAdvisorMaxDataLevels = verificationEvidencePlatformAdvisorFieldStats(payload, "max_data_level")
	summary.KindPlatformAdvisorMaxDataLevels = withEvidenceKindPlatformAdvisorValuePrefix(summary.Kind, summary.PlatformAdvisorMaxDataLevels)
	summary.Checks = checks
	summary.PassingChecks = passingChecks
	summary.FailingChecks = failingChecks
	summary.CheckPlatforms = checkPlatforms
	summary.PassingCheckPlatforms = passingCheckPlatforms
	summary.FailingCheckPlatforms = failingCheckPlatforms
	summary.KindCheckPlatforms = withEvidenceKindPrefix(summary.Kind, checkPlatforms)
	summary.PassingKindCheckPlatforms = withEvidenceKindPrefix(summary.Kind, passingCheckPlatforms)
	summary.FailingKindCheckPlatforms = withEvidenceKindPrefix(summary.Kind, failingCheckPlatforms)
	summary.WatchSamples = watchSamples
	summary.WatchSpan = watchSpan.String()
	summary.WatchSpanMillis = watchSpan.Milliseconds()
	return summary
}

func verificationEvidenceCoverageFromFiles(files []verificationEvidenceFileSummary) verificationEvidenceCoverage {
	kinds := []string{}
	platforms := []string{}
	agentIDs := []string{}
	serviceScopes := []string{}
	serviceNames := []string{}
	kindPlatforms := []string{}
	platformAgents := []string{}
	kindPlatformAgents := []string{}
	platformServiceScopes := []string{}
	kindPlatformServiceScopes := []string{}
	platformServiceNames := []string{}
	kindPlatformServiceNames := []string{}
	advisorProviders := []string{}
	platformAdvisorProviders := []string{}
	kindPlatformAdvisorProviders := []string{}
	advisorModels := []string{}
	platformAdvisorModels := []string{}
	kindPlatformAdvisorModels := []string{}
	advisorMaxDataLevels := []string{}
	platformAdvisorMaxDataLevels := []string{}
	kindPlatformAdvisorMaxDataLevels := []string{}
	checks := []string{}
	passingChecks := []string{}
	failingChecks := []string{}
	checkPlatforms := []string{}
	passingCheckPlatforms := []string{}
	failingCheckPlatforms := []string{}
	kindCheckPlatforms := []string{}
	passingKindCheckPlatforms := []string{}
	failingKindCheckPlatforms := []string{}
	coverage := verificationEvidenceCoverage{TotalFiles: len(files)}
	var maxWatchSpan time.Duration
	platformMaxSpans := map[string]time.Duration{}
	for _, file := range files {
		if file.Error != "" {
			coverage.FailingFiles++
			continue
		}
		if file.OK {
			coverage.PassingFiles++
		} else {
			coverage.FailingFiles++
		}
		kinds = append(kinds, file.Kind)
		platforms = append(platforms, file.Platforms...)
		agentIDs = append(agentIDs, file.AgentIDs...)
		serviceScopes = append(serviceScopes, file.ServiceScopes...)
		serviceNames = append(serviceNames, file.ServiceNames...)
		platformAgents = append(platformAgents, file.PlatformAgents...)
		kindPlatformAgents = append(kindPlatformAgents, file.KindPlatformAgents...)
		platformServiceScopes = append(platformServiceScopes, file.PlatformServiceScopes...)
		kindPlatformServiceScopes = append(kindPlatformServiceScopes, file.KindPlatformServiceScopes...)
		platformServiceNames = append(platformServiceNames, file.PlatformServiceNames...)
		kindPlatformServiceNames = append(kindPlatformServiceNames, file.KindPlatformServiceNames...)
		advisorProviders = append(advisorProviders, file.AdvisorProviders...)
		platformAdvisorProviders = append(platformAdvisorProviders, file.PlatformAdvisorProviders...)
		kindPlatformAdvisorProviders = append(kindPlatformAdvisorProviders, file.KindPlatformAdvisorProviders...)
		advisorModels = append(advisorModels, file.AdvisorModels...)
		platformAdvisorModels = append(platformAdvisorModels, file.PlatformAdvisorModels...)
		kindPlatformAdvisorModels = append(kindPlatformAdvisorModels, file.KindPlatformAdvisorModels...)
		advisorMaxDataLevels = append(advisorMaxDataLevels, file.AdvisorMaxDataLevels...)
		platformAdvisorMaxDataLevels = append(platformAdvisorMaxDataLevels, file.PlatformAdvisorMaxDataLevels...)
		kindPlatformAdvisorMaxDataLevels = append(kindPlatformAdvisorMaxDataLevels, file.KindPlatformAdvisorMaxDataLevels...)
		checks = append(checks, file.Checks...)
		passingChecks = append(passingChecks, file.PassingChecks...)
		failingChecks = append(failingChecks, file.FailingChecks...)
		checkPlatforms = append(checkPlatforms, file.CheckPlatforms...)
		passingCheckPlatforms = append(passingCheckPlatforms, file.PassingCheckPlatforms...)
		failingCheckPlatforms = append(failingCheckPlatforms, file.FailingCheckPlatforms...)
		kindCheckPlatforms = append(kindCheckPlatforms, file.KindCheckPlatforms...)
		passingKindCheckPlatforms = append(passingKindCheckPlatforms, file.PassingKindCheckPlatforms...)
		failingKindCheckPlatforms = append(failingKindCheckPlatforms, file.FailingKindCheckPlatforms...)
		fileWatchSpan := time.Duration(file.WatchSpanMillis) * time.Millisecond
		for _, platform := range file.Platforms {
			kindPlatforms = append(kindPlatforms, file.Kind+":"+platform)
			if fileWatchSpan > platformMaxSpans[platform] {
				platformMaxSpans[platform] = fileWatchSpan
			}
		}
		if file.WatchSamples > coverage.MaxWatchSamples {
			coverage.MaxWatchSamples = file.WatchSamples
		}
		if fileWatchSpan > maxWatchSpan {
			maxWatchSpan = fileWatchSpan
		}
	}
	coverage.Kinds = normalizeStringSet(kinds)
	coverage.Platforms = normalizeStringSet(platforms)
	coverage.AgentIDs = normalizeStringSet(agentIDs)
	coverage.ServiceScopes = normalizeStringSet(serviceScopes)
	coverage.ServiceNames = normalizeStringSet(serviceNames)
	coverage.KindPlatforms = normalizeStringSet(kindPlatforms)
	coverage.PlatformAgents = normalizeStringSet(platformAgents)
	coverage.KindPlatformAgents = normalizeStringSet(kindPlatformAgents)
	coverage.PlatformServiceScopes = normalizeStringSet(platformServiceScopes)
	coverage.KindPlatformServiceScopes = normalizeStringSet(kindPlatformServiceScopes)
	coverage.PlatformServiceNames = normalizeStringSet(platformServiceNames)
	coverage.KindPlatformServiceNames = normalizeStringSet(kindPlatformServiceNames)
	coverage.AdvisorProviders = normalizeStringSet(advisorProviders)
	coverage.PlatformAdvisorProviders = normalizeStringSet(platformAdvisorProviders)
	coverage.KindPlatformAdvisorProviders = normalizeStringSet(kindPlatformAdvisorProviders)
	coverage.AdvisorModels = normalizeStringSet(advisorModels)
	coverage.PlatformAdvisorModels = normalizeStringSet(platformAdvisorModels)
	coverage.KindPlatformAdvisorModels = normalizeStringSet(kindPlatformAdvisorModels)
	coverage.AdvisorMaxDataLevels = normalizeStringSet(advisorMaxDataLevels)
	coverage.PlatformAdvisorMaxDataLevels = normalizeStringSet(platformAdvisorMaxDataLevels)
	coverage.KindPlatformAdvisorMaxDataLevels = normalizeStringSet(kindPlatformAdvisorMaxDataLevels)
	coverage.Checks = normalizeStringSet(checks)
	coverage.PassingChecks = normalizeStringSet(passingChecks)
	coverage.FailingChecks = normalizeStringSet(failingChecks)
	coverage.CheckPlatforms = normalizeStringSet(checkPlatforms)
	coverage.PassingCheckPlatforms = normalizeStringSet(passingCheckPlatforms)
	coverage.FailingCheckPlatforms = normalizeStringSet(failingCheckPlatforms)
	coverage.KindCheckPlatforms = normalizeStringSet(kindCheckPlatforms)
	coverage.PassingKindCheckPlatforms = normalizeStringSet(passingKindCheckPlatforms)
	coverage.FailingKindCheckPlatforms = normalizeStringSet(failingKindCheckPlatforms)
	coverage.MaxWatchSpan = maxWatchSpan.String()
	coverage.MaxWatchSpanMillis = maxWatchSpan.Milliseconds()
	coverage.PlatformMaxWatchSpans = map[string]string{}
	coverage.PlatformMaxWatchSpanMillis = map[string]int64{}
	for _, platform := range normalizeStringSet(platforms) {
		span := platformMaxSpans[platform]
		coverage.PlatformMaxWatchSpans[platform] = span.String()
		coverage.PlatformMaxWatchSpanMillis[platform] = span.Milliseconds()
	}
	return coverage
}

func addEvidenceManifestChecks(manifest *verificationEvidenceManifest) {
	if manifest.Coverage.TotalFiles == 0 {
		addEvidenceManifestCheck(manifest, "evidence.files", "error", "no steward verification evidence files were found", nil)
		return
	}
	addEvidenceManifestCheck(manifest, "evidence.files", "ok", fmt.Sprintf("%d evidence files found", manifest.Coverage.TotalFiles), map[string]int{"files": manifest.Coverage.TotalFiles})

	if manifest.Options.RequirePassing {
		if manifest.Coverage.FailingFiles > 0 {
			addEvidenceManifestCheck(manifest, "evidence.passing", "error", "one or more evidence files are failing or unreadable", map[string]int{"failing_files": manifest.Coverage.FailingFiles})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.passing", "ok", "all evidence files are passing", map[string]int{"passing_files": manifest.Coverage.PassingFiles})
		}
	}
	for _, kind := range manifest.Options.RequireKinds {
		if stringSetContains(manifest.Coverage.Kinds, kind) {
			addEvidenceManifestCheck(manifest, "evidence.kind."+kind, "ok", "required evidence kind is present", map[string]string{"kind": kind})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.kind."+kind, "error", "required evidence kind is missing", map[string]string{"kind": kind})
		}
	}
	for _, platform := range manifest.Options.RequirePlatforms {
		if stringSetContains(manifest.Coverage.Platforms, platform) {
			addEvidenceManifestCheck(manifest, "evidence.platform."+platform, "ok", "required platform is present", map[string]string{"platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.platform."+platform, "error", "required platform is missing", map[string]string{"platform": platform})
		}
	}
	for _, agentID := range manifest.Options.RequireAgentIDs {
		checkID := "evidence.agent." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.AgentIDs, agentID) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present", map[string]string{"agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing", map[string]string{"agent_id": agentID})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatforms {
		kind, platform, ok := splitKindPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform.format", "error", "required kind/platform must use KIND:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform." + kind + "." + platform
		if stringSetContains(manifest.Coverage.KindPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required evidence kind/platform is present", map[string]string{"kind": kind, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required evidence kind/platform is missing", map[string]string{"kind": kind, "platform": platform})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformAgents {
		platform, agentID, ok := splitPlatformAgentRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_agent.format", "error", "required platform/agent must use PLATFORM:AGENT_ID", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_agent." + platform + "." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.PlatformAgents, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present for platform", map[string]string{"platform": platform, "agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing for platform", map[string]string{"platform": platform, "agent_id": agentID})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformAgents {
		kind, platform, agentID, ok := splitKindPlatformAgentRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_agent.format", "error", "required kind/platform/agent must use KIND:PLATFORM:AGENT_ID", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_agent." + kind + "." + platform + "." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.KindPlatformAgents, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "agent_id": agentID})
		}
	}
	for _, scope := range manifest.Options.RequireServiceScopes {
		checkID := "evidence.service_scope." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.ServiceScopes, scope) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present", map[string]string{"scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing", map[string]string{"scope": scope})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformServiceScopes {
		platform, scope, ok := splitPlatformServiceScopeRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_service_scope.format", "error", "required platform/service-scope must use PLATFORM:SCOPE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_service_scope." + platform + "." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.PlatformServiceScopes, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present for platform", map[string]string{"platform": platform, "scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing for platform", map[string]string{"platform": platform, "scope": scope})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformServiceScopes {
		kind, platform, scope, ok := splitKindPlatformServiceScopeRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_service_scope.format", "error", "required kind/platform/service-scope must use KIND:PLATFORM:SCOPE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_service_scope." + kind + "." + platform + "." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.KindPlatformServiceScopes, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "scope": scope})
		}
	}
	for _, name := range manifest.Options.RequireServiceNames {
		checkID := "evidence.service_name." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.ServiceNames, name) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present", map[string]string{"service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing", map[string]string{"service_name": name})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformServiceNames {
		platform, name, ok := splitPlatformServiceNameRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_service_name.format", "error", "required platform/service-name must use PLATFORM:NAME", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_service_name." + platform + "." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.PlatformServiceNames, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present for platform", map[string]string{"platform": platform, "service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing for platform", map[string]string{"platform": platform, "service_name": name})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformServiceNames {
		kind, platform, name, ok := splitKindPlatformServiceNameRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_service_name.format", "error", "required kind/platform/service-name must use KIND:PLATFORM:NAME", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_service_name." + kind + "." + platform + "." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.KindPlatformServiceNames, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "service_name": name})
		}
	}
	addAdvisorEvidenceManifestChecks(manifest, "provider", manifest.Options.RequireAdvisorProviders, manifest.Options.RequirePlatformAdvisorProviders, manifest.Options.RequireKindPlatformAdvisorProviders, manifest.Coverage.AdvisorProviders, manifest.Coverage.PlatformAdvisorProviders, manifest.Coverage.KindPlatformAdvisorProviders)
	addAdvisorEvidenceManifestChecks(manifest, "model", manifest.Options.RequireAdvisorModels, manifest.Options.RequirePlatformAdvisorModels, manifest.Options.RequireKindPlatformAdvisorModels, manifest.Coverage.AdvisorModels, manifest.Coverage.PlatformAdvisorModels, manifest.Coverage.KindPlatformAdvisorModels)
	addAdvisorEvidenceManifestChecks(manifest, "max_data_level", manifest.Options.RequireAdvisorMaxDataLevels, manifest.Options.RequirePlatformAdvisorMaxDataLevels, manifest.Options.RequireKindPlatformAdvisorMaxDataLevels, manifest.Coverage.AdvisorMaxDataLevels, manifest.Coverage.PlatformAdvisorMaxDataLevels, manifest.Coverage.KindPlatformAdvisorMaxDataLevels)
	for _, check := range manifest.Options.RequireChecks {
		if stringSetContains(manifest.Coverage.PassingChecks, check) {
			addEvidenceManifestCheck(manifest, "evidence.check."+sanitizeEvidenceName(check), "ok", "required passing check is present", map[string]string{"check": check})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.check."+sanitizeEvidenceName(check), "error", "required passing check is missing", map[string]string{"check": check})
		}
	}
	for _, requirement := range manifest.Options.RequireCheckPlatforms {
		check, platform, ok := splitCheckPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.check_platform.format", "error", "required check/platform must use CHECK:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.check_platform." + sanitizeEvidenceName(check) + "." + platform
		if stringSetContains(manifest.Coverage.PassingCheckPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required passing check is present for platform", map[string]string{"check": check, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required passing check is missing for platform", map[string]string{"check": check, "platform": platform})
		}
	}
	for _, requirement := range manifest.Options.RequireKindCheckPlatforms {
		kind, check, platform, ok := splitKindCheckPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_check_platform.format", "error", "required kind/check/platform must use KIND:CHECK:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_check_platform." + kind + "." + sanitizeEvidenceName(check) + "." + platform
		if stringSetContains(manifest.Coverage.PassingKindCheckPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required passing check is present for evidence kind and platform", map[string]string{"kind": kind, "check": check, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required passing check is missing for evidence kind and platform", map[string]string{"kind": kind, "check": check, "platform": platform})
		}
	}
	if manifest.Options.MinWatchDuration > 0 {
		if time.Duration(manifest.Coverage.MaxWatchSpanMillis)*time.Millisecond >= manifest.Options.MinWatchDuration {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration", "ok", "minimum watch duration is covered", map[string]string{"min": manifest.Options.MinWatchDuration.String(), "max": manifest.Coverage.MaxWatchSpan})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration", "error", "minimum watch duration is not covered", map[string]string{"min": manifest.Options.MinWatchDuration.String(), "max": manifest.Coverage.MaxWatchSpan})
		}
		if manifest.Options.MinWatchDurationPerPlatform {
			addPerPlatformWatchDurationChecks(manifest)
		}
	}
}

func addPerPlatformWatchDurationChecks(manifest *verificationEvidenceManifest) {
	targets := manifest.Options.RequirePlatforms
	if len(targets) == 0 {
		targets = manifest.Coverage.Platforms
	}
	if len(targets) == 0 {
		addEvidenceManifestCheck(manifest, "evidence.watch_duration.platforms", "error", "no platforms are available for per-platform watch duration checks", nil)
		return
	}
	for _, platform := range targets {
		maxMillis := manifest.Coverage.PlatformMaxWatchSpanMillis[platform]
		maxSpan := time.Duration(maxMillis) * time.Millisecond
		detail := map[string]string{
			"platform": platform,
			"min":      manifest.Options.MinWatchDuration.String(),
			"max":      maxSpan.String(),
		}
		if maxSpan >= manifest.Options.MinWatchDuration {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration."+platform, "ok", "minimum watch duration is covered for platform", detail)
		} else {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration."+platform, "error", "minimum watch duration is not covered for platform", detail)
		}
	}
}

func addAdvisorEvidenceManifestChecks(manifest *verificationEvidenceManifest, field string, values []string, platformValues []string, kindPlatformValues []string, coverageValues []string, coveragePlatformValues []string, coverageKindPlatformValues []string) {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return
	}
	label := strings.ReplaceAll(field, "_", " ")
	checkField := sanitizeEvidenceName(field)
	for _, value := range values {
		checkID := "evidence.advisor_" + checkField + "." + sanitizeEvidenceName(value)
		if stringSetContains(coverageValues, value) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present", map[string]string{field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing", map[string]string{field: value})
		}
	}
	for _, requirement := range platformValues {
		platform, value, ok := splitPlatformAdvisorValueRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_advisor_"+checkField+".format", "error", "required platform/advisor-"+field+" must use PLATFORM:VALUE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_advisor_" + checkField + "." + platform + "." + sanitizeEvidenceName(value)
		if stringSetContains(coveragePlatformValues, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present for platform", map[string]string{"platform": platform, field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing for platform", map[string]string{"platform": platform, field: value})
		}
	}
	for _, requirement := range kindPlatformValues {
		kind, platform, value, ok := splitKindPlatformAdvisorValueRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_advisor_"+checkField+".format", "error", "required kind/platform/advisor-"+field+" must use KIND:PLATFORM:VALUE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_advisor_" + checkField + "." + kind + "." + platform + "." + sanitizeEvidenceName(value)
		if stringSetContains(coverageKindPlatformValues, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, field: value})
		}
	}
}

func addEvidenceManifestCheck(manifest *verificationEvidenceManifest, id string, status string, message string, detail any) {
	if status != "ok" {
		manifest.OK = false
	}
	manifest.Checks = append(manifest.Checks, runtimeVerificationCheck{ID: id, Status: status, Message: message, Detail: detail})
}

func collectEvidenceStringValues(value any, key string) []string {
	values := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for itemKey, itemValue := range typed {
				if strings.EqualFold(itemKey, key) {
					if text, ok := itemValue.(string); ok {
						values = append(values, text)
					}
				}
				visit(itemValue)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return values
}

func verificationEvidenceWatchStats(value any) (int, time.Duration) {
	maxSamples := 0
	var maxSpan time.Duration
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if samples, ok := typed["samples"].([]any); ok {
				if len(samples) > maxSamples {
					maxSamples = len(samples)
				}
				if span := sampleWatchSpan(samples); span > maxSpan {
					maxSpan = span
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return maxSamples, maxSpan
}

func verificationEvidenceCheckStats(value any) ([]string, []string, []string) {
	all := []string{}
	passing := []string{}
	failing := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			id, idOK := typed["id"].(string)
			status, statusOK := typed["status"].(string)
			if idOK && statusOK {
				id = strings.TrimSpace(id)
				status = strings.ToLower(strings.TrimSpace(status))
				if id != "" {
					all = append(all, id)
					if status == "ok" {
						passing = append(passing, id)
					} else if status != "" {
						failing = append(failing, id)
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(all), normalizeStringSet(passing), normalizeStringSet(failing)
}

func verificationEvidencePlatformAgentStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			agentID, agentOK := typed["agent_id"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			agentID = strings.ToLower(strings.TrimSpace(agentID))
			if platformOK && agentOK && platform != "" && agentID != "" {
				pairs = append(pairs, platform+":"+agentID)
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func verificationEvidenceServiceScopeStats(value any) []string {
	scopes := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if scope, ok := typed["service_scope"].(string); ok {
				scopes = append(scopes, scope)
			}
			if platform, platformOK := typed["platform"].(string); platformOK && strings.TrimSpace(platform) != "" {
				if scope, ok := typed["scope"].(string); ok {
					scopes = append(scopes, scope)
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(scopes)
}

func verificationEvidencePlatformServiceScopeStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			if platformOK && platform != "" {
				if scope, ok := typed["service_scope"].(string); ok {
					scope = strings.ToLower(strings.TrimSpace(scope))
					if scope != "" {
						pairs = append(pairs, platform+":"+scope)
					}
				}
				if scope, ok := typed["scope"].(string); ok {
					scope = strings.ToLower(strings.TrimSpace(scope))
					if scope != "" {
						pairs = append(pairs, platform+":"+scope)
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func verificationEvidenceServiceNameStats(value any) []string {
	names := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if name, ok := typed["service_name"].(string); ok {
				names = append(names, name)
			}
			if evidenceMapLooksLikeService(typed) {
				if name, ok := typed["name"].(string); ok {
					names = append(names, name)
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(names)
}

func verificationEvidencePlatformServiceNameStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			if platformOK && platform != "" {
				if name, ok := typed["service_name"].(string); ok {
					name = strings.ToLower(strings.TrimSpace(name))
					if name != "" {
						pairs = append(pairs, platform+":"+name)
					}
				}
				if evidenceMapLooksLikeService(typed) {
					if name, ok := typed["name"].(string); ok {
						name = strings.ToLower(strings.TrimSpace(name))
						if name != "" {
							pairs = append(pairs, platform+":"+name)
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func evidenceMapLooksLikeService(value map[string]any) bool {
	if _, ok := value["platform"].(string); !ok {
		return false
	}
	if _, ok := value["name"].(string); !ok {
		return false
	}
	for _, key := range []string{"scope", "service_scope", "status", "service_type", "run_args"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	if artifacts, ok := value["artifacts"].(map[string]any); ok {
		if _, ok := artifacts["service_type"]; ok {
			return true
		}
	}
	return false
}

func verificationEvidenceAdvisorFieldStats(value any, field string) []string {
	values, _ := verificationEvidenceAdvisorFieldSets(value, field)
	return values
}

func verificationEvidencePlatformAdvisorFieldStats(value any, field string) []string {
	_, platformValues := verificationEvidenceAdvisorFieldSets(value, field)
	return platformValues
}

func verificationEvidenceAdvisorFieldSets(value any, field string) ([]string, []string) {
	field = strings.ToLower(strings.TrimSpace(field))
	values := []string{}
	platformValues := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if rawChecks, ok := typed["checks"].([]any); ok {
				containerPlatforms := evidenceCheckContainerPlatforms(typed, rawChecks)
				for _, rawCheck := range rawChecks {
					check, ok := rawCheck.(map[string]any)
					if !ok {
						continue
					}
					checkValues := advisorFieldValuesFromCheck(check, field)
					if len(checkValues) == 0 {
						continue
					}
					values = append(values, checkValues...)
					platforms := append([]string{}, containerPlatforms...)
					platforms = append(platforms, evidencePlatformsFromCheck(check)...)
					for _, platform := range normalizeStringSet(platforms) {
						for _, checkValue := range checkValues {
							checkValue = strings.ToLower(strings.TrimSpace(checkValue))
							if checkValue != "" {
								platformValues = append(platformValues, platform+":"+checkValue)
							}
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(values), normalizeStringSet(platformValues)
}

func advisorFieldValuesFromCheck(check map[string]any, field string) []string {
	id, _ := check["id"].(string)
	status, _ := check["status"].(string)
	id = strings.ToLower(strings.TrimSpace(id))
	status = strings.ToLower(strings.TrimSpace(status))
	if !strings.HasPrefix(id, "s4.advisor.") || status != "ok" {
		return nil
	}
	detail, ok := check["detail"].(map[string]any)
	if !ok {
		return nil
	}
	values := advisorFieldValuesFromMap(detail, field)
	if actual, ok := detail["actual"].(string); ok && advisorExpectedCheckMatchesField(id, field) {
		values = append(values, actual)
	}
	return normalizeStringSet(values)
}

func advisorExpectedCheckMatchesField(checkID string, field string) bool {
	switch field {
	case "provider":
		return checkID == "s4.advisor.expected_provider"
	case "model":
		return checkID == "s4.advisor.expected_model"
	case "max_data_level":
		return checkID == "s4.advisor.expected_max_data_level"
	default:
		return false
	}
}

func advisorFieldValuesFromMap(value map[string]any, field string) []string {
	values := []string{}
	if direct, ok := value[field].(string); ok {
		values = append(values, direct)
	}
	for _, key := range []string{"advisor", "status"} {
		if nested, ok := value[key].(map[string]any); ok {
			values = append(values, advisorFieldValuesFromMap(nested, field)...)
		}
	}
	return normalizeStringSet(values)
}

func verificationEvidenceCheckPlatformStats(value any) ([]string, []string, []string) {
	all := []string{}
	passing := []string{}
	failing := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if rawChecks, ok := typed["checks"].([]any); ok {
				platforms := evidenceCheckContainerPlatforms(typed, rawChecks)
				for _, rawCheck := range rawChecks {
					check, ok := rawCheck.(map[string]any)
					if !ok {
						continue
					}
					id, idOK := check["id"].(string)
					status, statusOK := check["status"].(string)
					id = strings.ToLower(strings.TrimSpace(id))
					status = strings.ToLower(strings.TrimSpace(status))
					if !idOK || !statusOK || id == "" {
						continue
					}
					checkPlatforms := append([]string{}, platforms...)
					checkPlatforms = append(checkPlatforms, evidencePlatformsFromCheck(check)...)
					for _, platform := range normalizeStringSet(checkPlatforms) {
						pair := id + ":" + platform
						all = append(all, pair)
						if status == "ok" {
							passing = append(passing, pair)
						} else if status != "" {
							failing = append(failing, pair)
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(all), normalizeStringSet(passing), normalizeStringSet(failing)
}

func evidenceCheckContainerPlatforms(container map[string]any, checks []any) []string {
	platforms := []string{}
	if platform, ok := container["platform"].(string); ok {
		platforms = append(platforms, platform)
	}
	if service, ok := container["service"].(map[string]any); ok {
		platforms = append(platforms, collectEvidenceStringValues(service, "platform")...)
	}
	if host, ok := container["host"].(map[string]any); ok {
		platforms = append(platforms, collectEvidenceStringValues(host, "platform")...)
	}
	for _, rawCheck := range checks {
		check, ok := rawCheck.(map[string]any)
		if !ok {
			continue
		}
		platforms = append(platforms, evidencePlatformsFromCheck(check)...)
	}
	return normalizeStringSet(platforms)
}

func evidencePlatformsFromCheck(check map[string]any) []string {
	platforms := collectEvidenceStringValues(check, "platform")
	id, _ := check["id"].(string)
	if strings.EqualFold(strings.TrimSpace(id), "steward.agent.expected_platform") {
		if detail, ok := check["detail"].(map[string]any); ok {
			if actual, ok := detail["actual"].(string); ok {
				platforms = append(platforms, actual)
			}
			if expected, ok := detail["expected"].(string); ok {
				platforms = append(platforms, expected)
			}
		}
	}
	return normalizeStringSet(platforms)
}

func withEvidenceKindPrefix(kind string, checkPlatforms []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(checkPlatforms))
	for _, checkPlatform := range checkPlatforms {
		check, platform, ok := splitCheckPlatformRequirement(checkPlatform)
		if !ok {
			continue
		}
		values = append(values, kind+":"+check+":"+platform)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformServiceScopePrefix(kind string, platformScopes []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformScopes))
	for _, platformScope := range platformScopes {
		platform, scope, ok := splitPlatformServiceScopeRequirement(platformScope)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+scope)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformServiceNamePrefix(kind string, platformNames []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformNames))
	for _, platformName := range platformNames {
		platform, name, ok := splitPlatformServiceNameRequirement(platformName)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+name)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformAdvisorValuePrefix(kind string, platformValues []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformValues))
	for _, platformValue := range platformValues {
		platform, value, ok := splitPlatformAdvisorValueRequirement(platformValue)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+value)
	}
	return normalizeStringSet(values)
}

func sampleWatchSpan(samples []any) time.Duration {
	if len(samples) < 2 {
		return 0
	}
	first, ok := sampleTime(samples[0], "started_at")
	if !ok {
		return 0
	}
	last, ok := sampleTime(samples[len(samples)-1], "completed_at")
	if !ok {
		return 0
	}
	if last.Before(first) {
		return 0
	}
	return last.Sub(first)
}

func sampleTime(value any, key string) (time.Time, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	raw, ok := item[key].(string)
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return parsed, err == nil
}

func normalizeStringSet(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeKindPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, ok := splitKindPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformAgentRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, agentID, ok := splitPlatformAgentRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+agentID)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformAgentRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, agentID, ok := splitKindPlatformAgentRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+agentID)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformServiceScopeRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, scope, ok := splitPlatformServiceScopeRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+scope)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformServiceScopeRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, scope, ok := splitKindPlatformServiceScopeRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+scope)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformServiceNameRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, name, ok := splitPlatformServiceNameRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+name)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformServiceNameRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, name, ok := splitKindPlatformServiceNameRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+name)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformAdvisorValueRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, advisorValue, ok := splitPlatformAdvisorValueRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+advisorValue)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformAdvisorValueRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, advisorValue, ok := splitKindPlatformAdvisorValueRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+advisorValue)
	}
	return normalizeStringSet(normalized)
}

func normalizeCheckPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		check, platform, ok := splitCheckPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, check+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindCheckPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, check, platform, ok := splitKindCheckPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+check+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func splitKindPlatformRequirement(value string) (string, string, bool) {
	kind, platform, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	return kind, platform, ok && kind != "" && platform != ""
}

func splitPlatformAgentRequirement(value string) (string, string, bool) {
	platform, agentID, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	agentID = strings.TrimSpace(agentID)
	return platform, agentID, ok && platform != "" && agentID != ""
}

func splitKindPlatformAgentRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, agentID, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	agentID = strings.TrimSpace(agentID)
	return kind, platform, agentID, ok && platformOK && kind != "" && platform != "" && agentID != ""
}

func splitPlatformServiceScopeRequirement(value string) (string, string, bool) {
	platform, scope, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	scope = strings.TrimSpace(scope)
	return platform, scope, ok && platform != "" && scope != ""
}

func splitKindPlatformServiceScopeRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, scope, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	scope = strings.TrimSpace(scope)
	return kind, platform, scope, ok && platformOK && kind != "" && platform != "" && scope != ""
}

func splitPlatformServiceNameRequirement(value string) (string, string, bool) {
	platform, name, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	name = strings.TrimSpace(name)
	return platform, name, ok && platform != "" && name != ""
}

func splitKindPlatformServiceNameRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, name, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	name = strings.TrimSpace(name)
	return kind, platform, name, ok && platformOK && kind != "" && platform != "" && name != ""
}

func splitPlatformAdvisorValueRequirement(value string) (string, string, bool) {
	platform, advisorValue, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	advisorValue = strings.TrimSpace(advisorValue)
	return platform, advisorValue, ok && platform != "" && advisorValue != ""
}

func splitKindPlatformAdvisorValueRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, advisorValue, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	advisorValue = strings.TrimSpace(advisorValue)
	return kind, platform, advisorValue, ok && platformOK && kind != "" && platform != "" && advisorValue != ""
}

func splitCheckPlatformRequirement(value string) (string, string, bool) {
	check, platform, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	check = strings.TrimSpace(check)
	platform = strings.TrimSpace(platform)
	return check, platform, ok && check != "" && platform != ""
}

func splitKindCheckPlatformRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	check, platform, checkOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	check = strings.TrimSpace(check)
	platform = strings.TrimSpace(platform)
	return kind, check, platform, ok && checkOK && kind != "" && check != "" && platform != ""
}

func stringSetContains(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func writeEvidenceManifest(path string, manifest verificationEvidenceManifest) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve manifest output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", fmt.Errorf("create manifest output dir: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(absPath, data, 0o600); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return absPath, nil
}
