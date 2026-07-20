package stewardcompanion

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchCaptureControlProjectsAuthoritativeState(t *testing.T) {
	server := newCaptureControlServer(t, map[string]string{
		"/api/steward/intelligence-settings": `{"settings":{"enabled":true,"timezone":"Asia/Shanghai","activity_sample_seconds":23,"revision":7}}`,
		"/api/steward/collectors":            `{"collectors":[{"name":"windows-activity","enabled":true}]}`,
		"/api/steward/agent":                 `{"agent":{"status":"running"}}`,
		"/api/steward/runtime/control":       `{"control":{"paused":false,"stopped":false,"generation":4}}`,
	})
	defer server.Close()

	control, err := FetchCaptureControl(context.Background(), server.URL+"/api", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !control.CaptureEnabled || !control.FlushEnabled {
		t.Fatalf("expected capture and flush enabled: %#v", control)
	}
	if control.Interval != 23*time.Second || control.Timezone != "Asia/Shanghai" || control.Revision != 11 {
		t.Fatalf("unexpected projected settings: %#v", control)
	}
}

func TestFetchCaptureControlStopsForEveryAuthoritativeSwitch(t *testing.T) {
	tests := []struct {
		name       string
		settings   string
		collectors string
		agent      string
		control    string
		flush      bool
	}{
		{name: "intelligence disabled", settings: `{"settings":{"enabled":false}}`, collectors: enabledWindowsCollectorJSON(), agent: runningAgentJSON(), control: runningControlJSON(), flush: true},
		{name: "collector disabled", settings: enabledIntelligenceJSON(), collectors: `{"collectors":[{"name":"windows-activity","enabled":false}]}`, agent: runningAgentJSON(), control: runningControlJSON(), flush: true},
		{name: "agent stopped", settings: enabledIntelligenceJSON(), collectors: enabledWindowsCollectorJSON(), agent: `{"agent":{"status":"stopped"}}`, control: runningControlJSON()},
		{name: "globally paused", settings: enabledIntelligenceJSON(), collectors: enabledWindowsCollectorJSON(), agent: runningAgentJSON(), control: `{"control":{"paused":true}}`},
		{name: "emergency stopped", settings: enabledIntelligenceJSON(), collectors: enabledWindowsCollectorJSON(), agent: runningAgentJSON(), control: `{"control":{"stopped":true}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newCaptureControlServer(t, map[string]string{
				"/api/steward/intelligence-settings": test.settings,
				"/api/steward/collectors":            test.collectors,
				"/api/steward/agent":                 test.agent,
				"/api/steward/runtime/control":       test.control,
			})
			defer server.Close()
			got, err := FetchCaptureControl(context.Background(), server.URL+"/api", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if got.CaptureEnabled {
				t.Fatalf("capture remained enabled: %#v", got)
			}
			if got.FlushEnabled != test.flush {
				t.Fatalf("flush=%v, want %v", got.FlushEnabled, test.flush)
			}
		})
	}
}

func TestFetchCaptureControlReportsEndpointFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, err := FetchCaptureControl(context.Background(), server.URL+"/api", server.Client())
	if err == nil {
		t.Fatal("expected endpoint error")
	}
}

func TestOfflineFetchLeavesLastAuthenticatedControlUsable(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{13}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	binding, err := CaptureControlCacheBinding("http://127.0.0.1:18080/api", "authenticated-token")
	if err != nil {
		t.Fatal(err)
	}
	want := CaptureControl{CaptureEnabled: true, FlushEnabled: true, Interval: 10 * time.Second, Timezone: "Asia/Shanghai", Revision: 6}
	if err := buffer.SaveAuthenticatedCaptureControl(ctx, binding, want, time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	apiBase := server.URL + "/api"
	server.Close()
	if _, err := FetchCaptureControl(ctx, apiBase, server.Client()); err == nil {
		t.Fatal("expected offline control refresh failure")
	}
	cached, err := buffer.LoadAuthenticatedCaptureControl(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Control != want {
		t.Fatalf("offline fallback=%#v, want %#v", cached.Control, want)
	}
	sampler := &captureTestSampler{snapshots: []ActivitySnapshot{{
		CapturedAt: time.Date(2026, 7, 20, 8, 1, 0, 0, time.UTC), Application: "Code", WindowTitle: "offline.go", SessionID: "cached-session",
	}}}
	sink := &captureTestSink{}
	loop := NewCaptureLoop(sampler, sink, CaptureOptions{Interval: time.Minute})
	loop.ApplyControl(false, CaptureOptions{})
	loop.ApplyControl(cached.Control.CaptureEnabled, CaptureOptions{Interval: cached.Control.Interval, Timezone: cached.Control.Timezone})
	loop.captureOnce(ctx)
	if len(sink.items) != 2 || sink.items[0].SourceTimezone != "Asia/Shanghai" {
		t.Fatalf("cached control did not continue offline local enqueue: %#v", sink.items)
	}
}

func TestUnauthorizedFetchCannotOverwriteAuthenticatedControlCache(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{14}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	binding, err := CaptureControlCacheBinding("http://127.0.0.1:18080/api", "authenticated-token")
	if err != nil {
		t.Fatal(err)
	}
	want := CaptureControl{CaptureEnabled: false, FlushEnabled: false, Interval: 10 * time.Second, Revision: 10}
	if err := buffer.SaveAuthenticatedCaptureControl(ctx, binding, want, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "invalid bearer token", status)
		}))
		_, fetchErr := FetchCaptureControl(ctx, server.URL+"/api", server.Client())
		server.Close()
		if fetchErr == nil || !strings.Contains(fetchErr.Error(), fmt.Sprint(status)) {
			t.Fatalf("authentication failure %d error=%v", status, fetchErr)
		}
	}
	cached, err := buffer.LoadAuthenticatedCaptureControl(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Control != want {
		t.Fatalf("unauthorized response changed cache: %#v", cached.Control)
	}
}

func newCaptureControlServer(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			http.Error(w, fmt.Sprintf("unexpected endpoint %s", r.URL.Path), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func enabledIntelligenceJSON() string { return `{"settings":{"enabled":true}}` }
func enabledWindowsCollectorJSON() string {
	return `{"collectors":[{"name":"windows-activity","enabled":true}]}`
}
func runningAgentJSON() string   { return `{"agent":{"status":"running"}}` }
func runningControlJSON() string { return `{"control":{"paused":false,"stopped":false}}` }
