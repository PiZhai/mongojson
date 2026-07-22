package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithStaticWorkspaceServesAPIAndSPA(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(uiDir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<html>steward workspace</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "assets", "app.js"), []byte("console.log('steward')"), 0o644); err != nil {
		t.Fatal(err)
	}

	api := http.NewServeMux()
	api.HandleFunc("/api/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	api.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	handler, err := withStaticWorkspace(api, uiDir)
	if err != nil {
		t.Fatal(err)
	}

	assertResponseBody(t, handler, "/api/ping", `{"ok":true}`)
	assertResponseBody(t, handler, "/healthz", "ok")
	assertResponseBody(t, handler, "/assets/app.js", "console.log('steward')")
	assertResponseBody(t, handler, "/steward", "steward workspace")
	assertResponseBody(t, handler, "/tools/steward", "steward workspace")
	assertCacheControl(t, handler, "/assets/app.js", "no-cache")
	assertCacheControl(t, handler, "/steward", "no-store")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func assertCacheControl(t *testing.T, handler http.Handler, path string, want string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(rr, req)
	if got := rr.Header().Get("Cache-Control"); got != want {
		t.Fatalf("%s Cache-Control = %q, want %q", path, got, want)
	}
}

func TestWithStaticWorkspaceRequiresIndex(t *testing.T) {
	_, err := withStaticWorkspace(http.NewServeMux(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("expected missing index error, got %v", err)
	}
}

func assertResponseBody(t *testing.T, handler http.Handler, path string, want string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s status = %d, want 200; body=%s", path, rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("%s body = %q, want to contain %q", path, rr.Body.String(), want)
	}
}
