package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestUploadMusicTrackAcceptsMetadata(t *testing.T) {
	var captured music.UploadInput
	store := fakeMusicStore{saveUpload: func(_ context.Context, input music.UploadInput) (domain.MusicTrackRecord, error) {
		captured = input
		return domain.MusicTrackRecord{ID: "track-1", Title: input.Title, OriginalName: input.Header.Filename, CreatedAt: time.Now()}, nil
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
	_ = writer.Close()

	router := chi.NewRouter()
	RegisterRoutes(router, Dependencies{MusicService: store})
	req := httptest.NewRequest(http.MethodPost, "/api/music/tracks", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if captured.Title != "Remote song" || captured.Artist != "Artist" || captured.Header.Filename != "song.mp3" {
		t.Fatalf("unexpected upload input: %#v", captured)
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
	req := httptest.NewRequest(http.MethodGet, "/api/music/tracks?cursor=next-page&limit=7", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/api/music/tracks/track-1/content", nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("unexpected range response %d: %q", rec.Code, rec.Body.String())
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

type fakeMusicStore struct {
	saveUpload func(context.Context, music.UploadInput) (domain.MusicTrackRecord, error)
	list       func(context.Context, string, int) (music.Page, error)
	getByID    func(context.Context, string) (domain.MusicTrackRecord, error)
}

func (s fakeMusicStore) SaveUpload(ctx context.Context, input music.UploadInput) (domain.MusicTrackRecord, error) {
	return s.saveUpload(ctx, input)
}

func (s fakeMusicStore) List(ctx context.Context, cursor string, limit int) (music.Page, error) {
	return s.list(ctx, cursor, limit)
}

func (s fakeMusicStore) GetByID(ctx context.Context, id string) (domain.MusicTrackRecord, error) {
	return s.getByID(ctx, id)
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
