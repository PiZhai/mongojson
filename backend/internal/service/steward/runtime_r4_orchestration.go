package steward

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	OrchestrationDraft              = "draft"
	OrchestrationQueued             = "queued"
	OrchestrationRunning            = "running"
	OrchestrationCompensating       = "compensating"
	OrchestrationSucceeded          = "succeeded"
	OrchestrationFailed             = "failed"
	OrchestrationCompensated        = "compensated"
	OrchestrationCompensationFailed = "compensation_failed"
	OrchestrationCancelled          = "cancelled"
	OrchestrationBlocked            = "blocked"

	OrchestrationNodePending    = "pending"
	OrchestrationNodeDispatched = "dispatched"
	OrchestrationNodeRunning    = "running"
	OrchestrationNodeSucceeded  = "succeeded"
	OrchestrationNodeFailed     = "failed"
	OrchestrationNodeCancelled  = "cancelled"
	OrchestrationNodeBlocked    = "blocked"
)

var (
	ErrOrchestrationDisabled          = errors.New("steward R4.0 orchestration is disabled")
	ErrOrchestrationNotFound          = errors.New("orchestration not found")
	ErrOrchestrationAgentNotFound     = errors.New("orchestration agent not found")
	ErrOrchestrationInvalid           = errors.New("invalid orchestration")
	ErrOrchestrationConflict          = errors.New("orchestration idempotency conflict")
	ErrOrchestrationInvalidTransition = errors.New("invalid orchestration state transition")
)

var remoteCredentialIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{1,79}$`)

type UpsertOrchestrationAgentInput struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	Description       string   `json:"description"`
	PermissionCeiling string   `json:"permission_ceiling"`
	DataLevelCeiling  string   `json:"data_level_ceiling"`
	ToolAllowlist     []string `json:"tool_allowlist"`
	MaxConcurrency    int      `json:"max_concurrency"`
	MaxRuntimeSeconds int      `json:"max_runtime_seconds"`
	MaxAttempts       int      `json:"max_attempts"`
	MaxEvidenceBytes  int      `json:"max_evidence_bytes"`
	Enabled           *bool    `json:"enabled"`
}

type CreateOrchestrationInput struct {
	Goal              string                         `json:"goal"`
	IdempotencyKey    string                         `json:"idempotency_key"`
	RequestedBy       string                         `json:"requested_by"`
	PermissionCeiling string                         `json:"permission_ceiling"`
	DataLevel         string                         `json:"data_level"`
	FailurePolicy     string                         `json:"failure_policy"`
	MaxParallel       int                            `json:"max_parallel"`
	MaxChildren       int                            `json:"max_children"`
	AutoStart         bool                           `json:"auto_start"`
	DeadlineAt        *time.Time                     `json:"deadline_at"`
	Nodes             []CreateOrchestrationNodeInput `json:"nodes"`
}

type CreateOrchestrationNodeInput struct {
	Key               string                    `json:"key"`
	AgentID           string                    `json:"agent_id"`
	Goal              string                    `json:"goal"`
	TargetDevice      string                    `json:"target_device"`
	DependsOn         []string                  `json:"depends_on"`
	PermissionCeiling string                    `json:"permission_ceiling"`
	DataLevel         string                    `json:"data_level"`
	Steps             []CreateAgentRunStepInput `json:"steps"`
	CompensationSteps []CreateAgentRunStepInput `json:"compensation_steps"`
	CredentialRefs    []string                  `json:"credential_refs,omitempty"`
}

func deriveOrchestrationKeys(seed []byte) ([]byte, []byte) {
	if len(seed) != ed25519.SeedSize {
		return nil, nil
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return append([]byte(nil), privateKey...), append([]byte(nil), publicKey...)
}

func OrchestrationVerifyKeyFromSeed(seed []byte) []byte {
	_, publicKey := deriveOrchestrationKeys(seed)
	return publicKey
}

func orchestrationKeysFromEnv() ([]byte, []byte, error) {
	if raw := strings.TrimSpace(os.Getenv("STEWARD_ORCHESTRATION_SIGNING_KEY")); raw != "" {
		seed, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, nil, fmt.Errorf("STEWARD_ORCHESTRATION_SIGNING_KEY must be base64 encoding of a 32-byte Ed25519 seed")
		}
		privateKey, publicKey := deriveOrchestrationKeys(seed)
		return privateKey, publicKey, nil
	}
	raw := strings.TrimSpace(os.Getenv("STEWARD_ORCHESTRATION_VERIFY_KEY"))
	publicKey, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return nil, nil, fmt.Errorf("STEWARD_ORCHESTRATION_SIGNING_KEY or a 32-byte STEWARD_ORCHESTRATION_VERIFY_KEY is required")
	}
	return nil, publicKey, nil
}

func (s *Service) orchestrationEnabled() error {
	if s == nil || !s.orchestrationR4 {
		return ErrOrchestrationDisabled
	}
	if err := s.runtimeEnabled(); err != nil {
		return err
	}
	if s.orchestrationKeyError != nil {
		return fmt.Errorf("%w: %v", ErrOrchestrationDisabled, s.orchestrationKeyError)
	}
	if len(s.orchestrationVerifyKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: orchestration verification key is unavailable", ErrOrchestrationDisabled)
	}
	if s.orchestrationDelegationTTL < 30*time.Second || s.orchestrationDelegationTTL > time.Hour {
		return fmt.Errorf("%w: delegation TTL must be between 30 seconds and 1 hour", ErrOrchestrationDisabled)
	}
	if s.orchestrationMessageLease < time.Second || s.orchestrationMessageLease > 5*time.Minute {
		return fmt.Errorf("%w: Agent message lease must be between 1 second and 5 minutes", ErrOrchestrationDisabled)
	}
	return nil
}

func (s *Service) UpsertOrchestrationAgent(ctx context.Context, input UpsertOrchestrationAgentInput) (domain.StewardOrchestrationAgent, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestrationAgent{}, err
	}
	input.ID = strings.ToLower(strings.TrimSpace(input.ID))
	input.Name = strings.TrimSpace(input.Name)
	input.Role = strings.TrimSpace(input.Role)
	input.Description = strings.TrimSpace(input.Description)
	if !runtimeStepKeyPattern.MatchString(input.ID) || input.Name == "" || input.Role == "" {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: id, name and role are required; id must be a stable runtime key", ErrOrchestrationInvalid)
	}
	if len([]rune(input.Name)) > 120 || len([]rune(input.Role)) > 120 || len([]rune(input.Description)) > 1000 {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: agent metadata exceeds its size limit", ErrOrchestrationInvalid)
	}
	input.PermissionCeiling = strings.ToUpper(defaultString(strings.TrimSpace(input.PermissionCeiling), PermissionA0))
	input.DataLevelCeiling = strings.ToUpper(defaultString(strings.TrimSpace(input.DataLevelCeiling), DataD0))
	if !validRuntimePermission(input.PermissionCeiling) || !validRuntimeDataLevel(input.DataLevelCeiling) {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: invalid agent permission or data ceiling", ErrOrchestrationInvalid)
	}
	if input.MaxConcurrency == 0 {
		input.MaxConcurrency = 1
	}
	if input.MaxConcurrency < 1 || input.MaxConcurrency > 16 {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: max_concurrency must be between 1 and 16", ErrOrchestrationInvalid)
	}
	if input.MaxRuntimeSeconds == 0 {
		input.MaxRuntimeSeconds = 900
	}
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 20
	}
	if input.MaxEvidenceBytes == 0 {
		input.MaxEvidenceBytes = 256 << 10
	}
	if input.MaxRuntimeSeconds < 30 || input.MaxRuntimeSeconds > 3600 || input.MaxAttempts < 1 || input.MaxAttempts > 100 || input.MaxEvidenceBytes < 1024 || input.MaxEvidenceBytes > 1<<20 {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: invalid Agent resource quota", ErrOrchestrationInvalid)
	}
	allowlist := make([]string, 0, len(input.ToolAllowlist))
	seen := map[string]bool{}
	for _, name := range input.ToolAllowlist {
		name = strings.TrimSpace(name)
		tool, ok := s.runtimeTools.get(name)
		if name == "" || !ok {
			return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: unknown allowlisted runtime tool %q", ErrOrchestrationInvalid, name)
		}
		if !ownerModeEnabled() && permissionRank(tool.Spec().PermissionLevel) > permissionRank(input.PermissionCeiling) {
			return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: tool %q exceeds agent permission ceiling", ErrOrchestrationInvalid, name)
		}
		if !seen[name] {
			seen[name] = true
			allowlist = append(allowlist, name)
		}
	}
	if len(allowlist) == 0 || len(allowlist) > 64 {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("%w: tool_allowlist must contain between 1 and 64 tools", ErrOrchestrationInvalid)
	}
	sort.Strings(allowlist)
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	payload, _ := json.Marshal(allowlist)
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_orchestration_agents (
			id, name, role, description, permission_ceiling, data_level_ceiling,
			tool_allowlist, max_concurrency, max_runtime_seconds, max_attempts,
			max_evidence_bytes, enabled, created_at, updated_at
		) values ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,$13)
		on conflict (id) do update set
			name=excluded.name, role=excluded.role, description=excluded.description,
			permission_ceiling=excluded.permission_ceiling, data_level_ceiling=excluded.data_level_ceiling,
			tool_allowlist=excluded.tool_allowlist, max_concurrency=excluded.max_concurrency,
			max_runtime_seconds=excluded.max_runtime_seconds, max_attempts=excluded.max_attempts,
			max_evidence_bytes=excluded.max_evidence_bytes,
			enabled=excluded.enabled, updated_at=excluded.updated_at
	`, input.ID, input.Name, input.Role, input.Description, input.PermissionCeiling, input.DataLevelCeiling,
		string(payload), input.MaxConcurrency, input.MaxRuntimeSeconds, input.MaxAttempts, input.MaxEvidenceBytes, enabled, now)
	if err != nil {
		return domain.StewardOrchestrationAgent{}, fmt.Errorf("upsert orchestration agent: %w", err)
	}
	if !enabled {
		if _, err := s.db.Pool.Exec(ctx, `
			update steward_agent_runs run set cancel_requested=true, updated_at=$2
			from steward_orchestration_nodes node
			where node.runtime_run_id=run.id and node.agent_id=$1
			  and run.status in ('running','verifying','compensating')
		`, input.ID, now); err != nil {
			return domain.StewardOrchestrationAgent{}, fmt.Errorf("revoke active orchestration agent runs: %w", err)
		}
	}
	return s.getOrchestrationAgent(ctx, input.ID)
}

func (s *Service) ListOrchestrationAgents(ctx context.Context) ([]domain.StewardOrchestrationAgent, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, name, role, description, permission_ceiling, data_level_ceiling,
		       tool_allowlist, max_concurrency, max_runtime_seconds, max_attempts,
		       max_evidence_bytes, enabled, created_at, updated_at
		from steward_orchestration_agents order by id
	`)
	if err != nil {
		return nil, fmt.Errorf("list orchestration agents: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardOrchestrationAgent{}
	for rows.Next() {
		item, err := scanOrchestrationAgent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getOrchestrationAgent(ctx context.Context, id string) (domain.StewardOrchestrationAgent, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, name, role, description, permission_ceiling, data_level_ceiling,
		       tool_allowlist, max_concurrency, max_runtime_seconds, max_attempts,
		       max_evidence_bytes, enabled, created_at, updated_at
		from steward_orchestration_agents where id=$1
	`, id)
	item, err := scanOrchestrationAgent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardOrchestrationAgent{}, ErrOrchestrationAgentNotFound
	}
	return item, err
}

type orchestrationAgentScanner interface{ Scan(...any) error }

func scanOrchestrationAgent(row orchestrationAgentScanner) (domain.StewardOrchestrationAgent, error) {
	var item domain.StewardOrchestrationAgent
	var allowlist []byte
	if err := row.Scan(&item.ID, &item.Name, &item.Role, &item.Description, &item.PermissionCeiling,
		&item.DataLevelCeiling, &allowlist, &item.MaxConcurrency, &item.MaxRuntimeSeconds,
		&item.MaxAttempts, &item.MaxEvidenceBytes, &item.Enabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return item, err
	}
	_ = json.Unmarshal(allowlist, &item.ToolAllowlist)
	if item.ToolAllowlist == nil {
		item.ToolAllowlist = []string{}
	}
	return item, nil
}

func (s *Service) normalizeOrchestrationInput(ctx context.Context, input CreateOrchestrationInput) (CreateOrchestrationInput, string, error) {
	input.Goal = strings.TrimSpace(input.Goal)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestedBy = defaultString(strings.TrimSpace(input.RequestedBy), "local-user")
	input.PermissionCeiling = strings.ToUpper(defaultString(strings.TrimSpace(input.PermissionCeiling), PermissionA0))
	input.DataLevel = strings.ToUpper(defaultString(strings.TrimSpace(input.DataLevel), DataD0))
	input.FailurePolicy = strings.ToLower(defaultString(strings.TrimSpace(input.FailurePolicy), "fail_fast"))
	// "continue" was the name used by early Agent-loop clients. Store one
	// canonical value so plan hashes and recovery behavior remain stable.
	if input.FailurePolicy == "continue" {
		input.FailurePolicy = "collect_all"
	}
	if input.Goal == "" || len([]rune(input.Goal)) > 2000 || len(input.IdempotencyKey) > 200 {
		return input, "", fmt.Errorf("%w: goal is required and idempotency metadata exceeds its limit", ErrOrchestrationInvalid)
	}
	if !validRuntimePermission(input.PermissionCeiling) || !validRuntimeDataLevel(input.DataLevel) ||
		(input.FailurePolicy != "fail_fast" && input.FailurePolicy != "compensate" && input.FailurePolicy != "collect_all") {
		return input, "", fmt.Errorf("%w: invalid permission, data level or failure policy", ErrOrchestrationInvalid)
	}
	if input.MaxParallel == 0 {
		input.MaxParallel = 2
	}
	if input.MaxChildren == 0 {
		input.MaxChildren = 16
	}
	if input.MaxParallel < 1 || input.MaxParallel > 16 || input.MaxChildren < 1 || input.MaxChildren > 100 {
		return input, "", fmt.Errorf("%w: max_parallel must be 1-16 and max_children must be 1-100", ErrOrchestrationInvalid)
	}
	if len(input.Nodes) < 1 || len(input.Nodes) > input.MaxChildren {
		return input, "", fmt.Errorf("%w: nodes must contain 1-max_children entries", ErrOrchestrationInvalid)
	}
	now := time.Now().UTC()
	if input.DeadlineAt != nil {
		deadline := input.DeadlineAt.UTC()
		if !deadline.After(now) || deadline.After(now.Add(7*24*time.Hour)) {
			return input, "", fmt.Errorf("%w: deadline_at must be within the next seven days", ErrOrchestrationInvalid)
		}
		input.DeadlineAt = &deadline
	}
	agents, err := s.ListOrchestrationAgents(ctx)
	if err != nil {
		return input, "", err
	}
	agentByID := make(map[string]domain.StewardOrchestrationAgent, len(agents))
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}
	seenNodes := map[string]bool{}
	totalSteps := 0
	compensationNodeCount := 0
	for index := range input.Nodes {
		node := &input.Nodes[index]
		node.Key = strings.TrimSpace(node.Key)
		node.AgentID = strings.ToLower(strings.TrimSpace(node.AgentID))
		node.Goal = defaultString(strings.TrimSpace(node.Goal), node.Key)
		node.TargetDevice = defaultString(strings.TrimSpace(node.TargetDevice), "local")
		if strings.EqualFold(node.TargetDevice, "local") || strings.EqualFold(node.TargetDevice, "auto") {
			node.TargetDevice = strings.ToLower(node.TargetDevice)
		}
		node.PermissionCeiling = strings.ToUpper(defaultString(strings.TrimSpace(node.PermissionCeiling), input.PermissionCeiling))
		node.DataLevel = strings.ToUpper(defaultString(strings.TrimSpace(node.DataLevel), input.DataLevel))
		credentialSeen := map[string]bool{}
		normalizedCredentials := make([]string, 0, len(node.CredentialRefs))
		for _, credentialID := range node.CredentialRefs {
			credentialID = strings.ToLower(strings.TrimSpace(credentialID))
			if !remoteCredentialIDPattern.MatchString(credentialID) || credentialSeen[credentialID] {
				return input, "", fmt.Errorf("%w: node %q has an invalid or duplicate credential reference", ErrOrchestrationInvalid, node.Key)
			}
			credentialSeen[credentialID] = true
			normalizedCredentials = append(normalizedCredentials, credentialID)
		}
		sort.Strings(normalizedCredentials)
		node.CredentialRefs = normalizedCredentials
		if !runtimeStepKeyPattern.MatchString(node.Key) || strings.HasPrefix(node.Key, "compensate-") || seenNodes[node.Key] || len([]rune(node.Goal)) > 2000 {
			return input, "", fmt.Errorf("%w: invalid or duplicate node key %q", ErrOrchestrationInvalid, node.Key)
		}
		agent, ok := agentByID[node.AgentID]
		if !ok || !agent.Enabled {
			return input, "", fmt.Errorf("%w: node %q references an unavailable agent", ErrOrchestrationInvalid, node.Key)
		}
		if !validRuntimePermission(node.PermissionCeiling) || (!ownerModeEnabled() && (permissionRank(node.PermissionCeiling) > permissionRank(input.PermissionCeiling) || permissionRank(node.PermissionCeiling) > permissionRank(agent.PermissionCeiling))) {
			return input, "", fmt.Errorf("%w: node %q exceeds its permission delegation ceiling", ErrOrchestrationInvalid, node.Key)
		}
		if !validRuntimeDataLevel(node.DataLevel) || (!ownerModeEnabled() && (dataLevelRank(node.DataLevel) > dataLevelRank(input.DataLevel) || dataLevelRank(node.DataLevel) > dataLevelRank(agent.DataLevelCeiling))) {
			return input, "", fmt.Errorf("%w: node %q exceeds its data delegation ceiling", ErrOrchestrationInvalid, node.Key)
		}
		for depIndex, dependency := range node.DependsOn {
			dependency = strings.TrimSpace(dependency)
			node.DependsOn[depIndex] = dependency
			if dependency == node.Key || !seenNodes[dependency] {
				return input, "", fmt.Errorf("%w: node %q dependency %q must reference an earlier node", ErrOrchestrationInvalid, node.Key, dependency)
			}
		}
		if node.TargetDevice != "local" {
			if !s.orchestrationRemote {
				return input, "", fmt.Errorf("%w: node %q requires disabled remote execution", ErrOrchestrationInvalid, node.Key)
			}
			if node.TargetDevice != "auto" {
				if err := s.validateRemoteExecutionDevice(ctx, node.TargetDevice, node.PermissionCeiling, remoteStepsRequireBroker(node.Steps)); err != nil {
					return input, "", fmt.Errorf("%w: node %q target device: %v", ErrOrchestrationInvalid, node.Key, err)
				}
			}
			if !ownerModeEnabled() && permissionRank(node.PermissionCeiling) > permissionRank(PermissionA7) {
				return input, "", fmt.Errorf("%w: remote node %q exceeds the R4.4 A7 ceiling", ErrOrchestrationInvalid, node.Key)
			}
			if !ownerModeEnabled() && permissionRank(node.PermissionCeiling) <= permissionRank(PermissionA2) && dataLevelRank(node.DataLevel) > dataLevelRank(DataD2) {
				return input, "", fmt.Errorf("%w: low-permission remote node %q exceeds the R4.3 D2 ceiling", ErrOrchestrationInvalid, node.Key)
			}
			if remoteStepsRequireBroker(node.Steps) {
				if !s.runtimeR3 || len(node.Steps) != 1 || node.Steps[0].ToolName != "privilege.execute" || len(node.CompensationSteps) != 0 {
					return input, "", fmt.Errorf("%w: R4.4 remote privilege node %q must contain exactly one privilege.execute step and no compensation", ErrOrchestrationInvalid, node.Key)
				}
				capability, _ := node.Steps[0].Arguments["capability"].(string)
				if !configuredToolActionPattern.MatchString(strings.ToLower(strings.TrimSpace(capability))) {
					return input, "", fmt.Errorf("%w: R4.4 node %q has an invalid fixed Broker capability", ErrOrchestrationInvalid, node.Key)
				}
			}
		}
		child, _, err := s.normalizeAgentRunInput(CreateAgentRunInput{
			Goal: node.Goal, Mode: "manual", RequestedBy: "orchestrator:" + node.AgentID,
			TargetDevice: node.TargetDevice, DataLevel: node.DataLevel, PermissionCeiling: node.PermissionCeiling,
			Planner: "r4-orchestrator", PlannerVersion: "4.0.0", Steps: node.Steps,
		})
		if err != nil {
			return input, "", fmt.Errorf("%w: node %q plan: %v", ErrOrchestrationInvalid, node.Key, err)
		}
		node.Steps = child.Steps
		if len(node.CompensationSteps) > 0 {
			compensationNodeCount++
			compensation, _, err := s.normalizeAgentRunInput(CreateAgentRunInput{
				Goal: "compensate " + node.Goal, Mode: "manual", RequestedBy: "orchestrator:" + node.AgentID,
				TargetDevice: node.TargetDevice, DataLevel: node.DataLevel, PermissionCeiling: node.PermissionCeiling,
				Planner: "r4-saga", PlannerVersion: "4.2.0", Steps: node.CompensationSteps,
			})
			if err != nil {
				return input, "", fmt.Errorf("%w: node %q compensation plan: %v", ErrOrchestrationInvalid, node.Key, err)
			}
			node.CompensationSteps = compensation.Steps
		}
		runtimeBudget := 0
		attemptBudget := 0
		allSteps := append(append([]CreateAgentRunStepInput{}, node.Steps...), node.CompensationSteps...)
		for _, step := range allSteps {
			runtimeBudget += step.TimeoutSeconds * step.MaxAttempts
			attemptBudget += step.MaxAttempts
		}
		if quotaErr := orchestrationNodeQuotaError(node.Key, runtimeBudget, attemptBudget, agent.MaxRuntimeSeconds, agent.MaxAttempts); quotaErr != nil {
			return input, "", quotaErr
		}
		allowed := make(map[string]bool, len(agent.ToolAllowlist))
		for _, name := range agent.ToolAllowlist {
			allowed[name] = true
		}
		for _, step := range allSteps {
			if !allowed[step.ToolName] {
				return input, "", fmt.Errorf("%w: agent %q is not delegated tool %q", ErrOrchestrationInvalid, agent.ID, step.ToolName)
			}
			if node.TargetDevice != "local" && !remoteStepsRequireBroker(node.Steps) && !ownerModeEnabled() && !s.remoteExecutionToolAllowed(step.ToolName) {
				return input, "", fmt.Errorf("%w: remote tool %q is outside the R4.3 allowlist", ErrOrchestrationInvalid, step.ToolName)
			}
		}
		totalSteps += len(allSteps)
		if totalSteps > 500 {
			return input, "", fmt.Errorf("%w: orchestration exceeds 500 total runtime steps", ErrOrchestrationInvalid)
		}
		seenNodes[node.Key] = true
	}
	if len(input.Nodes)+compensationNodeCount > input.MaxChildren {
		return input, "", fmt.Errorf("%w: forward and potential compensation nodes exceed max_children", ErrOrchestrationInvalid)
	}
	if input.FailurePolicy != "compensate" && compensationNodeCount > 0 {
		return input, "", fmt.Errorf("%w: compensation_steps require failure_policy=compensate", ErrOrchestrationInvalid)
	}
	canonical := struct {
		Goal              string                         `json:"goal"`
		PermissionCeiling string                         `json:"permission_ceiling"`
		DataLevel         string                         `json:"data_level"`
		FailurePolicy     string                         `json:"failure_policy"`
		MaxParallel       int                            `json:"max_parallel"`
		MaxChildren       int                            `json:"max_children"`
		DeadlineAt        *time.Time                     `json:"deadline_at"`
		Nodes             []CreateOrchestrationNodeInput `json:"nodes"`
	}{input.Goal, input.PermissionCeiling, input.DataLevel, input.FailurePolicy, input.MaxParallel, input.MaxChildren, input.DeadlineAt, input.Nodes}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return input, "", fmt.Errorf("%w: encode plan: %v", ErrOrchestrationInvalid, err)
	}
	digest := sha256.Sum256(payload)
	return input, hex.EncodeToString(digest[:]), nil
}

func orchestrationNodeQuotaError(nodeKey string, runtimeBudget, attemptBudget, maxRuntimeSeconds, maxAttempts int) error {
	runtimeExceeded := runtimeBudget > maxRuntimeSeconds
	attemptsExceeded := attemptBudget > maxAttempts
	switch {
	case runtimeExceeded && attemptsExceeded:
		return fmt.Errorf("%w: node %q requests runtime budget %ds and %d attempts; Agent quotas are %ds and %d attempts",
			ErrOrchestrationInvalid, nodeKey, runtimeBudget, attemptBudget, maxRuntimeSeconds, maxAttempts)
	case runtimeExceeded:
		return fmt.Errorf("%w: node %q requests runtime budget %ds (sum of timeout_seconds x max_attempts), exceeding the Agent quota of %ds",
			ErrOrchestrationInvalid, nodeKey, runtimeBudget, maxRuntimeSeconds)
	case attemptsExceeded:
		return fmt.Errorf("%w: node %q requests %d attempts, exceeding the Agent quota of %d attempts",
			ErrOrchestrationInvalid, nodeKey, attemptBudget, maxAttempts)
	default:
		return nil
	}
}

func (s *Service) CreateOrchestration(ctx context.Context, input CreateOrchestrationInput) (domain.StewardOrchestration, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestration{}, err
	}
	normalized, planHash, err := s.normalizeOrchestrationInput(ctx, input)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if normalized.IdempotencyKey != "" {
		var id, existingHash string
		err := s.db.Pool.QueryRow(ctx, `select id::text, plan_hash from steward_orchestrations where idempotency_key=$1`, normalized.IdempotencyKey).Scan(&id, &existingHash)
		if err == nil {
			if existingHash != planHash {
				return domain.StewardOrchestration{}, ErrOrchestrationConflict
			}
			return s.GetOrchestration(ctx, id)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardOrchestration{}, err
		}
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	status := OrchestrationDraft
	if normalized.AutoStart {
		status = OrchestrationQueued
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		insert into steward_orchestrations (
			id, goal, status, plan_hash, idempotency_key, requested_by, permission_ceiling,
			data_level, failure_policy, max_parallel, max_children, control_generation,
			created_at, updated_at, deadline_at
		) values ($1,$2,$3,$4,nullif($5,''),$6,$7,$8,$9,$10,$11,$12,$13,$13,$14)
	`, id, normalized.Goal, status, planHash, normalized.IdempotencyKey, normalized.RequestedBy,
		normalized.PermissionCeiling, normalized.DataLevel, normalized.FailurePolicy,
		normalized.MaxParallel, normalized.MaxChildren, control.Generation, now, normalized.DeadlineAt)
	if err != nil {
		return domain.StewardOrchestration{}, fmt.Errorf("insert orchestration: %w", err)
	}
	for index, node := range normalized.Nodes {
		dependsOn, _ := json.Marshal(node.DependsOn)
		steps, _ := json.Marshal(node.Steps)
		compensationSteps, _ := json.Marshal(node.CompensationSteps)
		credentialRefs, _ := json.Marshal(node.CredentialRefs)
		remoteCapability := ""
		if node.TargetDevice != "local" && remoteStepsRequireBroker(node.Steps) {
			remoteCapability, _ = node.Steps[0].Arguments["capability"].(string)
			remoteCapability = strings.ToLower(strings.TrimSpace(remoteCapability))
		}
		if _, err := tx.Exec(ctx, `
			insert into steward_orchestration_nodes (
				id, orchestration_id, node_key, position, agent_id, goal, status, depends_on,
				permission_ceiling, data_level, steps, compensation_steps, kind, target_device,
				remote_privilege_capability, remote_credential_refs, created_at, updated_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11::jsonb,$12::jsonb,'forward',$13,$14,$15::jsonb,$16,$16)
		`, uuid.NewString(), id, node.Key, index+1, node.AgentID, node.Goal, OrchestrationNodePending,
			string(dependsOn), node.PermissionCeiling, node.DataLevel, string(steps), string(compensationSteps), node.TargetDevice,
			remoteCapability, string(credentialRefs), now); err != nil {
			return domain.StewardOrchestration{}, fmt.Errorf("insert orchestration node %s: %w", node.Key, err)
		}
	}
	if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.created", status,
		"immutable multi-agent DAG persisted", map[string]any{"plan_hash": planHash, "node_count": len(normalized.Nodes)}); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardOrchestration{}, err
	}
	return s.GetOrchestration(ctx, id)
}

func appendOrchestrationEvent(ctx context.Context, tx pgx.Tx, orchestrationID, nodeID, eventType, status, message string, payload map[string]any) error {
	encoded, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `
		insert into steward_orchestration_events (
			id, orchestration_id, node_id, type, status, message, payload, created_at
		) values ($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7::jsonb,$8)
	`, uuid.NewString(), orchestrationID, nodeID, eventType, status, message, string(encoded), time.Now().UTC())
	return err
}

func (s *Service) ListOrchestrations(ctx context.Context, status string, limit int) ([]domain.StewardOrchestration, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return nil, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && !orchestrationTerminal(status) && status != OrchestrationDraft && status != OrchestrationQueued && status != OrchestrationRunning {
		return nil, fmt.Errorf("%w: invalid orchestration status", ErrOrchestrationInvalid)
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text from steward_orchestrations
		where ($1='' or status=$1) order by updated_at desc limit $2
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	items := make([]domain.StewardOrchestration, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetOrchestration(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetOrchestration(ctx context.Context, id string) (domain.StewardOrchestration, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return domain.StewardOrchestration{}, ErrOrchestrationNotFound
	}
	var item domain.StewardOrchestration
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, goal, status, plan_hash, coalesce(idempotency_key,''), requested_by,
		       permission_ceiling, data_level, failure_policy, max_parallel, max_children,
		       control_generation, failure_summary, created_at, updated_at, started_at,
		       completed_at, deadline_at
		from steward_orchestrations where id=$1
	`, id).Scan(&item.ID, &item.Goal, &item.Status, &item.PlanHash, &item.IdempotencyKey,
		&item.RequestedBy, &item.PermissionCeiling, &item.DataLevel, &item.FailurePolicy,
		&item.MaxParallel, &item.MaxChildren, &item.ControlGeneration, &item.FailureSummary,
		&item.CreatedAt, &item.UpdatedAt, &item.StartedAt, &item.CompletedAt, &item.DeadlineAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardOrchestration{}, ErrOrchestrationNotFound
	}
	if err != nil {
		return item, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select node.id::text, node.node_key, node.position, node.agent_id, node.goal, node.status,
		       node.kind, coalesce(node.compensation_of_id::text,''),
		       node.target_device, coalesce(node.selected_device_id,''),
		       node.depends_on, node.permission_ceiling, node.data_level, node.steps,
		       node.compensation_steps,
		       node.remote_privilege_capability, node.remote_credential_refs,
		       node.remote_privilege_plan_hash, node.remote_broker_delegation_id,
		       node.remote_broker_delegation_expires_at,
		       coalesce(node.runtime_run_id::text,''), node.failure_summary,
		       node.created_at, node.updated_at, node.started_at, node.completed_at,
		       coalesce(claim.id::text,''), coalesce(claim.plan_hash,''),
		       coalesce(claim.control_generation,0), claim.expires_at, coalesce(claim.signature,'')
		from steward_orchestration_nodes node
		left join steward_delegation_claims claim on claim.id=node.delegation_id
		where node.orchestration_id=$1 order by node.position
	`, id)
	if err != nil {
		return item, err
	}
	for rows.Next() {
		var node domain.StewardOrchestrationNode
		var dependsOn, steps, compensationSteps, remoteCredentialRefs []byte
		var claimExpires *time.Time
		var remoteCapability, remotePrivilegePlanHash, remoteDelegationID string
		var remoteDelegationExpires *time.Time
		if err := rows.Scan(&node.ID, &node.Key, &node.Position, &node.AgentID, &node.Goal, &node.Status,
			&node.Kind, &node.CompensationOfID, &node.TargetDevice, &node.SelectedDeviceID,
			&dependsOn, &node.PermissionCeiling, &node.DataLevel, &steps,
			&compensationSteps, &remoteCapability, &remoteCredentialRefs,
			&remotePrivilegePlanHash, &remoteDelegationID, &remoteDelegationExpires, &node.RuntimeRunID,
			&node.FailureSummary, &node.CreatedAt, &node.UpdatedAt, &node.StartedAt, &node.CompletedAt,
			&node.Delegation.ID, &node.Delegation.PlanHash, &node.Delegation.ControlGeneration,
			&claimExpires, &node.Delegation.Signature); err != nil {
			rows.Close()
			return item, err
		}
		node.OrchestrationID = id
		_ = json.Unmarshal(dependsOn, &node.DependsOn)
		var runSteps []CreateAgentRunStepInput
		_ = json.Unmarshal(steps, &runSteps)
		node.Steps = orchestrationDomainSteps(runSteps)
		var compensationRunSteps []CreateAgentRunStepInput
		_ = json.Unmarshal(compensationSteps, &compensationRunSteps)
		node.CompensationSteps = orchestrationDomainSteps(compensationRunSteps)
		if remoteCapability != "" {
			credentialRefs := []string{}
			_ = json.Unmarshal(remoteCredentialRefs, &credentialRefs)
			remoteStatus := "awaiting_approval"
			if remoteDelegationID != "" {
				remoteStatus = "delegated"
			}
			node.RemotePrivilege = &domain.StewardRemotePrivilege{Required: true, Status: remoteStatus,
				Capability: remoteCapability, CredentialRefs: credentialRefs,
				Subject: "remote-broker:" + id + ":" + node.ID, PlanHash: remotePrivilegePlanHash,
				DelegationID: remoteDelegationID, ExpiresAt: remoteDelegationExpires}
			if node.SelectedDeviceID != "" {
				if device, deviceErr := s.getDevice(ctx, node.SelectedDeviceID); deviceErr == nil {
					node.RemotePrivilege.TargetBrokerKeyID = device.BrokerKeyID
				}
			}
		}
		if node.DependsOn == nil {
			node.DependsOn = []string{}
		}
		if node.Delegation.ID != "" {
			node.Delegation.AgentID = node.AgentID
			node.Delegation.NodeID = node.ID
			node.Delegation.PermissionCeiling = node.PermissionCeiling
			node.Delegation.DataLevel = node.DataLevel
			if claimExpires != nil {
				node.Delegation.ExpiresAt = *claimExpires
			}
		}
		node.RemoteDispatch, err = s.getRemoteDispatchForNode(ctx, node.ID)
		if err != nil {
			rows.Close()
			return item, err
		}
		if node.RemotePrivilege != nil && node.RemoteDispatch != nil {
			node.RemotePrivilege.Status = node.RemoteDispatch.Status
		}
		item.Nodes = append(item.Nodes, node)
	}
	rows.Close()
	if item.Nodes == nil {
		item.Nodes = []domain.StewardOrchestrationNode{}
	}
	item.Evidence, err = s.orchestrationEvidenceSummary(ctx, id)
	if err != nil {
		return item, err
	}
	eventRows, err := s.db.Pool.Query(ctx, `
		select sequence, id::text, coalesce(node_id::text,''), type, status, message, payload, created_at
		from steward_orchestration_events where orchestration_id=$1 order by sequence limit 500
	`, id)
	if err != nil {
		return item, err
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var event domain.StewardOrchestrationEvent
		var payload []byte
		if err := eventRows.Scan(&event.Sequence, &event.ID, &event.NodeID, &event.Type, &event.Status,
			&event.Message, &payload, &event.CreatedAt); err != nil {
			return item, err
		}
		event.OrchestrationID = id
		event.Payload = decodeRuntimeMap(payload)
		item.Events = append(item.Events, event)
	}
	if item.Events == nil {
		item.Events = []domain.StewardOrchestrationEvent{}
	}
	item.Messages, err = s.listOrchestrationMessages(ctx, id)
	if err != nil {
		return item, err
	}
	item.Workers, err = s.listOrchestrationWorkers(ctx, id)
	if err != nil {
		return item, err
	}
	return item, eventRows.Err()
}

func orchestrationDomainSteps(steps []CreateAgentRunStepInput) []domain.StewardOrchestrationStep {
	out := make([]domain.StewardOrchestrationStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, domain.StewardOrchestrationStep{
			Key: step.Key, Title: step.Title, ToolName: step.ToolName, ToolVersion: step.ToolVersion,
			Arguments: step.Arguments, ExpectedOutput: step.ExpectedOutput, DependsOn: step.DependsOn,
			MaxAttempts: step.MaxAttempts, TimeoutSeconds: step.TimeoutSeconds, RequiresApproval: step.RequiresApproval,
		})
	}
	return out
}

func (s *Service) orchestrationEvidenceSummary(ctx context.Context, id string) (domain.StewardOrchestrationEvidenceSummary, error) {
	var summary domain.StewardOrchestrationEvidenceSummary
	rows, err := s.db.Pool.Query(ctx, `
		select evidence.id::text, evidence.sha256, evidence.data_level, evidence.redacted
		from steward_orchestration_nodes node
		join steward_evidence_artifacts evidence on evidence.run_id=node.runtime_run_id
		where node.orchestration_id=$1 order by evidence.id
	`, id)
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	manifest := sha256.New()
	levels := map[string]bool{}
	for rows.Next() {
		var evidenceID, digest, level string
		var redacted bool
		if err := rows.Scan(&evidenceID, &digest, &level, &redacted); err != nil {
			return summary, err
		}
		summary.ArtifactCount++
		if redacted {
			summary.RedactedCount++
		}
		levels[level] = true
		_, _ = manifest.Write([]byte(evidenceID + ":" + digest + "\n"))
	}
	remoteRows, err := s.db.Pool.Query(ctx, `
		select remote.id::text, remote.result_payload
		from steward_remote_dispatches remote
		where remote.orchestration_id=$1 and remote.status in ('succeeded','failed','cancelled','blocked')
		order by remote.id
	`, id)
	if err != nil {
		return summary, err
	}
	for remoteRows.Next() {
		var dispatchID string
		var encoded []byte
		if err := remoteRows.Scan(&dispatchID, &encoded); err != nil {
			remoteRows.Close()
			return summary, err
		}
		result := decodeRuntimeMap(encoded)
		artifactCount := intFromAny(result["artifact_count"])
		redactedCount := intFromAny(result["redacted_count"])
		summary.ArtifactCount += artifactCount
		summary.RedactedCount += redactedCount
		if values, ok := result["data_levels"].([]any); ok {
			for _, value := range values {
				if level, ok := value.(string); ok {
					levels[level] = true
				}
			}
		}
		if digest, _ := result["manifest_sha256"].(string); digest != "" {
			_, _ = manifest.Write([]byte("remote:" + dispatchID + ":" + digest + "\n"))
		}
	}
	remoteRows.Close()
	if err := s.db.Pool.QueryRow(ctx, `
		select count(runtime_run_id)::int + count(remote.id)::int
		from steward_orchestration_nodes node
		left join steward_remote_dispatches remote on remote.node_id=node.id
		where node.orchestration_id=$1
	`, id).Scan(&summary.ChildRunCount); err != nil {
		return summary, err
	}
	for level := range levels {
		summary.DataLevels = append(summary.DataLevels, level)
	}
	sort.Strings(summary.DataLevels)
	if summary.DataLevels == nil {
		summary.DataLevels = []string{}
	}
	if summary.ArtifactCount > 0 {
		summary.ManifestSHA256 = hex.EncodeToString(manifest.Sum(nil))
	}
	return summary, rows.Err()
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func orchestrationTerminal(status string) bool {
	switch status {
	case OrchestrationSucceeded, OrchestrationFailed, OrchestrationCompensated,
		OrchestrationCompensationFailed, OrchestrationCancelled, OrchestrationBlocked:
		return true
	default:
		return false
	}
}

func orchestrationNodeTerminal(status string) bool {
	switch status {
	case OrchestrationNodeSucceeded, OrchestrationNodeFailed, OrchestrationNodeCancelled, OrchestrationNodeBlocked:
		return true
	default:
		return false
	}
}

func (s *Service) StartOrchestration(ctx context.Context, id string) (domain.StewardOrchestration, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestration{}, err
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `select status from steward_orchestrations where id=$1 for update`, id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardOrchestration{}, ErrOrchestrationNotFound
	} else if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if status == OrchestrationQueued || status == OrchestrationRunning {
		if err := tx.Commit(ctx); err != nil {
			return domain.StewardOrchestration{}, err
		}
		return s.GetOrchestration(ctx, id)
	}
	if status != OrchestrationDraft {
		return domain.StewardOrchestration{}, ErrOrchestrationInvalidTransition
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		update steward_orchestrations set status=$2, control_generation=$3,
		       started_at=coalesce(started_at,$4), updated_at=$4 where id=$1
	`, id, OrchestrationQueued, control.Generation, now)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.started", OrchestrationQueued,
		"orchestration entered the durable scheduler queue", map[string]any{"control_generation": control.Generation}); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardOrchestration{}, err
	}
	return s.GetOrchestration(ctx, id)
}

func (s *Service) CancelOrchestration(ctx context.Context, id string) (domain.StewardOrchestration, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return domain.StewardOrchestration{}, err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `select status from steward_orchestrations where id=$1 for update`, id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardOrchestration{}, ErrOrchestrationNotFound
	} else if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if status == OrchestrationCancelled {
		_ = tx.Commit(ctx)
		return s.GetOrchestration(ctx, id)
	}
	if orchestrationTerminal(status) {
		return domain.StewardOrchestration{}, ErrOrchestrationInvalidTransition
	}
	now := time.Now().UTC()
	if err := s.cancelOrchestrationChildrenTx(ctx, tx, id, now, "parent orchestration cancelled"); err != nil {
		return domain.StewardOrchestration{}, err
	}
	_, err = tx.Exec(ctx, `
		update steward_orchestrations set status=$2, failure_summary='cancelled by user',
		       updated_at=$3, completed_at=$3 where id=$1
	`, id, OrchestrationCancelled, now)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.cancelled", OrchestrationCancelled,
		"cancellation propagated to every non-terminal child run", map[string]any{}); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardOrchestration{}, err
	}
	return s.GetOrchestration(ctx, id)
}

// RunOrchestrationCycle reconciles child Runtime V2 runs and materializes ready
// DAG nodes. The scheduler never executes a tool itself.
func (s *Service) RunOrchestrationCycle(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.orchestrationR4 {
		return 0, nil
	}
	if err := s.orchestrationEnabled(); err != nil {
		return 0, err
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err != nil {
		return 0, err
	}
	if control.Stopped || control.Paused {
		return 0, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text from steward_orchestrations
		where status in ('queued','running','compensating') order by updated_at, created_at limit $1
	`, limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	processed := 0
	for _, id := range ids {
		claimed, err := s.processOrchestration(ctx, id, control.Generation)
		if err != nil {
			return processed, err
		}
		if claimed {
			processed++
		}
	}
	return processed, nil
}

type orchestrationScheduleNode struct {
	ID                        string
	Key                       string
	Position                  int
	AgentID                   string
	Goal                      string
	Kind                      string
	CompensationOfID          string
	TargetDevice              string
	SelectedDeviceID          string
	Status                    string
	DependsOn                 []string
	PermissionCeiling         string
	DataLevel                 string
	Steps                     []CreateAgentRunStepInput
	CompensationSteps         []CreateAgentRunStepInput
	RuntimeRunID              string
	RuntimeStatus             string
	FailureSummary            string
	RemotePrivilegeCapability string
	RemotePrivilegePlanHash   string
	RemoteBrokerDelegation    []byte
	RemoteBrokerDelegationID  string
	RemoteCredentialRefs      []string
}

func (s *Service) processOrchestration(ctx context.Context, id string, generation int64) (bool, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock(hashtextextended($1, 0))`, "steward:r4:"+id).Scan(&locked); err != nil || !locked {
		return false, err
	}
	var status, planHash, failurePolicy string
	var maxParallel int
	var storedGeneration int64
	var deadline *time.Time
	if err := tx.QueryRow(ctx, `
		select status, plan_hash, failure_policy, max_parallel, control_generation, deadline_at
		from steward_orchestrations where id=$1 for update
	`, id).Scan(&status, &planHash, &failurePolicy, &maxParallel, &storedGeneration, &deadline); errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if status != OrchestrationQueued && status != OrchestrationRunning && status != OrchestrationCompensating {
		return false, nil
	}
	now := time.Now().UTC()
	if s.orchestrationWorkers {
		if err := s.recoverUnavailableOrchestrationWorkersTx(ctx, tx, id, now); err != nil {
			return false, err
		}
	}
	if deadline != nil && !deadline.After(now) {
		if err := s.cancelOrchestrationChildrenTx(ctx, tx, id, now, "orchestration deadline exceeded"); err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `update steward_orchestrations set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`,
			id, OrchestrationBlocked, "orchestration deadline exceeded", now)
		if err == nil {
			err = appendOrchestrationEvent(ctx, tx, id, "", "orchestration.deadline", OrchestrationBlocked, "deadline stopped further delegation", map[string]any{})
		}
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	var delegationRefreshDue bool
	if err := tx.QueryRow(ctx, `
		select exists (
			select 1 from steward_delegation_claims claim
			join steward_orchestration_nodes node on node.id=claim.node_id
			where claim.orchestration_id=$1 and node.status in ('dispatched','running')
			  and claim.expires_at <= $2
		)
	`, id, now.Add(s.orchestrationDelegationTTL/3)).Scan(&delegationRefreshDue); err != nil {
		return false, err
	}
	if storedGeneration != generation || delegationRefreshDue {
		if err := s.refreshOrchestrationDelegationsTx(ctx, tx, id, planHash, generation, deadline, now); err != nil {
			return false, err
		}
		if _, err := tx.Exec(ctx, `update steward_orchestrations set control_generation=$2, updated_at=$3 where id=$1`, id, generation, now); err != nil {
			return false, err
		}
		if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.delegations_refreshed", status,
			"delegations refreshed and fenced to the active execution generation", map[string]any{"control_generation": generation}); err != nil {
			return false, err
		}
	}
	nodes, err := loadOrchestrationScheduleNodes(ctx, tx, id)
	if err != nil {
		return false, err
	}
	statusByKey := map[string]string{}
	active := 0
	for index := range nodes {
		node := &nodes[index]
		next := orchestrationNodeStatusForRuntime(node.Status, node.RuntimeStatus)
		if next != node.Status {
			completed := any(nil)
			if next == OrchestrationNodeSucceeded || next == OrchestrationNodeFailed || next == OrchestrationNodeCancelled || next == OrchestrationNodeBlocked {
				completed = now
			}
			_, err := tx.Exec(ctx, `
				update steward_orchestration_nodes set status=$2, failure_summary=$3,
				       started_at=case when $2='running' then coalesce(started_at,$4) else started_at end,
				       completed_at=$5, updated_at=$4 where id=$1
			`, node.ID, next, node.FailureSummary, now, completed)
			if err != nil {
				return false, err
			}
			node.Status = next
			if err := appendOrchestrationEvent(ctx, tx, id, node.ID, "node."+next, next,
				"child Runtime V2 state reconciled", map[string]any{"runtime_run_id": node.RuntimeRunID}); err != nil {
				return false, err
			}
		}
		statusByKey[node.Key] = node.Status
		if node.Status == OrchestrationNodeDispatched || node.Status == OrchestrationNodeRunning {
			active++
		}
	}
	// collect_all keeps independent siblings running, but a node whose declared
	// dependency has already failed can never become ready. Make that outcome
	// explicit (and cascade it through dependent nodes) rather than leaving the
	// parent running forever with pending work.
	if failurePolicy == "collect_all" && status != OrchestrationCompensating {
		for index := range nodes {
			node := &nodes[index]
			if node.Kind == "compensation" || node.Status != OrchestrationNodePending {
				continue
			}
			for _, dependency := range node.DependsOn {
				dependencyStatus := statusByKey[dependency]
				if !orchestrationNodeTerminal(dependencyStatus) || dependencyStatus == OrchestrationNodeSucceeded {
					continue
				}
				message := fmt.Sprintf("dependency %s ended as %s", dependency, dependencyStatus)
				if _, err := tx.Exec(ctx, `
					update steward_orchestration_nodes set status=$2, failure_summary=$3,
					       completed_at=$4, updated_at=$4 where id=$1
				`, node.ID, OrchestrationNodeBlocked, message, now); err != nil {
					return false, err
				}
				node.Status = OrchestrationNodeBlocked
				node.FailureSummary = message
				statusByKey[node.Key] = node.Status
				if err := appendOrchestrationEvent(ctx, tx, id, node.ID, "node.blocked", OrchestrationNodeBlocked,
					message, map[string]any{"dependency": dependency, "dependency_status": dependencyStatus}); err != nil {
					return false, err
				}
				break
			}
		}
	}
	allForwardSucceeded := false
	allForwardTerminal := false
	forwardCount := 0
	forwardNonSuccess := 0
	failedNode := ""
	failedStatus := ""
	collectAllHasFailure := false
	compensationCount := 0
	allCompensationsSucceeded := true
	compensationFailedNode := ""
	forwardDrainActive := 0
	for index := range nodes {
		node := &nodes[index]
		if node.Kind == "compensation" {
			compensationCount++
			if node.Status != OrchestrationNodeSucceeded {
				allCompensationsSucceeded = false
			}
			if node.Status == OrchestrationNodeFailed || node.Status == OrchestrationNodeCancelled || node.Status == OrchestrationNodeBlocked {
				compensationFailedNode = node.Key
			}
		} else {
			forwardCount++
			if node.RuntimeRunID != "" && !runtimeRunTerminal(node.RuntimeStatus) {
				forwardDrainActive++
			}
			if node.Status == OrchestrationNodeFailed || node.Status == OrchestrationNodeCancelled || node.Status == OrchestrationNodeBlocked {
				forwardNonSuccess++
				if failedNode == "" {
					failedNode, failedStatus = node.Key, node.Status
				}
				if node.Status == OrchestrationNodeFailed || node.Status == OrchestrationNodeCancelled {
					collectAllHasFailure = true
				}
			}
		}
	}
	if forwardCount == 0 {
		allForwardSucceeded, allForwardTerminal = false, false
	} else {
		allForwardSucceeded, allForwardTerminal = true, true
		for _, node := range nodes {
			if node.Kind == "compensation" {
				continue
			}
			if node.Status != OrchestrationNodeSucceeded {
				allForwardSucceeded = false
			}
			if !orchestrationNodeTerminal(node.Status) {
				allForwardTerminal = false
			}
		}
	}
	if status == OrchestrationCompensating {
		if forwardDrainActive > 0 {
			return true, tx.Commit(ctx)
		}
		if compensationFailedNode != "" {
			if err := s.cancelOrchestrationChildrenTx(ctx, tx, id, now, "compensation failure stopped remaining compensations"); err != nil {
				return false, err
			}
			message := fmt.Sprintf("compensation node %s failed", compensationFailedNode)
			if _, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`,
				id, OrchestrationCompensationFailed, message, now); err != nil {
				return false, err
			}
			if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.compensation_failed", OrchestrationCompensationFailed,
				message, map[string]any{"node": compensationFailedNode}); err != nil {
				return false, err
			}
			return true, tx.Commit(ctx)
		}
		if compensationCount > 0 && allCompensationsSucceeded {
			if _, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, updated_at=$3, completed_at=$3 where id=$1`,
				id, OrchestrationCompensated, now); err != nil {
				return false, err
			}
			if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration.compensated", OrchestrationCompensated,
				"all declared compensations completed in reverse order", map[string]any{"compensation_count": compensationCount}); err != nil {
				return false, err
			}
			return true, tx.Commit(ctx)
		}
	}
	if failedNode != "" && failurePolicy == "fail_fast" {
		if err := s.cancelOrchestrationChildrenTx(ctx, tx, id, now, "fail-fast sibling cancellation"); err != nil {
			return false, err
		}
		parentStatus := OrchestrationFailed
		if failedStatus == OrchestrationNodeBlocked {
			parentStatus = OrchestrationBlocked
		}
		message := fmt.Sprintf("node %s ended as %s", failedNode, failedStatus)
		_, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`, id, parentStatus, message, now)
		if err == nil {
			err = appendOrchestrationEvent(ctx, tx, id, "", "orchestration."+parentStatus, parentStatus, message, map[string]any{"node": failedNode})
		}
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if failedNode != "" && failurePolicy == "compensate" && status != OrchestrationCompensating {
		if failedStatus == OrchestrationNodeFailed {
			started, err := s.beginOrchestrationCompensationTx(ctx, tx, id, nodes, failedNode, now)
			if err != nil {
				return false, err
			}
			if started {
				return true, tx.Commit(ctx)
			}
		}
		if err := s.cancelOrchestrationChildrenTx(ctx, tx, id, now, "failure stopped remaining forward nodes"); err != nil {
			return false, err
		}
		parentStatus := OrchestrationFailed
		if failedStatus == OrchestrationNodeBlocked {
			parentStatus = OrchestrationBlocked
		}
		message := fmt.Sprintf("node %s ended as %s; no compensation was started", failedNode, failedStatus)
		if _, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`, id, parentStatus, message, now); err != nil {
			return false, err
		}
		if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration."+parentStatus, parentStatus, message, map[string]any{"node": failedNode}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if failurePolicy == "collect_all" && allForwardTerminal && !allForwardSucceeded {
		parentStatus := OrchestrationBlocked
		if collectAllHasFailure {
			parentStatus = OrchestrationFailed
		}
		message := fmt.Sprintf("%d of %d nodes ended without success; first: %s=%s",
			forwardNonSuccess, forwardCount, failedNode, failedStatus)
		if _, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`,
			id, parentStatus, message, now); err != nil {
			return false, err
		}
		if err := appendOrchestrationEvent(ctx, tx, id, "", "orchestration."+parentStatus, parentStatus, message,
			map[string]any{"failed_node": failedNode, "non_success_count": forwardNonSuccess, "node_count": forwardCount}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if allForwardSucceeded && status != OrchestrationCompensating {
		_, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, updated_at=$3, completed_at=$3 where id=$1`, id, OrchestrationSucceeded, now)
		if err == nil {
			err = appendOrchestrationEvent(ctx, tx, id, "", "orchestration.succeeded", OrchestrationSucceeded,
				"all delegated child runs satisfied their postconditions", map[string]any{"node_count": len(nodes)})
		}
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if status == OrchestrationQueued {
		if _, err := tx.Exec(ctx, `update steward_orchestrations set status=$2, started_at=coalesce(started_at,$3), updated_at=$3 where id=$1`, id, OrchestrationRunning, now); err != nil {
			return false, err
		}
		status = OrchestrationRunning
	}
	for index := range nodes {
		if active >= maxParallel {
			break
		}
		node := &nodes[index]
		if (status == OrchestrationCompensating) != (node.Kind == "compensation") {
			continue
		}
		if node.Status != OrchestrationNodePending {
			continue
		}
		if node.TargetDevice != "local" && node.RemotePrivilegeCapability != "" && node.RemoteBrokerDelegationID == "" {
			continue
		}
		ready := true
		for _, dependency := range node.DependsOn {
			if statusByKey[dependency] != OrchestrationNodeSucceeded {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		var agentLimit, agentActive int
		if err := tx.QueryRow(ctx, `
			select agent.max_concurrency,
			       count(node.id) filter (where node.status in ('dispatched','running'))::int
			from steward_orchestration_agents agent
			left join steward_orchestration_nodes node on node.agent_id=agent.id
			where agent.id=$1 and agent.enabled group by agent.id
		`, node.AgentID).Scan(&agentLimit, &agentActive); errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("%w: agent %q is disabled", ErrOrchestrationInvalid, node.AgentID)
		} else if err != nil {
			return false, err
		}
		if agentActive >= agentLimit {
			continue
		}
		if err := s.dispatchOrchestrationNodeTx(ctx, tx, id, planHash, generation, deadline, *node, now); err != nil {
			return false, err
		}
		node.Status = OrchestrationNodeDispatched
		statusByKey[node.Key] = node.Status
		active++
	}
	return true, tx.Commit(ctx)
}

func (s *Service) beginOrchestrationCompensationTx(ctx context.Context, tx pgx.Tx, orchestrationID string, nodes []orchestrationScheduleNode, failedNode string, now time.Time) (bool, error) {
	if err := s.cancelOrchestrationChildrenTx(ctx, tx, orchestrationID, now, "forward failure entered compensation"); err != nil {
		return false, err
	}
	candidates := make([]orchestrationScheduleNode, 0, len(nodes))
	maxPosition := 0
	for _, node := range nodes {
		if node.Position > maxPosition {
			maxPosition = node.Position
		}
		if node.Kind == "forward" && node.Status == OrchestrationNodeSucceeded && len(node.CompensationSteps) > 0 {
			candidates = append(candidates, node)
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}
	previousKey := ""
	for index := len(candidates) - 1; index >= 0; index-- {
		forward := candidates[index]
		key := fmt.Sprintf("compensate-%03d", forward.Position)
		dependsOn := []string{}
		if previousKey != "" {
			dependsOn = append(dependsOn, previousKey)
		}
		encodedDependsOn, _ := json.Marshal(dependsOn)
		encodedSteps, _ := json.Marshal(forward.CompensationSteps)
		targetDevice := forward.TargetDevice
		selectedDeviceID := forward.SelectedDeviceID
		if selectedDeviceID != "" {
			targetDevice = selectedDeviceID
		}
		maxPosition++
		if _, err := tx.Exec(ctx, `
			insert into steward_orchestration_nodes (
				id, orchestration_id, node_key, position, agent_id, goal, status, depends_on,
				permission_ceiling, data_level, steps, compensation_steps, kind,
				compensation_of_id, target_device, selected_device_id, created_at, updated_at
			) values ($1,$2,$3,$4,$5,$6,'pending',$7::jsonb,$8,$9,$10::jsonb,'[]'::jsonb,
			          'compensation',$11,$12,nullif($13,''),$14,$14)
			on conflict (orchestration_id, node_key) do nothing
		`, uuid.NewString(), orchestrationID, key, maxPosition, forward.AgentID,
			"compensate "+forward.Goal, string(encodedDependsOn), forward.PermissionCeiling,
			forward.DataLevel, string(encodedSteps), forward.ID, targetDevice, selectedDeviceID, now); err != nil {
			return false, fmt.Errorf("materialize compensation for node %s: %w", forward.Key, err)
		}
		previousKey = key
	}
	message := fmt.Sprintf("node %s failed; %d compensations scheduled in reverse order", failedNode, len(candidates))
	if _, err := tx.Exec(ctx, `
		update steward_orchestrations set status=$2, failure_summary=$3,
		       updated_at=$4, completed_at=null where id=$1
	`, orchestrationID, OrchestrationCompensating, message, now); err != nil {
		return false, err
	}
	if err := appendOrchestrationEvent(ctx, tx, orchestrationID, "", "orchestration.compensating",
		OrchestrationCompensating, message, map[string]any{"failed_node": failedNode, "compensation_count": len(candidates)}); err != nil {
		return false, err
	}
	return true, nil
}

func loadOrchestrationScheduleNodes(ctx context.Context, tx pgx.Tx, id string) ([]orchestrationScheduleNode, error) {
	rows, err := tx.Query(ctx, `
		select node.id::text, node.node_key, node.position, node.agent_id, node.goal, node.status,
		       node.kind, coalesce(node.compensation_of_id::text,''), node.depends_on,
		       node.target_device, coalesce(node.selected_device_id,''),
		       node.permission_ceiling, node.data_level, node.steps, node.compensation_steps,
		       coalesce(node.runtime_run_id::text,''), coalesce(run.status, remote.status, ''),
		       coalesce(nullif(run.failure_summary,''), nullif(remote.last_error,''), node.failure_summary),
		       node.remote_privilege_capability, node.remote_privilege_plan_hash,
		       node.remote_broker_delegation, node.remote_broker_delegation_id, node.remote_credential_refs
		from steward_orchestration_nodes node
		left join steward_agent_runs run on run.id=node.runtime_run_id
		left join steward_remote_dispatches remote on remote.node_id=node.id
		where node.orchestration_id=$1 order by node.position for update of node
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []orchestrationScheduleNode{}
	for rows.Next() {
		var item orchestrationScheduleNode
		var dependsOn, steps, compensationSteps, credentialRefs []byte
		if err := rows.Scan(&item.ID, &item.Key, &item.Position, &item.AgentID, &item.Goal, &item.Status,
			&item.Kind, &item.CompensationOfID, &dependsOn, &item.TargetDevice, &item.SelectedDeviceID,
			&item.PermissionCeiling, &item.DataLevel,
			&steps, &compensationSteps,
			&item.RuntimeRunID, &item.RuntimeStatus, &item.FailureSummary,
			&item.RemotePrivilegeCapability, &item.RemotePrivilegePlanHash,
			&item.RemoteBrokerDelegation, &item.RemoteBrokerDelegationID, &credentialRefs); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(dependsOn, &item.DependsOn)
		_ = json.Unmarshal(steps, &item.Steps)
		_ = json.Unmarshal(compensationSteps, &item.CompensationSteps)
		_ = json.Unmarshal(credentialRefs, &item.RemoteCredentialRefs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func orchestrationNodeStatusForRuntime(current, runtimeStatus string) string {
	if current == OrchestrationNodeSucceeded || current == OrchestrationNodeFailed ||
		current == OrchestrationNodeCancelled || current == OrchestrationNodeBlocked {
		return current
	}
	if runtimeStatus == "" {
		return current
	}
	switch runtimeStatus {
	case RuntimeRunDraft, RuntimeRunPlanning, RuntimeRunAwaitingApproval, RuntimeRunQueued, "pending", "sent", "accepted":
		return OrchestrationNodeDispatched
	case RuntimeRunRunning, RuntimeRunVerifying, RuntimeRunCompensating:
		return OrchestrationNodeRunning
	case RuntimeRunSucceeded:
		return OrchestrationNodeSucceeded
	case RuntimeRunFailed:
		return OrchestrationNodeFailed
	case RuntimeRunCancelled:
		return OrchestrationNodeCancelled
	case RuntimeRunBlocked:
		return OrchestrationNodeBlocked
	default:
		return current
	}
}

func (s *Service) dispatchOrchestrationNodeTx(ctx context.Context, tx pgx.Tx, orchestrationID, orchestrationPlanHash string, generation int64, deadline *time.Time, node orchestrationScheduleNode, now time.Time) error {
	if node.TargetDevice != "local" {
		return s.dispatchRemoteOrchestrationNodeTx(ctx, tx, orchestrationID, generation, deadline, node, now)
	}
	planner := "r4-orchestrator"
	plannerVersion := "4.0.0"
	planSummary := "R4.0 node " + node.Key + " assigned to " + node.AgentID
	if node.Kind == "compensation" {
		planner = "r4-saga"
		plannerVersion = "4.2.0"
		planSummary = "R4.2 compensation node " + node.Key + " assigned to " + node.AgentID
	}
	child, err := s.CreateAgentRun(ctx, CreateAgentRunInput{
		Goal: node.Goal, Mode: "manual",
		IdempotencyKey: fmt.Sprintf("r4:%s:%s:%s", orchestrationID, node.Key, orchestrationPlanHash[:16]),
		RequestedBy:    "orchestrator:" + node.AgentID, TargetDevice: "local",
		DataLevel: node.DataLevel, PermissionCeiling: node.PermissionCeiling,
		Planner: planner, PlannerVersion: plannerVersion,
		SourceInstruction: "delegated by local orchestration " + orchestrationID,
		PlanSummary:       planSummary,
		AutoStart:         false, Steps: node.Steps,
	})
	if err != nil {
		return fmt.Errorf("create child run for node %s: %w", node.Key, err)
	}
	expiresAt := now.Add(s.orchestrationDelegationTTL)
	if deadline != nil && deadline.Before(expiresAt) {
		expiresAt = *deadline
	}
	expiresAt = expiresAt.UTC().Truncate(time.Microsecond)
	claim := domain.StewardDelegationClaim{
		ID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte("steward-r4:"+node.ID)).String(),
		AgentID: node.AgentID, NodeID: node.ID, PlanHash: child.PlanHash,
		PermissionCeiling: node.PermissionCeiling, DataLevel: node.DataLevel,
		ControlGeneration: generation, ExpiresAt: expiresAt,
	}
	claim.Signature = s.signDelegationClaim(orchestrationID, claim)
	if claim.Signature == "" {
		return fmt.Errorf("R4 scheduler does not hold the Ed25519 delegation signing key")
	}
	_, err = tx.Exec(ctx, `
		insert into steward_delegation_claims (
			id, orchestration_id, node_id, agent_id, plan_hash, permission_ceiling,
			data_level, control_generation, expires_at, signature, created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		on conflict (node_id) do update set
			agent_id=excluded.agent_id, plan_hash=excluded.plan_hash,
			permission_ceiling=excluded.permission_ceiling, data_level=excluded.data_level,
			control_generation=excluded.control_generation, expires_at=excluded.expires_at,
			signature=excluded.signature
	`, claim.ID, orchestrationID, node.ID, claim.AgentID, claim.PlanHash, claim.PermissionCeiling,
		claim.DataLevel, claim.ControlGeneration, claim.ExpiresAt, claim.Signature, now)
	if err != nil {
		return fmt.Errorf("persist node delegation: %w", err)
	}
	// Preserve an existing claim id when recovering an idempotently-created
	// draft child after a scheduler crash.
	if err := tx.QueryRow(ctx, `select id::text from steward_delegation_claims where node_id=$1`, node.ID).Scan(&claim.ID); err != nil {
		return err
	}
	childStatus := RuntimeRunQueued
	for _, step := range node.Steps {
		if step.RequiresApproval {
			childStatus = RuntimeRunAwaitingApproval
			break
		}
	}
	useWorker := false
	if s.orchestrationWorkers && childStatus == RuntimeRunQueued {
		useWorker, err = s.hasLiveOrchestrationWorkerTx(ctx, tx, node.AgentID, now)
		if err != nil {
			return err
		}
	}
	if useWorker {
		childStatus = RuntimeRunDraft
	}
	command, err := tx.Exec(ctx, `
		update steward_agent_runs set orchestration_id=$2, orchestration_node_id=$3,
		       delegation_id=$4, status=$5, updated_at=$6
		where id=$1 and status in ('draft','planning','queued','awaiting_approval')
	`, child.ID, orchestrationID, node.ID, claim.ID, childStatus, now)
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("child run is no longer dispatchable")
		}
		return fmt.Errorf("bind delegated child run: %w", err)
	}
	_, err = tx.Exec(ctx, `
		update steward_orchestration_nodes set status=$2, runtime_run_id=$3,
		       delegation_id=$4, updated_at=$5 where id=$1 and status='pending'
	`, node.ID, OrchestrationNodeDispatched, child.ID, claim.ID, now)
	if err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, child.ID, nil, "run.delegated", childStatus,
		"R4 scheduler bound the run to a task-scoped delegation", map[string]any{
			"orchestration_id": orchestrationID, "node_id": node.ID, "agent_id": node.AgentID,
			"delegation_id": claim.ID, "control_generation": generation, "node_kind": node.Kind,
		}); err != nil {
		return err
	}
	if useWorker {
		var maxRuntime, maxAttempts, maxEvidence int
		if err := tx.QueryRow(ctx, `
			select max_runtime_seconds, max_attempts, max_evidence_bytes
			from steward_orchestration_agents where id=$1
		`, node.AgentID).Scan(&maxRuntime, &maxAttempts, &maxEvidence); err != nil {
			return err
		}
		messagePayload, _ := json.Marshal(map[string]any{
			"plan_hash": child.PlanHash, "delegation_id": claim.ID,
			"runtime_budget_seconds": maxRuntime, "attempt_budget": maxAttempts,
			"evidence_budget_bytes": maxEvidence,
		})
		if _, err := tx.Exec(ctx, `
			insert into steward_agent_messages (
				id, agent_id, orchestration_id, node_id, runtime_run_id, type, status,
				payload, max_attempts, available_at, created_at, updated_at
			) values ($1,$2,$3,$4,$5,'execute','pending',$6::jsonb,3,$7,$7,$7)
			on conflict (node_id, type) do nothing
		`, uuid.NewString(), node.AgentID, orchestrationID, node.ID, child.ID, string(messagePayload), now); err != nil {
			return fmt.Errorf("enqueue Agent message: %w", err)
		}
	}
	return appendOrchestrationEvent(ctx, tx, orchestrationID, node.ID, "node.dispatched", OrchestrationNodeDispatched,
		"node materialized as a standard Runtime V2 child run", map[string]any{
			"runtime_run_id": child.ID, "agent_id": node.AgentID, "delegation_id": claim.ID, "node_kind": node.Kind,
		})
}

func (s *Service) orchestrationWorkerFreshnessWindow() time.Duration {
	window := 2 * s.orchestrationMessageLease
	if window < 30*time.Second {
		window = 30 * time.Second
	}
	return window
}

func (s *Service) hasLiveOrchestrationWorkerTx(ctx context.Context, tx pgx.Tx, agentID string, now time.Time) (bool, error) {
	var live bool
	err := tx.QueryRow(ctx, `
		select exists (
			select 1 from steward_agent_workers
			where agent_id=$1 and status='running' and heartbeat_at >= $2
		)
	`, agentID, now.Add(-s.orchestrationWorkerFreshnessWindow())).Scan(&live)
	return live, err
}

// recoverUnavailableOrchestrationWorkersTx prevents an enabled-but-undeployed
// Worker plane from leaving local child runs in draft forever. Healthy workers
// retain mailbox ownership; only drafts with no fresh worker fall back to the
// standard local Runtime V2 queue.
func (s *Service) recoverUnavailableOrchestrationWorkersTx(ctx context.Context, tx pgx.Tx, orchestrationID string, now time.Time) error {
	cutoff := now.Add(-s.orchestrationWorkerFreshnessWindow())
	rows, err := tx.Query(ctx, `
		select run.id::text, node.id::text, message.id::text, node.agent_id
		from steward_orchestration_nodes node
		join steward_agent_runs run on run.id=node.runtime_run_id
		join steward_agent_messages message on message.node_id=node.id and message.type='execute'
		where node.orchestration_id=$1 and node.status='dispatched' and run.status='draft'
		  and message.status in ('pending','leased')
		  and not exists (
			select 1 from steward_agent_workers worker
			where worker.agent_id=node.agent_id and worker.status='running' and worker.heartbeat_at >= $2
		  )
		for update of node, run, message
	`, orchestrationID, cutoff)
	if err != nil {
		return err
	}
	type fallback struct{ runID, nodeID, messageID, agentID string }
	items := []fallback{}
	for rows.Next() {
		var item fallback
		if err := rows.Scan(&item.runID, &item.nodeID, &item.messageID, &item.agentID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `update steward_agent_runs set status='queued',updated_at=$2 where id=$1 and status='draft'`, item.runID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update steward_agent_messages set status='dead',lease_owner='',lease_expires_at=null,
			last_error='no healthy Agent worker; execution fell back to local Runtime V2',acknowledged_at=$2,updated_at=$2
			where id=$1 and status in ('pending','leased')`, item.messageID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update steward_agent_workers set status='stopped',current_message_id=null,stopped_at=$2,heartbeat_at=$2
			where agent_id=$1 and status='running' and heartbeat_at < $3`, item.agentID, now, cutoff); err != nil {
			return err
		}
		if err := appendRuntimeEvent(ctx, tx, item.runID, nil, "run.worker_fallback", RuntimeRunQueued,
			"no healthy Agent worker was available; queued in local Runtime V2", map[string]any{"agent_id": item.agentID}); err != nil {
			return err
		}
		if err := appendOrchestrationEvent(ctx, tx, orchestrationID, item.nodeID, "node.worker_fallback", OrchestrationNodeDispatched,
			"no healthy Agent worker was available; child run fell back to local Runtime V2", map[string]any{"agent_id": item.agentID, "runtime_run_id": item.runID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) signDelegationClaim(orchestrationID string, claim domain.StewardDelegationClaim) string {
	if len(s.orchestrationSigningKey) != ed25519.PrivateKeySize {
		return ""
	}
	payload := delegationClaimPayload(orchestrationID, claim)
	signature := ed25519.Sign(ed25519.PrivateKey(s.orchestrationSigningKey), []byte(payload))
	return base64.RawURLEncoding.EncodeToString(signature)
}

func delegationClaimPayload(orchestrationID string, claim domain.StewardDelegationClaim) string {
	return strings.Join([]string{
		"steward-r4-delegation/v1", orchestrationID, claim.ID, claim.NodeID, claim.AgentID,
		claim.PlanHash, claim.PermissionCeiling, claim.DataLevel,
		fmt.Sprintf("%d", claim.ControlGeneration), claim.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}, "\n")
}

func (s *Service) refreshOrchestrationDelegationsTx(ctx context.Context, tx pgx.Tx, orchestrationID, _ string, generation int64, deadline *time.Time, now time.Time) error {
	rows, err := tx.Query(ctx, `
		select claim.id::text, claim.node_id::text, claim.agent_id, claim.plan_hash,
		       claim.permission_ceiling, claim.data_level
		from steward_delegation_claims claim
		join steward_orchestration_nodes node on node.id=claim.node_id
		where claim.orchestration_id=$1 and node.status in ('dispatched','running')
	`, orchestrationID)
	if err != nil {
		return err
	}
	claims := []domain.StewardDelegationClaim{}
	for rows.Next() {
		var claim domain.StewardDelegationClaim
		if err := rows.Scan(&claim.ID, &claim.NodeID, &claim.AgentID, &claim.PlanHash,
			&claim.PermissionCeiling, &claim.DataLevel); err != nil {
			rows.Close()
			return err
		}
		claims = append(claims, claim)
	}
	rows.Close()
	for _, claim := range claims {
		claim.ControlGeneration = generation
		claim.ExpiresAt = now.Add(s.orchestrationDelegationTTL)
		if deadline != nil && deadline.Before(claim.ExpiresAt) {
			claim.ExpiresAt = *deadline
		}
		claim.ExpiresAt = claim.ExpiresAt.UTC().Truncate(time.Microsecond)
		claim.Signature = s.signDelegationClaim(orchestrationID, claim)
		if claim.Signature == "" {
			return fmt.Errorf("R4 scheduler does not hold the Ed25519 delegation signing key")
		}
		if _, err := tx.Exec(ctx, `
			update steward_delegation_claims set control_generation=$2, expires_at=$3, signature=$4 where id=$1
		`, claim.ID, claim.ControlGeneration, claim.ExpiresAt, claim.Signature); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) cancelOrchestrationChildrenTx(ctx context.Context, tx pgx.Tx, orchestrationID string, now time.Time, reason string) error {
	if _, err := tx.Exec(ctx, `
		update steward_remote_dispatches set cancel_requested=true, last_error=$2,
		       available_at=$3, updated_at=$3
		where orchestration_id=$1 and status in ('pending','sent','accepted','running')
	`, orchestrationID, reason, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		select run.id::text, run.status from steward_agent_runs run
		join steward_orchestration_nodes node on node.runtime_run_id=run.id
		where node.orchestration_id=$1 and run.status not in ('succeeded','failed','cancelled','blocked')
		for update of run
	`, orchestrationID)
	if err != nil {
		return err
	}
	type childState struct{ id, status string }
	children := []childState{}
	for rows.Next() {
		var child childState
		if err := rows.Scan(&child.id, &child.status); err != nil {
			rows.Close()
			return err
		}
		children = append(children, child)
	}
	rows.Close()
	for _, child := range children {
		if child.status == RuntimeRunRunning || child.status == RuntimeRunVerifying || child.status == RuntimeRunCompensating {
			if _, err := tx.Exec(ctx, `update steward_agent_runs set cancel_requested=true, updated_at=$2 where id=$1`, child.id, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `update steward_agent_runs set status=$2, cancel_requested=false, failure_summary=$3, updated_at=$4, completed_at=$4 where id=$1`,
				child.id, RuntimeRunCancelled, reason, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `update steward_run_steps set status=$2, updated_at=$3, completed_at=$3 where run_id=$1 and status not in ('succeeded','failed','cancelled')`,
				child.id, RuntimeStepCancelled, now); err != nil {
				return err
			}
		}
		if err := appendRuntimeEvent(ctx, tx, child.id, nil, "run.parent_cancelled", RuntimeRunCancelled, reason, map[string]any{"orchestration_id": orchestrationID}); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		update steward_orchestration_nodes set status=$2, failure_summary=$3, updated_at=$4, completed_at=$4
		where orchestration_id=$1 and status in ('pending','dispatched','running')
	`, orchestrationID, OrchestrationNodeCancelled, reason, now); err != nil {
		return err
	}
	return nil
}

func (s *Service) verifyRuntimeOrchestrationDelegationTx(ctx context.Context, tx pgx.Tx, runID string, now time.Time) error {
	var orchestrationID, nodeID, agentID, claimID, runPlanHash, runPermission, runData string
	err := tx.QueryRow(ctx, `
		select coalesce(run.orchestration_id::text,''), coalesce(run.orchestration_node_id::text,''),
		       coalesce(node.agent_id,''), coalesce(run.delegation_id::text,''),
		       run.plan_hash, run.permission_ceiling, run.data_level
		from steward_agent_runs run
		left join steward_orchestration_nodes node on node.id=run.orchestration_node_id
		where run.id=$1
	`, runID).Scan(&orchestrationID, &nodeID, &agentID, &claimID, &runPlanHash, &runPermission, &runData)
	if err != nil || orchestrationID == "" {
		return err
	}
	if claimID == "" || nodeID == "" || agentID == "" {
		return fmt.Errorf("delegated run is missing its task-bound claim")
	}
	var claim domain.StewardDelegationClaim
	var parentStatus, nodeStatus string
	var agentEnabled bool
	var agentPermission, agentData string
	var agentTools []byte
	var currentGeneration int64
	err = tx.QueryRow(ctx, `
		select claim.id::text, claim.agent_id, claim.node_id::text, claim.plan_hash,
		       claim.permission_ceiling, claim.data_level, claim.control_generation,
		       claim.expires_at, claim.signature, parent.status, node.status,
		       agent.enabled, agent.permission_ceiling, agent.data_level_ceiling,
		       agent.tool_allowlist, control.generation
		from steward_delegation_claims claim
		join steward_orchestrations parent on parent.id=claim.orchestration_id
		join steward_orchestration_nodes node on node.id=claim.node_id
		join steward_orchestration_agents agent on agent.id=claim.agent_id
		join steward_runtime_execution_control control on control.id='global'
		where claim.id=$1 and claim.orchestration_id=$2 and claim.node_id=$3
	`, claimID, orchestrationID, nodeID).Scan(&claim.ID, &claim.AgentID, &claim.NodeID,
		&claim.PlanHash, &claim.PermissionCeiling, &claim.DataLevel, &claim.ControlGeneration,
		&claim.ExpiresAt, &claim.Signature, &parentStatus, &nodeStatus, &agentEnabled,
		&agentPermission, &agentData, &agentTools, &currentGeneration)
	if err != nil {
		return fmt.Errorf("load delegated run claim: %w", err)
	}
	if !agentEnabled || (parentStatus != OrchestrationQueued && parentStatus != OrchestrationRunning && parentStatus != OrchestrationCompensating) ||
		(nodeStatus != OrchestrationNodeDispatched && nodeStatus != OrchestrationNodeRunning) {
		return fmt.Errorf("delegation principal or parent state is no longer active")
	}
	if !claim.ExpiresAt.After(now) || claim.ControlGeneration != currentGeneration {
		return fmt.Errorf("delegation expired or was fenced by execution control")
	}
	if claim.AgentID != agentID || claim.PlanHash != runPlanHash || claim.PermissionCeiling != runPermission || claim.DataLevel != runData {
		return fmt.Errorf("delegation does not match the child run policy envelope")
	}
	if !ownerModeEnabled() && (permissionRank(claim.PermissionCeiling) > permissionRank(agentPermission) || dataLevelRank(claim.DataLevel) > dataLevelRank(agentData)) {
		return fmt.Errorf("agent policy was reduced after delegation")
	}
	var revokedTools int
	if err := tx.QueryRow(ctx, `
		select count(*)::int from steward_run_steps step
		where step.run_id=$1 and not ($2::jsonb ? step.tool_name)
	`, runID, string(agentTools)).Scan(&revokedTools); err != nil {
		return fmt.Errorf("verify delegated tool allowlist: %w", err)
	}
	if revokedTools > 0 {
		return fmt.Errorf("agent tool delegation was revoked")
	}
	provided, decodeErr := base64.RawURLEncoding.DecodeString(claim.Signature)
	payload := delegationClaimPayload(orchestrationID, claim)
	if decodeErr != nil || !ed25519.Verify(ed25519.PublicKey(s.orchestrationVerifyKey), []byte(payload), provided) {
		return fmt.Errorf("delegation signature is invalid")
	}
	return nil
}

func (s *Service) blockInvalidDelegatedRunTx(ctx context.Context, tx pgx.Tx, runID string, cause error, now time.Time) error {
	message := "R4.0 delegation rejected: " + cause.Error()
	var currentStatus string
	var cancelRequested bool
	if err := tx.QueryRow(ctx, `select status,cancel_requested from steward_agent_runs where id=$1 for update`, runID).Scan(&currentStatus, &cancelRequested); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if runtimeRunTerminal(currentStatus) {
		return nil
	}
	if cancelRequested {
		return finishAgentRunCancelledTx(ctx, tx, runID, "cancellation won before delegation rejection was committed", now)
	}
	var transitioned int
	// The Runtime run is the ownership boundary for a delegation rejection.
	// Fence this write against a concurrent success/cancel/failure first. The
	// orchestration scheduler will project the terminal child state to its node
	// and parent on the next cycle. Updating parent rows here would invert the
	// scheduler's parent -> child lock order and can deadlock with cancellation.
	err := tx.QueryRow(ctx, `
		update steward_agent_runs
		set status=$2, failure_summary=$3, cancel_requested=false,
		    updated_at=$4, completed_at=$4
		where id=$1 and status in ('draft','planning','awaiting_approval','queued','running','verifying','compensating')
		  and cancel_requested=false
		returning 1
	`, runID, RuntimeRunBlocked, message, now).Scan(&transitioned)
	if errors.Is(err, pgx.ErrNoRows) {
		// A newer terminal decision already won. An expired lease, stale NACK or
		// delayed verifier must never rewrite it.
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_run_steps set status=$2, last_error=$3, updated_at=$4, completed_at=$4
		where run_id=$1 and status in ('pending','running','verifying','blocked')
	`, runID, RuntimeStepBlocked, message, now); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, runID, nil, "run.delegation_rejected", RuntimeRunBlocked, message, map[string]any{}); err != nil {
		return err
	}
	return nil
}
