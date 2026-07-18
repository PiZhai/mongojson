package steward

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsFoundationCatalogIsUniqueAndValid(t *testing.T) {
	definitions := windowsFoundationToolDefinitions()
	if len(definitions) < 95 {
		t.Fatalf("expected a broad Windows foundation catalog, got %d tools", len(definitions))
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if seen[definition.name] {
			t.Fatalf("duplicate foundation tool %q", definition.name)
		}
		seen[definition.name] = true
		manifest := ToolPackageManifest{
			Name: definition.name, Version: "1.0.0", Title: definition.name, Description: definition.description,
			Origin: "platform", Runtime: toolRuntimePowerShell, ExecutionTarget: definition.target, Entrypoint: "tool.ps1",
			InputSchema: map[string]any{"type": "object", "properties": definition.properties}, OutputSchema: map[string]any{"type": "object"},
			Files:              []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
			DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "built-in"},
		}
		if _, err := normalizeToolPackageManifest(manifest); err != nil {
			t.Fatalf("invalid manifest for %s: %v", definition.name, err)
		}
	}
}

func TestWindowsFoundationPowerShellParses(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell adapter is Windows-specific")
	}
	path := filepath.Join(t.TempDir(), "tool.ps1")
	if err := os.WriteFile(path, []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$errors=$null; [void][System.Management.Automation.Language.Parser]::ParseFile('"+escapePowerShellTestPath(path)+"',[ref]$null,[ref]$errors); if($errors.Count){$errors|ForEach-Object{$_.ToString()}; exit 1}")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell adapter does not parse: %v\n%s", err, output)
	}
}

func TestWindowsFoundationExecutesFileProbe(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell adapter is Windows-specific")
	}
	packageDir := filepath.Join(t.TempDir(), "tools", "fs.exists", "1.0.0")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join(packageDir, "tool.ps1")
	if err := os.WriteFile(entrypoint, []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := ToolPackageManifest{
		Name: "fs.exists", Version: "1.0.0", Title: "fs.exists", Description: "test file probe", Runtime: toolRuntimePowerShell,
		ExecutionTarget: toolTargetAuto, Entrypoint: "tool.ps1", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
		OutputSchema: map[string]any{"type": "object"}, Files: []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "test"}, DefaultTimeoutSec: 30, OutputLimitBytes: 1 << 20,
	}
	result, err := ExecuteCompanionToolPackage(context.Background(), manifest, packageDir, map[string]any{"path": entrypoint})
	if err != nil {
		t.Fatal(err)
	}
	if exists, _ := result.Output["exists"].(bool); !exists {
		t.Fatalf("expected file probe to report exists, got %#v", result.Output)
	}
}

func TestWindowsFoundationWritesUnicodeText(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell adapter is Windows-specific")
	}
	packageDir := filepath.Join(t.TempDir(), "tools", "fs.write_text", "1.0.0")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join(packageDir, "tool.ps1")
	if err := os.WriteFile(entrypoint, []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "管家", "proof.txt")
	manifest := ToolPackageManifest{
		Name: "fs.write_text", Version: "1.0.0", Title: "fs.write_text", Description: "test Unicode file write", Runtime: toolRuntimePowerShell,
		ExecutionTarget: toolTargetAuto, Entrypoint: "tool.ps1", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "create_parents": map[string]any{"type": "boolean"}}, "required": []string{"path", "content"}},
		OutputSchema: map[string]any{"type": "object"}, Files: []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "test"}, DefaultTimeoutSec: 30, OutputLimitBytes: 1 << 20,
	}
	result, err := ExecuteCompanionToolPackage(context.Background(), manifest, packageDir, map[string]any{"path": target, "content": "由普通对话真实执行", "create_parents": true})
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil || string(content) != "由普通对话真实执行" {
		t.Fatalf("Unicode write mismatch: output=%#v content=%q err=%v", result.Output, content, readErr)
	}
}

func escapePowerShellTestPath(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}
