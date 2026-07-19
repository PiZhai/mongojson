package steward

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

func TestRuntimeV2CancelRequestWinsSuccessFinalizationRace(t *testing.T) {
	ctx, service := newRuntimeV2PostgresService(t)
	run := createRuntimeV2CancelRaceRun(t, ctx, service, "cancel wins success")
	stepID := firstRuntimeV2StepID(t, ctx, service.db, run.ID)
	now := time.Now().UTC()
	if _, err := service.db.Pool.Exec(ctx, `update steward_agent_runs set status=$2,cancel_requested=true,started_at=$3,updated_at=$3 where id=$1`,
		run.ID, RuntimeRunRunning, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Pool.Exec(ctx, `update steward_run_steps set status=$2,completed_at=$3,updated_at=$3 where id=$1`,
		stepID, RuntimeStepSucceeded, now); err != nil {
		t.Fatal(err)
	}
	if err := service.finishAgentRunSucceeded(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	assertRuntimeV2RunTerminal(t, ctx, service.db, run.ID, RuntimeRunCancelled)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.succeeded", 0)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.cancelled", 1)
}

func TestRuntimeV2CancelRequestWinsFailureFinalizationRace(t *testing.T) {
	ctx, service := newRuntimeV2PostgresService(t)
	run := createRuntimeV2CancelRaceRun(t, ctx, service, "cancel wins failure")
	stepID := firstRuntimeV2StepID(t, ctx, service.db, run.ID)
	now := time.Now().UTC()
	if _, err := service.db.Pool.Exec(ctx, `update steward_agent_runs set status=$2,cancel_requested=true,started_at=$3,updated_at=$3 where id=$1`,
		run.ID, RuntimeRunRunning, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Pool.Exec(ctx, `update steward_run_steps set status=$2,started_at=$3,updated_at=$3 where id=$1`,
		stepID, RuntimeStepRunning, now); err != nil {
		t.Fatal(err)
	}
	if err := service.finishAgentRunWithStatus(ctx, run.ID, RuntimeRunFailed, RuntimeStepFailed, "run.failed", "tool failed after cancel"); err != nil {
		t.Fatal(err)
	}
	assertRuntimeV2RunTerminal(t, ctx, service.db, run.ID, RuntimeRunCancelled)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.failed", 0)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.cancelled", 1)
}

func TestRuntimeV2RecoveryConvergesQueuedCancelRequest(t *testing.T) {
	ctx, service := newRuntimeV2PostgresService(t)
	run := createRuntimeV2CancelRaceRun(t, ctx, service, "recover queued cancel")
	if _, err := service.db.Pool.Exec(ctx, `update steward_agent_runs set status=$2,cancel_requested=true,updated_at=$3 where id=$1`, run.ID, RuntimeRunQueued, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.RecoverAgentRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == 0 {
		t.Fatal("recovery did not process the cancel-requested run")
	}
	assertRuntimeV2RunTerminal(t, ctx, service.db, run.ID, RuntimeRunCancelled)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.recovered", 0)
	assertRuntimeV2EventCount(t, ctx, service.db, run.ID, "run.cancelled", 1)
}

func newRuntimeV2PostgresService(t *testing.T) (context.Context, *Service) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed Runtime V2 cancellation tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := database.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, NewService(db, WithRuntimeV2Enabled(true))
}

func createRuntimeV2CancelRaceRun(t *testing.T, ctx context.Context, service *Service, goal string) domain.StewardAgentRun {
	t.Helper()
	run, err := service.CreateAgentRun(ctx, CreateAgentRunInput{
		Goal:      goal,
		AutoStart: true,
		Steps: []CreateAgentRunStepInput{{
			Key: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": goal},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func firstRuntimeV2StepID(t *testing.T, ctx context.Context, db *database.DB, runID string) string {
	t.Helper()
	var stepID string
	if err := db.Pool.QueryRow(ctx, `select id::text from steward_run_steps where run_id=$1 order by position limit 1`, runID).Scan(&stepID); err != nil {
		t.Fatal(err)
	}
	return stepID
}

func assertRuntimeV2RunTerminal(t *testing.T, ctx context.Context, db *database.DB, runID, wantStatus string) {
	t.Helper()
	var status string
	var cancelRequested bool
	if err := db.Pool.QueryRow(ctx, `select status,cancel_requested from steward_agent_runs where id=$1`, runID).Scan(&status, &cancelRequested); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || cancelRequested {
		t.Fatalf("run status=%s cancel_requested=%v, want status=%s cancel_requested=false", status, cancelRequested, wantStatus)
	}
}

func assertRuntimeV2EventCount(t *testing.T, ctx context.Context, db *database.DB, runID, eventType string, want int) {
	t.Helper()
	var got int
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_run_events where run_id=$1 and type=$2`, runID, eventType).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("event %s count=%d, want %d", eventType, got, want)
	}
}
