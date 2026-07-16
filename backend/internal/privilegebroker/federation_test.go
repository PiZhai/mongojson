package privilegebroker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrokerToBrokerDelegationAndOpaqueCredentialProxy(t *testing.T) {
	executable, digest := testExecutable(t)
	authority, err := GenerateApprovalAuthorityKeys()
	if err != nil {
		t.Fatal(err)
	}
	originPublic, originPrivate, _ := ed25519.GenerateKey(rand.Reader)
	targetPublic, targetPrivate, _ := ed25519.GenerateKey(rand.Reader)
	credentialPath := filepath.Join(t.TempDir(), "credential.txt")
	if err := os.WriteFile(credentialPath, []byte("federated-secret-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	capability := func(credentials []string) Capability {
		return Capability{Name: "tool:test", Description: "federated helper", PermissionLevel: "A4", RiskLevel: "high",
			Executable: executable, ExecutableSHA256: digest,
			Arguments:        []string{"-test.run=TestPrivilegeBrokerHelper", "--", "broker-child"},
			WorkingDirectory: filepath.Dir(executable), TimeoutSeconds: 10, MaxOutputBytes: 4096,
			CredentialIDs: credentials, Enabled: true}
	}
	originPolicy := Policy{Version: 3,
		ApprovalAuthorities: []ApprovalAuthority{{Name: "authority", PublicKey: authority.PublicKey, Enabled: true}},
		Capabilities:        []Capability{capability(nil)},
		BrokerPeers: []BrokerPeer{{DeviceID: "target-device", Name: "target", PublicKey: base64.StdEncoding.EncodeToString(targetPublic),
			AllowedCapabilities: []string{"tool:test"}, AllowedCredentials: []string{"credential:test"}, Enabled: true}},
	}
	targetPolicy := Policy{Version: 3,
		ApprovalAuthorities: []ApprovalAuthority{{Name: "authority", PublicKey: authority.PublicKey, Enabled: true}},
		Capabilities:        []Capability{capability([]string{"credential:test"})},
		Credentials:         []BrokerCredential{{ID: "credential:test", Path: credentialPath, MaxBytes: 1024, Enabled: true}},
		BrokerPeers: []BrokerPeer{{DeviceID: "origin-device", Name: "origin", PublicKey: base64.StdEncoding.EncodeToString(originPublic),
			AllowedCapabilities: []string{"tool:test"}, AllowedCredentials: []string{"credential:test"}, Enabled: true}},
	}
	newFederatedBroker := func(deviceID string, policy Policy, privateKey ed25519.PrivateKey) (*Server, []byte) {
		dir := t.TempDir()
		policyPath := filepath.Join(dir, "policy.json")
		payload, _ := json.Marshal(policy)
		if err := os.WriteFile(policyPath, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		clientKey := make([]byte, 32)
		controlKey := make([]byte, 32)
		_, _ = rand.Read(clientKey)
		_, _ = rand.Read(controlKey)
		config := ServerConfig{DeviceID: deviceID, PolicyPath: policyPath, StatePath: filepath.Join(dir, "state.json"),
			AuditPath: filepath.Join(dir, "audit.jsonl"), CheckpointPath: filepath.Join(dir, "checkpoint.json"),
			ClientKey: clientKey, ControlKey: controlKey, SigningKey: privateKey}
		if err := InitializeCheckpoint(config); err != nil {
			t.Fatal(err)
		}
		server, err := NewServer(config)
		if err != nil {
			t.Fatal(err)
		}
		return server, clientKey
	}
	originBroker, originClientKey := newFederatedBroker("origin-device", originPolicy, originPrivate)
	targetBroker, targetClientKey := newFederatedBroker("target-device", targetPolicy, targetPrivate)
	originHTTP := httptest.NewServer(originBroker)
	defer originHTTP.Close()
	targetHTTP := httptest.NewServer(targetBroker)
	defer targetHTTP.Close()
	originClient, _ := NewClient(originHTTP.URL, originClientKey, originPublic)
	targetClient, _ := NewClient(targetHTTP.URL, targetClientKey, targetPublic)
	targetStatus, err := targetClient.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	proofID := make([]byte, 32)
	_, _ = rand.Read(proofID)
	now := time.Now().UTC()
	proof, err := IssueApprovalProof(authority.PrivateKey, ApprovalProofClaims{ProofID: hex.EncodeToString(proofID),
		Subject: "remote-broker:orchestration:node", PlanHash: strings.Repeat("a", 64), Capability: "tool:test",
		ControlGeneration: 0, GrantedBy: "operator", Reason: "federated execution", IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	request := BrokerDelegationRequest{
		TargetDeviceID: "target-device", TargetStatus: targetStatus, Capability: "tool:test",
		CredentialRefs: []string{"credential:test"}, Subject: proof.Claims.Subject, PlanHash: proof.Claims.PlanHash,
		ApprovalRef: proof.Claims.ProofID, ApprovalProof: proof, ControlGeneration: 0,
	}
	tamperedStatusRequest := request
	tamperedStatusRequest.TargetStatus.PolicyDigest = strings.Repeat("f", 64)
	if _, err := originClient.IssueDelegation(t.Context(), tamperedStatusRequest); err == nil {
		t.Fatal("origin Broker trusted a tampered target Broker status")
	}
	delegation, err := originClient.IssueDelegation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	originStatus, err := originClient.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tamperedOriginStatus := originStatus
	tamperedOriginStatus.Stopped = true
	if _, err := targetClient.ExecuteDelegation(t.Context(), delegation, tamperedOriginStatus); err == nil {
		t.Fatal("target Broker accepted a tampered online origin control status")
	}
	result, err := targetClient.ExecuteDelegation(t.Context(), delegation, originStatus)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "" || result.Stderr != "" || !result.Receipt.Payload.Succeeded ||
		result.Receipt.Payload.DelegationID != delegation.Claims.DelegationID ||
		len(result.Receipt.Payload.CredentialRefs) != 1 {
		t.Fatalf("credential-bound execution escaped its opaque receipt contract: %+v", result)
	}
	if err := VerifyDelegatedReceipt(base64.StdEncoding.EncodeToString(targetPublic), delegation, result.Receipt); err != nil {
		t.Fatalf("valid delegated receipt was rejected: %v", err)
	}
	tamperedReceipt := result.Receipt
	tamperedReceipt.Payload.Capability = "tool:other"
	if err := VerifyDelegatedReceipt(base64.StdEncoding.EncodeToString(targetPublic), delegation, tamperedReceipt); err == nil {
		t.Fatal("tampered delegated receipt was accepted")
	}
	if _, err := targetClient.ExecuteDelegation(t.Context(), delegation, originStatus); err == nil {
		t.Fatal("target Broker replayed a consumed delegation")
	}
	tampered := delegation
	tampered.Claims.Capability = "tool:other"
	if _, err := targetClient.ExecuteDelegation(t.Context(), tampered, originStatus); err == nil {
		t.Fatal("target Broker accepted a tampered source delegation")
	}
}
