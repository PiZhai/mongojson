package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/service/jobs"
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

type repeatingReader byte

func (r repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}
