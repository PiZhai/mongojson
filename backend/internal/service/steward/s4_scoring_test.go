package steward

import (
	"strings"
	"testing"
)

func TestRuleBasedAutonomyProposalScorerExplainsEvidence(t *testing.T) {
	ruleID := "rule-1"
	sourceID := "event-1"
	score := NewRuleBasedAutonomyProposalScorer().Score(CreateAutonomyProposalInput{
		RuleID:           &ruleID,
		SourceEntityType: "event",
		SourceEntityID:   &sourceID,
		Summary:          "context",
		TriggerReason:    "follow-up is required",
		SuggestedAction:  "create a local task",
	})

	if score.Value != 1 {
		t.Fatalf("score = %.2f, want 1.00", score.Value)
	}
	for _, expected := range []string{"命中本地规则", "关联可追溯来源", "触发原因完整", "建议动作明确"} {
		if !strings.Contains(score.Reason, expected) {
			t.Fatalf("score reason %q does not contain %q", score.Reason, expected)
		}
	}
}

func TestRuleBasedAutonomyProposalScorerKeepsSparseCandidateLow(t *testing.T) {
	score := NewRuleBasedAutonomyProposalScorer().Score(CreateAutonomyProposalInput{Title: "manual note"})
	if score.Value != 0.25 {
		t.Fatalf("score = %.2f, want 0.25", score.Value)
	}
}
