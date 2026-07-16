package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
	"mongojson/backend/internal/service/steward"
)

func TestStewardRuntimeV2HTTPExecutesDurablePlanWithRetryEvidenceAndSSE(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime v2 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flaky := &runtimeV2FlakyTool{}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_v2"), "runtime-node",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeTool(flaky))

	body := map[string]any{
		"goal":               "prove durable R1 execution",
		"idempotency_key":    "runtime-v2-e2e",
		"permission_ceiling": "A0",
		"auto_start":         true,
		"steps": []map[string]any{
			{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "ready"}, "expected_output": map[string]any{"value": "ready"}},
			{"key": "retry", "tool_name": "runtime.test.flaky", "arguments": map[string]any{"value": "done"}, "expected_output": map[string]any{"value": "done"}, "depends_on": []string{"echo"}, "max_attempts": 2},
		},
	}
	created := postRuntimeV2Run(t, ctx, node, body, http.StatusCreated)
	if created.Status != steward.RuntimeRunQueued {
		t.Fatalf("created run status = %s, want queued", created.Status)
	}

	processed, err := node.service.RunAgentRuntimeCycle(ctx, 10)
	if err != nil || processed != 1 {
		t.Fatalf("first runtime cycle processed=%d err=%v", processed, err)
	}
	afterRetry, err := node.service.GetAgentRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("get run after retry: %v", err)
	}
	if afterRetry.Status != steward.RuntimeRunQueued || afterRetry.Steps[1].Attempt != 1 {
		t.Fatalf("first failure was not durably requeued: %+v", afterRetry)
	}
	processed, err = node.service.RunAgentRuntimeCycle(ctx, 10)
	if err != nil || processed != 1 {
		t.Fatalf("second runtime cycle processed=%d err=%v", processed, err)
	}
	completed := getRuntimeV2Run(t, ctx, node, created.ID)
	if completed.Status != steward.RuntimeRunSucceeded {
		t.Fatalf("completed run status = %s, want succeeded: %+v", completed.Status, completed)
	}
	if len(completed.Steps) != 2 || len(completed.Steps[1].Invocations) != 2 || len(completed.Steps[1].Evidence) < 2 {
		t.Fatalf("retry invocation/evidence history was not preserved: %+v", completed.Steps)
	}

	replayed := postRuntimeV2Run(t, ctx, node, body, http.StatusCreated)
	if replayed.ID != created.ID {
		t.Fatalf("idempotent replay returned run %s, want %s", replayed.ID, created.ID)
	}
	conflictBody := cloneRuntimeV2Body(t, body)
	conflictBody["goal"] = "different plan under the same idempotency key"
	postRuntimeV2Run(t, ctx, node, conflictBody, http.StatusConflict)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, node.apiBase+"/steward/runs/"+created.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("stream terminal run events: %v", err)
	}
	defer response.Body.Close()
	stream, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read run event stream: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(stream, []byte("event: run.succeeded")) || !bytes.Contains(stream, []byte("event: step.retry_scheduled")) {
		t.Fatalf("unexpected SSE response status=%d body=%s", response.StatusCode, stream)
	}
}

func TestStewardRuntimeV2ApprovalCancellationAndResume(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime v2 approval integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waitTool := &runtimeV2WaitTool{started: make(chan struct{}, 4)}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_v2_approval"), "runtime-approval-node",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeTool(waitTool))
	body := map[string]any{
		"goal":       "verify approval binding",
		"auto_start": true,
		"steps": []map[string]any{{
			"key": "approved", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "approved"},
			"expected_output": map[string]any{"value": "approved"}, "requires_approval": true,
		}},
	}
	run := postRuntimeV2Run(t, ctx, node, body, http.StatusCreated)
	if run.Status != steward.RuntimeRunAwaitingApproval {
		t.Fatalf("approval run status = %s, want awaiting_approval", run.Status)
	}
	postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{"plan_hash": "wrong"}, http.StatusConflict)
	run = postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{"plan_hash": run.PlanHash, "granted_by": "e2e-user"}, http.StatusOK)
	if run.Status != steward.RuntimeRunQueued || len(run.Approvals) != 1 {
		t.Fatalf("approved run was not queued: %+v", run)
	}
	run = postRuntimeV2Action(t, ctx, node, run.ID, "cancel", nil, http.StatusOK)
	if run.Status != steward.RuntimeRunCancelled {
		t.Fatalf("cancelled run status = %s", run.Status)
	}
	run = postRuntimeV2Action(t, ctx, node, run.ID, "resume", nil, http.StatusOK)
	if run.Status != steward.RuntimeRunQueued {
		t.Fatalf("resumed run status = %s, want queued", run.Status)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute resumed run: %v", err)
	}
	if final := getRuntimeV2Run(t, ctx, node, run.ID); final.Status != steward.RuntimeRunSucceeded {
		t.Fatalf("resumed run final status = %s, want succeeded", final.Status)
	}

	waitRun := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "cancel an active invocation", "auto_start": true,
		"steps": []map[string]any{{"key": "wait", "tool_name": "runtime.test.wait", "arguments": map[string]any{"value": "never"}, "timeout_seconds": 5}},
	}, http.StatusCreated)
	cycleDone := make(chan error, 1)
	go func() {
		_, err := node.service.RunAgentRuntimeCycle(ctx, 1)
		cycleDone <- err
	}()
	select {
	case <-waitTool.started:
	case <-time.After(3 * time.Second):
		t.Fatal("wait tool did not start")
	}
	cancelling := postRuntimeV2Action(t, ctx, node, waitRun.ID, "cancel", nil, http.StatusOK)
	if !cancelling.CancelRequested || cancelling.Status != steward.RuntimeRunRunning {
		t.Fatalf("running cancellation was not persisted: %+v", cancelling)
	}
	select {
	case err := <-cycleDone:
		if err != nil {
			t.Fatalf("cancelled runtime cycle: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active tool did not observe cooperative cancellation")
	}
	cancelled := getRuntimeV2Run(t, ctx, node, waitRun.ID)
	if cancelled.Status != steward.RuntimeRunCancelled || cancelled.Steps[0].Invocations[0].Status != steward.RuntimeStepCancelled {
		t.Fatalf("active invocation was not cancelled: %+v", cancelled)
	}

	timeoutRun := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "enforce a tool deadline", "auto_start": true,
		"steps": []map[string]any{{"key": "timeout", "tool_name": "runtime.test.wait", "arguments": map[string]any{"value": "never"}, "timeout_seconds": 1}},
	}, http.StatusCreated)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("run timeout cycle: %v", err)
	}
	timedOut := getRuntimeV2Run(t, ctx, node, timeoutRun.ID)
	if timedOut.Status != steward.RuntimeRunFailed || !strings.Contains(timedOut.FailureSummary, "deadline exceeded") {
		t.Fatalf("tool timeout did not fail durably: %+v", timedOut)
	}
}

func TestStewardRuntimeR25GlobalPauseControlsQueueAndBlocksUnknownNonIdempotentOutcome(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime R2.5 control-plane integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nonIdempotent := &runtimeR25NonIdempotentWaitTool{started: make(chan struct{}, 1)}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r25_control"), "runtime-r25-node",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeTool(nonIdempotent))

	queued := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "hold safe work behind the global pause", "auto_start": true,
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "after-resume"}}},
	}, http.StatusCreated)
	control := postRuntimeR25Control(t, ctx, node, "pause", map[string]any{"reason": "operator is inspecting the queue"})
	if !control.Paused || !control.Stopped || control.Generation != 1 || control.Reason != "operator is inspecting the queue" || len(control.Events) != 1 || control.Events[0].Action != "stopped" {
		t.Fatalf("global pause was not persisted and audited: %+v", control)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil || processed != 0 {
		t.Fatalf("paused runtime claimed queued work: processed=%d err=%v", processed, err)
	}
	autonomy, err := node.service.RunAutonomyCycle(ctx, 1)
	if err != nil || len(autonomy.Runs) == 0 || autonomy.Runs[0].Status != steward.RunBlocked || !strings.Contains(autonomy.Runs[0].TriggerReason, "emergency stop") {
		t.Fatalf("unified stop did not gate S4 autonomy: autonomy=%+v err=%v", autonomy, err)
	}
	summaries := listRuntimeR25Runs(t, ctx, node)
	if len(summaries) != 1 || summaries[0].ID != queued.ID || summaries[0].StepCount != 1 || summaries[0].CompletedSteps != 0 {
		t.Fatalf("run control list did not expose queue progress: %+v", summaries)
	}
	control = postRuntimeR25Control(t, ctx, node, "resume", map[string]any{"reason": "inspection complete"})
	if control.Paused || control.Stopped || control.Generation != 2 || len(control.Events) != 2 || control.Events[0].Action != "resumed" {
		t.Fatalf("global resume was not persisted and audited: %+v", control)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil || processed != 1 {
		t.Fatalf("resumed runtime did not execute queued work: processed=%d err=%v", processed, err)
	}
	if completed := getRuntimeV2Run(t, ctx, node, queued.ID); completed.Status != steward.RuntimeRunSucceeded {
		t.Fatalf("queued work did not finish after global resume: %+v", completed)
	}

	dangerous := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "interrupt a non-idempotent operating-system action", "auto_start": true, "permission_ceiling": "A3",
		"steps": []map[string]any{{"key": "process", "tool_name": "runtime.test.non_idempotent_wait", "arguments": map[string]any{"value": "unknown"}}},
	}, http.StatusCreated)
	if dangerous.Status != steward.RuntimeRunAwaitingApproval {
		t.Fatalf("non-idempotent run bypassed approval: %+v", dangerous)
	}
	dangerous = postRuntimeV2Action(t, ctx, node, dangerous.ID, "approve", map[string]any{
		"plan_hash": dangerous.PlanHash, "granted_by": "r25-e2e", "reason": "exercise the emergency stop",
	}, http.StatusOK)
	cycleDone := make(chan error, 1)
	go func() {
		_, err := node.service.RunAgentRuntimeCycle(ctx, 1)
		cycleDone <- err
	}()
	select {
	case <-nonIdempotent.started:
	case <-time.After(3 * time.Second):
		t.Fatal("non-idempotent tool did not start")
	}
	postRuntimeR25Control(t, ctx, node, "pause", map[string]any{"reason": "emergency operator stop"})
	select {
	case err := <-cycleDone:
		if err != nil {
			t.Fatalf("pause interrupted runtime cycle with an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active non-idempotent tool did not observe the global pause")
	}
	blocked := getRuntimeV2Run(t, ctx, node, dangerous.ID)
	if blocked.Status != steward.RuntimeRunBlocked || blocked.Steps[0].Status != steward.RuntimeStepBlocked || blocked.Approvals[0].Status != "revoked" {
		t.Fatalf("unknown non-idempotent outcome remained replayable: %+v", blocked)
	}
	foundPauseBlock := false
	for _, event := range runtimeR25Events(t, ctx, node, dangerous.ID) {
		if event.Type == "run.pause_blocked" {
			foundPauseBlock = true
		}
	}
	if !foundPauseBlock {
		t.Fatal("run.pause_blocked evidence event was not recorded")
	}
}

func TestStewardRuntimeR26EvidenceGovernanceAndWatchdogLeases(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime R2.6 safety integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "runtime-r26-test")
	nonIdempotent := &runtimeR25NonIdempotentWaitTool{started: make(chan struct{}, 1)}
	liveLeaseTool := &runtimeV2WaitTool{started: make(chan struct{}, 1)}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r26_safety"), "runtime-r26-node",
		steward.WithRuntimeV2Enabled(true), steward.WithRuntimeTool(nonIdempotent), steward.WithRuntimeTool(liveLeaseTool),
		steward.WithRuntimeEvidenceMaxBytes(64), steward.WithRuntimeLeaseTTL(10*time.Second))

	sensitive := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "persist protected evidence", "auto_start": true, "data_level": "D4",
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "protected"}}},
	}, http.StatusCreated)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute protected evidence run: %v", err)
	}
	sensitive = getRuntimeV2Run(t, ctx, node, sensitive.ID)
	protected := getRuntimeV2EvidenceByKind(t, ctx, node, sensitive, "tool_result")
	if protected.PayloadState != "encrypted" || protected.Payload["value"] != "protected" {
		t.Fatalf("D4 evidence was not encrypted at rest and revealable locally: %+v", protected)
	}

	oversized := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "drop oversized evidence body", "auto_start": true, "data_level": "D2",
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": strings.Repeat("x", 512)}}},
	}, http.StatusCreated)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute oversized evidence run: %v", err)
	}
	oversized = getRuntimeV2Run(t, ctx, node, oversized.ID)
	var oversizedEvidence domain.StewardEvidenceArtifact
	for _, evidence := range oversized.Steps[0].Evidence {
		if evidence.Kind == "tool_result" {
			oversizedEvidence = evidence
		}
	}
	if oversizedEvidence.PayloadState != "summary_only" || oversizedEvidence.PayloadAvailable || oversizedEvidence.SizeBytes <= 64 || oversizedEvidence.SHA256 == "" {
		t.Fatalf("oversized evidence body was retained: %+v", oversizedEvidence)
	}

	live := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "watchdog preserves a live lease", "auto_start": true,
		"steps": []map[string]any{{"key": "wait", "tool_name": "runtime.test.wait", "arguments": map[string]any{"value": "live"}, "timeout_seconds": 5}},
	}, http.StatusCreated)
	liveDone := make(chan error, 1)
	go func() {
		_, err := node.service.RunAgentRuntimeCycle(ctx, 1)
		liveDone <- err
	}()
	select {
	case <-liveLeaseTool.started:
	case <-time.After(3 * time.Second):
		t.Fatal("live lease tool did not start")
	}
	var liveLeaseExpiry time.Time
	if err := node.pool.QueryRow(ctx, `select lease_expires_at from steward_tool_invocations where run_id = $1 and status = 'running'`, live.ID).Scan(&liveLeaseExpiry); err != nil {
		t.Fatalf("read live worker lease: %v", err)
	}
	if !liveLeaseExpiry.After(time.Now()) {
		t.Fatalf("new worker lease was already expired: expiry=%s now=%s", liveLeaseExpiry, time.Now())
	}
	if recovered, err := node.service.RunAgentRuntimeWatchdog(ctx, 10); err != nil || recovered != 0 {
		t.Fatalf("watchdog stole a live worker lease: recovered=%d err=%v", recovered, err)
	}
	postRuntimeV2Action(t, ctx, node, live.ID, "cancel", nil, http.StatusOK)
	select {
	case err := <-liveDone:
		if err != nil {
			t.Fatalf("cancel live lease tool: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("live lease tool did not stop")
	}

	safe := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "watchdog recovers replay-safe work", "auto_start": true,
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "retry"}}},
	}, http.StatusCreated)
	dangerous := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "watchdog blocks unknown non-idempotent work", "auto_start": true, "permission_ceiling": "A3",
		"steps": []map[string]any{{"key": "process", "tool_name": "runtime.test.non_idempotent_wait", "arguments": map[string]any{"value": "unknown"}}},
	}, http.StatusCreated)
	dangerous = postRuntimeV2Action(t, ctx, node, dangerous.ID, "approve", map[string]any{
		"plan_hash": dangerous.PlanHash, "granted_by": "r26-watchdog", "reason": "watchdog fencing test",
	}, http.StatusOK)
	seedExpiredRuntimeInvocation(t, ctx, node, safe)
	seedExpiredRuntimeInvocation(t, ctx, node, dangerous)
	if recovered, err := node.service.RunAgentRuntimeWatchdog(ctx, 10); err != nil || recovered != 2 {
		t.Fatalf("watchdog recovered=%d err=%v", recovered, err)
	}
	safe = getRuntimeV2Run(t, ctx, node, safe.ID)
	if safe.Status != steward.RuntimeRunQueued || safe.Steps[0].Status != steward.RuntimeStepPending || safe.Steps[0].Invocations[0].Status != steward.RuntimeStepFailed {
		t.Fatalf("watchdog did not requeue replay-safe work: %+v", safe)
	}
	dangerous = getRuntimeV2Run(t, ctx, node, dangerous.ID)
	if dangerous.Status != steward.RuntimeRunBlocked || dangerous.Steps[0].Status != steward.RuntimeStepBlocked || dangerous.Approvals[0].Status != "revoked" {
		t.Fatalf("watchdog left unknown non-idempotent work replayable: %+v", dangerous)
	}
	if !runtimeEventTypePresent(t, ctx, node, safe.ID, "run.watchdog_recovered") || !runtimeEventTypePresent(t, ctx, node, dangerous.ID, "run.watchdog_blocked") {
		t.Fatal("watchdog evidence events were not persisted")
	}
}

func TestStewardRuntimeR31PrivilegeBrokerRequiresIndependentApprovalProof(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime R3.1 broker integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	broker := newRuntimeR3TestBroker(t)
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r31_broker"), "runtime-r31-node",
		steward.WithRuntimeR2Enabled(true), steward.WithRuntimeR3Enabled(true), steward.WithPrivilegeBrokerClient(broker))

	planned := postRuntimeR2Plan(t, ctx, node, map[string]any{
		"instruction": "执行高权限能力 tool:test-system-action", "permission_ceiling": "A7", "auto_start": false,
	}, http.StatusCreated)
	if planned.Status != steward.RuntimeRunDraft || len(planned.Steps) != 1 ||
		planned.Steps[0].ToolName != "privilege.execute" || planned.Steps[0].Arguments["capability"] != "tool:test-system-action" ||
		!planned.Steps[0].RequiresApproval {
		t.Fatalf("natural-language R3 plan was not compiled into an approval-bound Broker step: %+v", planned)
	}

	run := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "execute one isolated privileged capability", "auto_start": true, "permission_ceiling": "A7",
		"steps": []map[string]any{{
			"key": "privileged", "tool_name": "privilege.execute",
			"arguments":       map[string]any{"capability": "tool:test-system-action"},
			"expected_output": map[string]any{"stdout_contains": "broker-e2e-ok"},
		}},
	}, http.StatusCreated)
	if run.Status != steward.RuntimeRunAwaitingApproval {
		t.Fatalf("R3 run status=%s, want awaiting approval", run.Status)
	}
	runReason := "explicit privileged execution test"
	postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{
		"plan_hash": run.PlanHash, "granted_by": "r3-e2e-user", "reason": runReason,
	}, http.StatusBadRequest)
	runProof := broker.approvalProof(t, "runtime:"+run.ID, run.PlanHash, "tool:test-system-action", 0, "r3-e2e-user", runReason)
	tamperedProof := runProof
	tamperedProof.Claims.Capability = "tool:tampered"
	postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{
		"plan_hash": run.PlanHash, "granted_by": "r3-e2e-user", "reason": runReason, "approval_proof": tamperedProof,
	}, http.StatusBadRequest)
	run = postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{
		"plan_hash": run.PlanHash, "granted_by": "r3-e2e-user", "reason": runReason, "approval_proof": runProof,
	}, http.StatusOK)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute R3 broker run: %v", err)
	}
	run = getRuntimeV2Run(t, ctx, node, run.ID)
	if run.Status != steward.RuntimeRunSucceeded {
		t.Fatalf("R3 broker run did not succeed: %+v", run)
	}
	authorization := broker.lastAuthorization()
	if authorization.Capability != "tool:test-system-action" || authorization.Subject != "runtime:"+run.ID ||
		authorization.PlanHash != run.PlanHash || authorization.ApprovalRef != run.Approvals[0].ApprovalProofID ||
		authorization.ApprovalProof.Claims.ProofID != run.Approvals[0].ApprovalProofID || authorization.ControlGeneration != 0 {
		t.Fatalf("R3 grant was not bound to run approval and generation: %+v run=%+v", authorization, run)
	}
	receiptEvidence := getRuntimeV2EvidenceByKind(t, ctx, node, run, "privilege_broker_receipt")
	if receiptEvidence.Payload["receipt"] == nil || receiptEvidence.Payload["capability"] != "tool:test-system-action" {
		t.Fatalf("signed broker receipt evidence was not governed and revealable: %+v", receiptEvidence)
	}

	broker.setAuditFailure(true)
	failed := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "retain a signed receipt when broker audit persistence fails", "auto_start": true, "permission_ceiling": "A7",
		"steps": []map[string]any{{
			"key": "audit-failure", "tool_name": "privilege.execute",
			"arguments": map[string]any{"capability": "tool:test-system-action"},
		}},
	}, http.StatusCreated)
	failedReason := "audit failure evidence test"
	failedProof := broker.approvalProof(t, "runtime:"+failed.ID, failed.PlanHash, "tool:test-system-action", 0, "r3-e2e-user", failedReason)
	failed = postRuntimeV2Action(t, ctx, node, failed.ID, "approve", map[string]any{
		"plan_hash": failed.PlanHash, "granted_by": "r3-e2e-user", "reason": failedReason, "approval_proof": failedProof,
	}, http.StatusOK)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute R3 audit failure run: %v", err)
	}
	failed = getRuntimeV2Run(t, ctx, node, failed.ID)
	if failed.Status != steward.RuntimeRunFailed || !strings.Contains(failed.FailureSummary, "audit was not persisted") {
		t.Fatalf("R3 audit failure was not surfaced as a failed non-idempotent run: %+v", failed)
	}
	failedReceipt := getRuntimeV2EvidenceByKind(t, ctx, node, failed, "privilege_broker_receipt")
	receiptPayload, _ := failedReceipt.Payload["receipt"].(map[string]any)
	signedPayload, _ := receiptPayload["payload"].(map[string]any)
	if auditPersisted, _ := signedPayload["audit_persisted"].(bool); auditPersisted {
		t.Fatalf("failed broker audit receipt did not preserve the signed failure evidence: %+v", failedReceipt)
	}
	broker.setAuditFailure(false)

	control := postRuntimeR25Control(t, ctx, node, "pause", map[string]any{"reason": "R3 unified stop test", "changed_by": "r3-e2e-user"})
	if !control.Stopped || !control.Broker.Configured || !control.Broker.Reachable || !control.Broker.Stopped || control.Broker.Generation != control.Generation {
		t.Fatalf("unified stop did not synchronize Broker: %+v", control)
	}
}

func TestStewardRuntimeV2RecoversInterruptedInvocation(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime v2 recovery integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_v2_recovery"), "runtime-recovery-node",
		steward.WithRuntimeV2Enabled(true))
	run := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "recover an interrupted worker", "auto_start": true,
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "recovered"}}},
	}, http.StatusCreated)
	step := run.Steps[0]
	now := time.Now().UTC()
	if _, err := node.pool.Exec(ctx, `update steward_agent_runs set status = $2, started_at = $3, updated_at = $3 where id = $1`, run.ID, steward.RuntimeRunRunning, now); err != nil {
		t.Fatalf("seed interrupted run: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `update steward_run_steps set status = $2, attempt = 1, started_at = $3, updated_at = $3 where id = $1`, step.ID, steward.RuntimeStepRunning, now); err != nil {
		t.Fatalf("seed interrupted step: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `
		insert into steward_tool_invocations (
			id, run_id, step_id, tool_name, tool_version, attempt, idempotency_key, status, input, output, started_at
		) values ($1,$2,$3,'runtime.echo','1.0.0',1,$4,$5,'{"value":"recovered"}'::jsonb,'{}'::jsonb,$6)
	`, uuid.NewString(), run.ID, step.ID, step.IdempotencyKey+":1", steward.RuntimeStepRunning, now); err != nil {
		t.Fatalf("seed interrupted invocation: %v", err)
	}
	recoveredCount, err := node.service.RecoverAgentRuntime(ctx)
	if err != nil || recoveredCount != 1 {
		t.Fatalf("recover interrupted runtime count=%d err=%v", recoveredCount, err)
	}
	recovered := getRuntimeV2Run(t, ctx, node, run.ID)
	if recovered.Status != steward.RuntimeRunQueued || recovered.Steps[0].Status != steward.RuntimeStepPending || recovered.Steps[0].MaxAttempts != 2 || recovered.Steps[0].Invocations[0].Status != steward.RuntimeStepFailed {
		t.Fatalf("interrupted state was not durably recovered: %+v", recovered)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute recovered run: %v", err)
	}
	if final := getRuntimeV2Run(t, ctx, node, run.ID); final.Status != steward.RuntimeRunSucceeded || final.Steps[0].Attempt != 2 {
		t.Fatalf("recovered run did not complete on next attempt: %+v", final)
	}
}

func TestStewardRuntimeR2PlansAndExecutesRealLocalTools(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime R2 integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("R2 web evidence"))
	}))
	defer webServer.Close()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate go executable for real-process R2 test: %v", err)
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r2"), "runtime-r2-node",
		steward.WithRuntimeR2Enabled(true),
		steward.WithRuntimeAllowedRoots(root),
		steward.WithRuntimeExecutables(goExecutable),
		steward.WithRuntimeWebAllowedHosts("127.0.0.1"),
	)

	plannerRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.apiBase+"/steward/runtime/planner", nil)
	plannerResponse, err := node.server.Client().Do(plannerRequest)
	if err != nil {
		t.Fatalf("get runtime planner status: %v", err)
	}
	defer plannerResponse.Body.Close()
	var plannerEnvelope struct {
		Planner domain.StewardRuntimePlannerStatus `json:"planner"`
	}
	if err := json.NewDecoder(plannerResponse.Body).Decode(&plannerEnvelope); err != nil {
		t.Fatalf("decode runtime planner status: %v", err)
	}
	if plannerResponse.StatusCode != http.StatusOK || !plannerEnvelope.Planner.Enabled || plannerEnvelope.Planner.Provider != "local-rules" {
		t.Fatalf("unexpected runtime planner status=%d body=%+v", plannerResponse.StatusCode, plannerEnvelope.Planner)
	}

	filePath := filepath.Join(root, "planned", "note.txt")
	createInstruction := fmt.Sprintf(`创建文件 "%s" 内容 "R2 已真实落盘"`, filePath)
	created := postRuntimeR2Plan(t, ctx, node, map[string]any{"instruction": createInstruction}, http.StatusCreated)
	if created.Status != steward.RuntimeRunAwaitingApproval || created.Planner != "local-rules" || created.SourceInstruction != createInstruction {
		t.Fatalf("create plan did not preserve provenance and approval gate: %+v", created)
	}
	if created.Steps[0].PolicyDecision != steward.RuntimePolicyApproval || created.Steps[0].ToolIdempotency != steward.RuntimeIdempotencyKeyed {
		t.Fatalf("create policy was not persisted: %+v", created.Steps[0])
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("file existed before approval: %v", err)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil || processed != 0 {
		t.Fatalf("unapproved run was executable: processed=%d err=%v", processed, err)
	}
	created = postRuntimeV2Action(t, ctx, node, created.ID, "approve", map[string]any{"plan_hash": created.PlanHash, "granted_by": "r2-e2e"}, http.StatusOK)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute approved file plan: %v", err)
	}
	created = getRuntimeV2Run(t, ctx, node, created.ID)
	if created.Status != steward.RuntimeRunSucceeded || len(created.Steps[0].Evidence) == 0 {
		t.Fatalf("file plan did not finish with evidence: %+v", created)
	}
	content, err := os.ReadFile(filePath)
	if err != nil || string(content) != "R2 已真实落盘" {
		t.Fatalf("planned file content=%q err=%v", content, err)
	}

	read := postRuntimeR2Plan(t, ctx, node, map[string]any{"instruction": fmt.Sprintf(`读取文件 "%s"`, filePath)}, http.StatusCreated)
	if read.Status != steward.RuntimeRunQueued || read.PermissionCeiling != steward.PermissionA1 || read.Steps[0].RequiresApproval {
		t.Fatalf("read-only plan was not auto-queued within A1: %+v", read)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute read plan: %v", err)
	}
	read = getRuntimeV2Run(t, ctx, node, read.ID)
	if read.Status != steward.RuntimeRunSucceeded || read.Steps[0].Invocations[0].Output["content"] == "R2 已真实落盘" {
		t.Fatalf("read plan did not return verified content: %+v", read)
	}
	readEvidence := getRuntimeV2EvidenceByKind(t, ctx, node, read, "tool_result")
	if readEvidence.Payload["content"] != "R2 已真实落盘" {
		t.Fatalf("explicit read evidence did not reveal verified content: %+v", readEvidence)
	}

	command := postRuntimeR2Plan(t, ctx, node, map[string]any{"instruction": fmt.Sprintf(`运行命令 "%s" version`, goExecutable)}, http.StatusCreated)
	if command.Status != steward.RuntimeRunAwaitingApproval || command.Steps[0].MaxAttempts != 1 || command.Steps[0].ToolIdempotency != steward.RuntimeIdempotencyNonIdempotent {
		t.Fatalf("process plan did not receive non-idempotent approval policy: %+v", command)
	}
	command = postRuntimeV2Action(t, ctx, node, command.ID, "approve", map[string]any{"plan_hash": command.PlanHash, "granted_by": "r2-e2e"}, http.StatusOK)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute allowlisted command: %v", err)
	}
	command = getRuntimeV2Run(t, ctx, node, command.ID)
	if command.Status != steward.RuntimeRunSucceeded || strings.Contains(fmt.Sprint(command.Steps[0].Invocations[0].Output["stdout"]), "go version") {
		t.Fatalf("allowlisted process did not produce verified output: %+v", command)
	}
	commandEvidence := getRuntimeV2EvidenceByKind(t, ctx, node, command, "tool_result")
	if !strings.Contains(fmt.Sprint(commandEvidence.Payload["stdout"]), "go version") {
		t.Fatalf("explicit command evidence did not reveal stdout: %+v", commandEvidence)
	}

	webRun := postRuntimeR2Plan(t, ctx, node, map[string]any{"instruction": "获取网页 " + webServer.URL}, http.StatusCreated)
	if webRun.Status != steward.RuntimeRunAwaitingApproval {
		t.Fatalf("network plan bypassed approval: %+v", webRun)
	}
	webRun = postRuntimeV2Action(t, ctx, node, webRun.ID, "approve", map[string]any{"plan_hash": webRun.PlanHash, "granted_by": "r2-e2e"}, http.StatusOK)
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatalf("execute approved web fetch: %v", err)
	}
	webRun = getRuntimeV2Run(t, ctx, node, webRun.ID)
	if webRun.Status != steward.RuntimeRunSucceeded || webRun.Steps[0].Invocations[0].Output["content"] == "R2 web evidence" {
		t.Fatalf("web plan did not preserve response evidence: %+v", webRun)
	}
	webEvidence := getRuntimeV2EvidenceByKind(t, ctx, node, webRun, "tool_result")
	if webEvidence.Payload["content"] != "R2 web evidence" {
		t.Fatalf("explicit web evidence did not reveal response content: %+v", webEvidence)
	}
}

func TestStewardRuntimeR2BlocksInterruptedNonIdempotentTool(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime R2 recovery test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r2_recovery"), "runtime-r2-recovery-node",
		steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root), steward.WithRuntimeExecutables(goExecutable))
	run := postRuntimeR2Plan(t, ctx, node, map[string]any{"instruction": fmt.Sprintf(`运行命令 "%s" version`, goExecutable)}, http.StatusCreated)
	run = postRuntimeV2Action(t, ctx, node, run.ID, "approve", map[string]any{"plan_hash": run.PlanHash, "granted_by": "r2-recovery"}, http.StatusOK)
	step := run.Steps[0]
	now := time.Now().UTC()
	if _, err := node.pool.Exec(ctx, `update steward_agent_runs set status = $2, started_at = $3, updated_at = $3 where id = $1`, run.ID, steward.RuntimeRunRunning, now); err != nil {
		t.Fatalf("seed interrupted non-idempotent run: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `update steward_run_steps set status = $2, attempt = 1, started_at = $3, updated_at = $3 where id = $1`, step.ID, steward.RuntimeStepRunning, now); err != nil {
		t.Fatalf("seed interrupted non-idempotent step: %v", err)
	}
	inputJSON, _ := json.Marshal(step.Arguments)
	if _, err := node.pool.Exec(ctx, `
		insert into steward_tool_invocations (
			id, run_id, step_id, tool_name, tool_version, attempt, idempotency_key, status, input, output, started_at
		) values ($1,$2,$3,$4,$5,1,$6,$7,$8::jsonb,'{}'::jsonb,$9)
	`, uuid.NewString(), run.ID, step.ID, step.ToolName, step.ToolVersion, step.IdempotencyKey+":1", steward.RuntimeStepRunning, string(inputJSON), now); err != nil {
		t.Fatalf("seed interrupted non-idempotent invocation: %v", err)
	}

	if recovered, err := node.service.RecoverAgentRuntime(ctx); err != nil || recovered != 1 {
		t.Fatalf("recover non-idempotent run count=%d err=%v", recovered, err)
	}
	blocked := getRuntimeV2Run(t, ctx, node, run.ID)
	if blocked.Status != steward.RuntimeRunBlocked || blocked.Steps[0].Status != steward.RuntimeStepBlocked || blocked.Approvals[0].Status != "revoked" {
		t.Fatalf("interrupted non-idempotent run was replayable: %+v", blocked)
	}
	if processed, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil || processed != 0 {
		t.Fatalf("blocked non-idempotent run entered queue: processed=%d err=%v", processed, err)
	}
	resumed := postRuntimeV2Action(t, ctx, node, run.ID, "resume", nil, http.StatusOK)
	if resumed.Status != steward.RuntimeRunAwaitingApproval {
		t.Fatalf("non-idempotent resume did not require fresh approval: %+v", resumed)
	}
	events, err := node.service.ListAgentRunEvents(ctx, run.ID, 0, 100)
	if err != nil {
		t.Fatalf("list recovery events: %v", err)
	}
	foundRecoveryBlock := false
	for _, event := range events {
		if event.Type == "run.recovery_blocked" {
			foundRecoveryBlock = true
		}
	}
	if !foundRecoveryBlock {
		t.Fatalf("run.recovery_blocked audit event missing: %+v", events)
	}
}

func TestStewardRuntimeV2DaemonExecutesQueuedRun(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed runtime v2 daemon integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_v2_daemon"), "runtime-daemon-node",
		steward.WithRuntimeV2Enabled(true))
	run := postRuntimeV2Run(t, ctx, node, map[string]any{
		"goal": "execute through the daemon loop", "auto_start": true,
		"steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "daemon"}}},
	}, http.StatusCreated)
	daemon := steward.NewDaemon(node.service, steward.DaemonOptions{
		HeartbeatInterval: time.Hour, CollectionInterval: time.Hour, ModelDispatchInterval: time.Hour,
		RuntimeInterval: 10 * time.Millisecond, RuntimeLimit: 1,
	})
	daemon.Start(ctx)
	t.Cleanup(daemon.Stop)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, err := node.service.GetAgentRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("read daemon run: %v", err)
		}
		if current.Status == steward.RuntimeRunSucceeded {
			status, err := node.service.GetAgentStatus(ctx)
			if err != nil {
				t.Fatalf("read daemon status: %v", err)
			}
			for _, loop := range status.BackgroundLoops {
				if loop.Name == "runtime-v2" && loop.Enabled && loop.Running {
					return
				}
			}
			t.Fatalf("runtime-v2 daemon loop was not reported running: %+v", status.BackgroundLoops)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon did not complete runtime run %s", run.ID)
}

type runtimeV2FlakyTool struct {
	mu    sync.Mutex
	calls int
}

type runtimeV2WaitTool struct {
	started chan struct{}
}

type runtimeR25NonIdempotentWaitTool struct {
	started chan struct{}
}

type runtimeR3TestBroker struct {
	mu                 sync.Mutex
	status             privilegebroker.Status
	authorization      privilegebroker.Authorization
	failAudit          bool
	approvalPrivateKey string
	proofSequence      int64
}

func newRuntimeR3TestBroker(t *testing.T) *runtimeR3TestBroker {
	t.Helper()
	authority, err := privilegebroker.GenerateApprovalAuthorityKeys()
	if err != nil {
		t.Fatal(err)
	}
	capability := privilegebroker.PublicCapability{
		Name: "tool:test-system-action", Description: "R3 integration test", PermissionLevel: "A7", RiskLevel: "critical",
		ExecutableName: "test-system-action.exe", ArgumentCount: 0, TimeoutSeconds: 30, MaxOutputBytes: 4096,
		CapabilityDigest: strings.Repeat("c", 64),
	}
	return &runtimeR3TestBroker{status: privilegebroker.Status{
		Version: privilegebroker.APIVersion, InstanceID: "r3-e2e-broker", PolicyDigest: strings.Repeat("d", 64),
		KeyID: "r3-e2e-key", Capabilities: []privilegebroker.PublicCapability{capability},
		ApprovalAuthorities: []privilegebroker.PublicApprovalAuthority{{Name: "r3-test-authority", Algorithm: "ed25519", PublicKey: authority.PublicKey, KeyID: authority.KeyID}},
	}, approvalPrivateKey: authority.PrivateKey}
}

func (b *runtimeR3TestBroker) approvalProof(t *testing.T, subject, planHash, capability string, generation int64, grantedBy, reason string) privilegebroker.SignedApprovalProof {
	t.Helper()
	b.mu.Lock()
	b.proofSequence++
	sequence := b.proofSequence
	privateKey := b.approvalPrivateKey
	b.mu.Unlock()
	now := time.Now().UTC()
	proof, err := privilegebroker.IssueApprovalProof(privateKey, privilegebroker.ApprovalProofClaims{
		ProofID: fmt.Sprintf("%064x", sequence), Subject: subject, PlanHash: planHash, Capability: capability,
		ControlGeneration: generation, GrantedBy: grantedBy, Reason: reason, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func (b *runtimeR3TestBroker) Status(context.Context) (privilegebroker.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, nil
}

func (b *runtimeR3TestBroker) Capability(_ context.Context, name string) (privilegebroker.PublicCapability, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.status.Capabilities) == 0 || name != b.status.Capabilities[0].Name {
		return privilegebroker.PublicCapability{}, fmt.Errorf("unknown test capability %s", name)
	}
	return b.status.Capabilities[0], nil
}

func (b *runtimeR3TestBroker) ExecuteCapability(_ context.Context, authorization privilegebroker.Authorization) (privilegebroker.ExecuteResponse, error) {
	b.mu.Lock()
	b.authorization = authorization
	instanceID := b.status.InstanceID
	failAudit := b.failAudit
	b.mu.Unlock()
	now := time.Now().UTC()
	response := privilegebroker.ExecuteResponse{
		Stdout: "broker-e2e-ok",
		Receipt: privilegebroker.SignedExecutionReceipt{
			KeyID: "r3-e2e-key", Signature: "test-signature",
			Payload: privilegebroker.ExecutionReceipt{
				ExecutionID: "r3-e2e-execution", BrokerInstanceID: instanceID,
				Capability: authorization.Capability, CapabilityDigest: strings.Repeat("c", 64), Subject: authorization.Subject,
				PlanHash: authorization.PlanHash, ApprovalRef: authorization.ApprovalRef,
				ApprovalProofID: authorization.ApprovalProof.Claims.ProofID, ApprovalKeyID: authorization.ApprovalProof.KeyID,
				ApprovalExpiresAt: authorization.ApprovalProof.Claims.ExpiresAt, ControlGeneration: authorization.ControlGeneration,
				ExitCode: 0, Succeeded: true, AuditPersisted: !failAudit,
				StdoutSHA256: strings.Repeat("a", 64), StderrSHA256: strings.Repeat("b", 64),
				StdoutBytes: int64(len("broker-e2e-ok")), StartedAt: now, FinishedAt: now,
			},
		},
	}
	if failAudit {
		return response, &privilegebroker.ExecutionError{Response: response}
	}
	return response, nil
}

func (b *runtimeR3TestBroker) SetControl(_ context.Context, stopped bool, input privilegebroker.ControlRequest) (privilegebroker.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.Stopped = stopped
	b.status.Generation = input.Generation
	return b.status, nil
}

func (b *runtimeR3TestBroker) lastAuthorization() privilegebroker.Authorization {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authorization
}

func (b *runtimeR3TestBroker) setAuditFailure(fail bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failAudit = fail
}

func (t *runtimeR25NonIdempotentWaitTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "runtime.test.non_idempotent_wait", Version: "1.0.0", Description: "models a cancellable process with an unknown external outcome",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		PermissionLevel: steward.PermissionA3, RiskLevel: "medium", SideEffect: steward.RuntimeSideEffectProcess,
		ApprovalMode: steward.RuntimeApprovalAlways, IdempotencyMode: steward.RuntimeIdempotencyNonIdempotent,
		SupportsCancel: true, DefaultTimeoutSec: 5,
	}
}

func (t *runtimeR25NonIdempotentWaitTool) Execute(ctx context.Context, _ map[string]any) (steward.RuntimeToolResult, error) {
	select {
	case t.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return steward.RuntimeToolResult{}, ctx.Err()
}

func (*runtimeR25NonIdempotentWaitTool) Verify(context.Context, map[string]any, map[string]any, map[string]any) error {
	return nil
}

func (t *runtimeV2WaitTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "runtime.test.wait", Version: "1.0.0", Description: "waits until its context ends",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		PermissionLevel: steward.PermissionA0, RiskLevel: "low", Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 5,
	}
}

func (t *runtimeV2WaitTool) Execute(ctx context.Context, _ map[string]any) (steward.RuntimeToolResult, error) {
	select {
	case t.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return steward.RuntimeToolResult{}, ctx.Err()
}

func (*runtimeV2WaitTool) Verify(context.Context, map[string]any, map[string]any, map[string]any) error {
	return nil
}

func (t *runtimeV2FlakyTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "runtime.test.flaky", Version: "1.0.0", Description: "fails exactly once",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
		PermissionLevel: steward.PermissionA0, RiskLevel: "low", Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 5,
	}
}

func (t *runtimeV2FlakyTool) Execute(_ context.Context, input map[string]any) (steward.RuntimeToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	if t.calls == 1 {
		return steward.RuntimeToolResult{}, fmt.Errorf("intentional first-attempt failure")
	}
	output := map[string]any{"value": input["value"]}
	return steward.RuntimeToolResult{Output: output, Evidence: []steward.RuntimeEvidence{{Kind: "test", Summary: "flaky tool recovered", Payload: output}}}, nil
}

func (*runtimeV2FlakyTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if fmt.Sprint(output["value"]) != fmt.Sprint(expected["value"]) {
		return fmt.Errorf("output did not match expected value")
	}
	return nil
}

func postRuntimeV2Run(t *testing.T, ctx context.Context, node stewardHTTPNode, body map[string]any, expectedStatus int) domain.StewardAgentRun {
	t.Helper()
	payload, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, node.apiBase+"/steward/runs", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("post runtime run: %v", err)
	}
	defer response.Body.Close()
	responsePayload, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("post runtime run status=%d want=%d body=%s", response.StatusCode, expectedStatus, responsePayload)
	}
	if expectedStatus >= 400 {
		return domain.StewardAgentRun{}
	}
	var envelope struct {
		Run domain.StewardAgentRun `json:"run"`
	}
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		t.Fatalf("decode runtime run response: %v body=%s", err, responsePayload)
	}
	return envelope.Run
}

func postRuntimeR2Plan(t *testing.T, ctx context.Context, node stewardHTTPNode, body map[string]any, expectedStatus int) domain.StewardAgentRun {
	t.Helper()
	payload, _ := json.Marshal(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, node.apiBase+"/steward/runs/plan", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("post runtime plan: %v", err)
	}
	defer response.Body.Close()
	responsePayload, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("post runtime plan status=%d want=%d body=%s", response.StatusCode, expectedStatus, responsePayload)
	}
	if expectedStatus >= 400 {
		return domain.StewardAgentRun{}
	}
	var envelope struct {
		Run domain.StewardAgentRun `json:"run"`
	}
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		t.Fatalf("decode runtime plan response: %v body=%s", err, responsePayload)
	}
	return envelope.Run
}

func postRuntimeV2Action(t *testing.T, ctx context.Context, node stewardHTTPNode, runID string, action string, body map[string]any, expectedStatus int) domain.StewardAgentRun {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, node.apiBase+"/steward/runs/"+runID+"/"+action, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("post runtime action %s: %v", action, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("runtime action %s status=%d want=%d body=%s", action, response.StatusCode, expectedStatus, payload)
	}
	if expectedStatus >= 400 {
		return domain.StewardAgentRun{}
	}
	var envelope struct {
		Run domain.StewardAgentRun `json:"run"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode runtime action response: %v body=%s", err, payload)
	}
	return envelope.Run
}

func getRuntimeV2Run(t *testing.T, ctx context.Context, node stewardHTTPNode, runID string) domain.StewardAgentRun {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.apiBase+"/steward/runs/"+runID, nil)
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("get runtime run: %v", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Run domain.StewardAgentRun `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode runtime get response: %v", err)
	}
	return envelope.Run
}

func getRuntimeV2EvidenceByKind(t *testing.T, ctx context.Context, node stewardHTTPNode, run domain.StewardAgentRun, kind string) domain.StewardEvidenceArtifact {
	t.Helper()
	for _, step := range run.Steps {
		for _, evidence := range step.Evidence {
			if evidence.Kind != kind {
				continue
			}
			if evidence.Payload != nil {
				t.Fatalf("run detail leaked evidence payload by default: %+v", evidence)
			}
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				node.apiBase+"/steward/runs/"+run.ID+"/evidence/"+evidence.ID, nil)
			response, err := node.server.Client().Do(request)
			if err != nil {
				t.Fatalf("get governed evidence: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				payload, _ := io.ReadAll(response.Body)
				t.Fatalf("get governed evidence status=%d body=%s", response.StatusCode, payload)
			}
			var envelope struct {
				Evidence domain.StewardEvidenceArtifact `json:"evidence"`
			}
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode governed evidence: %v", err)
			}
			return envelope.Evidence
		}
	}
	t.Fatalf("run %s has no %s evidence", run.ID, kind)
	return domain.StewardEvidenceArtifact{}
}

func postRuntimeR25Control(t *testing.T, ctx context.Context, node stewardHTTPNode, action string, body map[string]any) domain.StewardRuntimeExecutionControl {
	t.Helper()
	payload, _ := json.Marshal(body)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, node.apiBase+"/steward/runtime/control/"+action, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("post runtime control %s: %v", action, err)
	}
	defer response.Body.Close()
	responsePayload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read runtime control %s: %v", action, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("runtime control %s status=%d body=%s", action, response.StatusCode, responsePayload)
	}
	var envelope struct {
		Control domain.StewardRuntimeExecutionControl `json:"control"`
	}
	if err := json.Unmarshal(responsePayload, &envelope); err != nil {
		t.Fatalf("decode runtime control %s: %v body=%s", action, err, responsePayload)
	}
	return envelope.Control
}

func listRuntimeR25Runs(t *testing.T, ctx context.Context, node stewardHTTPNode) []domain.StewardAgentRunSummary {
	t.Helper()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.apiBase+"/steward/runs?limit=20", nil)
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatalf("list runtime runs: %v", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Runs []domain.StewardAgentRunSummary `json:"runs"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode runtime run list: %v", err)
	}
	return envelope.Runs
}

func runtimeR25Events(t *testing.T, ctx context.Context, node stewardHTTPNode, runID string) []domain.StewardRunEvent {
	t.Helper()
	events, err := node.service.ListAgentRunEvents(ctx, runID, 0, 100)
	if err != nil {
		t.Fatalf("list runtime events: %v", err)
	}
	return events
}

func runtimeEventTypePresent(t *testing.T, ctx context.Context, node stewardHTTPNode, runID, eventType string) bool {
	t.Helper()
	for _, event := range runtimeR25Events(t, ctx, node, runID) {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func seedExpiredRuntimeInvocation(t *testing.T, ctx context.Context, node stewardHTTPNode, run domain.StewardAgentRun) {
	t.Helper()
	step := run.Steps[0]
	now := time.Now().UTC()
	expiredAt := now.Add(-time.Minute)
	if _, err := node.pool.Exec(ctx, `
		update steward_agent_runs set status = $2, started_at = $3, updated_at = $3 where id = $1
	`, run.ID, steward.RuntimeRunRunning, now); err != nil {
		t.Fatalf("seed watchdog run: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `
		update steward_run_steps set status = $2, attempt = 1, started_at = $3, updated_at = $3 where id = $1
	`, step.ID, steward.RuntimeStepRunning, now); err != nil {
		t.Fatalf("seed watchdog step: %v", err)
	}
	inputJSON, _ := json.Marshal(step.Arguments)
	if _, err := node.pool.Exec(ctx, `
		insert into steward_tool_invocations (
			id, run_id, step_id, tool_name, tool_version, attempt, idempotency_key,
			status, input, output, lease_owner, control_generation, heartbeat_at,
			lease_expires_at, started_at
		) values ($1,$2,$3,$4,$5,1,$6,$7,$8::jsonb,'{}'::jsonb,'dead-worker',0,$9,$9,$10)
	`, uuid.NewString(), run.ID, step.ID, step.ToolName, step.ToolVersion, step.IdempotencyKey+":1",
		steward.RuntimeStepRunning, string(inputJSON), expiredAt, now); err != nil {
		t.Fatalf("seed expired watchdog invocation: %v", err)
	}
}

func cloneRuntimeV2Body(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	payload, _ := json.Marshal(source)
	var clone map[string]any
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
