package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/steward"
)

func TestStewardR45ConversationExecutesAndControlsLocalTasks(t *testing.T) {
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r45-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.5 conversation acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r45_conversation"), "r45-local",
		steward.WithRuntimeR2Enabled(true), steward.WithRuntimeAllowedRoots(root), steward.WithRuntimeExecutables(goExecutable))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.5 acceptance", DataLevel: "D0"})
	if err != nil {
		t.Fatal(err)
	}

	filePath := filepath.Join(root, "conversation", "proof.txt")
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{
		Content: fmt.Sprintf(`创建文件 "%s" 内容 "由普通对话真实执行"`, filePath), DataLevel: "D0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Executions) != 1 || created.Message.Executions[0].Kind != "run" ||
		created.Message.Executions[0].Status != "queued" || created.Message.Executions[0].RequiresConfirmation {
		t.Fatalf("low-risk conversation was not silently queued: %+v", created.Message.Executions)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	messages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	fileExecution := latestR45Execution(t, messages)
	if fileExecution.Status != "succeeded" || intFromR45Evidence(fileExecution.Evidence["artifact_count"]) == 0 {
		t.Fatalf("completed execution and evidence did not flow back to conversation: %+v", fileExecution)
	}
	content, err := os.ReadFile(filePath)
	if err != nil || string(content) != "由普通对话真实执行" {
		t.Fatalf("conversation did not create expected file: content=%q err=%v", content, err)
	}

	command, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{
		Content: fmt.Sprintf(`运行命令 "%s" version`, goExecutable), DataLevel: "D0",
	})
	if err != nil {
		t.Fatal(err)
	}
	commandExecution := command.Message.Executions[0]
	if commandExecution.Status != "awaiting_confirmation" || !commandExecution.RequiresConfirmation || commandExecution.RiskLevel != "medium" {
		t.Fatalf("medium-risk process did not produce one confirmation card: %+v", commandExecution)
	}
	if _, err := node.service.DecideConversationExecution(ctx, commandExecution.ID, steward.DecideConversationExecutionInput{
		Decision: "confirm", Reason: "R4.5 acceptance",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 5); err != nil {
		t.Fatal(err)
	}
	messages, _ = node.service.ListConversationMessages(ctx, conversation.ID, 30)
	if result := latestR45Execution(t, messages); result.Status != "succeeded" {
		t.Fatalf("confirmed conversation process did not finish: %+v", result)
	}

	paused, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{
		Content: fmt.Sprintf(`运行命令 "%s" env GOROOT`, goExecutable), DataLevel: "D0",
	})
	if err != nil {
		t.Fatal(err)
	}
	pausedID := paused.Message.Executions[0].ID
	if _, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "暂停", DataLevel: "D0"}); err != nil {
		t.Fatal(err)
	}
	continued, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "继续", DataLevel: "D0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(continued.Message.Executions) != 1 || continued.Message.Executions[0].ID != pausedID {
		t.Fatalf("contextual continue did not target the latest execution: %+v", continued.Message.Executions)
	}
	pausedExecution := continued.Message.Executions[0]
	if pausedExecution.Status != "queued" {
		t.Fatalf("paused run was not resumable from conversation: %+v", pausedExecution)
	}

	question, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "帮我整理一下", DataLevel: "D0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(question.Message.Executions) != 1 || question.Message.Executions[0].Status != "needs_input" || question.Message.Executions[0].Question == "" {
		t.Fatalf("ambiguous executable request did not ask a safe follow-up question: %+v", question.Message.Executions)
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
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r46-test", Model: "model-first-test", MaxDataLevel: "D6"}
}

func (r46ModelFirstAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a r46ModelFirstAdvisor) Converse(_ context.Context, input steward.ConversationAdvisorInput) (steward.ConversationAdvisorResponse, error) {
	if strings.Contains(input.Message, "提醒") {
		return steward.ConversationAdvisorResponse{
			Intent: "task", Confidence: 0.99, Reply: "已创建提醒。",
			TaskAction: &steward.ConversationTaskAction{Title: "检查模型优先链路", Description: "验证对话创建提醒", DueAt: "2030-01-02T09:00:00+08:00"},
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
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r46-test")
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
	tasks, err := node.service.Search(ctx, steward.SearchInput{Query: "检查模型优先链路", EntityType: "task", Limit: 10})
	if err != nil || len(tasks) == 0 {
		t.Fatalf("explicit reminder was not persisted as a task: tasks=%+v err=%v", tasks, err)
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

func TestStewardR45ConversationAutomaticallyChoosesMultiAgentOrchestration(t *testing.T) {
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "r45-test")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.5 multi-Agent acceptance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r45_multi_agent"), "r45-multi",
		steward.WithRuntimeR2Enabled(true), steward.WithRuntimePlanner(r45MultiStepPlanner{}),
		steward.WithOrchestrationR4Enabled(true), steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x45}, 32)))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{DataLevel: "D0"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "帮我完成两步整理", DataLevel: "D0"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Message.Executions) != 1 || result.Message.Executions[0].Kind != "orchestration" || result.Message.Executions[0].Status != "queued" {
		t.Fatalf("multi-step instruction did not become a silent orchestration: %+v", result.Message.Executions)
	}
	for index := 0; index < 12; index++ {
		_, _ = node.service.RunOrchestrationCycle(ctx, 10)
		_, _ = node.service.RunConversationExecutionCycle(ctx, 10)
		_, _ = node.service.RunAgentRuntimeCycle(ctx, 10)
	}
	messages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	execution := latestR45Execution(t, messages)
	if execution.Status != "succeeded" || intFromR45Evidence(execution.Evidence["child_run_count"]) != 2 {
		t.Fatalf("multi-Agent state/evidence did not return to conversation: %+v", execution)
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
