package steward

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestLocalRuntimePlannerCompilesSupportedChineseInstructions(t *testing.T) {
	planner := localRuntimePlanner{}
	createPlan, err := planner.Plan(context.Background(), RuntimePlannerInput{Instruction: `创建文件 "C:\work\note.txt" 内容 "完成 R2"`})
	if err != nil {
		t.Fatalf("compile create instruction: %v", err)
	}
	if len(createPlan.Steps) != 1 || createPlan.Steps[0].ToolName != "fs.create_text" || createPlan.Steps[0].Arguments["content"] != "完成 R2" {
		t.Fatalf("unexpected create plan: %+v", createPlan)
	}
	readPlan, err := planner.Plan(context.Background(), RuntimePlannerInput{Instruction: `读取文件 "C:\work\note.txt"`})
	if err != nil {
		t.Fatalf("compile read instruction: %v", err)
	}
	if len(readPlan.Steps) != 1 || readPlan.Steps[0].ToolName != "fs.read_text" || readPlan.Steps[0].Arguments["path"] != `C:\work\note.txt` {
		t.Fatalf("unexpected read plan: %+v", readPlan)
	}
	commandPlan, err := planner.Plan(context.Background(), RuntimePlannerInput{Instruction: `运行命令 "C:\Program Files\Go\bin\go.exe" version`})
	if err != nil {
		t.Fatalf("compile command instruction: %v", err)
	}
	if commandPlan.Steps[0].Arguments["command"] != `C:\Program Files\Go\bin\go.exe` {
		t.Fatalf("quoted Windows executable path was not preserved: %+v", commandPlan)
	}
	directoryPlan, err := planner.Plan(context.Background(), RuntimePlannerInput{Instruction: "在桌面创建文件夹：动漫"})
	if err != nil {
		t.Fatalf("compile directory instruction: %v", err)
	}
	if directoryPlan.Steps[0].ToolName != "fs.create_directory" || directoryPlan.Steps[0].Arguments["path"] != "桌面"+string(os.PathSeparator)+"动漫" {
		t.Fatalf("unexpected directory plan: %+v", directoryPlan)
	}
}

func TestLocalRuntimePlannerRejectsDisabledTool(t *testing.T) {
	service := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeBrowserOpenEnabled(false))
	_, err := (localRuntimePlanner{}).Plan(context.Background(), RuntimePlannerInput{
		Instruction: "打开网页 https://example.com",
		Tools:       service.runtimeTools.specs(),
	})
	if !errors.Is(err, ErrRuntimePlannerToolUnavailable) || !strings.Contains(err.Error(), "browser.open_url") {
		t.Fatalf("disabled browser planner error = %v", err)
	}
}

func TestLocalRuntimePlannerCompilesOnlyExactR30BrokerCapability(t *testing.T) {
	planner := localRuntimePlanner{}
	tools := []domain.StewardToolSpec{{Name: "privilege.execute"}}
	plan, err := planner.Plan(context.Background(), RuntimePlannerInput{
		Instruction: "执行高权限能力 tool:restart-approved-service", Tools: tools,
	})
	if err != nil {
		t.Fatalf("compile explicit R3 capability: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].ToolName != "privilege.execute" ||
		plan.Steps[0].Arguments["capability"] != "tool:restart-approved-service" {
		t.Fatalf("unexpected R3 plan: %+v", plan)
	}
	_, err = planner.Plan(context.Background(), RuntimePlannerInput{
		Instruction: "执行高权限能力 重启服务", Tools: tools,
	})
	if !errors.Is(err, ErrRuntimePlannerUnsupported) {
		t.Fatalf("ambiguous privileged action was not rejected: %v", err)
	}
}

func TestRuntimeR2PolicyCannotBeDowngradedByPlanInput(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil,
		WithRuntimeR2Enabled(true),
		WithRuntimeAllowedRoots(root),
		WithRuntimeExecutables(executable),
	)

	create, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "create a file", PermissionCeiling: PermissionA2,
		Steps: []CreateAgentRunStepInput{{
			Key: "create", ToolName: "fs.create_text", MaxAttempts: 4,
			Arguments: map[string]any{"path": filepath.Join(root, "note.txt"), "content": "safe"},
		}},
	})
	if err != nil {
		t.Fatalf("normalize create plan: %v", err)
	}
	step := create.Steps[0]
	if !step.RequiresApproval || step.PolicyDecision != RuntimePolicyApproval || step.ToolIdempotency != RuntimeIdempotencyKeyed {
		t.Fatalf("side-effect policy was not enforced: %+v", step)
	}

	command, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "run an executable", PermissionCeiling: PermissionA3,
		Steps: []CreateAgentRunStepInput{{
			Key: "command", ToolName: "shell.exec", MaxAttempts: 8,
			Arguments: map[string]any{"command": executable, "working_directory": root},
		}},
	})
	if err != nil {
		t.Fatalf("normalize command plan: %v", err)
	}
	if command.Steps[0].MaxAttempts != 1 || command.Steps[0].ToolIdempotency != RuntimeIdempotencyNonIdempotent || !command.Steps[0].RequiresApproval {
		t.Fatalf("non-idempotent policy was not enforced: %+v", command.Steps[0])
	}

	_, _, err = service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "exceed permission ceiling", PermissionCeiling: PermissionA1,
		Steps: []CreateAgentRunStepInput{{
			Key: "create", ToolName: "fs.create_text",
			Arguments: map[string]any{"path": filepath.Join(root, "denied.txt"), "content": "no"},
		}},
	})
	if !errors.Is(err, ErrRuntimePolicyDenied) {
		t.Fatalf("permission ceiling error = %v, want ErrRuntimePolicyDenied", err)
	}
}

func TestRuntimePlannerAllowsUnauthenticatedFallbackOnlyOnLoopback(t *testing.T) {
	t.Setenv("STEWARD_RUNTIME_PLANNER_PROVIDER", "openai-compatible")
	t.Setenv("STEWARD_RUNTIME_PLANNER_MODEL", "test-model")
	t.Setenv("STEWARD_RUNTIME_PLANNER_API_KEY", "")
	t.Setenv("STEWARD_LLM_API_KEY", "")
	t.Setenv("STEWARD_RUNTIME_PLANNER_ALLOW_NO_API_KEY", "true")
	t.Setenv("STEWARD_RUNTIME_PLANNER_BASE_URL", "https://planner.example.test/v1")
	status := newRuntimePlannerFromEnv().Status()
	if !status.Enabled || status.Provider != "local-rules" || !strings.Contains(status.Reason, "restricted to loopback") {
		t.Fatalf("remote unauthenticated planner was not disabled safely: %+v", status)
	}

	t.Setenv("STEWARD_RUNTIME_PLANNER_BASE_URL", "http://127.0.0.1:11434/v1")
	status = newRuntimePlannerFromEnv().Status()
	if !status.Enabled || status.Provider != "local-rules+openai-compatible" || status.Model != "test-model" {
		t.Fatalf("loopback planner fallback was not enabled: %+v", status)
	}
}

func TestRuntimeR2RejectsPathsOutsideAllowedRoots(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	service := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeAllowedRoots(root))
	_, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "escape root", PermissionCeiling: PermissionA2,
		Steps: []CreateAgentRunStepInput{{
			Key: "escape", ToolName: "fs.create_text",
			Arguments: map[string]any{"path": outside, "content": "blocked"},
		}},
	})
	if !errors.Is(err, ErrRuntimeToolInput) || !errors.Is(err, ErrRuntimePathDenied) {
		t.Fatalf("path escape error = %v, want tool-input and path-denied errors", err)
	}
}

func TestRuntimeR2RejectsPlannerFieldsOutsideToolSchema(t *testing.T) {
	root := t.TempDir()
	service := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeAllowedRoots(root))
	_, _, err := service.normalizeAgentRunInput(CreateAgentRunInput{
		Goal: "do not silently ignore planner fields", PermissionCeiling: PermissionA1,
		Steps: []CreateAgentRunStepInput{{
			Key: "read", ToolName: "fs.read_text",
			Arguments: map[string]any{"path": filepath.Join(root, "note.txt"), "hidden_option": true},
		}},
	})
	if !errors.Is(err, ErrRuntimeToolInput) || !strings.Contains(err.Error(), "unsupported input field") {
		t.Fatalf("unknown tool input error = %v", err)
	}
}

func TestRuntimeCreateTextIsCreateOnlyAndReconcilesMatchingContent(t *testing.T) {
	root := t.TempDir()
	service := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeAllowedRoots(root))
	tool := newRuntimeCreateTextTool(service)
	path := filepath.Join(root, "nested", "note.txt")
	input := map[string]any{"path": path, "content": "verified", "create_parents": true}

	created, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("create text: %v", err)
	}
	if created.Output["created"] != true || created.Output["reconciled"] != false {
		t.Fatalf("unexpected create output: %+v", created.Output)
	}
	if err := tool.Verify(context.Background(), input, created.Output, nil); err != nil {
		t.Fatalf("verify created text: %v", err)
	}

	reconciled, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("reconcile matching text: %v", err)
	}
	if reconciled.Output["created"] != false || reconciled.Output["reconciled"] != true {
		t.Fatalf("unexpected reconcile output: %+v", reconciled.Output)
	}

	_, err = tool.Execute(context.Background(), map[string]any{"path": path, "content": "different"})
	if err == nil || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("different existing content error = %v", err)
	}
}

func TestRuntimeCreateDirectoryToolCreatesAndReconciles(t *testing.T) {
	root := t.TempDir()
	service := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeAllowedRoots(root))
	tool := newRuntimeCreateDirectoryTool(service)
	target := filepath.Join(root, "nested", "动漫")
	input := map[string]any{"path": target}

	first, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Output["created"] != true || first.Output["reconciled"] != false {
		t.Fatalf("unexpected first directory result: %+v", first.Output)
	}
	if err := tool.Verify(context.Background(), input, first.Output, nil); err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.Output["created"] != false || second.Output["reconciled"] != true {
		t.Fatalf("unexpected reconciled directory result: %+v", second.Output)
	}
}

func TestExpandRuntimeKnownFolderDesktopAlias(t *testing.T) {
	desktop := runtimeKnownFolders()["desktop"]
	if desktop == "" {
		t.Skip("current user has no discoverable desktop folder")
	}
	want := filepath.Join(desktop, "动漫")
	if got := expandRuntimeKnownFolder("桌面/动漫"); got != want {
		t.Fatalf("desktop alias resolved to %q, want %q", got, want)
	}
}

func TestRuntimeWebFetchBlocksPrivateHostsUnlessExplicitlyAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("runtime-r2"))
	}))
	defer server.Close()

	blockedService := NewService(nil, WithRuntimeR2Enabled(true))
	if err := newRuntimeWebFetchTool(blockedService).(RuntimeToolValidator).Validate(map[string]any{"url": server.URL}); err == nil {
		t.Fatal("private HTTP host was accepted without an explicit allowlist entry")
	}

	allowedService := NewService(nil, WithRuntimeR2Enabled(true), WithRuntimeWebAllowedHosts("127.0.0.1"))
	tool := newRuntimeWebFetchTool(allowedService)
	result, err := tool.Execute(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("fetch explicitly allowed local host: %v", err)
	}
	if result.Output["content"] != "runtime-r2" || result.Output["status_code"] != 200 {
		t.Fatalf("unexpected web fetch output: %+v", result.Output)
	}
}

func TestRuntimeShellVerifierDoesNotMutateExpectedOutput(t *testing.T) {
	expected := map[string]any{"stdout_contains": "ready", "exit_code": 0}
	err := (runtimeShellExecTool{}).Verify(context.Background(), nil, map[string]any{"exit_code": 0, "stdout": "ready\n", "stderr": ""}, expected)
	if err != nil {
		t.Fatalf("verify shell output: %v", err)
	}
	if expected["stdout_contains"] != "ready" {
		t.Fatalf("shell verifier mutated persisted expected output: %+v", expected)
	}
}
