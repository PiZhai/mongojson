package steward

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareCompanionToolPackageUsesPrivateManifestCopy(t *testing.T) {
	root := t.TempDir()
	manifest := ToolPackageManifest{
		Name: "session.probe", Version: "1.0.0", Title: "probe", Description: "probe",
		Runtime: toolRuntimePowerShell, ExecutionTarget: toolTargetSession, Entrypoint: "tool.ps1",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		Files:              []ToolPackageFile{{Path: "tool.ps1", Content: "Write-Output test"}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "test"},
		DefaultTimeoutSec:  1, OutputLimitBytes: 1024, SupportsCancel: true,
	}
	dir, err := PrepareCompanionToolPackage(manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if dir == root || !strings.HasPrefix(filepath.Clean(dir), filepath.Clean(root)+string(os.PathSeparator)) {
		t.Fatalf("unexpected package dir: %s", dir)
	}
	content, err := os.ReadFile(filepath.Join(dir, "tool.ps1"))
	if err != nil || string(content) != "Write-Output test" {
		t.Fatalf("materialized content = %q, err=%v", content, err)
	}
}
