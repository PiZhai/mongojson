package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
)

func TestReadyzUsesReadinessChecker(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{
				"database": "ok",
				"storage":  "ok",
				"worker":   "ok",
			}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("expected ready response, got %s", rec.Body.String())
	}
}

func TestReadyzReturnsServiceUnavailableWhenCheckFails(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{"database": "error"}, context.DeadlineExceeded
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("expected not_ready response, got %s", rec.Body.String())
	}
}

func TestManagementAndPeerRoutersExposeDisjointStewardSurfaces(t *testing.T) {
	deps := Dependencies{
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{"steward": "ok"}, nil
		},
	}
	management := chi.NewRouter()
	RegisterManagementRoutes(management, deps)
	peer := chi.NewRouter()
	RegisterPeerRoutes(peer, PeerDependencies{Readiness: deps.Readiness})

	assertRouteStatus(t, management, http.MethodGet, "/api/steward/sync/changes", http.StatusMethodNotAllowed)
	assertRouteStatus(t, management, http.MethodPost, "/api/steward/sync/changes/import", http.StatusNotFound)
	assertRouteStatus(t, management, http.MethodPost, "/api/steward/pairing/challenge", http.StatusNotFound)

	assertRouteStatus(t, peer, http.MethodGet, "/api/steward/sync/status", http.StatusNotFound)
	assertRouteStatus(t, peer, http.MethodGet, "/api/steward/tasks", http.StatusNotFound)
	assertRouteStatus(t, peer, http.MethodPost, "/api/steward/devices/device-1/revoke", http.StatusNotFound)
	assertRouteStatus(t, peer, http.MethodGet, "/healthz", http.StatusOK)

	// A registered peer protocol route reaches the handler. With no service
	// dependency it fails closed as unavailable instead of disappearing as 404.
	assertRouteStatus(t, peer, http.MethodGet, "/api/steward/sync/changes", http.StatusServiceUnavailable)
	assertRouteStatus(t, peer, http.MethodGet, "/api/steward/sync/probe?entity_type=task&entity_id=task-1", http.StatusServiceUnavailable)
}

func TestPeerRouterRejectsOversizedBodiesBeforeProtocolHandling(t *testing.T) {
	peer := chi.NewRouter()
	RegisterPeerRoutes(peer, PeerDependencies{})
	req := httptest.NewRequest(http.MethodPost, "/api/steward/pairing/challenge", io.LimitReader(repeatingReader('x'), maxPeerRequestBodyBytes+1))
	req.ContentLength = maxPeerRequestBodyBytes + 1
	rec := httptest.NewRecorder()

	peer.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized peer request status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func assertRouteStatus(t *testing.T, handler http.Handler, method string, path string, want int) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
}

func TestCreateJobReturnsServiceUnavailableWhenProcessingIsDisabled(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		JobService: jobs.NewService(nil, nil, time.Hour),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/jobs", strings.NewReader(`{"tool_type":"visualize"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "asynchronous job processing is disabled") {
		t.Fatalf("expected disabled message, got %s", rec.Body.String())
	}
}

func TestUploadFileRejectsBodyOverHardLimit(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{})

	const boundary = "test-boundary"
	prefix := "--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="large.json"` + "\r\n" +
		"Content-Type: application/json\r\n\r\n"
	suffix := "\r\n--" + boundary + "--\r\n"
	body := io.MultiReader(
		strings.NewReader(prefix),
		io.LimitReader(repeatingReader('x'), maxUploadBytes+1),
		strings.NewReader(suffix),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/files", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upload body exceeds 64 MiB") {
		t.Fatalf("expected upload size error, got %s", rec.Body.String())
	}
}

func TestSaveMemoAcceptsFloatingCards(t *testing.T) {
	var captured memo.SaveInput
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		MemoService: fakeMemoStore{
			saveMemo: func(_ context.Context, input memo.SaveInput) (domain.MemoRecord, error) {
				captured = input
				return domain.MemoRecord{
					ID:            "memo-1",
					Slug:          input.Slug,
					Title:         input.Title,
					ContentHTML:   input.ContentHTML,
					ContentText:   input.ContentText,
					FloatingCards: []domain.MemoFloatingCard{},
					CreatedAt:     time.Now(),
					UpdatedAt:     time.Now(),
				}, nil
			},
		},
	})

	body := `{"slug":"inbox","title":"随手记","content_html":"<p>x</p>","content_text":"x","floating_cards":[{"id":"card-1","content":"note","color":"#fff7d6","created_at":"2026-06-28T01:02:03Z","updated_at":"2026-06-28T01:02:03Z"}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/memo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.FloatingCards == nil {
		t.Fatalf("expected floating_cards to be passed to service")
	}
	var cards []map[string]any
	if err := json.Unmarshal(*captured.FloatingCards, &cards); err != nil {
		t.Fatalf("expected captured cards JSON to decode: %v", err)
	}
	if got := cards[0]["id"]; got != "card-1" {
		t.Fatalf("expected card id card-1, got %v", got)
	}
}

func TestSaveMemoPreservesFloatingCardsWhenFieldIsOmitted(t *testing.T) {
	var captured memo.SaveInput
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		MemoService: fakeMemoStore{
			saveMemo: func(_ context.Context, input memo.SaveInput) (domain.MemoRecord, error) {
				captured = input
				return domain.MemoRecord{ID: "memo-1", Slug: input.Slug, Title: input.Title, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
			},
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/memo", strings.NewReader(`{"slug":"inbox","title":"随手记","content_html":"","content_text":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.FloatingCards != nil {
		t.Fatalf("expected omitted floating_cards to remain nil")
	}
}

func TestSaveMemoReturnsBadRequestForInvalidFloatingCards(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		MemoService: fakeMemoStore{
			saveMemo: func(context.Context, memo.SaveInput) (domain.MemoRecord, error) {
				return domain.MemoRecord{}, memo.ErrInvalidFloatingCards
			},
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/memo", strings.NewReader(`{"slug":"inbox","floating_cards":{}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type repeatingReader byte

func (r repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

type fakeMemoStore struct {
	getOrCreate func(context.Context, string) (domain.MemoRecord, error)
	saveMemo    func(context.Context, memo.SaveInput) (domain.MemoRecord, error)
}

func (s fakeMemoStore) GetOrCreate(ctx context.Context, slug string) (domain.MemoRecord, error) {
	if s.getOrCreate != nil {
		return s.getOrCreate(ctx, slug)
	}
	return domain.MemoRecord{ID: "memo-1", Slug: slug, FloatingCards: []domain.MemoFloatingCard{}}, nil
}

func (s fakeMemoStore) SaveMemo(ctx context.Context, input memo.SaveInput) (domain.MemoRecord, error) {
	if s.saveMemo != nil {
		return s.saveMemo(ctx, input)
	}
	return domain.MemoRecord{ID: "memo-1", Slug: input.Slug, FloatingCards: []domain.MemoFloatingCard{}}, nil
}
