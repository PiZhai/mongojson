package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

type recordingContextRecoveryAdvisor struct {
	inputs    []AgentTurnInput
	decisions []AgentTurnDecision
	errors    []error
}

func (a *recordingContextRecoveryAdvisor) NextTurn(_ context.Context, input AgentTurnInput) (AgentTurnDecision, error) {
	a.inputs = append(a.inputs, input)
	index := len(a.inputs) - 1
	var decision AgentTurnDecision
	var err error
	if index < len(a.decisions) {
		decision = a.decisions[index]
	}
	if index < len(a.errors) {
		err = a.errors[index]
	}
	return decision, err
}

func TestAdvisorContextSizeExceededOnlyMatchesExplicitSizeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"400 context length", &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "This model's maximum context length is 65536 tokens"}, true},
		{"400 provider code", &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "bad input; code=context_length_exceeded"}, true},
		{"400 request too large", &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "request too large"}, true},
		{"413 empty response", &advisorHTTPError{StatusCode: 413, Status: "413 Request Entity Too Large"}, true},
		{"400 invalid schema", &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "invalid schema for function parameters"}, false},
		{"401 auth", &advisorHTTPError{StatusCode: 401, Status: "401 Unauthorized", ProviderMsg: "invalid api key"}, false},
		{"500 mentioning token", &advisorHTTPError{StatusCode: 500, Status: "500 Internal Server Error", ProviderMsg: "token service unavailable"}, false},
		{"plain error", errors.New("request too large"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := advisorContextSizeExceeded(test.err); got != test.want {
				t.Fatalf("advisorContextSizeExceeded(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestCompactAgentTurnInputForContextRetryPreservesToolPairs(t *testing.T) {
	history := make([]ConversationAdvisorMessage, 0, 12)
	localContext := make([]domain.StewardSearchResult, 0, 10)
	transcript := make([]AgentTurnTranscript, 0, 14)
	for index := 0; index < 14; index++ {
		callID := fmt.Sprintf("call_%d", index)
		history = append(history, ConversationAdvisorMessage{Role: "user", Content: strings.Repeat("history", 500)})
		localContext = append(localContext, domain.StewardSearchResult{ID: fmt.Sprint(index), Title: strings.Repeat("title", 100), Summary: strings.Repeat("summary", 500)})
		transcript = append(transcript, AgentTurnTranscript{
			AssistantContent: strings.Repeat("assistant", 500),
			ReasoningContent: strings.Repeat("reasoning", 500),
			ToolCalls: []domain.StewardAgentToolCall{{
				ID: callID, ToolName: "fs.read_text", Arguments: map[string]any{"path": fmt.Sprintf("C:/work/%d", index), "large": strings.Repeat("a", 8000)},
			}},
			ToolResults: []domain.StewardAgentToolResult{
				{ToolCallID: "orphan", ToolName: "ignored", Output: map[string]any{"large": strings.Repeat("x", 8000)}},
				{ToolCallID: callID, ToolName: "fs.read_text", Output: map[string]any{"content": strings.Repeat("b", 12000)}, Evidence: map[string]any{"evidence_id": fmt.Sprintf("evidence-%d", index)}},
			},
		})
	}
	originalLastOutput := transcript[len(transcript)-1].ToolResults[1].Output["content"].(string)
	input := AgentTurnInput{History: history, Context: localContext, Transcript: transcript}

	compacted := compactAgentTurnInputForContextRetry(input)
	if len(compacted.History) != agentContextRetryHistoryLimit {
		t.Fatalf("history length = %d, want %d", len(compacted.History), agentContextRetryHistoryLimit)
	}
	if len(compacted.Context) != agentContextRetryContextLimit {
		t.Fatalf("context length = %d, want %d", len(compacted.Context), agentContextRetryContextLimit)
	}
	if len(compacted.Transcript) != agentContextRetryTranscriptLimit+1 {
		t.Fatalf("transcript length = %d, want summary + %d recent", len(compacted.Transcript), agentContextRetryTranscriptLimit)
	}
	if len(compacted.Transcript[0].ToolCalls) != 0 || len(compacted.Transcript[0].ToolResults) != 0 {
		t.Fatalf("old transcript summary must be assistant-only: %+v", compacted.Transcript[0])
	}
	for index, turn := range compacted.Transcript[1:] {
		if len(turn.ToolCalls) != 1 || len(turn.ToolResults) != 1 {
			t.Fatalf("recent turn %d pair counts = %d/%d", index, len(turn.ToolCalls), len(turn.ToolResults))
		}
		if turn.ToolCalls[0].ID != turn.ToolResults[0].ToolCallID {
			t.Fatalf("recent turn %d call/result mismatch: %s/%s", index, turn.ToolCalls[0].ID, turn.ToolResults[0].ToolCallID)
		}
		if turn.ToolResults[0].ToolCallID == "orphan" {
			t.Fatalf("recent turn %d retained an orphan tool result", index)
		}
	}
	// Request compaction must not mutate the durable in-memory representation
	// that is subsequently persisted/read from the database.
	if transcript[len(transcript)-1].ToolResults[1].Output["content"] != originalLastOutput {
		t.Fatal("context retry mutated original tool evidence")
	}
	if len(input.History) != 14 || len(input.Transcript) != 14 {
		t.Fatal("context retry mutated original input slices")
	}
}

func TestNextAgentTurnWithContextRecoveryRetriesOnceWithCompactedView(t *testing.T) {
	contextErr := &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "context_length_exceeded"}
	advisor := &recordingContextRecoveryAdvisor{
		decisions: []AgentTurnDecision{{}, {Content: "recovered"}},
		errors:    []error{contextErr, nil},
	}
	input := AgentTurnInput{
		History:    make([]ConversationAdvisorMessage, 12),
		Context:    make([]domain.StewardSearchResult, 10),
		Transcript: make([]AgentTurnTranscript, 12),
	}
	decision, err := nextAgentTurnWithContextRecovery(context.Background(), advisor, input)
	if err != nil || decision.Content != "recovered" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if len(advisor.inputs) != 2 {
		t.Fatalf("advisor calls = %d, want 2", len(advisor.inputs))
	}
	if len(advisor.inputs[1].History) >= len(advisor.inputs[0].History) || len(advisor.inputs[1].Transcript) >= len(advisor.inputs[0].Transcript) {
		t.Fatalf("retry was not compacted: first=%d/%d retry=%d/%d", len(advisor.inputs[0].History), len(advisor.inputs[0].Transcript), len(advisor.inputs[1].History), len(advisor.inputs[1].Transcript))
	}
	if !strings.Contains(advisor.inputs[1].NoProgressNotice, "上下文大小") {
		t.Fatalf("retry notice = %q", advisor.inputs[1].NoProgressNotice)
	}
}

func TestNextAgentTurnWithContextRecoveryDoesNotRetryOtherErrors(t *testing.T) {
	want := &advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "invalid schema for function parameters"}
	advisor := &recordingContextRecoveryAdvisor{errors: []error{want}}
	_, err := nextAgentTurnWithContextRecovery(context.Background(), advisor, AgentTurnInput{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want original schema error", err)
	}
	if len(advisor.inputs) != 1 {
		t.Fatalf("advisor calls = %d, want 1", len(advisor.inputs))
	}
}

func TestContextSizeErrorDoesNotAffectProviderCircuit(t *testing.T) {
	base := &scriptedNativeResilienceAdvisor{
		turnDecisions: []AgentTurnDecision{{}, {Content: "recovered"}},
		turnErrors: []error{
			&advisorHTTPError{StatusCode: 413, Status: "413 Request Entity Too Large", ProviderMsg: "payload too large"},
			nil,
		},
	}
	advisor := &resilientAutonomyAdvisor{base: base, failureThreshold: 1}
	decision, err := nextAgentTurnWithContextRecovery(context.Background(), advisor, AgentTurnInput{})
	if err != nil || decision.Content != "recovered" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	status := advisor.Status()
	if status.CircuitOpen || status.ConsecutiveFailures != 0 {
		t.Fatalf("context error poisoned circuit: %+v", status)
	}
}

func TestContextSizeFailureHasSpecificUserFacingDiagnosis(t *testing.T) {
	cause := fmt.Errorf("model context remained too large after automatic compaction: %w",
		&advisorHTTPError{StatusCode: 400, Status: "400 Bad Request", ProviderMsg: "maximum context length exceeded"})
	detail := describeAdvisorFailure(cause)
	if detail.Code != "MODEL_CONTEXT_TOO_LARGE" || !strings.Contains(detail.Message, "自动压缩") {
		t.Fatalf("unexpected context failure detail: %+v", detail)
	}
}
