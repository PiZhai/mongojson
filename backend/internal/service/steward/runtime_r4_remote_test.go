package steward

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
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
		Steps:    []CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "ok"}}},
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute),
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
		TargetDeviceID: payload.TargetDeviceID, PlanHash: payload.PlanHash, Status: "succeeded", RemoteRunID: "run",
		HeartbeatAt: time.Now().UTC(), LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		Result: map[string]any{"manifest_sha256": "abc", "artifact_count": 2},
	}
	resultSignature := signRemotePayload(privateKey, status)
	status.Result["artifact_count"] = 3
	if err := verifyRemotePayload(publicKeyText, resultSignature, status); err == nil {
		t.Fatal("tampered remote result retained a valid signature")
	}
}
