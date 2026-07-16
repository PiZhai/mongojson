//go:build windows

package privilegebroker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCapabilityPathAcceptsProtectedSystemBinary(t *testing.T) {
	root := os.Getenv("WINDIR")
	executable := filepath.Join(root, "System32", "whoami.exe")
	if err := validateCapabilityPathSecurity(executable, filepath.Dir(executable)); err != nil {
		t.Fatalf("protected system capability was rejected: %v", err)
	}
}

func TestWindowsCapabilityPathRejectsUserWritableLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capability.exe")
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCapabilityPathSecurity(path, filepath.Dir(path)); err == nil {
		t.Fatal("user-writable capability path was accepted")
	}
}
