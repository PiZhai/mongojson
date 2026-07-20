package stewardcompanion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mongojson/backend/internal/service/steward"
)

func TestBufferEncryptsRedactsCredentialsAndFlushes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "companion.db")
	buffer, err := Open(ctx, Options{Path: path, Key: bytes.Repeat([]byte{7}, 32), MaxPending: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()

	secret := steward.CreateObservationInput{
		Source: "test", Type: "window", DataLevel: steward.DataD3,
		Summary: "settings password=never-store-this-value", Payload: map[string]any{"application": "code", "password": "never-store-this-value"},
	}
	if _, err := buffer.Enqueue(ctx, secret); err != nil {
		t.Fatalf("credential-bearing activity envelope must be preserved after redaction: %v", err)
	}
	input := steward.CreateObservationInput{
		Source: "test", Type: "window", DataLevel: steward.DataD2,
		Summary: "plaintext-must-not-appear-in-sqlite", Payload: map[string]any{"application": "code"},
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Enqueue(ctx, input); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected bounded queue, got %v", err)
	}
	for _, candidate := range []string{path, path + "-wal"} {
		content, err := os.ReadFile(candidate)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(input.Summary)) {
			t.Fatalf("plaintext observation leaked into %s", candidate)
		}
		if bytes.Contains(content, []byte("never-store-this-value")) {
			t.Fatalf("credential plaintext leaked into %s", candidate)
		}
	}

	received := 0
	receivedRedactedSecret := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got steward.CreateObservationInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode flushed payload: %v", err)
		}
		if strings.Contains(got.Summary, "never-store-this-value") {
			t.Errorf("credential plaintext survived encrypted outbox: %q", got.Summary)
		}
		if strings.Contains(got.Summary, "[REDACTED:") {
			receivedRedactedSecret = true
		} else if got.Summary != input.Summary {
			t.Errorf("unexpected decrypted payload: %q", got.Summary)
		}
		received++
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 3 || result.Pending != 0 || received != 3 || !receivedRedactedSecret {
		t.Fatalf("unexpected flush result: %#v received=%d", result, received)
	}
}

func TestBufferCoalescesSourceEventRevisionsAndDoesNotDeleteNewerRevision(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{9}, 32), MaxPending: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	start := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Second)
	input := steward.CreateObservationInput{
		Source: "companion:test", Type: "foreground_window", Summary: "Code",
		SourceEventKey: "windows:foreground:1:42", SourceRevision: 1,
		OccurredAt: &start, EndedAt: &end,
	}
	firstID, err := buffer.Enqueue(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatalf("same event revision must be idempotent: %v", err)
	}
	if pending, _ := buffer.Pending(ctx); pending != 1 {
		t.Fatalf("pending=%d, want 1", pending)
	}

	requestStarted := make(chan struct{})
	allowResponse := make(chan struct{})
	var requestOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blocked := false
		requestOnce.Do(func() {
			close(requestStarted)
			blocked = true
		})
		if blocked {
			<-allowResponse
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	flushDone := make(chan error, 1)
	go func() {
		_, flushErr := buffer.Flush(ctx, server.URL, server.Client(), 10)
		flushDone <- flushErr
	}()
	<-requestStarted
	newEnd := end.Add(10 * time.Second)
	input.SourceRevision = 2
	input.EndedAt = &newEnd
	secondID, err := buffer.Enqueue(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if firstID != secondID {
		t.Fatalf("revision changed row identity: %q != %q", firstID, secondID)
	}
	close(allowResponse)
	if err := <-flushDone; err != nil {
		t.Fatal(err)
	}
	if pending, _ := buffer.Pending(ctx); pending != 1 {
		t.Fatalf("newer in-flight revision was deleted; pending=%d", pending)
	}
	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 1 || result.Pending != 0 {
		t.Fatalf("newer revision flush=%#v", result)
	}
}

func TestBufferDataLevelConfigurationIsCompatibilityMetadataNotCollectionGate(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{
		Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{8}, 32),
		AllowedDataLevels: []string{steward.DataD0},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()

	input := steward.CreateObservationInput{
		Source: "companion:test", Type: "foreground_window", DataLevel: steward.DataD5,
		Summary: "Release 9d0f993d3d5844dba90f1d20b43fd52f",
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatalf("legacy data-level selection rejected activity: %v", err)
	}
	if got := buffer.AllowedDataLevels(); len(got) != 1 || got[0] != steward.DataD0 {
		t.Fatalf("allowed levels = %#v", got)
	}
}

func TestBufferDefaultCompatibilityLevelsIncludeAllHistoricalLabels(t *testing.T) {
	buffer, err := Open(context.Background(), Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{4}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	got := buffer.AllowedDataLevels()
	want := []string{steward.DataD0, steward.DataD1, steward.DataD2, steward.DataD3, steward.DataD4, steward.DataD5, steward.DataD6}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("default compatibility levels=%#v want=%#v", got, want)
	}
}

func TestBufferRoutesNotificationFeedbackEnvelope(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{6}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	feedback := NotificationFeedbackEnvelope{CallbackToken: "signed-token", Action: "snoozed", OccurredAt: time.Now().UTC()}
	if _, err := buffer.EnqueueEnvelope(ctx, EnvelopeNotificationFeedback, feedback.EventKey(), 1, feedback); err != nil {
		t.Fatal(err)
	}
	var received NotificationFeedbackEnvelope
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/steward/notifications/feedback/callback" {
			t.Errorf("unexpected feedback endpoint %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode feedback: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 1 || received.Action != "snoozed" || received.CallbackToken != "signed-token" {
		t.Fatalf("feedback flush result=%#v received=%#v", result, received)
	}
}

func TestBufferFlushUsesManagementBearerTokenForEveryEnvelopeKind(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{4}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()

	observation := steward.CreateObservationInput{
		Source: "companion:windows-activity", Type: "foreground_window", Summary: "Visual Studio Code",
		SourceEventKey: "authenticated-observation", SourceRevision: 1,
	}
	if _, err := buffer.Enqueue(ctx, observation); err != nil {
		t.Fatal(err)
	}
	feedback := NotificationFeedbackEnvelope{
		CallbackToken: "signed-callback", Action: "opened", OccurredAt: time.Now().UTC(),
	}
	if _, err := buffer.EnqueueEnvelope(ctx, EnvelopeNotificationFeedback, feedback.EventKey(), 1, feedback); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	receivedPaths := make([]string, 0, 2)
	unauthorized := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer companion-management-secret" {
			mu.Lock()
			unauthorized++
			mu.Unlock()
			http.Error(w, "missing management bearer token", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		receivedPaths = append(receivedPaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewManagementHTTPClient(" companion-management-secret ", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := buffer.Flush(ctx, server.URL, client, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 2 || result.Failed != 0 || result.Pending != 0 {
		t.Fatalf("authenticated flush=%#v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if unauthorized != 0 {
		t.Fatalf("server rejected %d unauthenticated requests", unauthorized)
	}
	wantPaths := map[string]bool{
		"/api/steward/activity/observations":           false,
		"/api/steward/notifications/feedback/callback": false,
	}
	for _, path := range receivedPaths {
		if _, ok := wantPaths[path]; !ok {
			t.Fatalf("unexpected authenticated endpoint %q", path)
		}
		wantPaths[path] = true
	}
	for path, seen := range wantPaths {
		if !seen {
			t.Errorf("authenticated endpoint %q was not flushed", path)
		}
	}
}

func TestBufferFlushRetainsUnauthorizedEnvelopeAndRetriesWithBearerToken(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{3}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	if _, err := buffer.Enqueue(ctx, steward.CreateObservationInput{
		Source: "companion:windows-activity", Type: "foreground_window",
		SourceEventKey: "retry-after-auth", SourceRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer restored-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 0 || result.Failed != 1 || result.Pending != 1 {
		t.Fatalf("unauthenticated flush=%#v", result)
	}
	if !strings.Contains(result.LastError, "HTTP 401") {
		t.Fatalf("unauthenticated flush last error=%q", result.LastError)
	}
	var attempts int
	var lastError string
	if err := buffer.db.QueryRowContext(ctx, `select attempts,last_error from pending_envelopes where event_key=?`, "retry-after-auth").Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !strings.Contains(lastError, "HTTP 401") {
		t.Fatalf("attempts=%d last_error=%q", attempts, lastError)
	}

	authorizedClient, err := NewManagementHTTPClient("restored-secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err = buffer.Flush(ctx, server.URL, authorizedClient, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 1 || result.Failed != 0 || result.Pending != 0 {
		t.Fatalf("authorized retry=%#v", result)
	}
}

func TestBufferFlushReportsRemainingObservationBacklog(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{5}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	for index := 1; index <= 2; index++ {
		input := steward.CreateObservationInput{
			Source: "companion:windows-activity", Type: "foreground_window",
			SourceEventKey: fmt.Sprintf("event-%d", index), SourceRevision: 1,
		}
		if _, err := buffer.Enqueue(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	backlogs := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got steward.CreateObservationInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode observation: %v", err)
		}
		backlogs = append(backlogs, int(got.Metadata["companion_outbox_backlog"].(float64)))
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 2 || len(backlogs) != 2 || backlogs[0] != 1 || backlogs[1] != 0 {
		t.Fatalf("flush=%#v backlogs=%#v", result, backlogs)
	}
}

func TestBufferPersistsAuthenticatedCaptureControlEncryptedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "companion.db")
	key := bytes.Repeat([]byte{11}, 32)
	binding, err := CaptureControlCacheBinding("http://127.0.0.1:18080/api", "management-secret")
	if err != nil {
		t.Fatal(err)
	}
	authenticatedAt := time.Date(2026, 7, 20, 9, 10, 11, 0, time.UTC)
	want := CaptureControl{
		CaptureEnabled: true, FlushEnabled: true, Interval: 17 * time.Second,
		Timezone: "Asia/Shanghai", Revision: 42,
	}
	buffer, err := Open(ctx, Options{Path: path, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.SaveAuthenticatedCaptureControl(ctx, binding, want, authenticatedAt); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{path, path + "-wal"} {
		content, readErr := os.ReadFile(candidate)
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		for _, plaintext := range []string{"Asia/Shanghai", "management-secret", "capture_enabled"} {
			if bytes.Contains(content, []byte(plaintext)) {
				t.Fatalf("authenticated control plaintext %q leaked into %s", plaintext, candidate)
			}
		}
	}

	reopened, err := Open(ctx, Options{Path: path, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.LoadAuthenticatedCaptureControl(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if got.Control != want || !got.AuthenticatedAt.Equal(authenticatedAt) {
		t.Fatalf("cached control=%#v, want control=%#v authenticated_at=%s", got, want, authenticatedAt)
	}
}

func TestBufferCaptureControlCacheIsCredentialBoundAndPreservesDisabledState(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{12}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	currentBinding, err := CaptureControlCacheBinding("http://127.0.0.1:18080/api", "current-token")
	if err != nil {
		t.Fatal(err)
	}
	otherBinding, err := CaptureControlCacheBinding("http://127.0.0.1:18080/api", "rotated-token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	enabled := CaptureControl{CaptureEnabled: true, FlushEnabled: true, Interval: 10 * time.Second, Revision: 8}
	if err := buffer.SaveAuthenticatedCaptureControl(ctx, currentBinding, enabled, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	disabled := CaptureControl{CaptureEnabled: false, FlushEnabled: false, Interval: 10 * time.Second, Revision: 9}
	if err := buffer.SaveAuthenticatedCaptureControl(ctx, currentBinding, disabled, now); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.LoadAuthenticatedCaptureControl(ctx, otherBinding); !errors.Is(err, ErrNoCachedCaptureControl) {
		t.Fatalf("credential-rotated cache error=%v", err)
	}
	got, err := buffer.LoadAuthenticatedCaptureControl(ctx, currentBinding)
	if err != nil {
		t.Fatal(err)
	}
	if got.Control.CaptureEnabled || got.Control.FlushEnabled {
		t.Fatalf("cached pause/disable was not preserved: %#v", got.Control)
	}
}
