package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) ListAutonomousRuns(ctx context.Context, limit int) ([]domain.StewardAutonomousRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, coalesce(proposal_id::text, ''), coalesce(rule_id::text, ''), mode, status, trigger_reason, impact_summary,
		       recovery_hint, coalesce(audit_id::text, ''), created_at
		from steward_autonomous_runs
		order by created_at desc
		limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list autonomous runs: %w", err)
	}
	defer rows.Close()
	runs := []domain.StewardAutonomousRun{}
	for rows.Next() {
		run, err := scanAutonomousRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Service) recordAutonomousRun(ctx context.Context, proposalID *string, ruleID *string, mode string, status string, triggerReason string, impactSummary string, recoveryHint string) (domain.StewardAutonomousRun, error) {
	var errorSummary *string
	if status == RunFailed {
		summary := strings.TrimSpace(recoveryHint)
		if summary == "" {
			summary = strings.TrimSpace(impactSummary)
		}
		errorSummary = stringPtr(summary)
	}
	auditID, _ := s.recordAudit(ctx, AuditInput{
		Actor:           "autonomy",
		Action:          "autonomy.run." + status,
		TargetType:      "autonomous_run",
		Source:          mode,
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    triggerReason,
		OutputSummary:   impactSummary,
		ResultStatus:    mapRunStatusToAudit(status),
		ErrorSummary:    errorSummary,
	})
	now := time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_autonomous_runs (
			id, proposal_id, rule_id, mode, status, trigger_reason, impact_summary,
			recovery_hint, audit_id, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		returning id, coalesce(proposal_id::text, ''), coalesce(rule_id::text, ''), mode, status, trigger_reason, impact_summary,
		          recovery_hint, coalesce(audit_id::text, ''), created_at
	`, uuid.NewString(), proposalID, ruleID, mode, status, triggerReason, impactSummary,
		recoveryHint, auditID, now)
	run, err := scanAutonomousRun(row)
	if err != nil {
		return domain.StewardAutonomousRun{}, fmt.Errorf("record autonomous run: %w", err)
	}
	return run, nil
}
