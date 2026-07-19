package steward

import (
	"context"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestConversationOrchestrationToolResultsPreserveStepOrderAndMissingResults(t *testing.T) {
	service := &Service{}
	orchestration := domain.StewardOrchestration{Nodes: []domain.StewardOrchestrationNode{
		{
			ID: "second", Position: 2, Status: OrchestrationNodeSucceeded,
			Steps: []domain.StewardOrchestrationStep{{Key: "second", ToolName: "tool.second", Arguments: map[string]any{"position": 2}}},
		},
		{
			ID: "first", Position: 1, Status: OrchestrationNodeFailed, FailureSummary: "child process did not start",
			Steps: []domain.StewardOrchestrationStep{
				{Key: "first-a", ToolName: "tool.first_a", Arguments: map[string]any{"position": 1}},
				{Key: "first-b", ToolName: "tool.first_b", Arguments: map[string]any{"position": 1}},
			},
		},
		{
			ID: "third", Position: 3, Status: OrchestrationNodeSucceeded,
			RemoteDispatch: &domain.StewardRemoteDispatch{ResultPayload: map[string]any{"ok": true}},
			Steps:          []domain.StewardOrchestrationStep{{Key: "third", ToolName: "tool.third", Arguments: map[string]any{"position": 3}}},
		},
	}}

	results := service.conversationOrchestrationToolResults(context.Background(), orchestration)
	if len(results) != 4 {
		t.Fatalf("result count=%d, want one result for every orchestration step", len(results))
	}
	wantTools := []string{"tool.first_a", "tool.first_b", "tool.second", "tool.third"}
	for index, want := range wantTools {
		if results[index].ToolName != want {
			t.Fatalf("result %d tool=%q, want %q; results=%+v", index, results[index].ToolName, want, results)
		}
	}
	for index := 0; index < 3; index++ {
		if !strings.Contains(results[index].Error, "no result") {
			t.Fatalf("result %d did not contain an explicit placeholder error: %+v", index, results[index])
		}
	}
	if results[3].Error != "" || results[3].Output["ok"] != true {
		t.Fatalf("remote result was not preserved: %+v", results[3])
	}
}
