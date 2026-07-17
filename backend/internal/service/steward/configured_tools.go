package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

const AutonomyActionConfiguredToolPrefix = "tool:"

var configuredToolActionPattern = regexp.MustCompile(`^tool:[a-z0-9][a-z0-9._-]{0,63}$`)

type UpsertToolDefinitionInput struct {
	Action             string   `json:"action"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Executable         string   `json:"executable"`
	Arguments          []string `json:"arguments"`
	WorkingDirectory   string   `json:"working_directory"`
	PermissionLevel    string   `json:"permission_level"`
	RiskLevel          string   `json:"risk_level"`
	Enabled            bool     `json:"enabled"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
	RollbackExecutable string   `json:"rollback_executable"`
	RollbackArguments  []string `json:"rollback_arguments"`
}

func (s *Service) ListToolDefinitions(ctx context.Context) ([]domain.StewardToolDefinition, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id,action,name,description,executable,arguments,working_directory,
		       permission_level,risk_level,enabled,timeout_seconds,
		       rollback_executable,rollback_arguments,created_at,updated_at
		from steward_tool_definitions order by action
	`)
	if err != nil {
		return nil, fmt.Errorf("list configured tools: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardToolDefinition{}
	for rows.Next() {
		item, scanErr := scanToolDefinition(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpsertToolDefinition(ctx context.Context, input UpsertToolDefinitionInput) (domain.StewardToolDefinition, error) {
	item, err := normalizeToolDefinition(input)
	if err != nil {
		return domain.StewardToolDefinition{}, err
	}
	current, found, err := s.findToolDefinition(ctx, item.Action)
	if err != nil {
		return domain.StewardToolDefinition{}, err
	}
	if found {
		item.ID = current.ID
		item.CreatedAt = current.CreatedAt
	} else {
		item.ID = uuid.NewString()
	}
	arguments, _ := json.Marshal(item.Arguments)
	rollbackArguments, _ := json.Marshal(item.RollbackArguments)
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_tool_definitions (
			id,action,name,description,executable,arguments,working_directory,
			permission_level,risk_level,enabled,timeout_seconds,rollback_executable,
			rollback_arguments,created_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$14)
		on conflict (action) do update set
			name=excluded.name,description=excluded.description,executable=excluded.executable,
			arguments=excluded.arguments,working_directory=excluded.working_directory,
			permission_level=excluded.permission_level,risk_level=excluded.risk_level,
			enabled=excluded.enabled,timeout_seconds=excluded.timeout_seconds,
			rollback_executable=excluded.rollback_executable,
			rollback_arguments=excluded.rollback_arguments,updated_at=excluded.updated_at
	`, item.ID, item.Action, item.Name, item.Description, item.Executable, arguments,
		item.WorkingDirectory, item.PermissionLevel, item.RiskLevel, item.Enabled,
		item.TimeoutSeconds, item.RollbackExecutable, rollbackArguments, now)
	if err != nil {
		return domain.StewardToolDefinition{}, fmt.Errorf("upsert configured tool: %w", err)
	}
	confirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "user", Action: "autonomy.tool.upsert", TargetType: "configured_tool", TargetID: &item.ID,
		Source: "manual", PermissionLevel: PermissionA7, DataLevel: DataD2,
		InputSummary:  item.Action + " permission=" + item.PermissionLevel,
		OutputSummary: fmt.Sprintf("enabled=%t executable=%s arguments=%d", item.Enabled, filepath.Base(item.Executable), len(item.Arguments)),
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK,
	})
	result, _, err := s.findToolDefinition(ctx, item.Action)
	return result, err
}

func normalizeToolDefinition(input UpsertToolDefinitionInput) (domain.StewardToolDefinition, error) {
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if !configuredToolActionPattern.MatchString(action) {
		return domain.StewardToolDefinition{}, fmt.Errorf("tool action must match tool:<name> using lowercase letters, digits, dot, underscore, or hyphen")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool name is required")
	}
	executable := filepath.Clean(strings.TrimSpace(input.Executable))
	if executable == "." || !filepath.IsAbs(executable) {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool executable must be an absolute path")
	}
	workingDirectory := strings.TrimSpace(input.WorkingDirectory)
	if workingDirectory != "" {
		workingDirectory = filepath.Clean(workingDirectory)
		if !filepath.IsAbs(workingDirectory) {
			return domain.StewardToolDefinition{}, fmt.Errorf("configured tool working_directory must be absolute")
		}
	}
	permission, err := autonomyPermissionValue(input.PermissionLevel, "")
	if err != nil {
		return domain.StewardToolDefinition{}, err
	}
	if !ownerModeEnabled() && (permissionRank(permission) < permissionRank(PermissionA4) || permissionRank(permission) > permissionRank(PermissionA7)) {
		return domain.StewardToolDefinition{}, fmt.Errorf("R3.0 configured broker tools must use A4-A7; use R2 tools for A0-A3 and keep A8-A9 disabled")
	}
	risk, err := autonomyRiskValue(input.RiskLevel, "high")
	if err != nil {
		return domain.StewardToolDefinition{}, err
	}
	timeoutSeconds := input.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 60
	}
	if timeoutSeconds < 1 || timeoutSeconds > 3600 {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool timeout_seconds must be between 1 and 3600")
	}
	rollbackExecutable := strings.TrimSpace(input.RollbackExecutable)
	if rollbackExecutable != "" {
		rollbackExecutable = filepath.Clean(rollbackExecutable)
		if !filepath.IsAbs(rollbackExecutable) {
			return domain.StewardToolDefinition{}, fmt.Errorf("rollback_executable must be an absolute path")
		}
	}
	if rollbackExecutable == "" && len(input.RollbackArguments) > 0 {
		return domain.StewardToolDefinition{}, fmt.Errorf("rollback_arguments require rollback_executable")
	}
	return domain.StewardToolDefinition{
		Action: action, Name: name, Description: strings.TrimSpace(input.Description),
		Executable: executable, Arguments: append([]string{}, input.Arguments...),
		WorkingDirectory: workingDirectory, PermissionLevel: permission, RiskLevel: risk,
		Enabled: input.Enabled, TimeoutSeconds: timeoutSeconds,
		RollbackExecutable: rollbackExecutable, RollbackArguments: append([]string{}, input.RollbackArguments...),
	}, nil
}

func (s *Service) findToolDefinition(ctx context.Context, action string) (domain.StewardToolDefinition, bool, error) {
	item, err := scanToolDefinition(s.db.Pool.QueryRow(ctx, `
		select id,action,name,description,executable,arguments,working_directory,
		       permission_level,risk_level,enabled,timeout_seconds,
		       rollback_executable,rollback_arguments,created_at,updated_at
		from steward_tool_definitions where action=$1
	`, strings.TrimSpace(action)))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardToolDefinition{}, false, nil
	}
	return item, err == nil, err
}

func scanToolDefinition(row rowScanner) (domain.StewardToolDefinition, error) {
	var item domain.StewardToolDefinition
	var arguments, rollbackArguments []byte
	err := row.Scan(&item.ID, &item.Action, &item.Name, &item.Description, &item.Executable,
		&arguments, &item.WorkingDirectory, &item.PermissionLevel, &item.RiskLevel,
		&item.Enabled, &item.TimeoutSeconds, &item.RollbackExecutable, &rollbackArguments,
		&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(arguments, &item.Arguments); err != nil {
		return item, fmt.Errorf("decode configured tool arguments: %w", err)
	}
	if item.Arguments == nil {
		item.Arguments = []string{}
	}
	if err := json.Unmarshal(rollbackArguments, &item.RollbackArguments); err != nil {
		return item, fmt.Errorf("decode configured tool rollback arguments: %w", err)
	}
	if item.RollbackArguments == nil {
		item.RollbackArguments = []string{}
	}
	return item, nil
}

type configuredToolAutonomyExecutor struct {
	service *Service
	action  string
}

func (e configuredToolAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action: e.action, Description: "通过独立 Privilege Broker 运行系统级固定能力",
		TargetType: "configured_tool_run", RiskLevel: "critical", MaxPermissionLevel: PermissionA7,
	}
}

func (e configuredToolAutonomyExecutor) Simulate(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	tool, capability, err := e.authorizedTool(ctx, proposal)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	recovery := ""
	if tool.RollbackExecutable != "" {
		rollbackName := capability.Name + ".rollback"
		if _, rollbackErr := e.service.privilegeBroker.Capability(ctx, rollbackName); rollbackErr != nil {
			return AutonomyExecutionResult{}, fmt.Errorf("rollback metadata requires broker capability %s: %w", rollbackName, rollbackErr)
		}
		recovery = "rollback is independently registered as broker capability " + rollbackName
	}
	return AutonomyExecutionResult{
		TargetType:    "configured_tool_run",
		ImpactSummary: fmt.Sprintf("would ask isolated broker capability %s to run pinned %s with %d fixed arguments as %s", capability.Name, capability.ExecutableName, capability.ArgumentCount, capability.PermissionLevel),
		RecoveryHint:  recovery,
	}, nil
}

func (e configuredToolAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	_, capability, err := e.authorizedTool(ctx, proposal)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	stopped, generation, err := e.service.runtimeExecutionState(ctx)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	if stopped {
		return AutonomyExecutionResult{}, ErrExecutionEmergencyStopped
	}
	approvalProof, err := e.service.approvedAutonomyApprovalProof(ctx, proposal.ID)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	response, err := e.service.privilegeBroker.ExecuteCapability(ctx, privilegebroker.Authorization{
		Capability: capability.Name, Subject: "s4:" + proposal.ID,
		PlanHash: autonomyBrokerPlanHash(proposal), ApprovalRef: approvalProof.Claims.ProofID,
		ApprovalProof:     approvalProof,
		ControlGeneration: generation,
	})
	if err != nil {
		return AutonomyExecutionResult{}, fmt.Errorf("privilege broker %s: %w", brokerExecutionErrorCode(err), err)
	}
	receipt := response.Receipt.Payload
	return AutonomyExecutionResult{
		TargetType: "configured_tool_run", TargetID: proposal.ID,
		ImpactSummary: fmt.Sprintf("broker executed %s; receipt=%s stdout_sha256=%s stderr_sha256=%s", capability.Name, receipt.ExecutionID, receipt.StdoutSHA256, receipt.StderrSHA256),
	}, nil
}

func (e configuredToolAutonomyExecutor) authorizedTool(ctx context.Context, proposal domain.StewardAutonomyProposal) (domain.StewardToolDefinition, privilegebroker.PublicCapability, error) {
	if e.service == nil || e.service.db == nil || e.service.db.Pool == nil {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("configured tool executor is not initialized")
	}
	tool, found, err := e.service.findToolDefinition(ctx, e.action)
	if err != nil {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, err
	}
	if !found || !tool.Enabled {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("configured tool %s is missing or disabled", e.action)
	}
	if !e.service.runtimeR3 || e.service.privilegeBroker == nil {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("configured tools require the independent R3 privilege broker")
	}
	if e.service.privilegeBrokerError != nil {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, e.service.privilegeBrokerError
	}
	capability, err := e.service.privilegeBroker.Capability(ctx, e.action)
	if err != nil {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, err
	}
	if capability.PermissionLevel != tool.PermissionLevel || autonomyRiskRank(capability.RiskLevel) < autonomyRiskRank(tool.RiskLevel) {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("broker policy is weaker than the registered tool declaration")
	}
	if !strings.EqualFold(filepath.Base(tool.Executable), capability.ExecutableName) || len(tool.Arguments) != capability.ArgumentCount {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("registered tool metadata does not match the broker-owned capability")
	}
	if !ownerModeEnabled() && permissionRank(proposal.PermissionLevel) < permissionRank(capability.PermissionLevel) {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("configured tool %s requires at least %s", e.action, capability.PermissionLevel)
	}
	if autonomyRiskRank(proposal.RiskLevel) < autonomyRiskRank(capability.RiskLevel) {
		return domain.StewardToolDefinition{}, privilegebroker.PublicCapability{}, fmt.Errorf("configured tool %s requires risk level %s", e.action, capability.RiskLevel)
	}
	return tool, capability, nil
}

func autonomyRiskRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 0
	}
}
