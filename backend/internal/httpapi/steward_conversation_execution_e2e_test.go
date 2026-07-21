package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/steward"
)

type r45AgentAdvisor struct {
	multi bool
}

const r45ConversationEchoProof = "r45-conversation-echo-proof"

func (r45AgentAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r45-test", Model: "r45-agent-test"}
}

func (r45AgentAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a r45AgentAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) > 0 {
		return steward.AgentTurnDecision{Content: "工具执行完成。"}, nil
	}
	if a.multi {
		return steward.AgentTurnDecision{Content: "执行两个步骤。", ToolCalls: []domain.StewardAgentToolCall{
			{ID: "collect", ToolName: "runtime.echo", Arguments: map[string]any{"value": "collected"}},
			{ID: "summarize", ToolName: "runtime.echo", Arguments: map[string]any{"value": "summarized"}},
		}}, nil
	}
	switch {
	case strings.Contains(input.Message, "执行内置回显"):
		return steward.AgentTurnDecision{Content: "执行确定性内置工具。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "echo_proof", ToolName: "runtime.echo", Arguments: map[string]any{"value": r45ConversationEchoProof},
		}}}, nil
	default:
		return steward.AgentTurnDecision{ToolCalls: []domain.StewardAgentToolCall{{
			ID: "ask", ToolName: "steward.ask_user", Arguments: map[string]any{"question": "你希望整理哪些内容？"},
		}}}, nil
	}
}

func TestStewardR45ConversationExecutesAndControlsLocalTasks(t *testing.T) {
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r45-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.5 conversation acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r45_conversation"), "r45-local",
		steward.WithAutonomyAdvisor(r45AgentAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.5 acceptance"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{
		Content: "执行内置回显并返回真实证据",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 || created.Message.Episodes[0].Status != "executing" || created.Message.Episodes[0].CurrentRound != 1 {
		t.Fatalf("tool request was not classified and dispatched directly: %+v", created.Message)
	}
	dispatched, err := node.service.GetAgentEpisode(ctx, created.Message.Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != "executing" || dispatched.CurrentRound != 1 || len(dispatched.Turns) != 1 ||
		len(dispatched.Turns[0].ToolCalls) != 1 || dispatched.Turns[0].ToolCalls[0].ToolName != "runtime.echo" {
		t.Fatalf("worker did not dispatch the deterministic builtin tool: %+v", dispatched)
	}
	queuedMessages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	queuedExecution := latestR45Execution(t, queuedMessages)
	if queuedExecution.Kind != "run" || queuedExecution.Status != "queued" || queuedExecution.RequiresConfirmation {
		t.Fatalf("worker did not silently queue the low-risk run: %+v", queuedExecution)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	messages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	echoExecution := latestR45Execution(t, messages)
	if echoExecution.Status != "succeeded" || intFromR45Evidence(echoExecution.Evidence["artifact_count"]) == 0 {
		t.Fatalf("completed builtin execution and evidence did not flow back to conversation: %+v", echoExecution)
	}
	withResult, err := node.service.GetAgentEpisode(ctx, created.Message.Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if withResult.Status != "thinking" || len(withResult.Turns) != 1 || len(withResult.Turns[0].ToolResults) != 1 ||
		withResult.Turns[0].ToolResults[0].Error != "" || withResult.Turns[0].ToolResults[0].Output["value"] != r45ConversationEchoProof {
		t.Fatalf("runtime.echo result was not durably mapped back to the Agent turn: %+v", withResult)
	}
	if _, err := node.service.RunAgentEpisodeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	completed, err := node.service.GetAgentEpisode(ctx, created.Message.Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.CurrentRound != 2 {
		t.Fatalf("Agent did not complete after receiving verified builtin output: %+v", completed)
	}

	question, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "帮我整理一下"})
	if err != nil {
		t.Fatal(err)
	}
	if len(question.Message.Episodes) != 1 || question.Message.Episodes[0].Status != "awaiting_input" {
		t.Fatalf("ambiguous request did not ask for the missing detail directly: %+v", question.Message)
	}
	questionEpisode, err := node.service.GetAgentEpisode(ctx, question.Message.Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	questionMessages, err := node.service.ListConversationMessages(ctx, conversation.ID, 30)
	if err != nil {
		t.Fatal(err)
	}
	foundQuestion := false
	for _, message := range questionMessages {
		foundQuestion = foundQuestion || strings.Contains(message.Content, "整理哪些内容")
	}
	if questionEpisode.Status != "awaiting_input" || !foundQuestion {
		t.Fatalf("worker did not enter the Agent input-wait state: episode=%+v messages=%+v", questionEpisode, questionMessages)
	}
}

type r45MultiStepPlanner struct{}

func (r45MultiStepPlanner) Status() domain.StewardRuntimePlannerStatus {
	return domain.StewardRuntimePlannerStatus{Enabled: true, Provider: "r45-test", Version: "4.5.0"}
}

type r46ModelFirstAdvisor struct {
	target string
}

func (r46ModelFirstAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r46-test", Model: "model-first-test"}
}

func (r46ModelFirstAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a r46ModelFirstAdvisor) Converse(_ context.Context, input steward.ConversationAdvisorInput) (steward.ConversationAdvisorResponse, error) {
	if strings.Contains(input.Message, "读取全部上下文") {
		for _, item := range input.Context {
			if strings.Contains(item.Title, "设备所有者上下文") || strings.Contains(item.Summary, "完整活动与记忆") {
				return steward.ConversationAdvisorResponse{Intent: "answer", Confidence: 0.99, Reply: "模型已经读取完整活动与记忆。"}, nil
			}
		}
		return steward.ConversationAdvisorResponse{Intent: "answer", Confidence: 0.99, Reply: "模型没有收到完整上下文。"}, nil
	}
	if strings.Contains(input.Message, "直接问模型") {
		return steward.ConversationAdvisorResponse{Intent: "question", Confidence: 0.99, Reply: "这条手动消息已经直接交给模型。"}, nil
	}
	if strings.Contains(input.Message, "提醒") {
		return steward.ConversationAdvisorResponse{
			Intent: "execution", Confidence: 0.99, Reply: "创建提醒。",
			ExecutionPlan: &steward.RuntimePlanDraft{Summary: "创建检查模型优先链路提醒", Steps: []steward.CreateAgentRunStepInput{{
				Key: "create_task", Title: "创建提醒", ToolName: "steward.create_task", Arguments: map[string]any{
					"title": "检查模型优先链路", "description": "验证对话创建提醒", "due_at": "2030-01-02T09:00:00+08:00",
				},
			}}},
		}, nil
	}
	if strings.Contains(input.Message, "记得什么") {
		if len(input.Context) == 0 {
			return steward.ConversationAdvisorResponse{Intent: "clarify", Reply: "没有检索到记忆", Clarification: "没有检索到记忆"}, nil
		}
		return steward.ConversationAdvisorResponse{Intent: "memory_query", Confidence: 0.98, Reply: "我从长期记忆中找到了你的偏好。"}, nil
	}
	if len(input.Tools) == 0 || len(input.Devices) == 0 {
		return steward.ConversationAdvisorResponse{}, fmt.Errorf("model did not receive tools and devices")
	}
	return steward.ConversationAdvisorResponse{
		Intent: "execution", Confidence: 0.99, Reply: "开始落实。",
		ExecutionPlan: &steward.RuntimePlanDraft{Summary: "创建模型决定的目录", Steps: []steward.CreateAgentRunStepInput{{
			Key: "mkdir", Title: "创建目录", ToolName: "fs.create_directory", Arguments: map[string]any{"path": a.target},
		}}},
	}, nil
}

func TestStewardR46ModelFirstConversationRoutesExecutionMemoryAndReminder(t *testing.T) {
	startStewardTestCompanion(t, "r46-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.6 conversation acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	target := filepath.Join(root, "model-first", "动漫")
	advisor := r46ModelFirstAdvisor{target: target}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r46_model_first"), "r46-local",
		steward.WithAutonomyAdvisor(advisor), steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.6 model first"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "替我把这件事情真正落实"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.Executions) != 1 || result.Message.Executions[0].Status != "queued" {
		t.Fatalf("model-first plan was not silently queued: %+v", result.Message.Executions)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("model-first directory execution was not verified: %v", err)
	}
	episodes, err := node.service.Search(ctx, steward.SearchInput{Query: "创建模型决定的目录", EntityType: "memory", Limit: 10})
	if err != nil || len(episodes) == 0 {
		t.Fatalf("successful execution was not written to long-term memory: results=%+v err=%v", episodes, err)
	}

	confirmed := true
	if _, err := node.service.CreateMemory(ctx, steward.CreateMemoryInput{Type: "preference", Title: "模型优先偏好", Summary: "喜欢由大模型先理解意图", Content: "喜欢由大模型先理解意图", Scope: "global", Source: "test", UserConfirmed: &confirmed}); err != nil {
		t.Fatal(err)
	}
	memoryReply, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "你记得什么模型优先偏好？"})
	if err != nil || !strings.Contains(memoryReply.Message.Content, "长期记忆") {
		t.Fatalf("memory query did not use retrieved context: message=%+v err=%v", memoryReply.Message, err)
	}
	if _, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "提醒我检查模型优先链路"}); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunConversationExecutionRefreshCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	tasks, err := node.service.Search(ctx, steward.SearchInput{Query: "检查模型优先链路", EntityType: "task", Limit: 10})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("explicit reminder was not persisted as a task: tasks=%+v err=%v", tasks, err)
	}
}

func TestStewardManualMessageDoesNotInheritConversationDataLevel(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r46-manual-message-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed manual message regression test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r46_manual_message"), "r46-manual-message",
		steward.WithAutonomyAdvisor(r46ModelFirstAdvisor{}))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "主动管家"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "把这句话直接问模型"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message.Content != "这条手动消息已经直接交给模型。" || result.Message.Model != "model-first-test" {
		t.Fatalf("manual message was not handled by the model: %+v", result.Message)
	}
	if result.Message.DataLevel != "D0" {
		t.Fatalf("manual message inherited conversation data level: got %s, want D0", result.Message.DataLevel)
	}
	confirmed := true
	if _, err := node.service.CreateMemory(ctx, steward.CreateMemoryInput{
		Type: "owner_context", Title: "设备所有者上下文", Summary: "完整活动与记忆", Content: "完整活动与记忆",
		Scope: "global", Source: "activitywatch", DataLevel: "D2", UserConfirmed: &confirmed,
	}); err != nil {
		t.Fatal(err)
	}
	contextResult, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "读取全部上下文"})
	if err != nil {
		t.Fatal(err)
	}
	if contextResult.Message.Content != "模型已经读取完整活动与记忆。" {
		t.Fatalf("legacy D-level still hid owner context: %+v", contextResult.Message)
	}
}

func (r45MultiStepPlanner) Plan(context.Context, steward.RuntimePlannerInput) (steward.RuntimePlanDraft, error) {
	return steward.RuntimePlanDraft{
		Summary: "两个 Agent 顺序完成任务", Planner: "r45-test", PlannerVersion: "4.5.0",
		Steps: []steward.CreateAgentRunStepInput{
			{Key: "collect", Title: "收集", ToolName: "runtime.echo", Arguments: map[string]any{"value": "collected"}},
			{Key: "summarize", Title: "汇总", ToolName: "runtime.echo", Arguments: map[string]any{"value": "summarized"}, DependsOn: []string{"collect"}},
		},
	}, nil
}

func TestStewardR45ConversationKeepsLocalMultiToolWorkInOneExecution(t *testing.T) {
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r45-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.5 multi-Agent acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r45_multi_agent"), "r45-multi",
		steward.WithAutonomyAdvisor(r45AgentAdvisor{multi: true}), steward.WithRuntimeR2Enabled(true), steward.WithRuntimePlanner(r45MultiStepPlanner{}),
		steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x45}, 32)))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "帮我完成两步整理"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.Episodes) != 1 || result.Message.Episodes[0].Status != "executing" || result.Message.Episodes[0].CurrentRound != 1 {
		t.Fatalf("multi-tool instruction was not dispatched directly: %+v", result.Message)
	}
	queuedMessages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	queuedExecution := latestR45Execution(t, queuedMessages)
	if queuedExecution.Kind != "run" || queuedExecution.RunID == "" || queuedExecution.OrchestrationID != "" {
		t.Fatalf("local tools were expanded into an orchestration: %+v", queuedExecution)
	}
	for index := 0; index < 12; index++ {
		_, _ = node.service.RunAgentRuntimeCycle(ctx, 10)
		_, _ = node.service.RunConversationExecutionRefreshCycle(ctx, 10)
		_, _ = node.service.RunAgentEpisodeCycle(ctx, 10)
	}
	messages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	execution := latestR45Execution(t, messages)
	episode, getErr := node.service.GetAgentEpisode(ctx, result.Message.Episodes[0].ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if execution.Status != "succeeded" || episode.Status != "completed" || len(episode.Turns) != 2 || len(episode.Turns[0].ToolResults) != 2 {
		t.Fatalf("local tool results did not return through one execution: %+v", execution)
	}
}

func latestR45Execution(t *testing.T, messages []domain.StewardConversationMessage) domain.StewardConversationExecution {
	t.Helper()
	for index := len(messages) - 1; index >= 0; index-- {
		if len(messages[index].Executions) > 0 {
			return messages[index].Executions[len(messages[index].Executions)-1]
		}
	}
	t.Fatal("conversation has no execution card")
	return domain.StewardConversationExecution{}
}

func intFromR45Evidence(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}
