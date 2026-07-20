package steward

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSanitizeObservationSecretsPreservesActivityAndRedactsFields(t *testing.T) {
	input := CreateObservationInput{
		DataLevel:  DataD3,
		Summary:    "Browser settings api_key=sk-thisisasecretcredentialvalue123456",
		ContextKey: "chrome|settings",
		Payload: map[string]any{
			"application": "chrome",
			"password":    "correct-horse-battery-staple",
			"nested": map[string]any{
				"url":            "https://example.test/settings",
				"access_token":   "never-persist-this-token",
				"document_title": "Release 8f41cbe9b90a4aa7b704f2be52e8d66a",
			},
		},
		Metadata: map[string]any{"capture_interval_seconds": 10},
	}
	sanitized, redaction := SanitizeObservationSecrets(input)
	if !redaction.Redacted || redaction.Count < 3 {
		t.Fatalf("unexpected redaction result: %#v", redaction)
	}
	if strings.Contains(sanitized.Summary, "sk-this") || !strings.Contains(sanitized.Summary, "[REDACTED:") {
		t.Fatalf("summary was not redacted: %q", sanitized.Summary)
	}
	if sanitized.Payload["application"] != "chrome" || sanitized.Payload["password"] != redactedCredentialValue {
		t.Fatalf("unrelated fields were lost or credential remained: %#v", sanitized.Payload)
	}
	nested := sanitized.Payload["nested"].(map[string]any)
	if nested["url"] != "https://example.test/settings" || nested["access_token"] != redactedCredentialValue {
		t.Fatalf("nested field redaction failed: %#v", nested)
	}
	if nested["document_title"] != "Release 8f41cbe9b90a4aa7b704f2be52e8d66a" {
		t.Fatalf("high-entropy activity title was altered: %#v", nested["document_title"])
	}
	if sanitized.Metadata["secret_redacted"] != true {
		t.Fatalf("missing redaction metadata: %#v", sanitized.Metadata)
	}
	if err := ValidateObservationBeforePersistence(sanitized); err != nil {
		t.Fatalf("sanitized observation rejected: %v", err)
	}
}

func TestValidateObservationBeforePersistenceAllowsOrdinaryMetadata(t *testing.T) {
	input := CreateObservationInput{
		DataLevel: DataD2,
		Summary:   "Visual Studio Code project activity",
		Payload:   map[string]any{"application": "code", "duration_seconds": 42},
	}
	if err := ValidateObservationBeforePersistence(input); err != nil {
		t.Fatalf("ordinary observation rejected: %v", err)
	}
}

func TestValidateObservationBeforePersistenceRejectsResidualCredentialPlaintext(t *testing.T) {
	input := CreateObservationInput{
		DataLevel: DataD2, Summary: "Terminal authorization: Bearer still-plain-secret-token",
		Payload: map[string]any{"application": "terminal"},
	}
	if err := ValidateObservationBeforePersistence(input); err == nil {
		t.Fatal("residual credential plaintext must never reach persistence")
	}
	level, category := ClassifyObservationDataLevel(input)
	if level != DataD5 || category != "authorization_header" {
		t.Fatalf("explicit credential classification level=%q category=%q", level, category)
	}
}

func TestLegacyDataLevelAndHighEntropyTitleDoNotBlockCollection(t *testing.T) {
	input := CreateObservationInput{
		DataLevel: DataD5,
		Summary:   "Release 2f184c68d13c442fbfce934732a2987490273c8199f2e98b",
		Payload:   map[string]any{"window_title": "build-aB91_D3eF72c998e6a1ffab8c12d0"},
	}
	if err := ValidateObservationBeforePersistence(input); err != nil {
		t.Fatalf("legacy D5 label or high-entropy title rejected: %v", err)
	}
	level, category := ClassifyObservationDataLevel(CreateObservationInput{DataLevel: DataD2, Summary: input.Summary, Payload: input.Payload})
	if level != DataD2 || category != "" {
		t.Fatalf("high-entropy activity was promoted: level=%q category=%q", level, category)
	}
}

func TestSanitizeObservationSecretsRedactsTextBlobWithoutDroppingEnvelope(t *testing.T) {
	input := CreateObservationInput{
		Source: "companion:clipboard", Type: "clipboard_text", Summary: "clipboard changed",
		Blob: &ObservationBlobInput{MIMEType: "text/plain; charset=utf-8", DataBase64: base64.StdEncoding.EncodeToString([]byte("password=do-not-persist-this"))},
	}
	sanitized, redaction := SanitizeObservationSecrets(input)
	if !redaction.Redacted || sanitized.Blob == nil {
		t.Fatalf("text blob redaction=%#v input=%#v", redaction, sanitized)
	}
	decoded, err := base64.StdEncoding.DecodeString(sanitized.Blob.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(decoded), "do-not-persist") || !strings.Contains(string(decoded), "[REDACTED:") {
		t.Fatalf("text blob credential remained: %q", decoded)
	}
}

func TestCreateObservationPersistsLegacyD5ActivityAfterCredentialRedaction(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	secret := "credential-that-must-not-be-stored-" + uuid.NewString()
	input := CreateObservationInput{
		Source: "companion:security-regression", Type: "foreground_window", DataLevel: DataD5,
		Summary:    "Settings password=" + secret,
		ContextKey: "settings|credentials",
		Payload: map[string]any{
			"application": "settings", "password": secret,
			"window_title": "Release 51f32854f6f44449a5f46d518c5f0d37",
		},
		OccurredAt: &now,
	}
	observation, err := service.CreateObservation(ctx, input)
	if err != nil {
		t.Fatalf("legacy D5 activity was rejected: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_observations where id=$1 and occurred_at=$2`, observation.ID, observation.OccurredAt)
	})
	if observation.DataLevel != DataD5 || !strings.Contains(observation.Summary, "[REDACTED:") {
		t.Fatalf("unexpected stored envelope: %#v", observation)
	}
	var payloadText, metadataText string
	if err := db.Pool.QueryRow(ctx, `select payload::text,metadata::text from steward_observations where id=$1 and occurred_at=$2`, observation.ID, observation.OccurredAt).Scan(&payloadText, &metadataText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payloadText, secret) || strings.Contains(metadataText, secret) {
		t.Fatalf("credential plaintext persisted: payload=%s metadata=%s", payloadText, metadataText)
	}
	if !strings.Contains(payloadText, "settings") || !strings.Contains(payloadText, "51f32854f6f44449a5f46d518c5f0d37") {
		t.Fatalf("unrelated activity evidence was lost: %s", payloadText)
	}
	if !strings.Contains(metadataText, "secret_redacted") {
		t.Fatalf("redaction provenance missing: %s", metadataText)
	}
}

func TestCalculateStewardValueAndGrouping(t *testing.T) {
	value := CalculateStewardValue(StewardValueSignals{
		UserUse: 1, Actionability: 1, Recurrence: 1, Uniqueness: 1,
		Confidence: 1, CrossSource: 1, Recency: 1,
	})
	if value != 1 {
		t.Fatalf("expected capped value 1, got %v", value)
	}
	start := time.Now().UTC().Add(-time.Hour)
	items := []pendingObservation{
		{ID: "1", Source: "watcher", Type: "window", DeviceID: "local", ContextKey: "code", OccurredAt: start},
		{ID: "2", Source: "watcher", Type: "window", DeviceID: "local", ContextKey: "code", OccurredAt: start.Add(time.Minute)},
		{ID: "3", Source: "watcher", Type: "window", DeviceID: "local", ContextKey: "browser", OccurredAt: start.Add(2 * time.Minute)},
	}
	groups := groupPendingObservations(items)
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 1 {
		t.Fatalf("unexpected heartbeat grouping: %#v", groups)
	}
}

func TestSessionizerCombinesCrossSourceOverlapAndSeparatesAFK(t *testing.T) {
	start := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	windowEnd := start.Add(time.Minute)
	webEnd := start.Add(45 * time.Second)
	afkEnd := start.Add(90 * time.Second)
	items := []pendingObservation{
		{ID: "window", Source: "companion:windows-activity", Type: "foreground_window", DeviceID: "local", InteractiveSessionID: "windows-1", ContextKey: "chrome|docs", OccurredAt: start, EndedAt: &windowEnd},
		{ID: "web", Source: "adapter:activitywatch", Type: "currentwebtab", DeviceID: "local", InteractiveSessionID: "windows-1", ContextKey: "chrome|example.com", OccurredAt: start.Add(5 * time.Second), EndedAt: &webEnd},
		{ID: "afk", Source: "companion:windows-activity", Type: "afk_status", DeviceID: "local", InteractiveSessionID: "windows-1", ContextKey: "afk", OccurredAt: start.Add(30 * time.Second), EndedAt: &afkEnd},
	}
	groups := groupPendingObservations(items)
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 1 {
		t.Fatalf("cross-source grouping=%#v", groups)
	}
	afks := buildActivityAFKIndex(items)
	active, afk := activityDurations(groups[0], afks)
	if active != 30 || afk != 0 {
		t.Fatalf("overlapping evidence or AFK was double counted: active=%v afk=%v", active, afk)
	}
	active, afk = activityDurations(groups[1], afks)
	if active != 0 || afk != 60 {
		t.Fatalf("AFK duration mismatch: active=%v afk=%v", active, afk)
	}
}

func TestCOL012SubtractsAFKOnlyWithinMatchingDeviceAndSession(t *testing.T) {
	start := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	foregroundEnd := start.Add(2 * time.Minute)
	overlapEnd := start.Add(90 * time.Second)
	fullAFKEnd := start.Add(3 * time.Minute)
	items := []pendingObservation{
		{ID: "foreground", Type: "foreground_window", DeviceID: "device-a", InteractiveSessionID: "session-1", OccurredAt: start, EndedAt: &foregroundEnd},
		{ID: "matching-afk", Type: "afk_status", ContextKey: "afk", DeviceID: "device-a", InteractiveSessionID: "session-1", OccurredAt: start.Add(30 * time.Second), EndedAt: &overlapEnd},
		{ID: "other-session-afk", Type: "afk_status", ContextKey: "afk", DeviceID: "device-a", InteractiveSessionID: "session-2", OccurredAt: start.Add(-time.Minute), EndedAt: &fullAFKEnd},
		{ID: "other-device-afk", Type: "afk_status", ContextKey: "afk", DeviceID: "device-b", InteractiveSessionID: "session-1", OccurredAt: start.Add(-time.Minute), EndedAt: &fullAFKEnd},
	}
	afks := buildActivityAFKIndex(items)
	active, afk := activityDurations(items[:1], afks)
	if active != 60 || afk != 0 {
		t.Fatalf("matching AFK must subtract exactly once without cross-scope subtraction: active=%v afk=%v", active, afk)
	}
	if itemActive := observationActiveSeconds(items[0], afks); itemActive != 60 {
		t.Fatalf("session item active time must match AFK subtraction, got %v", itemActive)
	}
}

func TestActivityDurationsMergeAFKIntervalsAndClampAtZero(t *testing.T) {
	start := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	foregroundEnd := start.Add(time.Minute)
	firstAFKEnd := start.Add(45 * time.Second)
	secondAFKEnd := start.Add(2 * time.Minute)
	items := []pendingObservation{
		{ID: "foreground", Type: "foreground_window", DeviceID: "device-a", InteractiveSessionID: "session-1", OccurredAt: start, EndedAt: &foregroundEnd},
		{ID: "afk-1", Type: "afk_status", ContextKey: "afk", DeviceID: "device-a", InteractiveSessionID: "session-1", OccurredAt: start.Add(-time.Minute), EndedAt: &firstAFKEnd},
		{ID: "afk-2", Type: "afk_status", ContextKey: "AFK", DeviceID: "device-a", InteractiveSessionID: "session-1", OccurredAt: start.Add(15 * time.Second), EndedAt: &secondAFKEnd},
	}
	afks := buildActivityAFKIndex(items)
	active, _ := activityDurations(items[:1], afks)
	if active != 0 {
		t.Fatalf("overlapping AFK intervals covering foreground must clamp active time to zero, got %v", active)
	}
	if itemActive := observationActiveSeconds(items[0], afks); itemActive != 0 {
		t.Fatalf("session item active time must not become negative, got %v", itemActive)
	}
}

func TestCompanionCollectionSourceStateTracksSessionRevisionAndBacklog(t *testing.T) {
	start := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Second)
	ingested := end.Add(2 * time.Second)
	state, ok := companionCollectionSourceState(CreateObservationInput{
		Source: "companion:windows-activity", Type: "foreground_window",
		SourceEventKey: "windows:foreground:1", SourceRevision: 7,
		InteractiveSessionID: "1", Metadata: map[string]any{
			"companion_outbox_backlog": 3.0, "capture_interval_seconds": 10.0,
		},
	}, start, &end, ingested)
	if !ok {
		t.Fatal("expected Companion source state")
	}
	if state.Collector != "companion:windows-activity" || state.SourceKey != "1:foreground_window" || state.InteractiveSessionID != "1" {
		t.Fatalf("unexpected source identity: %#v", state)
	}
	if state.BacklogCount != 3 || state.MaxExpectedLagSeconds != 60 || state.LastSourceEventAt == nil || !state.LastSourceEventAt.Equal(end) {
		t.Fatalf("unexpected freshness state: %#v", state)
	}
	if revision, ok := state.Cursor["source_revision"].(int64); !ok || revision != 7 {
		t.Fatalf("unexpected cursor: %#v", state.Cursor)
	}
	if _, ok := companionCollectionSourceState(CreateObservationInput{Source: "adapter:activitywatch"}, start, nil, ingested); ok {
		t.Fatal("non-Companion observation must not be projected as a Companion source")
	}
}

func TestActivityBatchRetryDelayUsesBoundedExponentialBackoff(t *testing.T) {
	if got := activityBatchRetryDelay(1); got != 15*time.Second {
		t.Fatalf("first retry delay=%s", got)
	}
	if got := activityBatchRetryDelay(4); got != 2*time.Minute {
		t.Fatalf("fourth retry delay=%s", got)
	}
	if got := activityBatchRetryDelay(100); got != time.Hour {
		t.Fatalf("retry delay must be capped at one hour, got %s", got)
	}
}

func TestLocalSummaryEmbeddingIsStableAndSized(t *testing.T) {
	left := localSummaryEmbedding("Personal steward project")
	right := localSummaryEmbedding("Personal steward project")
	if left != right {
		t.Fatal("local summary embedding must be deterministic")
	}
	if dimensions := len(strings.Split(strings.Trim(left, "[]"), ",")); dimensions != stewardEmbeddingDimensions {
		t.Fatalf("expected %d dimensions, got %d", stewardEmbeddingDimensions, dimensions)
	}
}

func TestRedactPresidioFindings(t *testing.T) {
	redacted := redactPresidioFindings("Email alice@example.com today", []presidioFinding{{Start: 6, End: 23, EntityType: "EMAIL_ADDRESS", Score: 0.99}})
	if redacted != "Email [REDACTED:EMAIL_ADDRESS] today" {
		t.Fatalf("unexpected redaction: %q", redacted)
	}
}
