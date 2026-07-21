package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

// codexParityTaskAdvisor deliberately has one observable, durable side effect.
// Sharing it between two Service instances lets the test prove that an Episode
// is advanced once even when two daemon cycles race on the same PostgreSQL row.
type codexParityTaskAdvisor struct {
	mu      sync.Mutex
	calls   int
	title   string
	started chan struct{}
	release <-chan struct{}
}

func (a *codexParityTaskAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "codex-parity", Model: "codex-parity"}
}

func (a *codexParityTaskAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a *codexParityTaskAdvisor) NextTurn(ctx context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	a.mu.Lock()
	a.calls++
	callNumber := a.calls
	a.mu.Unlock()
	if callNumber == 2 && a.started != nil {
		close(a.started)
	}
	if callNumber == 2 && a.release != nil {
		select {
		case <-a.release:
		case <-ctx.Done():
			return steward.AgentTurnDecision{}, ctx.Err()
		}
	}
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{Content: "创建一次可验证任务。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "create_once", ToolName: "steward.create_task", Arguments: map[string]any{"title": a.title},
		}}}, nil
	}
	return steward.AgentTurnDecision{Content: "任务只创建了一次。"}, nil
}

func (a *codexParityTaskAdvisor) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func newCodexParityService(t *testing.T, ctx context.Context, config *pgxpool.Config, agentID string, advisor steward.AutonomyAdvisor, options ...steward.ServiceOption) *steward.Service {
	t.Helper()
	pool, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatalf("connect second parity Service: %v", err)
	}
	t.Cleanup(pool.Close)
	serviceOptions := []steward.ServiceOption{
		steward.WithAgentID(agentID),
		steward.WithStorageDir(t.TempDir()),
		steward.WithAutonomyAdvisor(advisor),
		steward.WithRuntimeR2Enabled(true),
	}
	serviceOptions = append(serviceOptions, options...)
	service := steward.NewService(&database.DB{Pool: pool}, serviceOptions...)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatalf("ensure defaults for second parity Service: %v", err)
	}
	return service
}

func runCodexParityCyclesUntilTerminal(t *testing.T, ctx context.Context, service *steward.Service, episodeID string, orchestration bool) domain.StewardAgentEpisode {
	t.Helper()
	for cycle := 0; cycle < 80; cycle++ {
		if orchestration {
			if _, err := service.RunOrchestrationCycle(ctx, 20); err != nil {
				t.Fatalf("run parity orchestration cycle: %v", err)
			}
		}
		if _, err := service.RunAgentRuntimeCycle(ctx, 20); err != nil {
			t.Fatalf("run parity runtime cycle: %v", err)
		}
		if _, err := service.RunConversationExecutionRefreshCycle(ctx, 20); err != nil {
			t.Fatalf("refresh parity conversation execution: %v", err)
		}
		if _, err := service.RunAgentEpisodeCycle(ctx, 20); err != nil {
			t.Fatalf("run parity Episode cycle: %v", err)
		}
		episode, err := service.GetAgentEpisode(ctx, episodeID)
		if err != nil {
			t.Fatalf("get parity Episode: %v", err)
		}
		switch episode.Status {
		case "completed", "failed", "blocked", "cancelled":
			return episode
		}
		time.Sleep(10 * time.Millisecond)
	}
	episode, err := service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("parity Episode did not become terminal: %+v", episode)
	return domain.StewardAgentEpisode{}
}

const codexParityLongLoopToolRounds = 32

type codexParityLongLoopAdvisor struct{}

func (codexParityLongLoopAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "codex-parity", Model: "long-loop"}
}

func (codexParityLongLoopAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (codexParityLongLoopAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	completed := input.ToolCallCount
	if completed > 0 {
		expectedID := fmt.Sprintf("long_echo_%02d", completed)
		expectedValue := fmt.Sprintf("long-loop-value-%02d", completed)
		if len(input.Transcript) == 0 {
			return steward.AgentTurnDecision{Content: "长循环缺少上一轮工具结果。"}, nil
		}
		last := input.Transcript[len(input.Transcript)-1]
		if len(last.ToolResults) != 1 || last.ToolResults[0].ToolCallID != expectedID ||
			last.ToolResults[0].ToolName != "runtime.echo" || last.ToolResults[0].Error != "" ||
			last.ToolResults[0].Output["value"] != expectedValue {
			return steward.AgentTurnDecision{Content: "长循环上一轮工具结果未按 call_id 正确回填。"}, nil
		}
	}
	if completed >= codexParityLongLoopToolRounds {
		return steward.AgentTurnDecision{Content: "32 轮真实工具循环全部完成。"}, nil
	}
	next := completed + 1
	return steward.AgentTurnDecision{Content: fmt.Sprintf("执行第 %d 轮确定性工具。", next), ToolCalls: []domain.StewardAgentToolCall{{
		ID:        fmt.Sprintf("long_echo_%02d", next),
		ToolName:  "runtime.echo",
		Arguments: map[string]any{"value": fmt.Sprintf("long-loop-value-%02d", next)},
	}}}, nil
}

func TestStewardAgentCodexParityDefaultUnlimitedLoopExceedsTwelveRounds(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	// Empty environment overrides exercise the product defaults: zero means the
	// Controller has no fixed round, tool-call, or duration ceiling.
	t.Setenv("STEWARD_AGENT_MAX_ROUNDS", "")
	t.Setenv("STEWARD_AGENT_MAX_TOOL_CALLS", "")
	t.Setenv("STEWARD_AGENT_MAX_DURATION", "")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Codex-parity long-loop test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_codex_parity_long_loop"), "codex-parity-long-loop",
		steward.WithAutonomyAdvisor(codexParityLongLoopAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "Codex parity long loop"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "连续执行 32 轮真实工具后再回答"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 {
		t.Fatalf("long-loop request has no durable Episode: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	queued := created.Message.Episodes[0]
	if queued.Status != "executing" || queued.CurrentRound != 1 || queued.ToolCallCount != 1 ||
		queued.MaxRounds != 0 || queued.MaxToolCalls != 0 || queued.MaxDurationSeconds != 0 {
		t.Fatalf("long-loop Episode did not start with unlimited defaults: %+v", queued)
	}

	for round := 1; round <= codexParityLongLoopToolRounds; round++ {
		dispatched, getErr := node.service.GetAgentEpisode(ctx, episodeID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if dispatched.Status != "executing" || dispatched.CurrentRound != round ||
			dispatched.ToolCallCount != round || dispatched.ActiveExecutionID == "" {
			t.Fatalf("round %d was not durably dispatched: %+v", round, dispatched)
		}
		runtimeProcessed, runtimeErr := node.service.RunAgentRuntimeCycle(ctx, 1)
		if runtimeErr != nil || runtimeProcessed != 1 {
			t.Fatalf("round %d runtime cycle failed: processed=%d err=%v", round, runtimeProcessed, runtimeErr)
		}
		refreshed, refreshErr := node.service.RunConversationExecutionRefreshCycle(ctx, 1)
		if refreshErr != nil || refreshed != 1 {
			t.Fatalf("round %d execution refresh failed: refreshed=%d err=%v", round, refreshed, refreshErr)
		}
		withResult, getErr := node.service.GetAgentEpisode(ctx, episodeID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if withResult.Status != "thinking" || withResult.CurrentRound != round || len(withResult.Turns) != round {
			t.Fatalf("round %d tool result did not return to the model boundary: %+v", round, withResult)
		}
		if round < codexParityLongLoopToolRounds {
			processed, cycleErr := node.service.RunAgentEpisodeCycle(ctx, 1)
			if cycleErr != nil || processed != 1 {
				t.Fatalf("round %d next model cycle failed: processed=%d err=%v", round, processed, cycleErr)
			}
		}
	}
	processed, err := node.service.RunAgentEpisodeCycle(ctx, 1)
	if err != nil || processed != 1 {
		t.Fatalf("final model cycle failed: processed=%d err=%v", processed, err)
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || episode.CurrentRound < codexParityLongLoopToolRounds+1 ||
		episode.ToolCallCount != codexParityLongLoopToolRounds || episode.MaxRounds != 0 || episode.MaxToolCalls != 0 ||
		episode.MaxDurationSeconds != 0 || strings.Contains(episode.FailureSummary, "最大模型轮次") {
		t.Fatalf("default-unlimited Controller did not complete the 33-round loop: %+v", episode)
	}
	if len(episode.Turns) != codexParityLongLoopToolRounds+1 {
		t.Fatalf("long-loop turn history is incomplete: got=%d want=%d", len(episode.Turns), codexParityLongLoopToolRounds+1)
	}
	executionIDs := make(map[string]struct{}, codexParityLongLoopToolRounds)
	for index := 0; index < codexParityLongLoopToolRounds; index++ {
		turn := episode.Turns[index]
		expectedRound := index + 1
		expectedID := fmt.Sprintf("long_echo_%02d", expectedRound)
		expectedValue := fmt.Sprintf("long-loop-value-%02d", expectedRound)
		if turn.RoundIndex != expectedRound || turn.Status != "tools_complete" || turn.ExecutionID == "" ||
			len(turn.ToolCalls) != 1 || len(turn.ToolResults) != 1 ||
			turn.ToolCalls[0].ID != expectedID || turn.ToolCalls[0].ToolName != "runtime.echo" ||
			turn.ToolCalls[0].Arguments["value"] != expectedValue ||
			turn.ToolResults[0].ToolCallID != expectedID || turn.ToolResults[0].ToolName != "runtime.echo" ||
			turn.ToolResults[0].Error != "" || turn.ToolResults[0].Output["value"] != expectedValue {
			t.Fatalf("round %d call/result pairing is invalid: %+v", expectedRound, turn)
		}
		if _, duplicate := executionIDs[turn.ExecutionID]; duplicate {
			t.Fatalf("round %d reused execution %s", expectedRound, turn.ExecutionID)
		}
		executionIDs[turn.ExecutionID] = struct{}{}
	}
	finalTurn := episode.Turns[len(episode.Turns)-1]
	if finalTurn.RoundIndex < codexParityLongLoopToolRounds+1 || finalTurn.Status != "final" ||
		len(finalTurn.ToolCalls) != 0 || len(finalTurn.ToolResults) != 0 || !strings.Contains(finalTurn.AssistantContent, "32 轮") {
		t.Fatalf("long-loop final turn is invalid: %+v", finalTurn)
	}

	var invocationCount, succeededCount, distinctInputCount int
	if err := node.pool.QueryRow(ctx, `
		select count(*)::int,
		       count(*) filter (where invocation.status='succeeded')::int,
		       count(distinct invocation.input->>'value')::int
		from steward_tool_invocations invocation
		join steward_conversation_executions execution on execution.run_id=invocation.run_id
		where execution.episode_id=$1 and invocation.tool_name='runtime.echo'
	`, episodeID).Scan(&invocationCount, &succeededCount, &distinctInputCount); err != nil {
		t.Fatal(err)
	}
	if invocationCount != codexParityLongLoopToolRounds || succeededCount != codexParityLongLoopToolRounds ||
		distinctInputCount != codexParityLongLoopToolRounds {
		t.Fatalf("long loop repeated or lost runtime invocations: total=%d succeeded=%d distinct_inputs=%d",
			invocationCount, succeededCount, distinctInputCount)
	}
}

func TestStewardAgentCodexParityConcurrentServicesAdvanceEpisodeOnce(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Codex-parity concurrent Episode test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dbConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_codex_parity_concurrent")
	title := "codex-parity-once-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	advisor := &codexParityTaskAdvisor{title: title}
	node := newStewardHTTPNode(t, ctx, dbConfig.Copy(), "codex-parity-shared",
		steward.WithAutonomyAdvisor(advisor), steward.WithRuntimeR2Enabled(true))
	second := newCodexParityService(t, ctx, dbConfig, "codex-parity-shared", advisor)

	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "Codex parity concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "只创建一次任务"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 {
		t.Fatalf("queued message has no durable Episode: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan struct {
		processed int
		err       error
	}, 2)
	for _, service := range []*steward.Service{node.service, second} {
		go func(service *steward.Service) {
			<-start
			processed, cycleErr := service.RunAgentEpisodeCycle(ctx, 1)
			results <- struct {
				processed int
				err       error
			}{processed: processed, err: cycleErr}
		}(service)
	}
	close(start)
	processed := 0
	for index := 0; index < 2; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Episode cycle failed: %v", result.err)
		}
		processed += result.processed
	}
	if processed != 1 || advisor.callCount() != 2 {
		t.Fatalf("two Services advanced the same model boundary: processed=%d advisor_calls=%d", processed, advisor.callCount())
	}

	episode := runCodexParityCyclesUntilTerminal(t, ctx, node.service, episodeID, false)
	if episode.Status != "completed" || episode.CurrentRound != 2 || episode.ToolCallCount != 1 {
		t.Fatalf("concurrent Episode did not complete exactly one tool call: %+v", episode)
	}
	var taskCount, invocationCount int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tasks where title=$1`, title).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tool_invocations where tool_name='steward.create_task'`).Scan(&invocationCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 || invocationCount != 1 {
		t.Fatalf("concurrent cycle repeated a tool side effect: tasks=%d invocations=%d", taskCount, invocationCount)
	}
}

func TestStewardAgentCodexParitySlowAdvisorCannotBeDoubleAdvancedAfterLeaseExpiry(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Codex-parity lease-expiry test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dbConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_codex_parity_slow")
	started := make(chan struct{})
	release := make(chan struct{})
	title := "codex-parity-slow-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	advisor := &codexParityTaskAdvisor{title: title, started: started, release: release}
	node := newStewardHTTPNode(t, ctx, dbConfig.Copy(), "codex-parity-slow",
		steward.WithAutonomyAdvisor(advisor), steward.WithRuntimeR2Enabled(true))
	second := newCodexParityService(t, ctx, dbConfig, "codex-parity-slow", advisor)
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "Codex parity slow advisor"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "慢模型也只能创建一次"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := created.Message.Episodes[0].ID
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct {
		processed int
		err       error
	}, 1)
	go func() {
		processed, cycleErr := node.service.RunAgentEpisodeCycle(ctx, 1)
		firstDone <- struct {
			processed int
			err       error
		}{processed: processed, err: cycleErr}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("slow advisor was not entered")
	}
	// Simulate a provider call lasting past the persisted 90-second lease without
	// making the test sleep for 90 seconds. The session advisory lock must remain
	// the exclusive execution fence and the second Service must not steal the turn.
	if _, err := node.pool.Exec(ctx, `update steward_agent_episodes set lease_expires_at=now()-interval '1 second' where id=$1`, episodeID); err != nil {
		t.Fatal(err)
	}
	secondProcessed, err := second.RunAgentEpisodeCycle(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if secondProcessed != 0 || advisor.callCount() != 2 {
		t.Fatalf("expired lease allowed a second model call: processed=%d advisor_calls=%d", secondProcessed, advisor.callCount())
	}
	close(release)
	first := <-firstDone
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.processed != 1 || advisor.callCount() != 2 {
		t.Fatalf("the fenced slow turn was discarded or duplicated: processed=%d advisor_calls=%d", first.processed, advisor.callCount())
	}

	episode := runCodexParityCyclesUntilTerminal(t, ctx, node.service, episodeID, false)
	if episode.Status != "completed" || episode.ToolCallCount != 1 {
		t.Fatalf("slow-provider Episode did not preserve the original committed turn: %+v", episode)
	}
	var taskCount int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tasks where title=$1`, title).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 1 {
		t.Fatalf("slow-provider Episode repeated or lost the tool side effect: tasks=%d", taskCount)
	}
}

type codexParityCollectAllAdvisor struct {
	failureURL string
}

func (codexParityCollectAllAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "codex-parity", Model: "collect-all"}
}

func (codexParityCollectAllAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a codexParityCollectAllAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{Content: "同轮执行成功、失败、成功。", ToolCalls: []domain.StewardAgentToolCall{
			{ID: "stable_success_a", ToolName: "runtime.echo", Arguments: map[string]any{"value": "alpha"}},
			{ID: "stable_failure_b", ToolName: "web.fetch_text", Arguments: map[string]any{"url": a.failureURL}},
			{ID: "stable_success_c", ToolName: "runtime.echo", Arguments: map[string]any{"value": "charlie"}},
		}}, nil
	}
	results := input.Transcript[len(input.Transcript)-1].ToolResults
	if len(results) != 3 || results[0].ToolCallID != "stable_success_a" || results[1].ToolCallID != "stable_failure_b" || results[2].ToolCallID != "stable_success_c" {
		return steward.AgentTurnDecision{Content: "tool_call_id 映射不稳定。"}, nil
	}
	if results[0].Error != "" || results[1].Error == "" || results[2].Error != "" {
		return steward.AgentTurnDecision{Content: "失败状态错绑到了其他 tool_call_id。"}, nil
	}
	return steward.AgentTurnDecision{Content: "三个结果已按原始 tool_call_id 稳定回填。"}, nil
}

func TestStewardAgentCodexParityCollectAllKeepsSuccessFailureSuccessCallIDs(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Codex-parity collect-all test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_codex_parity_collect_all"), "codex-parity-collect-all",
		steward.WithAutonomyAdvisor(codexParityCollectAllAdvisor{}), steward.WithRuntimeR2Enabled(true),
		steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x51}, 32)))
	// The management router deterministically returns 404 here, making the middle
	// tool fail during execution rather than during plan validation.
	advisor := codexParityCollectAllAdvisor{failureURL: node.server.URL + "/definitely-missing"}
	// Replace the initial placeholder advisor before any Episode/model call.
	node.service = newCodexParityService(t, ctx, node.pool.Config(), "codex-parity-collect-all", advisor,
		steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x51}, 32)))

	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "Codex parity collect all"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "同轮执行三个独立工具"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := created.Message.Episodes[0].ID
	episode := runCodexParityCyclesUntilTerminal(t, ctx, node.service, episodeID, true)
	if episode.Status != "completed" || len(episode.Turns) != 2 || len(episode.Turns[0].ToolResults) != 3 {
		t.Fatalf("collect-all Episode did not retain all three results: %+v", episode)
	}
	results := episode.Turns[0].ToolResults
	if results[0].ToolCallID != "stable_success_a" || results[0].Error != "" ||
		results[1].ToolCallID != "stable_failure_b" || results[1].Error == "" ||
		results[2].ToolCallID != "stable_success_c" || results[2].Error != "" {
		t.Fatalf("success/failure/success results were reordered or misbound: %+v", results)
	}
}

type codexParityPauseAdvisor struct {
	command          string
	workingDirectory string
	markerPath       string
	mu               sync.Mutex
	calls            int
}

func (a *codexParityPauseAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "codex-parity", Model: "pause-non-idempotent"}
}

func (a *codexParityPauseAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a *codexParityPauseAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	a.mu.Lock()
	a.calls++
	callNumber := a.calls
	a.mu.Unlock()
	if len(input.Transcript) > 0 {
		last := input.Transcript[len(input.Transcript)-1]
		if len(last.ToolResults) == 1 && last.ToolResults[0].Error == "" {
			return steward.AgentTurnDecision{Content: "非幂等副作用已确认完成，不再重放。"}, nil
		}
	}
	return steward.AgentTurnDecision{Content: "执行一次非幂等追加。", ToolCalls: []domain.StewardAgentToolCall{{
		ID: fmt.Sprintf("append_once_%d", callNumber), ToolName: "shell.exec", Arguments: map[string]any{
			"command": a.command,
			"args": []any{
				"-test.run=^TestStewardAgentCodexParityHelperProcess$", "--", "codex-parity-append-sleep", a.markerPath, "1200",
			},
			"working_directory": a.workingDirectory,
		},
	}}}, nil
}

func TestStewardAgentCodexParityPauseResumeDoesNotReplayNonIdempotentTool(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Codex-parity pause/resume test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	root := t.TempDir()
	markerPath := filepath.Join(root, "non-idempotent-marker.txt")
	testExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	advisor := &codexParityPauseAdvisor{command: testExecutable, workingDirectory: root, markerPath: markerPath}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_codex_parity_pause"), "codex-parity-pause",
		steward.WithAutonomyAdvisor(advisor), steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "Codex parity pause"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "执行后暂停再继续也不能重复"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := created.Message.Episodes[0].ID
	if _, err := node.service.RunAgentEpisodeCycle(ctx, 1); err != nil {
		t.Fatal(err)
	}

	runtimeDone := make(chan error, 1)
	go func() {
		_, cycleErr := node.service.RunAgentRuntimeCycle(ctx, 1)
		runtimeDone <- cycleErr
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		payload, _ := os.ReadFile(markerPath)
		if string(payload) == "X" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("non-idempotent helper did not perform its first side effect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	paused, err := node.service.DecideAgentEpisode(ctx, episodeID, steward.DecideAgentEpisodeInput{Decision: "pause"})
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause failed after side effect began: episode=%+v err=%v", paused, err)
	}
	select {
	case err := <-runtimeDone:
		if err != nil {
			t.Fatalf("in-flight runtime cycle failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight non-idempotent tool did not settle after pause")
	}
	resumed, err := node.service.DecideAgentEpisode(ctx, episodeID, steward.DecideAgentEpisodeInput{Decision: "resume"})
	if err != nil || (resumed.Status != "thinking" && resumed.Status != "blocked") {
		t.Fatalf("resume failed: episode=%+v err=%v", resumed, err)
	}
	episode := resumed
	if resumed.Status == "thinking" {
		episode = runCodexParityCyclesUntilTerminal(t, ctx, node.service, episodeID, false)
	}
	if episode.Status != "completed" && episode.Status != "blocked" {
		t.Fatalf("resume must complete or explicitly block an unknown non-idempotent outcome: %+v", episode)
	}
	payload, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var invocationCount int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tool_invocations where tool_name='shell.exec'`).Scan(&invocationCount); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "X" || invocationCount != 1 {
		t.Fatalf("pause/resume replayed a non-idempotent side effect: marker=%q invocations=%d episode=%+v", string(payload), invocationCount, episode)
	}
}

// TestStewardAgentCodexParityHelperProcess is executed by shell.exec from the
// pause/resume acceptance above. Running it as an ordinary test is a no-op.
func TestStewardAgentCodexParityHelperProcess(t *testing.T) {
	marker := -1
	for index, argument := range os.Args {
		if argument == "codex-parity-append-sleep" {
			marker = index
			break
		}
	}
	if marker < 0 {
		t.Skip("helper process only")
	}
	if marker+2 >= len(os.Args) {
		t.Fatal("helper process arguments are incomplete")
	}
	file, err := os.OpenFile(os.Args[marker+1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("X"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	milliseconds, err := strconv.Atoi(os.Args[marker+2])
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}
