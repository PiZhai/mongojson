package steward

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestRuntimeV2IsDisabledByDefault(t *testing.T) {
	t.Setenv("STEWARD_RUNTIME_V2", "false")
	service := NewService(nil)
	if err := service.runtimeEnabled(); !errors.Is(err, ErrRuntimeV2Disabled) {
		t.Fatalf("runtime enabled error = %v, want ErrRuntimeV2Disabled", err)
	}
}

func TestRuntimeEvidenceGovernanceEncryptsSensitivePayloadAndRedactsSecrets(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", key)
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "runtime-test-v1")
	service := NewService(nil, WithRuntimeEvidenceMaxBytes(64<<10))
	governed, err := service.governRuntimePayload("evidence-1", "run-1", "step-1", DataD4, map[string]any{
		"content": "private result",
		"api_key": "should-never-be-stored",
	})
	if err != nil {
		t.Fatalf("govern sensitive evidence: %v", err)
	}
	if governed.State != runtimeEvidenceStateEncrypted || !governed.Available || !governed.Redacted {
		t.Fatalf("unexpected sensitive evidence governance: %+v", governed)
	}
	if encrypted, _ := governed.Stored["_encrypted"].(bool); !encrypted {
		t.Fatalf("sensitive evidence was not encrypted: %+v", governed.Stored)
	}
	if len(governed.Preview) != 0 {
		t.Fatalf("sensitive invocation preview must contain governance metadata only: %+v", governed.Preview)
	}
	keyring, err := localPayloadKeyringFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptPayloadEnvelope(keyring, runtimeEvidenceAAD("evidence-1", "run-1", "step-1"), governed.Stored, "test evidence")
	if err != nil {
		t.Fatalf("decrypt governed evidence: %v", err)
	}
	if plain["api_key"] != "[REDACTED]" || plain["content"] != "private result" {
		t.Fatalf("decrypted governed evidence mismatch: %+v", plain)
	}
}

func TestRuntimeEvidenceGovernanceDropsOversizedPayloadBody(t *testing.T) {
	service := NewService(nil, WithRuntimeEvidenceMaxBytes(64))
	governed, err := service.governRuntimePayload("evidence-2", "run-2", "step-2", DataD2, map[string]any{
		"content": strings.Repeat("x", 512),
	})
	if err != nil {
		t.Fatalf("govern oversized evidence: %v", err)
	}
	if governed.State != runtimeEvidenceStateSummaryOnly || governed.Available || governed.SizeBytes <= 64 || governed.SHA256 == "" {
		t.Fatalf("oversized evidence was not reduced to verifiable metadata: %+v", governed)
	}
}

func TestNormalizeAgentRunInputProducesStableImmutablePlanHash(t *testing.T) {
	service := NewService(nil, WithRuntimeV2Enabled(true))
	input := CreateAgentRunInput{
		Goal:           "verify the execution kernel",
		IdempotencyKey: "request-1",
		AutoStart:      true,
		Steps: []CreateAgentRunStepInput{
			{Key: "first", ToolName: "runtime.echo", Arguments: map[string]any{"value": "ok"}},
			{Key: "second", ToolName: "runtime.echo", Arguments: map[string]any{"value": 2}, DependsOn: []string{"first"}},
		},
	}
	first, firstHash, err := service.normalizeAgentRunInput(input)
	if err != nil {
		t.Fatalf("normalize first plan: %v", err)
	}
	input.IdempotencyKey = "request-2"
	input.RequestedBy = "another-local-user"
	input.AutoStart = false
	_, secondHash, err := service.normalizeAgentRunInput(input)
	if err != nil {
		t.Fatalf("normalize second plan: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("scheduling metadata changed plan hash: %s != %s", firstHash, secondHash)
	}
	if first.PermissionCeiling != PermissionA0 || first.Steps[0].ToolVersion != "1.0.0" || first.Steps[0].TimeoutSeconds != 10 || first.Steps[0].MaxAttempts != 1 {
		t.Fatalf("runtime defaults were not normalized: %+v", first)
	}
	input.Steps[1].Arguments["value"] = 3
	_, changedHash, err := service.normalizeAgentRunInput(input)
	if err != nil {
		t.Fatalf("normalize changed plan: %v", err)
	}
	if changedHash == firstHash {
		t.Fatal("material step change did not change immutable plan hash")
	}
}

func TestNormalizeAgentRunInputRejectsUnavailableToolVersion(t *testing.T) {
	service := NewService(nil, WithRuntimeV2Enabled(true))
	_, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "pin tool semantics",
		Steps: []CreateAgentRunStepInput{{
			Key: "echo", ToolName: "runtime.echo", ToolVersion: "2.0.0",
			Arguments: map[string]any{"value": "ok"},
		}},
	})
	if !errors.Is(err, ErrAgentRunInvalid) {
		t.Fatalf("unavailable tool version error = %v, want ErrAgentRunInvalid", err)
	}
}

func TestNormalizeAgentRunInputRejectsForwardDependency(t *testing.T) {
	service := NewService(nil, WithRuntimeV2Enabled(true))
	_, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "invalid graph",
		Steps: []CreateAgentRunStepInput{
			{Key: "first", ToolName: "runtime.echo", Arguments: map[string]any{"value": 1}, DependsOn: []string{"second"}},
			{Key: "second", ToolName: "runtime.echo", Arguments: map[string]any{"value": 2}},
		},
	})
	if !errors.Is(err, ErrAgentRunInvalid) {
		t.Fatalf("forward dependency error = %v, want ErrAgentRunInvalid", err)
	}
}

func TestRuntimeEchoToolRequiresVerifiedPostcondition(t *testing.T) {
	tool := newRuntimeEchoTool()
	result, err := tool.Execute(context.Background(), map[string]any{"value": "evidence"})
	if err != nil {
		t.Fatalf("execute echo: %v", err)
	}
	if err := tool.Verify(context.Background(), nil, result.Output, map[string]any{"value": "evidence"}); err != nil {
		t.Fatalf("verify matching output: %v", err)
	}
	if err := tool.Verify(context.Background(), nil, result.Output, map[string]any{"value": "different"}); err == nil {
		t.Fatal("mismatched postcondition was accepted")
	}
}

func TestRuntimeToolPanicIsConvertedToExecutionFailure(t *testing.T) {
	tool := runtimePanicTool{}
	if _, err := executeRuntimeTool(context.Background(), tool, nil); err == nil || err.Error() != "runtime tool panicked: execute panic" {
		t.Fatalf("execute panic error = %v", err)
	}
	if err := verifyRuntimeTool(context.Background(), tool, nil, nil, nil); err == nil || err.Error() != "runtime tool verifier panicked: verify panic" {
		t.Fatalf("verify panic error = %v", err)
	}
}

type runtimePanicTool struct{}

func (runtimePanicTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{Name: "runtime.test.panic", Version: "1.0.0"}
}

func (runtimePanicTool) Execute(context.Context, map[string]any) (RuntimeToolResult, error) {
	panic("execute panic")
}

func (runtimePanicTool) Verify(context.Context, map[string]any, map[string]any, map[string]any) error {
	panic("verify panic")
}
