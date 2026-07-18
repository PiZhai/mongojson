//go:build windows

package steward

import (
	"context"
	"os"
	"testing"
)

func TestWindowsPathEntryMatchingAndReconciliation(t *testing.T) {
	original := `C:\Windows;C:\Program Files\Tool\;C:\Other`
	if !windowsPathContains(original, `c:\program files\tool`) {
		t.Fatal("case-insensitive normalized PATH lookup failed")
	}
	appended := appendWindowsPath(original, `C:\New Tool\bin`)
	if !windowsPathContains(appended, `C:\New Tool\bin`) {
		t.Fatalf("PATH append failed: %s", appended)
	}
	reconciled := removeWindowsPathEntry(appended, `c:\new tool\bin\`)
	if reconciled != original {
		t.Fatalf("PATH rollback changed unrelated entries: got %q want %q", reconciled, original)
	}
}

func TestRequestedWindowsPathScopesPreferMachineThenUser(t *testing.T) {
	defaults := requestedWindowsPathScopes(nil)
	if len(defaults) != 2 || defaults[0] != "Machine" || defaults[1] != "User" {
		t.Fatalf("unexpected default scope order: %v", defaults)
	}
	deduplicated := requestedWindowsPathScopes([]any{"User", "user", "Machine"})
	if len(deduplicated) != 2 || deduplicated[0] != "User" || deduplicated[1] != "Machine" {
		t.Fatalf("scope preference was not preserved: %v", deduplicated)
	}
}

func TestWindowsPathEnsureToolIsRegisteredForAgentUse(t *testing.T) {
	service := NewService(nil, WithRuntimeR2Enabled(true))
	tool, ok := service.runtimeTools.get("windows.path.ensure")
	if !ok || tool.Spec().Version != "1.0.0" {
		t.Fatalf("windows.path.ensure is not registered: tool=%v ok=%v", tool, ok)
	}
	transactional, ok := tool.(RuntimeTransactionalTool)
	if !ok || !transactional.ChangeTransactionEnabled() {
		t.Fatal("windows.path.ensure is not covered by the transaction runtime")
	}
}

func TestWindowsPathEnsureRealUserScopeTransaction(t *testing.T) {
	if os.Getenv("STEWARD_RUN_WINDOWS_MUTATION_TEST") != "1" {
		t.Skip("set STEWARD_RUN_WINDOWS_MUTATION_TEST=1 to verify a real user PATH transaction")
	}
	tool := newRuntimeWindowsPathEnsureTool().(*runtimeWindowsPathEnsureTool)
	directory := t.TempDir()
	input := map[string]any{"directory": directory, "scope_preference": []any{"User"}}
	snapshot, err := tool.SnapshotChange(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = tool.RollbackChange(context.Background(), input, snapshot, RuntimeToolResult{}, nil)
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute real user PATH transaction: %v", err)
	}
	if err := tool.VerifyChange(context.Background(), input, snapshot, result); err != nil {
		t.Fatalf("verify real user PATH transaction: %v", err)
	}
	rollback, err := tool.RollbackChange(context.Background(), input, snapshot, result, nil)
	if err != nil {
		t.Fatalf("rollback real user PATH transaction: %v", err)
	}
	if rolledBack, _ := rollback.Output["rolled_back"].(bool); !rolledBack {
		t.Fatalf("rollback did not report success: %+v", rollback.Output)
	}
	userPath, _, _, err := readWindowsPath(windowsPathScopes["user"])
	if err != nil {
		t.Fatal(err)
	}
	if windowsPathContains(userPath, directory) {
		t.Fatalf("temporary PATH entry remains after rollback: %s", directory)
	}
	if windowsPathContains(os.Getenv("PATH"), directory) {
		t.Fatalf("temporary process PATH entry remains after rollback: %s", directory)
	}
}
