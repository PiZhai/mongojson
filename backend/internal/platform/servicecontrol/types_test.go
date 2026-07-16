package servicecontrol

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentIncludesRuntimeIntervalsAndRedactsSecret(t *testing.T) {
	options, err := NormalizeInstallOptions(InstallOptions{
		Name:                        "MongojsonStewardTest",
		BinaryPath:                  os.Args[0],
		WorkDir:                     ".",
		HTTPAddr:                    "127.0.0.1:18080",
		PeerHTTPAddr:                ":18081",
		DatabaseURL:                 "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",
		StorageDir:                  "./data",
		UIDir:                       "./ui",
		AgentID:                     "test-device",
		SyncSecret:                  "shared-secret",
		DevicePrivateKey:            "private-key-material",
		DevicePublicKey:             "public-key-material",
		SyncEncryptionKey:           "sync-encryption-key-material",
		SyncEncryptionKeyID:         "sync-key-v1",
		SyncEncryptionPreviousKeys:  "sync-key-v0:previous-key-material",
		LocalEncryptionKey:          "local-encryption-key-material",
		LocalEncryptionKeyID:        "local-key-v1",
		LocalEncryptionPreviousKeys: "local-key-v0:previous-local-key-material",
		HeartbeatInterval:           time.Second,
		SyncInterval:                5 * time.Minute,
		AutonomyInterval:            15 * time.Minute,
		LogDir:                      "./logs",
		ExtraEnv: map[string]string{
			" STEWARD_LLM_MODEL ":          "local-model",
			"STEWARD_LLM_ALLOW_NO_API_KEY": "true",
			"STEWARD_LLM_API_KEY":          "advisor-api-key",
			"STEWARD_LLM_PROVIDER":         "openai-compatible",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	env := Environment(options)
	if env["HTTP_ADDR"] != "127.0.0.1:18080" || env["STEWARD_PEER_HTTP_ADDR"] != ":18081" {
		t.Fatalf("split listener environment was not included: %#v", env)
	}
	if env["STEWARD_SYNC_SECRET"] != "shared-secret" {
		t.Fatalf("sync secret was not included in service environment")
	}
	if env["STEWARD_DEVICE_PRIVATE_KEY"] != "private-key-material" {
		t.Fatalf("device private key was not included in service environment")
	}
	if env["STEWARD_DEVICE_PUBLIC_KEY"] != "public-key-material" {
		t.Fatalf("device public key was not included in service environment")
	}
	if env["STEWARD_SYNC_ENCRYPTION_KEY"] != "sync-encryption-key-material" {
		t.Fatalf("sync encryption key was not included in service environment")
	}
	if env["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-key-v1" {
		t.Fatalf("sync encryption key id = %q, want sync-key-v1", env["STEWARD_SYNC_ENCRYPTION_KEY_ID"])
	}
	if env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != "sync-key-v0:previous-key-material" {
		t.Fatalf("sync previous encryption keys = %q, want sync-key-v0:previous-key-material", env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"])
	}
	if env["STEWARD_LOCAL_ENCRYPTION_KEY"] != "local-encryption-key-material" {
		t.Fatalf("local encryption key was not included in service environment")
	}
	if env["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] != "local-key-v1" {
		t.Fatalf("local encryption key id = %q, want local-key-v1", env["STEWARD_LOCAL_ENCRYPTION_KEY_ID"])
	}
	if env["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"] != "local-key-v0:previous-local-key-material" {
		t.Fatalf("local previous encryption keys = %q, want local-key-v0:previous-local-key-material", env["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"])
	}
	if env["STEWARD_HEARTBEAT_INTERVAL"] != "1s" {
		t.Fatalf("heartbeat interval = %q, want 1s", env["STEWARD_HEARTBEAT_INTERVAL"])
	}
	if env["STEWARD_SYNC_INTERVAL"] != "5m0s" {
		t.Fatalf("sync interval = %q, want 5m0s", env["STEWARD_SYNC_INTERVAL"])
	}
	if env["STEWARD_AUTONOMY_INTERVAL"] != "15m0s" {
		t.Fatalf("autonomy interval = %q, want 15m0s", env["STEWARD_AUTONOMY_INTERVAL"])
	}
	if env["STEWARD_LOG_DIR"] == "" {
		t.Fatalf("log dir was not included in service environment")
	}
	if env["STEWARD_UI_DIR"] == "" {
		t.Fatalf("ui dir was not included in service environment")
	}
	if env["STEWARD_LLM_PROVIDER"] != "openai-compatible" || env["STEWARD_LLM_MODEL"] != "local-model" {
		t.Fatalf("advisor extra env was not included: %#v", env)
	}

	redacted := redactedEnvironment(env)
	if redacted["STEWARD_SYNC_SECRET"] != "<redacted>" {
		t.Fatalf("sync secret redaction = %q, want <redacted>", redacted["STEWARD_SYNC_SECRET"])
	}
	if redacted["STEWARD_DEVICE_PRIVATE_KEY"] != "<redacted>" {
		t.Fatalf("device private key redaction = %q, want <redacted>", redacted["STEWARD_DEVICE_PRIVATE_KEY"])
	}
	if redacted["STEWARD_DEVICE_PUBLIC_KEY"] != "public-key-material" {
		t.Fatalf("device public key redaction = %q, want public-key-material", redacted["STEWARD_DEVICE_PUBLIC_KEY"])
	}
	if redacted["STEWARD_SYNC_ENCRYPTION_KEY"] != "<redacted>" {
		t.Fatalf("sync encryption key redaction = %q, want <redacted>", redacted["STEWARD_SYNC_ENCRYPTION_KEY"])
	}
	if redacted["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-key-v1" {
		t.Fatalf("sync encryption key id redaction = %q, want sync-key-v1", redacted["STEWARD_SYNC_ENCRYPTION_KEY_ID"])
	}
	if redacted["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != "<redacted>" {
		t.Fatalf("sync previous encryption keys redaction = %q, want <redacted>", redacted["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"])
	}
	if redacted["STEWARD_LOCAL_ENCRYPTION_KEY"] != "<redacted>" {
		t.Fatalf("local encryption key redaction = %q, want <redacted>", redacted["STEWARD_LOCAL_ENCRYPTION_KEY"])
	}
	if redacted["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] != "local-key-v1" {
		t.Fatalf("local encryption key id redaction = %q, want local-key-v1", redacted["STEWARD_LOCAL_ENCRYPTION_KEY_ID"])
	}
	if redacted["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"] != "<redacted>" {
		t.Fatalf("local previous encryption keys redaction = %q, want <redacted>", redacted["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"])
	}
	if redacted["DATABASE_URL"] != "<redacted>" {
		t.Fatalf("database URL redaction = %q, want <redacted>", redacted["DATABASE_URL"])
	}
	if redacted["STEWARD_LLM_API_KEY"] != "<redacted>" {
		t.Fatalf("advisor API key redaction = %q, want <redacted>", redacted["STEWARD_LLM_API_KEY"])
	}
	if redacted["STEWARD_LLM_ALLOW_NO_API_KEY"] != "true" {
		t.Fatalf("advisor allow-no-key redaction = %q, want true", redacted["STEWARD_LLM_ALLOW_NO_API_KEY"])
	}
}

func TestPlanInstallUsesExplicitServiceArgsAndRedactsBrokerKeys(t *testing.T) {
	options := InstallOptions{
		Name: "MongojsonStewardBroker", Scope: ScopeSystem,
		BinaryPath: `C:\Program Files\Mongojson\steward-broker.exe`, WorkDir: `C:\ProgramData\Mongojson\broker`,
		HTTPAddr: "127.0.0.1:18100", PeerHTTPAddr: "127.0.0.1:18101",
		ServiceArgs: []string{"run", "--service-name", "MongojsonStewardBroker", "--workdir", `C:\ProgramData\Mongojson\broker`},
		ExtraEnv: map[string]string{
			"STEWARD_BROKER_CLIENT_KEY":          "client-secret",
			"STEWARD_BROKER_CONTROL_KEY":         "control-secret",
			"STEWARD_BROKER_SIGNING_PRIVATE_KEY": "signing-secret",
		},
	}
	plan, err := PlanInstall("windows", options)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RunArgs) < 2 || plan.RunArgs[1] != "run" {
		t.Fatalf("explicit service args were not used: %+v", plan.RunArgs)
	}
	if plan.Environment["STEWARD_BROKER_CLIENT_KEY"] != "<redacted>" || plan.Environment["STEWARD_BROKER_CONTROL_KEY"] != "<redacted>" || plan.Environment["STEWARD_BROKER_SIGNING_PRIVATE_KEY"] != "<redacted>" {
		t.Fatalf("broker keys were not redacted: %+v", plan.Environment)
	}
}

func TestNormalizeInstallOptionsRejectsInvalidExtraEnvKey(t *testing.T) {
	_, err := NormalizeInstallOptions(InstallOptions{
		Name:       "MongojsonStewardTest",
		BinaryPath: os.Args[0],
		WorkDir:    ".",
		ExtraEnv:   map[string]string{"BAD-NAME": "value"},
	})
	if err == nil {
		t.Fatalf("expected invalid extra env key to fail")
	}
}

func TestExplicitEnvironmentOmitsGenericServiceVariables(t *testing.T) {
	options, err := NormalizeInstallOptionsForPlatform("windows", InstallOptions{
		Name: "Broker", Scope: ScopeSystem, BinaryPath: os.Args[0], WorkDir: ".",
		ExplicitEnvironment: map[string]string{"STEWARD_BROKER_LISTEN": "127.0.0.1:18100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := Environment(options)
	if len(env) != 1 || env["STEWARD_BROKER_LISTEN"] == "" {
		t.Fatalf("explicit environment was expanded: %+v", env)
	}
}

func TestNormalizeEnvPatchOptionsAndPatchEnvironment(t *testing.T) {
	options, err := NormalizeEnvPatchOptions(EnvPatchOptions{
		Name: "MongojsonStewardTest",
		Set: map[string]string{
			" STEWARD_SYNC_ENCRYPTION_KEY ":  "new-key",
			"STEWARD_SYNC_ENCRYPTION_KEY_ID": "sync-v2",
		},
		Remove: []string{"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "MongojsonStewardTest" {
		t.Fatalf("name = %q", options.Name)
	}
	if len(options.Remove) != 1 || options.Remove[0] != "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS" {
		t.Fatalf("remove list was not normalized: %#v", options.Remove)
	}

	next := patchEnvironment(map[string]string{
		"HTTP_ADDR":                             ":18080",
		"STEWARD_SYNC_ENCRYPTION_KEY":           "old-key",
		"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS": "sync-v1:old",
	}, options.Set, options.Remove)
	if next["HTTP_ADDR"] != ":18080" {
		t.Fatalf("existing env was not preserved: %#v", next)
	}
	if next["STEWARD_SYNC_ENCRYPTION_KEY"] != "new-key" || next["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-v2" {
		t.Fatalf("set values were not applied: %#v", next)
	}
	if _, ok := next["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"]; ok {
		t.Fatalf("removed value still present: %#v", next)
	}

	transformOnly, err := NormalizeEnvPatchOptions(EnvPatchOptions{
		Name: "MongojsonStewardTest",
		TransformTarget: func(current map[string]string, target map[string]string) error {
			target["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] = current["STEWARD_SYNC_ENCRYPTION_KEY_ID"] + ":" + current["STEWARD_SYNC_ENCRYPTION_KEY"]
			target["STEWARD_SYNC_ENCRYPTION_KEY_ID"] = "sync-v3"
			target["STEWARD_SYNC_ENCRYPTION_KEY"] = "generated-key"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := buildEnvPatchTarget(map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY":    "sync-key-v2-material",
		"STEWARD_SYNC_ENCRYPTION_KEY_ID": "sync-v2",
	}, transformOnly)
	if err != nil {
		t.Fatal(err)
	}
	if rotated["STEWARD_SYNC_ENCRYPTION_KEY"] != "generated-key" ||
		rotated["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-v3" ||
		rotated["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != "sync-v2:sync-key-v2-material" {
		t.Fatalf("transform target was not applied: %#v", rotated)
	}
}

func TestNormalizeEnvPatchOptionsRejectsInvalidKeys(t *testing.T) {
	_, err := NormalizeEnvPatchOptions(EnvPatchOptions{
		Set: map[string]string{"BAD-NAME": "value"},
	})
	if err == nil {
		t.Fatalf("expected invalid key to fail")
	}

	_, err = NormalizeEnvPatchOptions(EnvPatchOptions{})
	if err == nil {
		t.Fatalf("expected empty patch to fail")
	}
}

func TestValidateTargetEnvironmentUsesIsolatedCopy(t *testing.T) {
	target := map[string]string{"STEWARD_AGENT_ID": "windows-main"}
	err := validateTargetEnvironment(target, func(env map[string]string) error {
		env["STEWARD_AGENT_ID"] = "mutated"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if target["STEWARD_AGENT_ID"] != "windows-main" {
		t.Fatalf("target environment was mutated: %#v", target)
	}
}

func TestPlanInstallRendersSystemScopeArtifacts(t *testing.T) {
	base := InstallOptions{
		Scope:        ScopeSystem,
		Name:         "mongojson-steward",
		BinaryPath:   os.Args[0],
		WorkDir:      ".",
		HTTPAddr:     "127.0.0.1:18080",
		PeerHTTPAddr: ":18081",
		DatabaseURL:  "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",
		StorageDir:   "./data",
		SyncSecret:   "plan-secret-must-be-redacted",
	}

	darwinPlan, err := PlanInstall("darwin", base)
	if err != nil {
		t.Fatalf("darwin system plan: %v", err)
	}
	if darwinPlan.Scope != ScopeSystem || len(darwinPlan.Files) != 1 || darwinPlan.Files[0] != "/Library/LaunchDaemons/mongojson-steward.plist" {
		t.Fatalf("unexpected darwin system plan: %#v", darwinPlan)
	}
	if !strings.Contains(strings.Join(darwinPlan.Commands, "\n"), "launchctl bootstrap system") ||
		!strings.Contains(darwinPlan.Artifacts["service_type"], "LaunchDaemon") || darwinPlan.Artifacts["plist_mode"] != "0600" {
		t.Fatalf("darwin system plan did not target LaunchDaemon: %#v", darwinPlan)
	}

	linuxPlan, err := PlanInstall("linux", base)
	if err != nil {
		t.Fatalf("linux system plan: %v", err)
	}
	if linuxPlan.Scope != ScopeSystem || len(linuxPlan.Files) != 2 || linuxPlan.Files[0] != "/etc/systemd/system/mongojson-steward.service" ||
		linuxPlan.Files[1] != "/etc/mongojson-steward/mongojson-steward.service.env" {
		t.Fatalf("unexpected linux system plan: %#v", linuxPlan)
	}
	if !strings.Contains(strings.Join(linuxPlan.Commands, "\n"), "systemctl daemon-reload") ||
		!strings.Contains(linuxPlan.Artifacts["systemd_unit"], "WantedBy=multi-user.target") ||
		!strings.Contains(linuxPlan.Artifacts["systemd_unit"], `EnvironmentFile="/etc/mongojson-steward/mongojson-steward.service.env"`) ||
		strings.Contains(linuxPlan.Artifacts["systemd_unit"], "plan-secret") ||
		!strings.Contains(linuxPlan.Artifacts["environment_file"], `STEWARD_SYNC_SECRET="<redacted>"`) ||
		linuxPlan.Artifacts["environment_file_mode"] != "0600" {
		t.Fatalf("linux system plan did not target systemd system unit: %#v", linuxPlan)
	}
}

func TestPlanInstallKeepsUserScopeDefaultsForDarwinAndLinux(t *testing.T) {
	base := InstallOptions{
		Name:         "mongojson-steward",
		BinaryPath:   os.Args[0],
		WorkDir:      ".",
		HTTPAddr:     "127.0.0.1:18080",
		PeerHTTPAddr: ":18081",
		DatabaseURL:  "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",
		StorageDir:   "./data",
	}

	darwinPlan, err := PlanInstall("darwin", base)
	if err != nil {
		t.Fatalf("darwin user plan: %v", err)
	}
	if darwinPlan.Scope != ScopeUser || darwinPlan.Files[0] != "~/Library/LaunchAgents/mongojson-steward.plist" {
		t.Fatalf("unexpected darwin user plan: %#v", darwinPlan)
	}

	linuxPlan, err := PlanInstall("linux", base)
	if err != nil {
		t.Fatalf("linux user plan: %v", err)
	}
	if linuxPlan.Scope != ScopeUser || len(linuxPlan.Files) != 2 || linuxPlan.Files[0] != "~/.config/systemd/user/mongojson-steward.service" ||
		linuxPlan.Files[1] != "~/.config/mongojson-steward/mongojson-steward.service.env" ||
		!strings.Contains(linuxPlan.Artifacts["systemd_unit"], "WantedBy=default.target") ||
		!strings.Contains(linuxPlan.Artifacts["systemd_unit"], `EnvironmentFile="~/.config/mongojson-steward/mongojson-steward.service.env"`) {
		t.Fatalf("unexpected linux user plan: %#v", linuxPlan)
	}
}

func TestPlanInstallRejectsWindowsUserScope(t *testing.T) {
	_, err := PlanInstall("windows", InstallOptions{
		Scope:        ScopeUser,
		Name:         "MongojsonSteward",
		BinaryPath:   os.Args[0],
		WorkDir:      ".",
		HTTPAddr:     "127.0.0.1:18080",
		PeerHTTPAddr: ":18081",
		DatabaseURL:  "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable",
		StorageDir:   "./data",
	})
	if err == nil || !strings.Contains(err.Error(), "windows service scope") {
		t.Fatalf("expected windows user scope to be rejected, got %v", err)
	}
}
