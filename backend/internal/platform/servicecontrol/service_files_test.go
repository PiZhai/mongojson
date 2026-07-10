package servicecontrol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceFileWritesRejectOverwriteAndReplaceAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.env")
	if err := writeNewServiceFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("write new service file: %v", err)
	}
	if err := writeNewServiceFile(path, []byte("overwrite"), 0o600); err == nil {
		t.Fatalf("expected exclusive service file write to reject overwrite")
	}
	if err := writeServiceFileAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("replace service file atomically: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("service file content = %q, err=%v", data, err)
	}
	if err := ensureServicePathAbsent(path, "test service file"); err == nil {
		t.Fatalf("expected existing service path to be rejected")
	}
}
