package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

const (
	RuntimeRunDraft            = "draft"
	RuntimeRunPlanning         = "planning"
	RuntimeRunAwaitingApproval = "awaiting_approval"
	RuntimeRunQueued           = "queued"
	RuntimeRunRunning          = "running"
	RuntimeRunVerifying        = "verifying"
	RuntimeRunSucceeded        = "succeeded"
	RuntimeRunFailed           = "failed"
	RuntimeRunCancelled        = "cancelled"
	RuntimeRunCompensating     = "compensating"
	RuntimeRunBlocked          = "blocked"

	RuntimeStepPending   = "pending"
	RuntimeStepRunning   = "running"
	RuntimeStepVerifying = "verifying"
	RuntimeStepSucceeded = "succeeded"
	RuntimeStepFailed    = "failed"
	RuntimeStepCancelled = "cancelled"
	RuntimeStepBlocked   = "blocked"
)

var (
	ErrRuntimeV2Disabled         = errors.New("steward runtime v2 is disabled")
	ErrAgentRunNotFound          = errors.New("agent run not found")
	ErrAgentRunConflict          = errors.New("agent run idempotency conflict")
	ErrAgentRunInvalid           = errors.New("invalid agent run")
	ErrAgentRunInvalidTransition = errors.New("invalid agent run state transition")
	ErrAgentRunPlanHashMismatch  = errors.New("agent run plan hash mismatch")
	ErrExecutionEmergencyStopped = errors.New("system-wide execution emergency stop is active")
)

type CreateAgentRunInput struct {
	Goal              string                    `json:"goal"`
	Mode              string                    `json:"mode"`
	IdempotencyKey    string                    `json:"idempotency_key"`
	RequestedBy       string                    `json:"requested_by"`
	TargetDevice      string                    `json:"target_device"`
	DataLevel         string                    `json:"data_level"`
	PermissionCeiling string                    `json:"permission_ceiling"`
	AutoStart         bool                      `json:"auto_start"`
	Steps             []CreateAgentRunStepInput `json:"steps"`
	Planner           string                    `json:"-"`
	PlannerVersion    string                    `json:"-"`
	SourceInstruction string                    `json:"-"`
	PlanSummary       string                    `json:"-"`
	PolicySummary     map[string]any            `json:"-"`
}

type CreateAgentRunStepInput struct {
	Key              string         `json:"key"`
	Title            string         `json:"title"`
	ToolName         string         `json:"tool_name"`
	ToolVersion      string         `json:"tool_version"`
	Arguments        map[string]any `json:"arguments"`
	ExpectedOutput   map[string]any `json:"expected_output"`
	DependsOn        []string       `json:"depends_on"`
	MaxAttempts      int            `json:"max_attempts"`
	TimeoutSeconds   int            `json:"timeout_seconds"`
	ToolIdempotency  string         `json:"tool_idempotency,omitempty"`
	PolicyDecision   string         `json:"policy_decision,omitempty"`
	PolicyReason     string         `json:"policy_reason,omitempty"`
	RequiresApproval bool           `json:"requires_approval"`
}

type ApproveAgentRunInput struct {
	PlanHash      string                               `json:"plan_hash"`
	GrantedBy     string                               `json:"granted_by"`
	Scope         string                               `json:"scope"`
	Reason        string                               `json:"reason"`
	ExpiresAt     *time.Time                           `json:"expires_at"`
	ApprovalProof *privilegebroker.SignedApprovalProof `json:"approval_proof,omitempty"`
}

type RuntimeEvidence struct {
	Kind    string
	Summary string
	Payload map[string]any
}

type RuntimeToolResult struct {
	Output   map[string]any
	Evidence []RuntimeEvidence
}

type RuntimeTool interface {
	Spec() domain.StewardToolSpec
	Execute(context.Context, map[string]any) (RuntimeToolResult, error)
	Verify(context.Context, map[string]any, map[string]any, map[string]any) error
}

type runtimeToolRegistry struct {
	mu         sync.RWMutex
	tools      map[string]RuntimeTool
	generation int64
}

func newRuntimeToolRegistry(tools ...RuntimeTool) *runtimeToolRegistry {
	registry := &runtimeToolRegistry{tools: map[string]RuntimeTool{}}
	for _, tool := range tools {
		registry.register(tool)
	}
	return registry
}

func (r *runtimeToolRegistry) register(tool RuntimeTool) {
	if r == nil || tool == nil {
		return
	}
	name := normalizeRuntimeToolSpec(tool.Spec()).Name
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = tool
	r.generation++
}

func (r *runtimeToolRegistry) registerIfAbsent(tool RuntimeTool) {
	if r == nil || tool == nil {
		return
	}
	name := normalizeRuntimeToolSpec(tool.Spec()).Name
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; !exists {
		r.tools[name] = tool
		r.generation++
	}
}

func (r *runtimeToolRegistry) unregister(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[strings.TrimSpace(name)]; exists {
		delete(r.tools, strings.TrimSpace(name))
		r.generation++
	}
}

func (r *runtimeToolRegistry) generationValue() int64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.generation
}

func (r *runtimeToolRegistry) get(name string) (RuntimeTool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[strings.TrimSpace(name)]
	return tool, ok
}

func (r *runtimeToolRegistry) specs() []domain.StewardToolSpec {
	if r == nil {
		return []domain.StewardToolSpec{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make([]domain.StewardToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, normalizeRuntimeToolSpec(tool.Spec()))
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

type runtimeEchoTool struct{}

func newRuntimeEchoTool() RuntimeTool { return runtimeEchoTool{} }

func (runtimeEchoTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "runtime.echo", Version: "1.0.0",
		Description: "Deterministically echoes the supplied value for execution-kernel verification.",
		InputSchema: map[string]any{
			"type": "object", "required": []string{"value"},
			"properties": map[string]any{"value": map[string]any{}},
		},
		OutputSchema: map[string]any{
			"type": "object", "required": []string{"value"},
			"properties": map[string]any{"value": map[string]any{}},
		},
		PermissionLevel: PermissionA0, RiskLevel: "low", Deterministic: true,
		SideEffect: RuntimeSideEffectNone, ApprovalMode: RuntimeApprovalNever,
		IdempotencyMode: RuntimeIdempotencyInherent,
		SupportsCancel:  true, DefaultTimeoutSec: 10,
	}
}

func (runtimeEchoTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	select {
	case <-ctx.Done():
		return RuntimeToolResult{}, ctx.Err()
	default:
	}
	value, ok := input["value"]
	if !ok {
		return RuntimeToolResult{}, fmt.Errorf("runtime.echo requires arguments.value")
	}
	output := map[string]any{"value": value}
	return RuntimeToolResult{
		Output:   output,
		Evidence: []RuntimeEvidence{{Kind: "tool_output", Summary: "runtime.echo returned its input", Payload: output}},
	}, nil
}

func (runtimeEchoTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if _, ok := output["value"]; !ok {
		return fmt.Errorf("runtime.echo output is missing value")
	}
	if len(expected) > 0 && !reflect.DeepEqual(output, expected) {
		return fmt.Errorf("verified output does not match expected_output")
	}
	return nil
}

var runtimeStepKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func (s *Service) runtimeEnabled() error {
	if s == nil || !s.runtimeV2 {
		return ErrRuntimeV2Disabled
	}
	if s.db == nil || s.db.Pool == nil {
		return fmt.Errorf("%w: database is not configured", ErrRuntimeV2Disabled)
	}
	return nil
}

func (s *Service) normalizeAgentRunInput(input CreateAgentRunInput) (CreateAgentRunInput, string, error) {
	input.Goal = strings.TrimSpace(input.Goal)
	if input.Goal == "" || len([]rune(input.Goal)) > 2000 {
		return input, "", fmt.Errorf("%w: goal is required and must not exceed 2000 characters", ErrAgentRunInvalid)
	}
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.Mode == "" {
		input.Mode = "manual"
	}
	if input.Mode != "manual" && input.Mode != "planned" {
		return input, "", fmt.Errorf("%w: mode must be manual or planned", ErrAgentRunInvalid)
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if len(input.IdempotencyKey) > 200 {
		return input, "", fmt.Errorf("%w: idempotency_key must not exceed 200 characters", ErrAgentRunInvalid)
	}
	input.RequestedBy = defaultString(strings.TrimSpace(input.RequestedBy), "local-user")
	input.TargetDevice = defaultString(strings.TrimSpace(input.TargetDevice), "local")
	input.DataLevel = strings.ToUpper(defaultString(strings.TrimSpace(input.DataLevel), DataD0))
	if !validRuntimeDataLevel(input.DataLevel) {
		return input, "", fmt.Errorf("%w: invalid data_level", ErrAgentRunInvalid)
	}
	input.PermissionCeiling = strings.ToUpper(defaultString(strings.TrimSpace(input.PermissionCeiling), PermissionA0))
	if !validRuntimePermission(input.PermissionCeiling) {
		return input, "", fmt.Errorf("%w: invalid permission_ceiling", ErrAgentRunInvalid)
	}
	if len(input.Steps) == 0 || len(input.Steps) > 100 {
		return input, "", fmt.Errorf("%w: steps must contain between 1 and 100 entries", ErrAgentRunInvalid)
	}
	input.Planner = defaultString(strings.TrimSpace(input.Planner), "manual")
	input.PlannerVersion = defaultString(strings.TrimSpace(input.PlannerVersion), "1.0.0")
	input.SourceInstruction = strings.TrimSpace(input.SourceInstruction)
	input.PlanSummary = strings.TrimSpace(input.PlanSummary)
	if input.PolicySummary == nil {
		input.PolicySummary = map[string]any{}
	}
	policy := s.runtimePolicy
	if policy == nil {
		policy = newDefaultRuntimePolicyEngine()
	}
	seen := map[string]bool{}
	for index := range input.Steps {
		step := &input.Steps[index]
		step.Key = strings.TrimSpace(step.Key)
		step.Title = strings.TrimSpace(step.Title)
		step.ToolName = strings.TrimSpace(step.ToolName)
		step.ToolVersion = strings.TrimSpace(step.ToolVersion)
		if !runtimeStepKeyPattern.MatchString(step.Key) {
			return input, "", fmt.Errorf("%w: invalid step key %q", ErrAgentRunInvalid, step.Key)
		}
		if seen[step.Key] {
			return input, "", fmt.Errorf("%w: duplicate step key %q", ErrAgentRunInvalid, step.Key)
		}
		if step.Title == "" {
			step.Title = step.Key
		}
		tool, ok := s.runtimeTools.get(step.ToolName)
		if !ok {
			return input, "", fmt.Errorf("%w: unknown runtime tool %q", ErrAgentRunInvalid, step.ToolName)
		}
		spec := normalizeRuntimeToolSpec(tool.Spec())
		if err := validateRuntimeToolSpec(spec); err != nil {
			return input, "", fmt.Errorf("%w: tool %q has invalid policy metadata: %v", ErrAgentRunInvalid, step.ToolName, err)
		}
		if step.ToolVersion == "" {
			step.ToolVersion = spec.Version
		}
		if step.ToolVersion != spec.Version {
			return input, "", fmt.Errorf("%w: tool %q version %q is unavailable; active version is %q", ErrAgentRunInvalid, spec.Name, step.ToolVersion, spec.Version)
		}
		if step.Arguments == nil {
			step.Arguments = map[string]any{}
		}
		if step.ExpectedOutput == nil {
			step.ExpectedOutput = map[string]any{}
		}
		if validator, ok := tool.(RuntimeToolValidator); ok {
			if err := validator.Validate(step.Arguments); err != nil {
				return input, "", fmt.Errorf("%w: tool %q preflight failed: %w", ErrRuntimeToolInput, spec.Name, err)
			}
		}
		decision := policy.Evaluate(spec, step.Arguments, input.PermissionCeiling)
		if decision.Decision == RuntimePolicyDeny {
			return input, "", fmt.Errorf("%w: %s", ErrRuntimePolicyDenied, decision.Reason)
		}
		step.ToolIdempotency = spec.IdempotencyMode
		step.PolicyDecision = decision.Decision
		step.PolicyReason = decision.Reason
		step.RequiresApproval = step.RequiresApproval || decision.RequiresApproval
		if step.MaxAttempts == 0 {
			step.MaxAttempts = 1
		}
		if decision.MaxAttempts > 0 && step.MaxAttempts > decision.MaxAttempts {
			step.MaxAttempts = decision.MaxAttempts
		}
		if step.MaxAttempts < 1 || step.MaxAttempts > 10 {
			return input, "", fmt.Errorf("%w: step %q max_attempts must be between 1 and 10", ErrAgentRunInvalid, step.Key)
		}
		if step.TimeoutSeconds == 0 {
			step.TimeoutSeconds = spec.DefaultTimeoutSec
		}
		if step.TimeoutSeconds < 1 || step.TimeoutSeconds > 3600 {
			return input, "", fmt.Errorf("%w: step %q timeout_seconds must be between 1 and 3600", ErrAgentRunInvalid, step.Key)
		}
		for depIndex, dependency := range step.DependsOn {
			dependency = strings.TrimSpace(dependency)
			step.DependsOn[depIndex] = dependency
			if dependency == step.Key || !seen[dependency] {
				return input, "", fmt.Errorf("%w: step %q dependency %q must reference an earlier step", ErrAgentRunInvalid, step.Key, dependency)
			}
		}
		seen[step.Key] = true
	}
	input.PolicySummary = summarizeRuntimePolicy(input.Steps)
	canonical := struct {
		Goal              string                    `json:"goal"`
		Mode              string                    `json:"mode"`
		TargetDevice      string                    `json:"target_device"`
		DataLevel         string                    `json:"data_level"`
		PermissionCeiling string                    `json:"permission_ceiling"`
		Planner           string                    `json:"planner"`
		PlannerVersion    string                    `json:"planner_version"`
		Steps             []CreateAgentRunStepInput `json:"steps"`
	}{input.Goal, input.Mode, input.TargetDevice, input.DataLevel, input.PermissionCeiling, input.Planner, input.PlannerVersion, input.Steps}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return input, "", fmt.Errorf("%w: encode plan: %v", ErrAgentRunInvalid, err)
	}
	digest := sha256.Sum256(payload)
	return input, hex.EncodeToString(digest[:]), nil
}

func summarizeRuntimePolicy(steps []CreateAgentRunStepInput) map[string]any {
	tools := make([]string, 0, len(steps))
	approvalCount := 0
	nonIdempotentCount := 0
	for _, step := range steps {
		tools = append(tools, step.ToolName+"@"+step.ToolVersion)
		if step.RequiresApproval {
			approvalCount++
		}
		if step.ToolIdempotency == RuntimeIdempotencyNonIdempotent {
			nonIdempotentCount++
		}
	}
	return map[string]any{
		"step_count": len(steps), "approval_step_count": approvalCount,
		"non_idempotent_step_count": nonIdempotentCount, "tools": tools,
	}
}

func validRuntimePermission(value string) bool {
	return len(value) == 2 && value[0] == 'A' && value[1] >= '0' && value[1] <= '9'
}

func validRuntimeDataLevel(value string) bool {
	return len(value) == 2 && value[0] == 'D' && value[1] >= '0' && value[1] <= '6'
}

func runtimeRunTerminal(status string) bool {
	switch status {
	case RuntimeRunSucceeded, RuntimeRunFailed, RuntimeRunCancelled, RuntimeRunBlocked:
		return true
	default:
		return false
	}
}
