package steward

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestRestrictedWindowsMainServiceFlag(t *testing.T) {
	t.Setenv("STEWARD_RESTRICTED_SERVICE", "true")
	if runtime.GOOS == "windows" && !restrictedWindowsMainService() {
		t.Fatal("restricted Windows service flag was not detected")
	}
	if runtime.GOOS != "windows" && restrictedWindowsMainService() {
		t.Fatal("non-Windows process must not activate Windows service isolation")
	}
	if message := restrictedSystemToolError("windows.service.create").Error(); !strings.Contains(message, "tool:windows.service.create") {
		t.Fatalf("recovery message does not name the required Broker capability: %s", message)
	}
}

func TestRestrictedMainRejectsDirectSystemPackageExecution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows service isolation")
	}
	t.Setenv("STEWARD_RESTRICTED_SERVICE", "true")
	tool := &packageRuntimeTool{manifest: ToolPackageManifest{
		Name: "windows.service.create", Runtime: toolRuntimePowerShell, ExecutionTarget: toolTargetSystem,
		InputSchema: map[string]any{"type": "object"},
	}}
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "fixed Broker capability") {
		t.Fatalf("direct system execution was not blocked: %v", err)
	}
}
