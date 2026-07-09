package steward

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

type knowledgeSummaryAutonomyExecutor struct {
	service *Service
}

func newKnowledgeSummaryAutonomyExecutor(service *Service) AutonomyActionExecutor {
	return knowledgeSummaryAutonomyExecutor{service: service}
}

func (e knowledgeSummaryAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action:             AutonomyActionCreateKnowledgeSummary,
		Description:        "把低风险事件整理为本地知识摘要",
		TargetType:         "knowledge_item",
		RiskLevel:          "low",
		MaxPermissionLevel: PermissionA3,
	}
}

func (e knowledgeSummaryAutonomyExecutor) Simulate(_ context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	return AutonomyExecutionResult{
		TargetType:    "knowledge_item",
		ImpactSummary: defaultString(proposal.ImpactSummary, "would create one local knowledge summary without changing the source"),
		RecoveryHint:  "dismiss the proposal to keep the source unchanged",
	}, nil
}

func (e knowledgeSummaryAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	if e.service == nil {
		return AutonomyExecutionResult{}, fmt.Errorf("knowledge summary autonomy executor is not initialized")
	}
	sourceURI := "steward://autonomy/" + proposal.ID
	if proposal.SourceEntityID != nil && strings.TrimSpace(*proposal.SourceEntityID) != "" {
		sourceURI = "steward://" + defaultString(proposal.SourceEntityType, "entity") + "/" + strings.TrimSpace(*proposal.SourceEntityID)
	}
	item, err := e.service.CreateKnowledgeItem(ctx, CreateKnowledgeInput{
		ID:              autonomyKnowledgeItemID(proposal),
		Type:            "autonomy_summary",
		Title:           proposal.Title,
		Summary:         defaultString(proposal.Summary, proposal.SuggestedAction),
		Source:          "autonomy",
		OriginalURI:     sourceURI,
		ImportMethod:    "autonomy_summary",
		DataLevel:       proposal.DataLevel,
		PermissionLevel: proposal.PermissionLevel,
		AllowIndex:      boolPtr(true),
		UserConfirmed:   boolPtr(proposal.Status == ProposalApproved),
	})
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	return AutonomyExecutionResult{
		TargetType:    "knowledge_item",
		TargetID:      item.ID,
		ImpactSummary: "ensured idempotent local knowledge summary " + item.ID,
		RecoveryHint:  "delete the generated knowledge item to undo this local write",
	}, nil
}

func autonomyKnowledgeItemID(proposal domain.StewardAutonomyProposal) string {
	key := strings.Join([]string{"steward-autonomy-knowledge", proposal.ID, proposal.Action}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}
