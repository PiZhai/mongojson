package steward

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestParseConversationAdvisorResponseNormalizesCandidates(t *testing.T) {
	response, err := parseConversationAdvisorResponse("```json\n{\"reply\":\"收到\",\"intent_candidates\":[],\"memory_candidates\":[{\"title\":\"项目偏好\",\"summary\":\"偏好本地运行\"}],\"task_candidates\":[{\"title\":\"检查服务\",\"suggested_action\":\"创建本地任务\"}]}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if response.Reply != "收到" || len(response.MemoryCandidates) != 1 || len(response.TaskCandidates) != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestParseConversationAdvisorResponseAcceptsUnifiedExecutionIntent(t *testing.T) {
	response, err := parseConversationAdvisorResponse(`{
		"intent":"execution","confidence":0.98,"reply":"开始执行","target_device":"local-s1",
		"execution_plan":{"summary":"创建目录","steps":[{"key":"mkdir","title":"创建目录","tool_name":"fs.create_directory","arguments":{"path":"desktop/动漫"}}]},
		"task_action":null,"intent_candidates":[],"memory_candidates":[],"task_candidates":[]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if response.Intent != "execution" || response.ExecutionPlan == nil || len(response.ExecutionPlan.Steps) != 1 {
		t.Fatalf("unexpected unified response: %+v", response)
	}
	if response.ExecutionPlan.Planner != "conversation-model" || response.ExecutionPlan.Steps[0].ToolName != "fs.create_directory" {
		t.Fatalf("execution plan was not normalized: %+v", response.ExecutionPlan)
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

func TestExplicitConversationCandidatesRemainConfirmationOnly(t *testing.T) {
	result := mergeExplicitConversationCandidates(ConversationAdvisorResponse{Reply: "ok"}, "记住我偏好本地运行，并提醒我明天检查服务")
	if len(result.MemoryCandidates) != 1 || len(result.TaskCandidates) != 1 {
		t.Fatalf("expected memory and task candidates: %+v", result)
	}
}

func TestExplicitConversationCandidateOptOutOverridesKeywords(t *testing.T) {
	response := ConversationAdvisorResponse{
		Reply:          "ok",
		TaskCandidates: []ConversationAdvisorCandidate{{Title: "should be removed"}},
	}
	result := mergeExplicitConversationCandidates(response, "请确认服务正常，不要创建任务或记忆候选")
	if len(result.IntentCandidates) != 0 || len(result.MemoryCandidates) != 0 || len(result.TaskCandidates) != 0 {
		t.Fatalf("candidate opt-out was ignored: %+v", result)
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
