package steward

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

func TestCompleteAgentEpisodeExecutionIgnoresStaleActiveExecution(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed stale callback regression test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := database.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(db, WithAutonomyAdvisor(&scriptedNativeResilienceAdvisor{}))
	conversationID := uuid.NewString()
	requestMessageID := uuid.NewString()
	progressMessageID := uuid.NewString()
	episodeID := uuid.NewString()
	oldTurnID := uuid.NewString()
	newTurnID := uuid.NewString()
	oldExecutionID := uuid.NewString()
	newExecutionID := uuid.NewString()
	now := time.Now().UTC()
	oldCalls, _ := json.Marshal([]domain.StewardAgentToolCall{{
		ID: "old_call_1", ToolName: "runtime.echo", Arguments: map[string]any{"value": "old"},
	}})
	newCalls, _ := json.Marshal([]domain.StewardAgentToolCall{{
		ID: "new_call_1", ToolName: "runtime.echo", Arguments: map[string]any{"value": "new"},
	}})
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values($1,'stale callback regression','active','D0',$2,$2)`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
		values($1,$3,'user','run tools','D0','',$4),($2,$3,'assistant','progress','D0','',$4)`,
		requestMessageID, progressMessageID, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_episodes(
			id,conversation_id,trigger_message_id,progress_message_id,trigger_kind,goal,data_level,status,
			current_round,tool_call_count,max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,
			created_at,updated_at
		) values($1,$2,$3,$4,'conversation','stale callback regression','D0','executing',2,2,0,0,0,3,$5,$5)
	`, episodeID, conversationID, requestMessageID, progressMessageID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_agent_turns(id,episode_id,round_index,status,tool_calls,created_at,updated_at)
		values($1,$3,1,'tools_running',$5::jsonb,$4,$4),($2,$3,2,'tools_running',$6::jsonb,$4,$4)`,
		oldTurnID, newTurnID, episodeID, now, string(oldCalls), string(newCalls)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_executions(
			id,conversation_id,message_id,request_message_id,instruction,summary,kind,status,
			episode_id,turn_id,round_index,evidence,created_at,updated_at,completed_at
		) values
		($1,$3,$4,$4,'old','old','question','succeeded',$5,$6,1,'{"old":true}'::jsonb,$8,$8,$8),
		($2,$3,$4,$4,'new','new','question','running',$5,$7,2,'{"new":true}'::jsonb,$8,$8,null)
	`, oldExecutionID, newExecutionID, conversationID, requestMessageID, episodeID, oldTurnID, newTurnID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_agent_turns set execution_id=$1 where id=$2`, oldExecutionID, oldTurnID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_agent_turns set execution_id=$1 where id=$2`, newExecutionID, newTurnID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_agent_episodes set active_execution_id=$1 where id=$2`, newExecutionID, episodeID); err != nil {
		t.Fatal(err)
	}

	oldExecution := domain.StewardConversationExecution{
		ID: oldExecutionID, EpisodeID: episodeID, TurnID: oldTurnID, Status: RuntimeRunSucceeded,
		Evidence: map[string]any{"old": true},
	}
	if err := service.completeAgentEpisodeExecution(ctx, oldExecution, []ConversationToolResult{{
		ToolName: "runtime.echo", Output: map[string]any{"value": "old result"},
	}}); err != nil {
		t.Fatal(err)
	}
	assertEpisodeExecutionState(t, ctx, db, episodeID, newExecutionID, agentEpisodeExecuting)
	assertTurnState(t, ctx, db, oldTurnID, "tools_running", "[]")
	assertTurnState(t, ctx, db, newTurnID, "tools_running", "[]")

	if _, err := db.Pool.Exec(ctx, `update steward_conversation_executions set status='succeeded',completed_at=$2,updated_at=$2 where id=$1`, newExecutionID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	newExecution := domain.StewardConversationExecution{
		ID: newExecutionID, EpisodeID: episodeID, TurnID: newTurnID, Status: RuntimeRunSucceeded,
		Evidence: map[string]any{"new": true},
	}
	if err := service.completeAgentEpisodeExecution(ctx, newExecution, []ConversationToolResult{{
		ToolName: "runtime.echo", Output: map[string]any{"value": "new result"},
	}}); err != nil {
		t.Fatal(err)
	}
	assertEpisodeExecutionState(t, ctx, db, episodeID, "", agentEpisodeThinking)
	assertTurnState(t, ctx, db, oldTurnID, "tools_running", "[]")
	assertTurnStateContains(t, ctx, db, newTurnID, "tools_complete", "new result")

	if err := service.completeAgentEpisodeExecution(ctx, newExecution, []ConversationToolResult{{
		ToolName: "runtime.echo", Output: map[string]any{"value": "duplicate"},
	}}); err != nil {
		t.Fatal(err)
	}
	assertTurnStateContains(t, ctx, db, newTurnID, "tools_complete", "new result")
}

func assertEpisodeExecutionState(t *testing.T, ctx context.Context, db *database.DB, episodeID, wantExecutionID, wantStatus string) {
	t.Helper()
	var status, active string
	if err := db.Pool.QueryRow(ctx, `select status,coalesce(active_execution_id::text,'') from steward_agent_episodes where id=$1`, episodeID).Scan(&status, &active); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || active != wantExecutionID {
		t.Fatalf("episode status=%s active_execution_id=%s, want status=%s active_execution_id=%s", status, active, wantStatus, wantExecutionID)
	}
}

func assertTurnState(t *testing.T, ctx context.Context, db *database.DB, turnID, wantStatus, wantResults string) {
	t.Helper()
	var status, results string
	if err := db.Pool.QueryRow(ctx, `select status,tool_results::text from steward_agent_turns where id=$1`, turnID).Scan(&status, &results); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || results != wantResults {
		t.Fatalf("turn %s status=%s results=%s, want status=%s results=%s", turnID, status, results, wantStatus, wantResults)
	}
}

func assertTurnStateContains(t *testing.T, ctx context.Context, db *database.DB, turnID, wantStatus, wantResultsPart string) {
	t.Helper()
	var status, results string
	if err := db.Pool.QueryRow(ctx, `select status,tool_results::text from steward_agent_turns where id=$1`, turnID).Scan(&status, &results); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || !strings.Contains(results, wantResultsPart) {
		t.Fatalf("turn %s status=%s results=%s, want status=%s containing %q", turnID, status, results, wantStatus, wantResultsPart)
	}
}
