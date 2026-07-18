//go:build windows

package steward

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePowerShellExecutableWithoutPath(t *testing.T) {
	t.Setenv("PATH", "")
	root := strings.TrimSpace(os.Getenv("SYSTEMROOT"))
	if root == "" {
		root = `C:\Windows`
		t.Setenv("SYSTEMROOT", root)
	}

	got, err := resolvePowerShellExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if !strings.EqualFold(got, want) {
		t.Fatalf("PowerShell executable = %q, want trusted OS path %q", got, want)
	}
}
