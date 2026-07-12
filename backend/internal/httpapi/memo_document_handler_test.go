package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/memo"
)

func TestSaveMemoDocumentPassesStructuredContent(t *testing.T) {
	var capturedID string
	var captured memo.DocumentSaveInput
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MemoService: fakeMemoStore{
		saveDocument: func(_ context.Context, id string, input memo.DocumentSaveInput) (domain.MemoRecord, error) {
			capturedID, captured = id, input
			return domain.MemoRecord{ID: id, ContentJSON: input.ContentJSON, Revision: input.Revision + 1}, nil
		},
	}})

	body := `{"title":"随手记","content_json":[{"id":"block-1","type":"paragraph","content":[]}],"content_markdown":"hello","content_html":"<p>hello</p>","schema_version":1,"revision":4,"editor_type":"blocknote"}`
	req := httptest.NewRequest(http.MethodPut, "/api/memo/documents/memo-1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if capturedID != "memo-1" || captured.Revision != 4 || captured.SchemaVersion != 1 {
		t.Fatalf("unexpected save input: id=%q input=%#v", capturedID, captured)
	}
	if !json.Valid(captured.ContentJSON) {
		t.Fatalf("expected structured JSON, got %s", captured.ContentJSON)
	}
}

func TestSaveMemoDocumentReturnsConflict(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MemoService: fakeMemoStore{
		saveDocument: func(context.Context, string, memo.DocumentSaveInput) (domain.MemoRecord, error) {
			return domain.MemoRecord{}, memo.ErrRevisionConflict
		},
	}})
	req := httptest.NewRequest(http.MethodPut, "/api/memo/documents/memo-1", strings.NewReader(`{"content_json":[],"revision":1}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateMemoSideNote(t *testing.T) {
	var captured memo.SideNoteInput
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MemoService: fakeMemoStore{
		createSideNote: func(_ context.Context, documentID string, input memo.SideNoteInput) (domain.MemoSideNoteRecord, error) {
			captured = input
			return domain.MemoSideNoteRecord{ID: "note-1", DocumentID: documentID, BodyJSON: input.BodyJSON, Revision: 1}, nil
		},
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/memo/documents/memo-1/notes", strings.NewReader(`{"anchor_block_id":"block-1","body_json":{"text":"note"},"color":"#fff7d6"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.AnchorBlockID == nil || *captured.AnchorBlockID != "block-1" {
		t.Fatalf("expected block anchor, got %#v", captured.AnchorBlockID)
	}
}
