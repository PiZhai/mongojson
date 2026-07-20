package steward

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRemoteExecutionSignaturesBindDispatchAndResult(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey)
	payload := RemoteExecutionDispatchPayload{
		Version: remoteExecutionProtocolVersion, DispatchID: "dispatch", OriginDeviceID: "origin",
		TargetDeviceID: "target", OrchestrationID: "orchestration", NodeID: "node", AgentID: "agent",
		Goal: "remote", PermissionCeiling: PermissionA0, DataLevel: DataD0,
		ControlGeneration: 7,
		Steps:             []CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "ok"}}},
		IssuedAt:          time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	payload.PlanHash = remoteExecutionPlanHash(payload)
	signature := signRemotePayload(privateKey, payload)
	if err := verifyRemotePayload(publicKeyText, signature, payload); err != nil {
		t.Fatalf("verify signed dispatch: %v", err)
	}
	tampered := payload
	tampered.PermissionCeiling = PermissionA2
	if err := verifyRemotePayload(publicKeyText, signature, tampered); err == nil {
		t.Fatal("tampered remote dispatch retained a valid signature")
	}
	status := RemoteExecutionStatusPayload{
		Version: remoteExecutionProtocolVersion, DispatchID: payload.DispatchID, OriginDeviceID: payload.OriginDeviceID,
		TargetDeviceID: payload.TargetDeviceID, PlanHash: payload.PlanHash, ControlGeneration: payload.ControlGeneration,
		Status: "succeeded", RemoteRunID: "run",
		HeartbeatAt: time.Now().UTC(), LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		Result: map[string]any{"manifest_sha256": "abc", "artifact_count": 2},
	}
	resultSignature := signRemotePayload(privateKey, status)
	staleGeneration := status
	staleGeneration.ControlGeneration++
	if err := verifyRemotePayload(publicKeyText, resultSignature, staleGeneration); err == nil {
		t.Fatal("tampered remote control generation retained a valid signature")
	}
	status.Result["artifact_count"] = 3
	if err := verifyRemotePayload(publicKeyText, resultSignature, status); err == nil {
		t.Fatal("tampered remote result retained a valid signature")
	}
}

func TestRemoteStatusPollDelayKeepsFirstAcceptedResultImmediatelyObservable(t *testing.T) {
	lease := 6 * time.Second
	if got := remoteStatusPollDelay("sent", "accepted", lease); got != 0 {
		t.Fatalf("first accepted result poll delay=%s, want immediate", got)
	}
	if got := remoteStatusPollDelay("accepted", "accepted", lease); got != 2*time.Second {
		t.Fatalf("subsequent accepted result poll delay=%s, want %s", got, 2*time.Second)
	}
	if got := remoteStatusPollDelay("running", "running", lease); got != 2*time.Second {
		t.Fatalf("running heartbeat poll delay=%s, want %s", got, 2*time.Second)
	}
}

func TestRemoteBrokerInboxClaimIsExclusiveAndRecoverable(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	deviceID := "broker-origin-" + uuid.NewString()
	dispatchID := uuid.NewString()
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `insert into steward_devices(id,device_name,platform,created_at,updated_at)
		values($1,'Broker origin','windows',$2,$2)`, deviceID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_remote_inbox(
		dispatch_id,origin_device_id,orchestration_id,node_id,plan_hash,payload,signature,status,
		lease_expires_at,heartbeat_at,execution_kind,broker_delegation,created_at,updated_at
	) values($1,$2,'orchestration','node','plan','{}'::jsonb,'signature','accepted',$3,$4,'broker','{}'::jsonb,$4,$4)`,
		dispatchID, deviceID, now.Add(service.remoteExecutionLease), now); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type claimResult struct {
		id      string
		claimed bool
		err     error
	}
	results := make(chan claimResult, 2)
	var workers sync.WaitGroup
	for index := 0; index < 2; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claimID, claimed, err := service.claimRemoteBrokerInbox(ctx, dispatchID, now)
			results <- claimResult{id: claimID, claimed: claimed, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := []string{}
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			winners = append(winners, result.id)
		}
	}
	if len(winners) != 1 || !strings.HasPrefix(winners[0], "broker-claim:") {
		t.Fatalf("concurrent Broker inbox claims=%v, want exactly one", winners)
	}
	var storedStatus, storedClaim string
	if err := db.Pool.QueryRow(ctx, `select status,result_signature from steward_remote_inbox where dispatch_id=$1`, dispatchID).
		Scan(&storedStatus, &storedClaim); err != nil {
		t.Fatal(err)
	}
	if storedStatus != "running" || storedClaim != winners[0] {
		t.Fatalf("stored Broker claim status=%s claim=%s, want running/%s", storedStatus, storedClaim, winners[0])
	}

	if _, err := db.Pool.Exec(ctx, `update steward_remote_inbox set lease_expires_at=$2 where dispatch_id=$1`, dispatchID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	recoveredID, recovered, err := service.claimRemoteBrokerInbox(ctx, dispatchID, now)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered || recoveredID == storedClaim {
		t.Fatalf("expired Broker claim was not replaced: recovered=%v old=%s new=%s", recovered, storedClaim, recoveredID)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_remote_inbox
		set status='accepted',cancel_requested=true where dispatch_id=$1`, dispatchID); err != nil {
		t.Fatal(err)
	}
	if claimID, claimed, err := service.claimRemoteBrokerInbox(ctx, dispatchID, now.Add(time.Second)); err != nil || claimed || claimID != "" {
		t.Fatalf("cancelled Broker inbox was claimable: id=%s claimed=%v err=%v", claimID, claimed, err)
	}
}
