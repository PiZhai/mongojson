package steward

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
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
		Action: e.action, Description: "运行用户明确登记的结构化本地工具",
		TargetType: "configured_tool_run", RiskLevel: "critical", MaxPermissionLevel: PermissionA9,
	}
}

func (e configuredToolAutonomyExecutor) Simulate(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	tool, err := e.authorizedTool(ctx, proposal)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	if _, err := os.Stat(tool.Executable); err != nil {
		return AutonomyExecutionResult{}, fmt.Errorf("configured tool executable is unavailable: %w", err)
	}
	if tool.WorkingDirectory != "" {
		if info, err := os.Stat(tool.WorkingDirectory); err != nil || !info.IsDir() {
			return AutonomyExecutionResult{}, fmt.Errorf("configured tool working directory is unavailable")
		}
	}
	recovery := ""
	if tool.RollbackExecutable != "" {
		recovery = fmt.Sprintf("rollback configured with %s and %d fixed arguments", filepath.Base(tool.RollbackExecutable), len(tool.RollbackArguments))
	}
	return AutonomyExecutionResult{
		TargetType:    "configured_tool_run",
		ImpactSummary: fmt.Sprintf("would run %s with %d fixed arguments as %s", filepath.Base(tool.Executable), len(tool.Arguments), tool.PermissionLevel),
		RecoveryHint:  recovery,
	}, nil
}

func (e configuredToolAutonomyExecutor) Execute(ctx context.Context, proposal domain.StewardAutonomyProposal) (AutonomyExecutionResult, error) {
	tool, err := e.authorizedTool(ctx, proposal)
	if err != nil {
		return AutonomyExecutionResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(tool.TimeoutSeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, tool.Executable, tool.Arguments...)
	command.Dir = tool.WorkingDirectory
	command.Env = configuredToolEnvironment()
	var output cappedToolOutput
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if runCtx.Err() != nil {
			return AutonomyExecutionResult{}, fmt.Errorf("configured tool timed out after %d seconds", tool.TimeoutSeconds)
		}
		return AutonomyExecutionResult{}, fmt.Errorf("configured tool exited unsuccessfully: %w; output_sha256=%s bytes=%d", err, output.digest(), output.total)
	}
	recovery := ""
	if tool.RollbackExecutable != "" {
		recovery = fmt.Sprintf("rollback available through %s with %d fixed arguments", filepath.Base(tool.RollbackExecutable), len(tool.RollbackArguments))
	}
	return AutonomyExecutionResult{
		TargetType: "configured_tool_run", TargetID: proposal.ID,
		ImpactSummary: fmt.Sprintf("ran %s successfully; output_sha256=%s bytes=%d", tool.Name, output.digest(), output.total),
		RecoveryHint:  recovery,
	}, nil
}

func (e configuredToolAutonomyExecutor) authorizedTool(ctx context.Context, proposal domain.StewardAutonomyProposal) (domain.StewardToolDefinition, error) {
	if e.service == nil || e.service.db == nil || e.service.db.Pool == nil {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool executor is not initialized")
	}
	tool, found, err := e.service.findToolDefinition(ctx, e.action)
	if err != nil {
		return domain.StewardToolDefinition{}, err
	}
	if !found || !tool.Enabled {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool %s is missing or disabled", e.action)
	}
	if permissionRank(proposal.PermissionLevel) < permissionRank(tool.PermissionLevel) {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool %s requires at least %s", e.action, tool.PermissionLevel)
	}
	if autonomyRiskRank(proposal.RiskLevel) < autonomyRiskRank(tool.RiskLevel) {
		return domain.StewardToolDefinition{}, fmt.Errorf("configured tool %s requires risk level %s", e.action, tool.RiskLevel)
	}
	return tool, nil
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

type cappedToolOutput struct {
	buffer bytes.Buffer
	total  int64
}

func (w *cappedToolOutput) Write(value []byte) (int, error) {
	w.total += int64(len(value))
	remaining := 32*1024 - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(value[:min(remaining, len(value))])
	}
	return len(value), nil
}

func (w *cappedToolOutput) digest() string {
	digest := sha256.Sum256(w.buffer.Bytes())
	return hex.EncodeToString(digest[:])
}

func configuredToolEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true,
		"TEMP": true, "TMP": true, "TMPDIR": true, "HOME": true, "USERPROFILE": true,
		"PROGRAMDATA": true, "PROGRAMFILES": true, "PROGRAMFILES(X86)": true,
		"LANG": true, "LC_ALL": true,
	}
	result := []string{}
	for _, item := range os.Environ() {
		key, _, found := strings.Cut(item, "=")
		if found && allowed[strings.ToUpper(key)] {
			result = append(result, item)
		}
	}
	return result
}
