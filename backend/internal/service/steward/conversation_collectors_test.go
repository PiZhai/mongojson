package steward

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestOpenAIConversationToolsDescribeRuntimeContract(t *testing.T) {
	tools, names := openAIConversationTools([]domain.StewardToolSpec{{
		Name: "fs.create_directory", Version: "2.1.0", Description: "Create a directory.",
		InputSchema:     map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		OutputSchema:    map[string]any{"type": "object", "properties": map[string]any{"created": map[string]any{"type": "boolean"}}},
		PermissionLevel: PermissionA2, RiskLevel: "low", SideEffect: RuntimeSideEffectWrite,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyInherent, DefaultTimeoutSec: 15,
	}})
	if len(tools) != 1 || names["fs__create_directory"].Name != "fs.create_directory" {
		t.Fatalf("unexpected native tool mapping: tools=%+v names=%+v", tools, names)
	}
	function, _ := tools[0]["function"].(map[string]any)
	description, _ := function["description"].(string)
	if !strings.Contains(description, "permission=A2") || !strings.Contains(description, "side_effect=write") || !strings.Contains(description, "approval=always") {
		t.Fatalf("tool working mode missing from description: %q", description)
	}
}

func TestParseOpenAIConversationTurnUsesNativeToolCalls(t *testing.T) {
	spec := domain.StewardToolSpec{Name: "fs.create_directory", Description: "Create one directory."}
	response, err := parseOpenAIConversationTurn([]byte(`{
		"choices":[{"message":{"content":null,"reasoning_content":"need to create the requested directory","tool_calls":[{
			"id":"call_1","type":"function","function":{"name":"fs__create_directory","arguments":"{\"path\":\"desktop/动漫\"}"}
		}]}}]
	}`), "在桌面创建动漫文件夹", map[string]domain.StewardToolSpec{"fs__create_directory": spec})
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent != "execution" || response.ExecutionPlan == nil || len(response.ExecutionPlan.Steps) != 1 {
		t.Fatalf("unexpected native tool response: %+v", response)
	}
	step := response.ExecutionPlan.Steps[0]
	if step.ToolName != "fs.create_directory" || step.Arguments["path"] != "desktop/动漫" || len(step.ExpectedOutput) != 0 {
		t.Fatalf("native tool call was not translated safely: %+v", step)
	}
	if response.ExecutionPlan.Planner != "native-tool-calling" {
		t.Fatalf("unexpected planner provenance: %+v", response.ExecutionPlan)
	}
	if response.ExecutionPlan.ReasoningContent != "need to create the requested directory" {
		t.Fatalf("thinking-mode state was not retained: %+v", response.ExecutionPlan)
	}
}

func TestParseOpenAIConversationTurnReturnsOrdinaryAssistantText(t *testing.T) {
	response, err := parseOpenAIConversationTurn([]byte(`{"choices":[{"message":{"content":"当前没有可报告的运行中任务。"}}]}`), "你有哪些自动任务在运行？", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent != "answer" || response.Reply != "当前没有可报告的运行中任务。" || response.ExecutionPlan != nil {
		t.Fatalf("unexpected ordinary assistant response: %+v", response)
	}
}

func TestParseOpenAIConversationTurnDoesNotExecutePrivateJSONText(t *testing.T) {
	content := `{"intent":"task","task_candidates":[{"title":"不应创建"}]}`
	payload := `{"choices":[{"message":{"content":"{\"intent\":\"task\",\"task_candidates\":[{\"title\":\"不应创建\"}]}"}}]}`
	response, err := parseOpenAIConversationTurn([]byte(payload), "普通问题", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent != "answer" || response.Reply != content || response.ExecutionPlan != nil {
		t.Fatalf("private JSON text was treated as an action: %+v", response)
	}
}

func TestParseOpenAIConversationTurnLetsModelChooseCreateTaskTool(t *testing.T) {
	spec := domain.StewardToolSpec{Name: "steward.create_task", Description: "Create a durable task."}
	response, err := parseOpenAIConversationTurn([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[{
			"id":"call_task","type":"function","function":{"name":"steward__create_task","arguments":"{\"title\":\"明天检查服务\"}"}
		}]}}]
	}`), "提醒我明天检查服务", map[string]domain.StewardToolSpec{"steward__create_task": spec})
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent != "execution" || response.ExecutionPlan == nil || len(response.ExecutionPlan.Steps) != 1 {
		t.Fatalf("unexpected create-task response: %+v", response)
	}
	step := response.ExecutionPlan.Steps[0]
	if step.ToolName != "steward.create_task" || step.Arguments["title"] != "明天检查服务" {
		t.Fatalf("model create-task tool choice was not preserved: %+v", step)
	}
}

func TestHighSensitivityConversationPayloadRoundTripsEncrypted(t *testing.T) {
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "test-local")
	message := domain.StewardConversationMessage{
		ID: "message-id", ConversationID: "conversation-id", Role: conversationRoleUser,
		DataLevel: DataD5, Content: "[encrypted D5 message]", PayloadEncrypted: true,
	}
	keyring, err := localPayloadKeyringFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := encryptPayloadEnvelope(keyring,
		conversationMessageEncryptionAAD(message.ConversationID, message.ID, message.Role),
		map[string]any{"content": "password=highly-sensitive", "context_summary": "private context"},
		SyncEncryptionScopeLocalAtRest)
	if err != nil {
		t.Fatal(err)
	}
	if err := decryptConversationMessage(&message, envelope); err != nil {
		t.Fatal(err)
	}
	if message.Content != "password=highly-sensitive" || message.ContextSummary != "private context" {
		t.Fatalf("decrypted message = %#v", message)
	}
}

func TestConversationModelContentModesDoNotTreatSummaryAsRaw(t *testing.T) {
	secret := "password=highly-sensitive-value"
	if value := conversationModelText(secret, DataD5, ModelContentMetadata); strings.Contains(value, "highly-sensitive") {
		t.Fatalf("metadata mode leaked content: %q", value)
	}
	if value := conversationModelText(secret, DataD5, ModelContentSummary); strings.Contains(value, "highly-sensitive") {
		t.Fatalf("summary mode leaked credential: %q", value)
	}
	if value := conversationModelText(secret, DataD5, ModelContentRedacted); strings.Contains(value, "highly-sensitive") {
		t.Fatalf("redacted mode leaked credential: %q", value)
	}
	if value := conversationModelText(secret, DataD5, ModelContentRaw); value != secret {
		t.Fatalf("raw mode changed explicitly authorized content: %q", value)
	}
}

func TestConversationDataLevelAcceptsConfiguredSensitivityRange(t *testing.T) {
	for _, expected := range []string{DataD0, DataD1, DataD2, DataD3, DataD4, DataD5, DataD6} {
		level, err := conversationDataLevel(" " + strings.ToLower(expected) + " ")
		if err != nil || level != expected {
			t.Fatalf("conversationDataLevel(%s) = %q, %v", expected, level, err)
		}
	}
	if _, err := conversationDataLevel("D7"); err == nil {
		t.Fatal("expected unsupported conversation data level to be rejected")
	}
}

func TestNormalizeCollectorSettingsRejectsFilesystemRoot(t *testing.T) {
	root := string(filepath.Separator)
	if volume := filepath.VolumeName(t.TempDir()); volume != "" {
		root = volume + string(filepath.Separator)
	}
	_, err := normalizeCollectorSettings("watched-directory", map[string]any{"paths": []any{root}})
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("expected root path rejection, got %v", err)
	}
}

func TestScanDirectoryMetadataDoesNotReadFileContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("private content"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err := scanDirectoryMetadata(root, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RelativePath != "note.txt" || strings.Contains(items[0].Fingerprint, "private content") {
		t.Fatalf("unexpected metadata scan: %+v", items)
	}
}
