package steward

import (
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestAgentControlRunObservationsPreserveExactlyOnceBoundary(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		run        domain.StewardAgentRun
		want       agentControlCallState
		idempotent string
	}{
		{
			name: "successful receipt is authoritative",
			run: domain.StewardAgentRun{Status: RuntimeRunRunning, Steps: []domain.StewardRunStep{{
				ToolName: "side.effect", Status: RuntimeStepSucceeded, ToolIdempotency: RuntimeIdempotencyNonIdempotent,
				Invocations: []domain.StewardToolInvocation{{Status: RuntimeStepSucceeded, FinishedAt: &now}},
			}}},
			want:       agentControlCallKnown,
			idempotent: RuntimeIdempotencyNonIdempotent,
		},
		{
			name: "cancel before execution is safe to replan",
			run: domain.StewardAgentRun{Status: RuntimeRunCancelled, Steps: []domain.StewardRunStep{{
				ToolName: "side.effect", Status: RuntimeStepCancelled, ToolIdempotency: RuntimeIdempotencyNonIdempotent,
				Invocations: []domain.StewardToolInvocation{{Status: RuntimeStepCancelled, ErrorSummary: "tool invocation cancelled before execution", FinishedAt: &now}},
			}}},
			want:       agentControlCallNotExecuted,
			idempotent: RuntimeIdempotencyNonIdempotent,
		},
		{
			name: "side effect followed by interrupted receipt is unknown",
			run: domain.StewardAgentRun{Status: RuntimeRunCancelled, Steps: []domain.StewardRunStep{{
				ToolName: "side.effect", Status: RuntimeStepCancelled, ToolIdempotency: RuntimeIdempotencyNonIdempotent,
				Invocations: []domain.StewardToolInvocation{{Status: RuntimeStepCancelled, ErrorSummary: "tool invocation cancelled before verification", FinishedAt: &now}},
			}}},
			want:       agentControlCallUnknown,
			idempotent: RuntimeIdempotencyNonIdempotent,
		},
		{
			name: "running receipt is unknown even for keyed tools",
			run: domain.StewardAgentRun{Status: RuntimeRunRunning, Steps: []domain.StewardRunStep{{
				ToolName: "keyed.effect", Status: RuntimeStepRunning, ToolIdempotency: RuntimeIdempotencyKeyed,
				Invocations: []domain.StewardToolInvocation{{Status: RuntimeStepRunning}},
			}}},
			want:       agentControlCallUnknown,
			idempotent: RuntimeIdempotencyKeyed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := agentControlRunObservations(test.run)
			if len(observations) != 1 {
				t.Fatalf("observation count=%d, want 1", len(observations))
			}
			if observations[0].State != test.want || observations[0].Idempotency != test.idempotent {
				t.Fatalf("observation=%+v, want state=%s idempotency=%s", observations[0], test.want, test.idempotent)
			}
		})
	}
}

func TestAgentControlToolResultsNeverDescribeUnknownCallAsCompleted(t *testing.T) {
	turn := domain.StewardAgentTurn{ToolCalls: []domain.StewardAgentToolCall{
		{ID: "call-ok", ToolName: "known.tool"},
		{ID: "call-unknown", ToolName: "side.effect"},
		{ID: "call-not-started", ToolName: "not.started"},
	}}
	raw := []ConversationToolResult{
		{ToolName: "known.tool", Output: map[string]any{"receipt": "persisted"}},
		{ToolName: "side.effect", Output: map[string]any{"misleading": "must-not-surface"}},
		{ToolName: "not.started"},
	}
	results := agentControlToolResults(turn, domain.StewardConversationExecution{}, raw, []agentControlCallObservation{
		{State: agentControlCallKnown, Idempotency: RuntimeIdempotencyInherent},
		{State: agentControlCallUnknown, Idempotency: RuntimeIdempotencyNonIdempotent},
		{State: agentControlCallNotExecuted, Idempotency: RuntimeIdempotencyNonIdempotent},
	}, "resume")
	if got := results[0].Output["receipt"]; got != "persisted" || results[0].Error != "" {
		t.Fatalf("known result was not preserved: %+v", results[0])
	}
	if results[1].Error == "" || len(results[1].Output) != 0 {
		t.Fatalf("unknown side effect was exposed as success: %+v", results[1])
	}
	if results[2].Error == "" || len(results[2].Output) != 0 {
		t.Fatalf("not-started call needs an explicit replanning result: %+v", results[2])
	}
}
