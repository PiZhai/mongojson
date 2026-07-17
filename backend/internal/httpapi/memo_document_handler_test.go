package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/memosync"
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

func TestListMemoDocumentsReturnsArchiveSummaries(t *testing.T) {
	updatedAt := time.Date(2026, time.July, 14, 9, 30, 0, 0, time.UTC)
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MemoService: fakeMemoStore{
		listDocuments: func(context.Context) ([]domain.MemoDocumentSummary, error) {
			return []domain.MemoDocumentSummary{{
				ID: "memo-1", Slug: "inbox", Title: "随手记", Revision: 7,
				EditorType: "blocknote", NoteCount: 3, UpdatedAt: updatedAt,
			}}, nil
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/memo/documents", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Documents []domain.MemoDocumentSummary `json:"documents"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Documents) != 1 || response.Documents[0].Slug != "inbox" || response.Documents[0].NoteCount != 3 {
		t.Fatalf("unexpected documents: %#v", response.Documents)
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

func TestSaveMemoDocumentBroadcastsRevision(t *testing.T) {
	hub := memosync.NewHub()
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		MemoSync: hub,
		MemoService: fakeMemoStore{saveDocument: func(_ context.Context, id string, input memo.DocumentSaveInput) (domain.MemoRecord, error) {
			return domain.MemoRecord{ID: id, Revision: input.Revision + 1}, nil
		}},
	})
	server := httptest.NewServer(router)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/memo/documents/memo-1/ws", nil)
	if err != nil {
		t.Fatalf("dial memo sync: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ready memosync.Event
	if err := conn.ReadJSON(&ready); err != nil || ready.Type != memosync.EventReady {
		t.Fatalf("read ready event: %#v, %v", ready, err)
	}

	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/memo/documents/memo-1", strings.NewReader(`{"content_json":[],"revision":4}`))
	if err != nil {
		t.Fatalf("create save request: %v", err)
	}
	req.Header.Set("X-Memo-Client-ID", "client-1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("save memo document: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}

	var event memosync.Event
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read document update: %v", err)
	}
	if event.Type != memosync.EventDocumentUpdated || event.DocumentID != "memo-1" || event.Revision != 5 || event.ActorClientID != "client-1" {
		t.Fatalf("unexpected sync event: %#v", event)
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
