package steward

import (
	"testing"

	"mongojson/backend/internal/domain"
)

func TestAgentProgressFingerprintIgnoresProviderToolCallIDs(t *testing.T) {
	leftCalls := []domain.StewardAgentToolCall{{ID: "call-1", ToolName: "fs.list", Arguments: map[string]any{"path": "C:/Temp"}}}
	rightCalls := []domain.StewardAgentToolCall{{ID: "call-2", ToolName: "fs.list", Arguments: map[string]any{"path": "C:/Temp"}}}
	if agentToolCallProgressFingerprint(leftCalls, false) != agentToolCallProgressFingerprint(rightCalls, false) {
		t.Fatal("provider-generated tool call IDs must not hide repeated calls")
	}
	leftResults := []domain.StewardAgentToolResult{{ToolCallID: "call-1", ToolName: "fs.list", Output: map[string]any{"entries": []any{}}}}
	rightResults := []domain.StewardAgentToolResult{{ToolCallID: "call-2", ToolName: "fs.list", Output: map[string]any{"entries": []any{}}}}
	if agentToolResultProgressFingerprint(leftResults) != agentToolResultProgressFingerprint(rightResults) {
		t.Fatal("provider-generated tool result IDs must not hide repeated results")
	}
}

func TestAgentToolsmithFailureFingerprintGroupsSameProtocolDefect(t *testing.T) {
	firstCalls := []domain.StewardAgentToolCall{{ID: "call-1", ToolName: "tool.create", Arguments: map[string]any{"manifest": map[string]any{"name": "windows.startup_list", "version": "1.0.1"}}}}
	secondCalls := []domain.StewardAgentToolCall{{ID: "call-2", ToolName: "tool.create", Arguments: map[string]any{"manifest": map[string]any{"name": "windows.startup_entries", "version": "2.0.0"}}}}
	if agentToolCallProgressFingerprint(firstCalls, true) != agentToolCallProgressFingerprint(secondCalls, true) {
		t.Fatal("Toolsmith failure fingerprint must group the same operation despite cosmetic manifest changes")
	}
	firstResults := []domain.StewardAgentToolResult{{ToolCallID: "call-1", ToolName: "tool.create", Error: `tool test failed: decode tool windows.startup_list response: invalid steward-tool/1 response: missing required boolean field "ok"`}}
	secondResults := []domain.StewardAgentToolResult{{ToolCallID: "call-2", ToolName: "tool.create", Error: `tool test failed: decode tool windows.startup_entries response: invalid steward-tool/1 response: missing required boolean field "ok"`}}
	if agentToolResultProgressFingerprint(firstResults) != agentToolResultProgressFingerprint(secondResults) {
		t.Fatal("Toolsmith failure fingerprint must group the same protocol defect across generated tool names")
	}
}

func TestAgentProgressFingerprintStillDistinguishesRealArgumentChanges(t *testing.T) {
	left := []domain.StewardAgentToolCall{{ToolName: "fs.list", Arguments: map[string]any{"path": "C:/A"}}}
	right := []domain.StewardAgentToolCall{{ToolName: "fs.list", Arguments: map[string]any{"path": "C:/B"}}}
	if agentToolCallProgressFingerprint(left, false) == agentToolCallProgressFingerprint(right, false) {
		t.Fatal("normal Agent progress fingerprint must retain material argument changes")
	}
}
