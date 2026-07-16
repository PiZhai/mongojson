package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

type PrivilegeBrokerClient interface {
	Status(context.Context) (privilegebroker.Status, error)
	Capability(context.Context, string) (privilegebroker.PublicCapability, error)
	ExecuteCapability(context.Context, privilegebroker.Authorization) (privilegebroker.ExecuteResponse, error)
	SetControl(context.Context, bool, privilegebroker.ControlRequest) (privilegebroker.Status, error)
}

type PrivilegeBrokerFederationClient interface {
	IssueDelegation(context.Context, privilegebroker.BrokerDelegationRequest) (privilegebroker.SignedBrokerDelegation, error)
	ExecuteDelegation(context.Context, privilegebroker.SignedBrokerDelegation, privilegebroker.Status) (privilegebroker.ExecuteResponse, error)
}

func (s *Service) RemoteBrokerStatus(ctx context.Context) (privilegebroker.Status, error) {
	if s == nil || !s.runtimeR3 || s.privilegeBroker == nil {
		return privilegebroker.Status{}, fmt.Errorf("local privilege broker is not configured")
	}
	if s.privilegeBrokerError != nil {
		return privilegebroker.Status{}, s.privilegeBrokerError
	}
	return s.privilegeBroker.Status(ctx)
}

func newPrivilegeBrokerClientFromEnv() (PrivilegeBrokerClient, error) {
	return privilegebroker.NewClientFromEnv()
}

type runtimeExecutionAuthorization struct {
	RunID             string
	PlanHash          string
	ApprovalRef       string
	ApprovalProof     privilegebroker.SignedApprovalProof
	RequestedBy       string
	ControlGeneration int64
}

type runtimeAuthorizationContextKey struct{}

func withRuntimeExecutionAuthorization(ctx context.Context, authorization runtimeExecutionAuthorization) context.Context {
	return context.WithValue(ctx, runtimeAuthorizationContextKey{}, authorization)
}

func runtimeExecutionAuthorizationFromContext(ctx context.Context) (runtimeExecutionAuthorization, bool) {
	authorization, ok := ctx.Value(runtimeAuthorizationContextKey{}).(runtimeExecutionAuthorization)
	return authorization, ok
}

type runtimePrivilegeBrokerTool struct{ service *Service }

func newRuntimePrivilegeBrokerTool(service *Service) RuntimeTool {
	return runtimePrivilegeBrokerTool{service: service}
}

func (runtimePrivilegeBrokerTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "privilege.execute", Version: "3.1.0",
		Description: "Execute one fixed A4-A7 capability through the isolated Privilege Broker. The main process never receives an executable path or dynamic arguments.",
		InputSchema: map[string]any{"type": "object", "required": []string{"capability"}, "properties": map[string]any{
			"capability": map[string]any{"type": "string"},
		}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"capability", "exit_code", "receipt"}},
		PermissionLevel: PermissionA7, RiskLevel: "critical", SideEffect: RuntimeSideEffectProcess,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyNonIdempotent,
		Deterministic: false, SupportsCancel: true, DefaultTimeoutSec: 120,
	}
}

func (t runtimePrivilegeBrokerTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "capability"); err != nil {
		return err
	}
	capability, err := runtimeRequiredString(input, "capability")
	if err != nil {
		return err
	}
	if !configuredToolActionPattern.MatchString(strings.ToLower(capability)) {
		return fmt.Errorf("capability must use the registered tool:<name> form")
	}
	if t.service == nil || !t.service.runtimeR3 {
		return fmt.Errorf("R3 privilege broker execution is disabled")
	}
	if t.service.privilegeBrokerError != nil {
		return t.service.privilegeBrokerError
	}
	if t.service.privilegeBroker == nil {
		return fmt.Errorf("privilege broker client is not configured")
	}
	return nil
}

func (t runtimePrivilegeBrokerTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	authorization, ok := runtimeExecutionAuthorizationFromContext(ctx)
	if !ok || authorization.PlanHash == "" || authorization.ApprovalRef == "" || authorization.ApprovalProof.Claims.ProofID == "" {
		return RuntimeToolResult{}, fmt.Errorf("privilege execution requires an active independently signed plan-bound approval")
	}
	capabilityName, _ := runtimeRequiredString(input, "capability")
	capabilityName = strings.ToLower(capabilityName)
	capability, err := t.service.privilegeBroker.Capability(ctx, capabilityName)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	if permissionRank(capability.PermissionLevel) > permissionRank(PermissionA7) {
		return RuntimeToolResult{}, fmt.Errorf("R3.0 does not authorize A8-A9 credential capabilities")
	}
	response, err := t.service.privilegeBroker.ExecuteCapability(ctx, privilegebroker.Authorization{
		Capability: capabilityName, Subject: "runtime:" + authorization.RunID,
		PlanHash: authorization.PlanHash, ApprovalRef: authorization.ApprovalRef,
		ApprovalProof:     authorization.ApprovalProof,
		ControlGeneration: authorization.ControlGeneration,
	})
	if err != nil {
		var executionErr *privilegebroker.ExecutionError
		if errors.As(err, &executionErr) && executionErr.Response.Receipt.Payload.ExecutionID != "" {
			return runtimePrivilegeBrokerResult(capabilityName, executionErr.Response), err
		}
		return RuntimeToolResult{}, err
	}
	return runtimePrivilegeBrokerResult(capabilityName, response), nil
}

func runtimePrivilegeBrokerResult(capabilityName string, response privilegebroker.ExecuteResponse) RuntimeToolResult {
	receipt := response.Receipt.Payload
	output := map[string]any{
		"capability": capabilityName, "exit_code": receipt.ExitCode,
		"stdout": response.Stdout, "stderr": response.Stderr,
		"stdout_sha256": receipt.StdoutSHA256, "stderr_sha256": receipt.StderrSHA256,
		"receipt": response.Receipt,
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{
		Kind: "privilege_broker_receipt", Summary: "isolated broker returned a signed execution receipt", Payload: map[string]any{
			"capability": capabilityName, "receipt": response.Receipt,
		},
	}}}
}

func (runtimePrivilegeBrokerTool) Verify(_ context.Context, input map[string]any, output map[string]any, expected map[string]any) error {
	capability, _ := runtimeRequiredString(input, "capability")
	if output["capability"] != strings.ToLower(capability) || fmt.Sprint(output["exit_code"]) != "0" {
		return fmt.Errorf("privilege broker output does not match the approved capability or successful exit")
	}
	if output["receipt"] == nil {
		return fmt.Errorf("privilege broker output is missing its signed receipt")
	}
	remaining := make(map[string]any, len(expected))
	for key, value := range expected {
		remaining[key] = value
	}
	if contains, ok := remaining["stdout_contains"].(string); ok {
		stdout, _ := output["stdout"].(string)
		if !strings.Contains(stdout, contains) {
			return fmt.Errorf("broker stdout does not contain expected text")
		}
		delete(remaining, "stdout_contains")
	}
	return runtimeOutputMatchesExpected(output, remaining)
}

func (s *Service) privilegeBrokerStatus(ctx context.Context) domain.StewardPrivilegeBrokerStatus {
	status := domain.StewardPrivilegeBrokerStatus{
		Configured: s != nil && s.runtimeR3, Capabilities: []domain.StewardPrivilegeBrokerCapability{},
		ApprovalAuthorities: []domain.StewardApprovalAuthority{},
	}
	if !status.Configured {
		return status
	}
	if s.privilegeBrokerError != nil {
		status.Error = s.privilegeBrokerError.Error()
		return status
	}
	if s.privilegeBroker == nil {
		status.Error = "privilege broker client is not configured"
		return status
	}
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	remote, err := s.privilegeBroker.Status(statusCtx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Reachable = true
	status.Stopped = remote.Stopped
	status.Generation = remote.Generation
	status.InstanceID = remote.InstanceID
	status.PolicyDigest = remote.PolicyDigest
	status.KeyID = remote.KeyID
	status.CapabilityCount = len(remote.Capabilities)
	status.ActiveExecutions = remote.ActiveExecutions
	status.ApprovalProofRequired = len(remote.ApprovalAuthorities) > 0
	for _, authority := range remote.ApprovalAuthorities {
		status.ApprovalAuthorities = append(status.ApprovalAuthorities, domain.StewardApprovalAuthority{
			Name: authority.Name, Algorithm: authority.Algorithm, KeyID: authority.KeyID,
			CredentialID: authority.CredentialID, RPID: authority.RPID,
			AllowedOrigins: append([]string(nil), authority.AllowedOrigins...),
		})
	}
	for _, capability := range remote.Capabilities {
		status.Capabilities = append(status.Capabilities, domain.StewardPrivilegeBrokerCapability{
			Name: capability.Name, Description: capability.Description,
			PermissionLevel: capability.PermissionLevel, RiskLevel: capability.RiskLevel,
			ExecutableName: capability.ExecutableName, ArgumentCount: capability.ArgumentCount,
			TimeoutSeconds: capability.TimeoutSeconds, CapabilityDigest: capability.CapabilityDigest,
		})
	}
	return status
}

func autonomyBrokerPlanHash(proposal domain.StewardAutonomyProposal) string {
	canonical := struct {
		ID              string `json:"id"`
		Action          string `json:"action"`
		PermissionLevel string `json:"permission_level"`
		RiskLevel       string `json:"risk_level"`
		DataLevel       string `json:"data_level"`
		ImpactSummary   string `json:"impact_summary"`
	}{proposal.ID, proposal.Action, proposal.PermissionLevel, proposal.RiskLevel, proposal.DataLevel, proposal.ImpactSummary}
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func brokerExecutionErrorCode(err error) string {
	var brokerErr *privilegebroker.BrokerError
	if errors.As(err, &brokerErr) {
		return brokerErr.Code
	}
	return "broker_unavailable"
}
