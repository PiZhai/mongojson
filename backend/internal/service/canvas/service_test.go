package canvas

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeTitleUsesDefaultAndRuneLimit(t *testing.T) {
	if got := normalizeTitle("   "); got != "未命名画板" {
		t.Fatalf("unexpected default title: %q", got)
	}
	long := "画"
	for range 130 {
		long += "板"
	}
	if got := len([]rune(normalizeTitle(long))); got != 120 {
		t.Fatalf("expected 120 runes, got %d", got)
	}
}

func TestValidateSceneRequiresJSONObject(t *testing.T) {
	for _, scene := range []json.RawMessage{nil, json.RawMessage(`[]`), json.RawMessage(`not-json`)} {
		if err := validateScene(scene); !errors.Is(err, ErrInvalidScene) {
			t.Fatalf("expected invalid scene for %q, got %v", scene, err)
		}
	}
	if err := validateScene(json.RawMessage(`{"elements":[],"appState":{},"files":{}}`)); err != nil {
		t.Fatalf("expected valid scene, got %v", err)
	}
}
