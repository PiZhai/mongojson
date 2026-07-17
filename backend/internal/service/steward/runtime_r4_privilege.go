package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

type RemotePrivilegePreview struct {
	OrchestrationID    string   `json:"orchestration_id"`
	NodeID             string   `json:"node_id"`
	TargetDeviceID     string   `json:"target_device_id"`
	TargetBrokerKeyID  string   `json:"target_broker_key_id"`
	Capability         string   `json:"capability"`
	CredentialRefs     []string `json:"credential_refs,omitempty"`
	Subject            string   `json:"subject"`
	PlanHash           string   `json:"plan_hash"`
	ControlGeneration  int64    `json:"control_generation"`
	TargetPolicyDigest string   `json:"target_policy_digest"`
}

type ApproveRemotePrivilegeInput struct {
	PlanHash      string                              `json:"plan_hash"`
	ApprovalProof privilegebroker.SignedApprovalProof `json:"approval_proof"`
}

type remotePrivilegeNode struct {
	orchestrationID, nodeID, targetDevice, selectedDevice, capability string
	permission, dataLevel                                             string
	controlGeneration                                                 int64
	credentialRefs                                                    []string
	steps                                                             []CreateAgentRunStepInput
	delegationID                                                      string
}

func (s *Service) loadRemotePrivilegeNode(ctx context.Context, orchestrationID, nodeID string) (remotePrivilegeNode, error) {
	var item remotePrivilegeNode
	var refs, steps []byte
	err := s.db.Pool.QueryRow(ctx, `
		select node.orchestration_id::text, node.id::text, node.target_device,
		       coalesce(node.selected_device_id,''), node.remote_privilege_capability,
		       node.permission_ceiling, node.data_level, orchestration.control_generation,
		       node.remote_credential_refs, node.steps, node.remote_broker_delegation_id
		from steward_orchestration_nodes node
		join steward_orchestrations orchestration on orchestration.id=node.orchestration_id
		where node.orchestration_id=$1 and node.id=$2 and node.status='pending'
	`, orchestrationID, nodeID).Scan(&item.orchestrationID, &item.nodeID, &item.targetDevice,
		&item.selectedDevice, &item.capability, &item.permission, &item.dataLevel,
		&item.controlGeneration, &refs, &steps, &item.delegationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrOrchestrationInvalidTransition
	}
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal(refs, &item.credentialRefs)
	_ = json.Unmarshal(steps, &item.steps)
	if item.capability == "" || !remoteStepsRequireBroker(item.steps) {
		return item, fmt.Errorf("node is not an R4.4 remote privilege node")
	}
	return item, nil
}

func (s *Service) fetchRemoteBrokerStatus(ctx context.Context, device domain.StewardDevice) (privilegebroker.Status, error) {
	if device.BrokerPublicKey == "" || device.BrokerKeyID == "" {
		return privilegebroker.Status{}, fmt.Errorf("target device has no pinned Broker identity")
	}
	endpoint, err := stewardAPIEndpoint(device.APIBaseURL, "/steward/broker-federation/status", nil)
	if err != nil {
		return privilegebroker.Status{}, err
	}
	var status privilegebroker.Status
	if err := requestPeerJSON(ctx, http.DefaultClient, http.MethodGet, endpoint, nil, &status, s.remoteExecutionAuth()); err != nil {
		return status, err
	}
	if err := privilegebroker.VerifyStatus(device.BrokerPublicKey, status, time.Now().UTC()); err != nil {
		return status, err
	}
	if status.KeyID != device.BrokerKeyID || status.Stopped {
		return status, fmt.Errorf("target Broker identity is mismatched or stopped")
	}
	return status, nil
}

func remotePrivilegePlanHash(item remotePrivilegeNode, targetDeviceID, targetBrokerKeyID string) string {
	canonical := struct {
		OrchestrationID   string                    `json:"orchestration_id"`
		NodeID            string                    `json:"node_id"`
		TargetDeviceID    string                    `json:"target_device_id"`
		TargetBrokerKeyID string                    `json:"target_broker_key_id"`
		Capability        string                    `json:"capability"`
		CredentialRefs    []string                  `json:"credential_refs,omitempty"`
		Permission        string                    `json:"permission"`
		DataLevel         string                    `json:"data_level"`
		Steps             []CreateAgentRunStepInput `json:"steps"`
	}{item.orchestrationID, item.nodeID, targetDeviceID, targetBrokerKeyID, item.capability,
		item.credentialRefs, item.permission, item.dataLevel, item.steps}
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (s *Service) PreviewRemotePrivilegeNode(ctx context.Context, orchestrationID, nodeID string) (RemotePrivilegePreview, error) {
	if err := s.orchestrationEnabled(); err != nil {
		return RemotePrivilegePreview{}, err
	}
	item, err := s.loadRemotePrivilegeNode(ctx, orchestrationID, nodeID)
	if err != nil {
		return RemotePrivilegePreview{}, err
	}
	deviceID := item.selectedDevice
	var device domain.StewardDevice
	if deviceID == "" {
		device, err = s.selectRemoteExecutionDevice(ctx, item.targetDevice, item.permission, true)
		if err != nil {
			return RemotePrivilegePreview{}, err
		}
		deviceID = device.ID
	} else {
		device, err = s.getDevice(ctx, deviceID)
		if err != nil {
			return RemotePrivilegePreview{}, err
		}
	}
	status, err := s.fetchRemoteBrokerStatus(ctx, device)
	if err != nil {
		return RemotePrivilegePreview{}, err
	}
	planHash := remotePrivilegePlanHash(item, device.ID, status.KeyID)
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_orchestration_nodes set selected_device_id=$3,
		       remote_privilege_plan_hash=$4, updated_at=now()
		where orchestration_id=$1 and id=$2 and status='pending'
	`, orchestrationID, nodeID, device.ID, planHash); err != nil {
		return RemotePrivilegePreview{}, err
	}
	return RemotePrivilegePreview{
		OrchestrationID: orchestrationID, NodeID: nodeID, TargetDeviceID: device.ID,
		TargetBrokerKeyID: status.KeyID, Capability: item.capability,
		CredentialRefs: append([]string(nil), item.credentialRefs...),
		Subject:        "remote-broker:" + orchestrationID + ":" + nodeID,
		PlanHash:       planHash, ControlGeneration: item.controlGeneration,
		TargetPolicyDigest: status.PolicyDigest,
	}, nil
}

func (s *Service) ApproveRemotePrivilegeNode(ctx context.Context, orchestrationID, nodeID string, input ApproveRemotePrivilegeInput) (domain.StewardOrchestration, error) {
	item, err := s.loadRemotePrivilegeNode(ctx, orchestrationID, nodeID)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if item.delegationID != "" {
		return s.GetOrchestration(ctx, orchestrationID)
	}
	preview, err := s.PreviewRemotePrivilegeNode(ctx, orchestrationID, nodeID)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	if strings.ToLower(strings.TrimSpace(input.PlanHash)) != preview.PlanHash {
		return domain.StewardOrchestration{}, fmt.Errorf("remote privilege approval plan hash mismatch")
	}
	device, err := s.getDevice(ctx, preview.TargetDeviceID)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	targetStatus, err := s.fetchRemoteBrokerStatus(ctx, device)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	federation, ok := s.privilegeBroker.(PrivilegeBrokerFederationClient)
	if !ok {
		return domain.StewardOrchestration{}, fmt.Errorf("local Broker does not support R4.4 federation")
	}
	delegation, err := federation.IssueDelegation(ctx, privilegebroker.BrokerDelegationRequest{
		TargetDeviceID: preview.TargetDeviceID, TargetStatus: targetStatus,
		Capability: preview.Capability, CredentialRefs: preview.CredentialRefs,
		Subject: preview.Subject, PlanHash: preview.PlanHash,
		ApprovalRef: input.ApprovalProof.Claims.ProofID, ApprovalProof: input.ApprovalProof,
		ControlGeneration: preview.ControlGeneration,
	})
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	encoded, _ := json.Marshal(delegation)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardOrchestration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		update steward_orchestration_nodes set remote_broker_delegation=$3::jsonb,
		       remote_broker_delegation_id=$4, remote_broker_delegation_expires_at=$5,
		       updated_at=now() where orchestration_id=$1 and id=$2 and status='pending'
	`, orchestrationID, nodeID, string(encoded), delegation.Claims.DelegationID, delegation.Claims.ExpiresAt); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := appendOrchestrationEvent(ctx, tx, orchestrationID, nodeID, "node.remote_privilege_delegated",
		OrchestrationNodePending, "source Broker issued a plan-bound Broker-to-Broker delegation", map[string]any{
			"delegation_id": delegation.Claims.DelegationID, "target_device_id": preview.TargetDeviceID,
			"target_broker_key_id": preview.TargetBrokerKeyID, "capability": preview.Capability,
			"credential_refs": preview.CredentialRefs,
		}); err != nil {
		return domain.StewardOrchestration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardOrchestration{}, err
	}
	return s.GetOrchestration(ctx, orchestrationID)
}
