package steward

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

func TestConversationExecutionRefreshDoesNotOverwriteNewerPause(t *testing.T) {
	service, db, ctx := newConversationProjectionRaceService(t)
	item := seedTerminalRunConversationExecution(t, ctx, service, db)

	if _, err := db.Pool.Exec(ctx, `update steward_conversation_executions set status='paused' where id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.refreshConversationExecution(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != conversationExecutionPaused {
		t.Fatalf("stale refresh returned status %q, want paused", refreshed.Status)
	}
	var stored string
	if err := db.Pool.QueryRow(ctx, `select status from steward_conversation_executions where id=$1`, item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != conversationExecutionPaused {
		t.Fatalf("stale refresh overwrote newer pause with %q", stored)
	}
}

func TestConversationExecutionCancelDoesNotOverwriteTerminalChild(t *testing.T) {
	service, db, ctx := newConversationProjectionRaceService(t)
	item := seedTerminalRunConversationExecution(t, ctx, service, db)

	cancelled, err := service.cancelConversationExecution(ctx, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != RuntimeRunSucceeded {
		t.Fatalf("cancel projected terminal child as %q, want succeeded", cancelled.Status)
	}
	var stored string
	if err := db.Pool.QueryRow(ctx, `select status from steward_conversation_executions where id=$1`, item.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != RuntimeRunSucceeded {
		t.Fatalf("cancel overwrote terminal child with %q", stored)
	}
}

func TestConversationExecutionFailureDoesNotOverwritePause(t *testing.T) {
	service, db, ctx := newConversationProjectionRaceService(t)
	item := seedTerminalRunConversationExecution(t, ctx, service, db)
	if _, err := db.Pool.Exec(ctx, `update steward_conversation_executions
		set status='paused',completed_at=null where id=$1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.failConversationExecution(ctx, item.ID, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	var status, failure string
	if err := db.Pool.QueryRow(ctx, `select status,failure_summary from steward_conversation_executions where id=$1`, item.ID).
		Scan(&status, &failure); err != nil {
		t.Fatal(err)
	}
	if status != conversationExecutionPaused || failure != "" {
		t.Fatalf("late failure overwrote pause: status=%s failure=%q", status, failure)
	}
}

func newConversationProjectionRaceService(t *testing.T) (*Service, *database.DB, context.Context) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run conversation projection race tests")
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
	return NewService(db, WithRuntimeR2Enabled(true), WithRuntimeV2Enabled(true)), db, ctx
}

func seedTerminalRunConversationExecution(t *testing.T, ctx context.Context, service *Service, db *database.DB) domain.StewardConversationExecution {
	t.Helper()
	run, err := service.CreateAgentRun(ctx, CreateAgentRunInput{
		Goal: "conversation projection race", RequestedBy: "test", DataLevel: "D0", PermissionCeiling: PermissionA2,
		Steps: []CreateAgentRunStepInput{{Key: "echo", Title: "echo", ToolName: "runtime.echo", Arguments: map[string]any{"value": "ok"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Pool.Exec(ctx, `update steward_agent_runs set status='succeeded',completed_at=$2,updated_at=$2 where id=$1`, run.ID, now); err != nil {
		t.Fatal(err)
	}
	conversationID, requestID, messageID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values($1,'projection race','active','D0',$2,$2)`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
		values($1,$3,'user','run','D0','',$4),($2,$3,'assistant','progress','D0','',$4)`, requestID, messageID, conversationID, now); err != nil {
		t.Fatal(err)
	}
	item := domain.StewardConversationExecution{
		ID: uuid.NewString(), ConversationID: conversationID, MessageID: messageID, RequestMessageID: requestID,
		Instruction: "projection race", Summary: "projection race", Kind: conversationExecutionRun,
		Status: conversationExecutionRunning, RunID: run.ID, PermissionLevel: PermissionA2, Evidence: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.insertConversationExecution(ctx, item); err != nil {
		t.Fatal(err)
	}
	return item
}
