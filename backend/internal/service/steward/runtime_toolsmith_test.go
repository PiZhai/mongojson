package steward

import (
	"errors"
	"strings"
	"testing"
)

func TestToolsmithTimeoutsMatchOperationCost(t *testing.T) {
	service := NewService(nil)
	for _, action := range []string{"tool.search", "tool.describe"} {
		if got := newRuntimeToolsmithTool(service, action).Spec().DefaultTimeoutSec; got != 30 {
			t.Fatalf("%s timeout = %ds, want 30s", action, got)
		}
	}
	for _, action := range []string{"tool.enable", "tool.disable", "tool.rollback", "tool.delete"} {
		if got := newRuntimeToolsmithTool(service, action).Spec().DefaultTimeoutSec; got != 120 {
			t.Fatalf("%s timeout = %ds, want 120s", action, got)
		}
	}
	for _, action := range []string{"tool.create", "tool.update", "tool.test"} {
		if got := newRuntimeToolsmithTool(service, action).Spec().DefaultTimeoutSec; got != 1800 {
			t.Fatalf("%s timeout = %ds, want 1800s", action, got)
		}
	}
}

func TestOrchestrationQuotaErrorIdentifiesExceededResource(t *testing.T) {
	err := orchestrationNodeQuotaError("tool_1", 1800, 1, 900, 20)
	if !errors.Is(err, ErrOrchestrationInvalid) || !strings.Contains(err.Error(), "runtime budget 1800s") || strings.Contains(err.Error(), "exceeding the Agent quota of 20 attempts") {
		t.Fatalf("runtime quota error is not actionable: %v", err)
	}
	err = orchestrationNodeQuotaError("tool_2", 30, 21, 900, 20)
	if !strings.Contains(err.Error(), "requests 21 attempts") {
		t.Fatalf("attempt quota error is not actionable: %v", err)
	}
	if err := orchestrationNodeQuotaError("tool_3", 30, 1, 900, 20); err != nil {
		t.Fatalf("valid quota unexpectedly failed: %v", err)
	}
}
