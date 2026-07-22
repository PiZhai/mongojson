package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/music"
)

func newHTTPAPITestRequest(method, target string, body io.Reader) *http.Request {
	if strings.HasPrefix(target, "/") {
		target = "http://127.0.0.1:18080" + target
	}
	return httptest.NewRequest(method, target, body)
}

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

	req := newHTTPAPITestRequest(http.MethodGet, "/readyz", nil)
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

	req := newHTTPAPITestRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("expected not_ready response, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "database") || strings.Contains(rec.Body.String(), "deadline") {
		t.Fatalf("anonymous readiness leaked internal failure detail: %s", rec.Body.String())
	}
}

func TestAuthenticatedReadinessDetailsRemainAvailable(t *testing.T) {
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{"database": "ok", "runtime": "ok"}, nil
		},
	})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/system/readiness", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"database":"ok"`) {
		t.Fatalf("detailed readiness status = %d: %s", rec.Code, rec.Body.String())
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

func TestPeerReadyzDoesNotExposeInternalFailureDetails(t *testing.T) {
	peer := chi.NewRouter()
	RegisterPeerRoutes(peer, PeerDependencies{
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{"steward_runtime": "secret provider failure detail"}, errors.New("secret provider failure detail")
		},
	})
	req := newHTTPAPITestRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	peer.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("peer readiness status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret provider") || strings.Contains(rec.Body.String(), "steward_runtime") {
		t.Fatalf("peer readiness leaked internal detail: %s", rec.Body.String())
	}
}

func TestPeerRouterRejectsOversizedBodiesBeforeProtocolHandling(t *testing.T) {
	peer := chi.NewRouter()
	RegisterPeerRoutes(peer, PeerDependencies{})
	req := newHTTPAPITestRequest(http.MethodPost, "/api/steward/pairing/challenge", io.LimitReader(repeatingReader('x'), maxPeerRequestBodyBytes+1))
	req.ContentLength = maxPeerRequestBodyBytes + 1
	rec := httptest.NewRecorder()

	peer.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized peer request status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func assertRouteStatus(t *testing.T, handler http.Handler, method string, path string, want int) {
	t.Helper()
	req := newHTTPAPITestRequest(method, path, nil)
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

	req := newHTTPAPITestRequest(http.MethodPost, "/api/jobs", strings.NewReader(`{"tool_type":"visualize"}`))
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

	req := newHTTPAPITestRequest(http.MethodPost, "/api/files", body)
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

func TestUploadMusicTrackAcceptsMetadata(t *testing.T) {
	var captured music.UploadInput
	store := fakeMusicStore{saveUpload: func(_ context.Context, input music.UploadInput) (music.UploadResult, error) {
		captured = input
		return music.UploadResult{Track: domain.MusicTrackRecord{ID: "track-1", Title: input.Title, OriginalName: input.Header.Filename, CreatedAt: time.Now()}}, nil
	}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("title", "Remote song")
	_ = writer.WriteField("artist", "Artist")
	part, err := writer.CreateFormFile("file", "song.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("audio-data"))
	lyricPart, err := writer.CreateFormFile("lyric", "song.lrc")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = lyricPart.Write([]byte("[00:00.00]Song"))
	artworkPart, err := writer.CreateFormFile("artwork", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = artworkPart.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	_ = writer.Close()

	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodPost, "/api/music/tracks", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.Title != "Remote song" || captured.Artist != "Artist" || captured.Header.Filename != "song.mp3" || captured.LyricHeader.Filename != "song.lrc" || captured.ArtworkHeader.Filename != "cover.png" {
		t.Fatalf("unexpected upload input: %#v", captured)
	}
}

func TestUploadMusicTrackReturnsOKForDuplicate(t *testing.T) {
	store := fakeMusicStore{saveUpload: func(_ context.Context, _ music.UploadInput) (music.UploadResult, error) {
		return music.UploadResult{Track: domain.MusicTrackRecord{ID: "existing"}, Duplicate: true}, nil
	}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "song.mp3")
	_, _ = part.Write([]byte("same-audio"))
	_ = writer.Close()
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodPost, "/api/music/tracks", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"duplicate":true`) {
		t.Fatalf("unexpected duplicate response %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListMusicTracksPassesCursorAndLimit(t *testing.T) {
	store := fakeMusicStore{list: func(_ context.Context, cursor string, limit int) (music.Page, error) {
		if cursor != "next-page" || limit != 7 {
			t.Fatalf("unexpected pagination: cursor=%q limit=%d", cursor, limit)
		}
		return music.Page{Tracks: []domain.MusicTrackRecord{{ID: "track-1", Title: "Song"}}, NextCursor: "more"}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/music/tracks?cursor=next-page&limit=7", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"next_cursor":"more"`) {
		t.Fatalf("unexpected list response %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStreamMusicTrackSupportsRanges(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "music-*.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("0123456789"))
	_ = file.Close()
	store := fakeMusicStore{getByID: func(_ context.Context, id string) (domain.MusicTrackRecord, error) {
		return domain.MusicTrackRecord{ID: id, OriginalName: "song.mp3", MIMEType: "audio/mpeg", StoragePath: file.Name()}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/music/tracks/track-1/content", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("unexpected range response %d: %q", rec.Code, rec.Body.String())
	}
}

func TestStreamMusicLyrics(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "lyrics-*.lrc")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("[00:00.00]Lyrics"))
	_ = file.Close()
	store := fakeMusicStore{getByID: func(_ context.Context, id string) (domain.MusicTrackRecord, error) {
		return domain.MusicTrackRecord{ID: id, LyricFileName: "song.lrc", LyricMIMEType: "text/plain", LyricStoragePath: file.Name()}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/music/tracks/track-1/lyrics", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "[00:00.00]Lyrics" {
		t.Fatalf("unexpected lyric response %d: %q", rec.Code, rec.Body.String())
	}
}

func TestStreamMusicArtworkUsesInlinePrivateCaching(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "artwork-*.png")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	_, _ = file.Write(content)
	_ = file.Close()
	store := fakeMusicStore{getByID: func(_ context.Context, id string) (domain.MusicTrackRecord, error) {
		return domain.MusicTrackRecord{
			ID:                   id,
			ArtworkAvailable:     true,
			ArtworkFileName:      "cover.png",
			ArtworkMIMEType:      "image/png",
			ArtworkStoragePath:   file.Name(),
			ArtworkContentSHA256: "artwork-hash",
		}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/music/tracks/track-1/artwork", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("unexpected artwork response %d: %q", rec.Code, rec.Body.Bytes())
	}
	if rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") {
		t.Fatalf("unexpected artwork headers: %#v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=86400" || rec.Header().Get("ETag") != `"artwork-hash"` {
		t.Fatalf("unexpected artwork cache headers: %#v", rec.Header())
	}
}

func TestStreamMusicArtworkReturnsNotFoundWhenUnavailable(t *testing.T) {
	store := fakeMusicStore{getByID: func(_ context.Context, id string) (domain.MusicTrackRecord, error) {
		return domain.MusicTrackRecord{ID: id}, nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodGet, "/api/music/tracks/track-1/artwork", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteMusicTrack(t *testing.T) {
	deletedID := ""
	store := fakeMusicStore{deleteTrack: func(_ context.Context, id string) error {
		deletedID = id
		return nil
	}}
	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := newHTTPAPITestRequest(http.MethodDelete, "/api/music/tracks/track-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || deletedID != "track-1" {
		t.Fatalf("unexpected delete response %d for %q", rec.Code, deletedID)
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
	req := newHTTPAPITestRequest(http.MethodPut, "/api/memo", strings.NewReader(body))
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

	req := newHTTPAPITestRequest(http.MethodPut, "/api/memo", strings.NewReader(`{"slug":"inbox","title":"随手记","content_html":"","content_text":""}`))
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

	req := newHTTPAPITestRequest(http.MethodPut, "/api/memo", strings.NewReader(`{"slug":"inbox","floating_cards":{}}`))
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
	getOrCreate    func(context.Context, string) (domain.MemoRecord, error)
	saveMemo       func(context.Context, memo.SaveInput) (domain.MemoRecord, error)
	listDocuments  func(context.Context) ([]domain.MemoDocumentSummary, error)
	getDocument    func(context.Context, string) (domain.MemoRecord, error)
	saveDocument   func(context.Context, string, memo.DocumentSaveInput) (domain.MemoRecord, error)
	listSideNotes  func(context.Context, string) ([]domain.MemoSideNoteRecord, error)
	createSideNote func(context.Context, string, memo.SideNoteInput) (domain.MemoSideNoteRecord, error)
	saveSideNote   func(context.Context, string, memo.SideNoteInput) (domain.MemoSideNoteRecord, error)
}

type fakeMusicStore struct {
	saveUpload  func(context.Context, music.UploadInput) (music.UploadResult, error)
	list        func(context.Context, string, int) (music.Page, error)
	getByID     func(context.Context, string) (domain.MusicTrackRecord, error)
	deleteTrack func(context.Context, string) error
}

func (s fakeMusicStore) SaveUpload(ctx context.Context, input music.UploadInput) (music.UploadResult, error) {
	return s.saveUpload(ctx, input)
}

func (s fakeMusicStore) List(ctx context.Context, cursor string, limit int) (music.Page, error) {
	return s.list(ctx, cursor, limit)
}

func (s fakeMusicStore) GetByID(ctx context.Context, id string) (domain.MusicTrackRecord, error) {
	return s.getByID(ctx, id)
}

func (s fakeMusicStore) Delete(ctx context.Context, id string) error {
	return s.deleteTrack(ctx, id)
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

func (s fakeMemoStore) CreateDocument(_ context.Context, slug, title string) (domain.MemoRecord, error) {
	return domain.MemoRecord{ID: "memo-1", Slug: slug, Title: title, Revision: 1}, nil
}

func (s fakeMemoStore) ListDocuments(ctx context.Context) ([]domain.MemoDocumentSummary, error) {
	if s.listDocuments != nil {
		return s.listDocuments(ctx)
	}
	return []domain.MemoDocumentSummary{}, nil
}

func (s fakeMemoStore) GetDocument(ctx context.Context, slug string) (domain.MemoRecord, error) {
	if s.getDocument != nil {
		return s.getDocument(ctx, slug)
	}
	return domain.MemoRecord{ID: "memo-1", Slug: slug, Revision: 1}, nil
}

func (s fakeMemoStore) SaveDocument(ctx context.Context, id string, input memo.DocumentSaveInput) (domain.MemoRecord, error) {
	if s.saveDocument != nil {
		return s.saveDocument(ctx, id, input)
	}
	return domain.MemoRecord{ID: id, Title: input.Title, ContentJSON: input.ContentJSON, Revision: input.Revision + 1}, nil
}

func (s fakeMemoStore) DeleteDocument(context.Context, string) error { return nil }

func (s fakeMemoStore) ListSideNotes(ctx context.Context, documentID string) ([]domain.MemoSideNoteRecord, error) {
	if s.listSideNotes != nil {
		return s.listSideNotes(ctx, documentID)
	}
	return []domain.MemoSideNoteRecord{}, nil
}

func (s fakeMemoStore) CreateSideNote(ctx context.Context, documentID string, input memo.SideNoteInput) (domain.MemoSideNoteRecord, error) {
	if s.createSideNote != nil {
		return s.createSideNote(ctx, documentID, input)
	}
	return domain.MemoSideNoteRecord{ID: "note-1", DocumentID: documentID, Revision: 1}, nil
}

func (s fakeMemoStore) SaveSideNote(ctx context.Context, id string, input memo.SideNoteInput) (domain.MemoSideNoteRecord, error) {
	if s.saveSideNote != nil {
		return s.saveSideNote(ctx, id, input)
	}
	return domain.MemoSideNoteRecord{ID: id, Revision: input.Revision + 1}, nil
}

func (s fakeMemoStore) DeleteSideNote(context.Context, string) (string, error) { return "memo-1", nil }
