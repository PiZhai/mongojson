package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

func TestStewardR50WindowsCatalogPersistsAndIsQueryable(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R5.0 catalog test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r50_catalog"), "r50-catalog", steward.WithRuntimeR2Enabled(true))
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, node.apiBase+"/steward/tools", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tool catalog endpoint returned %s", response.Status)
	}
	var payload struct {
		Tools []domain.StewardTool `json:"tools"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tools) < 100 {
		t.Fatalf("expected at least 100 active and retained tools, got %d", len(payload.Tools))
	}
	foundSession, foundToolsmith := false, false
	for _, tool := range payload.Tools {
		if tool.Name == "screen.capture" && tool.ExecutionTarget == "session" && tool.ActiveVersion != "" {
			foundSession = true
		}
		if tool.Name == "tool.create" && tool.Enabled {
			foundToolsmith = true
		}
	}
	if !foundSession || !foundToolsmith {
		t.Fatalf("catalog missing session or Toolsmith capability: session=%v toolsmith=%v", foundSession, foundToolsmith)
	}
}

type r50ToolsmithAdvisor struct{}

func (r50ToolsmithAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r50-test", Model: "toolsmith-test"}
}

type durableFirstTurnAdvisor struct {
	calls           int
	sawQueueMessage bool
}

func (a *durableFirstTurnAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "durable-test", Model: "durable-test"}
}

func (a *durableFirstTurnAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a *durableFirstTurnAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	a.calls++
	for _, message := range input.History {
		if strings.Contains(message.Content, "持续分析、调用工具") {
			a.sawQueueMessage = true
		}
	}
	return steward.AgentTurnDecision{Content: "后台模型回合已经完成。"}, nil
}

func TestStewardSimpleConversationAnswersWithoutCreatingAgentEpisode(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the durable first-turn test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	advisor := &durableFirstTurnAdvisor{}
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_durable_first_turn"), "agent-durable-first-turn",
		steward.WithAutonomyAdvisor(advisor))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "durable first turn"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "你在做什么？"})
	if err != nil {
		t.Fatal(err)
	}
	if advisor.calls != 1 {
		t.Fatalf("simple conversation should call the model exactly once: calls=%d", advisor.calls)
	}
	if result.Message.Content != "后台模型回合已经完成。" || len(result.Message.Episodes) != 0 || len(result.Message.Executions) != 0 {
		t.Fatalf("simple conversation was turned into execution state: %+v", result.Message)
	}
	processed, err := node.service.RunAgentEpisodeCycle(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || advisor.calls != 1 || advisor.sawQueueMessage {
		t.Fatalf("simple answer leaked into the Agent worker: processed=%d calls=%d queue_in_history=%v", processed, advisor.calls, advisor.sawQueueMessage)
	}
}
func (r50ToolsmithAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}
func (r50ToolsmithAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		manifest := map[string]any{
			"name": "custom.echo_once", "version": "1.0.0", "title": "Echo once", "description": "Versioned composite echo used by R5 acceptance.",
			"runtime": "composite", "execution_target": "auto", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}},
			"output_schema": map[string]any{"type": "object"}, "dependency_strategy": map[string]any{"requested": "auto", "selected": "none", "selection_reason": "existing runtime.echo is sufficient"},
			"supports_cancel": true, "side_effect": "none", "idempotency_mode": "inherent",
			"tests":           []map[string]any{{"name": "smoke", "input": map[string]any{"value": "test"}}},
			"composite_steps": []map[string]any{{"key": "echo", "tool_name": "runtime.echo", "arguments": map[string]any{"value": "${input.value}"}}},
		}
		return steward.AgentTurnDecision{Content: "创建缺失的组合能力。", ToolCalls: []domain.StewardAgentToolCall{{ID: "create_tool", ToolName: "tool.create", Arguments: map[string]any{"manifest": manifest}}}}, nil
	}
	if len(input.Transcript) == 1 {
		found := false
		for _, spec := range input.Tools {
			if spec.Name == "custom.echo_once" {
				found = true
			}
		}
		if !found {
			return steward.AgentTurnDecision{Content: "新工具未在下一轮热加载。"}, nil
		}
		return steward.AgentTurnDecision{Content: "立即调用新工具。", ToolCalls: []domain.StewardAgentToolCall{{ID: "invoke_new", ToolName: "custom.echo_once", Arguments: map[string]any{"value": "ready"}}}}, nil
	}
	return steward.AgentTurnDecision{Content: "新工具已经在同一 Episode 创建、测试、热加载并成功调用。"}, nil
}

func TestStewardR50ToolsmithCreatesAndInvokesToolInSameEpisode(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "true")
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the R5.0 Toolsmith test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r50_toolsmith"), "r50-toolsmith",
		steward.WithAutonomyAdvisor(r50ToolsmithAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R5 Toolsmith"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "创建并使用一个 echo 组合工具"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 {
		t.Fatalf("expected one episode: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	for index := 0; index < 30; index++ {
		_, _ = node.service.RunAgentRuntimeCycle(ctx, 10)
		_, _ = node.service.RunConversationExecutionRefreshCycle(ctx, 10)
		_, _ = node.service.RunAgentEpisodeCycle(ctx, 10)
		episode, getErr := node.service.GetAgentEpisode(ctx, episodeID)
		if getErr == nil && episode.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || len(episode.Turns) != 3 {
		t.Fatalf("Toolsmith episode did not complete: %+v", episode)
	}
	tool, err := node.service.GetTool(ctx, "custom.echo_once")
	if err != nil {
		t.Fatal(err)
	}
	if !tool.Enabled || tool.ActiveVersion != "1.0.0" || len(tool.RecentTests) == 0 || tool.RecentTests[0].Status != "passed" {
		t.Fatalf("generated tool was not validated and enabled: %+v", tool)
	}
}

func TestStewardR50ToolsmithRejectsBareOutputAndRecoversWithNewVersion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell package protocol acceptance runs on Windows")
	}
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the R5.0 Toolsmith protocol test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r50_tool_protocol"), "r50-tool-protocol", steward.WithRuntimeR2Enabled(true))
	manifest := steward.ToolPackageManifest{
		Name: "custom.protocol_probe", Version: "1.0.0", Title: "Protocol probe", Description: "Verify generated Tool Host protocol failures are actionable.",
		Runtime: "powershell", ExecutionTarget: "system", Entrypoint: "tool.ps1",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}}, "required": []string{"count"}},
		Files: []steward.ToolPackageFile{{Path: "tool.ps1", Content: `[Console]::In.ReadLine() | Out-Null
[Console]::Out.WriteLine('{"count":0}')`}},
		Tests:              []steward.ToolPackageTest{{Name: "protocol", Input: map[string]any{}, Expected: map[string]any{"count": float64(0)}}},
		DependencyStrategy: steward.ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "PowerShell standard library only"},
		SupportsCancel:     true, IdempotencyMode: "inherent", SideEffect: "none",
	}
	if _, err := node.service.CreateToolPackage(ctx, steward.CreateToolPackageInput{Manifest: manifest}); err == nil || !strings.Contains(err.Error(), `missing required boolean field "ok"`) {
		t.Fatalf("bare business output was not rejected with protocol guidance: %v", err)
	}
	failed, err := node.service.GetTool(ctx, manifest.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Versions) != 1 || failed.Versions[0].Status != "failed" || failed.HealthStatus != "failed" {
		t.Fatalf("failed package status was not persisted: %+v", failed)
	}
	if _, err := node.service.CreateToolPackage(ctx, steward.CreateToolPackageInput{Manifest: manifest}); err == nil || !strings.Contains(err.Error(), "tool.update") || !strings.Contains(err.Error(), "1.0.1") {
		t.Fatalf("immutable version conflict lacks next-version guidance: %v", err)
	}
	manifest.Version = "1.0.1"
	manifest.Files[0].Content = `[Console]::In.ReadLine() | Out-Null
[Console]::Out.WriteLine('{"ok":true,"output":{"count":0},"evidence":[]}')`
	created, err := node.service.CreateToolPackage(ctx, steward.CreateToolPackageInput{Manifest: manifest})
	if err != nil {
		t.Fatalf("publish repaired protocol version: %v", err)
	}
	if !created.Enabled || created.ActiveVersion != "1.0.1" || created.HealthStatus != "healthy" {
		t.Fatalf("repaired package was not activated: %+v", created)
	}
}

type r49ParallelAdvisor struct{}

func (r49ParallelAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "parallel-test"}
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
	if len(created.Message.Episodes) != 1 || created.Message.Episodes[0].Status != "executing" ||
		created.Message.Episodes[0].CurrentRound != 1 || len(created.Message.Executions) != 1 {
		t.Fatalf("parallel tool request was not dispatched directly: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	dispatched, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.Status != "executing" || dispatched.CurrentRound != 1 || dispatched.ActiveExecutionID == "" {
		t.Fatalf("worker did not create the first parallel tool batch: %+v", dispatched)
	}
	queuedMessages, err := node.service.ListConversationMessages(ctx, conversation.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	queuedExecution := latestR45Execution(t, queuedMessages)
	if queuedExecution.ID != dispatched.ActiveExecutionID || queuedExecution.Kind != "run" {
		t.Fatalf("parallel tool calls did not stay in one ordinary execution: %+v", queuedExecution)
	}
	for index := 0; index < 12; index++ {
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

type r49CollectAllAdvisor struct{ missingPath string }

func (r49CollectAllAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "collect-all-test"}
}
func (r49CollectAllAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}
func (a r49CollectAllAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	if len(input.Transcript) == 0 {
		return steward.AgentTurnDecision{Content: "执行三个独立工具。", ToolCalls: []domain.StewardAgentToolCall{
			{ID: "collect_success_a", ToolName: "runtime.echo", Arguments: map[string]any{"value": "alpha"}},
			{ID: "collect_failure_b", ToolName: "fs.read_text", Arguments: map[string]any{"path": a.missingPath}},
			{ID: "collect_success_c", ToolName: "runtime.echo", Arguments: map[string]any{"value": "charlie"}},
		}}, nil
	}
	results := input.Transcript[0].ToolResults
	if len(results) != 3 || results[0].ToolCallID != "collect_success_a" || results[1].ToolCallID != "collect_failure_b" || results[2].ToolCallID != "collect_success_c" {
		return steward.AgentTurnDecision{Content: "三个工具结果的原始调用 ID 或顺序错误。"}, nil
	}
	if results[0].Error != "" || results[1].Error == "" || results[2].Error != "" || len(results[2].Output) == 0 {
		return steward.AgentTurnDecision{Content: "独立工具失败导致结果缺失或错绑。"}, nil
	}
	return steward.AgentTurnDecision{Content: "三个独立工具均返回了对应结果，失败未中断后续工具。"}, nil
}

func TestStewardR49CollectAllPreservesThreeCallResultsAcrossFailure(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed R4.9 collect-all Agent test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	missing := filepath.Join(root, "missing.txt")
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_collect_all_agent"), "r49-collect-all-agent",
		steward.WithAutonomyAdvisor(r49CollectAllAdvisor{missingPath: missing}), steward.WithRuntimeR2Enabled(true),
		steward.WithRuntimeAllowedRoots(root), steward.WithOrchestrationR4Enabled(true),
		steward.WithOrchestrationSigningKey(bytes.Repeat([]byte{0x4a}, 32)))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 collect all"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "同时执行三个独立检查，即使一个失败也继续"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 {
		t.Fatalf("three tool calls did not create one Agent Episode: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	for index := 0; index < 20; index++ {
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
		episode, getErr := node.service.GetAgentEpisode(ctx, episodeID)
		if getErr == nil && episode.Status == "completed" {
			break
		}
	}
	episode, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if episode.Status != "completed" || len(episode.Turns) != 2 || len(episode.Turns[0].ToolResults) != 3 {
		t.Fatalf("collect-all Agent episode did not complete with three results: %+v", episode)
	}
	results := episode.Turns[0].ToolResults
	if results[0].ToolCallID != "collect_success_a" || results[0].Error != "" ||
		results[1].ToolCallID != "collect_failure_b" || results[1].Error == "" ||
		results[2].ToolCallID != "collect_success_c" || results[2].Error != "" || len(results[2].Output) == 0 {
		t.Fatalf("collect-all results were missing or misbound: %+v", results)
	}
}

type r49RecoveryAdvisor struct{}

func (r49RecoveryAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "recovery-test"}
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
	if len(created.Message.Episodes) != 1 || created.Message.Episodes[0].Status != "executing" || created.Message.Episodes[0].CurrentRound != 1 {
		t.Fatalf("recovery request was not dispatched directly: %+v", created.Message)
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
		steward.WithStorageDir(t.TempDir()), steward.WithAutonomyAdvisor(r49RecoveryAdvisor{}), steward.WithRuntimeR2Enabled(true))
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

func TestStewardR49RestartReconcilesAlreadyTerminalConversationExecution(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed terminal projection recovery test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dbConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_terminal_projection")
	node := newStewardHTTPNode(t, ctx, dbConfig.Copy(), "r49-terminal-projection",
		steward.WithAutonomyAdvisor(r49RecoveryAdvisor{}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 terminal projection recovery"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "执行一次，模拟终态投影后立即重启"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 {
		t.Fatalf("first tool Episode was not persisted: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	dispatched, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil || dispatched.Status != "executing" || dispatched.ActiveExecutionID == "" {
		t.Fatalf("first model turn did not create its child execution: episode=%+v err=%v", dispatched, err)
	}
	executionID := dispatched.ActiveExecutionID
	if _, err := node.service.RunAgentRuntimeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	// Reproduce the crash window: the child status was projected as terminal,
	// but its callback did not commit tool_results / Episode=thinking.
	if _, err := node.pool.Exec(ctx, `update steward_conversation_executions
		set status='succeeded',completed_at=now(),updated_at=now() where id=$1`, executionID); err != nil {
		t.Fatal(err)
	}
	before, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil || before.Status != "executing" {
		t.Fatalf("test did not establish the executing/terminal crash window: episode=%+v err=%v", before, err)
	}

	restartedPool, err := pgxpool.NewWithConfig(ctx, dbConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restartedPool.Close)
	restarted := steward.NewService(&database.DB{Pool: restartedPool}, steward.WithAgentID("r49-terminal-projection"),
		steward.WithStorageDir(t.TempDir()), steward.WithAutonomyAdvisor(r49RecoveryAdvisor{}), steward.WithRuntimeR2Enabled(true))
	if err := restarted.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if refreshed, err := restarted.RunConversationExecutionRefreshCycle(ctx, 10); err != nil || refreshed == 0 {
		t.Fatalf("terminal execution was not selected for reconciliation: refreshed=%d err=%v", refreshed, err)
	}
	reconciled, err := restarted.GetAgentEpisode(ctx, episodeID)
	if err != nil || reconciled.Status != "thinking" || len(reconciled.Turns) != 1 || len(reconciled.Turns[0].ToolResults) != 1 {
		t.Fatalf("terminal execution did not restore its Episode tool turn: episode=%+v err=%v", reconciled, err)
	}
	if _, err := restarted.RunAgentEpisodeCycle(ctx, 10); err != nil {
		t.Fatal(err)
	}
	final, err := restarted.GetAgentEpisode(ctx, episodeID)
	if err != nil || final.Status != "completed" || final.ToolCallCount != 1 {
		t.Fatalf("reconciled Episode did not continue at the next turn: episode=%+v err=%v", final, err)
	}
	var invocationCount int
	if err := restartedPool.QueryRow(ctx, `select count(*) from steward_tool_invocations invocation
		join steward_conversation_executions execution on execution.run_id=invocation.run_id
		where execution.id=$1`, executionID).Scan(&invocationCount); err != nil {
		t.Fatal(err)
	}
	if invocationCount != 1 {
		t.Fatalf("terminal reconciliation repeated a side effect: invocations=%d", invocationCount)
	}
}

type r49LoopAdvisor struct {
	content string
}

func (r49LoopAdvisor) Status() domain.StewardAutonomyAdvisorStatus {
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "loop-test"}
}

func (r49LoopAdvisor) Suggest(context.Context, steward.AutonomyAdvisorInput) (steward.AutonomyAdvisorSuggestion, error) {
	return steward.AutonomyAdvisorSuggestion{}, nil
}

func (a r49LoopAdvisor) NextTurn(_ context.Context, input steward.AgentTurnInput) (steward.AgentTurnDecision, error) {
	switch len(input.Transcript) {
	case 0:
		return steward.AgentTurnDecision{Content: "先查看目录。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "call_list", ToolName: "runtime.echo", Arguments: map[string]any{"value": map[string]any{"entries": []any{"answer.txt"}}},
		}}}, nil
	case 1:
		return steward.AgentTurnDecision{Content: "找到候选文件，继续读取。", ToolCalls: []domain.StewardAgentToolCall{{
			ID: "call_read", ToolName: "runtime.echo", Arguments: map[string]any{"value": map[string]any{"content": a.content}},
		}}}, nil
	default:
		content := ""
		if results := input.Transcript[len(input.Transcript)-1].ToolResults; len(results) > 0 {
			if value, ok := results[0].Output["value"].(map[string]any); ok {
				content, _ = value["content"].(string)
			}
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
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_agent_loop"), "r49-local",
		steward.WithAutonomyAdvisor(r49LoopAdvisor{content: "R4.9-loop-proof"}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 loop"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "找出目录中的文件，读取内容后告诉我"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 || created.Message.Episodes[0].Status != "executing" ||
		created.Message.Episodes[0].CurrentRound != 1 || len(created.Message.Executions) != 1 {
		t.Fatalf("agent tool request was not dispatched directly: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	firstTurn, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if firstTurn.Status != "executing" || firstTurn.CurrentRound != 1 || firstTurn.ActiveExecutionID == "" {
		t.Fatalf("worker did not dispatch the first list tool turn: %+v", firstTurn)
	}
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
	return domain.StewardAutonomyAdvisorStatus{Enabled: true, Provider: "r49-test", Model: "ask-test"}
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
	if len(first.Message.Episodes) != 1 || first.Message.Episodes[0].Status != "awaiting_input" || first.Message.Episodes[0].CurrentRound != 1 {
		t.Fatalf("ask-user request did not ask directly: %+v", first.Message)
	}
	episodeID := first.Message.Episodes[0].ID
	waiting, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "awaiting_input" || waiting.CurrentRound != 1 {
		t.Fatalf("worker did not pause the episode for ask_user: %+v", waiting)
	}
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
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "runtime_r49_pause"), "r49-pause",
		steward.WithAutonomyAdvisor(r49LoopAdvisor{content: "pause-proof"}), steward.WithRuntimeR2Enabled(true))
	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "R4.9 pause"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "检查文件"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Message.Episodes) != 1 || created.Message.Episodes[0].Status != "executing" || created.Message.Episodes[0].CurrentRound != 1 {
		t.Fatalf("pause request was not dispatched directly: %+v", created.Message)
	}
	episodeID := created.Message.Episodes[0].ID
	firstTurn, err := node.service.GetAgentEpisode(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if firstTurn.Status != "executing" || firstTurn.CurrentRound != 1 || firstTurn.ActiveExecutionID == "" {
		t.Fatalf("worker did not dispatch the first tool turn before pause: %+v", firstTurn)
	}
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
	if episode.Status != "executing" || episode.CurrentRound != 2 || episode.ToolCallCount != 2 || len(episode.Turns) != 2 ||
		len(episode.Turns[1].ToolCalls) != 1 || episode.Turns[1].ToolCalls[0].ID != "call_read" {
		t.Fatalf("resumed episode did not let the model replan: %+v", episode)
	}
}
