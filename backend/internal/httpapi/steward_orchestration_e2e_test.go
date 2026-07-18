package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/privilegebroker"
	"mongojson/backend/internal/service/steward"
)

type r44BrokerClient struct {
	deviceID       string
	privateKey     ed25519.PrivateKey
	status         privilegebroker.Status
	originDeviceID string
}

func r44BrokerKeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:8])
}

func newR44BrokerClient(deviceID, originDeviceID string, privateKey ed25519.PrivateKey, capabilityDigest string) *r44BrokerClient {
	publicKey := privateKey.Public().(ed25519.PublicKey)
	status := privilegebroker.Status{Version: privilegebroker.APIVersion, InstanceID: deviceID + "-broker-instance",
		Generation: 0, PolicyDigest: strings.Repeat("b", 64), PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		KeyID: r44BrokerKeyID(publicKey), IssuedAt: time.Now().UTC(), Capabilities: []privilegebroker.PublicCapability{{
			Name: "tool:r44", PermissionLevel: "A4", RiskLevel: "high", CapabilityDigest: capabilityDigest,
			CredentialIDs: []string{"credential:r44"},
		}}}
	unsigned := status
	unsigned.Signature = ""
	payload, _ := json.Marshal(unsigned)
	status.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	return &r44BrokerClient{deviceID: deviceID, originDeviceID: originDeviceID, privateKey: privateKey, status: status}
}

func (b *r44BrokerClient) Status(context.Context) (privilegebroker.Status, error) {
	return b.status, nil
}
func (b *r44BrokerClient) Capability(context.Context, string) (privilegebroker.PublicCapability, error) {
	return b.status.Capabilities[0], nil
}
func (b *r44BrokerClient) ExecuteCapability(context.Context, privilegebroker.Authorization) (privilegebroker.ExecuteResponse, error) {
	return privilegebroker.ExecuteResponse{}, errors.New("local execution is not used by R4.4 acceptance")
}
func (b *r44BrokerClient) SetControl(context.Context, bool, privilegebroker.ControlRequest) (privilegebroker.Status, error) {
	return b.status, nil
}
func (b *r44BrokerClient) IssueDelegation(_ context.Context, request privilegebroker.BrokerDelegationRequest) (privilegebroker.SignedBrokerDelegation, error) {
	now := time.Now().UTC()
	claims := privilegebroker.BrokerDelegationClaims{Version: privilegebroker.DelegationVersion,
		DelegationID: uuid.NewString(), OriginBrokerPublicKey: b.status.PublicKey, OriginBrokerKeyID: b.status.KeyID,
		OriginBrokerInstanceID: b.status.InstanceID, OriginDeviceID: b.deviceID, OriginControlGeneration: request.ControlGeneration,
		TargetDeviceID: request.TargetDeviceID, TargetBrokerKeyID: request.TargetStatus.KeyID,
		TargetBrokerInstanceID: request.TargetStatus.InstanceID, TargetPolicyDigest: request.TargetStatus.PolicyDigest,
		TargetControlGeneration: request.TargetStatus.Generation, Capability: request.Capability,
		CapabilityDigest: request.TargetStatus.Capabilities[0].CapabilityDigest, CredentialRefs: request.CredentialRefs,
		Subject: request.Subject, PlanHash: request.PlanHash, ApprovalRef: request.ApprovalRef,
		ApprovalProofID: request.ApprovalRef, ApprovalKeyID: request.ApprovalProof.KeyID,
		ApprovalExpiresAt: request.ApprovalProof.Claims.ExpiresAt, IssuedAt: now, ExpiresAt: request.ApprovalProof.Claims.ExpiresAt}
	payload, _ := json.Marshal(claims)
	return privilegebroker.SignedBrokerDelegation{Claims: claims, KeyID: b.status.KeyID,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(b.privateKey, payload))}, nil
}
func (b *r44BrokerClient) ExecuteDelegation(_ context.Context, delegation privilegebroker.SignedBrokerDelegation, _ privilegebroker.Status) (privilegebroker.ExecuteResponse, error) {
	now := time.Now().UTC()
	claims := delegation.Claims
	receipt := privilegebroker.ExecutionReceipt{ExecutionID: claims.DelegationID, BrokerInstanceID: b.status.InstanceID,
		Capability: claims.Capability, CapabilityDigest: claims.CapabilityDigest, Subject: claims.Subject,
		PlanHash: claims.PlanHash, ApprovalRef: claims.ApprovalRef, ApprovalProofID: claims.ApprovalProofID,
		ApprovalKeyID: claims.ApprovalKeyID, ApprovalExpiresAt: claims.ApprovalExpiresAt,
		ControlGeneration: claims.TargetControlGeneration, ExitCode: 0, Succeeded: true,
		StdoutSHA256: strings.Repeat("0", 64), StderrSHA256: strings.Repeat("0", 64), AuditPersisted: true,
		StartedAt: now, FinishedAt: now, DelegationID: claims.DelegationID,
		OriginBrokerKeyID: claims.OriginBrokerKeyID, CredentialRefs: claims.CredentialRefs}
	payload, _ := json.Marshal(receipt)
	signed := privilegebroker.SignedExecutionReceipt{Payload: receipt, KeyID: b.status.KeyID,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(b.privateKey, payload))}
	return privilegebroker.ExecuteResponse{Receipt: signed}, nil
}

func TestStewardR43SignedRemoteExecutionAndDisconnectRecovery(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.3 remote execution test")
	}
	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	originPublic, originPrivate, _ := ed25519.GenerateKey(rand.Reader)
	targetPublic, targetPrivate, _ := ed25519.GenerateKey(rand.Reader)
	orchestrationKey := bytes.Repeat([]byte{0x73}, 32)
	origin := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r43_origin"), "r43-origin",
		steward.WithRuntimeV2Enabled(true), steward.WithOrchestrationR4Enabled(true), steward.WithRemoteExecutionEnabled(true),
		steward.WithOrchestrationSigningKey(orchestrationKey), steward.WithDeviceSigningKey(originPrivate), steward.WithRemoteExecutionLease(5*time.Second))
	target := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r43_target"), "r43-target",
		steward.WithRuntimeV2Enabled(true), steward.WithRemoteExecutionEnabled(true),
		steward.WithOrchestrationSigningKey(orchestrationKey), steward.WithDeviceSigningKey(targetPrivate), steward.WithRemoteExecutionLease(5*time.Second))
	registerR43Peer := func(node stewardHTTPNode, id, name, api, publicKey string) {
		enabled := true
		if _, err := node.service.RegisterDevice(ctx, steward.RegisterDeviceInput{
			ID: id, DeviceName: name, Platform: "windows", Role: steward.DeviceRolePeer, SyncEnabled: &enabled,
			PermissionLevel: steward.PermissionA2, PublicKey: publicKey, APIBaseURL: api,
		}); err != nil {
			t.Fatal(err)
		}
	}
	registerR43Peer(origin, "r43-target", "R43 Target", target.peerAPIBase, base64.StdEncoding.EncodeToString(targetPublic))
	registerR43Peer(target, "r43-origin", "R43 Origin", origin.peerAPIBase, base64.StdEncoding.EncodeToString(originPublic))
	if _, err := origin.service.UpsertOrchestrationAgent(ctx, steward.UpsertOrchestrationAgentInput{
		ID: "r43-agent", Name: "R43 Agent", Role: "remote low privilege", PermissionCeiling: "A2", DataLevelCeiling: "D2",
		ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.UpsertOrchestrationAgent(ctx, steward.UpsertOrchestrationAgentInput{
		ID: "r43-overprivileged", Name: "Rejected Remote Agent", Role: "must not cross device", PermissionCeiling: "A3", DataLevelCeiling: "D2",
		ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "reject cross-device A3", PermissionCeiling: "A3", DataLevel: "D2",
		Nodes: []steward.CreateOrchestrationNodeInput{{Key: "denied", AgentID: "r43-overprivileged", Goal: "denied", TargetDevice: "r43-target",
			Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "denied"}}}}},
	}); err == nil {
		t.Fatal("R4.3 accepted a remote node above the A2 ceiling")
	}
	createRemote := func(goal string) domain.StewardOrchestration {
		item, err := origin.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
			Goal: goal, AutoStart: true, PermissionCeiling: "A2", DataLevel: "D2",
			Nodes: []steward.CreateOrchestrationNodeInput{{
				Key: "remote", AgentID: "r43-agent", Goal: goal, TargetDevice: "auto",
				Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": goal}}},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	waitForRemoteStatus := func(orchestrationID, expected string) domain.StewardOrchestration {
		deadline := time.Now().Add(4 * time.Second)
		var item domain.StewardOrchestration
		for time.Now().Before(deadline) {
			if _, err := origin.service.RunRemoteExecutionCycle(ctx, 10); err != nil {
				t.Fatal(err)
			}
			item = getR40Orchestration(t, ctx, origin, orchestrationID)
			if len(item.Nodes) == 1 && item.Nodes[0].RemoteDispatch != nil && item.Nodes[0].RemoteDispatch.Status == expected {
				return item
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("remote dispatch did not reach %s: %+v", expected, item)
		return domain.StewardOrchestration{}
	}
	driveRemoteToTerminal := func(orchestrationID, orchestrationStatus, dispatchStatus string, driveTarget bool) domain.StewardOrchestration {
		deadline := time.Now().Add(8 * time.Second)
		var item domain.StewardOrchestration
		for time.Now().Before(deadline) {
			if driveTarget {
				_, _ = target.service.RunAgentRuntimeCycle(ctx, 10)
				_, _ = target.service.RunRemoteExecutionCycle(ctx, 10)
			}
			_, _ = origin.service.RunRemoteExecutionCycle(ctx, 10)
			_, _ = origin.service.RunOrchestrationCycle(ctx, 10)
			item = getR40Orchestration(t, ctx, origin, orchestrationID)
			if item.Status == orchestrationStatus && len(item.Nodes) == 1 && item.Nodes[0].RemoteDispatch != nil && item.Nodes[0].RemoteDispatch.Status == dispatchStatus {
				return item
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("remote orchestration did not reach %s/%s: %+v", orchestrationStatus, dispatchStatus, item)
		return domain.StewardOrchestration{}
	}
	first := createRemote("signed remote execution")
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	accepted := waitForRemoteStatus(first.ID, "accepted")
	if accepted.Nodes[0].SelectedDeviceID != "r43-target" || accepted.Nodes[0].RemoteDispatch == nil ||
		accepted.Nodes[0].RemoteDispatch.Status != "accepted" || accepted.Nodes[0].RemoteDispatch.HeartbeatAt == nil {
		t.Fatalf("remote placement or signed heartbeat missing: node=%+v dispatch=%+v", accepted.Nodes[0], accepted.Nodes[0].RemoteDispatch)
	}
	completed := driveRemoteToTerminal(first.ID, steward.OrchestrationSucceeded, "succeeded", true)
	if completed.Status != steward.OrchestrationSucceeded || completed.Nodes[0].RemoteDispatch == nil ||
		completed.Nodes[0].RemoteDispatch.Status != "succeeded" || completed.Nodes[0].RemoteDispatch.ResultSignature == "" ||
		completed.Evidence.ChildRunCount != 1 || completed.Evidence.ArtifactCount == 0 {
		t.Fatalf("signed remote result was not verified and aggregated: %+v", completed)
	}

	second := createRemote("recover after target disconnect")
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	acceptedBeforeDisconnect := waitForRemoteStatus(second.ID, "accepted")
	if acceptedBeforeDisconnect.Nodes[0].RemoteDispatch == nil || acceptedBeforeDisconnect.Nodes[0].RemoteDispatch.Status != "accepted" {
		t.Fatalf("target did not durably accept dispatch before disconnect: node=%+v dispatch=%+v", acceptedBeforeDisconnect.Nodes[0], acceptedBeforeDisconnect.Nodes[0].RemoteDispatch)
	}
	target.peerServer.Close()
	if _, err := target.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := target.service.RunRemoteExecutionCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	offlineDeadline := time.Now().Add(8 * time.Second)
	var offline domain.StewardOrchestration
	for time.Now().Before(offlineDeadline) {
		_, _ = target.service.RunAgentRuntimeCycle(ctx, 10)
		_, _ = target.service.RunRemoteExecutionCycle(ctx, 10)
		_, _ = origin.service.RunRemoteExecutionCycle(ctx, 10)
		offline = getR40Orchestration(t, ctx, origin, second.ID)
		if offline.Nodes[0].RemoteDispatch != nil && offline.Nodes[0].RemoteDispatch.Attempt >= 2 && offline.Nodes[0].RemoteDispatch.Status == "accepted" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if offline.Nodes[0].RemoteDispatch == nil || offline.Nodes[0].RemoteDispatch.Attempt < 2 || offline.Nodes[0].RemoteDispatch.Status != "accepted" {
		t.Fatalf("offline dispatch was not retained for recovery: %+v", offline.Nodes[0])
	}
	peerRouter := chi.NewRouter()
	RegisterPeerRoutes(peerRouter, PeerDependencies{StewardService: target.service, Readiness: func(context.Context) (map[string]string, error) { return map[string]string{"database": "ok"}, nil }})
	restartedPeer := httptest.NewServer(peerRouter)
	defer restartedPeer.Close()
	registerR43Peer(origin, "r43-target", "R43 Target", restartedPeer.URL+"/api", base64.StdEncoding.EncodeToString(targetPublic))
	recovered := driveRemoteToTerminal(second.ID, steward.OrchestrationSucceeded, "succeeded", false)
	if recovered.Status != steward.OrchestrationSucceeded || recovered.Nodes[0].RemoteDispatch.Attempt < 2 {
		t.Fatalf("remote dispatch did not recover after reconnect: %+v", recovered)
	}

	third := createRemote("propagate remote cancellation")
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	acceptedForCancel := waitForRemoteStatus(third.ID, "accepted")
	if acceptedForCancel.Nodes[0].RemoteDispatch == nil || acceptedForCancel.Nodes[0].RemoteDispatch.Status != "accepted" {
		t.Fatalf("remote cancellation precondition missing: %+v", acceptedForCancel)
	}
	if _, err := origin.service.CancelOrchestration(ctx, third.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := driveRemoteToTerminal(third.ID, steward.OrchestrationCancelled, "cancelled", false)
	if cancelled.Status != steward.OrchestrationCancelled || cancelled.Nodes[0].RemoteDispatch.Status != "cancelled" {
		t.Fatalf("parent cancellation did not reach target device: %+v", cancelled)
	}

	crashWindow := createRemote("cancel dispatch left sent before acceptance")
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.pool.Exec(ctx, `
		update steward_remote_dispatches set status='sent', updated_at=now()
		where orchestration_id=$1 and status='pending'
	`, crashWindow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.CancelOrchestration(ctx, crashWindow.ID); err != nil {
		t.Fatal(err)
	}
	crashWindowCancelled := driveRemoteToTerminal(crashWindow.ID, steward.OrchestrationCancelled, "cancelled", false)
	if crashWindowCancelled.Status != steward.OrchestrationCancelled ||
		crashWindowCancelled.Nodes[0].RemoteDispatch == nil ||
		crashWindowCancelled.Nodes[0].RemoteDispatch.Status != "cancelled" {
		t.Fatalf("sent-before-acceptance cancellation was stranded: %+v", crashWindowCancelled)
	}
}

func TestStewardR44BrokerDelegationAndVerifiedCredentialBoundResult(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.4 Broker federation test")
	}
	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	originDevicePublic, originDevicePrivate, _ := ed25519.GenerateKey(rand.Reader)
	targetDevicePublic, targetDevicePrivate, _ := ed25519.GenerateKey(rand.Reader)
	_, originBrokerPrivate, _ := ed25519.GenerateKey(rand.Reader)
	targetBrokerPublic, targetBrokerPrivate, _ := ed25519.GenerateKey(rand.Reader)
	capabilityDigest := strings.Repeat("c", 64)
	originBroker := newR44BrokerClient("r44-origin", "", originBrokerPrivate, capabilityDigest)
	targetBroker := newR44BrokerClient("r44-target", "r44-origin", targetBrokerPrivate, capabilityDigest)
	orchestrationKey := bytes.Repeat([]byte{0x74}, 32)
	origin := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r44_origin"), "r44-origin",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeR3Enabled(true), steward.WithOrchestrationR4Enabled(true),
		steward.WithRemoteExecutionEnabled(true), steward.WithPrivilegeBrokerClient(originBroker),
		steward.WithOrchestrationSigningKey(orchestrationKey), steward.WithDeviceSigningKey(originDevicePrivate),
		steward.WithRemoteExecutionLease(5*time.Second))
	target := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r44_target"), "r44-target",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeR3Enabled(true), steward.WithRemoteExecutionEnabled(true),
		steward.WithPrivilegeBrokerClient(targetBroker), steward.WithOrchestrationSigningKey(orchestrationKey),
		steward.WithDeviceSigningKey(targetDevicePrivate), steward.WithRemoteExecutionLease(5*time.Second))
	enabled := true
	if _, err := origin.service.RegisterDevice(ctx, steward.RegisterDeviceInput{ID: "r44-target", DeviceName: "R44 Target",
		Platform: "windows", Role: steward.DeviceRolePeer, SyncEnabled: &enabled, PermissionLevel: steward.PermissionA7,
		PublicKey: base64.StdEncoding.EncodeToString(targetDevicePublic), APIBaseURL: target.peerAPIBase,
		BrokerPublicKey: base64.StdEncoding.EncodeToString(targetBrokerPublic), BrokerKeyID: targetBroker.status.KeyID}); err != nil {
		t.Fatal(err)
	}
	originBrokerPublic := originBrokerPrivate.Public().(ed25519.PublicKey)
	if _, err := target.service.RegisterDevice(ctx, steward.RegisterDeviceInput{ID: "r44-origin", DeviceName: "R44 Origin",
		Platform: "windows", Role: steward.DeviceRolePeer, SyncEnabled: &enabled, PermissionLevel: steward.PermissionA7,
		PublicKey: base64.StdEncoding.EncodeToString(originDevicePublic), APIBaseURL: origin.peerAPIBase,
		BrokerPublicKey: base64.StdEncoding.EncodeToString(originBrokerPublic), BrokerKeyID: originBroker.status.KeyID}); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.UpsertOrchestrationAgent(ctx, steward.UpsertOrchestrationAgentInput{ID: "r44-agent",
		Name: "R44 Agent", Role: "remote privileged operator", PermissionCeiling: "A7", DataLevelCeiling: "D4",
		ToolAllowlist: []string{"privilege.execute"}, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	item, err := origin.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{Goal: "remote credential-bound capability",
		AutoStart: true, PermissionCeiling: "A7", DataLevel: "D4", Nodes: []steward.CreateOrchestrationNodeInput{{
			Key: "privileged", AgentID: "r44-agent", Goal: "run target capability", TargetDevice: "r44-target",
			PermissionCeiling: "A7", DataLevel: "D4", CredentialRefs: []string{"credential:r44"},
			Steps: []steward.CreateAgentRunStepInput{{Key: "execute", ToolName: "privilege.execute",
				Arguments: map[string]any{"capability": "tool:r44"}}},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	waiting := getR40Orchestration(t, ctx, origin, item.ID)
	if waiting.Nodes[0].Status != steward.OrchestrationNodePending || waiting.Nodes[0].RemotePrivilege == nil ||
		waiting.Nodes[0].RemotePrivilege.Status != "awaiting_approval" {
		t.Fatalf("high privilege node dispatched without Broker delegation: %+v", waiting.Nodes[0])
	}
	preview, err := origin.service.PreviewRemotePrivilegeNode(ctx, item.ID, item.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	proofID := strings.Repeat("d", 64)
	proof := privilegebroker.SignedApprovalProof{KeyID: "test-authority", Claims: privilegebroker.ApprovalProofClaims{
		ProofID: proofID, Subject: preview.Subject, PlanHash: preview.PlanHash, Capability: preview.Capability,
		ControlGeneration: preview.ControlGeneration, GrantedBy: "operator", Reason: "R4.4 acceptance",
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}}
	if _, err := origin.service.ApproveRemotePrivilegeNode(ctx, item.ID, item.Nodes[0].ID,
		steward.ApproveRemotePrivilegeInput{PlanHash: preview.PlanHash, ApprovalProof: proof}); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.RunRemoteExecutionCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := target.service.RunRemoteExecutionCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1800 * time.Millisecond)
	if _, err := origin.service.RunRemoteExecutionCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	completed := getR40Orchestration(t, ctx, origin, item.ID)
	if completed.Status != steward.OrchestrationSucceeded || completed.Nodes[0].RemoteDispatch == nil ||
		completed.Nodes[0].RemoteDispatch.Status != "succeeded" || completed.Nodes[0].RemoteDispatch.ResultSignature == "" ||
		completed.Evidence.ArtifactCount != 1 || completed.Evidence.RedactedCount != 1 {
		t.Fatalf("R4.4 result did not pass device and Broker receipt verification: %+v", completed)
	}
}

func TestStewardR41IndependentWorkersAndMailboxRecovery(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.1 worker acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := bytes.Repeat([]byte{0x51}, 32)
	options := []steward.ServiceOption{
		steward.WithRuntimeV2Enabled(true), steward.WithOrchestrationR4Enabled(true),
		steward.WithOrchestrationWorkersEnabled(true), steward.WithOrchestrationSigningKey(key),
		steward.WithOrchestrationMessageLease(time.Second), steward.WithRuntimeLeaseTTL(time.Second),
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r41_workers"), "r41-server", options...)
	for _, input := range []steward.UpsertOrchestrationAgentInput{
		{ID: "r41-research", Name: "R41 Research", Role: "collector", PermissionCeiling: "A0", DataLevelCeiling: "D0", ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1},
		{ID: "r41-writer", Name: "R41 Writer", Role: "synthesizer", PermissionCeiling: "A0", DataLevelCeiling: "D0", ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1},
	} {
		if _, err := node.service.UpsertOrchestrationAgent(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	workerDB := &database.DB{Pool: node.pool}
	researchWorker := steward.NewService(workerDB, append(options, steward.WithRuntimeWorkerID("worker-research-1"))...)
	writerWorker := steward.NewService(workerDB, append(options, steward.WithRuntimeWorkerID("worker-writer-1"))...)
	if _, err := researchWorker.RegisterAgentWorker(ctx, "r41-research", "worker-research-1", 1001); err != nil {
		t.Fatal(err)
	}
	if _, err := writerWorker.RegisterAgentWorker(ctx, "r41-writer", "worker-writer-1", 1002); err != nil {
		t.Fatal(err)
	}
	orchestration, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "execute through two independent Agent workers", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0", MaxParallel: 2,
		Nodes: []steward.CreateOrchestrationNodeInput{
			{Key: "research", AgentID: "r41-research", Goal: "research", Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "research"}}}},
			{Key: "draft", AgentID: "r41-writer", Goal: "draft", Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "draft"}}}},
			{Key: "join", AgentID: "r41-writer", Goal: "join", DependsOn: []string{"research", "draft"}, Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "joined"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil || processed != 0 {
		t.Fatalf("main Runtime worker stole R4.1 child work: processed=%d err=%v", processed, err)
	}
	messageA, claimed, err := researchWorker.ClaimAgentMessage(ctx, "r41-research", "worker-research-1")
	if err != nil || !claimed {
		t.Fatalf("research worker claim=%t err=%v", claimed, err)
	}
	messageB, claimed, err := writerWorker.ClaimAgentMessage(ctx, "r41-writer", "worker-writer-1")
	if err != nil || !claimed {
		t.Fatalf("writer worker claim=%t err=%v", claimed, err)
	}
	if err := researchWorker.ExecuteAgentMessage(ctx, messageA, "worker-research-1"); err != nil {
		t.Fatal(err)
	}
	if err := writerWorker.ExecuteAgentMessage(ctx, messageB, "worker-writer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	joinMessage, claimed, err := writerWorker.ClaimAgentMessage(ctx, "r41-writer", "worker-writer-1")
	if err != nil || !claimed {
		t.Fatalf("writer join claim=%t err=%v", claimed, err)
	}
	if err := writerWorker.ExecuteAgentMessage(ctx, joinMessage, "worker-writer-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	completed := getR40Orchestration(t, ctx, node, orchestration.ID)
	if completed.Status != steward.OrchestrationSucceeded || completed.Evidence.ChildRunCount != 3 {
		t.Fatalf("independent worker orchestration failed: %+v", completed)
	}

	crashRun, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "recover an expired worker mailbox lease", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{{Key: "recover", AgentID: "r41-research", Goal: "recover", Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "recovered"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	lost, claimed, err := researchWorker.ClaimAgentMessage(ctx, "r41-research", "worker-research-1")
	if err != nil || !claimed {
		t.Fatalf("crashing worker claim=%t err=%v", claimed, err)
	}
	time.Sleep(1100 * time.Millisecond)
	replacement := steward.NewService(workerDB, append(options, steward.WithRuntimeWorkerID("worker-research-2"))...)
	if _, err := replacement.RegisterAgentWorker(ctx, "r41-research", "worker-research-2", 1003); err != nil {
		t.Fatal(err)
	}
	recovered, claimed, err := replacement.ClaimAgentMessage(ctx, "r41-research", "worker-research-2")
	if err != nil || !claimed || recovered.ID != lost.ID || recovered.Attempt != 2 {
		t.Fatalf("expired mailbox lease was not redelivered: lost=%+v recovered=%+v claimed=%t err=%v", lost, recovered, claimed, err)
	}
	if err := replacement.ExecuteAgentMessage(ctx, recovered, "worker-research-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if final := getR40Orchestration(t, ctx, node, crashRun.ID); final.Status != steward.OrchestrationSucceeded {
		t.Fatalf("replacement worker did not recover orchestration: %+v", final)
	}
}

func TestStewardR42CompensatesSucceededNodesInReverseOrder(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.2 Saga acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r42_saga"), "r42-server",
		steward.WithRuntimeV2Enabled(true), steward.WithOrchestrationR4Enabled(true),
		steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x62}, 32)))
	if _, err := node.service.UpsertOrchestrationAgent(ctx, steward.UpsertOrchestrationAgentInput{
		ID: "r42-worker", Name: "R42 Worker", Role: "saga participant", PermissionCeiling: "A0", DataLevelCeiling: "D0",
		ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1, MaxRuntimeSeconds: 900, MaxAttempts: 20, MaxEvidenceBytes: 1 << 20,
	}); err != nil {
		t.Fatal(err)
	}
	orchestration, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "compensate two completed effects", AutoStart: true, FailurePolicy: "compensate",
		PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{
			{Key: "prepare", AgentID: "r42-worker", Goal: "prepare", Steps: []steward.CreateAgentRunStepInput{{Key: "do", ToolName: "runtime.echo", Arguments: map[string]any{"value": "prepared"}}}, CompensationSteps: []steward.CreateAgentRunStepInput{{Key: "undo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "unprepared"}}}},
			{Key: "publish", AgentID: "r42-worker", Goal: "publish", DependsOn: []string{"prepare"}, Steps: []steward.CreateAgentRunStepInput{{Key: "do", ToolName: "runtime.echo", Arguments: map[string]any{"value": "published"}}}, CompensationSteps: []steward.CreateAgentRunStepInput{{Key: "undo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "unpublished"}}}},
			{Key: "fail", AgentID: "r42-worker", Goal: "fail postcondition", DependsOn: []string{"publish"}, Steps: []steward.CreateAgentRunStepInput{{Key: "fail", ToolName: "runtime.echo", Arguments: map[string]any{"value": "actual"}, ExpectedOutput: map[string]any{"value": "different"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	compensating := getR40Orchestration(t, ctx, node, orchestration.ID)
	if compensating.Status != steward.OrchestrationCompensating || len(compensating.Nodes) != 5 {
		t.Fatalf("Saga did not materialize two compensations: %+v", compensating)
	}
	if compensating.Nodes[3].Key != "compensate-002" || len(compensating.Nodes[3].DependsOn) != 0 ||
		compensating.Nodes[4].Key != "compensate-001" || len(compensating.Nodes[4].DependsOn) != 1 || compensating.Nodes[4].DependsOn[0] != "compensate-002" {
		t.Fatalf("compensations are not reverse-ordered: %+v", compensating.Nodes)
	}
	for i := 0; i < 3; i++ {
		if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	final := getR40Orchestration(t, ctx, node, orchestration.ID)
	if final.Status != steward.OrchestrationCompensated || final.Nodes[3].Status != steward.OrchestrationNodeSucceeded || final.Nodes[4].Status != steward.OrchestrationNodeSucceeded {
		t.Fatalf("Saga compensation did not complete: %+v", final)
	}
	if final.Evidence.ChildRunCount != 5 || !hasR40Event(final.Events, "orchestration.compensating") || !hasR40Event(final.Events, "orchestration.compensated") {
		t.Fatalf("Saga evidence or events are incomplete: evidence=%+v events=%+v", final.Evidence, final.Events)
	}

	compensationFailure, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "surface a failed compensation", AutoStart: true, FailurePolicy: "compensate",
		PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{
			{Key: "effect", AgentID: "r42-worker", Goal: "effect", Steps: []steward.CreateAgentRunStepInput{{Key: "do", ToolName: "runtime.echo", Arguments: map[string]any{"value": "done"}}}, CompensationSteps: []steward.CreateAgentRunStepInput{{Key: "undo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "actual"}, ExpectedOutput: map[string]any{"value": "different"}}}},
			{Key: "trigger", AgentID: "r42-worker", Goal: "trigger", DependsOn: []string{"effect"}, Steps: []steward.CreateAgentRunStepInput{{Key: "fail", ToolName: "runtime.echo", Arguments: map[string]any{"value": "actual"}, ExpectedOutput: map[string]any{"value": "different"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	failedCompensation := getR40Orchestration(t, ctx, node, compensationFailure.ID)
	if failedCompensation.Status != steward.OrchestrationCompensationFailed || !hasR40Event(failedCompensation.Events, "orchestration.compensation_failed") {
		t.Fatalf("failed compensation was not isolated and surfaced: %+v", failedCompensation)
	}
}

func TestStewardR40LocalMultiAgentOrchestration(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.0 orchestration acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := bytes.Repeat([]byte{0x42}, 32)
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r40_orchestration"), "r4-local-node",
		steward.WithRuntimeV2Enabled(true), steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(key))

	upsertR40Agent(t, ctx, node, map[string]any{
		"id": "researcher", "name": "Research Agent", "role": "parallel fact collector",
		"permission_ceiling": "A0", "data_level_ceiling": "D0",
		"tool_allowlist": []string{"runtime.echo"}, "max_concurrency": 1,
	})
	upsertR40Agent(t, ctx, node, map[string]any{
		"id": "writer", "name": "Writer Agent", "role": "result synthesizer",
		"permission_ceiling": "A0", "data_level_ceiling": "D0",
		"tool_allowlist": []string{"runtime.echo"}, "max_concurrency": 1,
	})

	orchestration := postR40Orchestration(t, ctx, node, map[string]any{
		"goal":            "fan out two independent tasks and synthesize their evidence",
		"idempotency_key": "r40-real-multi-agent", "auto_start": true,
		"permission_ceiling": "A0", "data_level": "D0", "max_parallel": 2,
		"nodes": []map[string]any{
			{
				"key": "collect_a", "agent_id": "researcher", "goal": "collect A",
				"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "A"}, "expected_output": map[string]any{"value": "A"}}},
			},
			{
				"key": "collect_b", "agent_id": "writer", "goal": "collect B",
				"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "B"}, "expected_output": map[string]any{"value": "B"}}},
			},
			{
				"key": "synthesize", "agent_id": "writer", "goal": "synthesize A and B",
				"depends_on": []string{"collect_a", "collect_b"},
				"steps":      []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "A+B"}, "expected_output": map[string]any{"value": "A+B"}}},
			},
		},
	})
	if orchestration.Status != steward.OrchestrationQueued {
		t.Fatalf("created orchestration status=%s, want queued", orchestration.Status)
	}
	if processed, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil || processed != 1 {
		t.Fatalf("first orchestration cycle processed=%d err=%v", processed, err)
	}
	afterDispatch := getR40Orchestration(t, ctx, node, orchestration.ID)
	if countR40NodeStatus(afterDispatch.Nodes, steward.OrchestrationNodeDispatched) != 2 || afterDispatch.Nodes[2].Status != steward.OrchestrationNodePending {
		t.Fatalf("fan-out did not respect max_parallel/dependencies: %+v", afterDispatch.Nodes)
	}
	if afterDispatch.Nodes[0].AgentID == afterDispatch.Nodes[1].AgentID {
		t.Fatalf("fan-out did not use two Agent identities: %+v", afterDispatch.Nodes)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil || processed != 2 {
		t.Fatalf("parallel child runtime cycle processed=%d err=%v", processed, err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatalf("reconcile fan-out and dispatch join: %v", err)
	}
	afterJoin := getR40Orchestration(t, ctx, node, orchestration.ID)
	if afterJoin.Nodes[2].Status != steward.OrchestrationNodeDispatched {
		t.Fatalf("join node was not released after both dependencies: %+v", afterJoin.Nodes)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil || processed != 1 {
		t.Fatalf("join runtime cycle processed=%d err=%v", processed, err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
		t.Fatalf("final orchestration reconciliation: %v", err)
	}
	completed := getR40Orchestration(t, ctx, node, orchestration.ID)
	if completed.Status != steward.OrchestrationSucceeded || countR40NodeStatus(completed.Nodes, steward.OrchestrationNodeSucceeded) != 3 {
		t.Fatalf("orchestration did not succeed: %+v", completed)
	}
	if completed.Evidence.ChildRunCount != 3 || completed.Evidence.ArtifactCount < 3 || completed.Evidence.ManifestSHA256 == "" {
		t.Fatalf("child evidence lineage was not aggregated: %+v", completed.Evidence)
	}
	if !hasR40Event(completed.Events, "orchestration.succeeded") || !hasR40Event(completed.Events, "node.dispatched") {
		t.Fatalf("orchestration event trail is incomplete: %+v", completed.Events)
	}
}

func TestStewardR40RejectsTamperedDelegationAndPropagatesCancellation(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.0 security acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r40_security"), "r4-security-node",
		steward.WithRuntimeV2Enabled(true), steward.WithOrchestrationR4Enabled(true),
		steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x24}, 32)))
	_, err := node.service.UpsertOrchestrationAgent(ctx, steward.UpsertOrchestrationAgentInput{
		ID: "worker", Name: "Worker", Role: "bounded worker", PermissionCeiling: "A0", DataLevelCeiling: "D0",
		ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "reject a modified claim", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{{
			Key: "work", AgentID: "worker", Goal: "echo securely",
			Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "secure"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	dispatched, err := node.service.GetOrchestration(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.pool.Exec(ctx, `update steward_delegation_claims set signature='tampered' where id=$1`, dispatched.Nodes[0].Delegation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("tampered delegation should be durably blocked, not crash the worker: %v", err)
	}
	blocked := getR40Orchestration(t, ctx, node, created.ID)
	if blocked.Status != steward.OrchestrationBlocked || blocked.Nodes[0].Status != steward.OrchestrationNodeBlocked {
		t.Fatalf("tampered delegation was not fail-closed: %+v", blocked)
	}

	cancellable, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "propagate parent cancellation", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{{
			Key: "one", AgentID: "worker", Goal: "one",
			Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "one"}}},
		}, {
			Key: "two", AgentID: "worker", Goal: "two",
			Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "two"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	cancelled, err := node.service.CancelOrchestration(ctx, cancellable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != steward.OrchestrationCancelled || countR40NodeStatus(cancelled.Nodes, steward.OrchestrationNodeCancelled) != 2 {
		t.Fatalf("parent cancellation did not propagate: %+v", cancelled)
	}
	for _, child := range cancelled.Nodes {
		run, err := node.service.GetAgentRun(ctx, child.RuntimeRunID)
		if err != nil || run.Status != steward.RuntimeRunCancelled {
			t.Fatalf("child run was not cancelled: run=%+v err=%v", run, err)
		}
	}

	stopped, err := node.service.PauseRuntimeExecution(ctx, steward.SetRuntimeExecutionControlInput{Reason: "R4.0 fencing acceptance", ChangedBy: "test-operator"})
	if err != nil || !stopped.Stopped {
		t.Fatalf("activate global stop: control=%+v err=%v", stopped, err)
	}
	fenced, err := node.service.CreateOrchestration(ctx, steward.CreateOrchestrationInput{
		Goal: "do not dispatch while globally stopped", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []steward.CreateOrchestrationNodeInput{{
			Key: "fenced", AgentID: "worker", Goal: "wait for resume",
			Steps: []steward.CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "resumed"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil || processed != 0 {
		t.Fatalf("global stop did not fence scheduler: processed=%d err=%v", processed, err)
	}
	stillPending := getR40Orchestration(t, ctx, node, fenced.ID)
	if stillPending.Nodes[0].Status != steward.OrchestrationNodePending {
		t.Fatalf("node dispatched during global stop: %+v", stillPending.Nodes[0])
	}
	resumed, err := node.service.ResumeRuntimeExecution(ctx, steward.SetRuntimeExecutionControlInput{Reason: "R4.0 fencing acceptance complete", ChangedBy: "test-operator"})
	if err != nil || resumed.Stopped || resumed.Generation != stopped.Generation+1 {
		t.Fatalf("resume global execution: control=%+v err=%v", resumed, err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunOrchestrationCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if final := getR40Orchestration(t, ctx, node, fenced.ID); final.Status != steward.OrchestrationSucceeded || final.ControlGeneration != resumed.Generation {
		t.Fatalf("orchestration did not resume under the new generation: %+v", final)
	}
}

func upsertR40Agent(t *testing.T, ctx context.Context, node stewardHTTPNode, body map[string]any) domain.StewardOrchestrationAgent {
	t.Helper()
	var envelope struct {
		Agent domain.StewardOrchestrationAgent `json:"agent"`
	}
	doR40JSON(t, ctx, node, http.MethodPut, "/steward/orchestration/agents", body, http.StatusOK, &envelope)
	return envelope.Agent
}

func postR40Orchestration(t *testing.T, ctx context.Context, node stewardHTTPNode, body map[string]any) domain.StewardOrchestration {
	t.Helper()
	var envelope struct {
		Orchestration domain.StewardOrchestration `json:"orchestration"`
	}
	doR40JSON(t, ctx, node, http.MethodPost, "/steward/orchestrations", body, http.StatusCreated, &envelope)
	return envelope.Orchestration
}

func getR40Orchestration(t *testing.T, ctx context.Context, node stewardHTTPNode, id string) domain.StewardOrchestration {
	t.Helper()
	var envelope struct {
		Orchestration domain.StewardOrchestration `json:"orchestration"`
	}
	doR40JSON(t, ctx, node, http.MethodGet, "/steward/orchestrations/"+id, nil, http.StatusOK, &envelope)
	return envelope.Orchestration
}

func doR40JSON(t *testing.T, ctx context.Context, node stewardHTTPNode, method, path string, body any, expected int, target any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	}
	request, _ := http.NewRequestWithContext(ctx, method, node.apiBase+path, reader)
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != expected {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, expected, payload)
	}
	if target != nil && json.Unmarshal(payload, target) != nil {
		t.Fatalf("decode %s %s response: %s", method, path, payload)
	}
}

func countR40NodeStatus(nodes []domain.StewardOrchestrationNode, status string) int {
	count := 0
	for _, node := range nodes {
		if node.Status == status {
			count++
		}
	}
	return count
}

func hasR40Event(events []domain.StewardOrchestrationEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
