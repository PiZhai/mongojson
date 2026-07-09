package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

func TestServiceEnvVerificationAdviceBuildsStrictCommandsFromRedactedEnvironment(t *testing.T) {
	advice := serviceEnvVerificationAdviceFromEnvironment("MongojsonSteward", map[string]string{
		"HTTP_ADDR":                             ":18080",
		"STEWARD_AGENT_ID":                      "windows-main",
		"STEWARD_SYNC_ENCRYPTION_KEY":           "<redacted>",
		"STEWARD_SYNC_ENCRYPTION_KEY_ID":        "home-sync-v2",
		"STEWARD_LOCAL_ENCRYPTION_KEY":          "<redacted>",
		"STEWARD_LOCAL_ENCRYPTION_KEY_ID":       "windows-local-v1",
		"STEWARD_LLM_PROVIDER":                  "openai-compatible",
		"STEWARD_LLM_MODEL":                     "advisor-model",
		"STEWARD_LLM_API_KEY":                   "<redacted>",
		"STEWARD_LLM_MAX_DATA_LEVEL":            "D1",
		"STEWARD_LLM_ALLOW_NO_API_KEY":          "false",
		"STEWARD_LLM_FAILURE_COOLDOWN":          "1m",
		"STEWARD_LLM_FAILURE_THRESHOLD":         "3",
		"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS": "<redacted>",
	})
	if advice == nil {
		t.Fatalf("expected verification advice")
	}
	if advice.APIBase != "http://127.0.0.1:18080/api" {
		t.Fatalf("api base = %q", advice.APIBase)
	}
	if advice.ServiceScope != servicecontrol.DefaultScope() {
		t.Fatalf("service scope = %q, want %q", advice.ServiceScope, servicecontrol.DefaultScope())
	}
	assertArgsContain(t, advice.RuntimeArgs, "--strict-security")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-agent-id", "windows-main")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-agent-platform", runtime.GOOS)
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-sync-key-id", "home-sync-v2")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-local-key-id", "windows-local-v1")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-advisor-provider", "openai-compatible")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-advisor-model", "advisor-model")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-advisor-max-data-level", "D1")
	assertArgsContainPair(t, advice.ServiceArgs, "--name", "MongojsonSteward")
	assertArgsContainPair(t, advice.ServiceArgs, "--scope", servicecontrol.DefaultScope())
	assertArgsContainPair(t, advice.WatchArgs, "--watch-duration", "24h")
	assertArgsContainPair(t, advice.WatchArgs, "--watch-interval", "5m")
	for _, command := range []string{advice.RuntimeCommand, advice.ServiceCommand, advice.WatchCommand} {
		if strings.Contains(command, "<redacted>") {
			t.Fatalf("verification command leaked redacted marker: %s", command)
		}
		if strings.Contains(command, "STEWARD_LLM_API_KEY") || strings.Contains(command, "STEWARD_SYNC_ENCRYPTION_KEY") {
			t.Fatalf("verification command included sensitive env key: %s", command)
		}
	}
}

func TestServiceEnvVerificationAdviceMapsOpenAIProviderAndDefaultsDataLevel(t *testing.T) {
	advice := serviceEnvVerificationAdviceFromEnvironment("MongojsonSteward", map[string]string{
		"HTTP_ADDR":            "127.0.0.1:28080",
		"STEWARD_LLM_PROVIDER": "openai",
		"STEWARD_LLM_MODEL":    "gpt-test",
	})
	if advice == nil {
		t.Fatalf("expected verification advice")
	}
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-advisor-provider", "openai-compatible")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-advisor-max-data-level", "D1")
}

func TestServiceEnvVerificationAdviceCanTargetAnotherPlatform(t *testing.T) {
	advice := serviceEnvVerificationAdviceFromEnvironmentForPlatform("mongojson-steward", "system", map[string]string{
		"HTTP_ADDR":        "127.0.0.1:38080",
		"STEWARD_AGENT_ID": "linux-main",
	}, "linux")
	if advice == nil {
		t.Fatalf("expected verification advice")
	}
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-agent-id", "linux-main")
	assertArgsContainPair(t, advice.RuntimeArgs, "--expect-agent-platform", "linux")
	assertArgsContainPair(t, advice.ServiceArgs, "--name", "mongojson-steward")
	assertArgsContainPair(t, advice.ServiceArgs, "--scope", "system")
	if advice.ServiceScope != "system" {
		t.Fatalf("service scope = %q, want system", advice.ServiceScope)
	}
}

func TestServiceVerifyOptionsFromEnvironmentUsesStrictRuntimeExpectations(t *testing.T) {
	apiBase, opts := serviceVerifyOptionsFromEnvironment("MongojsonSteward", servicecontrol.DefaultScope(), map[string]string{
		"HTTP_ADDR":                       "127.0.0.1:28080",
		"STEWARD_AGENT_ID":                "linux-main",
		"STEWARD_SYNC_ENCRYPTION_KEY":     "<redacted>",
		"STEWARD_SYNC_ENCRYPTION_KEY_ID":  "home-sync-v3",
		"STEWARD_LOCAL_ENCRYPTION_KEY":    "<redacted>",
		"STEWARD_LOCAL_ENCRYPTION_KEY_ID": "linux-local-v1",
		"STEWARD_LLM_PROVIDER":            "openai",
		"STEWARD_LLM_MODEL":               "advisor-model",
		"STEWARD_LLM_API_KEY":             "<redacted>",
		"STEWARD_LLM_MAX_DATA_LEVEL":      "D0",
	}, servicePostVerifyOptions{
		Verify:                 true,
		WatchDuration:          2 * time.Second,
		WatchInterval:          time.Second,
		AdvisorProbe:           true,
		AdvisorProbeEachSample: true,
		AdvisorPrivacyProbe:    true,
	})
	if apiBase != "http://127.0.0.1:28080/api" {
		t.Fatalf("api base = %q", apiBase)
	}
	if opts.Name != "MongojsonSteward" || !opts.StrictSecurity || !opts.Runtime.StrictSecurity {
		t.Fatalf("strict service options not populated: %#v", opts)
	}
	if opts.WatchDuration != 2*time.Second || opts.WatchInterval != time.Second {
		t.Fatalf("watch options not propagated: %#v", opts)
	}
	if opts.Runtime.ExpectAgentID != "linux-main" ||
		!opts.Runtime.AdvisorProbe ||
		!opts.Runtime.AdvisorProbeEachSample ||
		!opts.Runtime.AdvisorPrivacyProbe ||
		opts.Runtime.ExpectAgentPlatform != runtime.GOOS ||
		opts.Runtime.ExpectSyncKeyID != "home-sync-v3" ||
		opts.Runtime.ExpectLocalKeyID != "linux-local-v1" ||
		opts.Runtime.ExpectAdvisorProvider != "openai-compatible" ||
		opts.Runtime.ExpectAdvisorModel != "advisor-model" ||
		opts.Runtime.ExpectAdvisorMaxDataLevel != "D0" {
		t.Fatalf("runtime expectations not populated: %#v", opts.Runtime)
	}
}

func TestValidateServicePostVerifyOptionsRejectsInvalidAdvisorProbeCombinations(t *testing.T) {
	cases := []struct {
		name string
		opts servicePostVerifyOptions
		want string
	}{
		{
			name: "advisor probe requires verify",
			opts: servicePostVerifyOptions{AdvisorProbe: true},
			want: "--verify-advisor-probe requires --verify",
		},
		{
			name: "privacy probe requires verify",
			opts: servicePostVerifyOptions{AdvisorPrivacyProbe: true},
			want: "--verify-advisor-privacy-probe requires --verify",
		},
		{
			name: "each sample requires probe",
			opts: servicePostVerifyOptions{Verify: true, AdvisorProbeEachSample: true, WatchDuration: time.Minute},
			want: "--verify-advisor-probe-each-sample requires --verify-advisor-probe",
		},
		{
			name: "each sample requires watch",
			opts: servicePostVerifyOptions{Verify: true, AdvisorProbe: true, AdvisorProbeEachSample: true},
			want: "--verify-advisor-probe-each-sample requires --verify-watch-duration",
		},
		{
			name: "evidence dir requires verify",
			opts: servicePostVerifyOptions{EvidenceDir: "evidence"},
			want: "--verify-evidence-dir requires --verify",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServicePostVerifyOptions("service install", tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
	if err := validateServicePostVerifyOptions("service install", servicePostVerifyOptions{
		Verify:                 true,
		AdvisorProbe:           true,
		AdvisorProbeEachSample: true,
		WatchDuration:          time.Minute,
	}); err != nil {
		t.Fatalf("expected valid post verify advisor options, got %v", err)
	}
}

func TestWriteServicePostVerificationEvidencePersistsResult(t *testing.T) {
	result := serviceVerificationResult{
		OK:      true,
		APIBase: "http://127.0.0.1:18080/api",
		Options: serviceVerifyOptions{
			Name: "MongojsonSteward",
		},
		Checks: []runtimeVerificationCheck{{ID: "service.status", Status: "ok"}},
	}
	path, err := writeServicePostVerificationEvidence("service-install", servicePostVerifyOptions{
		Verify:      true,
		EvidenceDir: t.TempDir(),
	}, result)
	if err != nil {
		t.Fatalf("write post verification evidence: %v", err)
	}
	if !strings.Contains(path, "service-install") {
		t.Fatalf("unexpected evidence path %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if !strings.Contains(string(data), `"service.status"`) || !strings.Contains(string(data), `"MongojsonSteward"`) {
		t.Fatalf("evidence did not include service verification result: %s", string(data))
	}
}

func TestNormalizeServicePostVerifyOptionsDefaultsStartupWait(t *testing.T) {
	opts := normalizeServicePostVerifyOptions(servicePostVerifyOptions{})
	if opts.StartupTimeout != 30*time.Second || opts.WatchInterval != time.Minute {
		t.Fatalf("unexpected default post verify options: %#v", opts)
	}
	opts = normalizeServicePostVerifyOptions(servicePostVerifyOptions{
		StartupTimeout: 5 * time.Second,
		WatchInterval:  2 * time.Second,
	})
	if opts.StartupTimeout != 5*time.Second || opts.WatchInterval != 2*time.Second {
		t.Fatalf("custom post verify options were not preserved: %#v", opts)
	}
}

func TestManagementAPIBaseFromHTTPAddrNormalizesLocalAddresses(t *testing.T) {
	cases := map[string]string{
		":18080":             "http://127.0.0.1:18080/api",
		"0.0.0.0:19090":      "http://127.0.0.1:19090/api",
		"[::]:28080":         "http://127.0.0.1:28080/api",
		"http://localhost:9": "http://localhost:9/api",
		"":                   defaultAPIBase,
	}
	for input, want := range cases {
		if got := managementAPIBaseFromHTTPAddr(input); got != want {
			t.Fatalf("managementAPIBaseFromHTTPAddr(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertArgsContain(t *testing.T, args []string, value string) {
	t.Helper()
	for _, arg := range args {
		if arg == value {
			return
		}
	}
	t.Fatalf("args %#v missing %q", args, value)
}

func assertArgsContainPair(t *testing.T, args []string, key string, value string) {
	t.Helper()
	for index, arg := range args {
		if arg == key && index+1 < len(args) && args[index+1] == value {
			return
		}
	}
	t.Fatalf("args %#v missing %s %s", args, key, value)
}
