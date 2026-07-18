package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mongojson/backend/internal/service/steward"
)

func TestStewardNotificationOutboxDeduplicatesDeliversAndAcknowledges(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed notification delivery test")
	}
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "notification-test")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "notifications"), "notification-node")

	var received atomic.Int32
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode ntfy body: %v", err)
		}
		if body["title"] != "数据库备份完成" || body["message"] != "备份文件已经校验。" {
			t.Errorf("unexpected ntfy payload: %+v", body)
		}
		received.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ntfy-message-1"}`))
	}))
	defer ntfy.Close()

	enabled := true
	endpoint, err := node.service.UpsertNotificationEndpoint(ctx, steward.UpdateNotificationEndpointInput{
		Channel: "ntfy", Name: "ntfy-test", Enabled: &enabled,
		Config: map[string]any{"url": ntfy.URL, "topic": "steward-test"},
	})
	if err != nil || endpoint.Channel != "ntfy" {
		t.Fatalf("configure ntfy endpoint: endpoint=%+v err=%v", endpoint, err)
	}
	now := time.Now().UTC().Add(-time.Second)
	created, err := node.service.CreateNotification(ctx, steward.CreateNotificationInput{
		Title: "数据库备份完成", Body: "备份文件已经校验。", Priority: "high", ScheduledAt: &now,
		DedupeKey: "notification-e2e", Channels: []string{"ntfy"},
	})
	if err != nil {
		t.Fatalf("create notification: %v", err)
	}
	replayed, err := node.service.CreateNotification(ctx, steward.CreateNotificationInput{
		Title: "数据库备份完成", Body: "备份文件已经校验。", Priority: "high", ScheduledAt: &now,
		DedupeKey: "notification-e2e", Channels: []string{"ntfy"},
	})
	if err != nil || replayed.ID != created.ID {
		t.Fatalf("dedupe replay=%+v err=%v want id=%s", replayed, err, created.ID)
	}
	processed, err := node.service.RunNotificationDeliveryCycle(ctx, 10)
	if err != nil || processed != 1 || received.Load() != 1 {
		t.Fatalf("delivery processed=%d received=%d err=%v", processed, received.Load(), err)
	}
	delivered, err := node.service.GetNotification(ctx, created.ID)
	if err != nil || delivered.Status != "sent" || len(delivered.Deliveries) != 1 || delivered.Deliveries[0].Status != "accepted" {
		t.Fatalf("delivered notification=%+v err=%v", delivered, err)
	}
	acknowledged, err := node.service.DecideNotification(ctx, created.ID, steward.NotificationDecisionInput{Decision: "acknowledge", DeviceID: "test-device"})
	if err != nil || acknowledged.Status != "acknowledged" || acknowledged.AcknowledgedAt == nil {
		t.Fatalf("acknowledged notification=%+v err=%v", acknowledged, err)
	}
}
