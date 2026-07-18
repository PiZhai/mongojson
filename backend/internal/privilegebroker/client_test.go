package privilegebroker

import (
	"strings"
	"testing"
)

func TestExecutionErrorIncludesStructuredToolHostFailure(t *testing.T) {
	err := (&ExecutionError{Response: ExecuteResponse{
		Stdout: `{"ok":false,"output":{},"error":"create isolated temp: access denied","evidence":[]}`,
		Receipt: SignedExecutionReceipt{Payload: ExecutionReceipt{
			ExecutionID: "test", ExitCode: 1, ErrorCode: "exit_failed", AuditPersisted: true,
		}},
	}}).Error()
	if !strings.Contains(err, "exit_failed") || !strings.Contains(err, "create isolated temp: access denied") {
		t.Fatalf("ExecutionError = %q", err)
	}
}
