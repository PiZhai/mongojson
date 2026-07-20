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
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

const remoteExecutionProtocolVersion = "steward-r4.4/v1"

type RemoteExecutionDispatchPayload struct {
	Version           string                                  `json:"version"`
	DispatchID        string                                  `json:"dispatch_id"`
	OriginDeviceID    string                                  `json:"origin_device_id"`
	TargetDeviceID    string                                  `json:"target_device_id"`
	OrchestrationID   string                                  `json:"orchestration_id"`
	NodeID            string                                  `json:"node_id"`
	AgentID           string                                  `json:"agent_id"`
	Goal              string                                  `json:"goal"`
	PlanHash          string                                  `json:"plan_hash"`
	PermissionCeiling string                                  `json:"permission_ceiling"`
	DataLevel         string                                  `json:"data_level"`
	ControlGeneration int64                                   `json:"control_generation"`
	Steps             []CreateAgentRunStepInput               `json:"steps"`
	IssuedAt          time.Time                               `json:"issued_at"`
	ExpiresAt         time.Time                               `json:"expires_at"`
	BrokerDelegation  *privilegebroker.SignedBrokerDelegation `json:"broker_delegation,omitempty"`
}

type RemoteExecutionDispatchEnvelope struct {
	Payload   RemoteExecutionDispatchPayload `json:"payload"`
	Signature string                         `json:"signature"`
}

type RemoteExecutionStatusPayload struct {
	Version           string         `json:"version"`
	DispatchID        string         `json:"dispatch_id"`
	OriginDeviceID    string         `json:"origin_device_id"`
	TargetDeviceID    string         `json:"target_device_id"`
	PlanHash          string         `json:"plan_hash"`
	ControlGeneration int64          `json:"control_generation,omitempty"`
	Status            string         `json:"status"`
	RemoteRunID       string         `json:"remote_run_id"`
	HeartbeatAt       time.Time      `json:"heartbeat_at"`
	LeaseExpiresAt    time.Time      `json:"lease_expires_at"`
	Result            map[string]any `json:"result,omitempty"`
	FailureSummary    string         `json:"failure_summary,omitempty"`
	CompletedAt       *time.Time     `json:"completed_at,omitempty"`
}

type RemoteExecutionStatusEnvelope struct {
	Payload   RemoteExecutionStatusPayload `json:"payload"`
	Signature string                       `json:"signature"`
}

func defaultRemoteExecutionTools() []string {
	configured := splitRuntimeCSV(strings.TrimSpace(envOrDefault("STEWARD_REMOTE_EXECUTION_TOOLS", "runtime.echo")))
	if len(configured) == 0 {
		return []string{"runtime.echo"}
	}
	return configured
}

func (s *Service) remoteExecutionToolAllowed(name string) bool {
	return s != nil && s.remoteExecutionTools[strings.TrimSpace(name)]
}

func (s *Service) remoteExecutionEnabled() error {
	if s == nil || !s.orchestrationRemote {
		return fmt.Errorf("R4.3 remote execution is disabled")
	}
	if err := s.runtimeEnabled(); err != nil {
		return err
	}
	if s.remoteExecutionLease < 5*time.Second || s.remoteExecutionLease > 5*time.Minute {
		return fmt.Errorf("remote execution lease must be between 5 seconds and 5 minutes")
	}
	if len(s.deviceSigningKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("STEWARD_DEVICE_PRIVATE_KEY is required for signed remote execution")
	}
	return nil
}

func remoteStepsRequireBroker(steps []CreateAgentRunStepInput) bool {
	return len(steps) == 1 && strings.TrimSpace(steps[0].ToolName) == "privilege.execute"
}

func (s *Service) validateRemoteExecutionDevice(ctx context.Context, deviceID, permission string, requireBroker bool) error {
	device, err := s.getDevice(ctx, strings.TrimSpace(deviceID))
	if err != nil {
		return fmt.Errorf("device is not registered")
	}
	if device.ID == s.agentIDValue() || device.Role != DeviceRolePeer || device.TrustStatus != DeviceTrusted || !device.SyncEnabled {
		return fmt.Errorf("device is not a trusted enabled peer")
	}
	if strings.TrimSpace(device.PublicKey) == "" || strings.TrimSpace(device.APIBaseURL) == "" {
		return fmt.Errorf("device requires a public key and Peer API address")
	}
	if !ownerModeEnabled() && permissionRank(permission) > permissionRank(device.PermissionLevel) {
		return fmt.Errorf("device permission ceiling %s is below %s", device.PermissionLevel, permission)
	}
	if requireBroker && (device.BrokerPublicKey == "" || device.BrokerKeyID == "") {
		return fmt.Errorf("device requires a pinned Broker identity for R4.4")
	}
	return nil
}

func (s *Service) selectRemoteExecutionDevice(ctx context.Context, selector, permission string, requireBroker bool) (domain.StewardDevice, error) {
	selector = strings.TrimSpace(selector)
	if selector != "auto" {
		if err := s.validateRemoteExecutionDevice(ctx, selector, permission, requireBroker); err != nil {
			return domain.StewardDevice{}, err
		}
		return s.getDevice(ctx, selector)
	}
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return domain.StewardDevice{}, err
	}
	now := time.Now().UTC()
	candidates := make([]domain.StewardDevice, 0, len(devices))
	for _, device := range devices {
		if device.ID == s.agentIDValue() || device.Role != DeviceRolePeer || device.TrustStatus != DeviceTrusted || !device.SyncEnabled ||
			device.PublicKey == "" || device.APIBaseURL == "" || (!ownerModeEnabled() && permissionRank(permission) > permissionRank(device.PermissionLevel)) ||
			(requireBroker && (device.BrokerPublicKey == "" || device.BrokerKeyID == "")) ||
			device.LastSeenAt == nil || now.Sub(device.LastSeenAt.UTC()) > 2*time.Minute {
			continue
		}
		candidates = append(candidates, device)
	}
	if len(candidates) == 0 {
		return domain.StewardDevice{}, fmt.Errorf("no recently seen trusted peer satisfies the remote node policy")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].LastSeenAt.Equal(*candidates[j].LastSeenAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].LastSeenAt.After(*candidates[j].LastSeenAt)
	})
	return candidates[0], nil
}

func remoteExecutionPlanHash(payload RemoteExecutionDispatchPayload) string {
	canonical := struct {
		OriginDeviceID    string                                  `json:"origin_device_id"`
		TargetDeviceID    string                                  `json:"target_device_id"`
		OrchestrationID   string                                  `json:"orchestration_id"`
		NodeID            string                                  `json:"node_id"`
		AgentID           string                                  `json:"agent_id"`
		Goal              string                                  `json:"goal"`
		PermissionCeiling string                                  `json:"permission_ceiling"`
		DataLevel         string                                  `json:"data_level"`
		Steps             []CreateAgentRunStepInput               `json:"steps"`
		BrokerDelegation  *privilegebroker.SignedBrokerDelegation `json:"broker_delegation,omitempty"`
	}{payload.OriginDeviceID, payload.TargetDeviceID, payload.OrchestrationID, payload.NodeID,
		payload.AgentID, payload.Goal, payload.PermissionCeiling, payload.DataLevel, payload.Steps, payload.BrokerDelegation}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func signRemotePayload(privateKey []byte, payload any) string {
	if len(privateKey) != ed25519.PrivateKeySize {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), encoded))
}

func verifyRemotePayload(publicKeyValue, signature string, payload any) error {
	publicKey, err := parseSyncPublicKey(publicKeyValue)
	if err != nil {
		return err
	}
	provided, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(provided) != ed25519.SignatureSize {
		return fmt.Errorf("invalid remote execution signature encoding")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, encoded, provided) {
		return fmt.Errorf("remote execution signature verification failed")
	}
	return nil
}

func (s *Service) dispatchRemoteOrchestrationNodeTx(ctx context.Context, tx pgx.Tx, orchestrationID string, generation int64, deadline *time.Time, node orchestrationScheduleNode, now time.Time) error {
	if err := s.remoteExecutionEnabled(); err != nil {
		return err
	}
	selector := node.TargetDevice
	if node.SelectedDeviceID != "" {
		selector = node.SelectedDeviceID
	}
	requireBroker := remoteStepsRequireBroker(node.Steps)
	device, err := s.selectRemoteExecutionDevice(ctx, selector, node.PermissionCeiling, requireBroker)
	if err != nil {
		return err
	}
	dispatchID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("steward-r43:"+node.ID)).String()
	expiresAt := now.Add(24 * time.Hour)
	if deadline != nil && deadline.Before(expiresAt) {
		expiresAt = *deadline
	}
	payload := RemoteExecutionDispatchPayload{
		Version: remoteExecutionProtocolVersion, DispatchID: dispatchID, OriginDeviceID: s.agentIDValue(),
		TargetDeviceID: device.ID, OrchestrationID: orchestrationID, NodeID: node.ID, AgentID: node.AgentID,
		Goal: node.Goal, PermissionCeiling: node.PermissionCeiling, DataLevel: node.DataLevel,
		ControlGeneration: generation, Steps: node.Steps, IssuedAt: now, ExpiresAt: expiresAt,
	}
	if requireBroker {
		var delegation privilegebroker.SignedBrokerDelegation
		if err := json.Unmarshal(node.RemoteBrokerDelegation, &delegation); err != nil || delegation.Claims.DelegationID == "" {
			return fmt.Errorf("R4.4 Broker delegation is missing or invalid")
		}
		payload.BrokerDelegation = &delegation
	}
	payload.PlanHash = remoteExecutionPlanHash(payload)
	signature := signRemotePayload(s.deviceSigningKey, payload)
	if signature == "" {
		return fmt.Errorf("sign remote execution dispatch")
	}
	encodedPayload, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `
		insert into steward_remote_dispatches (
			id, orchestration_id, node_id, target_device_id, status, plan_hash, payload,
			signature, available_at, created_at, updated_at
		) values ($1,$2,$3,$4,'pending',$5,$6::jsonb,$7,now(),$8,$8)
		on conflict (node_id) do nothing
	`, dispatchID, orchestrationID, node.ID, device.ID, payload.PlanHash, string(encodedPayload), signature, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_orchestration_nodes set status=$2, selected_device_id=$3, updated_at=$4
		where id=$1 and status='pending'
	`, node.ID, OrchestrationNodeDispatched, device.ID, now); err != nil {
		return err
	}
	return appendOrchestrationEvent(ctx, tx, orchestrationID, node.ID, "node.remote_dispatched", OrchestrationNodeDispatched,
		"signed remote dispatch persisted for a trusted peer", map[string]any{
			"dispatch_id": dispatchID, "target_device_id": device.ID, "plan_hash": payload.PlanHash,
		})
}

func (s *Service) AcceptRemoteExecution(ctx context.Context, envelope RemoteExecutionDispatchEnvelope, authenticatedDeviceID string) (RemoteExecutionStatusEnvelope, error) {
	if err := s.remoteExecutionEnabled(); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	payload := envelope.Payload
	now := time.Now().UTC()
	if payload.Version != remoteExecutionProtocolVersion || payload.OriginDeviceID != strings.TrimSpace(authenticatedDeviceID) ||
		payload.TargetDeviceID != s.agentIDValue() || payload.DispatchID == "" || payload.PlanHash != remoteExecutionPlanHash(payload) ||
		!payload.ExpiresAt.After(now) || payload.IssuedAt.After(now.Add(5*time.Minute)) {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("invalid remote execution dispatch envelope")
	}
	highPrivilege := remoteStepsRequireBroker(payload.Steps)
	if !ownerModeEnabled() && (permissionRank(payload.PermissionCeiling) > permissionRank(PermissionA7) || (!highPrivilege && dataLevelRank(payload.DataLevel) > dataLevelRank(DataD2))) {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote execution exceeds its R4.3/R4.4 ceiling")
	}
	origin, err := s.requireAuthorizedSyncDevice(ctx, payload.OriginDeviceID)
	if err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	if err := verifyRemotePayload(origin.PublicKey, envelope.Signature, payload); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	if highPrivilege {
		if payload.BrokerDelegation == nil || len(payload.Steps) != 1 || payload.Steps[0].ToolName != "privilege.execute" || !s.runtimeR3 {
			return RemoteExecutionStatusEnvelope{}, fmt.Errorf("R4.4 high privilege dispatch requires one Broker delegation and one privilege.execute step")
		}
		delegation := payload.BrokerDelegation.Claims
		capability, _ := payload.Steps[0].Arguments["capability"].(string)
		if delegation.TargetDeviceID != s.agentIDValue() || delegation.OriginDeviceID != payload.OriginDeviceID ||
			delegation.Subject != "remote-broker:"+payload.OrchestrationID+":"+payload.NodeID ||
			delegation.Capability != strings.ToLower(strings.TrimSpace(capability)) || !delegation.ExpiresAt.After(now) {
			return RemoteExecutionStatusEnvelope{}, fmt.Errorf("Broker delegation does not match the remote node")
		}
		if _, ok := s.privilegeBroker.(PrivilegeBrokerFederationClient); !ok {
			return RemoteExecutionStatusEnvelope{}, fmt.Errorf("target Broker does not support R4.4 federation")
		}
	} else {
		if payload.BrokerDelegation != nil {
			return RemoteExecutionStatusEnvelope{}, fmt.Errorf("low privilege dispatch must not contain a Broker delegation")
		}
		for _, step := range payload.Steps {
			if !ownerModeEnabled() && !s.remoteExecutionToolAllowed(step.ToolName) {
				return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote tool %q is not allowed on this device", step.ToolName)
			}
			tool, ok := s.runtimeTools.get(step.ToolName)
			if !ok || (!ownerModeEnabled() && permissionRank(tool.Spec().PermissionLevel) > permissionRank(PermissionA2)) {
				return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote tool %q is unavailable or too privileged", step.ToolName)
			}
		}
	}
	var existingPayload []byte
	var existingSignature string
	err = s.db.Pool.QueryRow(ctx, `select payload, signature from steward_remote_inbox where dispatch_id=$1`, payload.DispatchID).Scan(&existingPayload, &existingSignature)
	if err == nil {
		encodedPayload, _ := json.Marshal(payload)
		if string(existingPayload) != string(encodedPayload) || existingSignature != envelope.Signature {
			return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote dispatch idempotency conflict")
		}
		return s.GetRemoteExecutionStatus(ctx, payload.DispatchID, payload.OriginDeviceID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RemoteExecutionStatusEnvelope{}, err
	}
	var runID string
	if !highPrivilege {
		run, err := s.CreateAgentRun(ctx, CreateAgentRunInput{
			Goal: payload.Goal, Mode: "manual", IdempotencyKey: "r43:" + payload.OriginDeviceID + ":" + payload.DispatchID,
			RequestedBy: "remote-device:" + payload.OriginDeviceID, TargetDevice: "local", DataLevel: payload.DataLevel,
			PermissionCeiling: payload.PermissionCeiling, Planner: "r4-remote", PlannerVersion: "4.3.0",
			SourceInstruction: "signed remote dispatch " + payload.DispatchID, PlanSummary: "R4.3 remote node " + payload.NodeID,
			AutoStart: true, Steps: payload.Steps,
		})
		if err != nil {
			return RemoteExecutionStatusEnvelope{}, err
		}
		// The signed, device-authenticated R4 dispatch is the authorization for
		// its A0-A2 child run. This prevents a remote write/fetch from becoming
		// permanently stuck behind a second approval UI on the target device.
		// A4+ work never reaches this branch and still requires Broker proof.
		if run.Status == RuntimeRunAwaitingApproval {
			run, err = s.ApproveAgentRun(ctx, run.ID, ApproveAgentRunInput{
				PlanHash: run.PlanHash, GrantedBy: "remote-device:" + payload.OriginDeviceID,
				Scope: "run", Reason: "authorized by signed R4 remote dispatch",
			})
			if err != nil {
				return RemoteExecutionStatusEnvelope{}, err
			}
		}
		runID = run.ID
	}
	encodedPayload, _ := json.Marshal(payload)
	encodedDelegation := "{}"
	executionKind := "runtime"
	if highPrivilege {
		executionKind = "broker"
		delegationBytes, _ := json.Marshal(payload.BrokerDelegation)
		encodedDelegation = string(delegationBytes)
	}
	leaseExpires := now.Add(s.remoteExecutionLease)
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_remote_inbox (
			dispatch_id, origin_device_id, orchestration_id, node_id, plan_hash, payload,
			signature, status, local_run_id, lease_expires_at, heartbeat_at,
			execution_kind, broker_delegation, created_at, updated_at
		) values ($1,$2,$3,$4,$5,$6::jsonb,$7,'accepted',nullif($8,'')::uuid,$9,$10,$11,$12::jsonb,$10,$10)
		on conflict (dispatch_id) do nothing
	`, payload.DispatchID, payload.OriginDeviceID, payload.OrchestrationID, payload.NodeID,
		payload.PlanHash, string(encodedPayload), envelope.Signature, runID, leaseExpires, now,
		executionKind, encodedDelegation); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	return s.GetRemoteExecutionStatus(ctx, payload.DispatchID, payload.OriginDeviceID)
}

func (s *Service) RunRemoteExecutionCycle(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.orchestrationRemote {
		return 0, nil
	}
	if err := s.remoteExecutionEnabled(); err != nil {
		return 0, err
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	processed, err := s.reconcileRemoteInbox(ctx, limit)
	if err != nil {
		return processed, err
	}
	dispatched, err := s.reconcileRemoteOutbox(ctx, limit)
	return processed + dispatched, err
}

func (s *Service) reconcileRemoteInbox(ctx context.Context, limit int) (int, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select dispatch_id::text,coalesce(local_run_id::text,''),execution_kind,status,
		       broker_delegation, cancel_requested from steward_remote_inbox
		where status in ('accepted','running')
		  and (execution_kind<>'broker' or status='accepted' or lease_expires_at<now())
		order by updated_at limit $1
	`, limit)
	if err != nil {
		return 0, err
	}
	type item struct {
		dispatchID, runID, kind, status string
		delegation                      []byte
		cancelRequested                 bool
	}
	items := []item{}
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.dispatchID, &value.runID, &value.kind, &value.status, &value.delegation, &value.cancelRequested); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, value)
	}
	rows.Close()
	for _, value := range items {
		if value.kind == "broker" {
			if err := s.reconcileRemoteBrokerInbox(ctx, value.dispatchID, value.delegation, value.cancelRequested); err != nil {
				return 0, err
			}
			continue
		}
		run, err := s.GetAgentRun(ctx, value.runID)
		if err != nil {
			return 0, err
		}
		status := remoteStatusForRuntime(run.Status)
		now := time.Now().UTC()
		leaseExpires := now.Add(s.remoteExecutionLease)
		result := map[string]any{}
		resultSignature := ""
		var completed any
		if runtimeRunTerminal(run.Status) {
			result, err = s.remoteRunResult(ctx, run)
			if err != nil {
				return 0, err
			}
			completed = now
		}
		encodedResult, _ := json.Marshal(result)
		if _, err := s.db.Pool.Exec(ctx, `
			update steward_remote_inbox set status=$2, heartbeat_at=$3, lease_expires_at=$4,
			       result_payload=$5::jsonb, result_signature=$6, last_error=$7,
			       updated_at=$3, completed_at=$8
			where dispatch_id=$1 and status=$9 and cancel_requested=$10
		`, value.dispatchID, status, now, leaseExpires, string(encodedResult), resultSignature,
			run.FailureSummary, completed, value.status, value.cancelRequested); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func (s *Service) reconcileRemoteBrokerInbox(ctx context.Context, dispatchID string, encodedDelegation []byte, cancelRequested bool) error {
	now := time.Now().UTC()
	if cancelRequested {
		_, err := s.db.Pool.Exec(ctx, `
			update steward_remote_inbox set status='cancelled', heartbeat_at=$2,
			       lease_expires_at=$2, updated_at=$2, completed_at=$2,
			       result_signature='',last_error='cancelled before or during delegated Broker execution'
			where dispatch_id=$1 and status in ('accepted','running')
		`, dispatchID, now)
		return err
	}
	var delegation privilegebroker.SignedBrokerDelegation
	if err := json.Unmarshal(encodedDelegation, &delegation); err != nil || delegation.Claims.DelegationID == "" {
		return fmt.Errorf("decode persisted Broker delegation")
	}
	federation, ok := s.privilegeBroker.(PrivilegeBrokerFederationClient)
	if !ok {
		return fmt.Errorf("target Broker does not support federation")
	}
	originDevice, err := s.getDevice(ctx, delegation.Claims.OriginDeviceID)
	if err != nil {
		return err
	}
	originStatus, err := s.fetchRemoteBrokerStatus(ctx, originDevice)
	if err != nil {
		_, _ = s.db.Pool.Exec(ctx, `
			update steward_remote_inbox set heartbeat_at=$2, lease_expires_at=$3,
			       last_error=$4, updated_at=$2 where dispatch_id=$1 and status='accepted'
		`, dispatchID, now, now.Add(s.remoteExecutionLease), sanitizeRuntimeError(err))
		return nil
	}
	// Claim the high-privilege delegation with a durable lease before invoking
	// the Broker. Multiple daemon instances may have read the accepted inbox row,
	// but only one may consume the Broker's single-use delegation token. The
	// result_signature column is private target-side state until completion and
	// acts as the claim token without exposing it in the signed status payload.
	claimID, claimed, err := s.claimRemoteBrokerInbox(ctx, dispatchID, now)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	execCtx, cancel := context.WithCancel(ctx)
	s.remoteBrokerCancelMu.Lock()
	s.remoteBrokerCancels[dispatchID] = cancel
	s.remoteBrokerCancelMu.Unlock()
	heartbeatDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	go func() {
		defer close(heartbeatStopped)
		interval := s.remoteExecutionLease / 3
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-execCtx.Done():
				return
			case tickedAt := <-ticker.C:
				tag, heartbeatErr := s.db.Pool.Exec(execCtx, `update steward_remote_inbox
					set heartbeat_at=$3,lease_expires_at=$4,updated_at=$3
					where dispatch_id=$1 and status='running' and result_signature=$2 and cancel_requested=false`,
					dispatchID, claimID, tickedAt.UTC(), tickedAt.UTC().Add(s.remoteExecutionLease))
				if heartbeatErr != nil || tag.RowsAffected() != 1 {
					cancel()
					return
				}
			}
		}
	}()
	response, executeErr := federation.ExecuteDelegation(execCtx, delegation, originStatus)
	executionContextErr := execCtx.Err()
	close(heartbeatDone)
	<-heartbeatStopped
	cancel()
	s.remoteBrokerCancelMu.Lock()
	delete(s.remoteBrokerCancels, dispatchID)
	s.remoteBrokerCancelMu.Unlock()
	var requested bool
	_ = s.db.Pool.QueryRow(ctx, `select cancel_requested from steward_remote_inbox where dispatch_id=$1`, dispatchID).Scan(&requested)
	completed := time.Now().UTC()
	status := "succeeded"
	failure := ""
	if requested || errors.Is(executionContextErr, context.Canceled) {
		status = "cancelled"
		failure = "delegated Broker execution cancelled"
	} else if executeErr != nil {
		status = "failed"
		failure = sanitizeRuntimeError(executeErr)
	}
	receipt := response.Receipt
	receiptBytes, _ := json.Marshal(receipt)
	receiptDigest := sha256.Sum256(receiptBytes)
	redactedCount := 0
	if len(delegation.Claims.CredentialRefs) > 0 {
		redactedCount = 1
	}
	result := map[string]any{
		"execution_id": delegation.Claims.DelegationID, "status": status,
		"broker_receipt": receipt, "stdout": response.Stdout, "stderr": response.Stderr,
		"artifact_count": 1, "redacted_count": redactedCount,
		"data_levels": []string{DataD4}, "manifest_sha256": hex.EncodeToString(receiptDigest[:]),
		"credential_refs": delegation.Claims.CredentialRefs,
	}
	encodedResult, _ := json.Marshal(result)
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_remote_inbox set status=$2, heartbeat_at=$3, lease_expires_at=$3,
		       result_payload=$4::jsonb,result_signature='',last_error=$5,updated_at=$3,completed_at=$3
		where dispatch_id=$1 and status='running' and cancel_requested=$6 and result_signature=$7
	`, dispatchID, status, completed, string(encodedResult), failure, requested, claimID)
	if err == nil && tag.RowsAffected() == 0 {
		// Cancellation may have arrived after the post-execution read. It owns
		// the terminal decision and must not be replaced by a late Broker result.
		_, err = s.db.Pool.Exec(ctx, `update steward_remote_inbox set status='cancelled',heartbeat_at=$2,
			lease_expires_at=$2,result_signature='',last_error='delegated Broker execution cancelled',updated_at=$2,completed_at=$2
			where dispatch_id=$1 and status in ('accepted','running') and cancel_requested=true`, dispatchID, completed)
	}
	return err
}

func (s *Service) claimRemoteBrokerInbox(ctx context.Context, dispatchID string, now time.Time) (string, bool, error) {
	s.remoteBrokerCancelMu.Lock()
	_, activeInProcess := s.remoteBrokerCancels[dispatchID]
	s.remoteBrokerCancelMu.Unlock()
	if activeInProcess {
		return "", false, nil
	}
	claimID := "broker-claim:" + uuid.NewString()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_remote_inbox set status='running',heartbeat_at=$2,
		       lease_expires_at=$3,result_signature=$4,updated_at=$2
		where dispatch_id=$1 and cancel_requested=false
		  and (status='accepted' or (status='running' and lease_expires_at<$2))
	`, dispatchID, now, now.Add(s.remoteExecutionLease), claimID)
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() != 1 {
		return "", false, nil
	}
	return claimID, true, nil
}

func remoteStatusForRuntime(status string) string {
	switch status {
	case RuntimeRunDraft, RuntimeRunPlanning, RuntimeRunAwaitingApproval, RuntimeRunQueued:
		return "accepted"
	case RuntimeRunRunning, RuntimeRunVerifying, RuntimeRunCompensating:
		return "running"
	case RuntimeRunSucceeded:
		return "succeeded"
	case RuntimeRunFailed:
		return "failed"
	case RuntimeRunCancelled:
		return "cancelled"
	default:
		return "blocked"
	}
}

func (s *Service) remoteRunResult(ctx context.Context, run domain.StewardAgentRun) (map[string]any, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, sha256, data_level, redacted from steward_evidence_artifacts
		where run_id=$1 order by id
	`, run.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	manifest := sha256.New()
	artifactCount := 0
	redactedCount := 0
	levels := map[string]bool{}
	for rows.Next() {
		var id, digest, level string
		var redacted bool
		if err := rows.Scan(&id, &digest, &level, &redacted); err != nil {
			return nil, err
		}
		artifactCount++
		if redacted {
			redactedCount++
		}
		levels[level] = true
		_, _ = manifest.Write([]byte(id + ":" + digest + "\n"))
	}
	dataLevels := make([]string, 0, len(levels))
	for level := range levels {
		dataLevels = append(dataLevels, level)
	}
	sort.Strings(dataLevels)
	manifestDigest := ""
	if artifactCount > 0 {
		manifestDigest = hex.EncodeToString(manifest.Sum(nil))
	}
	return map[string]any{
		"run_id": run.ID, "status": run.Status, "plan_hash": run.PlanHash,
		"artifact_count": artifactCount, "redacted_count": redactedCount,
		"data_levels": dataLevels, "manifest_sha256": manifestDigest,
	}, rows.Err()
}

func (s *Service) GetRemoteExecutionStatus(ctx context.Context, dispatchID, authenticatedDeviceID string) (RemoteExecutionStatusEnvelope, error) {
	if err := s.remoteExecutionEnabled(); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	var payload RemoteExecutionStatusPayload
	var result, encodedDispatch []byte
	err := s.db.Pool.QueryRow(ctx, `
		select dispatch_id::text, origin_device_id, plan_hash, status, coalesce(local_run_id::text,''),
		       heartbeat_at, lease_expires_at, result_payload, last_error, completed_at,payload
		from steward_remote_inbox where dispatch_id=$1
	`, dispatchID).Scan(&payload.DispatchID, &payload.OriginDeviceID, &payload.PlanHash, &payload.Status,
		&payload.RemoteRunID, &payload.HeartbeatAt, &payload.LeaseExpiresAt, &result,
		&payload.FailureSummary, &payload.CompletedAt, &encodedDispatch)
	if err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	if payload.OriginDeviceID != strings.TrimSpace(authenticatedDeviceID) {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote dispatch is not owned by the authenticated device")
	}
	payload.Version = remoteExecutionProtocolVersion
	payload.TargetDeviceID = s.agentIDValue()
	var dispatch RemoteExecutionDispatchPayload
	if err := json.Unmarshal(encodedDispatch, &dispatch); err != nil {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("decode persisted remote dispatch: %w", err)
	}
	payload.ControlGeneration = dispatch.ControlGeneration
	_ = json.Unmarshal(result, &payload.Result)
	if payload.Result == nil {
		payload.Result = map[string]any{}
	}
	if payload.RemoteRunID == "" {
		payload.RemoteRunID, _ = payload.Result["execution_id"].(string)
	}
	signature := signRemotePayload(s.deviceSigningKey, payload)
	if signature == "" {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("sign remote execution status")
	}
	if runtimeRunTerminalStatus(payload.Status) {
		encodedResult, _ := json.Marshal(payload.Result)
		_, _ = s.db.Pool.Exec(ctx, `update steward_remote_inbox set result_signature=$2, result_payload=$3::jsonb where dispatch_id=$1`,
			dispatchID, signature, string(encodedResult))
	}
	return RemoteExecutionStatusEnvelope{Payload: payload, Signature: signature}, nil
}

func (s *Service) CancelRemoteExecution(ctx context.Context, dispatchID, authenticatedDeviceID string) (RemoteExecutionStatusEnvelope, error) {
	if err := s.remoteExecutionEnabled(); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	var originDeviceID, runID, status string
	if err := s.db.Pool.QueryRow(ctx, `
		select origin_device_id, coalesce(local_run_id::text,''), status
		from steward_remote_inbox where dispatch_id=$1
	`, dispatchID).Scan(&originDeviceID, &runID, &status); err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	if originDeviceID != strings.TrimSpace(authenticatedDeviceID) {
		return RemoteExecutionStatusEnvelope{}, fmt.Errorf("remote dispatch is not owned by the authenticated device")
	}
	if !runtimeRunTerminalStatus(status) {
		_, _ = s.db.Pool.Exec(ctx, `update steward_remote_inbox set cancel_requested=true,updated_at=now()
			where dispatch_id=$1 and status in ('accepted','running')`, dispatchID)
	}
	s.remoteBrokerCancelMu.Lock()
	brokerCancel := s.remoteBrokerCancels[dispatchID]
	s.remoteBrokerCancelMu.Unlock()
	if brokerCancel != nil {
		brokerCancel()
	}
	if !runtimeRunTerminalStatus(status) && runID != "" {
		if _, err := s.CancelAgentRun(ctx, runID); err != nil && !errors.Is(err, ErrAgentRunInvalidTransition) {
			return RemoteExecutionStatusEnvelope{}, err
		}
	}
	_, err := s.reconcileRemoteInbox(ctx, 20)
	if err != nil {
		return RemoteExecutionStatusEnvelope{}, err
	}
	return s.GetRemoteExecutionStatus(ctx, dispatchID, authenticatedDeviceID)
}

func runtimeRunTerminalStatus(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "blocked"
}

func (s *Service) reconcileRemoteOutbox(ctx context.Context, limit int) (int, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select remote.id::text, remote.target_device_id, remote.status, remote.payload,
		       remote.signature, device.api_base_url, device.public_key, remote.cancel_requested,
		       device.broker_public_key, device.broker_key_id
		from steward_remote_dispatches remote
		join steward_devices device on device.id=remote.target_device_id
		where remote.status in ('pending','sent','accepted','running') and remote.available_at <= now()
		order by remote.updated_at limit $1
	`, limit)
	if err != nil {
		return 0, err
	}
	type outboxItem struct {
		id, target, status, signature, apiBase, publicKey string
		brokerPublicKey, brokerKeyID                      string
		payload                                           []byte
		cancelRequested                                   bool
	}
	items := []outboxItem{}
	for rows.Next() {
		var value outboxItem
		if err := rows.Scan(&value.id, &value.target, &value.status, &value.payload, &value.signature, &value.apiBase, &value.publicKey, &value.cancelRequested,
			&value.brokerPublicKey, &value.brokerKeyID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, value)
	}
	rows.Close()
	for _, value := range items {
		var envelope RemoteExecutionStatusEnvelope
		if value.cancelRequested && value.status != "pending" {
			// A crash can leave the origin in sent before the target durably accepts the
			// dispatch. Replaying the signed dispatch is idempotent and closes that
			// window before cancellation, so a target-side 404 cannot strand the stop.
			if value.status == "sent" {
				var payload RemoteExecutionDispatchPayload
				if err := json.Unmarshal(value.payload, &payload); err != nil {
					return 0, err
				}
				endpoint, err := stewardAPIEndpoint(value.apiBase, "/steward/remote-execution/dispatches", nil)
				if err == nil {
					err = requestPeerJSON(ctx, http.DefaultClient, http.MethodPost, endpoint,
						RemoteExecutionDispatchEnvelope{Payload: payload, Signature: value.signature}, &envelope, s.remoteExecutionAuth())
				}
				if err != nil {
					s.recordRemoteDispatchError(ctx, value.id, err)
					continue
				}
			}
			endpoint, err := stewardAPIEndpoint(value.apiBase, "/steward/remote-execution/dispatches/"+value.id+"/cancel", nil)
			if err == nil {
				err = requestPeerJSON(ctx, http.DefaultClient, http.MethodPost, endpoint, nil, &envelope, s.remoteExecutionAuth())
			}
			if err != nil {
				s.recordRemoteDispatchError(ctx, value.id, err)
				continue
			}
		} else if value.cancelRequested && value.status == "pending" {
			now := time.Now().UTC()
			_, _ = s.db.Pool.Exec(ctx, `
				update steward_remote_dispatches set status='cancelled', last_error='cancelled before remote acceptance',
				       updated_at=$2, completed_at=$2, cancel_requested=false
				where id=$1 and status='pending' and cancel_requested=true
			`, value.id, now)
			continue
		} else if value.status == "pending" || value.status == "sent" {
			if value.status == "pending" {
				tag, updateErr := s.db.Pool.Exec(ctx, `update steward_remote_dispatches set status='sent', updated_at=now()
					where id=$1 and status='pending' and cancel_requested=false`, value.id)
				if updateErr != nil {
					return 0, updateErr
				}
				if tag.RowsAffected() != 1 {
					continue
				}
				value.status = "sent"
			}
			var payload RemoteExecutionDispatchPayload
			if err := json.Unmarshal(value.payload, &payload); err != nil {
				return 0, err
			}
			if payload.BrokerDelegation != nil && !payload.BrokerDelegation.Claims.ExpiresAt.After(time.Now().UTC()) {
				now := time.Now().UTC()
				_, _ = s.db.Pool.Exec(ctx, `
					update steward_remote_dispatches set status='blocked',
					       last_error='Broker delegation expired before target acceptance', updated_at=$2, completed_at=$2
					where id=$1 and status in ('pending','sent') and cancel_requested=false
				`, value.id, now)
				continue
			}
			endpoint, err := stewardAPIEndpoint(value.apiBase, "/steward/remote-execution/dispatches", nil)
			if err == nil {
				err = requestPeerJSON(ctx, http.DefaultClient, http.MethodPost, endpoint,
					RemoteExecutionDispatchEnvelope{Payload: payload, Signature: value.signature}, &envelope, s.remoteExecutionAuth())
			}
			if err != nil {
				s.recordRemoteDispatchError(ctx, value.id, err)
				continue
			}
		} else {
			endpoint, err := stewardAPIEndpoint(value.apiBase, "/steward/remote-execution/dispatches/"+value.id, nil)
			if err == nil {
				err = requestPeerJSON(ctx, http.DefaultClient, http.MethodGet, endpoint, nil, &envelope, s.remoteExecutionAuth())
			}
			if err != nil {
				s.recordRemoteDispatchError(ctx, value.id, err)
				continue
			}
		}
		if err := s.acceptRemoteStatus(ctx, value, envelope); err != nil {
			s.recordRemoteDispatchError(ctx, value.id, err)
			continue
		}
	}
	return len(items), nil
}

func (s *Service) remoteExecutionAuth() syncAuth {
	auth := s.syncAuth()
	auth.DeviceID = s.agentIDValue()
	auth.DevicePrivateKey = append([]byte(nil), s.deviceSigningKey...)
	return auth
}

func (s *Service) recordRemoteDispatchError(ctx context.Context, dispatchID string, cause error) {
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_remote_dispatches set attempt=least(attempt+1,100), last_error=$2,
		       available_at=now()+interval '1 second' * least(greatest(attempt+1,1),30), updated_at=now()
		where id=$1 and status in ('pending','sent','accepted','running')
	`, dispatchID, sanitizeRuntimeError(cause))
}

func remoteStatusPollDelay(previousStatus, nextStatus string, lease time.Duration) time.Duration {
	// A dispatch acceptance is only an acknowledgement that the target durably
	// stored the work. Keep the first result poll immediately eligible so a
	// target that finishes before the origin's next cycle is observed without
	// depending on application/VM clock alignment or a fixed sleep window.
	if previousStatus == "sent" && nextStatus == "accepted" {
		return 0
	}
	return lease / 3
}

func (s *Service) acceptRemoteStatus(ctx context.Context, item struct {
	id, target, status, signature, apiBase, publicKey string
	brokerPublicKey, brokerKeyID                      string
	payload                                           []byte
	cancelRequested                                   bool
}, envelope RemoteExecutionStatusEnvelope) error {
	payload := envelope.Payload
	if payload.Version != remoteExecutionProtocolVersion || payload.DispatchID != item.id || payload.TargetDeviceID != item.target {
		return fmt.Errorf("remote status identity mismatch")
	}
	if err := verifyRemotePayload(item.publicKey, envelope.Signature, payload); err != nil {
		return err
	}
	var dispatch RemoteExecutionDispatchPayload
	if err := json.Unmarshal(item.payload, &dispatch); err != nil {
		return fmt.Errorf("decode signed remote dispatch: %w", err)
	}
	if payload.ControlGeneration != dispatch.ControlGeneration {
		return fmt.Errorf("remote status belongs to stale control generation")
	}
	var expectedPlanHash string
	var currentGeneration int64
	if err := s.db.Pool.QueryRow(ctx, `select remote.plan_hash,parent.control_generation
		from steward_remote_dispatches remote join steward_orchestrations parent on parent.id=remote.orchestration_id
		where remote.id=$1`, item.id).Scan(&expectedPlanHash, &currentGeneration); err != nil {
		return err
	}
	if payload.PlanHash != expectedPlanHash || payload.OriginDeviceID != s.agentIDValue() {
		return fmt.Errorf("remote status does not match dispatch plan")
	}
	if dispatch.ControlGeneration != currentGeneration && !(item.cancelRequested && payload.Status == "cancelled") {
		return fmt.Errorf("remote status was fenced by a newer orchestration control generation")
	}
	if runtimeRunTerminalStatus(payload.Status) {
		if dispatch.BrokerDelegation != nil && (payload.Status == "succeeded" || payload.Status == "failed") {
			if item.brokerPublicKey == "" || item.brokerKeyID != dispatch.BrokerDelegation.Claims.TargetBrokerKeyID {
				return fmt.Errorf("target Broker identity is not pinned for delegated result verification")
			}
			receiptBytes, err := json.Marshal(payload.Result["broker_receipt"])
			if err != nil {
				return err
			}
			var receipt privilegebroker.SignedExecutionReceipt
			if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
				return fmt.Errorf("decode delegated Broker receipt: %w", err)
			}
			if payload.Status == "succeeded" && receipt.Payload.ExecutionID == "" {
				return fmt.Errorf("successful delegated execution is missing its Broker receipt")
			}
			if receipt.Payload.ExecutionID != "" {
				if err := privilegebroker.VerifyDelegatedReceipt(item.brokerPublicKey, *dispatch.BrokerDelegation, receipt); err != nil {
					return fmt.Errorf("verify delegated Broker receipt: %w", err)
				}
				if (payload.Status == "succeeded") != receipt.Payload.Succeeded {
					return fmt.Errorf("delegated Broker receipt outcome does not match remote status")
				}
			}
		}
	}
	encodedResult, _ := json.Marshal(payload.Result)
	var completed any
	resultSignature := ""
	if runtimeRunTerminalStatus(payload.Status) {
		completed = defaultTime(payload.CompletedAt, payload.HeartbeatAt)
		resultSignature = envelope.Signature
	}
	// Fence the network response against any control decision or newer status
	// response committed after the outbox row was read. In particular, an old
	// running response must never overwrite cancellation or a newer terminal
	// receipt, and heartbeats are monotonic even while status stays "running".
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_remote_dispatches set status=$2, remote_run_id=$3, heartbeat_at=$4,
		       lease_expires_at=$5, result_payload=$6::jsonb, result_signature=$7,
		       last_error=$8, attempt=attempt+1,
		       available_at=now()+make_interval(secs => $9), updated_at=now(), completed_at=$10,
		       cancel_requested=case when $10::timestamptz is not null then false else cancel_requested end
		where id=$1 and status=$11 and cancel_requested=$12
		  and (heartbeat_at is null or heartbeat_at <= $4)
		  and ($13 or exists(select 1 from steward_orchestrations parent
		      where parent.id=steward_remote_dispatches.orchestration_id and parent.control_generation=$14))
	`, item.id, payload.Status, payload.RemoteRunID, payload.HeartbeatAt, payload.LeaseExpiresAt,
		string(encodedResult), resultSignature, payload.FailureSummary,
		remoteStatusPollDelay(item.status, payload.Status, s.remoteExecutionLease).Seconds(), completed, item.status, item.cancelRequested,
		item.cancelRequested && payload.Status == "cancelled", dispatch.ControlGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("remote status response lost its state or control-generation fence")
	}
	return nil
}

func defaultTime(value *time.Time, fallback time.Time) time.Time {
	if value != nil {
		return *value
	}
	return fallback
}

func (s *Service) getRemoteDispatchForNode(ctx context.Context, nodeID string) (*domain.StewardRemoteDispatch, error) {
	var item domain.StewardRemoteDispatch
	var result []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, orchestration_id::text, node_id::text, target_device_id, status,
		       plan_hash, attempt, lease_expires_at, heartbeat_at, remote_run_id,
		       result_payload, result_signature, last_error, created_at, updated_at, completed_at
		from steward_remote_dispatches where node_id=$1
	`, nodeID).Scan(&item.ID, &item.OrchestrationID, &item.NodeID, &item.TargetDeviceID,
		&item.Status, &item.PlanHash, &item.Attempt, &item.LeaseExpiresAt, &item.HeartbeatAt,
		&item.RemoteRunID, &result, &item.ResultSignature, &item.LastError,
		&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(result, &item.ResultPayload)
	return &item, nil
}
