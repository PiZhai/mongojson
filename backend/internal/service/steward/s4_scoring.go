package steward

import (
	"fmt"
	"math"
	"strings"
)

type AutonomyProposalScore struct {
	Value  float64
	Reason string
}

type AutonomyProposalScorer interface {
	Score(CreateAutonomyProposalInput) AutonomyProposalScore
}

type ruleBasedAutonomyProposalScorer struct{}

func NewRuleBasedAutonomyProposalScorer() AutonomyProposalScorer {
	return ruleBasedAutonomyProposalScorer{}
}

func (ruleBasedAutonomyProposalScorer) Score(input CreateAutonomyProposalInput) AutonomyProposalScore {
	value := 0.25
	reasons := []string{"基础候选 25%"}
	add := func(weight float64, reason string) {
		value += weight
		reasons = append(reasons, fmt.Sprintf("%s +%d%%", reason, int(math.Round(weight*100))))
	}

	if input.RuleID != nil && strings.TrimSpace(*input.RuleID) != "" {
		add(0.20, "命中本地规则")
	}
	if input.SourceEntityID != nil && strings.TrimSpace(*input.SourceEntityID) != "" {
		add(0.15, "关联可追溯来源")
	}
	if strings.TrimSpace(input.TriggerReason) != "" {
		add(0.15, "触发原因完整")
	}
	if strings.TrimSpace(input.SuggestedAction) != "" {
		add(0.10, "建议动作明确")
	}
	if strings.TrimSpace(input.Summary) != "" {
		add(0.05, "上下文摘要完整")
	}
	switch strings.ToLower(strings.TrimSpace(input.SourceEntityType)) {
	case "event", "task", "intent":
		add(0.10, "来源类型可验证")
	}

	value = math.Round(math.Max(0, math.Min(1, value))*100) / 100
	return AutonomyProposalScore{Value: value, Reason: strings.Join(reasons, "；")}
}
