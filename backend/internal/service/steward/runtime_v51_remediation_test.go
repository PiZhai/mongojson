package steward

import (
	"context"
	"errors"
	"os"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestDiagnoseRuntimeFailureProvidesActionableAlternatives(t *testing.T) {
	tests := []struct {
		cause    error
		code     string
		category string
	}{
		{os.ErrPermission, "access_denied", "permission"},
		{os.ErrNotExist, "not_found", "availability"},
		{context.DeadlineExceeded, "timeout", "transient"},
		{errors.New("postcondition failed: value unchanged"), "verification_failed", "integrity"},
	}
	for _, test := range tests {
		diagnosis := diagnoseRuntimeFailure(test.cause)
		if diagnosis.Code != test.code || diagnosis.Category != test.category {
			t.Fatalf("diagnose %v = %+v, want %s/%s", test.cause, diagnosis, test.code, test.category)
		}
		if test.code != "verification_failed" && len(diagnosis.AlternativeHints) == 0 {
			t.Fatalf("diagnosis %s omitted alternative hints", diagnosis.Code)
		}
	}
}

func TestFailureDiagnosticsAreReturnedToNextModelTurn(t *testing.T) {
	result := attachRuntimeFailureDiagnostics(RuntimeToolResult{}, diagnoseRuntimeFailure(os.ErrPermission), "transaction-1", runtimeChangeRolledBack)
	remediation, ok := result.Output["_remediation"].(map[string]any)
	if !ok || remediation["transaction_id"] != "transaction-1" || remediation["rollback_status"] != runtimeChangeRolledBack {
		t.Fatalf("unexpected remediation output: %+v", result.Output)
	}
	failure, ok := remediation["failure"].(runtimeFailureDiagnosis)
	if !ok || failure.Code != "access_denied" {
		t.Fatalf("unexpected failure diagnosis: %#v", remediation["failure"])
	}
}

func TestGeneratedMutationRequiresCompleteTransactionContract(t *testing.T) {
	manifest := ToolPackageManifest{
		Name: "test.transactional", Version: "1.0.0", Title: "transactional", Description: "transactional test tool",
		Runtime: toolRuntimePowerShell, ExecutionTarget: toolTargetSystem, Entrypoint: "tool.ps1",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		Files:              []ToolPackageFile{{Path: "tool.ps1"}, {Path: "snapshot.ps1"}, {Path: "verify.ps1"}, {Path: "rollback.ps1"}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none"},
		SideEffect:         RuntimeSideEffectWrite,
		Transaction:        ToolTransactionContract{Mode: "automatic", SnapshotEntrypoint: "snapshot.ps1", VerificationEntrypoint: "verify.ps1", RollbackEntrypoint: "rollback.ps1"},
	}
	normalized, err := normalizeToolPackageManifest(manifest)
	if err != nil || normalized.Transaction.Mode != "automatic" {
		t.Fatalf("valid transaction contract rejected: normalized=%+v err=%v", normalized.Transaction, err)
	}
	manifest.Transaction.RollbackEntrypoint = ""
	if _, err := normalizeToolPackageManifest(manifest); err == nil {
		t.Fatal("incomplete transaction contract was accepted")
	}
}

func TestToolsmithRejectsNewMutatingScriptWithoutTransactionContract(t *testing.T) {
	service := NewService(nil)
	manifest := ToolPackageManifest{
		Name: "test.unrecoverable", Version: "1.0.0", Title: "unrecoverable", Description: "must be rejected",
		Runtime: toolRuntimePowerShell, ExecutionTarget: toolTargetSystem, Entrypoint: "tool.ps1",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		Files: []ToolPackageFile{{Path: "tool.ps1"}}, Tests: []ToolPackageTest{{Name: "contract", Input: map[string]any{}}},
		DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none"}, SideEffect: RuntimeSideEffectWrite,
	}
	if _, err := service.CreateToolPackage(context.Background(), CreateToolPackageInput{Manifest: manifest}); err == nil {
		t.Fatal("Toolsmith accepted a mutating script without an automatic transaction contract")
	}
}

type runtimeTransactionalProbe struct{}

func (runtimeTransactionalProbe) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{Name: "runtime.transaction.probe", Version: "1.0.0"}
}
func (runtimeTransactionalProbe) Execute(context.Context, map[string]any) (RuntimeToolResult, error) {
	return RuntimeToolResult{}, nil
}
func (runtimeTransactionalProbe) Verify(context.Context, map[string]any, map[string]any, map[string]any) error {
	return nil
}
func (runtimeTransactionalProbe) ChangeTransactionEnabled() bool { return true }
func (runtimeTransactionalProbe) SnapshotChange(context.Context, map[string]any) (RuntimeChangeSnapshot, error) {
	return RuntimeChangeSnapshot{State: map[string]any{"before": "value"}}, nil
}
func (runtimeTransactionalProbe) VerifyChange(context.Context, map[string]any, RuntimeChangeSnapshot, RuntimeToolResult) error {
	return nil
}
func (runtimeTransactionalProbe) RollbackChange(context.Context, map[string]any, RuntimeChangeSnapshot, RuntimeToolResult, error) (RuntimeToolResult, error) {
	return RuntimeToolResult{Output: map[string]any{"rolled_back": true}}, nil
}
