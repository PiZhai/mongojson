package privilegebroker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivilegeBrokerHelper(t *testing.T) {
	if !argumentPresent("broker-child") {
		return
	}
	fmt.Print("broker-helper-ok")
}

func TestPrivilegeBrokerSlowHelper(t *testing.T) {
	if !argumentPresent("broker-slow") {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestBrokerIssuesSingleUseBoundGrantAndSignedReceipt(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	client.controlKey = fixture.config.ControlKey
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("read signed broker status: %v", err)
	}
	if status.Stopped || status.Generation != 0 || len(status.Capabilities) != 2 {
		t.Fatalf("unexpected initial broker status: %+v", status)
	}
	authorization := fixture.authorization(t, "tool:test", "runtime:run-1", strings.Repeat("a", 64), 0)
	grant, err := client.Grant(ctx, authorization)
	if err != nil {
		t.Fatalf("issue grant: %v", err)
	}
	if grant.Claims.ExpiresAt.Sub(grant.Claims.IssuedAt) != 30*time.Second {
		t.Fatalf("grant TTL = %s", grant.Claims.ExpiresAt.Sub(grant.Claims.IssuedAt))
	}
	result, err := client.Execute(ctx, grant)
	if err != nil {
		t.Fatalf("execute fixed capability: %v", err)
	}
	if !strings.Contains(result.Stdout, "broker-helper-ok") || !result.Receipt.Payload.Succeeded ||
		!result.Receipt.Payload.AuditPersisted || result.Receipt.Payload.ExitCode != 0 {
		t.Fatalf("unexpected broker result: %+v", result)
	}
	if _, err := client.Execute(ctx, grant); err == nil {
		t.Fatal("single-use capability token was replayed")
	} else {
		var brokerErr *BrokerError
		if !errors.As(err, &brokerErr) || brokerErr.Code != "token_replayed" {
			t.Fatalf("replay error = %v", err)
		}
	}
	if records := readAuditRecords(t, fixture.auditPath); len(records) < 4 || records[len(records)-1].PreviousHash == "" {
		t.Fatalf("signed audit chain is incomplete: %+v", records)
	}
}

func TestBrokerControlCancelsExecutionAndFencesOldGeneration(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	client.controlKey = fixture.config.ControlKey
	if err != nil {
		t.Fatal(err)
	}
	authorization := fixture.authorization(t, "tool:slow", "s4:proposal-1", strings.Repeat("b", 64), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, executeErr := client.ExecuteCapability(ctx, authorization)
		done <- executeErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, statusErr := client.Status(ctx)
		if statusErr == nil && status.ActiveExecutions == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow broker execution did not become active")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopped, err := client.SetControl(ctx, true, ControlRequest{Generation: 1, Reason: "test emergency stop", ChangedBy: "test"})
	if err != nil || !stopped.Stopped || stopped.Generation != 1 {
		t.Fatalf("stop broker: %+v %v", stopped, err)
	}
	select {
	case executeErr := <-done:
		if executeErr == nil {
			t.Fatal("emergency-stopped broker execution succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("emergency stop did not cancel broker execution")
	}
	auditCountAfterStop := len(readAuditRecords(t, fixture.auditPath))
	if _, err := client.SetControl(ctx, true, ControlRequest{Generation: 1, Reason: "duplicate synchronization", ChangedBy: "test"}); err != nil {
		t.Fatalf("repeat idempotent broker stop: %v", err)
	}
	if auditCount := len(readAuditRecords(t, fixture.auditPath)); auditCount != auditCountAfterStop {
		t.Fatalf("idempotent control synchronization appended audit records: before=%d after=%d", auditCountAfterStop, auditCount)
	}
	if _, err := client.Grant(ctx, authorization); err == nil {
		t.Fatal("stopped broker issued a new grant")
	}
	resumed, err := client.SetControl(ctx, false, ControlRequest{Generation: 2, Reason: "test resume", ChangedBy: "test"})
	if err != nil || resumed.Stopped || resumed.Generation != 2 {
		t.Fatalf("resume broker: %+v %v", resumed, err)
	}
	if _, err := client.Grant(ctx, authorization); err == nil {
		t.Fatal("old control generation was accepted after resume")
	}
}

func TestBrokerResumeRequiresIndependentControlKey(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	defer server.Close()

	executionClient, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executionClient.SetControl(context.Background(), true, ControlRequest{Generation: 1, Reason: "emergency", ChangedBy: "steward"}); err != nil {
		t.Fatalf("execution key must retain stop authority: %v", err)
	}
	if _, err := executionClient.SetControl(context.Background(), false, ControlRequest{Generation: 2, Reason: "unsafe resume", ChangedBy: "steward"}); err == nil || !strings.Contains(err.Error(), "independent control key") {
		t.Fatalf("execution key resumed broker: %v", err)
	}
	wrongKey := make([]byte, 32)
	_, _ = rand.Read(wrongKey)
	executionClient.controlKey = wrongKey
	if _, err := executionClient.SetControl(context.Background(), false, ControlRequest{Generation: 2, Reason: "wrong key", ChangedBy: "attacker"}); err == nil {
		t.Fatal("wrong control key resumed broker")
	}
	executionClient.controlKey = fixture.config.ControlKey
	if status, err := executionClient.SetControl(context.Background(), false, ControlRequest{Generation: 2, Reason: "operator verified", ChangedBy: "local-admin"}); err != nil || status.Stopped {
		t.Fatalf("independent control key could not resume broker: %+v %v", status, err)
	}
}

func TestBrokerResumeAuditFailureKeepsStoppedState(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	client.controlKey = fixture.config.ControlKey
	if _, err := client.SetControl(context.Background(), true, ControlRequest{Generation: 1, Reason: "safe stop", ChangedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.config.CheckpointPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fixture.config.CheckpointPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetControl(context.Background(), false, ControlRequest{Generation: 2, Reason: "must remain stopped", ChangedBy: "operator"}); err == nil {
		t.Fatal("resume succeeded after checkpoint persistence failure")
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Stopped || status.Generation != 1 {
		t.Fatalf("resume audit failure exposed unsafe state: %+v", status)
	}
	state, _, err := loadBrokerState(fixture.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Stopped || state.Generation != 1 {
		t.Fatalf("durable state was not rolled back: %+v", state)
	}
}

func TestBrokerStopPersistenceFailureLatchesInMemoryStop(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	defer server.Close()
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.config.StatePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fixture.config.StatePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetControl(context.Background(), true, ControlRequest{Generation: 1, Reason: "disk failure stop", ChangedBy: "test"}); err == nil {
		t.Fatal("stop unexpectedly reported durable success")
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Stopped || status.Generation != 1 {
		t.Fatalf("failed stop persistence did not latch memory stop: %+v", status)
	}
}

func TestBrokerCheckpointFailsClosedOnMissingOrTruncatedAudit(t *testing.T) {
	fixture := newBrokerFixture(t)
	if err := os.Remove(fixture.config.CheckpointPath); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(fixture.config); err == nil || !strings.Contains(err.Error(), "checkpoint is missing") {
		t.Fatalf("missing checkpoint was accepted: %v", err)
	}

	fixture = newBrokerFixture(t)
	payload, err := os.ReadFile(fixture.auditPath)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimSuffix(payload, []byte{'\n'})
	last := bytes.LastIndex(trimmed, []byte{'\n'})
	truncated := []byte{}
	if last >= 0 {
		truncated = payload[:last+1]
	}
	if err := os.WriteFile(fixture.auditPath, truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(fixture.config); err == nil || !strings.Contains(err.Error(), "behind or inconsistent") {
		t.Fatalf("truncated signed audit was accepted: %v", err)
	}
}

func TestBrokerCheckpointFailsClosedOnStateRollback(t *testing.T) {
	fixture := newBrokerFixture(t)
	initialState, err := os.ReadFile(fixture.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture.server)
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetControl(context.Background(), true, ControlRequest{Generation: 1, Reason: "checkpoint state", ChangedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := os.WriteFile(fixture.config.StatePath, initialState, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(fixture.config); err == nil || !strings.Contains(err.Error(), "behind or inconsistent") {
		t.Fatalf("rolled-back broker state was accepted: %v", err)
	}
}

func TestBrokerResumeAuditFailureRollsBackToStoppedState(t *testing.T) {
	fixture := newBrokerFixture(t)
	if err := fixture.server.updateControl(true, ControlRequest{Generation: 1, Reason: "emergency", ChangedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.config.CheckpointPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(fixture.config.CheckpointPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := fixture.server.updateControl(false, ControlRequest{Generation: 2, Reason: "resume", ChangedBy: "operator"}); err == nil {
		t.Fatal("resume succeeded without a durable audit checkpoint")
	}
	state := fixture.server.currentState()
	if !state.Stopped || state.Generation != 1 {
		t.Fatalf("failed resume escaped stopped state: %+v", state)
	}
	durable, _, err := loadBrokerState(fixture.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !durable.Stopped || durable.Generation != 1 {
		t.Fatalf("failed resume persisted unsafe state: %+v", durable)
	}
}

func TestBrokerRejectsApprovalProofReplayAcrossRestart(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	client, err := NewClient(server.URL, fixture.clientKey, fixture.publicKey)
	client.controlKey = fixture.config.ControlKey
	if err != nil {
		t.Fatal(err)
	}
	authorization := fixture.authorization(t, "tool:test", "runtime:replay-run", strings.Repeat("e", 64), 0)
	if _, err := client.Grant(context.Background(), authorization); err != nil {
		t.Fatalf("issue first grant: %v", err)
	}
	if _, err := client.Grant(context.Background(), authorization); err == nil {
		t.Fatal("replayed approval proof issued a second grant")
	} else {
		var brokerErr *BrokerError
		if !errors.As(err, &brokerErr) || brokerErr.Code != "approval_proof_replayed" {
			t.Fatalf("approval proof replay error = %v", err)
		}
	}
	server.Close()

	restarted, err := NewServer(fixture.config)
	if err != nil {
		t.Fatalf("restart broker from signed audit: %v", err)
	}
	restartedServer := httptest.NewServer(restarted)
	defer restartedServer.Close()
	restartedClient, err := NewClient(restartedServer.URL, fixture.clientKey, fixture.publicKey)
	restartedClient.controlKey = fixture.config.ControlKey
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restartedClient.Grant(context.Background(), authorization); err == nil {
		t.Fatal("broker restart forgot a consumed approval proof")
	} else {
		var brokerErr *BrokerError
		if !errors.As(err, &brokerErr) || brokerErr.Code != "approval_proof_replayed" {
			t.Fatalf("post-restart approval proof replay error = %v", err)
		}
	}
}

func TestApprovalProofRejectsTamperExpiryAndBindingMismatch(t *testing.T) {
	fixture := newBrokerFixture(t)
	authorization := fixture.authorization(t, "tool:test", "runtime:proof-run", strings.Repeat("f", 64), 3)
	authorities := fixture.server.policy.PublicApprovalAuthorities()
	expected := ApprovalProofExpectation{
		Subject: authorization.Subject, PlanHash: authorization.PlanHash, Capability: authorization.Capability,
		ControlGeneration: authorization.ControlGeneration, Reason: "test approval",
	}
	if err := VerifyApprovalProof(authorities, authorization.ApprovalProof, expected, time.Now().UTC()); err != nil {
		t.Fatalf("valid approval proof was rejected: %v", err)
	}
	if err := VerifyApprovalProof(nil, authorization.ApprovalProof, expected, time.Now().UTC()); err == nil {
		t.Fatal("approval proof remained valid after its authority was removed from policy")
	}
	tampered := authorization.ApprovalProof
	tampered.Claims.Capability = "tool:slow"
	if err := VerifyApprovalProof(authorities, tampered, expected, time.Now().UTC()); err == nil {
		t.Fatal("tampered approval proof was accepted")
	}
	if err := VerifyApprovalProof(authorities, authorization.ApprovalProof, expected, authorization.ApprovalProof.Claims.ExpiresAt); err == nil {
		t.Fatal("expired approval proof was accepted")
	}
	expected.ControlGeneration++
	if err := VerifyApprovalProof(authorities, authorization.ApprovalProof, expected, time.Now().UTC()); err == nil {
		t.Fatal("approval proof was accepted for a different control generation")
	}
}

func TestBrokerRequestNonceAndAuditChainRejectReplayAndTamper(t *testing.T) {
	fixture := newBrokerFixture(t)
	server := httptest.NewServer(fixture.server)
	client := &http.Client{Timeout: 5 * time.Second}
	nonce := strings.Repeat("n", 32)
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	makeRequest := func() *http.Request {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/status", nil)
		request.Header.Set(HeaderTimestamp, timestamp)
		request.Header.Set(HeaderNonce, nonce)
		request.Header.Set(HeaderSignature, requestSignature(fixture.clientKey, http.MethodGet, "/v1/status", timestamp, nonce, nil))
		return request
	}
	first, err := client.Do(makeRequest())
	if err != nil || first.StatusCode != http.StatusOK {
		t.Fatalf("first signed status request = %v %v", first, err)
	}
	_ = first.Body.Close()
	second, err := client.Do(makeRequest())
	if err != nil || second.StatusCode != http.StatusConflict {
		t.Fatalf("replayed status request = %v %v", second, err)
	}
	_ = second.Body.Close()
	server.Close()
	file, err := os.OpenFile(fixture.auditPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("{\"tampered\":true}\n")
	_ = file.Close()
	if _, err := NewServer(fixture.config); err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("tampered broker audit was accepted: %v", err)
	}
}

func TestPolicyRejectsCredentialAuthorityAndBrokerEnvironmentSecrets(t *testing.T) {
	executable, digest := testExecutable(t)
	authority, err := GenerateApprovalAuthorityKeys()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidatePolicy(Policy{Version: 2, ApprovalAuthorities: []ApprovalAuthority{{Name: "test", PublicKey: authority.PublicKey, Enabled: true}}, Capabilities: []Capability{{
		Name: "tool:credential", PermissionLevel: "A8", RiskLevel: "critical",
		Executable: executable, ExecutableSHA256: digest, Enabled: true,
	}}})
	if err == nil || !strings.Contains(err.Error(), "A8-A9") {
		t.Fatalf("A8 capability was accepted: %v", err)
	}
	t.Setenv("STEWARD_BROKER_SIGNING_PRIVATE_KEY", "must-not-leak")
	t.Setenv("STEWARD_BROKER_CLIENT_KEY", "must-not-leak")
	environment := strings.Join(brokerEnvironment(), "\n")
	if strings.Contains(environment, "STEWARD_BROKER_") {
		t.Fatalf("broker child inherited service secrets: %s", environment)
	}
}

type brokerFixture struct {
	server             *Server
	config             ServerConfig
	clientKey          []byte
	publicKey          ed25519.PublicKey
	auditPath          string
	approvalPrivateKey string
}

func newBrokerFixture(t *testing.T) brokerFixture {
	t.Helper()
	executable, digest := testExecutable(t)
	dir := t.TempDir()
	authority, err := GenerateApprovalAuthorityKeys()
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{Version: 2, ApprovalAuthorities: []ApprovalAuthority{{Name: "test-authority", PublicKey: authority.PublicKey, Enabled: true}}, Capabilities: []Capability{
		{Name: "tool:test", Description: "test helper", PermissionLevel: "A4", RiskLevel: "high", Executable: executable, ExecutableSHA256: digest, Arguments: []string{"-test.run=TestPrivilegeBrokerHelper", "--", "broker-child"}, WorkingDirectory: filepath.Dir(executable), TimeoutSeconds: 10, MaxOutputBytes: 4096, Enabled: true},
		{Name: "tool:slow", Description: "slow helper", PermissionLevel: "A5", RiskLevel: "critical", Executable: executable, ExecutableSHA256: digest, Arguments: []string{"-test.run=TestPrivilegeBrokerSlowHelper", "--", "broker-slow"}, WorkingDirectory: filepath.Dir(executable), TimeoutSeconds: 30, MaxOutputBytes: 4096, Enabled: true},
	}}
	policyPath := filepath.Join(dir, "policy.json")
	payload, _ := json.Marshal(policy)
	if err := os.WriteFile(policyPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	clientKey := make([]byte, 32)
	_, _ = rand.Read(clientKey)
	controlKey := make([]byte, 32)
	_, _ = rand.Read(controlKey)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := ServerConfig{
		PolicyPath: policyPath, StatePath: filepath.Join(dir, "state.json"), AuditPath: filepath.Join(dir, "audit.jsonl"), CheckpointPath: filepath.Join(dir, "checkpoint.json"),
		ClientKey: clientKey, ControlKey: controlKey, SigningKey: privateKey, GrantTTL: 30 * time.Second, RequestSkew: 30 * time.Second,
	}
	if err := InitializeCheckpoint(config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	return brokerFixture{server: server, config: config, clientKey: clientKey, publicKey: publicKey, auditPath: config.AuditPath, approvalPrivateKey: authority.PrivateKey}
}

func (f brokerFixture) authorization(t *testing.T, capability, subject, planHash string, generation int64) Authorization {
	t.Helper()
	proofBytes := make([]byte, 32)
	if _, err := rand.Read(proofBytes); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	proof, err := IssueApprovalProof(f.approvalPrivateKey, ApprovalProofClaims{
		ProofID: hex.EncodeToString(proofBytes), Subject: subject, PlanHash: planHash, Capability: capability,
		ControlGeneration: generation, GrantedBy: "test-operator", Reason: "test approval",
		IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return Authorization{Capability: capability, Subject: subject, PlanHash: planHash, ApprovalRef: proof.Claims.ProofID, ApprovalProof: proof, ControlGeneration: generation}
}

func testExecutable(t *testing.T) (string, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, _ = filepath.Abs(executable)
	digest, err := hashFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	return executable, digest
}

func readAuditRecords(t *testing.T, path string) []AuditRecord {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	records := make([]AuditRecord, 0, len(lines))
	for _, line := range lines {
		var record AuditRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func argumentPresent(value string) bool {
	for _, argument := range os.Args {
		if argument == value {
			return true
		}
	}
	return false
}
