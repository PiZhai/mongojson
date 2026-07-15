package steward

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	PolicyModeDeny   = "deny"
	PolicyModeManual = "manual"
	PolicyModeAuto   = "auto"

	ModelContentMetadata = "metadata"
	ModelContentSummary  = "summary"
	ModelContentRedacted = "redacted"
	ModelContentRaw      = "raw"
)

var ErrDataPolicyDenied = errors.New("steward data policy denied the operation")

type UpsertDataPolicyInput struct {
	DataLevel             string     `json:"data_level"`
	SourcePattern         string     `json:"source_pattern"`
	CollectMode           string     `json:"collect_mode"`
	ModelMode             string     `json:"model_mode"`
	ModelContentMode      string     `json:"model_content_mode"`
	AllowLocalPersistence *bool      `json:"allow_local_persistence,omitempty"`
	AllowSync             *bool      `json:"allow_sync,omitempty"`
	RequireEncryption     *bool      `json:"require_encryption,omitempty"`
	ConsentExpiresAt      *time.Time `json:"consent_expires_at,omitempty"`
	Description           *string    `json:"description,omitempty"`
}

type UpsertPermissionPolicyInput struct {
	PermissionLevel   string  `json:"permission_level"`
	ActionPattern     string  `json:"action_pattern"`
	ExecutionMode     string  `json:"execution_mode"`
	RequireSimulation *bool   `json:"require_simulation,omitempty"`
	RequireRollback   *bool   `json:"require_rollback,omitempty"`
	MaxBatchSize      *int    `json:"max_batch_size,omitempty"`
	CooldownSeconds   *int    `json:"cooldown_seconds,omitempty"`
	Description       *string `json:"description,omitempty"`
}

func (s *Service) ensureAutomationPolicyDefaults(ctx context.Context, now time.Time) error {
	dataDefaults := []domain.StewardDataPolicy{
		{DataLevel: DataD0, SourcePattern: "*", CollectMode: PolicyModeAuto, ModelMode: PolicyModeAuto, ModelContentMode: ModelContentRaw, AllowLocalPersistence: true, AllowSync: true, Description: "用户输入可采集并按模型策略处理"},
		{DataLevel: DataD1, SourcePattern: "*", CollectMode: PolicyModeAuto, ModelMode: PolicyModeAuto, ModelContentMode: ModelContentSummary, AllowLocalPersistence: true, AllowSync: true, Description: "公开资料默认发送摘要"},
		{DataLevel: DataD2, SourcePattern: "*", CollectMode: PolicyModeAuto, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentSummary, AllowLocalPersistence: true, AllowSync: false, Description: "启用数据源后可自动采集本地元数据"},
		{DataLevel: DataD3, SourcePattern: "*", CollectMode: PolicyModeManual, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentRedacted, AllowLocalPersistence: true, AllowSync: false, Description: "用户内容默认只允许手动采集"},
		{DataLevel: DataD4, SourcePattern: "*", CollectMode: PolicyModeManual, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentRedacted, AllowLocalPersistence: true, AllowSync: false, RequireEncryption: true, Description: "敏感个人内容默认手动且加密"},
		{DataLevel: DataD5, SourcePattern: "*", CollectMode: PolicyModeDeny, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentRedacted, AllowLocalPersistence: false, AllowSync: false, RequireEncryption: true, Description: "凭据默认拒绝；必须独立显式授权"},
		{DataLevel: DataD6, SourcePattern: "*", CollectMode: PolicyModeManual, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentRedacted, AllowLocalPersistence: true, AllowSync: false, RequireEncryption: true, Description: "高风险个人数据默认手动且加密"},
	}
	for _, item := range dataDefaults {
		_, err := s.db.Pool.Exec(ctx, `
			insert into steward_data_policies (
				id,data_level,source_pattern,collect_mode,model_mode,model_content_mode,
				allow_local_persistence,allow_sync,require_encryption,description,created_at,updated_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
			on conflict (data_level,source_pattern) do nothing
		`, uuid.NewString(), item.DataLevel, item.SourcePattern, item.CollectMode, item.ModelMode,
			item.ModelContentMode, item.AllowLocalPersistence, item.AllowSync, item.RequireEncryption,
			item.Description, now)
		if err != nil {
			return fmt.Errorf("ensure %s data policy: %w", item.DataLevel, err)
		}
	}

	for rank := 0; rank <= 9; rank++ {
		level := fmt.Sprintf("A%d", rank)
		mode := PolicyModeDeny
		requireSimulation := true
		requireRollback := rank >= 4
		if rank <= 3 {
			mode = PolicyModeAuto
			requireSimulation = false
			requireRollback = false
		}
		_, err := s.db.Pool.Exec(ctx, `
			insert into steward_permission_policies (
				id,permission_level,action_pattern,execution_mode,require_simulation,
				require_rollback,max_batch_size,cooldown_seconds,description,created_at,updated_at
			) values ($1,$2,'*',$3,$4,$5,1,0,$6,$7,$7)
			on conflict (permission_level,action_pattern) do nothing
		`, uuid.NewString(), level, mode, requireSimulation, requireRollback,
			fmt.Sprintf("%s 自动执行策略", level), now)
		if err != nil {
			return fmt.Errorf("ensure %s permission policy: %w", level, err)
		}
	}
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_permission_policies (
			id,permission_level,action_pattern,execution_mode,require_simulation,
			require_rollback,max_batch_size,cooldown_seconds,description,created_at,updated_at
		) values ($1,$2,'model:*',$3,false,false,100,0,$4,$5,$5)
		on conflict (permission_level,action_pattern) do nothing
	`, uuid.NewString(), PermissionA6, PolicyModeAuto, "已配置模型的数据外发动作", now)
	if err != nil {
		return fmt.Errorf("ensure model disclosure permission policy: %w", err)
	}
	return nil
}

func (s *Service) ListDataPolicies(ctx context.Context) ([]domain.StewardDataPolicy, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id,data_level,source_pattern,collect_mode,model_mode,model_content_mode,
		       allow_local_persistence,allow_sync,require_encryption,consent_expires_at,
		       description,created_at,updated_at
		from steward_data_policies order by data_level,source_pattern
	`)
	if err != nil {
		return nil, fmt.Errorf("list data policies: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardDataPolicy{}
	for rows.Next() {
		item, err := scanDataPolicy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpsertDataPolicy(ctx context.Context, input UpsertDataPolicyInput) (domain.StewardDataPolicy, error) {
	level, err := autonomyDataLevelValue(input.DataLevel, "")
	if err != nil {
		return domain.StewardDataPolicy{}, err
	}
	pattern, err := automationPattern(input.SourcePattern)
	if err != nil {
		return domain.StewardDataPolicy{}, err
	}
	current, found, err := s.findDataPolicy(ctx, level, pattern)
	if err != nil {
		return domain.StewardDataPolicy{}, err
	}
	if !found {
		current = domain.StewardDataPolicy{ID: uuid.NewString(), DataLevel: level, SourcePattern: pattern, CollectMode: PolicyModeDeny, ModelMode: PolicyModeDeny, ModelContentMode: ModelContentSummary, AllowLocalPersistence: true}
	}
	if strings.TrimSpace(input.CollectMode) != "" {
		current.CollectMode, err = automationMode(input.CollectMode)
		if err != nil {
			return domain.StewardDataPolicy{}, err
		}
	}
	if strings.TrimSpace(input.ModelMode) != "" {
		current.ModelMode, err = automationMode(input.ModelMode)
		if err != nil {
			return domain.StewardDataPolicy{}, err
		}
	}
	if strings.TrimSpace(input.ModelContentMode) != "" {
		current.ModelContentMode, err = modelContentMode(input.ModelContentMode)
		if err != nil {
			return domain.StewardDataPolicy{}, err
		}
	}
	if input.AllowLocalPersistence != nil {
		current.AllowLocalPersistence = *input.AllowLocalPersistence
	}
	if input.AllowSync != nil {
		current.AllowSync = *input.AllowSync
	}
	if input.RequireEncryption != nil {
		current.RequireEncryption = *input.RequireEncryption
	}
	if input.ConsentExpiresAt != nil {
		value := input.ConsentExpiresAt.UTC()
		current.ConsentExpiresAt = &value
	}
	if input.Description != nil {
		current.Description = strings.TrimSpace(*input.Description)
	}
	if dataLevelRank(level) >= dataLevelRank(DataD4) && current.AllowLocalPersistence {
		current.RequireEncryption = true
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_data_policies (
			id,data_level,source_pattern,collect_mode,model_mode,model_content_mode,
			allow_local_persistence,allow_sync,require_encryption,consent_expires_at,
			description,created_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
		on conflict (data_level,source_pattern) do update set
			collect_mode=excluded.collect_mode,model_mode=excluded.model_mode,
			model_content_mode=excluded.model_content_mode,
			allow_local_persistence=excluded.allow_local_persistence,allow_sync=excluded.allow_sync,
			require_encryption=excluded.require_encryption,consent_expires_at=excluded.consent_expires_at,
			description=excluded.description,updated_at=excluded.updated_at
	`, current.ID, level, pattern, current.CollectMode, current.ModelMode, current.ModelContentMode,
		current.AllowLocalPersistence, current.AllowSync, current.RequireEncryption,
		current.ConsentExpiresAt, current.Description, now)
	if err != nil {
		return domain.StewardDataPolicy{}, fmt.Errorf("upsert data policy: %w", err)
	}
	confirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "user", Action: "policy.data.upsert", TargetType: "data_policy", TargetID: &current.ID, Source: "manual", PermissionLevel: PermissionA3, DataLevel: DataD2, InputSummary: level + ":" + pattern, OutputSummary: fmt.Sprintf("collect=%s model=%s content=%s", current.CollectMode, current.ModelMode, current.ModelContentMode), UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK})
	item, _, err := s.findDataPolicy(ctx, level, pattern)
	return item, err
}

func (s *Service) ListPermissionPolicies(ctx context.Context) ([]domain.StewardPermissionPolicy, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id,permission_level,action_pattern,execution_mode,require_simulation,
		       require_rollback,max_batch_size,cooldown_seconds,description,created_at,updated_at
		from steward_permission_policies order by permission_level,action_pattern
	`)
	if err != nil {
		return nil, fmt.Errorf("list permission policies: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardPermissionPolicy{}
	for rows.Next() {
		item, err := scanPermissionPolicy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpsertPermissionPolicy(ctx context.Context, input UpsertPermissionPolicyInput) (domain.StewardPermissionPolicy, error) {
	level, err := autonomyPermissionValue(input.PermissionLevel, "")
	if err != nil {
		return domain.StewardPermissionPolicy{}, err
	}
	pattern, err := automationPattern(input.ActionPattern)
	if err != nil {
		return domain.StewardPermissionPolicy{}, err
	}
	current, found, err := s.findPermissionPolicy(ctx, level, pattern)
	if err != nil {
		return domain.StewardPermissionPolicy{}, err
	}
	if !found {
		current = domain.StewardPermissionPolicy{ID: uuid.NewString(), PermissionLevel: level, ActionPattern: pattern, ExecutionMode: PolicyModeDeny, RequireSimulation: true, MaxBatchSize: 1}
	}
	if strings.TrimSpace(input.ExecutionMode) != "" {
		current.ExecutionMode, err = automationMode(input.ExecutionMode)
		if err != nil {
			return domain.StewardPermissionPolicy{}, err
		}
	}
	if input.RequireSimulation != nil {
		current.RequireSimulation = *input.RequireSimulation
	}
	if input.RequireRollback != nil {
		current.RequireRollback = *input.RequireRollback
	}
	if input.MaxBatchSize != nil {
		if *input.MaxBatchSize < 1 || *input.MaxBatchSize > 10000 {
			return domain.StewardPermissionPolicy{}, fmt.Errorf("max_batch_size must be between 1 and 10000")
		}
		current.MaxBatchSize = *input.MaxBatchSize
	}
	if input.CooldownSeconds != nil {
		if *input.CooldownSeconds < 0 || *input.CooldownSeconds > 31_536_000 {
			return domain.StewardPermissionPolicy{}, fmt.Errorf("cooldown_seconds must be between 0 and 31536000")
		}
		current.CooldownSeconds = *input.CooldownSeconds
	}
	if input.Description != nil {
		current.Description = strings.TrimSpace(*input.Description)
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_permission_policies (
			id,permission_level,action_pattern,execution_mode,require_simulation,
			require_rollback,max_batch_size,cooldown_seconds,description,created_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		on conflict (permission_level,action_pattern) do update set
			execution_mode=excluded.execution_mode,require_simulation=excluded.require_simulation,
			require_rollback=excluded.require_rollback,max_batch_size=excluded.max_batch_size,
			cooldown_seconds=excluded.cooldown_seconds,description=excluded.description,
			updated_at=excluded.updated_at
	`, current.ID, level, pattern, current.ExecutionMode, current.RequireSimulation,
		current.RequireRollback, current.MaxBatchSize, current.CooldownSeconds, current.Description, now)
	if err != nil {
		return domain.StewardPermissionPolicy{}, fmt.Errorf("upsert permission policy: %w", err)
	}
	confirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "user", Action: "policy.permission.upsert", TargetType: "permission_policy", TargetID: &current.ID, Source: "manual", PermissionLevel: PermissionA3, DataLevel: DataD2, InputSummary: level + ":" + pattern, OutputSummary: "execution=" + current.ExecutionMode, UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK})
	item, _, err := s.findPermissionPolicy(ctx, level, pattern)
	return item, err
}

func (s *Service) ResolveDataPolicy(ctx context.Context, level, source string) (domain.StewardDataPolicy, error) {
	level, err := autonomyDataLevelValue(level, "")
	if err != nil {
		return domain.StewardDataPolicy{}, err
	}
	items, err := s.ListDataPolicies(ctx)
	if err != nil {
		return domain.StewardDataPolicy{}, err
	}
	candidates := []domain.StewardDataPolicy{}
	now := time.Now().UTC()
	for _, item := range items {
		if item.DataLevel != level || (item.ConsentExpiresAt != nil && !item.ConsentExpiresAt.After(now)) {
			continue
		}
		matched, matchErr := path.Match(item.SourcePattern, source)
		if matchErr == nil && matched {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return domain.StewardDataPolicy{}, fmt.Errorf("%w: no active %s policy matches %s", ErrDataPolicyDenied, level, source)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return policySpecificity(candidates[i].SourcePattern) > policySpecificity(candidates[j].SourcePattern)
	})
	return candidates[0], nil
}

func (s *Service) ResolvePermissionPolicy(ctx context.Context, level, action string) (domain.StewardPermissionPolicy, error) {
	level, err := autonomyPermissionValue(level, "")
	if err != nil {
		return domain.StewardPermissionPolicy{}, err
	}
	items, err := s.ListPermissionPolicies(ctx)
	if err != nil {
		return domain.StewardPermissionPolicy{}, err
	}
	candidates := []domain.StewardPermissionPolicy{}
	for _, item := range items {
		if item.PermissionLevel != level {
			continue
		}
		matched, matchErr := path.Match(item.ActionPattern, action)
		if matchErr == nil && matched {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return domain.StewardPermissionPolicy{}, fmt.Errorf("no %s permission policy matches %s", level, action)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return policySpecificity(candidates[i].ActionPattern) > policySpecificity(candidates[j].ActionPattern)
	})
	return candidates[0], nil
}

func (s *Service) findDataPolicy(ctx context.Context, level, pattern string) (domain.StewardDataPolicy, bool, error) {
	item, err := scanDataPolicy(s.db.Pool.QueryRow(ctx, `
		select id,data_level,source_pattern,collect_mode,model_mode,model_content_mode,
		       allow_local_persistence,allow_sync,require_encryption,consent_expires_at,
		       description,created_at,updated_at
		from steward_data_policies where data_level=$1 and source_pattern=$2
	`, level, pattern))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardDataPolicy{}, false, nil
	}
	return item, err == nil, err
}

func (s *Service) findPermissionPolicy(ctx context.Context, level, pattern string) (domain.StewardPermissionPolicy, bool, error) {
	item, err := scanPermissionPolicy(s.db.Pool.QueryRow(ctx, `
		select id,permission_level,action_pattern,execution_mode,require_simulation,
		       require_rollback,max_batch_size,cooldown_seconds,description,created_at,updated_at
		from steward_permission_policies where permission_level=$1 and action_pattern=$2
	`, level, pattern))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardPermissionPolicy{}, false, nil
	}
	return item, err == nil, err
}

type rowScanner interface{ Scan(...any) error }

func scanDataPolicy(row rowScanner) (domain.StewardDataPolicy, error) {
	var item domain.StewardDataPolicy
	err := row.Scan(&item.ID, &item.DataLevel, &item.SourcePattern, &item.CollectMode,
		&item.ModelMode, &item.ModelContentMode, &item.AllowLocalPersistence, &item.AllowSync,
		&item.RequireEncryption, &item.ConsentExpiresAt, &item.Description, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanPermissionPolicy(row rowScanner) (domain.StewardPermissionPolicy, error) {
	var item domain.StewardPermissionPolicy
	err := row.Scan(&item.ID, &item.PermissionLevel, &item.ActionPattern, &item.ExecutionMode,
		&item.RequireSimulation, &item.RequireRollback, &item.MaxBatchSize, &item.CooldownSeconds,
		&item.Description, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func automationMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case PolicyModeDeny, PolicyModeManual, PolicyModeAuto:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported automation mode %q", value)
	}
}

func modelContentMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case ModelContentMetadata, ModelContentSummary, ModelContentRedacted, ModelContentRaw:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported model content mode %q", value)
	}
}

func automationPattern(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "*"
	}
	if len(value) > 200 {
		return "", fmt.Errorf("policy pattern is too long")
	}
	if _, err := path.Match(value, "policy-probe"); err != nil {
		return "", fmt.Errorf("invalid policy pattern %q: %w", value, err)
	}
	return value, nil
}

func policySpecificity(pattern string) int {
	return len(strings.ReplaceAll(strings.ReplaceAll(pattern, "*", ""), "?", ""))
}

func dataPolicyAllowsCollection(policy domain.StewardDataPolicy, systemGenerated bool) bool {
	if !policy.AllowLocalPersistence {
		return false
	}
	switch policy.CollectMode {
	case PolicyModeAuto:
		return true
	case PolicyModeManual:
		return !systemGenerated
	default:
		return false
	}
}

func dataPolicyAllowsManualModel(policy domain.StewardDataPolicy) bool {
	return policy.ModelMode == PolicyModeManual || policy.ModelMode == PolicyModeAuto
}

func (s *Service) permissionExecutionCooldownReady(ctx context.Context, proposal domain.StewardAutonomyProposal, policy domain.StewardPermissionPolicy) (bool, time.Time, error) {
	if policy.CooldownSeconds <= 0 {
		return true, time.Time{}, nil
	}
	var last time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select coalesce(max(r.created_at),to_timestamp(0))
		from steward_autonomous_runs r
		join steward_autonomy_proposals p on p.id=r.proposal_id
		where r.status=$1 and r.mode=$2 and p.action=$3 and p.permission_level=$4
	`, RunSuccess, RunModeExecute, proposal.Action, proposal.PermissionLevel).Scan(&last)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, err
	}
	if last.IsZero() {
		return true, time.Time{}, nil
	}
	retryAt := last.Add(time.Duration(policy.CooldownSeconds) * time.Second)
	return !retryAt.After(time.Now().UTC()), retryAt, nil
}
