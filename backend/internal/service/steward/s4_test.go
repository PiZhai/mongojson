package steward

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestNormalizeBulkDismissProposalStatus(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default", input: "", want: ProposalCandidate},
		{name: "candidate", input: ProposalCandidate, want: ProposalCandidate},
		{name: "approved", input: ProposalApproved, want: ProposalApproved},
		{name: "blocked", input: ProposalBlocked, want: ProposalBlocked},
		{name: "dismissed rejected", input: ProposalDismissed, wantErr: true},
		{name: "executed rejected", input: ProposalExecuted, wantErr: true},
		{name: "unknown rejected", input: "needs_approval", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBulkDismissProposalStatus(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapRunStatusToAuditPreservesFailures(t *testing.T) {
	if got := mapRunStatusToAudit(RunSuccess); got != ResultOK {
		t.Fatalf("success audit status = %q", got)
	}
	if got := mapRunStatusToAudit(RunBlocked); got != ResultBlocked {
		t.Fatalf("blocked audit status = %q", got)
	}
	if got := mapRunStatusToAudit(RunFailed); got != ResultFailed {
		t.Fatalf("failed audit status = %q, want %q", got, ResultFailed)
	}
}

func TestNormalizeBulkDismissLimit(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{input: 0, want: 50},
		{input: -1, want: 50},
		{input: 1, want: 1},
		{input: 199, want: 199},
		{input: 201, want: 200},
	}

	for _, tt := range tests {
		if got := normalizeBulkDismissLimit(tt.input); got != tt.want {
			t.Fatalf("normalizeBulkDismissLimit(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestValidateProposalTransition(t *testing.T) {
	base := domain.StewardAutonomyProposal{
		Status:          ProposalCandidate,
		Policy:          AutonomyPolicyConfirm,
		RiskLevel:       "low",
		PermissionLevel: PermissionA3,
	}
	tests := []struct {
		name      string
		proposal  domain.StewardAutonomyProposal
		target    string
		wantError string
	}{
		{name: "candidate can be approved", proposal: base, target: ProposalApproved},
		{name: "candidate can be dismissed", proposal: base, target: ProposalDismissed},
		{name: "candidate can be blocked", proposal: base, target: ProposalBlocked},
		{
			name:      "dismissed is terminal",
			proposal:  proposalWithStatus(base, ProposalDismissed),
			target:    ProposalApproved,
			wantError: "closed autonomy proposal status",
		},
		{
			name:      "executed is terminal",
			proposal:  proposalWithStatus(base, ProposalExecuted),
			target:    ProposalDismissed,
			wantError: "closed autonomy proposal status",
		},
		{
			name:      "blocked cannot be approved",
			proposal:  proposalWithStatus(base, ProposalBlocked),
			target:    ProposalApproved,
			wantError: "only candidate",
		},
		{
			name:      "high risk cannot be approved",
			proposal:  proposalWithRisk(base, "high", PermissionA3),
			target:    ProposalApproved,
			wantError: "cannot be approved",
		},
		{
			name:      "permission A4 cannot be approved",
			proposal:  proposalWithRisk(base, "low", PermissionA4),
			target:    ProposalApproved,
			wantError: "cannot be approved",
		},
		{
			name:      "never policy cannot be approved",
			proposal:  proposalWithPolicy(base, AutonomyPolicyNever),
			target:    ProposalApproved,
			wantError: "cannot be approved",
		},
		{
			name:      "cannot reset to candidate",
			proposal:  proposalWithStatus(base, ProposalApproved),
			target:    ProposalCandidate,
			wantError: "cannot be reset",
		},
		{
			name:      "executed requires execute path",
			proposal:  proposalWithStatus(base, ProposalApproved),
			target:    ProposalExecuted,
			wantError: "ExecuteAutonomyProposal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProposalTransition(tt.proposal, tt.target)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestApprovalProposalTransition(t *testing.T) {
	proposal := domain.StewardAutonomyProposal{
		Status:          ProposalCandidate,
		Policy:          AutonomyPolicyConfirm,
		RiskLevel:       "low",
		PermissionLevel: PermissionA3,
	}
	approval := domain.StewardApprovalRequest{RequestedAction: "approve autonomous execution"}

	target, ok, err := approvalProposalTransition(approval, proposal, ApprovalApproved)
	if err != nil {
		t.Fatalf("approve transition returned error: %v", err)
	}
	if !ok || target != ProposalApproved {
		t.Fatalf("approve transition = (%q,%t), want (%q,true)", target, ok, ProposalApproved)
	}

	target, ok, err = approvalProposalTransition(approval, proposal, ApprovalRejected)
	if err != nil {
		t.Fatalf("reject transition returned error: %v", err)
	}
	if !ok || target != ProposalDismissed {
		t.Fatalf("reject transition = (%q,%t), want (%q,true)", target, ok, ProposalDismissed)
	}

	target, ok, err = approvalProposalTransition(
		domain.StewardApprovalRequest{RequestedAction: "manual high-risk review"},
		proposal,
		ApprovalApproved,
	)
	if err != nil {
		t.Fatalf("manual review transition returned error: %v", err)
	}
	if ok || target != "" {
		t.Fatalf("manual review transition = (%q,%t), want no proposal transition", target, ok)
	}

	_, _, err = approvalProposalTransition(approval, proposalWithStatus(proposal, ProposalDismissed), ApprovalApproved)
	if err == nil || !strings.Contains(err.Error(), "closed autonomy proposal status") {
		t.Fatalf("dismissed approval error = %v, want closed-status rejection", err)
	}
}

func TestParseAutonomyAdvisorSuggestionStripsFencedJSON(t *testing.T) {
	suggestion, err := parseAutonomyAdvisorSuggestion("```json\n{\"title\":\"跟进项目\",\"summary\":\"整理下一步\",\"trigger_reason\":\"事件需要处理\",\"suggested_action\":\"创建本地任务\",\"impact_summary\":\"只影响本地任务\"}\n```")
	if err != nil {
		t.Fatalf("parse suggestion failed: %v", err)
	}
	if suggestion.Title != "跟进项目" || suggestion.SuggestedAction != "创建本地任务" {
		t.Fatalf("unexpected suggestion: %#v", suggestion)
	}
}

func TestEnhanceAutonomyProposalDoesNotEscalateSafetyFields(t *testing.T) {
	ruleID := "rule-1"
	sourceID := "event-1"
	input := CreateAutonomyProposalInput{
		RuleID:           &ruleID,
		SourceEntityType: "event",
		SourceEntityID:   &sourceID,
		Title:            "fallback",
		RiskLevel:        "low",
		PermissionLevel:  PermissionA3,
		DataLevel:        DataD0,
		Policy:           AutonomyPolicyConfirm,
	}
	service := &Service{advisor: fakeAutonomyAdvisor{suggestion: AutonomyAdvisorSuggestion{
		Title:           "model title",
		Summary:         "model summary",
		TriggerReason:   "model reason",
		SuggestedAction: "model action",
		ImpactSummary:   "model impact",
	}}}
	enhanced := service.enhanceAutonomyProposal(context.Background(), input, AutonomyAdvisorInput{DataLevel: DataD0})

	if enhanced.Title != "model title" || enhanced.Summary != "model summary" {
		t.Fatalf("advisor text was not applied: %#v", enhanced)
	}
	if enhanced.RiskLevel != "low" || enhanced.PermissionLevel != PermissionA3 || enhanced.DataLevel != DataD0 || enhanced.Policy != AutonomyPolicyConfirm {
		t.Fatalf("advisor changed safety fields: %#v", enhanced)
	}
	if !strings.Contains(enhanced.ImpactSummary, "不会自动执行外部操作") {
		t.Fatalf("impact summary should preserve local-only boundary: %q", enhanced.ImpactSummary)
	}
}

func TestEnhanceAutonomyProposalRejectsUnsafeAdvisorText(t *testing.T) {
	input := CreateAutonomyProposalInput{
		Title:           "fallback title",
		Summary:         "fallback summary",
		TriggerReason:   "fallback reason",
		SuggestedAction: "create a local review task",
		ImpactSummary:   "local only",
		RiskLevel:       "low",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD0,
		Policy:          AutonomyPolicyConfirm,
	}
	service := &Service{advisor: fakeAutonomyAdvisor{suggestion: AutonomyAdvisorSuggestion{
		Title:           "send update",
		Summary:         "looks useful",
		SuggestedAction: "发送邮件给客户并推送代码",
		ImpactSummary:   "external change",
	}}}
	enhanced := service.enhanceAutonomyProposal(context.Background(), input, AutonomyAdvisorInput{
		DataLevel: DataD0,
		RuleName:  "unsafe-output-test",
	})

	if enhanced.Title != input.Title || enhanced.SuggestedAction != input.SuggestedAction {
		t.Fatalf("unsafe advisor text should be rejected and fallback preserved: %#v", enhanced)
	}
}

func TestAdvisorSuggestionSafetyViolationDetectsHighRiskText(t *testing.T) {
	violation := advisorSuggestionSafetyViolation(AutonomyAdvisorSuggestion{
		Title:           "整理下一步",
		SuggestedAction: "读取密码后自动付款",
	})
	if violation == "" {
		t.Fatalf("expected unsafe advisor suggestion to be detected")
	}
}

func TestOpenAICompatibleAdvisorCallsChatCompletions(t *testing.T) {
	var capturedModel string
	var capturedMessages []map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected advisor request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization header = %q, want bearer key", got)
		}
		var payload struct {
			Model    string              `json:"model"`
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode advisor request: %v", err)
		}
		capturedModel = payload.Model
		capturedMessages = payload.Messages
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]string{
						"content": `{"title":"probe title","summary":"probe summary","trigger_reason":"low-risk probe","suggested_action":"create local candidate","impact_summary":"local only"}`,
					},
				},
			},
		})
	}))
	defer server.Close()

	advisor := openAICompatibleAutonomyAdvisor{
		client:       server.Client(),
		baseURL:      server.URL,
		apiKey:       "test-key",
		model:        "test-model",
		maxDataLevel: DataD1,
	}
	suggestion, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{
		Kind:             "verification_probe",
		SourceEntityType: "verification",
		Title:            "Probe",
		Summary:          "D0 probe",
		DataLevel:        DataD0,
		RuleName:         "rule",
		RuleScope:        "scope",
	})
	if err != nil {
		t.Fatalf("advisor suggest failed: %v", err)
	}
	if capturedModel != "test-model" || len(capturedMessages) != 2 {
		t.Fatalf("unexpected advisor request model=%q messages=%#v", capturedModel, capturedMessages)
	}
	if suggestion.Title != "probe title" || suggestion.SuggestedAction != "create local candidate" {
		t.Fatalf("unexpected suggestion: %#v", suggestion)
	}
}

func TestOpenAICompatibleAdvisorBlocksDataAboveMaxWithoutRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	advisor := openAICompatibleAutonomyAdvisor{
		client:       server.Client(),
		baseURL:      server.URL,
		model:        "test-model",
		maxDataLevel: DataD1,
	}
	_, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD2})
	if err == nil || !strings.Contains(err.Error(), "exceeds advisor max") {
		t.Fatalf("expected max data level error, got %v", err)
	}
	if called {
		t.Fatalf("advisor should not issue HTTP request for data above max level")
	}
}

func TestProbeAutonomyAdvisorReturnsSuggestion(t *testing.T) {
	service := &Service{advisor: fakeAutonomyAdvisor{suggestion: AutonomyAdvisorSuggestion{
		Title:           "probe title",
		Summary:         "probe summary",
		SuggestedAction: "create local candidate",
	}}}
	result, err := service.ProbeAutonomyAdvisor(context.Background(), ProbeAutonomyAdvisorInput{})
	if err != nil {
		t.Fatalf("probe advisor failed: %v", err)
	}
	if !result.OK || result.Suggestion == nil || result.Suggestion.Title != "probe title" {
		t.Fatalf("unexpected probe result: %#v", result)
	}
	if result.DataLevel != DataD0 {
		t.Fatalf("probe data level = %q, want D0", result.DataLevel)
	}
}

func TestResilientAutonomyAdvisorOpensCircuitAfterConsecutiveFailures(t *testing.T) {
	now := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	base := &scriptedAutonomyAdvisor{err: errors.New("upstream unavailable")}
	advisor := &resilientAutonomyAdvisor{
		base:             base,
		failureThreshold: 2,
		cooldown:         time.Minute,
		now: func() time.Time {
			return now
		},
	}

	if _, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD0}); err == nil {
		t.Fatalf("first failure returned nil error")
	}
	if status := advisor.Status(); status.CircuitOpen || status.ConsecutiveFailures != 1 {
		t.Fatalf("unexpected status after first failure: %#v", status)
	}
	if _, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD0}); err == nil {
		t.Fatalf("second failure returned nil error")
	}
	status := advisor.Status()
	if !status.CircuitOpen || status.ConsecutiveFailures != 2 || status.RetryAt == nil {
		t.Fatalf("expected open circuit after threshold: %#v", status)
	}
	callsAfterOpen := base.calls
	if _, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD0}); err == nil || !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("expected circuit-open error, got %v", err)
	}
	if base.calls != callsAfterOpen {
		t.Fatalf("circuit-open call should not reach base advisor: before=%d after=%d", callsAfterOpen, base.calls)
	}

	now = now.Add(time.Minute + time.Second)
	base.err = nil
	base.suggestion = AutonomyAdvisorSuggestion{Title: "recovered"}
	suggestion, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD0})
	if err != nil {
		t.Fatalf("half-open recovery failed: %v", err)
	}
	if suggestion.Title != "recovered" {
		t.Fatalf("unexpected recovered suggestion: %#v", suggestion)
	}
	if status := advisor.Status(); status.CircuitOpen || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("expected reset status after success: %#v", status)
	}
}

func TestResilientAutonomyAdvisorDoesNotCountPrivacyDenialAsProviderFailure(t *testing.T) {
	base := &scriptedAutonomyAdvisor{err: ErrAdvisorDataLevelDenied}
	advisor := &resilientAutonomyAdvisor{
		base:             base,
		failureThreshold: 1,
		cooldown:         time.Minute,
		now:              time.Now,
	}

	_, err := advisor.Suggest(context.Background(), AutonomyAdvisorInput{DataLevel: DataD2})
	if !errors.Is(err, ErrAdvisorDataLevelDenied) {
		t.Fatalf("expected data-level denial, got %v", err)
	}
	status := advisor.Status()
	if status.CircuitOpen || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("privacy denial should not affect provider circuit status: %#v", status)
	}
	if base.calls != 1 {
		t.Fatalf("base advisor calls = %d, want 1", base.calls)
	}
}

type fakeAutonomyAdvisor struct {
	suggestion AutonomyAdvisorSuggestion
}

func (f fakeAutonomyAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "fake", MaxDataLevel: DataD1}
}

func (f fakeAutonomyAdvisor) Suggest(context.Context, AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	return f.suggestion, nil
}

type scriptedAutonomyAdvisor struct {
	calls      int
	err        error
	suggestion AutonomyAdvisorSuggestion
}

func (f *scriptedAutonomyAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "scripted", MaxDataLevel: DataD1}
}

func (f *scriptedAutonomyAdvisor) Suggest(context.Context, AutonomyAdvisorInput) (AutonomyAdvisorSuggestion, error) {
	f.calls++
	if f.err != nil {
		return AutonomyAdvisorSuggestion{}, f.err
	}
	return f.suggestion, nil
}

func proposalWithStatus(proposal domain.StewardAutonomyProposal, status string) domain.StewardAutonomyProposal {
	proposal.Status = status
	return proposal
}

func proposalWithRisk(proposal domain.StewardAutonomyProposal, risk string, permission string) domain.StewardAutonomyProposal {
	proposal.RiskLevel = risk
	proposal.PermissionLevel = permission
	return proposal
}

func proposalWithPolicy(proposal domain.StewardAutonomyProposal, policy string) domain.StewardAutonomyProposal {
	proposal.Policy = policy
	return proposal
}
