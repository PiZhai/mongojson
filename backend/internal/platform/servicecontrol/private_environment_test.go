package servicecontrol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrivateEnvironmentFileRejectsPublicConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-secrets.json")
	if err := os.WriteFile(path, []byte(`{"DATABASE_URL":"postgres://secret","HTTP_ADDR":"0.0.0.0:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadPrivateEnvironmentFile(path); err == nil {
		t.Fatal("expected public configuration in private environment to be rejected")
	}
}

func TestLoadPrivateEnvironmentFileLoadsSensitiveValues(t *testing.T) {
	t.Setenv("STEWARD_LLM_API_KEY", "old")
	path := filepath.Join(t.TempDir(), "service-secrets.json")
	if err := os.WriteFile(path, []byte(`{"STEWARD_LLM_API_KEY":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadPrivateEnvironmentFile(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("STEWARD_LLM_API_KEY"); got != "new" {
		t.Fatalf("API key = %q", got)
	}
}
