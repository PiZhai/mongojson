package steward

import (
	"context"
	"fmt"

	"mongojson/backend/internal/domain"
)

type autonomyDiagnosticSnapshot struct {
	PendingChanges   int
	PendingRelations int
	OpenConflicts    int
	FailedRuns       int
	OpenTasks        int
}

type readOnlyDiagnosticsAutonomyExecutor struct {
	service *Service
}

func newReadOnlyDiagnosticsAutonomyExecutor(service *Service) AutonomyActionExecutor {
	return readOnlyDiagnosticsAutonomyExecutor{service: service}
}

func (e readOnlyDiagnosticsAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action:             AutonomyActionRunReadOnlyDiagnostics,
		Description:        "只读检查本地同步、任务和自主运行状态并生成报告",
		TargetType:         "knowledge_item",
		RiskLevel:          "low",
		MaxPermissionLevel: PermissionA3,
	}
}

func (e readOnlyDiagnosticsAutonomyExecutor) Simulate(ctx context.Context, _ domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	snapshot, err := e.snapshot(ctx)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	return AutonomyExecutionResult{
		TargetType:    "knowledge_item",
		ImpactSummary: "would record local read-only diagnostics: " + snapshot.summary(),
		RecoveryHint:  "simulation made no changes",
	}, nil
}

func (e readOnlyDiagnosticsAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	if e.service == nil {
		return AutonomyExecutionResult{}, fmt.Errorf("read-only diagnostics autonomy executor is not initialized")
	}
	snapshot, err := e.snapshot(ctx)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	item, err := e.service.CreateKnowledgeItem(ctx, CreateKnowledgeInput{
		ID:              autonomyKnowledgeItemID(proposal),
		Type:            "diagnostic_report",
		Title:           defaultString(proposal.Title, "私人管家只读诊断"),
		Summary:         snapshot.summary(),
		Source:          "autonomy",
		OriginalURI:     "steward://diagnostics/" + proposal.ID,
		ImportMethod:    "readonly_diagnostics",
		DataLevel:       DataD0,
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
		ImpactSummary: "completed read-only diagnostics and stored local report " + item.ID + ": " + snapshot.summary(),
		RecoveryHint:  "delete the generated diagnostic report to undo the local write",
	}, nil
}

func (e readOnlyDiagnosticsAutonomyExecutor) snapshot(ctx context.Context) (autonomyDiagnosticSnapshot, error) {
	if e.service == nil || e.service.db == nil || e.service.db.Pool == nil {
		return autonomyDiagnosticSnapshot{}, fmt.Errorf("read-only diagnostics database is not initialized")
	}
	var snapshot autonomyDiagnosticSnapshot
	err := e.service.db.Pool.QueryRow(ctx, `
		select
			(select count(*) from steward_sync_changes where sync_status = $1),
			(select count(*) from steward_timeline_pending_events),
			(select count(*) from steward_sync_conflicts where status = $2),
			(select count(*) from steward_autonomous_runs where status = $3),
			(select count(*) from steward_tasks where deleted_at is null and status in ('open','in_progress','waiting'))
	`, SyncPending, StatusOpen, RunFailed).Scan(
		&snapshot.PendingChanges,
		&snapshot.PendingRelations,
		&snapshot.OpenConflicts,
		&snapshot.FailedRuns,
		&snapshot.OpenTasks,
	)
	if err != nil {
		return autonomyDiagnosticSnapshot{}, fmt.Errorf("collect read-only autonomy diagnostics: %w", err)
	}
	return snapshot, nil
}

func (s autonomyDiagnosticSnapshot) summary() string {
	return fmt.Sprintf(
		"pending_sync=%d pending_relations=%d open_conflicts=%d failed_autonomy_runs=%d open_tasks=%d",
		s.PendingChanges,
		s.PendingRelations,
		s.OpenConflicts,
		s.FailedRuns,
		s.OpenTasks,
	)
}
