package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/steward"
)

func TestStewardConversationArchiveAndRestore(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed conversation archive test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "conversation_archive"), "archive-test")

	conversation, err := node.service.CreateConversation(ctx, steward.CreateConversationInput{Title: "需要归档的对话"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.service.SendConversationMessage(ctx, conversation.ID, steward.SendConversationMessageInput{Content: "这条消息归档后仍需保留"}); err != nil {
		t.Fatal(err)
	}
	messagesBefore, err := node.service.ListConversationMessages(ctx, conversation.ID, 100)
	if err != nil {
		t.Fatal(err)
	}

	patchConversation := func(archived bool) domain.StewardConversation {
		t.Helper()
		body, _ := json.Marshal(map[string]bool{"archived": archived})
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPatch, node.apiBase+"/steward/conversations/"+conversation.ID, bytes.NewReader(body))
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch conversation status = %d", resp.StatusCode)
		}
		var result struct {
			Conversation domain.StewardConversation `json:"conversation"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return result.Conversation
	}

	archived := patchConversation(true)
	if archived.ArchivedAt == nil {
		t.Fatal("expected archived_at after archiving")
	}
	activeItems, err := node.service.ListConversations(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeItems) != 0 {
		t.Fatalf("active conversations = %d, want 0", len(activeItems))
	}

	resp, err := http.Get(node.apiBase + "/steward/conversations?archived=true&limit=30")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var archivedResult struct {
		Conversations []domain.StewardConversation `json:"conversations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&archivedResult); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(archivedResult.Conversations) != 1 || archivedResult.Conversations[0].ID != conversation.ID {
		t.Fatalf("archived list status=%d conversations=%+v", resp.StatusCode, archivedResult.Conversations)
	}

	restored := patchConversation(false)
	if restored.ArchivedAt != nil {
		t.Fatal("expected archived_at to be cleared after restore")
	}
	activeItems, err = node.service.ListConversations(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeItems) != 1 || activeItems[0].ID != conversation.ID {
		t.Fatalf("restored active conversations=%+v", activeItems)
	}
	messagesAfter, err := node.service.ListConversationMessages(ctx, conversation.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(messagesAfter) != len(messagesBefore) {
		t.Fatalf("messages after restore = %d, want %d", len(messagesAfter), len(messagesBefore))
	}
}
