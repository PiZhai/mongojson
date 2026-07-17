package steward

import (
	"errors"
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

const (
	RuntimeSideEffectNone    = "none"
	RuntimeSideEffectWrite   = "write"
	RuntimeSideEffectProcess = "process"
	RuntimeSideEffectLaunch  = "launch"
	RuntimeSideEffectNetwork = "network"

	RuntimeApprovalNever  = "never"
	RuntimeApprovalAlways = "always"

	RuntimeIdempotencyInherent      = "inherent"
	RuntimeIdempotencyKeyed         = "keyed"
	RuntimeIdempotencyNonIdempotent = "non_idempotent"

	RuntimePolicyAllow    = "allow"
	RuntimePolicyApproval = "approval"
	RuntimePolicyDeny     = "deny"
)

var (
	ErrRuntimeR2Disabled    = errors.New("steward runtime r2 is disabled")
	ErrRuntimePolicyDenied  = errors.New("runtime policy denied plan")
	ErrRuntimeToolInput     = errors.New("invalid runtime tool input")
	ErrRuntimePathDenied    = errors.New("runtime path is outside allowed roots")
	ErrRuntimeCommandDenied = errors.New("runtime executable is not allowlisted")
)

type RuntimeToolValidator interface {
	Validate(map[string]any) error
}

type RuntimePolicyDecision struct {
	Decision           string `json:"decision"`
	Reason             string `json:"reason"`
	RequiredPermission string `json:"required_permission"`
	RequiresApproval   bool   `json:"requires_approval"`
	MaxAttempts        int    `json:"max_attempts"`
}

type RuntimePolicyEngine interface {
	Evaluate(domain.StewardToolSpec, map[string]any, string) RuntimePolicyDecision
}

type defaultRuntimePolicyEngine struct{}

func newDefaultRuntimePolicyEngine() RuntimePolicyEngine { return defaultRuntimePolicyEngine{} }

func (defaultRuntimePolicyEngine) Evaluate(rawSpec domain.StewardToolSpec, _ map[string]any, permissionCeiling string) RuntimePolicyDecision {
	spec := normalizeRuntimeToolSpec(rawSpec)
	decision := RuntimePolicyDecision{
		Decision:           RuntimePolicyAllow,
		Reason:             "read-only low-risk tool is allowed within the declared permission ceiling",
		RequiredPermission: spec.PermissionLevel,
		MaxAttempts:        10,
	}
	if ownerModeEnabled() {
		if spec.IdempotencyMode == RuntimeIdempotencyNonIdempotent {
			decision.MaxAttempts = 1
		}
		decision.Reason = "device owner mode grants the tool full local execution access"
		return decision
	}
	if !validRuntimePermission(spec.PermissionLevel) || permissionRank(spec.PermissionLevel) > permissionRank(permissionCeiling) {
		decision.Decision = RuntimePolicyDeny
		decision.Reason = fmt.Sprintf("tool %s requires %s above permission ceiling %s", spec.Name, spec.PermissionLevel, permissionCeiling)
		decision.MaxAttempts = 0
		return decision
	}
	if spec.IdempotencyMode == RuntimeIdempotencyNonIdempotent {
		decision.MaxAttempts = 1
	}
	if spec.ApprovalMode == RuntimeApprovalAlways || spec.SideEffect != RuntimeSideEffectNone || !strings.EqualFold(spec.RiskLevel, "low") {
		decision.Decision = RuntimePolicyApproval
		decision.RequiresApproval = true
		decision.Reason = fmt.Sprintf("tool %s has side_effect=%s risk=%s idempotency=%s and requires plan-bound approval",
			spec.Name, spec.SideEffect, spec.RiskLevel, spec.IdempotencyMode)
	}
	return decision
}

func normalizeRuntimeToolSpec(spec domain.StewardToolSpec) domain.StewardToolSpec {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Version = defaultString(strings.TrimSpace(spec.Version), "1.0.0")
	spec.PermissionLevel = strings.ToUpper(defaultString(strings.TrimSpace(spec.PermissionLevel), PermissionA0))
	spec.RiskLevel = strings.ToLower(defaultString(strings.TrimSpace(spec.RiskLevel), "low"))
	spec.SideEffect = strings.ToLower(defaultString(strings.TrimSpace(spec.SideEffect), RuntimeSideEffectNone))
	spec.ApprovalMode = strings.ToLower(defaultString(strings.TrimSpace(spec.ApprovalMode), RuntimeApprovalNever))
	spec.IdempotencyMode = strings.ToLower(defaultString(strings.TrimSpace(spec.IdempotencyMode), RuntimeIdempotencyInherent))
	if spec.DefaultTimeoutSec <= 0 {
		spec.DefaultTimeoutSec = 30
	}
	return spec
}

func validateRuntimeToolSpec(spec domain.StewardToolSpec) error {
	spec = normalizeRuntimeToolSpec(spec)
	if spec.Name == "" || !validRuntimePermission(spec.PermissionLevel) {
		return fmt.Errorf("invalid runtime tool name or permission")
	}
	switch spec.SideEffect {
	case RuntimeSideEffectNone, RuntimeSideEffectWrite, RuntimeSideEffectProcess, RuntimeSideEffectLaunch, RuntimeSideEffectNetwork:
	default:
		return fmt.Errorf("invalid side_effect %q", spec.SideEffect)
	}
	switch spec.ApprovalMode {
	case RuntimeApprovalNever, RuntimeApprovalAlways:
	default:
		return fmt.Errorf("invalid approval_mode %q", spec.ApprovalMode)
	}
	switch spec.IdempotencyMode {
	case RuntimeIdempotencyInherent, RuntimeIdempotencyKeyed, RuntimeIdempotencyNonIdempotent:
	default:
		return fmt.Errorf("invalid idempotency_mode %q", spec.IdempotencyMode)
	}
	if spec.SideEffect != RuntimeSideEffectNone && spec.ApprovalMode != RuntimeApprovalAlways {
		return fmt.Errorf("side-effecting tool %s must use approval_mode=always", spec.Name)
	}
	return nil
}
