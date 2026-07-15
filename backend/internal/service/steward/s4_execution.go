package steward

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

const (
	AutonomyActionCreateLocalTask        = "create_local_task"
	AutonomyActionCreateFollowUpTask     = "create_follow_up_task"
	AutonomyActionCreateReviewChecklist  = "create_review_checklist"
	AutonomyActionCreateReminderTask     = "create_reminder_task"
	AutonomyActionCreateKnowledgeSummary = "create_knowledge_summary"
	AutonomyActionRunReadOnlyDiagnostics = "run_readonly_diagnostics"
)

type AutonomyExecutionResult struct {
	TargetType    string
	TargetID      string
	ImpactSummary string
	RecoveryHint  string
}

type AutonomyActionExecutor interface {
	Capability() domain.StewardAutonomyActionCapability
	Simulate(context.Context, domain.StewardAutonomyProposal) (AutonomyExecutionResult, error)
	Execute(context.Context, domain.StewardAutonomyProposal) (AutonomyExecutionResult, error)
}

type autonomyActionExecutorRegistry struct {
	executors map[string]AutonomyActionExecutor
}

func newAutonomyActionExecutorRegistry(executors ...AutonomyActionExecutor) *autonomyActionExecutorRegistry {
	registry := &autonomyActionExecutorRegistry{executors: map[string]AutonomyActionExecutor{}}
	for _, executor := range executors {
		registry.register(executor)
	}
	return registry
}

func (r *autonomyActionExecutorRegistry) register(executor AutonomyActionExecutor) {
	if r == nil || executor == nil {
		return
	}
	action := strings.TrimSpace(executor.Capability().Action)
	if action == "" {
		return
	}
	r.executors[action] = executor
}

func (r *autonomyActionExecutorRegistry) resolve(action string) (AutonomyActionExecutor, bool) {
	if r == nil {
		return nil, false
	}
	executor, ok := r.executors[strings.TrimSpace(action)]
	return executor, ok
}

func (r *autonomyActionExecutorRegistry) capabilities() []domain.StewardAutonomyActionCapability {
	if r == nil {
		return []domain.StewardAutonomyActionCapability{}
	}
	capabilities := make([]domain.StewardAutonomyActionCapability, 0, len(r.executors))
	for _, executor := range r.executors {
		capabilities = append(capabilities, executor.Capability())
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Action < capabilities[j].Action })
	return capabilities
}

func (s *Service) autonomyActionExecutor(action string) (AutonomyActionExecutor, bool) {
	if s == nil || s.actionExecutors == nil {
		return nil, false
	}
	executor, found := s.actionExecutors.resolve(action)
	if found {
		return executor, true
	}
	action = strings.ToLower(strings.TrimSpace(action))
	if configuredToolActionPattern.MatchString(action) {
		return configuredToolAutonomyExecutor{service: s, action: action}, true
	}
	return nil, false
}

func (s *Service) autonomyActionCapabilities() []domain.StewardAutonomyActionCapability {
	if s == nil || s.actionExecutors == nil {
		return []domain.StewardAutonomyActionCapability{}
	}
	return s.actionExecutors.capabilities()
}

type localTaskAutonomyExecutor struct {
	service    *Service
	capability domain.StewardAutonomyActionCapability
	taskType   string
}

func newReminderTaskAutonomyExecutor(service *Service) AutonomyActionExecutor {
	return reminderTaskAutonomyExecutor{service: service}
}

func newLocalTaskAutonomyExecutor(service *Service, action string, description string, taskType string) AutonomyActionExecutor {
	return localTaskAutonomyExecutor{
		service: service,
		capability: domain.StewardAutonomyActionCapability{
			Action:             action,
			Description:        description,
			TargetType:         "task",
			RiskLevel:          "low",
			MaxPermissionLevel: PermissionA3,
		},
		taskType: taskType,
	}
}

func (e localTaskAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return e.capability
}

func (e localTaskAutonomyExecutor) Simulate(_ context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	return AutonomyExecutionResult{
		TargetType:    e.capability.TargetType,
		ImpactSummary: defaultString(proposal.ImpactSummary, "would create a low-risk local task"),
	}, nil
}

func (e localTaskAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	if e.service == nil {
		return AutonomyExecutionResult{}, fmt.Errorf("local task autonomy executor is not initialized")
	}
	return executeAutonomyTask(ctx, e.service, proposal, defaultString(e.taskType, "autonomous"), nil)
}

type reminderTaskAutonomyExecutor struct {
	service *Service
}

func (e reminderTaskAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action:             AutonomyActionCreateReminderTask,
		Description:        "创建保留源任务截止时间的本地提醒任务",
		TargetType:         "task",
		RiskLevel:          "low",
		MaxPermissionLevel: PermissionA3,
	}
}

func (e reminderTaskAutonomyExecutor) Simulate(_ context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	return AutonomyExecutionResult{
		TargetType:    "task",
		ImpactSummary: defaultString(proposal.ImpactSummary, "would create one local reminder task"),
		RecoveryHint:  "dismiss the proposal to make no changes",
	}, nil
}

func (e reminderTaskAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	if e.service == nil {
		return AutonomyExecutionResult{}, fmt.Errorf("reminder task autonomy executor is not initialized")
	}
	var dueAt *time.Time
	if proposal.SourceEntityType == "task" && proposal.SourceEntityID != nil {
		if source, err := e.service.getTask(ctx, *proposal.SourceEntityID); err == nil {
			dueAt = source.DueAt
		}
	}
	if dueAt == nil {
		base := proposal.CreatedAt
		if base.IsZero() {
			base = time.Now().UTC()
		}
		fallback := base.UTC().Add(24 * time.Hour)
		dueAt = &fallback
	}
	return executeAutonomyTask(ctx, e.service, proposal, "autonomous_reminder", dueAt)
}

func executeAutonomyTask(ctx context.Context, service *Service, proposal domain.StewardAutonomyProposal, taskType string, dueAt *time.Time) (AutonomyExecutionResult, error) {
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ID:              autonomyTaskID(proposal),
		Type:            defaultString(taskType, "autonomous"),
		Title:           proposal.Title,
		Description:     defaultString(proposal.Summary, proposal.SuggestedAction),
		Priority:        "normal",
		DueAt:           dueAt,
		Source:          "autonomy",
		DataLevel:       proposal.DataLevel,
		PermissionLevel: proposal.PermissionLevel,
		RiskLevel:       proposal.RiskLevel,
		UserConfirmed:   boolPtr(proposal.Status == ProposalApproved),
	})
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	return AutonomyExecutionResult{
		TargetType:    "task",
		TargetID:      task.ID,
		ImpactSummary: "ensured idempotent local task " + task.ID,
	}, nil
}

func autonomyTaskID(proposal domain.StewardAutonomyProposal) string {
	key := strings.Join([]string{"steward-autonomy-task", proposal.ID, proposal.Action}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func executorAllowsProposal(executor AutonomyActionExecutor, proposal domain.StewardAutonomyProposal) error {
	if executor == nil {
		return fmt.Errorf("autonomy action executor is required")
	}
	capability := executor.Capability()
	if strings.TrimSpace(proposal.Action) != strings.TrimSpace(capability.Action) {
		return fmt.Errorf("executor action %s does not match proposal action %s", capability.Action, proposal.Action)
	}
	maxPermission := defaultString(capability.MaxPermissionLevel, PermissionA3)
	if permissionRank(proposal.PermissionLevel) > permissionRank(maxPermission) {
		return fmt.Errorf("action %s allows up to %s, proposal requires %s", capability.Action, maxPermission, proposal.PermissionLevel)
	}
	return nil
}

func validateAutonomyExecutionResult(executor AutonomyActionExecutor, result AutonomyExecutionResult, requireTarget bool) error {
	capability := executor.Capability()
	targetType := strings.TrimSpace(result.TargetType)
	targetID := strings.TrimSpace(result.TargetID)
	if targetType != "" && targetType != strings.TrimSpace(capability.TargetType) {
		return fmt.Errorf("action %s returned target type %s, expected %s", capability.Action, targetType, capability.TargetType)
	}
	if requireTarget && (targetType == "" || targetID == "") {
		return fmt.Errorf("action %s did not return an execution target", capability.Action)
	}
	if (targetType == "task" || targetType == "knowledge_item") && targetID != "" {
		if _, err := uuid.Parse(targetID); err != nil {
			return fmt.Errorf("action %s returned invalid %s target id: %w", capability.Action, targetType, err)
		}
	}
	return nil
}
