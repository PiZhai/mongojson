package steward

import (
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestAutonomyActionExecutorRegistryPublishesCapabilities(t *testing.T) {
	service := &Service{}
	registry := newAutonomyActionExecutorRegistry(
		newLocalTaskAutonomyExecutor(service, AutonomyActionCreateReviewChecklist, "review", "autonomous_review"),
		newLocalTaskAutonomyExecutor(service, AutonomyActionCreateLocalTask, "local", "autonomous"),
	)
	capabilities := registry.capabilities()
	if len(capabilities) != 2 || capabilities[0].Action != AutonomyActionCreateLocalTask || capabilities[1].Action != AutonomyActionCreateReviewChecklist {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}

func TestExecutorAllowsProposalEnforcesDeclaredPermission(t *testing.T) {
	executor := newLocalTaskAutonomyExecutor(&Service{}, AutonomyActionCreateLocalTask, "local", "autonomous")
	err := executorAllowsProposal(executor, domain.StewardAutonomyProposal{
		Action:          AutonomyActionCreateLocalTask,
		RiskLevel:       "low",
		PermissionLevel: PermissionA4,
	})
	if err == nil || !strings.Contains(err.Error(), "allows up to") {
		t.Fatalf("expected executor permission rejection, got %v", err)
	}
}

func TestValidateAutonomyExecutionResultRejectsInvalidTaskTarget(t *testing.T) {
	executor := newLocalTaskAutonomyExecutor(&Service{}, AutonomyActionCreateLocalTask, "local", "autonomous")
	err := validateAutonomyExecutionResult(executor, AutonomyExecutionResult{
		TargetType: "task",
		TargetID:   "not-a-uuid",
	}, true)
	if err == nil || !strings.Contains(err.Error(), "invalid task target id") {
		t.Fatalf("expected invalid task target rejection, got %v", err)
	}
}

func TestAutonomyTaskIDIsDeterministicPerProposalAction(t *testing.T) {
	proposal := domain.StewardAutonomyProposal{ID: "proposal-1", Action: AutonomyActionCreateLocalTask}
	first := autonomyTaskID(proposal)
	second := autonomyTaskID(proposal)
	if first != second {
		t.Fatalf("autonomy task id is not deterministic: %s != %s", first, second)
	}
	proposal.Action = AutonomyActionCreateReviewChecklist
	if autonomyTaskID(proposal) == first {
		t.Fatalf("different actions should use different deterministic task ids")
	}
}

func TestAutonomyKnowledgeItemIDIsDeterministicPerProposalAction(t *testing.T) {
	proposal := domain.StewardAutonomyProposal{ID: "proposal-1", Action: AutonomyActionCreateKnowledgeSummary}
	first := autonomyKnowledgeItemID(proposal)
	if first != autonomyKnowledgeItemID(proposal) {
		t.Fatalf("autonomy knowledge item id is not deterministic")
	}
	proposal.Action = AutonomyActionRunReadOnlyDiagnostics
	if autonomyKnowledgeItemID(proposal) == first {
		t.Fatalf("different knowledge actions should use different deterministic ids")
	}
}
