package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func windowsHardenedServiceInstallTestArgs(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return nil
	}
	root := t.TempDir()
	return []string{
		"--windows-hardened",
		"--windows-install-dir", filepath.Join(root, "install"),
		"--windows-private-environment-file", filepath.Join(root, "config", "service-secrets.json"),
		"--management-auth-token", strings.Repeat("m", 32),
	}
}

func TestAutonomyRulePolicyResolvesRuleName(t *testing.T) {
	var patchedPath string
	var patchedPolicy string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/steward/autonomy/rules":
			_ = json.NewEncoder(w).Encode(map[string]any{"rules": []map[string]string{{"id": "rule-123", "name": "event-knowledge-summary"}}})
		case r.Method == http.MethodPatch:
			patchedPath = r.URL.Path
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			patchedPolicy, _ = body["policy"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{"rule": body})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := cli{apiBase: server.URL, client: server.Client()}
	if err := c.autonomy([]string{"rule-policy", "event-knowledge-summary", "auto"}); err != nil {
		t.Fatalf("set autonomy rule policy: %v", err)
	}
	if patchedPath != "/steward/autonomy/rules/rule-123" || patchedPolicy != "auto" {
		t.Fatalf("unexpected rule patch: path=%s policy=%s", patchedPath, patchedPolicy)
	}
}

func TestAutonomyModeRejectsUnknownValue(t *testing.T) {
	c := cli{}
	if err := c.autonomy([]string{"mode", "unrestricted"}); err == nil {
		t.Fatalf("expected unsupported autonomy mode to be rejected")
	}
}

func TestDevicesPermissionSetPreservesCurrentScopeAndMaxPermission(t *testing.T) {
	var patchedPath string
	var patchedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/steward/devices/macbook-main/permissions":
			_ = json.NewEncoder(w).Encode(map[string]any{"permissions": []map[string]string{{
				"device_id":            "macbook-main",
				"capability":           "sync.memory",
				"policy":               "allow",
				"max_permission_level": "A2",
				"scope_summary":        "同步记忆条目",
			}}})
		case r.Method == http.MethodPut:
			patchedPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&patchedBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"permission": patchedBody})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := cli{apiBase: server.URL, client: server.Client()}
	if err := c.devices([]string{"permission-set", "macbook-main", "sync.memory", "deny"}); err != nil {
		t.Fatalf("set device permission: %v", err)
	}
	if patchedPath != "/steward/devices/macbook-main/permissions/sync.memory" {
		t.Fatalf("unexpected permission patch path %s", patchedPath)
	}
	if patchedBody["policy"] != "deny" || patchedBody["max_permission_level"] != "A2" || patchedBody["scope_summary"] != "同步记忆条目" {
		t.Fatalf("unexpected permission patch body %#v", patchedBody)
	}
}

func TestDevicesPermissionSetRejectsUnknownPolicy(t *testing.T) {
	c := cli{}
	if err := c.devices([]string{"permission-set", "macbook-main", "sync.memory", "auto"}); err == nil {
		t.Fatalf("expected unsupported permission policy to be rejected")
	}
}

func TestDevicesPermissionsListsDevicePolicies(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"permissions": []map[string]string{{"capability": "sync.tasks"}}})
	}))
	defer server.Close()

	c := cli{apiBase: server.URL, client: server.Client()}
	if err := captureStdout(t, func() error {
		return c.devices([]string{"permissions", "macbook-main"})
	}); err != nil {
		t.Fatalf("list device permissions: %v", err)
	}
	if requestedPath != "/steward/devices/macbook-main/permissions" {
		t.Fatalf("unexpected permissions path %s", requestedPath)
	}
}

func TestPrintVersionIncludesBuildInfo(t *testing.T) {
	output, err := captureStdoutText(t, printVersion)
	if err != nil {
		t.Fatalf("print version: %v", err)
	}
	if !strings.Contains(output, `"name": "steward"`) || !strings.Contains(output, `"version":`) ||
		!strings.Contains(output, `"go_version": "go1.`) || !strings.Contains(output, `"goos":`) {
		t.Fatalf("version output missing expected fields: %s", output)
	}
}

func TestCLIHelpTopicsDoNotRequireAPI(t *testing.T) {
	c := cli{}
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "top-level topic", run: func() error { return c.run("help", []string{"service"}) }, want: "usage: steward service"},
		{name: "service", run: func() error { return c.service([]string{"--help"}) }, want: "service commands:"},
		{name: "service env", run: func() error { return serviceEnv([]string{"help"}) }, want: "service env commands:"},
		{name: "verify", run: func() error { return c.verify([]string{"--help"}) }, want: "verify commands:"},
		{name: "autonomy", run: func() error { return c.autonomy([]string{"help"}) }, want: "autonomy commands:"},
		{name: "devices", run: func() error { return c.devices([]string{"--help"}) }, want: "device commands:"},
		{name: "pairing", run: func() error { return c.pairing([]string{"help"}) }, want: "pairing commands:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := captureStdoutText(t, test.run)
			if err != nil {
				t.Fatalf("help failed: %v", err)
			}
			if !strings.Contains(output, test.want) {
				t.Fatalf("help output missing %q:\n%s", test.want, output)
			}
		})
	}
}

func TestServiceInstallAdvisorFlagsWriteRedactedEnvironment(t *testing.T) {
	output, err := captureStdoutText(t, func() error {
		args := []string{
			"--dry-run",
			"--name", "MongojsonStewardAdvisorTest",
			"--workdir", ".",
			"--ui-dir", ".",
			"--llm-provider", "openai-compatible",
			"--llm-base-url", "http://127.0.0.1:11434/v1",
			"--llm-model", "local-advisor",
			"--llm-api-key", "advisor-secret",
			"--llm-allow-no-api-key=false",
			"--llm-timeout", "3s",
			"--llm-max-data-level", "D0",
			"--llm-failure-threshold", "2",
			"--llm-failure-cooldown", "10s",
			"--autonomy-retry-max-attempts", "4",
			"--autonomy-retry-backoff", "30s",
			"--autonomy-retry-max-backoff", "15m",
		}
		return serviceInstall(append(args, windowsHardenedServiceInstallTestArgs(t)...))
	})
	if err != nil {
		t.Fatalf("service install dry-run: %v", err)
	}
	if strings.Contains(output, "advisor-secret") {
		t.Fatalf("dry-run output leaked advisor API key: %s", output)
	}

	var payload struct {
		Service struct {
			Environment map[string]string `json:"environment"`
		} `json:"service"`
		Verification *serviceEnvVerificationAdvice `json:"verification"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode service install output: %v\n%s", err, output)
	}
	env := payload.Service.Environment
	if env["STEWARD_LLM_PROVIDER"] != "openai-compatible" ||
		env["STEWARD_LLM_BASE_URL"] != "http://127.0.0.1:11434/v1" ||
		env["STEWARD_LLM_MODEL"] != "local-advisor" ||
		env["STEWARD_LLM_MAX_DATA_LEVEL"] != "D0" {
		t.Fatalf("advisor environment values missing from dry-run output: %#v", env)
	}
	if env["STEWARD_UI_DIR"] == "" {
		t.Fatalf("ui dir was not included in dry-run output: %#v", env)
	}
	if runtime.GOOS == "windows" {
		if _, exposed := env["STEWARD_LLM_API_KEY"]; exposed {
			t.Fatalf("hardened Windows plan exposed private advisor API key metadata: %#v", env)
		}
	} else if env["STEWARD_LLM_API_KEY"] != "<redacted>" {
		t.Fatalf("advisor API key redaction = %q, want <redacted>", env["STEWARD_LLM_API_KEY"])
	}
	if env["STEWARD_LLM_ALLOW_NO_API_KEY"] != "false" ||
		env["STEWARD_LLM_TIMEOUT"] != "3s" ||
		env["STEWARD_LLM_FAILURE_THRESHOLD"] != "2" ||
		env["STEWARD_LLM_FAILURE_COOLDOWN"] != "10s" {
		t.Fatalf("advisor tuning values missing from dry-run output: %#v", env)
	}
	if env["STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS"] != "4" ||
		env["STEWARD_AUTONOMY_RETRY_BACKOFF"] != "30s" ||
		env["STEWARD_AUTONOMY_RETRY_MAX_BACKOFF"] != "15m0s" {
		t.Fatalf("autonomy retry values missing from dry-run output: %#v", env)
	}
	if payload.Verification == nil {
		t.Fatalf("service install output should include verification advice")
	}
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-advisor-model", "local-advisor")
	if strings.Contains(payload.Verification.RuntimeCommand, "advisor-secret") {
		t.Fatalf("verification command leaked advisor API key: %s", payload.Verification.RuntimeCommand)
	}
}

func TestServiceInstallAdvisorEnvOmitsZeroTuningValues(t *testing.T) {
	env := serviceInstallAdvisorEnv(flag.NewFlagSet("test", flag.ContinueOnError), serviceInstallAdvisorFlagValues{})
	for _, key := range []string{"STEWARD_LLM_TIMEOUT", "STEWARD_LLM_FAILURE_THRESHOLD", "STEWARD_LLM_FAILURE_COOLDOWN"} {
		if _, ok := env[key]; ok {
			t.Fatalf("zero-value tuning key %s should be omitted: %#v", key, env)
		}
	}
}

func TestServiceInstallR3ValidatesAndRedactsBrokerConfiguration(t *testing.T) {
	clientKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x21}, 32))
	publicKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	orchestrationKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x63}, 32))
	output, err := captureStdoutText(t, func() error {
		args := []string{
			"--dry-run", "--name", "MongojsonStewardR3Test", "--workdir", ".", "--ui-dir", ".",
			"--runtime-r3", "--broker-url", "http://127.0.0.1:18100",
			"--broker-client-key", clientKey, "--broker-public-key", publicKey,
			"--orchestration-signing-key", orchestrationKey,
		}
		return serviceInstall(append(args, windowsHardenedServiceInstallTestArgs(t)...))
	})
	if err != nil {
		t.Fatalf("service install R3 dry-run: %v", err)
	}
	if strings.Contains(output, clientKey) {
		t.Fatalf("R3 dry-run leaked broker client key: %s", output)
	}
	var payload struct {
		Service struct {
			Environment map[string]string `json:"environment"`
		} `json:"service"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode R3 service output: %v\n%s", err, output)
	}
	env := payload.Service.Environment
	brokerClientKeyProtected := env["STEWARD_BROKER_CLIENT_KEY"] == "<redacted>"
	if runtime.GOOS == "windows" {
		_, brokerClientKeyProtected = env["STEWARD_BROKER_CLIENT_KEY"]
		brokerClientKeyProtected = !brokerClientKeyProtected
	}
	if env["STEWARD_RUNTIME_R3"] != "true" || env["STEWARD_RUNTIME_V2"] != "true" ||
		env["STEWARD_BROKER_URL"] != "http://127.0.0.1:18100" || !brokerClientKeyProtected ||
		env["STEWARD_BROKER_PUBLIC_KEY"] != publicKey {
		t.Fatalf("unexpected R3 service environment: %#v", env)
	}

	err = serviceInstall([]string{
		"--dry-run", "--runtime-r3", "--broker-url", "http://192.0.2.10:18100",
		"--broker-client-key", clientKey, "--broker-public-key", publicKey,
		"--orchestration-signing-key", orchestrationKey,
	})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback R3 broker was accepted: %v", err)
	}
}

func TestServiceInstallGeneratesAndRedactsOrchestrationKey(t *testing.T) {
	output, err := captureStdoutText(t, func() error {
		args := []string{"--dry-run", "--runtime-v2"}
		return serviceInstall(append(args, windowsHardenedServiceInstallTestArgs(t)...))
	})
	if err != nil {
		t.Fatalf("runtime-v2 did not generate an orchestration key: %v", err)
	}
	var payload struct {
		Service struct {
			Environment map[string]string `json:"environment"`
		} `json:"service"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode service install output: %v\n%s", err, output)
	}
	if runtime.GOOS == "windows" {
		if _, exposed := payload.Service.Environment["STEWARD_ORCHESTRATION_SIGNING_KEY"]; exposed {
			t.Fatalf("hardened Windows plan exposed generated orchestration key metadata: %#v", payload.Service.Environment)
		}
	} else if payload.Service.Environment["STEWARD_ORCHESTRATION_SIGNING_KEY"] != "<redacted>" {
		t.Fatalf("generated orchestration key was not redacted: %#v", payload.Service.Environment)
	}
	if err := serviceInstall([]string{"--dry-run", "--runtime-v2", "--orchestration-signing-key", "not-base64"}); err == nil ||
		!strings.Contains(err.Error(), "32-byte Ed25519 seed") {
		t.Fatalf("runtime-v2 accepted invalid orchestration key: %v", err)
	}
}

func TestServiceInstallWindowsProductionIsolationPlan(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows production service")
	}
	root := t.TempDir()
	output, err := captureStdoutText(t, func() error {
		return serviceInstall([]string{
			"--dry-run", "--name", "MongojsonStewardR51Test", "--workdir", root,
			"--storage-dir", filepath.Join(root, "data"), "--log-dir", filepath.Join(root, "logs"),
			"--database-url", "postgres://private@127.0.0.1:5432/private",
			"--windows-hardened", "--windows-install-dir", filepath.Join(root, "install"),
			"--windows-private-environment-file", filepath.Join(root, "config", "service-secrets.json"),
			"--windows-service-account", "localservice", "--windows-service-sid-type", "restricted",
			"--management-auth-token", "test-management-token-with-at-least-32-characters",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "postgres://private") {
		t.Fatalf("private database URL leaked from production install plan: %s", output)
	}
	var payload struct {
		Service struct {
			Commands    []string          `json:"commands"`
			Environment map[string]string `json:"environment"`
		} `json:"service"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatal(err)
	}
	commands := strings.Join(payload.Service.Commands, "\n")
	if !strings.Contains(commands, "service account localservice") || !strings.Contains(commands, "restricted per-service SID") {
		t.Fatalf("production identity plan is incomplete: %s", commands)
	}
	if _, leaked := payload.Service.Environment["DATABASE_URL"]; leaked {
		t.Fatalf("DATABASE_URL must be stored only in the private environment file: %#v", payload.Service.Environment)
	}
	if payload.Service.Environment["STEWARD_RESTRICTED_SERVICE"] != "true" {
		t.Fatalf("restricted runtime marker is missing: %#v", payload.Service.Environment)
	}
}

func TestConfigureServiceLoggingWritesSanitizedLogFile(t *testing.T) {
	logDir := t.TempDir()
	logName := serviceLogFileName("Service/Name:*")
	cleanup, err := configureServiceLogging(logDir, "Service/Name:*")
	if err != nil {
		t.Fatalf("configure service logging: %v", err)
	}
	log.Print("runtime evidence marker")
	cleanup()

	if strings.ContainsAny(logName, `\/:*?"<>|`) {
		t.Fatalf("log file name was not sanitized: %q", logName)
	}
	data, err := os.ReadFile(filepath.Join(logDir, logName))
	if err != nil {
		t.Fatalf("read service log file: %v", err)
	}
	if !strings.Contains(string(data), "runtime evidence marker") {
		t.Fatalf("service log did not contain marker: %s", string(data))
	}
}

func captureStdout(t *testing.T, fn func() error) error {
	_, err := captureStdoutText(t, fn)
	return err
}

func captureStdoutText(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buffer bytes.Buffer
	original := stdout
	stdout = &buffer
	t.Cleanup(func() { stdout = original })
	err := fn()
	return buffer.String(), err
}
