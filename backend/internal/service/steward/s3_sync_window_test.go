package steward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestSyncChangeListModesUseProtocolSafeOrdering(t *testing.T) {
	replayQuery := syncChangeListQuery(syncChangeListReplay)
	if !strings.Contains(replayQuery, "order by sequence asc") {
		t.Fatalf("replay query must return oldest changes first: %s", replayQuery)
	}

	recentQuery := syncChangeListQuery(syncChangeListRecent)
	if !strings.Contains(recentQuery, "order by sequence desc") {
		t.Fatalf("recent query must return newest changes first: %s", recentQuery)
	}
}

func TestGetPeerSyncChangesAcceptsLegacyResponseWithoutCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"changes":[{"sequence":7,"payload":{}}]}`)
	}))
	defer server.Close()

	window, err := getPeerSyncChanges(context.Background(), server.Client(), server.URL+"/api", 4, syncAuth{})
	if err != nil {
		t.Fatalf("read legacy peer response: %v", err)
	}
	if window.NextSequence != 7 || len(window.Changes) != 1 {
		t.Fatalf("legacy response cursor = %+v", window)
	}
}

func TestGetPeerSyncChangesPreservesFilteredCursor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"changes":[],"next_sequence":12,"has_more":false}`)
	}))
	defer server.Close()

	window, err := getPeerSyncChanges(context.Background(), server.Client(), server.URL+"/api", 4, syncAuth{})
	if err != nil {
		t.Fatalf("read filtered peer response: %v", err)
	}
	if window.NextSequence != 12 || len(window.Changes) != 0 || window.HasMore {
		t.Fatalf("filtered response cursor = %+v", window)
	}
}

func TestBuildPullSyncWindowPreservesReplayOrderAndWaterline(t *testing.T) {
	device := domain.StewardDevice{
		ID:              "macbook-main",
		DeviceName:      "MacBook Main",
		Platform:        "darwin",
		SyncEnabled:     true,
		PermissionLevel: PermissionA3,
		PublicKey:       "peer-public-key",
		APIBaseURL:      "http://192.168.1.12:18080/api",
	}
	remoteChanges := []domain.StewardSyncChange{
		{ID: "remote-10", Sequence: 10, EntityType: "task", EntityID: "task-10", Operation: SyncCreate, OriginDeviceID: "macbook-main", Version: 1, Payload: map[string]any{"title": "first"}},
		{ID: "remote-11", Sequence: 11, EntityType: "task", EntityID: "task-11", Operation: SyncUpdate, OriginDeviceID: "windows-main", Version: 1, Payload: map[string]any{"title": "echo"}},
		{ID: "remote-12", Sequence: 12, EntityType: EntityMemory, EntityID: "memory-12", Operation: SyncCreate, OriginDeviceID: "linux-lab", Version: 1, Payload: map[string]any{"title": "third"}},
	}

	window := buildPullSyncWindow("windows-main", device, remoteChanges, 9)

	if window.RemoteLastSequence != 12 {
		t.Fatalf("remote waterline = %d, want 12", window.RemoteLastSequence)
	}
	if window.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", window.Skipped)
	}
	if got := changeInputIDs(window.Input.Changes); strings.Join(got, ",") != "remote-10,remote-12" {
		t.Fatalf("pull window order = %v, want remote-10,remote-12", got)
	}
	if window.Input.Device.ID != "macbook-main" || window.Input.Device.Role != DeviceRolePeer {
		t.Fatalf("unexpected pull device registration: %#v", window.Input.Device)
	}
}

func TestBuildPushSyncWindowPreservesReplayOrderAndWaterline(t *testing.T) {
	localDevice := RegisterDeviceInput{
		ID:              "windows-main",
		DeviceName:      "Windows Main",
		Platform:        "windows",
		Role:            DeviceRolePeer,
		SyncEnabled:     boolPtr(true),
		PermissionLevel: PermissionA3,
		PublicKey:       "local-public-key",
		APIBaseURL:      "http://192.168.1.10:18080/api",
	}
	localChanges := []domain.StewardSyncChange{
		{ID: "local-20", Sequence: 20, EntityType: "task", EntityID: "task-20", Operation: SyncCreate, OriginDeviceID: "windows-main", Version: 1, Payload: map[string]any{"title": "first"}},
		{ID: "local-21", Sequence: 21, EntityType: "task", EntityID: "task-21", Operation: SyncUpdate, OriginDeviceID: "macbook-main", Version: 1, Payload: map[string]any{"title": "peer echo"}},
		{ID: "local-22", Sequence: 22, EntityType: EntityKnowledgeItem, EntityID: "knowledge-22", Operation: SyncCreate, OriginDeviceID: "linux-lab", Version: 1, Payload: map[string]any{"title": "third"}},
	}

	window := buildPushSyncWindow(localDevice, "macbook-main", localChanges, 19)

	if window.LocalSentSequence != 22 {
		t.Fatalf("local sent waterline = %d, want 22", window.LocalSentSequence)
	}
	if got := changeInputIDs(window.Input.Changes); strings.Join(got, ",") != "local-20,local-22" {
		t.Fatalf("push window order = %v, want local-20,local-22", got)
	}
	if window.Input.Device.ID != "windows-main" {
		t.Fatalf("unexpected push device registration: %#v", window.Input.Device)
	}
}

func changeInputIDs(changes []CreateSyncChangeInput) []string {
	ids := make([]string, 0, len(changes))
	for _, change := range changes {
		ids = append(ids, change.ID)
	}
	return ids
}
