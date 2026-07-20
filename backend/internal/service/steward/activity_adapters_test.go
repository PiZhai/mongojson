package steward

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchActivityWatchEventsPaginatesAndSorts(t *testing.T) {
	base := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/buckets/window/events" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "2" {
			t.Errorf("limit=%q", r.URL.Query().Get("limit"))
		}
		page := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			_ = json.NewEncoder(w).Encode([]activityWatchEvent{
				{ID: 3, Timestamp: base.Add(20 * time.Minute).Format(time.RFC3339Nano), Duration: 10},
				{ID: 2, Timestamp: base.Add(10 * time.Minute).Format(time.RFC3339Nano), Duration: 10},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]activityWatchEvent{{ID: 1, Timestamp: base.Format(time.RFC3339Nano), Duration: 10}})
	}))
	defer server.Close()
	events, err := fetchActivityWatchEvents(context.Background(), server.URL, "window", base.Add(-time.Minute), base.Add(30*time.Minute), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].ID != 1 || events[1].ID != 2 || events[2].ID != 3 {
		t.Fatalf("events=%#v", events)
	}
	if calls.Load() != 2 {
		t.Fatalf("pages=%d", calls.Load())
	}
}

func TestResolveActivityWatchEndpointUsesConfiguredHealthyServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/buckets" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	endpoint, response, err := resolveActivityWatchEndpoint(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if endpoint != server.URL || response.StatusCode != http.StatusOK {
		t.Fatalf("endpoint=%q status=%d", endpoint, response.StatusCode)
	}
}

func TestResolveActivityWatchEndpointRequiresConfiguredEndpoint(t *testing.T) {
	if _, response, err := resolveActivityWatchEndpoint(context.Background(), ""); err == nil || response != nil {
		t.Fatalf("response=%v err=%v", response, err)
	}
}

func TestFetchActivityWatchInfoUsesRealServerContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/0/info" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hostname": "DESKTOP-01", "version": "0.13.2", "testing": false,
			"device_id": "719d113c-3e2a-4b3f-8623-e3fda28662df",
		})
	}))
	defer server.Close()
	info, err := fetchActivityWatchInfo(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if info.Hostname != "DESKTOP-01" || info.Version != "0.13.2" || info.DeviceID != "719d113c-3e2a-4b3f-8623-e3fda28662df" {
		t.Fatalf("info=%+v", info)
	}
}

func TestActivityWatchFreshnessUsesEventEndWhileCursorKeepsStartOverlap(t *testing.T) {
	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	cursor := activityWatchCursor{Timestamp: start, EventID: 42}
	if got := activityWatchFetchStart(cursor, start.Add(time.Hour)); !got.Equal(start.Add(-2 * time.Minute)) {
		t.Fatalf("overlap start=%s", got)
	}
	if got := activityWatchEventEnd(start, 90.5); !got.Equal(start.Add(90*time.Second + 500*time.Millisecond)) {
		t.Fatalf("source freshness end=%s", got)
	}
	if got := cursor.asMap()["timestamp"]; got != start.Format(time.RFC3339Nano) {
		t.Fatalf("cursor advanced to duration end: %v", got)
	}
}

func TestNormalizeActivityWatchInteractiveSessionMatchesLocalCompanion(t *testing.T) {
	serverDeviceID := "719d113c-3e2a-4b3f-8623-e3fda28662df"
	if got := normalizeActivityWatchInteractiveSessionID(
		"DESKTOP-01", serverDeviceID, "desktop-01.local", serverDeviceID, "windows-3",
	); got != "windows-3" {
		t.Fatalf("local ActivityWatch session=%q", got)
	}
	if got := normalizeActivityWatchInteractiveSessionID(
		"DESKTOP-01", serverDeviceID, "LAPTOP-REMOTE", "remote-device", "windows-3",
	); got != "activitywatch:remote-device" {
		t.Fatalf("remote ActivityWatch session=%q", got)
	}
}

func TestActivityWatchSourceClockUsesConfiguredTimezoneAndOffset(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	clock := (*Service)(nil).activityWatchSourceClock(context.Background(), map[string]any{"source_timezone": "Asia/Shanghai"}, activityWatchInfo{}, now)
	if clock.Timezone != "Asia/Shanghai" || clock.UTCOffsetSeconds != 8*60*60 || clock.Source != "collector_setting" {
		t.Fatalf("clock=%+v", clock)
	}
}

func TestActivitySourceHealthDoesNotTreatPollingAsSourceFreshness(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	pollAt := now.Add(-time.Minute)
	item := ActivitySourceStatus{Status: "healthy", LastPollAt: &pollAt, MaxExpectedLagSeconds: 300}
	classifyActivitySourceHealth(&item, now)
	if !item.Reachable || item.SourceFresh || item.IngestionFresh || item.Fresh {
		t.Fatalf("poll-only health=%+v", item)
	}
	eventAt, ingestedAt := now.Add(-2*time.Minute), now.Add(-30*time.Second)
	item.LastSourceEventAt, item.LastIngestedAt = &eventAt, &ingestedAt
	classifyActivitySourceHealth(&item, now)
	if !item.Reachable || !item.SourceFresh || !item.IngestionFresh || !item.Fresh {
		t.Fatalf("end-to-end health=%+v", item)
	}
}

func TestActivityWatchEventRevisionTracksHeartbeatDuration(t *testing.T) {
	for _, test := range []struct {
		duration float64
		want     int64
	}{{0, 1}, {1.5, 1501}, {-10, 1}} {
		t.Run(strconv.FormatFloat(test.duration, 'f', -1, 64), func(t *testing.T) {
			if got := activityWatchEventRevision(test.duration); got != test.want {
				t.Fatalf("revision=%d want=%d", got, test.want)
			}
		})
	}
}

func TestCollectActivityWatchPersistsSourceTruthPostgres(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the ActivityWatch source-truth Postgres test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("activitywatch-source-truth-e2e"), WithAutonomyAdvisor(DisabledAutonomyAdvisor("test")))
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	companionSourceAt := now.Add(-30 * time.Second)
	if err := service.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
		Collector: "companion:windows-activity", SourceKey: "windows-7:foreground_window",
		Watcher: "steward-companion", Host: "DESKTOP-01", EventType: "foreground_window",
		InteractiveSessionID: "windows-7", Status: "healthy", LastPollAt: companionSourceAt,
		LastSourceEventAt: &companionSourceAt, LastIngestedAt: &companionSourceAt, MaxExpectedLagSeconds: 600,
	}); err != nil {
		t.Fatal(err)
	}

	eventStart := now.Add(-90 * time.Second)
	eventDuration := 45.0
	serverDeviceID := "719d113c-3e2a-4b3f-8623-e3fda28662df"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/0/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hostname": "DESKTOP-01", "version": "0.13.2", "testing": false,
				"device_id": serverDeviceID, "capabilities": map[string]any{"query_api": true},
			})
		case "/api/0/buckets":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"aw-watcher-window_DESKTOP-01": map[string]any{
					"type": "currentwindow", "client": "aw-watcher-window", "hostname": "desktop-01.local",
					"data": map[string]any{"device_id": serverDeviceID},
				},
			})
		case "/api/0/buckets/aw-watcher-window_DESKTOP-01/events":
			_ = json.NewEncoder(w).Encode([]activityWatchEvent{{
				ID: 42, Timestamp: eventStart.In(time.FixedZone("UTC+8", 8*60*60)).Format(time.RFC3339Nano),
				Duration: eventDuration, Data: map[string]any{"app": "Code.exe", "title": "activity_adapters.go"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := service.collectActivityWatch(ctx, map[string]any{
		"endpoint": server.URL, "limit": 100, "source_timezone": "Asia/Shanghai",
	}); err != nil {
		t.Fatal(err)
	}

	var sessionID, sourceTimezone string
	var occurredAt, endedAt time.Time
	if err := db.Pool.QueryRow(ctx, `select interactive_session_id,source_timezone,occurred_at,ended_at
		from steward_observations where source='adapter:activitywatch' order by created_at desc limit 1`).
		Scan(&sessionID, &sourceTimezone, &occurredAt, &endedAt); err != nil {
		t.Fatal(err)
	}
	if sessionID != "windows-7" || sourceTimezone != "Asia/Shanghai" || !occurredAt.Equal(eventStart) || !endedAt.Equal(eventStart.Add(45*time.Second)) {
		t.Fatalf("observation session=%q timezone=%q occurred=%s ended=%s", sessionID, sourceTimezone, occurredAt, endedAt)
	}

	var apiVersion, host, persistedSession string
	var cursorJSON, capabilitiesJSON []byte
	var lastSourceEventAt time.Time
	if err := db.Pool.QueryRow(ctx, `select api_version,host,interactive_session_id,cursor,capabilities,last_source_event_at
		from steward_collection_source_states where device_id=$1 and collector_name='activitywatch-bridge'
		and source_key='aw-watcher-window_DESKTOP-01'`, service.agentIDValue()).
		Scan(&apiVersion, &host, &persistedSession, &cursorJSON, &capabilitiesJSON, &lastSourceEventAt); err != nil {
		t.Fatal(err)
	}
	var cursor, capabilities map[string]any
	if err := json.Unmarshal(cursorJSON, &cursor); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		t.Fatal(err)
	}
	if apiVersion != "0.13.2" || host != "desktop-01.local" || persistedSession != "windows-7" {
		t.Fatalf("source identity api=%q host=%q session=%q", apiVersion, host, persistedSession)
	}
	if cursor["timestamp"] != eventStart.Format(time.RFC3339Nano) || !lastSourceEventAt.Equal(eventStart.Add(45*time.Second)) {
		t.Fatalf("cursor=%v source_end=%s", cursor, lastSourceEventAt)
	}
	if capabilities["source_device_id"] != serverDeviceID || capabilities["source_timezone"] != "Asia/Shanghai" || capabilities["source_utc_offset_seconds"] != float64(8*60*60) {
		t.Fatalf("capabilities=%+v", capabilities)
	}

	pipeline, err := service.ActivityPipelineStatus(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var serverStatus, bucketStatus *ActivitySourceStatus
	for index := range pipeline.Sources {
		item := &pipeline.Sources[index]
		if item.CollectorName != "activitywatch-bridge" {
			continue
		}
		if item.SourceKey == "server" {
			serverStatus = item
		} else if item.SourceKey == "aw-watcher-window_DESKTOP-01" {
			bucketStatus = item
		}
	}
	if serverStatus == nil || !serverStatus.Reachable || serverStatus.SourceFresh || !serverStatus.Fresh {
		t.Fatalf("server poll health=%+v", serverStatus)
	}
	if bucketStatus == nil || !bucketStatus.Reachable || !bucketStatus.SourceFresh || !bucketStatus.IngestionFresh || !bucketStatus.Fresh {
		t.Fatalf("bucket source health=%+v", bucketStatus)
	}
}
