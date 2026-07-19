package steward

import (
	"context"
	"fmt"
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

func TestWindowsScreenCaptureContract(t *testing.T) {
	if windowsFoundationToolVersion != "1.0.7" {
		t.Fatalf("windows foundation tool version = %q, want 1.0.7", windowsFoundationToolVersion)
	}

	var capture *windowsFoundationToolDefinition
	definitions := windowsFoundationToolDefinitions()
	for i := range definitions {
		if definitions[i].name == "screen.capture" {
			capture = &definitions[i]
			break
		}
	}
	if capture == nil {
		t.Fatal("screen.capture is missing from the Windows foundation catalog")
	}
	if capture.target != toolTargetSession {
		t.Fatalf("screen.capture target = %q, want %q", capture.target, toolTargetSession)
	}
	if len(capture.required) != 0 {
		t.Fatalf("screen.capture required fields = %v, want optional path", capture.required)
	}
	pathSchema, ok := capture.properties["path"].(map[string]any)
	if !ok || pathSchema["type"] != "string" {
		t.Fatalf("screen.capture path schema = %#v, want optional string", capture.properties["path"])
	}
	if description, _ := pathSchema["description"].(string); !strings.Contains(description, "Relative paths use Pictures") || !strings.Contains(description, "folder aliases") {
		t.Fatalf("screen.capture path description does not explain path resolution: %q", description)
	}

	for _, contract := range []string{
		"function Get-StewardKnownFolder",
		"@('downloads','download')",
		"function Resolve-StewardCapturePath",
		"@('pictures','desktop','home')",
		"[IO.Directory]::CreateDirectory($parent)",
		"[IO.FileMode]::CreateNew",
		"path=$p;width=$w;height=$h",
	} {
		if !strings.Contains(windowsFoundationPowerShell, contract) {
			t.Errorf("screen.capture PowerShell is missing contract %q", contract)
		}
	}
}

func TestWindowsUserFileToolsReplaceBuiltinsAndRouteToSession(t *testing.T) {
	definitions := map[string]windowsFoundationToolDefinition{}
	for _, definition := range windowsFoundationToolDefinitions() {
		definitions[definition.name] = definition
	}
	for _, name := range []string{"fs.list", "fs.read_text", "fs.create_directory", "fs.create_text"} {
		definition, ok := definitions[name]
		if !ok {
			t.Fatalf("%s is missing from the Windows foundation catalog", name)
		}
		if !windowsFoundationReplacesBuiltin(name) {
			t.Fatalf("%s must replace its compiled LocalService implementation", name)
		}
		if definition.target != toolTargetSession {
			t.Fatalf("%s target = %q, want %q", name, definition.target, toolTargetSession)
		}
		if got, _ := windowsFoundationOutputSchema(definition)["type"].(string); got != "object" {
			t.Fatalf("%s output schema type = %q", name, got)
		}
		if required, _ := windowsFoundationOutputSchema(definition)["required"].([]string); len(required) == 0 {
			t.Fatalf("%s must declare its output contract", name)
		}
		if windowsFoundationIdempotency(definition) == RuntimeIdempotencyNonIdempotent {
			t.Fatalf("%s must preserve its retry-safe compiled idempotency contract", name)
		}
	}
	for _, name := range []string{"fs.exists", "fs.stat", "fs.search", "fs.read_bytes", "fs.write_text", "fs.append_text", "fs.patch_text", "fs.copy", "fs.move", "fs.delete", "fs.hash", "fs.create_temp", "archive.list", "archive.create", "archive.extract", "archive.test"} {
		if definitions[name].target != toolTargetSession {
			t.Fatalf("user file tool %s target = %q, want session; auto does not dynamically route user paths", name, definitions[name].target)
		}
	}
	if windowsFoundationReplacesBuiltin("fs.exists") {
		t.Fatal("platform tools without compiled name conflicts do not need the replacement exception")
	}
}

func TestWindowsUserFilePackageOverridesCompiledRegistryEntry(t *testing.T) {
	registry := newRuntimeToolRegistry(newRuntimeListDirectoryTool(NewService(nil)))
	before, ok := registry.get("fs.list")
	if !ok || before.Spec().Version != "2.0.0" {
		t.Fatalf("expected compiled fs.list 2.0.0, got %#v", before)
	}
	var definition windowsFoundationToolDefinition
	for _, candidate := range windowsFoundationToolDefinitions() {
		if candidate.name == "fs.list" {
			definition = candidate
			break
		}
	}
	manifest := windowsFoundationTestManifest(definition)
	registry.register(newPackageRuntimeTool(NewService(nil), manifest))
	after, ok := registry.get("fs.list")
	if !ok {
		t.Fatal("fs.list disappeared after platform registration")
	}
	if _, packageTool := after.(*packageRuntimeTool); !packageTool {
		t.Fatalf("fs.list runtime type = %T, want platform package", after)
	}
	if after.Spec().Version != windowsFoundationToolVersion {
		t.Fatalf("fs.list version = %q, want %q", after.Spec().Version, windowsFoundationToolVersion)
	}
	if manifest.ExecutionTarget != toolTargetSession {
		t.Fatalf("fs.list manifest target = %q, want session", manifest.ExecutionTarget)
	}
	// Preflight runs in the restricted LocalService process. It must validate
	// only the declared schema and leave filesystem existence/access checks to
	// the signed-in user's Companion process.
	if err := after.(RuntimeToolValidator).Validate(map[string]any{"path": `C:\Users\interactive-user\Desktop`}); err != nil {
		t.Fatalf("session fs.list performed a LocalService filesystem preflight: %v", err)
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

func TestWindowsScreenCaptureResolvesAbsolutePathWithoutDefaultFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell adapter is Windows-specific")
	}
	marker := "function Resolve-WinGet"
	preludeEnd := strings.Index(windowsFoundationPowerShell, marker)
	if preludeEnd < 0 {
		t.Fatalf("PowerShell adapter is missing %q", marker)
	}
	script := windowsFoundationPowerShell[:preludeEnd] + `
function Get-StewardCaptureFolder { throw 'default screenshot folder must not be resolved' }
[Console]::Out.Write((Resolve-StewardCapturePath $env:STEWARD_TEST_PATH))
`
	path := filepath.Join(t.TempDir(), "resolve-capture-path.ps1")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "nested", "capture.png")
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	command.Env = append(os.Environ(), "STEWARD_TEST_PATH="+target)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("absolute screenshot path resolution failed: %v\n%s", err, output)
	}
	if got := filepath.Clean(strings.TrimSpace(string(output))); got != filepath.Clean(target) {
		t.Fatalf("resolved screenshot path = %q, want %q", got, target)
	}
	if info, statErr := os.Stat(filepath.Dir(target)); statErr != nil || !info.IsDir() {
		t.Fatalf("absolute screenshot parent was not created: info=%v err=%v", info, statErr)
	}
}

func TestWindowsFoundationRunsOnWindowsPowerShell51(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell adapter is Windows-specific")
	}
	path := filepath.Join(t.TempDir(), "tool.ps1")
	if err := os.WriteFile(path, []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	command.Env = append(os.Environ(), "STEWARD_TOOL_NAME=fs.exists")
	command.Stdin = strings.NewReader(`{"protocol":"steward-tool/1","invocation_id":"test","arguments":{"path":` + fmt.Sprintf("%q", target) + `},"context":{}}` + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Windows PowerShell 5.1 execution failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"exists":true`) {
		t.Fatalf("unexpected Windows PowerShell 5.1 output: %s", output)
	}
}

func TestWindowsFoundationGetsKnownFoldersWithoutOverwritingPowerShellHome(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell adapter is Windows-specific")
	}
	path := filepath.Join(t.TempDir(), "tool.ps1")
	if err := os.WriteFile(path, []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.Command(powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path)
	command.Env = append(os.Environ(), "STEWARD_TOOL_NAME=fs.get_known_folders")
	command.Stdin = strings.NewReader(`{"protocol":"steward-tool/1","invocation_id":"test","arguments":{},"context":{}}` + "\n")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("known folders execution failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `"ok":true`) || !strings.Contains(string(output), `"desktop"`) {
		t.Fatalf("unexpected known folders output: %s", output)
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

func TestWindowsFoundationUserFileLifecycleThroughCompanionProtocol(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell Session Companion adapter is Windows-specific")
	}
	definitions := map[string]windowsFoundationToolDefinition{}
	for _, definition := range windowsFoundationToolDefinitions() {
		definitions[definition.name] = definition
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "工作区")
	file := filepath.Join(workspace, "管家验收.txt")

	createDirectory := executeWindowsFoundationTestTool(t, definitions["fs.create_directory"], map[string]any{"path": workspace})
	if created, _ := createDirectory.Output["created"].(bool); !created {
		t.Fatalf("directory was not created: %#v", createDirectory.Output)
	}

	createText := executeWindowsFoundationTestTool(t, definitions["fs.create_text"], map[string]any{
		"path": file, "content": "截图保存位置验收", "create_parents": true,
	})
	if created, _ := createText.Output["created"].(bool); !created {
		t.Fatalf("text file was not created: %#v", createText.Output)
	}
	if reconciled := executeWindowsFoundationTestTool(t, definitions["fs.create_text"], map[string]any{
		"path": file, "content": "截图保存位置验收", "create_parents": true,
	}); reconciled.Output["reconciled"] != true || reconciled.Output["created"] != false {
		t.Fatalf("idempotent create did not reconcile: %#v", reconciled.Output)
	}

	listing := executeWindowsFoundationTestTool(t, definitions["fs.list"], map[string]any{"path": workspace})
	if count, _ := listing.Output["count"].(float64); count != 1 {
		t.Fatalf("directory listing count = %v, output=%#v", listing.Output["count"], listing.Output)
	}
	entries, _ := listing.Output["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["name"] != "管家验收.txt" {
		t.Fatalf("directory listing does not contain the created file: %#v", listing.Output)
	}

	read := executeWindowsFoundationTestTool(t, definitions["fs.read_text"], map[string]any{"path": file})
	if read.Output["content"] != "截图保存位置验收" {
		t.Fatalf("read content = %#v", read.Output["content"])
	}
	if hash, _ := read.Output["sha256"].(string); len(hash) != 64 {
		t.Fatalf("read hash = %q", hash)
	}

	// This is the exact cross-tool shape used by a screenshot workflow:
	// screen.capture returns a source path, then fs.move places it in the
	// user-selected desktop workspace through the same Session Companion.
	captureSource := filepath.Join(root, "capture.png")
	captureDestination := filepath.Join(workspace, "capture.png")
	if err := os.WriteFile(captureSource, []byte("png-proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := executeWindowsFoundationTestTool(t, definitions["fs.move"], map[string]any{
		"source": captureSource, "destination": captureDestination,
	})
	if moved.Output["destination"] != captureDestination {
		t.Fatalf("move destination = %#v, want %q", moved.Output["destination"], captureDestination)
	}
	if _, err := os.Stat(captureSource); !os.IsNotExist(err) {
		t.Fatalf("capture source still exists after move: %v", err)
	}
	if content, err := os.ReadFile(captureDestination); err != nil || string(content) != "png-proof" {
		t.Fatalf("moved capture mismatch: content=%q err=%v", content, err)
	}
}

func windowsFoundationTestManifest(definition windowsFoundationToolDefinition) ToolPackageManifest {
	return ToolPackageManifest{
		Name: definition.name, Version: windowsFoundationToolVersion, Title: definition.name, Description: definition.description,
		Origin: "platform", Runtime: toolRuntimePowerShell, ExecutionTarget: definition.target, Entrypoint: "tool.ps1",
		InputSchema:  map[string]any{"type": "object", "properties": definition.properties, "required": definition.required, "additionalProperties": false},
		OutputSchema: windowsFoundationOutputSchema(definition), Files: []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "test"},
		DefaultTimeoutSec:  30, OutputLimitBytes: 1 << 20, SupportsCancel: true,
		IdempotencyMode: windowsFoundationIdempotency(definition), SideEffect: definition.sideEffect,
	}
}

func executeWindowsFoundationTestTool(t *testing.T, definition windowsFoundationToolDefinition, input map[string]any) RuntimeToolResult {
	t.Helper()
	manifest := windowsFoundationTestManifest(definition)
	packageDir := filepath.Join(t.TempDir(), "tools", definition.name, windowsFoundationToolVersion)
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "tool.ps1"), []byte(windowsFoundationPowerShell), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteCompanionToolPackage(context.Background(), manifest, packageDir, input)
	if err != nil {
		t.Fatalf("%s companion execution failed: %v", definition.name, err)
	}
	return result
}

func escapePowerShellTestPath(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}
