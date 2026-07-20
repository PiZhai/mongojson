package steward

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

func TestFinishAgentEpisodeRejectsStaleControlGeneration(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	for _, test := range []struct {
		name, storedStatus string
	}{
		{name: "paused", storedStatus: agentEpisodePaused},
		{name: "cancelled", storedStatus: agentEpisodeCancelled},
		{name: "generation_changed", storedStatus: agentEpisodeThinking},
	} {
		t.Run(test.name, func(t *testing.T) {
			conversationID := uuid.NewString()
			triggerMessageID := uuid.NewString()
			episodeID := uuid.NewString()
			turnID := uuid.NewString()
			now := time.Now().UTC()
			if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
				values($1,'legacy finish CAS','active','D0',$2,$2)`, conversationID, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
				values($1,$2,'user','run one legacy tool','D0','',$3)`, triggerMessageID, conversationID, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `insert into steward_agent_episodes(
				id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,current_round,
				tool_call_count,max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,control_generation,created_at,updated_at
			) values($1,$2,$3,'conversation','legacy finish CAS','D0',$4,1,1,12,40,1800,3,1,$5,$5)`,
				episodeID, conversationID, triggerMessageID, test.storedStatus, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `insert into steward_agent_turns(
				id,episode_id,round_index,status,model,created_at,updated_at,completed_at
			) values($1,$2,1,'tools_complete','legacy-advisor',$3,$3,$3)`, turnID, episodeID, now); err != nil {
				t.Fatal(err)
			}

			staleEpisode := domain.StewardAgentEpisode{
				ID: episodeID, ConversationID: conversationID, DataLevel: DataD0,
				Status: agentEpisodeThinking, ControlGeneration: 0,
			}
			turn := domain.StewardAgentTurn{ID: turnID, EpisodeID: episodeID, Status: "tools_complete", Model: "legacy-advisor"}
			message, err := service.finishAgentEpisode(ctx, staleEpisode, turn, "must not be emitted", false)
			if err != nil {
				t.Fatal(err)
			}
			if message.ID != "" {
				t.Fatalf("stale finish emitted final message %s", message.ID)
			}

			var status, activeExecutionID, finalMessageID, storedTurnStatus string
			var generation int64
			if err := db.Pool.QueryRow(ctx, `select status,control_generation,coalesce(active_execution_id::text,''),coalesce(final_message_id::text,'')
				from steward_agent_episodes where id=$1`, episodeID).Scan(&status, &generation, &activeExecutionID, &finalMessageID); err != nil {
				t.Fatal(err)
			}
			if status != test.storedStatus || generation != 1 || activeExecutionID != "" || finalMessageID != "" {
				t.Fatalf("episode overwritten by stale finish: status=%s generation=%d active=%s final=%s", status, generation, activeExecutionID, finalMessageID)
			}
			if err := db.Pool.QueryRow(ctx, `select status from steward_agent_turns where id=$1`, turnID).Scan(&storedTurnStatus); err != nil {
				t.Fatal(err)
			}
			if storedTurnStatus != "tools_complete" {
				t.Fatalf("turn status=%s, want tools_complete", storedTurnStatus)
			}
			var finalMessages int
			if err := db.Pool.QueryRow(ctx, `select count(*) from steward_conversation_messages where conversation_id=$1 and context_summary=$2`,
				conversationID, "agent-final:"+episodeID).Scan(&finalMessages); err != nil {
				t.Fatal(err)
			}
			if finalMessages != 0 {
				t.Fatalf("stale finish persisted %d final messages", finalMessages)
			}
		})
	}
}

func TestFinishAgentEpisodeWithTextRejectsStaleControlGeneration(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	for _, test := range []struct {
		name, storedStatus string
	}{
		{name: "paused", storedStatus: agentEpisodePaused},
		{name: "cancelled", storedStatus: agentEpisodeCancelled},
		{name: "generation_changed", storedStatus: agentEpisodeThinking},
	} {
		t.Run(test.name, func(t *testing.T) {
			conversationID := uuid.NewString()
			triggerMessageID := uuid.NewString()
			episodeID := uuid.NewString()
			now := time.Now().UTC()
			if _, err := db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
				values($1,'terminal text CAS','active','D0',$2,$2)`, conversationID, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
				values($1,$2,'user','finish with terminal text','D0','',$3)`, triggerMessageID, conversationID, now); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `insert into steward_agent_episodes(
				id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,current_round,
				tool_call_count,max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,control_generation,created_at,updated_at
			) values($1,$2,$3,'conversation','terminal text CAS','D0',$4,1,1,12,40,1800,3,1,$5,$5)`,
				episodeID, conversationID, triggerMessageID, test.storedStatus, now); err != nil {
				t.Fatal(err)
			}

			staleEpisode := domain.StewardAgentEpisode{
				ID: episodeID, ConversationID: conversationID, DataLevel: DataD0,
				Status: agentEpisodeThinking, ControlGeneration: 0,
			}
			if err := service.finishAgentEpisodeWithText(ctx, staleEpisode, "must not be emitted", agentEpisodeFailed); err != nil {
				t.Fatal(err)
			}

			var status, finalMessageID string
			var generation int64
			if err := db.Pool.QueryRow(ctx, `select status,control_generation,coalesce(final_message_id::text,'')
				from steward_agent_episodes where id=$1`, episodeID).Scan(&status, &generation, &finalMessageID); err != nil {
				t.Fatal(err)
			}
			if status != test.storedStatus || generation != 1 || finalMessageID != "" {
				t.Fatalf("episode overwritten by stale terminal text: status=%s generation=%d final=%s", status, generation, finalMessageID)
			}
			var finalMessages int
			if err := db.Pool.QueryRow(ctx, `select count(*) from steward_conversation_messages
				where conversation_id=$1 and context_summary=$2`, conversationID, "agent-terminal:"+episodeID).Scan(&finalMessages); err != nil {
				t.Fatal(err)
			}
			if finalMessages != 0 {
				t.Fatalf("stale terminal transition persisted %d messages", finalMessages)
			}
		})
	}
}

func TestConcurrentInitialAgentTurnDispatchCreatesAndLinksOneExecution(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	tool := newDispatchBarrierRuntimeTool()
	service := NewService(db, WithRuntimeV2Enabled(true), WithRuntimeTool(tool), WithAgentID("dispatch-cas-test"))
	tool.arm()
	defer tool.release()

	conversationID := uuid.NewString()
	triggerMessageID := uuid.NewString()
	episodeID := uuid.NewString()
	turnID := uuid.NewString()
	now := time.Now().UTC()
	toolCalls := []domain.StewardAgentToolCall{{
		ID: "dispatch_call_1", ToolName: tool.specName(), Arguments: map[string]any{"value": "once"},
	}}
	encodedCalls, err := json.Marshal(toolCalls)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level,created_at,updated_at)
		values($1,'concurrent dispatch CAS','active','D0',$2,$2)`, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level,model,created_at)
		values($1,$2,'user','dispatch exactly once','D0','',$3)`, triggerMessageID, conversationID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `insert into steward_agent_episodes(
		id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,current_round,
		tool_call_count,max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,control_generation,created_at,updated_at
	) values($1,$2,$3,'conversation','concurrent dispatch CAS','D0','thinking',1,1,12,40,1800,3,0,$4,$4)`,
		episodeID, conversationID, triggerMessageID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `insert into steward_agent_turns(
		id,episode_id,round_index,status,assistant_content,tool_calls,provider,model,created_at,updated_at
	) values($1,$2,1,'model_complete','dispatching once',$3::jsonb,'test','test-model',$4,$4)`,
		turnID, episodeID, string(encodedCalls), now); err != nil {
		t.Fatal(err)
	}

	conversation := domain.StewardConversation{ID: conversationID, DataLevel: DataD0}
	trigger := domain.StewardConversationMessage{
		ID: triggerMessageID, ConversationID: conversationID, Role: conversationRoleUser, Content: "dispatch exactly once", DataLevel: DataD0,
	}
	episode := domain.StewardAgentEpisode{
		ID: episodeID, ConversationID: conversationID, TriggerMessageID: triggerMessageID,
		TriggerKind: "conversation", Goal: "concurrent dispatch CAS", DataLevel: DataD0,
		Status: agentEpisodeThinking, CurrentRound: 1, ToolCallCount: 1, ControlGeneration: 0,
	}
	turn := domain.StewardAgentTurn{
		ID: turnID, EpisodeID: episodeID, RoundIndex: 1, Status: "model_complete", AssistantContent: "dispatching once",
		ToolCalls: toolCalls, Provider: "test", Model: "test-model", CreatedAt: now, UpdatedAt: now,
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			_, dispatchErr := service.dispatchAgentTurn(ctx, conversation, trigger, episode, turn)
			errs <- dispatchErr
		}()
	}
	close(start)
	select {
	case <-tool.entered:
	case <-ctx.Done():
		t.Fatalf("first dispatch never reached child creation: %v", ctx.Err())
	}
	// Give the competing dispatcher a real overlap window. With the turn lock it
	// waits before child creation; without it both dispatchers enter this barrier.
	select {
	case <-tool.entered:
	case <-time.After(300 * time.Millisecond):
	}
	preReleaseEntrants := tool.preReleaseEntrants.Load()
	tool.release()
	for index := 0; index < 2; index++ {
		select {
		case dispatchErr := <-errs:
			if dispatchErr != nil {
				t.Fatalf("dispatch %d failed: %v", index+1, dispatchErr)
			}
		case <-ctx.Done():
			t.Fatalf("dispatch %d did not finish: %v", index+1, ctx.Err())
		}
	}
	if preReleaseEntrants != 1 {
		t.Fatalf("child creation had %d concurrent entrants, want 1", preReleaseEntrants)
	}

	var executionCount, runCount, assistantCount int
	var executionID, runID, turnExecutionID, activeExecutionID, turnStatus, episodeStatus string
	if err := db.Pool.QueryRow(ctx, `select count(*),coalesce(max(id::text),''),coalesce(max(run_id::text),'')
		from steward_conversation_executions where turn_id=$1`, turnID).Scan(&executionCount, &executionID, &runID); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_agent_runs where idempotency_key=$1`,
		"agent:"+episodeID+":1:dispatch_call_1").Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select count(*) from steward_conversation_messages
		where conversation_id=$1 and role='assistant' and context_summary=$2`, conversationID, episode.Goal).Scan(&assistantCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select status,coalesce(execution_id::text,'') from steward_agent_turns where id=$1`, turnID).Scan(&turnStatus, &turnExecutionID); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select status,coalesce(active_execution_id::text,'') from steward_agent_episodes where id=$1`, episodeID).Scan(&episodeStatus, &activeExecutionID); err != nil {
		t.Fatal(err)
	}
	if executionCount != 1 || runCount != 1 || assistantCount != 1 {
		t.Fatalf("created executions=%d runs=%d assistant_messages=%d, want exactly one of each", executionCount, runCount, assistantCount)
	}
	if executionID == "" || runID == "" || turnExecutionID != executionID || activeExecutionID != executionID {
		t.Fatalf("split execution links: execution=%s run=%s turn=%s episode=%s", executionID, runID, turnExecutionID, activeExecutionID)
	}
	if turnStatus != "tools_running" || episodeStatus != agentEpisodeExecuting {
		t.Fatalf("turn status=%s episode status=%s, want tools_running/executing", turnStatus, episodeStatus)
	}
}

func openAgentLoopCASTestDB(t *testing.T) (context.Context, *database.DB) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed agent CAS regression tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	db, err := database.Connect(ctx, baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

type dispatchBarrierRuntimeTool struct {
	armed              atomic.Bool
	released           atomic.Bool
	preReleaseEntrants atomic.Int32
	entered            chan struct{}
	releaseCh          chan struct{}
	releaseOnce        sync.Once
}

func newDispatchBarrierRuntimeTool() *dispatchBarrierRuntimeTool {
	return &dispatchBarrierRuntimeTool{entered: make(chan struct{}, 2), releaseCh: make(chan struct{})}
}

func (t *dispatchBarrierRuntimeTool) specName() string { return "runtime.dispatch_cas_barrier" }

func (t *dispatchBarrierRuntimeTool) arm() { t.armed.Store(true) }

func (t *dispatchBarrierRuntimeTool) release() {
	t.releaseOnce.Do(func() {
		t.released.Store(true)
		close(t.releaseCh)
	})
}

func (t *dispatchBarrierRuntimeTool) Spec() domain.StewardToolSpec {
	if t.armed.Load() && !t.released.Load() {
		t.preReleaseEntrants.Add(1)
		t.entered <- struct{}{}
		<-t.releaseCh
	}
	spec := runtimeEchoTool{}.Spec()
	spec.Name = t.specName()
	spec.Description = "Blocks creation so concurrent Agent turn dispatch can be tested."
	return spec
}

func (t *dispatchBarrierRuntimeTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	return runtimeEchoTool{}.Execute(ctx, input)
}

func (t *dispatchBarrierRuntimeTool) Verify(ctx context.Context, input, output, expected map[string]any) error {
	return runtimeEchoTool{}.Verify(ctx, input, output, expected)
}
