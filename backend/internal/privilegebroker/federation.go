package privilegebroker

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleDelegation(response http.ResponseWriter, _ *http.Request, body []byte) {
	var input BrokerDelegationRequest
	if err := decodeStrictJSON(body, &input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_delegation", err.Error())
		return
	}
	delegation, err := s.issueDelegation(input)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, errEmergencyStopped) {
			status = http.StatusLocked
		}
		writeBrokerError(response, status, "delegation_denied", err.Error())
		return
	}
	writeBrokerJSON(response, http.StatusCreated, delegation)
}

func (s *Server) issueDelegation(input BrokerDelegationRequest) (SignedBrokerDelegation, error) {
	input.TargetDeviceID = strings.TrimSpace(input.TargetDeviceID)
	input.Capability = strings.ToLower(strings.TrimSpace(input.Capability))
	input.Subject = strings.TrimSpace(input.Subject)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.ApprovalRef = strings.TrimSpace(input.ApprovalRef)
	credentials, err := normalizePolicyNames(input.CredentialRefs, credentialIDPattern, "credential")
	if err != nil || input.TargetDeviceID == "" || !capabilityNamePattern.MatchString(input.Capability) ||
		input.Subject == "" || len(input.Subject) > 200 || !hexDigestPattern.MatchString(input.PlanHash) ||
		input.ApprovalRef != input.ApprovalProof.Claims.ProofID {
		return SignedBrokerDelegation{}, fmt.Errorf("delegation request bindings are invalid")
	}
	state := s.currentState()
	if state.Stopped {
		return SignedBrokerDelegation{}, errEmergencyStopped
	}
	if input.ControlGeneration != state.Generation {
		return SignedBrokerDelegation{}, errGenerationMismatch
	}
	peer, ok := s.policy.BrokerPeer(input.TargetDeviceID)
	if !ok || !containsString(peer.AllowedCapabilities, input.Capability) || !allAllowed(credentials, peer.AllowedCredentials) {
		return SignedBrokerDelegation{}, fmt.Errorf("target broker is not trusted for the requested capability or credentials")
	}
	targetKey, err := decodePublicKey(peer.PublicKey)
	if err != nil || input.TargetStatus.KeyID != peer.keyID || input.TargetStatus.PublicKey != peer.PublicKey || input.TargetStatus.Version != APIVersion {
		return SignedBrokerDelegation{}, fmt.Errorf("target broker identity does not match federation policy")
	}
	unsignedStatus := input.TargetStatus
	unsignedStatus.Signature = ""
	if err := verifyValue(targetKey, unsignedStatus, input.TargetStatus.Signature); err != nil {
		return SignedBrokerDelegation{}, fmt.Errorf("verify target broker status: %w", err)
	}
	now := s.now()
	if input.TargetStatus.Stopped || input.TargetStatus.IssuedAt.Before(now.Add(-time.Minute)) || input.TargetStatus.IssuedAt.After(now.Add(time.Minute)) {
		return SignedBrokerDelegation{}, fmt.Errorf("target broker status is stopped or stale")
	}
	var targetCapability PublicCapability
	for _, candidate := range input.TargetStatus.Capabilities {
		if candidate.Name == input.Capability {
			targetCapability = candidate
			break
		}
	}
	if targetCapability.Name == "" || !allAllowed(credentials, targetCapability.CredentialIDs) ||
		(targetCapability.PermissionLevel != "A4" && targetCapability.PermissionLevel != "A5" && targetCapability.PermissionLevel != "A6" && targetCapability.PermissionLevel != "A7") {
		return SignedBrokerDelegation{}, fmt.Errorf("target capability metadata does not authorize the requested execution")
	}
	if err := VerifyApprovalProof(s.policy.PublicApprovalAuthorities(), input.ApprovalProof, ApprovalProofExpectation{
		Subject: input.Subject, PlanHash: input.PlanHash, Capability: input.Capability, ControlGeneration: input.ControlGeneration,
	}, now); err != nil {
		return SignedBrokerDelegation{}, err
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if _, used := s.consumedApprovals[input.ApprovalRef]; used {
		return SignedBrokerDelegation{}, fmt.Errorf("approval proof was already consumed")
	}
	delegationID, err := randomID()
	if err != nil {
		return SignedBrokerDelegation{}, err
	}
	expiresAt := input.ApprovalProof.Claims.ExpiresAt
	claims := BrokerDelegationClaims{
		Version: DelegationVersion, DelegationID: delegationID,
		OriginBrokerPublicKey: base64.StdEncoding.EncodeToString(s.publicKey), OriginBrokerKeyID: s.keyID,
		OriginBrokerInstanceID: s.instanceID, OriginDeviceID: s.deviceID, OriginControlGeneration: state.Generation,
		TargetDeviceID: input.TargetDeviceID, TargetBrokerKeyID: input.TargetStatus.KeyID,
		TargetBrokerInstanceID: input.TargetStatus.InstanceID, TargetPolicyDigest: input.TargetStatus.PolicyDigest,
		TargetControlGeneration: input.TargetStatus.Generation, Capability: input.Capability,
		CapabilityDigest: targetCapability.CapabilityDigest, CredentialRefs: credentials,
		Subject: input.Subject, PlanHash: input.PlanHash, ApprovalRef: input.ApprovalRef,
		ApprovalProofID: input.ApprovalProof.Claims.ProofID, ApprovalKeyID: input.ApprovalProof.KeyID,
		ApprovalExpiresAt: input.ApprovalProof.Claims.ExpiresAt, IssuedAt: now, ExpiresAt: expiresAt,
	}
	if claims.OriginDeviceID == "" || !expiresAt.After(now) {
		return SignedBrokerDelegation{}, fmt.Errorf("broker device identity or delegation lifetime is invalid")
	}
	signature, err := signValue(s.privateKey, claims)
	if err != nil {
		return SignedBrokerDelegation{}, err
	}
	if err := s.audit.Append(AuditRecord{Type: "delegation.issued", Subject: claims.Subject, Capability: claims.Capability,
		Generation: state.Generation, Outcome: "issued", Details: map[string]any{
			"delegation_id": delegationID, "target_device_id": claims.TargetDeviceID, "target_broker_key_id": claims.TargetBrokerKeyID,
			"plan_hash": claims.PlanHash, "approval_proof_id": claims.ApprovalProofID, "approval_key_id": claims.ApprovalKeyID,
			"credential_refs": claims.CredentialRefs, "expires_at": claims.ExpiresAt,
		}}); err != nil {
		return SignedBrokerDelegation{}, err
	}
	s.consumedApprovals[input.ApprovalRef] = struct{}{}
	return SignedBrokerDelegation{Claims: claims, KeyID: s.keyID, Signature: signature}, nil
}

func (s *Server) handleDelegatedExecute(response http.ResponseWriter, request *http.Request, body []byte) {
	var input DelegatedExecuteRequest
	if err := decodeStrictJSON(body, &input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_delegated_execute", err.Error())
		return
	}
	result, err := s.executeDelegation(request.Context(), input.Delegation, input.OriginStatus)
	if err != nil && result.Receipt.Payload.ExecutionID == "" {
		status := http.StatusForbidden
		if errors.Is(err, errTokenReplay) || errors.Is(err, errGenerationMismatch) {
			status = http.StatusConflict
		} else if errors.Is(err, errEmergencyStopped) {
			status = http.StatusLocked
		}
		writeBrokerError(response, status, "delegated_execute_denied", err.Error())
		return
	}
	writeBrokerJSON(response, http.StatusOK, result)
}

func (s *Server) executeDelegation(ctx context.Context, signed SignedBrokerDelegation, originStatus Status) (ExecuteResponse, error) {
	claims := signed.Claims
	now := s.now()
	if claims.Version != DelegationVersion || signed.KeyID != claims.OriginBrokerKeyID || claims.TargetDeviceID != s.deviceID ||
		claims.TargetBrokerKeyID != s.keyID || claims.TargetBrokerInstanceID != s.instanceID || claims.TargetPolicyDigest != s.policy.Digest ||
		!claims.ExpiresAt.After(now) || claims.IssuedAt.After(now.Add(s.requestSkew)) || claims.ApprovalRef != claims.ApprovalProofID {
		return ExecuteResponse{}, fmt.Errorf("delegation target, lifetime, or approval binding is invalid")
	}
	state := s.currentState()
	if state.Stopped {
		return ExecuteResponse{}, errEmergencyStopped
	}
	if state.Generation != claims.TargetControlGeneration {
		return ExecuteResponse{}, errGenerationMismatch
	}
	peer, ok := s.policy.BrokerPeer(claims.OriginDeviceID)
	if !ok || peer.keyID != claims.OriginBrokerKeyID || peer.PublicKey != claims.OriginBrokerPublicKey ||
		!containsString(peer.AllowedCapabilities, claims.Capability) || !allAllowed(claims.CredentialRefs, peer.AllowedCredentials) {
		return ExecuteResponse{}, fmt.Errorf("origin broker is not trusted for the delegated scope")
	}
	originKey, err := decodePublicKey(peer.PublicKey)
	if err != nil || !ed25519.Verify(originKey, mustJSON(claims), decodeSignature(signed.Signature)) {
		return ExecuteResponse{}, fmt.Errorf("origin broker delegation signature is invalid")
	}
	if originStatus.Version != APIVersion || originStatus.KeyID != claims.OriginBrokerKeyID ||
		originStatus.PublicKey != claims.OriginBrokerPublicKey || originStatus.InstanceID != claims.OriginBrokerInstanceID ||
		originStatus.Generation != claims.OriginControlGeneration || originStatus.Stopped ||
		originStatus.IssuedAt.Before(now.Add(-s.requestSkew)) || originStatus.IssuedAt.After(now.Add(s.requestSkew)) {
		return ExecuteResponse{}, fmt.Errorf("origin Broker online control status is stopped, stale, or mismatched")
	}
	unsignedOriginStatus := originStatus
	unsignedOriginStatus.Signature = ""
	if err := verifyValue(originKey, unsignedOriginStatus, originStatus.Signature); err != nil {
		return ExecuteResponse{}, fmt.Errorf("verify origin Broker online status: %w", err)
	}
	capability, ok := s.policy.Capability(claims.Capability)
	if !ok || capability.digest != claims.CapabilityDigest || !allAllowed(claims.CredentialRefs, capability.CredentialIDs) {
		return ExecuteResponse{}, fmt.Errorf("local capability or credential policy changed after delegation")
	}
	for _, credentialID := range claims.CredentialRefs {
		if _, ok := s.policy.Credential(credentialID); !ok {
			return ExecuteResponse{}, fmt.Errorf("credential %q is unavailable", credentialID)
		}
	}
	tokenClaims := CapabilityTokenClaims{
		TokenID: claims.DelegationID, BrokerInstanceID: s.instanceID, Capability: claims.Capability,
		CapabilityDigest: claims.CapabilityDigest, Subject: claims.Subject, PlanHash: claims.PlanHash,
		ApprovalRef: claims.ApprovalRef, ApprovalProofID: claims.ApprovalProofID, ApprovalKeyID: claims.ApprovalKeyID,
		ApprovalExpiresAt: claims.ApprovalExpiresAt, ControlGeneration: claims.TargetControlGeneration,
		IssuedAt: claims.IssuedAt, ExpiresAt: claims.ExpiresAt, DelegationID: claims.DelegationID,
		OriginBrokerKeyID: claims.OriginBrokerKeyID, CredentialRefs: append([]string(nil), claims.CredentialRefs...),
	}
	if err := s.audit.Append(AuditRecord{Type: "delegation.accepted", Subject: claims.Subject, Capability: claims.Capability,
		Generation: state.Generation, Outcome: "accepted", Details: map[string]any{
			"delegation_id": claims.DelegationID, "origin_device_id": claims.OriginDeviceID,
			"origin_broker_key_id": claims.OriginBrokerKeyID, "credential_refs": claims.CredentialRefs,
		}}); err != nil {
		return ExecuteResponse{}, err
	}
	return s.executeCapabilityWithCredentialRefs(ctx, capability, tokenClaims, claims.CredentialRefs)
}

func (s *Server) readCredentials(refs []string) (map[string]string, error) {
	values := make(map[string]string, len(refs))
	for _, id := range refs {
		credential, ok := s.policy.Credential(id)
		if !ok {
			return nil, fmt.Errorf("credential %q is unavailable", id)
		}
		if err := credentialPathSecurityValidator(credential.Path); err != nil {
			return nil, fmt.Errorf("credential %q protection changed: %w", id, err)
		}
		payload, err := os.ReadFile(credential.Path)
		if err != nil || len(payload) == 0 || len(payload) > credential.MaxBytes {
			return nil, fmt.Errorf("read protected credential %q", id)
		}
		values[id] = string(payload)
	}
	return values, nil
}

func allAllowed(values, allowed []string) bool {
	for _, value := range values {
		if !containsString(allowed, value) {
			return false
		}
	}
	return true
}

func containsString(values []string, expected string) bool {
	index := sort.SearchStrings(values, expected)
	return index < len(values) && values[index] == expected
}

func mustJSON(value any) []byte {
	payload, _ := json.Marshal(value)
	return payload
}

func decodeSignature(value string) []byte {
	payload, _ := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	return payload
}
