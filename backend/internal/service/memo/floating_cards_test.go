package memo

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeFloatingCardsJSON(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 11, 12, 0, time.UTC)
	raw := json.RawMessage(`[
		{"id":"card-1","content":"first","color":"#EAF6FF","created_at":"2026-06-28T01:02:03Z","updated_at":"2026-06-28T04:05:06Z"},
		{"content":"second","color":"not-a-color"}
	]`)

	cards, normalized, err := NormalizeFloatingCardsJSON(raw, now)
	if err != nil {
		t.Fatalf("expected cards to normalize, got %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}
	if cards[0].ID != "card-1" || cards[0].Color != "#eaf6ff" || cards[0].Content != "first" {
		t.Fatalf("unexpected first card: %#v", cards[0])
	}
	if cards[1].ID == "" {
		t.Fatalf("expected missing id to be generated")
	}
	if cards[1].Color != defaultFloatingCardColor {
		t.Fatalf("expected invalid color to normalize to default, got %s", cards[1].Color)
	}
	if !cards[1].CreatedAt.Equal(now) || !cards[1].UpdatedAt.Equal(now) {
		t.Fatalf("expected missing timestamps to use fallback, got %#v", cards[1])
	}
	if !json.Valid(normalized) {
		t.Fatalf("expected normalized JSON, got %s", normalized)
	}
}

func TestNormalizeFloatingCardsJSONRejectsNonArray(t *testing.T) {
	if _, _, err := NormalizeFloatingCardsJSON(json.RawMessage(`{"id":"card-1"}`), time.Now()); err == nil {
		t.Fatalf("expected non-array payload to fail")
	}
}
