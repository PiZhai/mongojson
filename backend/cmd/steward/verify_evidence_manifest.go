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
	options.RequireKinds = append(options.RequireKinds, "service-install-e2e", "service", "mesh", "s3s4-final-host")
	options.RequirePlatforms = append(options.RequirePlatforms, platforms...)
	for _, platform := range platforms {
		options.RequireKindPlatforms = append(options.RequireKindPlatforms, "service-install-e2e:"+platform, "service:"+platform, "mesh:"+platform, "s3s4-final-host:"+platform)
		for _, check := range s3s4FinalServiceInstallChecks() {
			options.RequireKindCheckPlatforms = append(options.RequireKindCheckPlatforms, "service-install-e2e:"+check+":"+platform)
		}
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
		options.RequireKindPlatformServiceScopes = append(options.RequireKindPlatformServiceScopes,
			"service-install-e2e:"+platform+":system",
			"service:"+platform+":system",
		)
	}
	return options
}

func s3s4FinalServiceInstallChecks() []string {
	return []string{
		"service_install_e2e.binary",
		"service_install_e2e.redaction",
		"service_install_e2e.command",
		"service_install_e2e.install",
	}
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
		"daemon.loops.status",
		"s3.device.policy_contract",
		"s3.sync.change_contract",
		"s3.sync.security.strict",
		"s3.sync.security.expected_sync_key",
		"s3.sync.security.expected_local_key",
		"s4.autonomy.status",
		"s4.autonomy.policy_contract",
		"s4.autonomy.policy_gate",
		"s4.autonomy.retry_policy",
		"s4.advisor.probe",
		"s4.advisor.privacy_probe",
	}
}

func s3s4FinalMeshChecks() []string {
	return []string{
		"mesh.watch",
		"mesh.watch.heartbeat",
		"daemon.loops.status",
		"s3.device.policy_contract",
		"s3.sync.change_contract",
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
		"s4.autonomy.policy_contract",
		"s4.autonomy.policy_gate",
		"s4.autonomy.retry_policy",
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
