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

func TestToolsmithCreateExposesCompleteHostProtocolAndManifestSchema(t *testing.T) {
	spec := newRuntimeToolsmithTool(NewService(nil), "tool.create").Spec()
	for _, expected := range []string{"steward-tool/1", `{"ok":true,"output":{...},"evidence":[]}`, `never nest test input under "arguments"`, "$toolArguments"} {
		if !strings.Contains(spec.Description, expected) {
			t.Fatalf("tool.create description is missing %q", expected)
		}
	}
	parameters, err := normalizeOpenAIToolParameters(spec.InputSchema)
	if err != nil {
		t.Fatalf("tool.create schema is not provider-compatible: %v", err)
	}
	properties := parameters["properties"].(map[string]any)
	manifest := properties["manifest"].(map[string]any)
	manifestProperties := manifest["properties"].(map[string]any)
	for _, name := range []string{"name", "version", "title", "description", "runtime", "execution_target", "input_schema", "output_schema", "files", "dependency_strategy", "tests", "transaction"} {
		if _, ok := manifestProperties[name]; !ok {
			t.Fatalf("tool.create manifest schema is missing %s", name)
		}
	}
	tests := manifestProperties["tests"].(map[string]any)
	if !strings.Contains(tests["description"].(string), "At least one executable test") {
		t.Fatalf("tests schema lacks executable-test guidance: %#v", tests)
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
