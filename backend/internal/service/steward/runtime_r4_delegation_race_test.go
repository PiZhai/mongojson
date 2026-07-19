package steward

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/platform/database"
)

func TestDelegationRejectionDoesNotOverwriteTerminalRun(t *testing.T) {
	ctx, service := newDelegationRacePostgresService(t)
	run, err := service.CreateAgentRun(ctx, CreateAgentRunInput{
		Goal: "terminal delegation race",
		Steps: []CreateAgentRunStepInput{{
			Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "done"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := service.db.Pool.Exec(ctx, `
		update steward_agent_runs set status=$2,updated_at=$3,completed_at=$3 where id=$1
	`, run.ID, RuntimeRunSucceeded, now); err != nil {
		t.Fatal(err)
	}
	tx, err := service.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.blockInvalidDelegatedRunTx(ctx, tx, run.ID, errors.New("stale verifier"), now.Add(time.Second)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertRuntimeV2RunTerminal(t, ctx, service.db, run.ID, RuntimeRunSucceeded)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.delegation_rejected", 0)
}

func TestStaleAgentMessageNACKDoesNotBlockCompletedRun(t *testing.T) {
	ctx, service := newDelegationRacePostgresService(t)
	suffix := strings.ToLower(strings.ReplaceAll(uuid.NewString(), "-", ""))[:12]
	agentID := "race-agent-" + suffix
	workerID := "race-worker-" + suffix
	if _, err := service.UpsertOrchestrationAgent(ctx, UpsertOrchestrationAgentInput{
		ID: agentID, Name: "Race Agent", Role: "test worker", PermissionCeiling: "A0", DataLevelCeiling: "D0",
		ToolAllowlist: []string{"runtime.echo"}, MaxConcurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RegisterAgentWorker(ctx, agentID, workerID, 1001); err != nil {
		t.Fatal(err)
	}
	orchestration, err := service.CreateOrchestration(ctx, CreateOrchestrationInput{
		Goal: "stale NACK must lose", AutoStart: true, PermissionCeiling: "A0", DataLevel: "D0",
		Nodes: []CreateOrchestrationNodeInput{{
			Key: "work", AgentID: agentID, Goal: "finish before stale NACK",
			Steps: []CreateAgentRunStepInput{{Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "done"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processDelegationRaceOrchestration(t, ctx, service, orchestration.ID); err != nil {
		t.Fatal(err)
	}
	message, claimed, err := service.ClaimAgentMessage(ctx, agentID, workerID)
	if err != nil || !claimed {
		t.Fatalf("claim message: claimed=%t err=%v", claimed, err)
	}
	now := time.Now().UTC()
	if _, err := service.db.Pool.Exec(ctx, `
		update steward_agent_messages
		set status='acknowledged',lease_owner='',lease_expires_at=null,acknowledged_at=$2,updated_at=$2
		where id=$1
	`, message.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Pool.Exec(ctx, `
		update steward_agent_runs set status=$2,cancel_requested=false,updated_at=$3,completed_at=$3 where id=$1
	`, message.RuntimeRunID, RuntimeRunSucceeded, now); err != nil {
		t.Fatal(err)
	}
	message.Attempt = message.MaxAttempts
	err = service.nackAgentMessage(ctx, message, workerID, errors.New("late worker failure"))
	if err == nil || !strings.Contains(err.Error(), "NACK lost its lease") {
		t.Fatalf("stale NACK error=%v, want lost lease", err)
	}
	assertRuntimeV2RunTerminal(t, ctx, service.db, message.RuntimeRunID, RuntimeRunSucceeded)
	assertRuntimeV2EventCount(t, ctx, service.db, message.RuntimeRunID, "run.delegation_rejected", 0)
	if _, err := processDelegationRaceOrchestration(t, ctx, service, orchestration.ID); err != nil {
		t.Fatal(err)
	}
	final, err := service.GetOrchestration(ctx, orchestration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != OrchestrationSucceeded {
		t.Fatalf("stale NACK rewrote completed orchestration as %q", final.Status)
	}
}

func processDelegationRaceOrchestration(t *testing.T, ctx context.Context, service *Service, orchestrationID string) (bool, error) {
	t.Helper()
	var generation int64
	if err := service.db.Pool.QueryRow(ctx, `select generation from steward_runtime_execution_control where id='global'`).Scan(&generation); err != nil {
		return false, err
	}
	return service.processOrchestration(ctx, orchestrationID, generation)
}

func newDelegationRacePostgresService(t *testing.T) (context.Context, *Service) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run delegation race tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := database.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, NewService(db,
		WithRuntimeR2Enabled(true),
		WithRuntimeV2Enabled(true),
		WithOrchestrationR4Enabled(true),
		WithOrchestrationWorkersEnabled(true),
		WithOrchestrationSigningKey(bytes.Repeat([]byte{0x6d}, 32)),
	)
}
