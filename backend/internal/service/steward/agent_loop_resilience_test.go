package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

type scriptedAgentTurnAdvisor struct {
	decisions []AgentTurnDecision
	errors    []error
	calls     int
}

type scriptedNativeResilienceAdvisor struct {
	turnDecisions []AgentTurnDecision
	turnErrors    []error
	turnCalls     int
}

func (a *scriptedNativeResilienceAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "scripted", Model: "scripted-model"}
}

func (a *scriptedNativeResilienceAdvisor) Suggest(context.Context, AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	return AutonomyAdvisorSuggestion{Title: "ok"}, nil
}

func (a *scriptedNativeResilienceAdvisor) NextTurn(context.Context, AgentTurnInput) (AgentTurnDecision, error) {
	index := a.turnCalls
	a.turnCalls++
	var decision AgentTurnDecision
	var err error
	if index < len(a.turnDecisions) {
		decision = a.turnDecisions[index]
	}
	if index < len(a.turnErrors) {
		err = a.turnErrors[index]
	}
	return decision, err
}

func (a *scriptedAgentTurnAdvisor) NextTurn(context.Context, AgentTurnInput) (AgentTurnDecision, error) {
	index := a.calls
	a.calls++
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

func TestNextValidAgentTurnPreservesFirstProviderError(t *testing.T) {
	want := errors.New("original provider failure")
	advisor := &scriptedAgentTurnAdvisor{
		decisions: []AgentTurnDecision{{}, {Content: "must not be reached"}},
		errors:    []error{want, nil},
	}
	_, err := nextValidAgentTurn(context.Background(), advisor, AgentTurnInput{})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want original provider failure", err)
	}
	if advisor.calls != 1 {
		t.Fatalf("calls = %d, want 1", advisor.calls)
	}
}

func TestNextValidAgentTurnRetriesOnlyEmptyResponse(t *testing.T) {
	advisor := &scriptedAgentTurnAdvisor{decisions: []AgentTurnDecision{{}, {Content: "recovered"}}}
	decision, err := nextValidAgentTurn(context.Background(), advisor, AgentTurnInput{})
	if err != nil || decision.Content != "recovered" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if advisor.calls != 2 {
		t.Fatalf("calls = %d, want 2", advisor.calls)
	}
}

func TestBuildAgentTranscriptBoundsLongHistoryAndKeepsPairs(t *testing.T) {
	turns := make([]domain.StewardAgentTurn, 0, 200)
	for round := 1; round <= 200; round++ {
		callID := fmt.Sprintf("call_%d", round)
		turns = append(turns, domain.StewardAgentTurn{
			RoundIndex: round,
			Status:     "tools_complete",
			ToolCalls: []domain.StewardAgentToolCall{{
				ID: callID, ToolName: "fs.list", Arguments: map[string]any{"path": "C:/workspace", "large": strings.Repeat("a", 10000)},
			}},
			ToolResults: []domain.StewardAgentToolResult{{
				ToolCallID: callID, ToolName: "fs.list", Output: map[string]any{"entries": strings.Repeat("b", 20000), "path": fmt.Sprintf("C:/workspace/round-%d", round)},
				Evidence: map[string]any{"evidence_id": fmt.Sprintf("evidence_%d", round)},
			}},
		})
	}
	transcript := buildAgentTranscript(turns)
	if len(transcript) != agentTranscriptRecentTurnLimit+1 {
		t.Fatalf("transcript length = %d, want %d", len(transcript), agentTranscriptRecentTurnLimit+1)
	}
	if len(transcript[0].ToolCalls) != 0 || len(transcript[0].ToolResults) != 0 || !strings.Contains(transcript[0].AssistantContent, "已压缩") {
		t.Fatalf("invalid old-turn summary: %+v", transcript[0])
	}
	if !strings.Contains(transcript[0].AssistantContent, "C:/workspace/round-1") {
		t.Fatalf("old-turn summary lost the first round's working fact: %s", transcript[0].AssistantContent)
	}
	for index, turn := range transcript[1:] {
		if len(turn.ToolCalls) != len(turn.ToolResults) {
			t.Fatalf("recent turn %d has unpaired calls/results", index)
		}
		for callIndex := range turn.ToolCalls {
			if turn.ToolCalls[callIndex].ID != turn.ToolResults[callIndex].ToolCallID {
				t.Fatalf("recent turn %d call %d id mismatch", index, callIndex)
			}
		}
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 150000 {
		t.Fatalf("compacted transcript is too large: %d bytes", len(encoded))
	}
}

func TestBuildAgentTranscriptSynthesizesMissingToolResult(t *testing.T) {
	transcript := buildAgentTranscript([]domain.StewardAgentTurn{{
		RoundIndex: 1, Status: "tools_complete",
		ToolCalls: []domain.StewardAgentToolCall{{ID: "call_1", ToolName: "fs.list"}},
	}})
	if len(transcript) != 1 || len(transcript[0].ToolResults) != 1 || transcript[0].ToolResults[0].ToolCallID != "call_1" {
		t.Fatalf("missing result was not paired: %+v", transcript)
	}
}

func TestSummarizeAgentResultsHidesGovernanceAndFormatsWindowsTools(t *testing.T) {
	summary := summarizeAgentResults([]domain.StewardAgentToolResult{
		{ToolName: "screen.capture", Output: map[string]any{"path": `C:\Users\me\Pictures\shot.png`, "width": 2560, "height": 1440, "_governance": map[string]any{"secret": "raw"}}},
		{ToolName: "fs.get_known_folders", Output: map[string]any{"desktop": `C:\Users\me\Desktop`, "_governance": map[string]any{"evidence_id": "x"}}},
	})
	if strings.Contains(summary, "_governance") || strings.Contains(summary, "evidence_id") {
		t.Fatalf("summary leaked governance payload: %s", summary)
	}
	if !strings.Contains(summary, "shot.png 2560×1440") || !strings.Contains(summary, "已获取当前登录用户") {
		t.Fatalf("unexpected compact summary: %s", summary)
	}
}

func TestAdvisorCircuitRetryAtTyped(t *testing.T) {
	retryAt := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	advisor := &resilientAutonomyAdvisor{
		base: &scriptedAutonomyAdvisor{}, now: func() time.Time { return retryAt.Add(-time.Minute) },
		circuitOpenUntil: retryAt, lastError: "original provider failure",
	}
	_, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{})
	actual, ok := advisorCircuitRetryAt(err)
	if !ok || !actual.Equal(retryAt) {
		t.Fatalf("circuit error=%v retryAt=%v", err, actual)
	}
	var typed *AdvisorCircuitOpenError
	if !errors.As(err, &typed) || typed.LastError != "original provider failure" {
		t.Fatalf("typed circuit error lost original status: %#v", typed)
	}
}

func TestAdvisorFailureCircuitClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"timeout", context.DeadlineExceeded, true},
		{"rate-limit", &advisorHTTPError{StatusCode: 429, Status: "429", ProviderMsg: "rate limit"}, true},
		{"provider-5xx", &advisorHTTPError{StatusCode: 503, Status: "503", ProviderMsg: "unavailable"}, true},
		{"cancelled", context.Canceled, false},
		{"authentication", &advisorHTTPError{StatusCode: 401, Status: "401", ProviderMsg: "invalid api key"}, false},
		{"bad-request", &advisorHTTPError{StatusCode: 400, Status: "400", ProviderMsg: "invalid schema for function"}, false},
		{"unknown-tool", errors.New("agent model requested unknown tool bad.name"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := advisorFailureAffectsCircuit(test.err); got != test.want {
				t.Fatalf("advisorFailureAffectsCircuit(%v)=%v want %v", test.err, got, test.want)
			}
		})
	}
}

func TestResilientAdvisorRetriesTransientProviderFailure(t *testing.T) {
	providerErr := &advisorHTTPError{StatusCode: 503, Status: "503", ProviderMsg: "temporarily unavailable"}
	base := &scriptedNativeResilienceAdvisor{
		turnDecisions: []AgentTurnDecision{{}, {Content: "recovered"}},
		turnErrors:    []error{providerErr, nil},
	}
	advisor := &resilientAutonomyAdvisor{base: base, failureThreshold: 2, cooldown: time.Minute, now: time.Now}
	decision, err := advisor.NextTurn(context.Background(), AgentTurnInput{})
	if err != nil || decision.Content != "recovered" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if base.turnCalls != 2 {
		t.Fatalf("provider calls=%d want 2", base.turnCalls)
	}
	if status := advisor.Status(); status.CircuitOpen || status.ConsecutiveFailures != 0 {
		t.Fatalf("successful retry left breaker dirty: %#v", status)
	}
}

func TestResilientAdvisorHalfOpenAllowsOneProbeAndIgnoresStaleSuccess(t *testing.T) {
	now := time.Date(2026, 7, 19, 5, 0, 0, 0, time.UTC)
	advisor := &resilientAutonomyAdvisor{
		base: &scriptedNativeResilienceAdvisor{}, failureThreshold: 1, cooldown: time.Minute,
		now: func() time.Time { return now }, circuitOpenUntil: now.Add(-time.Second),
	}
	token, err := advisor.beginCall(false)
	if err != nil || !token.halfOpen {
		t.Fatalf("first half-open token=%+v err=%v", token, err)
	}
	if _, err := advisor.beginCall(false); err == nil {
		t.Fatal("second concurrent half-open call was allowed")
	}
	providerErr := &advisorHTTPError{StatusCode: 503, Status: "503", ProviderMsg: "still unavailable"}
	advisor.completeCall(token, providerErr)
	if status := advisor.Status(); !status.CircuitOpen {
		t.Fatalf("failed half-open probe did not reopen circuit: %#v", status)
	}

	// A request that started in the previous generation cannot clear the newly
	// opened circuit when it completes late.
	stale := advisorCallToken{generation: token.generation}
	advisor.completeCall(stale, nil)
	if status := advisor.Status(); !status.CircuitOpen {
		t.Fatalf("stale success cleared new circuit: %#v", status)
	}
}

func TestExplicitProbeBypassesOpenCircuitAndClosesItOnSuccess(t *testing.T) {
	now := time.Date(2026, 7, 19, 6, 0, 0, 0, time.UTC)
	base := &scriptedNativeResilienceAdvisor{turnDecisions: []AgentTurnDecision{{Content: "protocol ok"}}}
	advisor := &resilientAutonomyAdvisor{
		base: base, failureThreshold: 1, cooldown: time.Minute, now: func() time.Time { return now },
		circuitOpenUntil: now.Add(time.Minute), consecutiveFailures: 1, lastError: "old outage",
	}
	decision, err := advisor.ProbeNextTurn(context.Background(), AgentTurnInput{})
	if err != nil || decision.Content != "protocol ok" {
		t.Fatalf("probe decision=%+v err=%v", decision, err)
	}
	if status := advisor.Status(); status.CircuitOpen || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("successful explicit probe did not recover circuit: %#v", status)
	}
}

func TestExplicitNonHealthProbeDoesNotOverwriteOpenCircuit(t *testing.T) {
	now := time.Date(2026, 7, 19, 6, 30, 0, 0, time.UTC)
	authErr := &advisorHTTPError{StatusCode: 401, Status: "401", ProviderMsg: "invalid api key"}
	base := &scriptedNativeResilienceAdvisor{turnErrors: []error{authErr}}
	advisor := &resilientAutonomyAdvisor{
		base: base, failureThreshold: 1, cooldown: time.Minute, now: func() time.Time { return now },
		circuitOpenUntil: now.Add(time.Minute), consecutiveFailures: 2, lastError: "original outage",
	}
	if _, err := advisor.ProbeNextTurn(context.Background(), AgentTurnInput{}); !errors.Is(err, authErr) {
		t.Fatalf("probe error=%v want auth error", err)
	}
	status := advisor.Status()
	if !status.CircuitOpen || status.ConsecutiveFailures != 2 || status.LastError != "original outage" {
		t.Fatalf("non-health probe overwrote breaker state: %#v", status)
	}
}
