//go:build !windows

package privilegebroker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixCapabilityPathRejectsWritableParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capability")
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateCapabilityPathSecurity(path, filepath.Dir(path)); err == nil {
		t.Fatal("capability below writable temporary root was accepted")
	}
}
