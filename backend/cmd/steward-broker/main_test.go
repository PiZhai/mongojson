package main

import (
	"os"
	"path/filepath"
	"testing"

	"mongojson/backend/internal/platform/servicecontrol"
)

func TestLoadPrivateEnvironmentAllowsOnlyBrokerSecrets(t *testing.T) {
	t.Setenv("STEWARD_BROKER_CLIENT_KEY", "old")
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"STEWARD_BROKER_CLIENT_KEY":"client","STEWARD_BROKER_CONTROL_KEY":"control","STEWARD_BROKER_SIGNING_PRIVATE_KEY":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := servicecontrol.LoadPrivateEnvironmentFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("STEWARD_BROKER_CLIENT_KEY"); got != "client" {
		t.Fatalf("client key = %q, want client", got)
	}
}

func TestLoadPrivateEnvironmentRejectsUnexpectedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"PATH":"malicious"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := servicecontrol.LoadPrivateEnvironmentFile(path); err == nil {
		t.Fatal("expected unsupported key to be rejected")
	}
}
