package steward

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateObservationBeforePersistenceBlocksD5(t *testing.T) {
	tests := []CreateObservationInput{
		{DataLevel: DataD5, Summary: "declared credential"},
		{DataLevel: DataD3, Summary: "api_key=sk-thisisasecretcredentialvalue123456"},
		{DataLevel: DataD3, Payload: map[string]any{"password": "correct-horse-battery-staple"}},
		{DataLevel: DataD3, Summary: "-----BEGIN PRIVATE KEY-----"},
	}
	for _, input := range tests {
		if err := ValidateObservationBeforePersistence(input); !errors.Is(err, ErrCredentialDataBlocked) {
			t.Fatalf("expected D5 block for %#v, got %v", input, err)
		}
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
