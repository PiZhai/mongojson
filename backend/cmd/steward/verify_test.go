package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

func TestRunRuntimeVerificationWithWriteProbes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok", "checks": map[string]string{"database": "ok", "steward_daemon": "ok"}})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("task-1", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayload("")})
	})
	mux.HandleFunc("/api/steward/tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected tasks method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"task": map[string]any{"id": "task-1"}})
	})
	mux.HandleFunc("/api/steward/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected events method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"event": map[string]any{"id": "event-1"}})
	})
	mux.HandleFunc("/api/steward/autonomy/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected autonomy run method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayload("event-1")})
	})
	mux.HandleFunc("/api/steward/autonomy/proposals/proposal-1/dismiss", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected proposal dismiss method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"proposal": map[string]any{"id": "proposal-1", "status": "dismissed"}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{WriteProbes: true, StrictSecurity: true, AutonomyLimit: 5})

	if !result.OK {
		t.Fatalf("expected verification to pass: %#v", result.Checks)
	}
	if result.Artifacts["task_id"] != "task-1" || result.Artifacts["event_id"] != "event-1" || result.Artifacts["proposal_id"] != "proposal-1" {
		t.Fatalf("unexpected artifacts: %#v", result.Artifacts)
	}
}

func TestRunRuntimeVerificationStrictSecurityFailsWhenKeysMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", false)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayload("")})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{StrictSecurity: true})

	if result.OK {
		t.Fatalf("expected strict security verification to fail")
	}
	if !hasCheckStatus(result.Checks, "s3.sync.security.strict", "error") {
		t.Fatalf("missing strict security error check: %#v", result.Checks)
	}
}

func TestRunRuntimeVerificationChecksDiscoveryHealth(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tests := []struct {
		name      string
		discovery map[string]any
		peers     []any
		wantOK    bool
	}{
		{
			name: "healthy",
			discovery: map[string]any{
				"enabled": true, "running": true, "candidate_count": 1,
				"last_announcement_at": now, "last_error": "",
			},
			peers:  []any{map[string]any{"device_id": "linux-main", "signature_verified": true}},
			wantOK: true,
		},
		{
			name: "sender error",
			discovery: map[string]any{
				"enabled": true, "running": true, "candidate_count": 0,
				"last_announcement_at": now, "last_error": "send failed",
			},
		},
		{
			name: "no announcement",
			discovery: map[string]any{
				"enabled": true, "running": true, "candidate_count": 0, "last_error": "",
			},
		},
		{
			name: "candidate count mismatch",
			discovery: map[string]any{
				"enabled": true, "running": true, "candidate_count": 2,
				"last_announcement_at": now, "last_error": "",
			},
			peers: []any{map[string]any{"device_id": "linux-main", "signature_verified": true}},
		},
		{
			name: "unverified candidate",
			discovery: map[string]any{
				"enabled": true, "running": true, "candidate_count": 1,
				"last_announcement_at": now, "last_error": "",
			},
			peers: []any{map[string]any{"device_id": "linux-main", "signature_verified": false}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, map[string]any{"status": "ok"})
			})
			mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, map[string]any{"status": "ok"})
			})
			mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
			})
			mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
				sync := testSyncStatus("", false)
				sync["discovery"] = tt.discovery
				sync["discovered_peers"] = tt.peers
				writeTestJSON(w, map[string]any{"sync": sync})
			})
			mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
				writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayload("")})
			})

			server := httptest.NewServer(mux)
			defer server.Close()
			c := cli{apiBase: server.URL + "/api", client: server.Client()}
			result := c.runRuntimeVerification(runtimeVerifyOptions{})
			if result.OK != tt.wantOK {
				t.Fatalf("verification OK=%t, want %t: %#v", result.OK, tt.wantOK, result.Checks)
			}
			wantStatus := "error"
			if tt.wantOK {
				wantStatus = "ok"
			}
			if !hasCheckStatus(result.Checks, "s3.discovery.status", wantStatus) {
				t.Fatalf("missing discovery %s check: %#v", wantStatus, result.Checks)
			}
		})
	}
}

func TestRunRuntimeVerificationStrictSecurityChecksAdvisorRuntime(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayloadWithAdvisor("", map[string]any{
			"enabled":        true,
			"provider":       "openai-compatible",
			"model":          "advisor-model",
			"base_url":       "https://api.openai.com/v1",
			"max_data_level": "D2",
		})})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{StrictSecurity: true})

	if result.OK || !hasCheckStatus(result.Checks, "s4.advisor.strict", "error") {
		t.Fatalf("expected strict advisor runtime validation to fail: %#v", result.Checks)
	}
}

func TestRunRuntimeVerificationStrictSecurityAllowsDisabledAdvisor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayloadWithAdvisor("", map[string]any{
			"enabled":  false,
			"provider": "disabled",
			"reason":   "STEWARD_LLM_PROVIDER is not enabled",
		})})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{StrictSecurity: true})

	if !result.OK || !hasCheckStatus(result.Checks, "s4.advisor.strict", "ok") {
		t.Fatalf("expected disabled advisor to satisfy strict runtime validation: %#v", result.Checks)
	}
}

func TestRunRuntimeVerificationAdvisorProbe(t *testing.T) {
	mux := http.NewServeMux()
	advisorProbeCalled := false
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayloadWithAdvisor("", map[string]any{
			"enabled":        true,
			"provider":       "openai-compatible",
			"model":          "local-advisor",
			"max_data_level": "D1",
		})})
	})
	mux.HandleFunc("/api/steward/autonomy/advisor/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected advisor probe method %s", r.Method)
		}
		advisorProbeCalled = true
		writeTestJSON(w, map[string]any{"probe": map[string]any{
			"ok":          true,
			"data_level":  "D0",
			"duration_ms": 12,
			"probed_at":   "2026-07-05T00:00:00Z",
			"status": map[string]any{
				"enabled":        true,
				"provider":       "openai-compatible",
				"model":          "test-model",
				"max_data_level": "D1",
			},
			"suggestion": map[string]any{
				"title":            "probe title",
				"suggested_action": "create local candidate",
			},
		}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{AdvisorProbe: true})

	if !result.OK {
		t.Fatalf("expected advisor probe verification to pass: %#v", result.Checks)
	}
	if !advisorProbeCalled || result.Artifacts["advisor_probe_at"] == "" {
		t.Fatalf("expected advisor probe call and artifact, called=%t artifacts=%#v", advisorProbeCalled, result.Artifacts)
	}
	if !hasCheckStatus(result.Checks, "s4.advisor.probe", "ok") {
		t.Fatalf("missing advisor probe check: %#v", result.Checks)
	}
}

func TestRunRuntimeVerificationAdvisorPrivacyProbe(t *testing.T) {
	mux := http.NewServeMux()
	advisorPrivacyProbeCalled := false
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayload("")})
	})
	mux.HandleFunc("/api/steward/autonomy/advisor/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected advisor probe method %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode privacy probe body: %v", err)
		}
		if body["data_level"] != "D2" {
			t.Fatalf("privacy probe data_level = %#v, want D2", body["data_level"])
		}
		advisorPrivacyProbeCalled = true
		writeTestJSON(w, map[string]any{"probe": map[string]any{
			"ok":          false,
			"data_level":  "D2",
			"duration_ms": 0,
			"probed_at":   "2026-07-05T00:00:00Z",
			"error":       "data level D2 exceeds advisor max D1",
			"status": map[string]any{
				"enabled":        true,
				"provider":       "openai-compatible",
				"model":          "test-model",
				"max_data_level": "D1",
			},
		}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{AdvisorPrivacyProbe: true})

	if !result.OK {
		t.Fatalf("expected advisor privacy probe verification to pass: %#v", result.Checks)
	}
	if !advisorPrivacyProbeCalled || result.Artifacts["advisor_privacy_probe_at"] == "" {
		t.Fatalf("expected advisor privacy probe call and artifact, called=%t artifacts=%#v", advisorPrivacyProbeCalled, result.Artifacts)
	}
	if !hasCheckStatus(result.Checks, "s4.advisor.privacy_probe", "ok") {
		t.Fatalf("missing advisor privacy probe check: %#v", result.Checks)
	}
}

func TestRunRuntimeVerificationExpectedRuntimeSecurity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"agent": testAgentPayload("windows-main", "windows")})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatus("", true)})
	})
	mux.HandleFunc("/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayloadWithAdvisor("", map[string]any{
			"enabled":        true,
			"provider":       "openai-compatible",
			"model":          "local-advisor",
			"max_data_level": "D1",
		})})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	zero := 0
	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runRuntimeVerification(runtimeVerifyOptions{
		ExpectAgentID:               "windows-main",
		ExpectAgentVersion:          "version-smoke",
		ExpectAgentPlatform:         "windows",
		ExpectAdvisorProvider:       "openai-compatible",
		ExpectAdvisorModel:          "local-advisor",
		ExpectAdvisorMaxDataLevel:   "D1",
		ExpectSyncKeyID:             "sync-v1",
		ExpectLocalKeyID:            "local-v1",
		ExpectSyncPreviousKeyCount:  &zero,
		ExpectLocalPreviousKeyCount: &zero,
	})

	if !result.OK {
		t.Fatalf("expected runtime expected-value verification to pass: %#v", result.Checks)
	}
	for _, id := range []string{
		"steward.agent.expected",
		"steward.agent.expected_version",
		"steward.agent.expected_platform",
		"s4.advisor.expected_provider",
		"s4.advisor.expected_model",
		"s4.advisor.expected_max_data_level",
		"s3.sync.security.expected_sync_key",
		"s3.sync.security.expected_local_key",
		"s3.sync.security.expected_sync_previous_keys",
		"s3.sync.security.expected_local_previous_keys",
	} {
		if !hasCheckStatus(result.Checks, id, "ok") {
			t.Fatalf("missing ok check %s: %#v", id, result.Checks)
		}
	}

	mismatch := c.runRuntimeVerification(runtimeVerifyOptions{ExpectSyncKeyID: "sync-v2"})
	if mismatch.OK || !hasCheckStatus(mismatch.Checks, "s3.sync.security.expected_sync_key", "error") {
		t.Fatalf("expected sync key mismatch to fail: %#v", mismatch.Checks)
	}

	versionMismatch := c.runRuntimeVerification(runtimeVerifyOptions{ExpectAgentVersion: "other-version"})
	if versionMismatch.OK || !hasCheckStatus(versionMismatch.Checks, "steward.agent.expected_version", "error") {
		t.Fatalf("expected agent version mismatch to fail: %#v", versionMismatch.Checks)
	}

	platformMismatch := c.runRuntimeVerification(runtimeVerifyOptions{ExpectAgentPlatform: "linux"})
	if platformMismatch.OK || !hasCheckStatus(platformMismatch.Checks, "steward.agent.expected_platform", "error") {
		t.Fatalf("expected agent platform mismatch to fail: %#v", platformMismatch.Checks)
	}

	advisorMismatch := c.runRuntimeVerification(runtimeVerifyOptions{ExpectAdvisorModel: "other-model"})
	if advisorMismatch.OK || !hasCheckStatus(advisorMismatch.Checks, "s4.advisor.expected_model", "error") {
		t.Fatalf("expected advisor model mismatch to fail: %#v", advisorMismatch.Checks)
	}
}

func TestBuildRuntimeWatchVerificationResultChecksHeartbeat(t *testing.T) {
	opts := runtimeVerifyOptions{WatchDuration: time.Minute, WatchInterval: 30 * time.Second}
	firstHeartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	firstSample := runtimeWatchSampleWithHeartbeat(1, firstHeartbeat)
	firstSample.Artifacts = map[string]string{"advisor_probe_at": "2026-07-05T18:00:00Z"}
	secondSample := runtimeWatchSampleWithHeartbeat(2, firstHeartbeat.Add(2*time.Minute))
	secondSample.Artifacts = map[string]string{}
	result := buildRuntimeWatchVerificationResult("http://127.0.0.1:18080/api", opts, []runtimeVerificationSample{firstSample, secondSample})

	if !result.OK ||
		len(result.Samples) != 2 ||
		!hasCheckStatus(result.Checks, "runtime.watch", "ok") ||
		!hasCheckStatus(result.Checks, "runtime.watch.heartbeat", "ok") {
		t.Fatalf("expected passing runtime watch result, got %#v", result)
	}
	if result.Artifacts["advisor_probe_at"] != "2026-07-05T18:00:00Z" {
		t.Fatalf("expected watch artifacts to preserve first-sample active probe evidence, got %#v", result.Artifacts)
	}
}

func TestBuildRuntimeWatchVerificationResultFailsWhenHeartbeatDoesNotAdvance(t *testing.T) {
	opts := runtimeVerifyOptions{WatchDuration: time.Minute, WatchInterval: 30 * time.Second}
	heartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	result := buildRuntimeWatchVerificationResult("http://127.0.0.1:18080/api", opts, []runtimeVerificationSample{
		runtimeWatchSampleWithHeartbeat(1, heartbeat),
		runtimeWatchSampleWithHeartbeat(2, heartbeat),
	})

	if result.OK || !hasCheckStatus(result.Checks, "runtime.watch.heartbeat", "error") {
		t.Fatalf("expected heartbeat watch failure, got %#v", result)
	}

	result = buildRuntimeWatchVerificationResult("http://127.0.0.1:18080/api", opts, []runtimeVerificationSample{
		runtimeWatchSampleWithHeartbeat(1, heartbeat),
	})
	if result.OK || !hasCheckStatus(result.Checks, "runtime.watch.heartbeat", "error") {
		t.Fatalf("expected single-sample runtime watch failure, got %#v", result)
	}
}

func TestRuntimeWatchSampleOptionsOnlyRunsActiveProbesOnFirstSample(t *testing.T) {
	opts := runtimeVerifyOptions{
		WriteProbes:         true,
		AdvisorProbe:        true,
		AdvisorPrivacyProbe: true,
		WatchDuration:       time.Minute,
	}
	first := runtimeWatchSampleOptions(opts, 1)
	if !first.WriteProbes || !first.AdvisorProbe || !first.AdvisorPrivacyProbe || first.WatchDuration != 0 {
		t.Fatalf("first runtime watch sample should keep active probes and clear nested watch duration: %#v", first)
	}
	second := runtimeWatchSampleOptions(opts, 2)
	if second.WriteProbes || second.AdvisorProbe || second.AdvisorPrivacyProbe || second.WatchDuration != 0 {
		t.Fatalf("second runtime watch sample should disable active probes and clear nested watch duration: %#v", second)
	}

	opts.AdvisorProbeEachSample = true
	second = runtimeWatchSampleOptions(opts, 2)
	if second.WriteProbes || !second.AdvisorProbe || second.AdvisorPrivacyProbe || second.WatchDuration != 0 {
		t.Fatalf("second runtime watch sample should keep only advisor probe when explicitly requested: %#v", second)
	}
}

func TestValidateAdvisorProbeEachSampleRequiresProbeAndWatch(t *testing.T) {
	if err := validateAdvisorProbeEachSample(true, false, time.Minute, "verify runtime"); err == nil || !strings.Contains(err.Error(), "--advisor-probe") {
		t.Fatalf("expected missing advisor probe error, got %v", err)
	}
	if err := validateAdvisorProbeEachSample(true, true, 0, "verify runtime"); err == nil || !strings.Contains(err.Error(), "--watch-duration") {
		t.Fatalf("expected missing watch duration error, got %v", err)
	}
	if err := validateAdvisorProbeEachSample(true, true, time.Minute, "verify runtime"); err != nil {
		t.Fatalf("expected valid advisor probe watch options, got %v", err)
	}
}

func runtimeWatchSampleWithHeartbeat(index int, heartbeat time.Time) runtimeVerificationSample {
	return runtimeVerificationSample{
		Index: index,
		OK:    true,
		Checks: []runtimeVerificationCheck{{
			ID:     "steward.agent",
			Status: "ok",
			Detail: map[string]any{
				"agent_id":          "windows-main",
				"status":            "running",
				"last_heartbeat_at": heartbeat.Format(time.RFC3339Nano),
			},
		}},
	}
}

func TestRunPeersVerificationVerifiesAndSyncsPeer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatusWithDevices([]any{
			map[string]any{"id": "windows-main", "device_name": "Windows", "platform": "windows", "role": "local", "trust_status": "trusted", "sync_enabled": true},
			map[string]any{
				"id":           "macbook-main",
				"device_name":  "MacBook",
				"platform":     "darwin",
				"role":         "peer",
				"trust_status": "trusted",
				"sync_enabled": true,
				"api_base_url": "http://macbook.local:18080/api",
				"public_key":   "peer-public-key",
			},
		})})
	})
	mux.HandleFunc("/api/steward/devices/macbook-main/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected verify method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"verification": map[string]any{"verified": true}})
	})
	mux.HandleFunc("/api/steward/devices/macbook-main/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected sync method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"sync": map[string]any{"pulled": 1, "imported": 1, "applied": 1, "skipped": 0, "pushed": 2, "errors": []any{}}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runPeersVerification(peerVerifyOptions{Sync: true, Strict: true, RequirePeers: true})

	if !result.OK {
		t.Fatalf("expected peer verification to pass: %#v", result.Peers)
	}
	if len(result.Peers) != 1 || !result.Peers[0].Verified || !result.Peers[0].Synced {
		t.Fatalf("unexpected peer verification result: %#v", result.Peers)
	}
}

func TestRunPeersVerificationSyncWriteProbeChecksPeerVisibility(t *testing.T) {
	mux := http.NewServeMux()
	var server *httptest.Server
	taskCreated := false
	sourceRefCreated := false
	tagCreated := false
	tagAssigned := false
	eventCreated := false
	timelineCreated := false
	peerProbeCalls := map[string]int{}

	mux.HandleFunc("/api/steward/tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected tasks method %s", r.Method)
		}
		taskCreated = true
		writeTestJSON(w, map[string]any{"task": map[string]any{"id": "task-1"}})
	})
	mux.HandleFunc("/api/steward/source-refs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected source ref method %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode source ref probe: %v", err)
		}
		if body["target_type"] != "task" || body["target_id"] != "task-1" {
			t.Fatalf("unexpected source ref body: %#v", body)
		}
		sourceRefCreated = true
		writeTestJSON(w, map[string]any{"source_ref": map[string]any{"id": "source-ref-1"}})
	})
	mux.HandleFunc("/api/steward/tags", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected tags method %s", r.Method)
		}
		tagCreated = true
		writeTestJSON(w, map[string]any{"tag": map[string]any{"id": "tag-1"}})
	})
	mux.HandleFunc("/api/steward/tags/assign", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected tag assign method %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode tag assign probe: %v", err)
		}
		if body["entity_type"] != "task" || body["entity_id"] != "task-1" || body["tag_id"] != "tag-1" {
			t.Fatalf("unexpected tag assign body: %#v", body)
		}
		tagAssigned = true
		writeTestJSON(w, map[string]any{"status": "assigned"})
	})
	mux.HandleFunc("/api/steward/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected events method %s", r.Method)
		}
		eventCreated = true
		writeTestJSON(w, map[string]any{"event": map[string]any{"id": "event-1"}})
	})
	mux.HandleFunc("/api/steward/events/event-1/convert", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected event convert method %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode event convert probe: %v", err)
		}
		if body["target_type"] != "timeline" {
			t.Fatalf("unexpected event convert body: %#v", body)
		}
		timelineCreated = true
		writeTestJSON(w, map[string]any{"timeline_segment": map[string]any{"id": "timeline-1"}})
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatusWithDevices([]any{
			map[string]any{"id": "windows-main", "device_name": "Windows", "platform": "windows", "role": "local", "trust_status": "trusted", "sync_enabled": true},
			map[string]any{
				"id":           "macbook-main",
				"device_name":  "MacBook",
				"platform":     "darwin",
				"role":         "peer",
				"trust_status": "trusted",
				"sync_enabled": true,
				"api_base_url": server.URL + "/peer-api",
				"public_key":   "peer-public-key",
			},
		})})
	})
	mux.HandleFunc("/api/steward/devices/macbook-main/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected verify method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"verification": map[string]any{"verified": true}})
	})
	mux.HandleFunc("/api/steward/devices/macbook-main/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected sync method %s", r.Method)
		}
		writeTestJSON(w, map[string]any{"sync": map[string]any{"pulled": 0, "imported": 0, "applied": 0, "skipped": 0, "pushed": 1, "errors": []any{}}})
	})
	mux.HandleFunc("/api/steward/devices/macbook-main/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected peer probe method %s", r.Method)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode peer probe: %v", err)
		}
		entityType := body["entity_type"].(string)
		entityID := body["entity_id"].(string)
		expected := map[string]string{
			"task":             "task-1",
			"source_ref":       "source-ref-1",
			"data_tag":         "tag-1",
			"entity_tag":       verificationEntityTagID("task", "task-1", "tag-1"),
			"event":            "event-1",
			"timeline_segment": "timeline-1",
		}
		if expected[entityType] != entityID {
			t.Fatalf("unexpected peer probe body: %#v", body)
		}
		peerProbeCalls[entityType]++
		writeTestJSON(w, map[string]any{"probe": map[string]any{
			"device_id": "macbook-main", "entity_type": entityType, "entity_id": entityID, "exists": true,
		}})
	})

	server = httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runPeersVerification(peerVerifyOptions{Sync: true, Strict: true, RequirePeers: true, WriteProbes: true})

	if !result.OK {
		t.Fatalf("expected peer verification to pass: %#v", result.Peers)
	}
	if result.Probe == nil || result.Probe.TaskID != "task-1" || result.Probe.Title == "" {
		t.Fatalf("unexpected write probe: %#v", result.Probe)
	}
	if len(result.Probe.Entities) != 6 {
		t.Fatalf("expected relation probe entities, got %#v", result.Probe.Entities)
	}
	if result.LocalDevice == nil || result.LocalDevice.AgentID != "windows-main" || result.LocalDevice.Platform != "windows" {
		t.Fatalf("unexpected local device evidence: %#v", result.LocalDevice)
	}
	if !taskCreated || !sourceRefCreated || !tagCreated || !tagAssigned || !eventCreated || !timelineCreated {
		t.Fatalf("expected all local relation probes to be created: task=%t source_ref=%t tag=%t assign=%t event=%t timeline=%t", taskCreated, sourceRefCreated, tagCreated, tagAssigned, eventCreated, timelineCreated)
	}
	for _, entityType := range []string{"task", "source_ref", "data_tag", "entity_tag", "event", "timeline_segment"} {
		if peerProbeCalls[entityType] != 1 {
			t.Fatalf("expected one peer probe for %s, got calls=%#v", entityType, peerProbeCalls)
		}
	}
	if len(result.Peers) != 1 || !result.Peers[0].ProbeVisible {
		t.Fatalf("expected visible peer probe: %#v", result.Peers)
	}
	if result.Peers[0].Platform != "darwin" {
		t.Fatalf("expected peer platform evidence, got %#v", result.Peers[0])
	}
	for _, checkID := range []string{
		"s3.peers.status",
		"s3.peers.present",
		"s3.peer_probe.task",
		"s3.peer_probe.source_ref",
		"s3.peer_probe.data_tag",
		"s3.peer_probe.entity_tag",
		"s3.peer_probe.event",
		"s3.peer_probe.timeline_segment",
		"s3.peer_probe.relations",
	} {
		if !hasCheckStatus(result.Checks, checkID, "ok") {
			t.Fatalf("missing ok check %s: %#v", checkID, result.Checks)
		}
	}
}

func TestVerifyPeersWriteProbesRequireSync(t *testing.T) {
	c := cli{apiBase: "http://127.0.0.1:18080/api", client: http.DefaultClient}
	err := c.verifyPeers([]string{"--write-probes"})
	if err == nil || !strings.Contains(err.Error(), "requires --sync") {
		t.Fatalf("expected write probe sync requirement error, got %v", err)
	}
}

func TestRunPeersVerificationWriteProbeDoesNotCreateTaskWithoutSyncablePeer(t *testing.T) {
	mux := http.NewServeMux()
	taskCreateCalled := false
	mux.HandleFunc("/api/steward/tasks", func(w http.ResponseWriter, _ *http.Request) {
		taskCreateCalled = true
		http.Error(w, "unexpected task create", http.StatusInternalServerError)
	})
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatusWithDevices([]any{
			map[string]any{"id": "windows-main", "device_name": "Windows", "platform": "windows", "role": "local", "trust_status": "trusted", "sync_enabled": true},
			map[string]any{
				"id":           "linux-lab",
				"device_name":  "Linux Lab",
				"role":         "peer",
				"trust_status": "trusted",
				"sync_enabled": true,
			},
		})})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runPeersVerification(peerVerifyOptions{Sync: true, WriteProbes: true})

	if result.OK {
		t.Fatalf("expected write probe verification to fail without a syncable peer")
	}
	if taskCreateCalled || result.Probe != nil {
		t.Fatalf("write probe should not create a task without a syncable peer, taskCreateCalled=%t probe=%#v", taskCreateCalled, result.Probe)
	}
	if !hasCheckStatus(result.Checks, "s3.peer_probe.relations", "error") {
		t.Fatalf("expected relation probe coverage check to fail: %#v", result.Checks)
	}
}

func TestRunPeersVerificationStrictFailsForIncompletePeer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"sync": testSyncStatusWithDevices([]any{
			map[string]any{"id": "windows-main", "device_name": "Windows", "platform": "windows", "role": "local", "trust_status": "trusted", "sync_enabled": true},
			map[string]any{
				"id":           "linux-lab",
				"device_name":  "Linux Lab",
				"role":         "peer",
				"trust_status": "trusted",
				"sync_enabled": true,
			},
		})})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/api", client: server.Client()}
	result := c.runPeersVerification(peerVerifyOptions{Strict: true, RequirePeers: true})

	if result.OK {
		t.Fatalf("expected strict peer verification to fail")
	}
	if len(result.Peers) != 1 || result.Peers[0].Status != "error" || result.Peers[0].Error == "" {
		t.Fatalf("unexpected strict peer failure: %#v", result.Peers)
	}
}

func TestRunMeshVerificationChecksMultipleNodes(t *testing.T) {
	mux := http.NewServeMux()
	for _, prefix := range []string{"/win", "/mac"} {
		nodePrefix := prefix
		mux.HandleFunc(nodePrefix+"/healthz", func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, map[string]any{"status": "ok"})
		})
		mux.HandleFunc(nodePrefix+"/readyz", func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, map[string]any{"status": "ok", "checks": map[string]string{"database": "ok"}})
		})
		mux.HandleFunc(nodePrefix+"/api/steward/agent", func(w http.ResponseWriter, _ *http.Request) {
			platform := map[string]string{"/win": "windows", "/mac": "darwin"}[nodePrefix]
			writeTestJSON(w, map[string]any{"agent": testAgentPayload(strings.TrimPrefix(nodePrefix, "/"), platform)})
		})
		mux.HandleFunc(nodePrefix+"/api/steward/sync/status", func(w http.ResponseWriter, _ *http.Request) {
			platform := map[string]string{"/win": "windows", "/mac": "darwin"}[nodePrefix]
			sync := testSyncStatusWithDevices([]any{
				map[string]any{"id": strings.TrimPrefix(nodePrefix, "/"), "device_name": nodePrefix, "platform": platform, "role": "local", "trust_status": "trusted", "sync_enabled": true},
			})
			sync["security"] = map[string]any{
				"sync_encryption_key_id":  "sync-v1",
				"local_encryption_key_id": "local-v1",
				"config_errors":           []any{},
			}
			writeTestJSON(w, map[string]any{"sync": sync})
		})
		mux.HandleFunc(nodePrefix+"/api/steward/autonomy", func(w http.ResponseWriter, _ *http.Request) {
			writeTestJSON(w, map[string]any{"autonomy": testAutonomyPayloadWithAdvisor("", map[string]any{
				"enabled":        true,
				"provider":       "openai-compatible",
				"model":          "mesh-advisor",
				"max_data_level": "D1",
			})})
		})
	}

	server := httptest.NewServer(mux)
	defer server.Close()

	c := cli{apiBase: server.URL + "/win/api", client: server.Client()}
	result := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:                  []string{server.URL + "/win/api", server.URL + "/mac/api"},
		ExpectAgentIDs:            []string{"win", "mac"},
		ExpectAgentPlatforms:      []string{"windows", "darwin"},
		ExpectSyncKeyIDs:          []string{"sync-v1"},
		ExpectLocalKeyIDs:         []string{"local-v1"},
		ExpectAdvisorProvider:     "openai-compatible",
		ExpectAdvisorModel:        "mesh-advisor",
		ExpectAdvisorMaxDataLevel: "D1",
		AutonomyLimit:             5,
	})

	if !result.OK {
		t.Fatalf("expected mesh verification to pass: %#v", result)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(result.Nodes))
	}
	for _, node := range result.Nodes {
		if !node.Runtime.OK || !node.Peers.OK {
			t.Fatalf("unexpected node result: %#v", node)
		}
	}

	identityMismatch := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:       []string{server.URL + "/win/api", server.URL + "/mac/api"},
		ExpectAgentIDs: []string{"win", "linux"},
		AutonomyLimit:  5,
	})
	if identityMismatch.OK {
		t.Fatalf("expected agent identity mismatch to fail mesh verification")
	}
	if !hasCheckStatus(identityMismatch.Nodes[1].Runtime.Checks, "steward.agent.expected", "error") {
		t.Fatalf("missing agent mismatch check for node: %#v", identityMismatch.Nodes[1].Runtime.Checks)
	}

	platformMismatch := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:             []string{server.URL + "/win/api", server.URL + "/mac/api"},
		ExpectAgentPlatforms: []string{"windows", "linux"},
		AutonomyLimit:        5,
	})
	if platformMismatch.OK {
		t.Fatalf("expected agent platform mismatch to fail mesh verification")
	}
	if !hasCheckStatus(platformMismatch.Nodes[1].Runtime.Checks, "steward.agent.expected_platform", "error") {
		t.Fatalf("missing platform mismatch check for node: %#v", platformMismatch.Nodes[1].Runtime.Checks)
	}

	mismatch := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:           []string{server.URL + "/win/api", server.URL + "/mac/api"},
		ExpectAdvisorModel: "other-advisor",
		AutonomyLimit:      5,
	})
	if mismatch.OK {
		t.Fatalf("expected advisor mismatch to fail mesh verification")
	}
	for _, node := range mismatch.Nodes {
		if !hasCheckStatus(node.Runtime.Checks, "s4.advisor.expected_model", "error") {
			t.Fatalf("missing advisor mismatch check for node: %#v", node.Runtime.Checks)
		}
	}
}

func TestRunMeshVerificationRejectsMismatchedExpectationCounts(t *testing.T) {
	c := cli{apiBase: "http://127.0.0.1:18080/api", client: http.DefaultClient}
	result := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:       []string{"http://127.0.0.1:18080/api", "http://127.0.0.1:28080/api", "http://127.0.0.1:38080/api"},
		ExpectAgentIDs: []string{"windows-main", "macbook-main"},
	})
	if result.OK || !hasCheckStatus(result.Checks, "mesh.options", "error") {
		t.Fatalf("expected mesh options error, got %#v", result)
	}

	platformResult := c.runMeshVerification(meshVerifyOptions{
		NodeAPIs:             []string{"http://127.0.0.1:18080/api", "http://127.0.0.1:28080/api", "http://127.0.0.1:38080/api"},
		ExpectAgentPlatforms: []string{"windows", "darwin"},
	})
	if platformResult.OK || !hasCheckStatus(platformResult.Checks, "mesh.options", "error") {
		t.Fatalf("expected mesh platform options error, got %#v", platformResult)
	}
}

func TestBuildMeshWatchVerificationResultChecksNodeHeartbeats(t *testing.T) {
	opts := meshVerifyOptions{
		NodeAPIs:      []string{"http://127.0.0.1:18080/api", "http://127.0.0.1:28080/api"},
		WatchDuration: time.Minute,
		WatchInterval: 30 * time.Second,
	}
	firstHeartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	result := buildMeshWatchVerificationResult(opts, []meshVerificationSample{
		{
			Index: 1,
			OK:    true,
			Nodes: []meshNodeVerificationResult{
				meshNodeWithHeartbeat("http://127.0.0.1:18080/api", firstHeartbeat),
				meshNodeWithHeartbeat("http://127.0.0.1:28080/api", firstHeartbeat.Add(time.Second)),
			},
		},
		{
			Index: 2,
			OK:    true,
			Nodes: []meshNodeVerificationResult{
				meshNodeWithHeartbeat("http://127.0.0.1:18080/api", firstHeartbeat.Add(2*time.Minute)),
				meshNodeWithHeartbeat("http://127.0.0.1:28080/api", firstHeartbeat.Add(3*time.Minute)),
			},
		},
	})

	if !result.OK ||
		len(result.Samples) != 2 ||
		!hasCheckStatus(result.Checks, "mesh.watch", "ok") ||
		!hasCheckStatus(result.Checks, "mesh.watch.heartbeat", "ok") {
		t.Fatalf("expected passing mesh watch result, got %#v", result)
	}
}

func TestBuildMeshWatchVerificationResultFailsWhenNodeHeartbeatDoesNotAdvance(t *testing.T) {
	opts := meshVerifyOptions{
		NodeAPIs:      []string{"http://127.0.0.1:18080/api"},
		WatchDuration: time.Minute,
		WatchInterval: 30 * time.Second,
	}
	heartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	result := buildMeshWatchVerificationResult(opts, []meshVerificationSample{
		{Index: 1, OK: true, Nodes: []meshNodeVerificationResult{meshNodeWithHeartbeat("http://127.0.0.1:18080/api", heartbeat)}},
		{Index: 2, OK: true, Nodes: []meshNodeVerificationResult{meshNodeWithHeartbeat("http://127.0.0.1:18080/api", heartbeat)}},
	})

	if result.OK || !hasCheckStatus(result.Checks, "mesh.watch.heartbeat", "error") {
		t.Fatalf("expected heartbeat watch failure, got %#v", result)
	}

	result = buildMeshWatchVerificationResult(opts, []meshVerificationSample{
		{Index: 1, OK: true, Nodes: []meshNodeVerificationResult{meshNodeWithHeartbeat("http://127.0.0.1:18080/api", heartbeat)}},
	})
	if result.OK || !hasCheckStatus(result.Checks, "mesh.watch.heartbeat", "error") {
		t.Fatalf("expected single-sample mesh watch failure, got %#v", result)
	}
}

func TestMeshWatchSampleOptionsOnlyRunsActiveProbesOnFirstSample(t *testing.T) {
	opts := meshVerifyOptions{
		WriteProbes:         true,
		AdvisorProbe:        true,
		AdvisorPrivacyProbe: true,
		WatchDuration:       time.Minute,
	}
	first := meshWatchSampleOptions(opts, 1)
	if !first.WriteProbes || !first.AdvisorProbe || !first.AdvisorPrivacyProbe || first.WatchDuration != 0 {
		t.Fatalf("first mesh watch sample should keep active probes and clear nested watch duration: %#v", first)
	}
	second := meshWatchSampleOptions(opts, 2)
	if second.WriteProbes || second.AdvisorProbe || second.AdvisorPrivacyProbe || second.WatchDuration != 0 {
		t.Fatalf("second mesh watch sample should disable active probes and clear nested watch duration: %#v", second)
	}

	opts.AdvisorProbeEachSample = true
	second = meshWatchSampleOptions(opts, 2)
	if second.WriteProbes || !second.AdvisorProbe || second.AdvisorPrivacyProbe || second.WatchDuration != 0 {
		t.Fatalf("second mesh watch sample should keep only advisor probe when explicitly requested: %#v", second)
	}
}

func meshNodeWithHeartbeat(apiBase string, heartbeat time.Time) meshNodeVerificationResult {
	return meshNodeVerificationResult{
		OK:      true,
		APIBase: apiBase,
		Runtime: runtimeVerificationResult{
			OK:      true,
			APIBase: apiBase,
			Checks: []runtimeVerificationCheck{{
				ID:     "steward.agent",
				Status: "ok",
				Detail: map[string]any{
					"agent_id":          "windows-main",
					"status":            "running",
					"last_heartbeat_at": heartbeat.Format(time.RFC3339Nano),
				},
			}},
		},
		Peers: peersVerificationResult{OK: true, APIBase: apiBase},
	}
}

func TestVerifyMeshWriteProbesRequireSync(t *testing.T) {
	c := cli{apiBase: "http://127.0.0.1:18080/api", client: http.DefaultClient}
	err := c.verifyMesh([]string{"--write-probes"})
	if err == nil || !strings.Contains(err.Error(), "requires --sync") {
		t.Fatalf("expected write probe sync requirement error, got %v", err)
	}
}

func TestNormalizeMeshAPIBase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "http://127.0.0.1:18080", want: "http://127.0.0.1:18080/api"},
		{input: "http://127.0.0.1:18080/api/", want: "http://127.0.0.1:18080/api"},
	}
	for _, tt := range tests {
		got, err := normalizeMeshAPIBase(tt.input)
		if err != nil {
			t.Fatalf("normalizeMeshAPIBase(%q) failed: %v", tt.input, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeMeshAPIBase(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	if _, err := normalizeMeshAPIBase("not a url"); err == nil {
		t.Fatalf("expected invalid API base to fail")
	}
}

func TestBuildServiceVerificationResultRequiresActiveServiceAndRuntimeOK(t *testing.T) {
	runtime := runtimeVerificationResult{OK: true, APIBase: "http://127.0.0.1:18080/api", Checks: []runtimeVerificationCheck{{ID: "http.healthz", Status: "ok"}}}
	result := buildServiceVerificationResult(
		runtime.APIBase,
		serviceVerifyOptions{Name: "MongojsonSteward", Runtime: runtimeVerifyOptions{}},
		servicecontrol.StatusResult{Platform: "windows", Name: "MongojsonSteward", Status: "running"},
		nil,
		runtime,
	)
	if !result.OK {
		t.Fatalf("expected service verification to pass: %#v", result.Checks)
	}

	result = buildServiceVerificationResult(
		runtime.APIBase,
		serviceVerifyOptions{Name: "MongojsonSteward", Runtime: runtimeVerifyOptions{}},
		servicecontrol.StatusResult{Platform: "windows", Name: "MongojsonSteward", Status: "stopped"},
		nil,
		runtime,
	)
	if result.OK || !hasCheckStatus(result.Checks, "service.status", "error") {
		t.Fatalf("expected stopped service to fail: %#v", result.Checks)
	}

	runtime.OK = false
	result = buildServiceVerificationResult(
		runtime.APIBase,
		serviceVerifyOptions{Name: "MongojsonSteward", Runtime: runtimeVerifyOptions{}},
		servicecontrol.StatusResult{Platform: "windows", Name: "MongojsonSteward", Status: "running"},
		nil,
		runtime,
	)
	if result.OK || !hasCheckStatus(result.Checks, "service.runtime", "error") {
		t.Fatalf("expected runtime failure to fail service verification: %#v", result.Checks)
	}

	result = buildServiceVerificationResult(
		runtime.APIBase,
		serviceVerifyOptions{Name: "MongojsonSteward", Runtime: runtimeVerifyOptions{}},
		servicecontrol.StatusResult{Platform: "windows", Name: "MongojsonSteward", Status: ""},
		errors.New("service not installed"),
		runtimeVerificationResult{OK: true, APIBase: runtime.APIBase},
	)
	if result.OK || !hasCheckStatus(result.Checks, "service.status", "error") {
		t.Fatalf("expected service status error to fail: %#v", result.Checks)
	}
}

func TestBuildServiceWatchVerificationResultAggregatesSamples(t *testing.T) {
	opts := serviceVerifyOptions{Name: "MongojsonSteward", WatchDuration: time.Minute, WatchInterval: 30 * time.Second}
	firstHeartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	passSample := serviceWatchSampleWithHeartbeat(1, firstHeartbeat)
	result := buildServiceWatchVerificationResult("http://127.0.0.1:18080/api", opts, []serviceVerificationSample{
		passSample,
		serviceWatchSampleWithHeartbeat(2, firstHeartbeat.Add(2*time.Minute)),
	})
	if !result.OK ||
		len(result.Samples) != 2 ||
		!hasCheckStatus(result.Checks, "service.watch", "ok") ||
		!hasCheckStatus(result.Checks, "service.watch.heartbeat", "ok") {
		t.Fatalf("expected passing watch result, got %#v", result)
	}

	failSample := passSample
	failSample.Index = 2
	failSample.OK = false
	failSample.Checks = []runtimeVerificationCheck{{ID: "service.runtime", Status: "error"}}
	result = buildServiceWatchVerificationResult("http://127.0.0.1:18080/api", opts, []serviceVerificationSample{passSample, failSample})
	if result.OK || !hasCheckStatus(result.Checks, "service.watch", "error") {
		t.Fatalf("expected failing watch result, got %#v", result)
	}
}

func TestBuildServiceWatchVerificationResultFailsWhenHeartbeatDoesNotAdvance(t *testing.T) {
	opts := serviceVerifyOptions{Name: "MongojsonSteward", WatchDuration: time.Minute, WatchInterval: 30 * time.Second}
	heartbeat := time.Date(2026, 7, 5, 18, 0, 0, 0, time.UTC)
	result := buildServiceWatchVerificationResult("http://127.0.0.1:18080/api", opts, []serviceVerificationSample{
		serviceWatchSampleWithHeartbeat(1, heartbeat),
		serviceWatchSampleWithHeartbeat(2, heartbeat),
	})

	if result.OK || !hasCheckStatus(result.Checks, "service.watch.heartbeat", "error") {
		t.Fatalf("expected heartbeat watch failure, got %#v", result)
	}

	result = buildServiceWatchVerificationResult("http://127.0.0.1:18080/api", opts, []serviceVerificationSample{
		serviceWatchSampleWithHeartbeat(1, heartbeat),
	})
	if result.OK || !hasCheckStatus(result.Checks, "service.watch.heartbeat", "error") {
		t.Fatalf("expected single-sample service watch failure, got %#v", result)
	}
}

func serviceWatchSampleWithHeartbeat(index int, heartbeat time.Time) serviceVerificationSample {
	agentDetail := map[string]any{
		"agent_id":          "windows-main",
		"status":            "running",
		"last_heartbeat_at": heartbeat.Format(time.RFC3339Nano),
	}
	return serviceVerificationSample{
		Index:   index,
		OK:      true,
		Service: servicecontrol.StatusResult{Platform: "windows", Name: "MongojsonSteward", Status: "running"},
		Runtime: runtimeVerificationResult{
			OK:      true,
			APIBase: "http://127.0.0.1:18080/api",
			Checks: []runtimeVerificationCheck{{
				ID:     "steward.agent",
				Status: "ok",
				Detail: agentDetail,
			}},
		},
		Checks: []runtimeVerificationCheck{{ID: "service.status", Status: "ok"}},
	}
}

func TestServiceWatchSampleOptionsOnlyRunsActiveProbesOnFirstSample(t *testing.T) {
	opts := serviceVerifyOptions{
		WriteProbes: true,
		Runtime: runtimeVerifyOptions{
			WriteProbes:         true,
			AdvisorProbe:        true,
			AdvisorPrivacyProbe: true,
		},
	}
	first := serviceWatchSampleOptions(opts, 1)
	if !first.WriteProbes || !first.Runtime.WriteProbes || !first.Runtime.AdvisorProbe || !first.Runtime.AdvisorPrivacyProbe {
		t.Fatalf("first watch sample should keep active probes enabled: %#v", first)
	}
	second := serviceWatchSampleOptions(opts, 2)
	if second.WriteProbes || second.Runtime.WriteProbes || second.Runtime.AdvisorProbe || second.Runtime.AdvisorPrivacyProbe {
		t.Fatalf("second watch sample should disable active probes: %#v", second)
	}

	opts.Runtime.AdvisorProbeEachSample = true
	second = serviceWatchSampleOptions(opts, 2)
	if second.WriteProbes || second.Runtime.WriteProbes || !second.Runtime.AdvisorProbe || second.Runtime.AdvisorPrivacyProbe {
		t.Fatalf("second watch sample should keep only runtime advisor probe when explicitly requested: %#v", second)
	}
}

func TestServiceStatusIsActiveByPlatform(t *testing.T) {
	tests := []struct {
		platform string
		status   string
		want     bool
	}{
		{platform: "windows", status: "running", want: true},
		{platform: "windows", status: "stopped", want: false},
		{platform: "linux", status: "active", want: true},
		{platform: "linux", status: "inactive", want: false},
		{platform: "darwin", status: "loaded", want: true},
		{platform: "darwin", status: "not_loaded", want: false},
	}
	for _, tt := range tests {
		if got := serviceStatusIsActive(tt.platform, tt.status); got != tt.want {
			t.Fatalf("serviceStatusIsActive(%q,%q) = %t, want %t", tt.platform, tt.status, got, tt.want)
		}
	}
}

func testSyncStatus(taskID string, secure bool) map[string]any {
	changes := []any{}
	if taskID != "" {
		changes = append(changes, map[string]any{"entity_type": "task", "entity_id": taskID})
	}
	status := testSyncStatusWithDevices([]any{
		map[string]any{
			"id": "windows-main", "device_name": "Windows", "platform": "windows", "role": "local",
			"trust_status": "trusted", "sync_enabled": true, "permission_level": "A3",
		},
	})
	status["pending_changes"] = len(changes)
	status["last_change_at"] = time.Now().UTC().Format(time.RFC3339)
	status["recent_changes"] = changes
	status["security"] = map[string]any{
		"auth_required":                  secure,
		"peer_api_enabled":               secure,
		"peer_api_advertised":            secure,
		"management_remote_access":       false,
		"hmac_secret_configured":         secure,
		"device_signing_ready":           secure,
		"device_identity_advertisable":   secure,
		"sync_encryption_configured":     secure,
		"local_encryption_configured":    secure,
		"sync_previous_key_count":        0,
		"local_previous_key_count":       0,
		"device_private_key_configured":  secure,
		"device_private_key_valid":       secure,
		"device_public_key_configured":   secure,
		"device_public_key_valid":        secure,
		"sync_encryption_key_id":         "sync-v1",
		"local_encryption_key_id":        "local-v1",
		"config_errors":                  []any{},
		"hmac_secret_configured_warning": false,
	}
	return status
}

func testSyncStatusWithDevices(devices []any) map[string]any {
	localID := ""
	localName := ""
	localPlatform := "unknown"
	permissions := []any{}
	for _, item := range devices {
		device, _ := item.(map[string]any)
		id := stringAt(device, "id")
		if stringAt(device, "platform") == "" {
			device["platform"] = "unknown"
		}
		if stringAt(device, "permission_level") == "" {
			device["permission_level"] = "A3"
		}
		if stringAt(device, "api_base_url") == "" {
			device["api_base_url"] = ""
		}
		if stringAt(device, "role") == "local" {
			localID = id
			localName = stringAt(device, "device_name")
			localPlatform = stringAt(device, "platform")
		}
		permissions = append(permissions, map[string]any{
			"device_id": id, "capability": "sync.metadata", "policy": "allow", "max_permission_level": "A1",
		})
	}
	if localID == "" {
		localID = "windows-main"
	}
	if localName == "" {
		localName = localID
	}
	return map[string]any{
		"local_device":    map[string]any{"id": localID, "device_name": localName, "platform": localPlatform},
		"devices":         devices,
		"permissions":     permissions,
		"pending_changes": 0,
		"conflict_count":  0,
		"security":        map[string]any{"config_errors": []any{}},
		"recent_changes":  []any{},
		"change_contract": map[string]any{"healthy": true, "checked_changes": 0, "invalid_changes": 0, "issues": []any{}},
	}
}

func testAutonomyPayload(sourceEntityID string) map[string]any {
	return testAutonomyPayloadWithAdvisor(sourceEntityID, nil)
}

func testAgentPayload(agentID string, platform string) map[string]any {
	return map[string]any{
		"agent_id": agentID,
		"version":  "version-smoke",
		"platform": platform,
		"status":   "running",
		"background_loops": []any{
			map[string]any{"name": "heartbeat", "enabled": true, "running": true, "interval": "1m", "consecutive_failures": 0},
			map[string]any{"name": "sync", "enabled": false, "running": false, "interval": "0s", "consecutive_failures": 0},
			map[string]any{"name": "autonomy", "enabled": false, "running": false, "interval": "0s", "consecutive_failures": 0},
		},
	}
}

func testAutonomyPayloadWithAdvisor(sourceEntityID string, advisor map[string]any) map[string]any {
	proposals := []any{}
	if sourceEntityID != "" {
		proposals = append(proposals, map[string]any{
			"id":               "proposal-1",
			"source_entity_id": sourceEntityID,
			"status":           "candidate",
		})
	}
	payload := map[string]any{
		"settings":     map[string]any{"paused": false, "mode": "suggest_only", "max_auto_permission": "A3"},
		"retry_policy": map[string]any{"max_attempts": 3, "backoff": "5m0s", "max_backoff": "1h0m0s"},
		"policy_gate": map[string]any{
			"enabled": true, "backend": "postgres_advisory_rw", "cycle_read_barrier": true,
			"execution_read_barrier": true, "settings_write_barrier": true, "rule_write_barrier": true,
			"current_rule_revalidation": true,
		},
		"rules": []any{map[string]any{
			"id": "rule-1", "name": "rule-1", "action": "create_local_task", "enabled": true,
			"policy": "confirm", "risk_level": "low", "max_permission_level": "A3",
		}},
		"proposals": proposals,
	}
	if advisor != nil {
		payload["advisor"] = advisor
	}
	return payload
}

func hasCheckStatus(checks []runtimeVerificationCheck, id string, status string) bool {
	for _, check := range checks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func TestSyncChangeContractRuntimeIssues(t *testing.T) {
	healthy := map[string]any{
		"healthy":         true,
		"checked_changes": float64(4),
		"invalid_changes": float64(0),
		"issues":          []any{},
	}
	if issues := syncChangeContractRuntimeIssues(healthy); len(issues) != 0 {
		t.Fatalf("healthy contract issues: %#v", issues)
	}
	broken := map[string]any{
		"healthy":         false,
		"checked_changes": float64(2),
		"invalid_changes": float64(1),
		"issues":          []any{"change-id: operation is invalid"},
	}
	issues := syncChangeContractRuntimeIssues(broken)
	if len(issues) < 3 {
		t.Fatalf("broken contract was not rejected: %#v", issues)
	}
	if issues := syncChangeContractRuntimeIssues(nil); len(issues) != 1 {
		t.Fatalf("missing contract issues: %#v", issues)
	}
}

func TestAutonomyPolicyGateRuntimeIssues(t *testing.T) {
	healthy := map[string]any{
		"enabled": true, "backend": "postgres_advisory_rw", "cycle_read_barrier": true,
		"execution_read_barrier": true, "settings_write_barrier": true, "rule_write_barrier": true,
		"current_rule_revalidation": true,
	}
	if issues := autonomyPolicyGateRuntimeIssues(healthy); len(issues) != 0 {
		t.Fatalf("healthy policy gate issues: %#v", issues)
	}
	broken := map[string]any{"enabled": true, "backend": "memory", "cycle_read_barrier": true}
	if issues := autonomyPolicyGateRuntimeIssues(broken); len(issues) < 5 {
		t.Fatalf("broken policy gate was not rejected: %#v", issues)
	}
	if issues := autonomyPolicyGateRuntimeIssues(nil); len(issues) != 1 {
		t.Fatalf("missing policy gate issues: %#v", issues)
	}
}

func TestAutonomyRetryRuntimeIssues(t *testing.T) {
	valid := map[string]any{"max_attempts": 3, "backoff": "5m", "max_backoff": "1h"}
	if issues := autonomyRetryRuntimeIssues(valid); len(issues) != 0 {
		t.Fatalf("valid retry policy issues = %v", issues)
	}
	invalid := map[string]any{"max_attempts": 0, "backoff": "1h", "max_backoff": "5m"}
	if issues := autonomyRetryRuntimeIssues(invalid); len(issues) != 2 {
		t.Fatalf("invalid retry policy issues = %v, want 2", issues)
	}
}

func TestAutonomyPolicyRuntimeIssues(t *testing.T) {
	validSettings := map[string]any{"mode": "controlled", "max_auto_permission": "A3"}
	validRules := []any{
		map[string]any{"name": "auto-low", "action": "create_local_task", "policy": "auto", "risk_level": "low", "max_permission_level": "A3"},
		map[string]any{"name": "high-plan", "action": "block_high_risk_execution", "policy": "never", "risk_level": "high", "max_permission_level": "A4"},
	}
	if issues := autonomyPolicyRuntimeIssues(validSettings, validRules); len(issues) != 0 {
		t.Fatalf("valid autonomy policy issues = %v", issues)
	}
	invalidSettings := map[string]any{"mode": "automatic", "max_auto_permission": "A4"}
	invalidRules := []any{
		map[string]any{"name": "unsafe-auto", "action": "send", "policy": "auto", "risk_level": "medium", "max_permission_level": "A6"},
		map[string]any{"name": "invalid", "policy": "allow", "risk_level": "unknown", "max_permission_level": "root"},
	}
	issues := autonomyPolicyRuntimeIssues(invalidSettings, invalidRules)
	if len(issues) != 7 {
		t.Fatalf("invalid autonomy policy issues = %v, want 7", issues)
	}
}

func TestDevicePolicyRuntimeIssues(t *testing.T) {
	valid := testSyncStatusWithDevices([]any{
		map[string]any{
			"id": "windows-main", "platform": "windows", "role": "local", "trust_status": "trusted",
			"sync_enabled": true, "permission_level": "A3", "api_base_url": "",
		},
		map[string]any{
			"id": "macbook-main", "platform": "darwin", "role": "peer", "trust_status": "trusted",
			"sync_enabled": true, "permission_level": "A2", "api_base_url": "https://peer.example/api",
		},
	})
	if issues := devicePolicyRuntimeIssues(valid); len(issues) != 0 {
		t.Fatalf("valid device policy issues = %v", issues)
	}

	invalid := map[string]any{
		"local_device": map[string]any{"id": "windows-main"},
		"devices": []any{
			map[string]any{"id": "windows-main", "platform": "windows", "role": "peer", "trust_status": "trusted", "sync_enabled": true, "permission_level": "A3"},
			map[string]any{"id": "evil", "platform": "macos", "role": "local", "trust_status": "revoked", "sync_enabled": true, "permission_level": "root", "api_base_url": "https://user:pass@peer.example/api"},
		},
		"permissions": []any{
			map[string]any{"device_id": "ghost", "capability": "admin", "policy": "auto", "max_permission_level": "root"},
		},
	}
	if issues := devicePolicyRuntimeIssues(invalid); len(issues) < 10 {
		t.Fatalf("invalid device policy issues = %v, want at least 10", issues)
	}
}

func TestBackgroundLoopRuntimeIssuesAllowsObservableDegradation(t *testing.T) {
	loops := []any{
		map[string]any{"name": "heartbeat", "enabled": true, "running": true, "consecutive_failures": 0},
		map[string]any{"name": "sync", "enabled": true, "running": true, "consecutive_failures": 3, "last_error": "peer offline"},
	}
	if issues := backgroundLoopRuntimeIssues(loops); len(issues) != 0 {
		t.Fatalf("running degraded loop should remain runtime-ready: %v", issues)
	}
	loops[0] = map[string]any{"name": "heartbeat", "enabled": true, "running": false}
	if issues := backgroundLoopRuntimeIssues(loops); len(issues) != 1 {
		t.Fatalf("stopped enabled heartbeat issues = %v, want 1", issues)
	}
}

func writeTestJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
