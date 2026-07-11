package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

func (s *Service) GetAutonomySettings(ctx context.Context) (domain.StewardAutonomySettings, error) {
	var settings domain.StewardAutonomySettings
	if err := s.db.Pool.QueryRow(ctx, `
		select id, paused, mode, max_auto_permission, updated_at
		from steward_autonomy_settings
		where id = $1
	`, AutonomySettingsID).Scan(&settings.ID, &settings.Paused, &settings.Mode,
		&settings.MaxAutoPermission, &settings.UpdatedAt); err != nil {
		return domain.StewardAutonomySettings{}, fmt.Errorf("get autonomy settings: %w", err)
	}
	return settings, nil
}

func (s *Service) UpdateAutonomySettings(ctx context.Context, input UpdateAutonomySettingsInput) (domain.StewardAutonomySettings, error) {
	requestedMode := ""
	if strings.TrimSpace(input.Mode) != "" {
		var err error
		requestedMode, err = autonomyModeValue(input.Mode, "")
		if err != nil {
			return domain.StewardAutonomySettings{}, err
		}
	}
	requestedMaxPermission := ""
	if strings.TrimSpace(input.MaxAutoPermission) != "" {
		var err error
		requestedMaxPermission, err = autonomyAutoPermissionValue(input.MaxAutoPermission, "")
		if err != nil {
			return domain.StewardAutonomySettings{}, err
		}
	}
	gate, err := acquireAutonomyPolicyWriteGate(ctx, s.db.Pool)
	if err != nil {
		return domain.StewardAutonomySettings{}, err
	}
	defer gate.Release()
	current, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomySettings{}, err
	}
	paused := current.Paused
	if input.Paused != nil {
		paused = *input.Paused
	}
	mode := current.Mode
	if requestedMode != "" {
		mode = requestedMode
	}
	maxPermission := current.MaxAutoPermission
	if requestedMaxPermission != "" {
		maxPermission = requestedMaxPermission
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_settings
		set paused = $1, mode = $2, max_auto_permission = $3, updated_at = $4
		where id = $5
	`, paused, mode, maxPermission, now, AutonomySettingsID); err != nil {
		return domain.StewardAutonomySettings{}, fmt.Errorf("update autonomy settings: %w", err)
	}
	action := "autonomy.settings.update"
	if current.Paused != paused {
		action = "autonomy.pause"
		if !paused {
			action = "autonomy.resume"
		}
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          action,
		TargetType:      "autonomy",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    fmt.Sprintf("paused=%t mode=%s max=%s", paused, mode, maxPermission),
		OutputSummary:   "autonomy settings updated",
		ResultStatus:    ResultOK,
	})
	return s.GetAutonomySettings(ctx)
}

func (s *Service) ListAutonomyRules(ctx context.Context) ([]domain.StewardAutonomyRule, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, name, trigger_type, target_type, action, policy, risk_level,
		       max_permission_level, enabled, scope_summary, created_at, updated_at
		from steward_autonomy_rules
		order by enabled desc, name
	`)
	if err != nil {
		return nil, fmt.Errorf("list autonomy rules: %w", err)
	}
	defer rows.Close()

	rules := []domain.StewardAutonomyRule{}
	for rows.Next() {
		rule, err := scanAutonomyRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Service) UpdateAutonomyRule(ctx context.Context, id string, input UpdateAutonomyRuleInput) (domain.StewardAutonomyRule, error) {
	var requestedPolicy *string
	if input.Policy != nil {
		policy, err := autonomyPolicyValue(*input.Policy, "")
		if err != nil {
			return domain.StewardAutonomyRule{}, err
		}
		requestedPolicy = &policy
	}
	var requestedMaxPermission *string
	if input.MaxPermissionLevel != nil {
		permission, err := autonomyPermissionValue(*input.MaxPermissionLevel, "")
		if err != nil {
			return domain.StewardAutonomyRule{}, err
		}
		requestedMaxPermission = &permission
	}
	gate, err := acquireAutonomyPolicyWriteGate(ctx, s.db.Pool)
	if err != nil {
		return domain.StewardAutonomyRule{}, err
	}
	defer gate.Release()
	current, err := s.getAutonomyRule(ctx, id)
	if err != nil {
		return domain.StewardAutonomyRule{}, err
	}
	policy := current.Policy
	if requestedPolicy != nil {
		policy = *requestedPolicy
	}
	enabled := current.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	maxPermission := current.MaxPermissionLevel
	if requestedMaxPermission != nil {
		maxPermission = *requestedMaxPermission
	}
	scope := current.ScopeSummary
	if input.ScopeSummary != nil {
		scope = strings.TrimSpace(*input.ScopeSummary)
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_autonomy_rules
		set policy = $1, enabled = $2, max_permission_level = $3, scope_summary = $4, updated_at = $5
		where id = $6
	`, policy, enabled, maxPermission, scope, now, id); err != nil {
		return domain.StewardAutonomyRule{}, fmt.Errorf("update autonomy rule: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "autonomy.rule.update",
		TargetType:      "autonomy_rule",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    current.Name,
		OutputSummary:   fmt.Sprintf("enabled=%t policy=%s", enabled, policy),
		ResultStatus:    ResultOK,
	})
	return s.getAutonomyRule(ctx, id)
}
