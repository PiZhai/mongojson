package httpapi

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

type r49ParallelAdvisor struct{}

func (r49ParallelAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "parallel-test", MaxDataLevel: "D6"}
}

func (r49ParallelAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (r49ParallelAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{Content: "并行检查两个值。", ToolCalls: []domain.StewardAgentToolCall{
			{ID: "parallel_a", ToolName: "runtime.echo", Arguments: map[string]any{"value": "alpha"}},
			{ID: "parallel_b", ToolName: "runtime.echo", Arguments: map[string]any{"value": "beta"}},
		}}, nil
	}
	results := input.Transcript[0].ToolResults
	if len(results) != 2 || results[0].ToolCallID != "parallel_a" || results[1].ToolCallID != "parallel_b" {
		return steward.AgentTurnDecision{Content: "工具结果顺序错误。"}, nil
	}
	return steward.AgentTurnDecision{Content: "并行结果已按原始调用 ID 返回。"}, nil
}

func TestStewardR49ParallelToolBatchPreservesCallIDs(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 parallel tool test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_parallel"), "r49-parallel",
		steward.WithAutonomyAdvisor(r49ParallelAdvisor{}), steward.WithRuntimeR2Enabled(true),
		steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x49}, 32)))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 parallel"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "同时检查两个值"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 || len(created.Message.Executions) != 1 || created.Message.Executions[0].Kind != "orchestration" {
		t.Fatalf("parallel tool calls did not create one orchestration batch: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	for index := 0; index < 12; index++ {
		if _, err := node.service.RunOrchestrationCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentEpisodeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || len(episode.Turns) != 2 || len(episode.Turns[0].ToolResults) != 2 {
		t.Fatalf("parallel episode did not complete: %+v", episode)
	}
	if episode.Turns[0].ToolResults[0].ToolCallID != "parallel_a" || episode.Turns[0].ToolResults[1].ToolCallID != "parallel_b" {
		t.Fatalf("tool results lost native call IDs: %+v", episode.Turns[0].ToolResults)
	}
}

type r49RecoveryAdvisor struct{}

func (r49RecoveryAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "recovery-test", MaxDataLevel: "D6"}
}
func (r49RecoveryAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}
func (r49RecoveryAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{ToolCalls: []domain.StewardAgentToolCall{{ID: "recovery_once", ToolName: "runtime.echo", Arguments: map[string]any{"value": "durable"}}}}, nil
	}
	return steward.AgentTurnDecision{Content: "已从持久化工具结果继续，没有重复执行。"}, nil
}

func TestStewardR49RestartAfterToolCompletionResumesEpisode(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 recovery test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dbConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_recovery")
	node := newStewardHTTPNode(t, ctx, dbConfig.Copy(), "r49-recovery", steward.WithAutonomyAdvisor(r49RecoveryAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 recovery"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "执行一次并在重启后继续"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := created.Message.Episodes[0].ID
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	beforeRestart, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil || beforeRestart.Status != "thinking" || beforeRestart.ToolCallCount != 1 {
		t.Fatalf("tool result was not durably recorded before restart: %+v err=%v", beforeRestart, err)
	}

	restartedPool, err := pgxpool.NewWithConfig(ctx, dbConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restartedPool.Close)
	restarted := steward.NewService(&database.DB{Pool: restartedPool}, steward.WithAgentID("r49-recovery"),
		steward.WithAutonomyAdvisor(r49RecoveryAdvisor{}), steward.WithRuntimeR2Enabled(true))
	if err := restarted.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RunAgentEpisodeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	afterRestart, err := restarted.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.Status != "completed" || afterRestart.CurrentRound != 2 || afterRestart.ToolCallCount != 1 {
		t.Fatalf("restarted service did not resume at the next model turn: %+v", afterRestart)
	}
}

type r49LoopAdvisor struct {
	root string
	file string
}

func (r49LoopAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "loop-test", MaxDataLevel: "D6"}
}

func (r49LoopAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a r49LoopAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	switch len(input.Transcript) {
	case 0:
		return steward.AgentTurnDecision{Content: "先查看目录。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "call_list", ToolName: "fs.list", Arguments: map[string]any{"path": a.root},
		}}}, nil
	case 1:
		return steward.AgentTurnDecision{Content: "找到候选文件，继续读取。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "call_read", ToolName: "fs.read_text", Arguments: map[string]any{"path": a.file},
		}}}, nil
	default:
		content := ""
		if results := input.Transcript[len(input.Transcript)-1].ToolResults; len(results) > 0 {
			content, _ = results[0].Output["content"].(string)
		}
		return steward.AgentTurnDecision{Content: "已读取真实文件内容：" + content}, nil
	}
}

func TestStewardR49AgentLoopsFromListToReadToFinalAnswer(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 agent loop test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	file := filepath.Join(root, "answer.txt")
	if err := os.WriteFile(file, []byte("R4.9-loop-proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_agent_loop"), "r49-local",
		steward.WithAutonomyAdvisor(r49LoopAdvisor{root: root, file: file}), steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 loop"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "找出目录中的文件，读取内容后告诉我"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 || len(created.Message.Executions) != 1 {
		t.Fatalf("first model turn did not create an episode execution: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	for round := 0; round < 2; round++ {
		if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
		if _, err := node.service.RunAgentEpisodeCycle(ctx, 10); err != nil {
			t.Fatal(err)
		}
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || episode.CurrentRound != 3 || episode.ToolCallCount != 2 || len(episode.Turns) != 3 {
		t.Fatalf("agent episode did not complete three rounds: %+v", episode)
	}
	for _, turn := range episode.Turns {
		if turn.ToolCalls == nil || turn.ToolResults == nil {
			t.Fatalf("agent turn arrays must be encoded as empty arrays, not null: %+v", turn)
		}
	}
	messages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if strings.Contains(message.Content, "R4.9-loop-proof") {
			found = true
		}
	}
	if !found {
		t.Fatalf("final answer did not contain verified tool output: %+v", messages)
	}
}

type r49AskAdvisor struct{}

func (r49AskAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "ask-test", MaxDataLevel: "D6"}
}
func (r49AskAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}
func (r49AskAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{ToolCalls: []domain.StewardAgentToolCall{{ID: "ask_1", ToolName: "steward.ask_user", Arguments: map[string]any{"question": "目标文件叫什么？"}}}}, nil
	}
	response := ""
	if results := input.Transcript[0].ToolResults; len(results) > 0 {
		response, _ = results[0].Output["user_response"].(string)
	}
	return steward.AgentTurnDecision{Content: "收到补充：" + response}, nil
}

func TestStewardR49AskUserResumesSameEpisode(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 ask-user test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_ask_user"), "r49-ask",
		steward.WithAutonomyAdvisor(r49AskAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 ask"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "帮我处理文件"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Message.Episodes) != 1 || first.Message.Episodes[0].Status != "awaiting_input" {
		t.Fatalf("ask_user did not pause the episode: %+v", first.Message)
	}
	episodeID := first.Message.Episodes[0].ID
	if _, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "answer.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunAgentEpisodeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || len(episode.Turns) != 2 || len(episode.Turns[0].ToolResults) != 1 {
		t.Fatalf("answer did not resume the original episode: %+v", episode)
	}
}

func TestStewardR49PauseResumeReplansWithoutRepeatingSideEffects(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 control test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	file := filepath.Join(root, "pause.txt")
	if err := os.WriteFile(file, []byte("pause-proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_pause"), "r49-pause",
		steward.WithAutonomyAdvisor(r49LoopAdvisor{root: root, file: file}), steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 pause"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "检查文件"})
	if err != nil {
		t.Fatal(err)
	}
	episodeID := created.Message.Episodes[0].ID
	paused, err := node.service.DecideAgentEpisode(ctx, episodeID, steward.DecideAgentEpisodeInput{Decision: "pause"})
	if err != nil || paused.Status != "paused" {
		t.Fatalf("episode was not paused: %+v err=%v", paused, err)
	}
	resumed, err := node.service.DecideAgentEpisode(ctx, episodeID, steward.DecideAgentEpisodeInput{Decision: "resume"})
	if err != nil || resumed.Status != "thinking" || resumed.ActiveExecutionID != "" {
		t.Fatalf("episode did not resume at a model boundary: %+v err=%v", resumed, err)
	}
	if _, err := node.service.RunAgentEpisodeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "executing" || episode.CurrentRound != 2 {
		t.Fatalf("resumed episode did not let the model replan: %+v", episode)
	}
}
