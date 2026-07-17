package memo

import (
	"encoding/json"
	"testing"
)

func TestNormalizeDocumentJSONCollectsNestedBlockIDs(t *testing.T) {
	raw := json.RawMessage(`[
		{"id":"heading-1","type":"heading","content":[]},
		{"id":"list-1","type":"bulletListItem","children":[{"id":"child-1","type":"paragraph","content":[]}]}
	]`)

	normalized, ids, err := normalizeDocumentJSON(raw)
	if err != nil {
		t.Fatalf("expected valid BlockNote document, got %v", err)
	}
	if !json.Valid(normalized) {
		t.Fatalf("expected normalized JSON, got %s", normalized)
	}
	if len(ids) != 3 || ids[0] != "heading-1" || ids[1] != "list-1" || ids[2] != "child-1" {
		t.Fatalf("unexpected block ids: %#v", ids)
	}
}

func TestNormalizeDocumentJSONRejectsObjectRoot(t *testing.T) {
	if _, _, err := normalizeDocumentJSON(json.RawMessage(`{"id":"block-1"}`)); err == nil {
		t.Fatal("expected non-array document to fail")
	}
}

func TestNormalizeSideNoteInput(t *testing.T) {
	anchor := " block-1 "
	input, err := normalizeSideNoteInput(SideNoteInput{
		AnchorBlockID: &anchor,
		BodyJSON:      json.RawMessage(`{"text":"remember"}`),
		Color:         "#EAF6FF",
		SortOrder:     -1,
	})
	if err != nil {
		t.Fatalf("expected valid side note, got %v", err)
	}
	if input.AnchorBlockID == nil || *input.AnchorBlockID != "block-1" {
		t.Fatalf("expected trimmed anchor, got %#v", input.AnchorBlockID)
	}
	if input.Color != "#eaf6ff" || input.SortOrder != 0 || input.Status != "active" {
		t.Fatalf("unexpected normalized side note: %#v", input)
	}
}
