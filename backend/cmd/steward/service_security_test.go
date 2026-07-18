package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

func TestValidateStrictServiceSecurityAcceptsCompleteConfig(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	if err := validateStrictServiceSecurity(options); err != nil {
		t.Fatalf("strict security validation failed: %v", err)
	}
}

func TestValidateStrictServiceSecurityRejectsDefaultAgentID(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.AgentID = servicecontrol.DefaultName()
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "STEWARD_AGENT_ID") {
		t.Fatalf("error = %v, want agent id validation failure", err)
	}
}

func TestValidateStrictServiceSecurityRejectsMismatchedDeviceKeys(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(otherPublicKey, privateKey)
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v, want mismatched device key validation failure", err)
	}
}

func TestValidateStrictServiceSecurityRejectsInvalidPreviousKeys(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.LocalEncryptionPreviousKeys = "missing-key-material"
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS") {
		t.Fatalf("error = %v, want previous key validation failure", err)
	}
}

func TestValidateStrictServiceSecurityRejectsSharedRemoteManagementListener(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.HTTPAddr = ":18080"
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "management API") {
		t.Fatalf("error = %v, want management listener boundary failure", err)
	}
}

func TestValidateStrictServiceSecurityRequiresAdvertisedPeerListener(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.PeerHTTPAddr = ""
	options.PublicAPIBase = ""
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "STEWARD_PEER_HTTP_ADDR") || !strings.Contains(err.Error(), "STEWARD_PUBLIC_API_BASE") {
		t.Fatalf("error = %v, want peer listener and advertised URL failures", err)
	}
}

func TestValidateStrictServiceSecurityRejectsUnsafeAdvisorConfig(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.ExtraEnv = map[string]string{
		"STEWARD_LLM_PROVIDER":       "openai-compatible",
		"STEWARD_LLM_BASE_URL":       "https://api.openai.com/v1",
		"STEWARD_LLM_MODEL":          "advisor-model",
		"STEWARD_LLM_API_KEY":        "advisor-key",
		"STEWARD_LLM_MAX_DATA_LEVEL": "D7",
	}
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "STEWARD_LLM_MAX_DATA_LEVEL") {
		t.Fatalf("error = %v, want advisor max data level validation failure", err)
	}
}

func TestValidateStrictServiceSecurityRejectsRemoteNoKeyAdvisor(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.ExtraEnv = map[string]string{
		"STEWARD_LLM_PROVIDER":         "openai-compatible",
		"STEWARD_LLM_BASE_URL":         "https://example.com/v1",
		"STEWARD_LLM_MODEL":            "advisor-model",
		"STEWARD_LLM_ALLOW_NO_API_KEY": "true",
		"STEWARD_LLM_MAX_DATA_LEVEL":   "D1",
	}
	err = validateStrictServiceSecurity(options)
	if err == nil || !strings.Contains(err.Error(), "STEWARD_LLM_ALLOW_NO_API_KEY") {
		t.Fatalf("error = %v, want remote no-key advisor validation failure", err)
	}
}

func TestValidateStrictServiceSecurityAcceptsLoopbackNoKeyAdvisor(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.ExtraEnv = map[string]string{
		"STEWARD_LLM_PROVIDER":          "openai-compatible",
		"STEWARD_LLM_BASE_URL":          "http://127.0.0.1:11434/v1",
		"STEWARD_LLM_MODEL":             "advisor-model",
		"STEWARD_LLM_ALLOW_NO_API_KEY":  "true",
		"STEWARD_LLM_MAX_DATA_LEVEL":    "D1",
		"STEWARD_LLM_TIMEOUT":           "20s",
		"STEWARD_LLM_FAILURE_THRESHOLD": "3",
		"STEWARD_LLM_FAILURE_COOLDOWN":  "1m",
	}
	if err := validateStrictServiceSecurity(options); err != nil {
		t.Fatalf("strict loopback advisor validation failed: %v", err)
	}
}

func TestServiceEnvPlanRejectsRestart(t *testing.T) {
	err := serviceEnv([]string{"plan", "--restart", "--set", "STEWARD_SYNC_INTERVAL=5m"})
	if err == nil || !strings.Contains(err.Error(), "does not support --restart") {
		t.Fatalf("expected plan restart rejection, got %v", err)
	}
}

func TestServiceEnvPlanRejectsVerify(t *testing.T) {
	err := serviceEnv([]string{"plan", "--verify", "--set", "STEWARD_SYNC_INTERVAL=5m"})
	if err == nil || !strings.Contains(err.Error(), "does not support --verify") {
		t.Fatalf("expected plan verify rejection, got %v", err)
	}
}

func TestServiceEnvPlanFromCurrentEnvFileRotatesKeysWithoutServiceManager(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	options := strictServiceSecurityFixture(publicKey, privateKey)
	env := servicecontrol.Environment(options)
	env["STEWARD_LLM_PROVIDER"] = "openai-compatible"
	env["STEWARD_LLM_BASE_URL"] = "http://127.0.0.1:11434/v1"
	env["STEWARD_LLM_MODEL"] = "advisor-model"
	env["STEWARD_LLM_ALLOW_NO_API_KEY"] = "true"
	env["STEWARD_LLM_MAX_DATA_LEVEL"] = "D1"

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/current-env.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdoutText(t, func() error {
		return serviceEnv([]string{
			"plan",
			"--name", "MongojsonStewardTest",
			"--current-env-file", path,
			"--rotate-sync-key-id", "sync-v2",
			"--rotate-local-key-id", "local-v2",
			"--strict-security",
		})
	})
	if err != nil {
		t.Fatalf("service env plan from current env file: %v", err)
	}
	for _, secret := range []string{
		options.SyncSecret,
		options.DevicePrivateKey,
		options.SyncEncryptionKey,
		options.LocalEncryptionKey,
		env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"],
	} {
		if secret != "" && strings.Contains(output, secret) {
			t.Fatalf("plan output leaked secret %q: %s", secret, output)
		}
	}

	var payload serviceEnvApplyOutput
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode service env plan output: %v\n%s", err, output)
	}
	if payload.ServiceEnv.Message == "" || !strings.Contains(payload.ServiceEnv.Message, "explicit current environment") {
		t.Fatalf("plan did not use explicit current environment: %#v", payload.ServiceEnv)
	}
	envOut := payload.ServiceEnv.Environment
	if envOut["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-v2" ||
		envOut["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] != "local-v2" {
		t.Fatalf("rotation key ids missing from output: %#v", envOut)
	}
	if envOut["STEWARD_SYNC_ENCRYPTION_KEY"] != "<redacted>" ||
		envOut["STEWARD_LOCAL_ENCRYPTION_KEY"] != "<redacted>" ||
		envOut["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != "<redacted>" {
		t.Fatalf("rotated key material was not redacted: %#v", envOut)
	}
	if payload.Verification == nil {
		t.Fatalf("expected verification advice")
	}
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-sync-key-id", "sync-v2")
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-local-key-id", "local-v2")
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-advisor-model", "advisor-model")
}

func TestServicePlanFromCurrentEnvFileRendersAllTargetPlatforms(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	options := strictServiceSecurityFixture(publicKey, privateKey)
	options.LogDir = t.TempDir()
	env := servicecontrol.Environment(options)
	env["STEWARD_LLM_PROVIDER"] = "openai-compatible"
	env["STEWARD_LLM_BASE_URL"] = "http://127.0.0.1:11434/v1"
	env["STEWARD_LLM_MODEL"] = "advisor-model"
	env["STEWARD_LLM_ALLOW_NO_API_KEY"] = "true"
	env["STEWARD_LLM_MAX_DATA_LEVEL"] = "D1"

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/current-env.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdoutText(t, func() error {
		return servicePlan([]string{
			"--current-env-file", path,
			"--target", "windows,darwin,linux",
			"--binary", "C:/tools/steward.exe",
			"--workdir", "C:/tools/steward",
			"--log-dir", "C:/tools/steward/logs",
			"--strict-security",
		})
	})
	if err != nil {
		t.Fatalf("service plan: %v", err)
	}
	for _, secret := range []string{
		options.SyncSecret,
		options.DevicePrivateKey,
		options.SyncEncryptionKey,
		options.LocalEncryptionKey,
		env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"],
	} {
		if secret != "" && strings.Contains(output, secret) {
			t.Fatalf("service plan leaked secret %q: %s", secret, output)
		}
	}

	var payload servicePlanOutput
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode service plan output: %v\n%s", err, output)
	}
	if len(payload.Plans) != 3 {
		t.Fatalf("plans = %d, want 3: %#v", len(payload.Plans), payload.Plans)
	}
	platforms := map[string]servicecontrol.InstallPlan{}
	for _, plan := range payload.Plans {
		platforms[plan.Platform] = plan
		if plan.Environment["STEWARD_SYNC_ENCRYPTION_KEY"] != "<redacted>" ||
			plan.Environment["STEWARD_DEVICE_PRIVATE_KEY"] != "<redacted>" {
			t.Fatalf("plan did not redact sensitive env for %s: %#v", plan.Platform, plan.Environment)
		}
	}
	if platforms["windows"].Artifacts["service_type"] != "Windows Service" ||
		!strings.Contains(platforms["darwin"].Artifacts["plist"], "<key>KeepAlive</key>") ||
		!strings.Contains(platforms["linux"].Artifacts["systemd_unit"], "Restart=always") {
		t.Fatalf("missing expected platform artifacts: %#v", platforms)
	}
	if payload.Verification == nil {
		t.Fatalf("expected verification advice")
	}
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-agent-id", options.AgentID)
	assertArgsContainPair(t, payload.Verification.RuntimeArgs, "--expect-advisor-model", "advisor-model")
	if len(payload.VerificationByPlatform) != 3 {
		t.Fatalf("verification_by_platform = %#v, want 3 platforms", payload.VerificationByPlatform)
	}
	for _, platform := range []string{"windows", "darwin", "linux"} {
		advice := payload.VerificationByPlatform[platform]
		if advice == nil {
			t.Fatalf("missing verification advice for %s: %#v", platform, payload.VerificationByPlatform)
		}
		assertArgsContainPair(t, advice.RuntimeArgs, "--expect-agent-platform", platform)
	}
}

func TestServiceEnvApplyVerifyRequiresRestart(t *testing.T) {
	err := serviceEnv([]string{"apply", "--confirm", "--verify", "--set", "STEWARD_SYNC_INTERVAL=5m"})
	if err == nil || !strings.Contains(err.Error(), "requires --restart") {
		t.Fatalf("expected apply verify restart requirement, got %v", err)
	}
}

func TestServiceInstallRejectsStartAndVerifyWithDryRun(t *testing.T) {
	err := serviceInstall([]string{"--dry-run", "--start"})
	if err == nil || !strings.Contains(err.Error(), "cannot be used with --dry-run") {
		t.Fatalf("expected dry-run start rejection, got %v", err)
	}
	err = serviceInstall([]string{"--dry-run", "--verify"})
	if err == nil || !strings.Contains(err.Error(), "cannot be used with --dry-run") {
		t.Fatalf("expected dry-run verify rejection, got %v", err)
	}
}

func TestServiceInstallOptionsFromEnvSupportsStrictValidation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := strictServiceSecurityFixture(publicKey, privateKey)
	env := servicecontrol.Environment(fixture)
	env["STEWARD_MANAGEMENT_AUTH_REQUIRED"] = "true"
	env["STEWARD_MANAGEMENT_AUTH_TOKEN"] = "management-token-0123456789abcdef"
	env["STEWARD_ORCHESTRATION_SIGNING_KEY"] = "orchestration-seed"
	env["STEWARD_RUNTIME_V2"] = "true"
	env["STEWARD_OWNER_MODE"] = "true"
	env["STEWARD_ORCHESTRATION_REMOTE"] = "true"
	env["STEWARD_ALLOW_REMOTE_MANAGEMENT"] = "true"
	env["STEWARD_RESTRICTED_SERVICE"] = "true"
	env["STEWARD_API_TIMEOUT"] = "45s"
	env["STEWARD_PAIRING_PUBLIC_KEY"] = "pairing-public-key"

	options := serviceInstallOptionsFromEnv("MongojsonSteward", env)
	if options.UIDir != fixture.UIDir {
		t.Fatalf("ui dir from service env = %q, want %q", options.UIDir, fixture.UIDir)
	}
	for _, key := range []string{
		"STEWARD_MANAGEMENT_AUTH_REQUIRED", "STEWARD_MANAGEMENT_AUTH_TOKEN",
		"STEWARD_ORCHESTRATION_SIGNING_KEY", "STEWARD_RUNTIME_V2",
		"STEWARD_OWNER_MODE", "STEWARD_ORCHESTRATION_REMOTE",
		"STEWARD_ALLOW_REMOTE_MANAGEMENT", "STEWARD_RESTRICTED_SERVICE",
		"STEWARD_API_TIMEOUT", "STEWARD_PAIRING_PUBLIC_KEY",
	} {
		if options.ExtraEnv[key] != env[key] {
			t.Fatalf("runtime security env %s was lost: got %q want %q", key, options.ExtraEnv[key], env[key])
		}
	}
	if err := validateStrictServiceSecurity(options); err != nil {
		t.Fatalf("strict validation from service env failed: %v", err)
	}

	delete(env, "STEWARD_SYNC_SECRET")
	err = validateStrictServiceSecurity(serviceInstallOptionsFromEnv("MongojsonSteward", env))
	if err == nil || !strings.Contains(err.Error(), "STEWARD_SYNC_SECRET") {
		t.Fatalf("expected missing sync secret to fail strict env validation, got %v", err)
	}

	env = servicecontrol.Environment(fixture)
	env["STEWARD_LLM_PROVIDER"] = "openai-compatible"
	env["STEWARD_LLM_BASE_URL"] = "https://api.openai.com/v1"
	env["STEWARD_LLM_MODEL"] = "advisor-model"
	env["STEWARD_LLM_API_KEY"] = "advisor-key"
	env["STEWARD_LLM_MAX_DATA_LEVEL"] = "D7"
	err = validateStrictServiceSecurity(serviceInstallOptionsFromEnv("MongojsonSteward", env))
	if err == nil || !strings.Contains(err.Error(), "STEWARD_LLM_MAX_DATA_LEVEL") {
		t.Fatalf("expected advisor env to participate in strict validation, got %v", err)
	}
}

func TestValidateStrictAutonomyRetryEnvironment(t *testing.T) {
	valid := map[string]string{
		"STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS": "3",
		"STEWARD_AUTONOMY_RETRY_BACKOFF":      "5m",
		"STEWARD_AUTONOMY_RETRY_MAX_BACKOFF":  "1h",
	}
	if err := validateStrictAutonomyRetryEnvironment(valid); err != nil {
		t.Fatalf("valid autonomy retry environment rejected: %v", err)
	}

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "attempt limit",
			env:  map[string]string{"STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS": "0"},
			want: "integer from 1 to 20",
		},
		{
			name: "invalid backoff",
			env:  map[string]string{"STEWARD_AUTONOMY_RETRY_BACKOFF": "later"},
			want: "duration greater than 0",
		},
		{
			name: "max below initial",
			env: map[string]string{
				"STEWARD_AUTONOMY_RETRY_BACKOFF":     "1h",
				"STEWARD_AUTONOMY_RETRY_MAX_BACKOFF": "5m",
			},
			want: "must be greater than or equal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStrictAutonomyRetryEnvironment(test.env)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServiceRestartDryRun(t *testing.T) {
	if err := serviceSimpleAction([]string{"--name", "MongojsonStewardTest", "--dry-run"}, "restart"); err != nil {
		t.Fatalf("restart dry run failed: %v", err)
	}
}

func strictServiceSecurityFixture(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey) servicecontrol.InstallOptions {
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	localKey := base64.StdEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
	previousKey := "old-key:" + base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	return servicecontrol.InstallOptions{
		Name:                        "MongojsonSteward",
		BinaryPath:                  "steward.exe",
		WorkDir:                     ".",
		HTTPAddr:                    "127.0.0.1:18080",
		PeerHTTPAddr:                ":18081",
		DatabaseURL:                 "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",
		StorageDir:                  "./data",
		UIDir:                       "./ui",
		AgentID:                     "windows-main",
		PublicAPIBase:               "http://192.0.2.10:18081/api",
		SyncSecret:                  "0123456789abcdef01234567",
		DevicePrivateKey:            base64.StdEncoding.EncodeToString(privateKey),
		DevicePublicKey:             base64.StdEncoding.EncodeToString(publicKey),
		SyncEncryptionKey:           syncKey,
		SyncEncryptionKeyID:         "home-sync-v1",
		SyncEncryptionPreviousKeys:  previousKey,
		LocalEncryptionKey:          localKey,
		LocalEncryptionKeyID:        "windows-local-v1",
		LocalEncryptionPreviousKeys: previousKey,
		HeartbeatInterval:           time.Minute,
		SyncInterval:                5 * time.Minute,
		AutonomyInterval:            15 * time.Minute,
	}
}
