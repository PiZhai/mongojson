package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildVerificationEvidenceManifestChecksCoverage(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(2*time.Hour))
	writeManifestTestEvidence(t, dir, "mesh", "linux", "linux-main", startedAt, startedAt.Add(30*time.Minute))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                  dir,
		RequirePassing:       true,
		RequireKinds:         []string{"runtime", "mesh"},
		RequirePlatforms:     []string{"windows", "linux"},
		RequireKindPlatforms: []string{"runtime:windows", "mesh:linux"},
		RequireChecks:        []string{"steward.agent"},
		MinWatchDuration:     time.Hour,
	})
	if !manifest.OK {
		t.Fatalf("expected manifest to pass: %#v", manifest.Checks)
	}
	if manifest.Coverage.TotalFiles != 2 || manifest.Coverage.PassingFiles != 2 {
		t.Fatalf("unexpected file coverage: %#v", manifest.Coverage)
	}
	if !stringSetContains(manifest.Coverage.Platforms, "windows") || !stringSetContains(manifest.Coverage.Kinds, "mesh") {
		t.Fatalf("expected platform and kind coverage, got %#v", manifest.Coverage)
	}
	if !stringSetContains(manifest.Coverage.KindPlatforms, "runtime:windows") || !stringSetContains(manifest.Coverage.KindPlatforms, "mesh:linux") {
		t.Fatalf("expected kind/platform coverage, got %#v", manifest.Coverage)
	}
	if manifest.Coverage.MaxWatchSpanMillis < int64(time.Hour/time.Millisecond) {
		t.Fatalf("expected watch span >= 1h, got %#v", manifest.Coverage)
	}
	if !stringSetContains(manifest.Coverage.PassingChecks, "steward.agent") {
		t.Fatalf("expected passing check coverage, got %#v", manifest.Coverage)
	}
}

func TestBuildVerificationEvidenceManifestFailsMissingPlatform(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(time.Minute))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:              dir,
		RequirePlatforms: []string{"darwin"},
		MinWatchDuration: time.Hour,
	})
	if manifest.OK {
		t.Fatalf("expected missing platform and watch duration to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.platform.darwin", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.watch_duration", "error") {
		t.Fatalf("expected missing platform and watch duration checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestLatestPerKindIgnoresStaleFailures(t *testing.T) {
	dir := t.TempDir()
	writeRawManifestEvidence(t, dir, "steward-verify-runtime-old-fail.json", "runtime", false, time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC), []any{
		map[string]any{"id": "steward.agent", "status": "error"},
	})
	writeRawManifestEvidence(t, dir, "steward-verify-runtime-new-pass.json", "runtime", true, time.Date(2026, 7, 5, 19, 0, 0, 0, time.UTC), []any{
		map[string]any{
			"id":     "steward.agent",
			"status": "ok",
			"detail": map[string]any{
				"agent_id": "windows-main",
				"platform": "windows",
			},
		},
	})

	withoutFilter := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:            dir,
		RequirePassing: true,
	})
	if withoutFilter.OK {
		t.Fatalf("expected stale failing evidence to fail without latest-per-kind")
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:            dir,
		LatestPerKind:  true,
		RequirePassing: true,
		RequireKinds:   []string{"runtime"},
		RequireChecks:  []string{"steward.agent"},
	})
	if !manifest.OK {
		t.Fatalf("expected latest runtime evidence to pass: %#v", manifest.Checks)
	}
	if manifest.Coverage.TotalFiles != 1 || manifest.Coverage.FailingFiles != 0 {
		t.Fatalf("expected only latest per kind to be evaluated, got %#v", manifest.Coverage)
	}
}

func TestBuildVerificationEvidenceManifestFailsMissingKindPlatform(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "service", "windows", "windows-main", startedAt, startedAt.Add(2*time.Hour))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                  dir,
		RequireKindPlatforms: []string{"service:windows", "service:darwin"},
	})
	if manifest.OK {
		t.Fatalf("expected missing service:darwin evidence to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_platform.service.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform.service.darwin", "error") {
		t.Fatalf("expected kind/platform checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestFailsPerPlatformWatchDuration(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "service", "windows", "windows-main", startedAt, startedAt.Add(2*time.Hour))
	writeManifestTestEvidence(t, dir, "service", "linux", "linux-main", startedAt, startedAt.Add(30*time.Minute))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                         dir,
		RequirePlatforms:            []string{"windows", "linux"},
		MinWatchDuration:            time.Hour,
		MinWatchDurationPerPlatform: true,
	})
	if manifest.OK {
		t.Fatalf("expected linux watch duration to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.watch_duration.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.watch_duration.linux", "error") {
		t.Fatalf("expected per-platform watch duration checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestFailsMissingRequiredCheck(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(time.Hour))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:           dir,
		RequireChecks: []string{"steward.agent", "s4.advisor.probe"},
	})
	if manifest.OK {
		t.Fatalf("expected missing advisor probe check to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.check.steward.agent", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.check.s4.advisor.probe", "error") {
		t.Fatalf("expected required check coverage results: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresCheckPerPlatform(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidenceWithExtraChecks(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(time.Hour), []string{"s4.advisor.probe"})
	writeManifestTestEvidenceWithExtraChecks(t, dir, "runtime", "linux", "linux-main", startedAt, startedAt.Add(time.Hour), nil)

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                   dir,
		RequirePlatforms:      []string{"windows", "linux"},
		RequireCheckPlatforms: []string{"s4.advisor.probe:windows", "s4.advisor.probe:linux"},
	})
	if manifest.OK {
		t.Fatalf("expected missing linux advisor probe check to fail")
	}
	if !stringSetContains(manifest.Coverage.PassingCheckPlatforms, "s4.advisor.probe:windows") {
		t.Fatalf("expected windows advisor probe platform coverage, got %#v", manifest.Coverage)
	}
	if stringSetContains(manifest.Coverage.PassingCheckPlatforms, "s4.advisor.probe:linux") {
		t.Fatalf("did not expect linux advisor probe platform coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.check_platform.s4.advisor.probe.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.check_platform.s4.advisor.probe.linux", "error") {
		t.Fatalf("expected check/platform coverage checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresKindCheckPerPlatform(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidenceWithExtraChecks(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(time.Hour), []string{"s4.advisor.probe"})
	writeManifestTestEvidenceWithExtraChecks(t, dir, "mesh", "windows", "windows-main", startedAt, startedAt.Add(time.Hour), nil)

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                       dir,
		RequireKindCheckPlatforms: []string{"runtime:s4.advisor.probe:windows", "mesh:s4.advisor.probe:windows"},
	})
	if manifest.OK {
		t.Fatalf("expected missing mesh advisor probe check to fail")
	}
	if !stringSetContains(manifest.Coverage.PassingKindCheckPlatforms, "runtime:s4.advisor.probe:windows") {
		t.Fatalf("expected runtime advisor probe kind/platform coverage, got %#v", manifest.Coverage)
	}
	if stringSetContains(manifest.Coverage.PassingKindCheckPlatforms, "mesh:s4.advisor.probe:windows") {
		t.Fatalf("did not expect mesh advisor probe kind/platform coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.runtime.s4.advisor.probe.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s4.advisor.probe.windows", "error") {
		t.Fatalf("expected kind/check/platform coverage checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresPeerRelationProbeKindPlatform(t *testing.T) {
	dir := t.TempDir()
	checks := []any{
		map[string]any{
			"id":     "s3.peer_probe.task",
			"status": "ok",
			"detail": map[string]any{
				"agent_id": "windows-main",
				"platform": "windows",
			},
		},
		map[string]any{
			"id":     "s3.peer_probe.source_ref",
			"status": "ok",
			"detail": map[string]any{
				"agent_id": "windows-main",
				"platform": "windows",
			},
		},
		map[string]any{
			"id":     "s3.peer_probe.relations",
			"status": "ok",
			"detail": map[string]any{
				"agent_id": "windows-main",
				"platform": "windows",
			},
		},
	}
	_, err := writeVerificationEvidence("mesh", dir, map[string]any{
		"verification": map[string]any{
			"ok": true,
			"local_device": map[string]any{
				"agent_id": "windows-main",
				"platform": "windows",
			},
			"checks": checks,
		},
	}, true)
	if err != nil {
		t.Fatalf("write peer relation evidence: %v", err)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir: dir,
		RequireKindCheckPlatforms: []string{
			"mesh:s3.peer_probe.task:windows",
			"mesh:s3.peer_probe.source_ref:windows",
			"mesh:s3.peer_probe.relations:windows",
			"mesh:s3.peer_probe.timeline_segment:windows",
		},
	})
	if manifest.OK {
		t.Fatalf("expected missing timeline segment probe check to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s3.peer_probe.task.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s3.peer_probe.source_ref.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s3.peer_probe.relations.windows", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s3.peer_probe.timeline_segment.windows", "error") {
		t.Fatalf("expected peer relation probe kind/platform checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresKindPlatformAgent(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	writeManifestTestEvidence(t, dir, "runtime", "windows", "windows-main", startedAt, startedAt.Add(time.Hour))
	writeManifestTestEvidence(t, dir, "mesh", "windows", "unexpected-windows", startedAt, startedAt.Add(time.Hour))

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:                       dir,
		RequireAgentIDs:           []string{"windows-main"},
		RequirePlatformAgents:     []string{"windows:windows-main"},
		RequireKindPlatformAgents: []string{"runtime:windows:windows-main", "mesh:windows:windows-main"},
	})
	if manifest.OK {
		t.Fatalf("expected missing mesh windows-main identity to fail")
	}
	if !stringSetContains(manifest.Coverage.AgentIDs, "windows-main") ||
		!stringSetContains(manifest.Coverage.PlatformAgents, "windows:windows-main") ||
		!stringSetContains(manifest.Coverage.KindPlatformAgents, "runtime:windows:windows-main") {
		t.Fatalf("expected runtime windows-main identity coverage, got %#v", manifest.Coverage)
	}
	if stringSetContains(manifest.Coverage.KindPlatformAgents, "mesh:windows:windows-main") {
		t.Fatalf("did not expect mesh windows-main identity coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.agent.windows-main", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_agent.windows.windows-main", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_agent.runtime.windows.windows-main", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_agent.mesh.windows.windows-main", "error") {
		t.Fatalf("expected identity coverage checks: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresServiceScope(t *testing.T) {
	dir := t.TempDir()
	_, err := writeVerificationEvidence("service", dir, map[string]any{
		"verification": map[string]any{
			"ok": true,
			"service": map[string]any{
				"platform": "windows",
				"name":     "MongojsonSteward",
				"scope":    "system",
				"status":   "running",
			},
			"checks": []any{
				map[string]any{
					"id":     "service.status",
					"status": "ok",
					"detail": map[string]any{
						"platform": "windows",
						"agent_id": "windows-main",
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write service scope evidence: %v", err)
	}
	_, err = writeVerificationEvidence("s3s4-final-host", dir, map[string]any{
		"verification": map[string]any{
			"ok":            true,
			"platform":      "linux",
			"service_scope": "system",
			"checks": []any{
				map[string]any{"id": "s3s4_final_host.plan", "status": "ok"},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write host scope evidence: %v", err)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir: dir,
		RequireServiceScopes: []string{
			"system",
		},
		RequirePlatformServiceScopes: []string{
			"windows:system",
			"linux:system",
			"darwin:system",
		},
		RequireKindPlatformServiceScopes: []string{
			"service:windows:system",
			"s3s4-final-host:linux:system",
			"service:darwin:system",
		},
	})
	if manifest.OK {
		t.Fatalf("expected missing darwin service scope evidence to fail")
	}
	for _, value := range []string{"system"} {
		if !stringSetContains(manifest.Coverage.ServiceScopes, value) {
			t.Fatalf("expected service scope %s in coverage: %#v", value, manifest.Coverage)
		}
	}
	for _, value := range []string{"windows:system", "linux:system"} {
		if !stringSetContains(manifest.Coverage.PlatformServiceScopes, value) {
			t.Fatalf("expected platform service scope %s in coverage: %#v", value, manifest.Coverage)
		}
	}
	if !stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "service:windows:system") ||
		!stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "s3s4-final-host:linux:system") {
		t.Fatalf("expected kind/platform service scope coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.service_scope.system", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_service_scope.windows.system", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_service_scope.darwin.system", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_service_scope.service.windows.system", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_service_scope.service.darwin.system", "error") {
		t.Fatalf("expected service scope coverage checks, got %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresServiceName(t *testing.T) {
	dir := t.TempDir()
	_, err := writeVerificationEvidence("service", dir, map[string]any{
		"verification": map[string]any{
			"ok": true,
			"service": map[string]any{
				"platform": "windows",
				"name":     "MongojsonSteward",
				"scope":    "system",
				"status":   "running",
			},
			"checks": []any{
				map[string]any{
					"id":     "service.status",
					"status": "ok",
					"detail": map[string]any{
						"platform": "windows",
						"agent_id": "windows-main",
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write service name evidence: %v", err)
	}
	_, err = writeVerificationEvidence("s3s4-final-host", dir, map[string]any{
		"verification": map[string]any{
			"ok": true,
			"host": map[string]any{
				"platform":      "linux",
				"service_name":  "mongojson-steward",
				"service_scope": "system",
			},
			"checks": []any{
				map[string]any{"id": "s3s4_final_host.plan", "status": "ok"},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write host service name evidence: %v", err)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir: dir,
		RequireServiceNames: []string{
			"MongojsonSteward",
		},
		RequirePlatformServiceNames: []string{
			"windows:MongojsonSteward",
			"linux:mongojson-steward",
			"darwin:com.mongojson.steward",
		},
		RequireKindPlatformServiceNames: []string{
			"service:windows:MongojsonSteward",
			"s3s4-final-host:linux:mongojson-steward",
			"service:darwin:com.mongojson.steward",
		},
	})
	if manifest.OK {
		t.Fatalf("expected missing darwin service name evidence to fail")
	}
	for _, value := range []string{"mongojsonsteward", "mongojson-steward"} {
		if !stringSetContains(manifest.Coverage.ServiceNames, value) {
			t.Fatalf("expected service name %s in coverage: %#v", value, manifest.Coverage)
		}
	}
	for _, value := range []string{"windows:mongojsonsteward", "linux:mongojson-steward"} {
		if !stringSetContains(manifest.Coverage.PlatformServiceNames, value) {
			t.Fatalf("expected platform service name %s in coverage: %#v", value, manifest.Coverage)
		}
	}
	if !stringSetContains(manifest.Coverage.KindPlatformServiceNames, "service:windows:mongojsonsteward") ||
		!stringSetContains(manifest.Coverage.KindPlatformServiceNames, "s3s4-final-host:linux:mongojson-steward") {
		t.Fatalf("expected kind/platform service name coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.service_name.mongojsonsteward", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_service_name.windows.mongojsonsteward", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_service_name.darwin.com.mongojson.steward", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_service_name.service.windows.mongojsonsteward", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_service_name.service.darwin.com.mongojson.steward", "error") {
		t.Fatalf("expected service name coverage checks, got %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestRequiresAdvisorMetadata(t *testing.T) {
	dir := t.TempDir()
	_, err := writeVerificationEvidence("service", dir, map[string]any{
		"verification": map[string]any{
			"ok":       true,
			"platform": "windows",
			"checks": []any{
				map[string]any{
					"id":     "s4.advisor.status",
					"status": "ok",
					"detail": map[string]any{
						"provider":       "openai-compatible",
						"model":          "advisor-model",
						"max_data_level": "D1",
					},
				},
				map[string]any{
					"id":     "s4.advisor.probe",
					"status": "ok",
					"detail": map[string]any{
						"provider":       "openai-compatible",
						"model":          "advisor-model",
						"max_data_level": "D1",
					},
				},
				map[string]any{
					"id":     "s4.advisor.expected_model",
					"status": "ok",
					"detail": map[string]any{
						"expected": "advisor-model",
						"actual":   "advisor-model",
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write advisor metadata evidence: %v", err)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir: dir,
		RequireAdvisorProviders: []string{
			"openai-compatible",
		},
		RequirePlatformAdvisorModels: []string{
			"windows:advisor-model",
			"linux:advisor-model",
		},
		RequireKindPlatformAdvisorProviders: []string{
			"service:windows:openai-compatible",
		},
		RequireKindPlatformAdvisorModels: []string{
			"service:windows:advisor-model",
			"mesh:windows:advisor-model",
		},
		RequireKindPlatformAdvisorMaxDataLevels: []string{
			"service:windows:D1",
		},
	})
	if manifest.OK {
		t.Fatalf("expected missing linux/mesh advisor metadata to fail")
	}
	if !stringSetContains(manifest.Coverage.AdvisorProviders, "openai-compatible") ||
		!stringSetContains(manifest.Coverage.PlatformAdvisorModels, "windows:advisor-model") ||
		!stringSetContains(manifest.Coverage.KindPlatformAdvisorMaxDataLevels, "service:windows:d1") {
		t.Fatalf("expected advisor metadata coverage, got %#v", manifest.Coverage)
	}
	if !hasCheckStatus(manifest.Checks, "evidence.advisor_provider.openai-compatible", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_advisor_model.windows.advisor-model", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.platform_advisor_model.linux.advisor-model", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_advisor_provider.service.windows.openai-compatible", "ok") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_advisor_model.mesh.windows.advisor-model", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_platform_advisor_max_data_level.service.windows.d1", "ok") {
		t.Fatalf("expected advisor metadata checks, got %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestS3S4FinalPresetPassesCompletePhysicalGate(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidence(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt)
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:              dir,
		Preset:           "s3s4-final",
		MinWatchDuration: time.Hour,
	})
	if !manifest.OK {
		t.Fatalf("expected complete S3/S4 final preset evidence to pass: %#v", manifest.Checks)
	}
	if manifest.Options.MinWatchDuration != s3s4FinalMinWatchDuration || !manifest.Options.MinWatchDurationPerPlatform {
		t.Fatalf("expected preset to enforce 24h per-platform watch, got %#v", manifest.Options)
	}
	for _, platform := range []string{"windows", "darwin", "linux"} {
		for _, requirement := range []string{
			"evidence.kind_platform.service." + platform,
			"evidence.kind_platform.mesh." + platform,
			"evidence.kind_platform.s3s4-final-host." + platform,
			"evidence.kind_check_platform.s3s4-final-host.s3s4_final_host.local_manifest." + platform,
			"evidence.kind_check_platform.service.service.status." + platform,
			"evidence.kind_check_platform.service.s4.advisor.privacy_probe." + platform,
			"evidence.kind_check_platform.mesh.s3.peer_probe.relations." + platform,
			"evidence.watch_duration." + platform,
		} {
			if !hasCheckStatus(manifest.Checks, requirement, "ok") {
				t.Fatalf("expected preset check %s to pass, got %#v", requirement, manifest.Checks)
			}
		}
		finalHostAgent := "s3s4-final-host:" + platform + ":" + platform + "-main"
		if !stringSetContains(manifest.Coverage.KindPlatformAgents, finalHostAgent) {
			t.Fatalf("expected final-host wrapper agent coverage %s, got %#v", finalHostAgent, manifest.Coverage.KindPlatformAgents)
		}
	}
}

func TestBuildVerificationEvidenceManifestCanRequireFinalHostAgentIdentity(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidence(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt)
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final",
		RequireKindPlatformAgents: []string{
			"s3s4-final-host:windows:windows-main",
			"s3s4-final-host:darwin:darwin-main",
			"s3s4-final-host:linux:linux-main",
		},
	})
	if !manifest.OK {
		t.Fatalf("expected final-host agent identity requirements to pass: %#v", manifest.Checks)
	}

	manifest = buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final",
		RequireKindPlatformAgents: []string{
			"s3s4-final-host:darwin:macbook-main",
		},
	})
	if manifest.OK || !hasCheckStatus(manifest.Checks, "evidence.kind_platform_agent.s3s4-final-host.darwin.macbook-main", "error") {
		t.Fatalf("expected mismatched final-host agent identity to fail: %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestS3S4FinalPresetRequiresFinalHostWrapper(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidence(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt)
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		if platform != "linux" {
			writeS3S4FinalHostPresetEvidence(t, dir, platform)
		}
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final",
	})
	if manifest.OK {
		t.Fatalf("expected missing linux final-host wrapper evidence to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_platform.s3s4-final-host.linux", "error") ||
		!hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.s3s4-final-host.s3s4_final_host.local_manifest.linux", "error") {
		t.Fatalf("expected missing final-host wrapper checks, got %#v", manifest.Checks)
	}
}

func TestBuildVerificationEvidenceManifestS3S4FinalSystemPresetRequiresSystemServiceScopes(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidenceWithServiceScope(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt, "system")
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final-system",
	})
	if !manifest.OK {
		t.Fatalf("expected complete S3/S4 final system preset evidence to pass: %#v", manifest.Checks)
	}
	for _, platform := range []string{"windows", "darwin", "linux"} {
		requirement := "evidence.kind_platform_service_scope.service." + platform + ".system"
		if !hasCheckStatus(manifest.Checks, requirement, "ok") {
			t.Fatalf("expected system service scope check %s to pass, got %#v", requirement, manifest.Checks)
		}
	}
	if !stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "service:windows:system") ||
		!stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "service:darwin:system") ||
		!stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "service:linux:system") {
		t.Fatalf("expected system service scope coverage, got %#v", manifest.Coverage)
	}
}

func TestBuildVerificationEvidenceManifestS3S4FinalSystemPresetFailsUserServiceScope(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		scope := "system"
		if platform == "darwin" {
			scope = "user"
		}
		writeS3S4FinalPresetEvidenceWithServiceScope(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt, scope)
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final-system",
	})
	if manifest.OK {
		t.Fatalf("expected darwin user service scope to fail final-system preset")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_platform_service_scope.service.darwin.system", "error") {
		t.Fatalf("expected missing darwin system service scope check, got %#v", manifest.Checks)
	}
	if !stringSetContains(manifest.Coverage.KindPlatformServiceScopes, "service:darwin:user") {
		t.Fatalf("expected darwin user scope to be visible in coverage, got %#v", manifest.Coverage)
	}
}

func TestApplyEvidenceManifestPresetIsIdempotent(t *testing.T) {
	first, err := applyEvidenceManifestPreset(evidenceManifestOptions{Preset: "s3s4-final"})
	if err != nil {
		t.Fatalf("first preset apply: %v", err)
	}
	second, err := applyEvidenceManifestPreset(first)
	if err != nil {
		t.Fatalf("second preset apply: %v", err)
	}
	first = normalizeEvidenceManifestOptions(first)
	second = normalizeEvidenceManifestOptions(second)
	if !second.PresetApplied {
		t.Fatalf("expected preset-applied marker to survive normalization")
	}
	if strings.Join(first.RequireKindCheckPlatforms, "|") != strings.Join(second.RequireKindCheckPlatforms, "|") ||
		strings.Join(first.RequireKindPlatforms, "|") != strings.Join(second.RequireKindPlatforms, "|") ||
		first.MinWatchDuration != second.MinWatchDuration ||
		first.MinWatchDurationPerPlatform != second.MinWatchDurationPerPlatform {
		t.Fatalf("preset apply should be idempotent:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestBuildVerificationEvidenceManifestS3S4FinalPresetFailsMissingCriticalCheck(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidence(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt)
		meshChecks := s3s4FinalMeshChecks()
		if platform == "linux" {
			meshChecks = removeString(meshChecks, "s3.peer_probe.relations")
		}
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", meshChecks, startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}

	manifest := buildVerificationEvidenceManifest(evidenceManifestOptions{
		Dir:    dir,
		Preset: "s3s4-final",
	})
	if manifest.OK {
		t.Fatalf("expected missing linux relation probe to fail")
	}
	if !hasCheckStatus(manifest.Checks, "evidence.kind_check_platform.mesh.s3.peer_probe.relations.linux", "error") {
		t.Fatalf("expected missing linux relation probe check, got %#v", manifest.Checks)
	}
}

func TestVerifyEvidenceS3S4FinalPresetWritesManifestOutput(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidence(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt)
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}
	outputPath := filepath.Join(t.TempDir(), "s3s4-final-manifest.json")

	c := cli{}
	output, err := captureStdoutText(t, func() error {
		return c.verifyEvidence([]string{"--dir", dir, "--preset", "s3s4-final", "--output", outputPath})
	})
	if err != nil {
		t.Fatalf("verify evidence preset: %v", err)
	}
	if !strings.Contains(output, `"preset": "s3s4-final"`) || !strings.Contains(output, `"manifest_path"`) {
		t.Fatalf("expected stdout to include preset and manifest path: %s", output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read preset manifest output: %v", err)
	}
	if !strings.Contains(string(data), `"s3s4-final"`) ||
		!strings.Contains(string(data), `"mesh:s3.peer_probe.relations:linux"`) ||
		!strings.Contains(string(data), `"s3s4-final-host:s3s4_final_host.local_manifest:linux"`) {
		t.Fatalf("preset manifest output missing final gate requirements: %s", string(data))
	}
}

func TestVerifyEvidenceS3S4FinalSystemPresetWritesServiceScopeRequirements(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(25 * time.Hour)
	for _, platform := range []string{"windows", "darwin", "linux"} {
		writeS3S4FinalPresetEvidenceWithServiceScope(t, dir, "service", platform, platform+"-main", s3s4FinalServiceChecks(), startedAt, completedAt, "system")
		writeS3S4FinalPresetEvidence(t, dir, "mesh", platform, platform+"-main", s3s4FinalMeshChecks(), startedAt, completedAt)
		writeS3S4FinalHostPresetEvidence(t, dir, platform)
	}
	outputPath := filepath.Join(t.TempDir(), "s3s4-final-system-manifest.json")

	c := cli{}
	output, err := captureStdoutText(t, func() error {
		return c.verifyEvidence([]string{"--dir", dir, "--preset", "s3s4-final-system", "--output", outputPath})
	})
	if err != nil {
		t.Fatalf("verify evidence system preset: %v", err)
	}
	if !strings.Contains(output, `"preset": "s3s4-final-system"`) || !strings.Contains(output, `"manifest_path"`) {
		t.Fatalf("expected stdout to include system preset and manifest path: %s", output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read system preset manifest output: %v", err)
	}
	if !strings.Contains(string(data), `"service:darwin:system"`) ||
		!strings.Contains(string(data), `"evidence.kind_platform_service_scope.service.darwin.system"`) {
		t.Fatalf("system preset manifest output missing service scope requirements: %s", string(data))
	}
}

func TestVerifyEvidenceWritesServiceNameRequirements(t *testing.T) {
	dir := t.TempDir()
	_, err := writeVerificationEvidence("service", dir, map[string]any{
		"verification": map[string]any{
			"ok": true,
			"service": map[string]any{
				"platform": "windows",
				"name":     "MongojsonSteward",
				"scope":    "system",
				"status":   "running",
			},
			"checks": []any{map[string]any{"id": "service.status", "status": "ok"}},
		},
	}, true)
	if err != nil {
		t.Fatalf("write service name evidence: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "service-name-manifest.json")

	c := cli{}
	output, err := captureStdoutText(t, func() error {
		return c.verifyEvidence([]string{
			"--dir", dir,
			"--require-service-name", "MongojsonSteward",
			"--require-platform-service-name", "windows:MongojsonSteward",
			"--require-kind-platform-service-name", "service:windows:MongojsonSteward",
			"--output", outputPath,
		})
	})
	if err != nil {
		t.Fatalf("verify evidence service name: %v", err)
	}
	if !strings.Contains(output, `"manifest_path"`) {
		t.Fatalf("expected stdout to include manifest path: %s", output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read service name manifest output: %v", err)
	}
	if !strings.Contains(string(data), `"service:windows:mongojsonsteward"`) ||
		!strings.Contains(string(data), `"evidence.kind_platform_service_name.service.windows.mongojsonsteward"`) {
		t.Fatalf("service name manifest output missing requirements: %s", string(data))
	}
}

func TestVerifyEvidenceWritesAdvisorMetadataRequirements(t *testing.T) {
	dir := t.TempDir()
	_, err := writeVerificationEvidence("service", dir, map[string]any{
		"verification": map[string]any{
			"ok":       true,
			"platform": "windows",
			"checks": []any{
				map[string]any{
					"id":     "s4.advisor.probe",
					"status": "ok",
					"detail": map[string]any{
						"provider":       "openai-compatible",
						"model":          "advisor-model",
						"max_data_level": "D1",
					},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write advisor metadata evidence: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "advisor-metadata-manifest.json")

	c := cli{}
	output, err := captureStdoutText(t, func() error {
		return c.verifyEvidence([]string{
			"--dir", dir,
			"--require-kind-platform-advisor-provider", "service:windows:openai-compatible",
			"--require-kind-platform-advisor-model", "service:windows:advisor-model",
			"--require-kind-platform-advisor-max-data-level", "service:windows:D1",
			"--output", outputPath,
		})
	})
	if err != nil {
		t.Fatalf("verify evidence advisor metadata: %v", err)
	}
	if !strings.Contains(output, `"manifest_path"`) {
		t.Fatalf("expected stdout to include manifest path: %s", output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read advisor metadata manifest output: %v", err)
	}
	if !strings.Contains(string(data), `"service:windows:advisor-model"`) ||
		!strings.Contains(string(data), `"evidence.kind_platform_advisor_max_data_level.service.windows.d1"`) {
		t.Fatalf("advisor metadata manifest output missing requirements: %s", string(data))
	}
}

func TestVerifyEvidenceWritesManifestOutput(t *testing.T) {
	dir := t.TempDir()
	writeManifestTestEvidence(t, dir, "runtime", "windows", "windows-main", time.Now().UTC(), time.Now().UTC().Add(time.Minute))
	outputPath := filepath.Join(t.TempDir(), "manifest.json")

	c := cli{}
	output, err := captureStdoutText(t, func() error {
		return c.verifyEvidence([]string{"--dir", dir, "--require-kind", "runtime", "--output", outputPath})
	})
	if err != nil {
		t.Fatalf("verify evidence: %v", err)
	}
	if !strings.Contains(output, `"manifest_path"`) {
		t.Fatalf("expected stdout to include manifest path: %s", output)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read manifest output: %v", err)
	}
	if !strings.Contains(string(data), `"runtime"`) {
		t.Fatalf("manifest output missing evidence data: %s", string(data))
	}
}

func writeManifestTestEvidence(t *testing.T, dir string, kind string, platform string, agentID string, startedAt time.Time, completedAt time.Time) {
	writeManifestTestEvidenceWithExtraChecks(t, dir, kind, platform, agentID, startedAt, completedAt, nil)
}

func writeManifestTestEvidenceWithExtraChecks(t *testing.T, dir string, kind string, platform string, agentID string, startedAt time.Time, completedAt time.Time, extraChecks []string) {
	t.Helper()
	checks := []any{
		map[string]any{
			"id":     "steward.agent",
			"status": "ok",
			"detail": map[string]any{
				"platform": platform,
				"agent_id": agentID,
			},
		},
	}
	for _, checkID := range extraChecks {
		checks = append(checks, map[string]any{
			"id":     checkID,
			"status": "ok",
			"detail": map[string]any{
				"probe": "test",
			},
		})
	}
	_, err := writeVerificationEvidence(kind, dir, map[string]any{
		"verification": map[string]any{
			"ok":     true,
			"checks": checks,
			"samples": []any{
				map[string]any{
					"index":      1,
					"started_at": startedAt.Format(time.RFC3339Nano),
				},
				map[string]any{
					"index":        2,
					"completed_at": completedAt.Format(time.RFC3339Nano),
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("write test evidence: %v", err)
	}
}

func writeRawManifestEvidence(t *testing.T, dir string, name string, kind string, ok bool, createdAt time.Time, checks []any) {
	t.Helper()
	envelope := map[string]any{
		"kind":       kind,
		"ok":         ok,
		"created_at": createdAt.Format(time.RFC3339Nano),
		"payload": map[string]any{
			"verification": map[string]any{
				"ok":     ok,
				"checks": checks,
			},
		},
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatalf("marshal raw evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write raw evidence: %v", err)
	}
}

func writeS3S4FinalPresetEvidence(t *testing.T, dir string, kind string, platform string, agentID string, checks []string, startedAt time.Time, completedAt time.Time) {
	writeS3S4FinalPresetEvidenceWithServiceScope(t, dir, kind, platform, agentID, checks, startedAt, completedAt, "")
}

func writeS3S4FinalPresetEvidenceWithServiceScope(t *testing.T, dir string, kind string, platform string, agentID string, checks []string, startedAt time.Time, completedAt time.Time, serviceScope string) {
	t.Helper()
	rawChecks := []any{}
	for _, checkID := range checks {
		rawChecks = append(rawChecks, map[string]any{
			"id":     checkID,
			"status": "ok",
			"detail": map[string]any{
				"platform": platform,
				"agent_id": agentID,
			},
		})
	}
	verification := map[string]any{
		"ok":       true,
		"platform": platform,
		"agent_id": agentID,
		"checks":   rawChecks,
		"samples": []any{
			map[string]any{
				"index":      1,
				"started_at": startedAt.Format(time.RFC3339Nano),
			},
			map[string]any{
				"index":        2,
				"completed_at": completedAt.Format(time.RFC3339Nano),
			},
		},
	}
	if serviceScope != "" {
		verification["service"] = map[string]any{
			"platform": platform,
			"scope":    serviceScope,
			"status":   "running",
		}
	}
	_, err := writeVerificationEvidence(kind, dir, map[string]any{
		"verification": verification,
	}, true)
	if err != nil {
		t.Fatalf("write S3/S4 final preset evidence: %v", err)
	}
}

func writeS3S4FinalHostPresetEvidence(t *testing.T, dir string, platform string) {
	t.Helper()
	agentID := platform + "-main"
	rawChecks := []any{}
	for _, checkID := range s3s4FinalHostChecks() {
		rawChecks = append(rawChecks, map[string]any{
			"id":     checkID,
			"status": "ok",
		})
	}
	_, err := writeVerificationEvidence("s3s4-final-host", dir, map[string]any{
		"verification": map[string]any{
			"ok":       true,
			"platform": platform,
			"agent_id": agentID,
			"host": map[string]any{
				"platform": platform,
				"agent_id": agentID,
			},
			"checks": rawChecks,
		},
	}, true)
	if err != nil {
		t.Fatalf("write S3/S4 final-host preset evidence: %v", err)
	}
}

func removeString(values []string, target string) []string {
	out := []string{}
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}
