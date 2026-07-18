package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

func TestStewardSyncHTTPReplicatesTasksAcrossIndependentDatabases(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward HTTP sync integration test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-http-sync-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	windowsConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "windows")
	macConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mac")

	windowsNode := newStewardHTTPNode(t, ctx, windowsConfig, "windows-main")
	macNode := newStewardHTTPNode(t, ctx, macConfig, "macbook-main")

	registerPeer(t, ctx, windowsNode, "macbook-main", "MacBook Main", "darwin", macNode.peerAPIBase)
	registerPeer(t, ctx, macNode, "windows-main", "Windows Main", "windows", windowsNode.peerAPIBase)

	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	windowsTaskTitle := "steward http sync from windows " + nonce
	windowsTask, err := windowsNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           windowsTaskTitle,
		Description:     "created by S3/S4 HTTP sync integration test",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create windows task: %v", err)
	}

	firstSync, err := windowsNode.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("sync windows to mac: %+v: %v", firstSync, err)
	}
	if firstSync.Pushed == 0 {
		t.Fatalf("expected first sync to push at least one change, got %+v", firstSync)
	}
	assertTaskVisibleThroughHTTP(t, macNode, windowsTask.ID, windowsTaskTitle)
	probe, err := windowsNode.service.ProbeDeviceSyncEntity(ctx, "macbook-main", steward.SyncEntityProbeInput{
		EntityType: steward.EntityTask,
		EntityID:   windowsTask.ID,
	})
	if err != nil {
		t.Fatalf("probe synced task through signed peer API: %v", err)
	}
	if !probe.Exists || probe.DeviceID != "macbook-main" {
		t.Fatalf("unexpected signed peer probe: %+v", probe)
	}
	var observedRole string
	if err := macNode.pool.QueryRow(ctx, `select role from steward_devices where id = 'windows-main'`).Scan(&observedRole); err != nil {
		t.Fatalf("read observed peer role: %v", err)
	}
	if observedRole != steward.DeviceRolePeer {
		t.Fatalf("sync heartbeat changed peer role to %q", observedRole)
	}

	macTaskTitle := "steward http sync from mac " + nonce
	macTask, err := macNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           macTaskTitle,
		Description:     "created while windows has not pulled yet",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create mac task: %v", err)
	}

	catchUpSync, err := windowsNode.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("sync mac catch-up to windows: %+v: %v", catchUpSync, err)
	}
	if catchUpSync.Pulled == 0 || catchUpSync.Applied == 0 {
		t.Fatalf("expected catch-up sync to pull and apply remote changes, got %+v", catchUpSync)
	}
	assertTaskVisibleThroughHTTP(t, windowsNode, macTask.ID, macTaskTitle)
	if _, err := macNode.service.RevokeDevice(ctx, "windows-main"); err != nil {
		t.Fatalf("revoke windows peer on mac: %v", err)
	}
	if _, err := windowsNode.service.ProbeDeviceSyncEntity(ctx, "macbook-main", steward.SyncEntityProbeInput{
		EntityType: steward.EntityTask,
		EntityID:   windowsTask.ID,
	}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("revoked HMAC device retained peer access: %v", err)
	}
}

func TestStewardSyncChangeCreationIsAtomicForConcurrentDuplicateID(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed sync change concurrency test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "sync_change_concurrency"), "windows-main")

	changeID := uuid.NewString()
	input := steward.CreateSyncChangeInput{
		ID:             changeID,
		EntityType:     "task",
		EntityID:       uuid.NewString(),
		Operation:      steward.SyncCreate,
		OriginDeviceID: "windows-main",
		Version:        1,
		DataLevel:      steward.DataD0,
		Payload:        map[string]any{"title": "concurrent idempotency probe"},
	}

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			change, err := node.service.CreateSyncChange(ctx, input)
			if err == nil && change.ID != changeID {
				err = fmt.Errorf("change id = %s, want %s", change.ID, changeID)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent duplicate sync change failed: %v", err)
		}
	}

	var count int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_sync_changes where id = $1`, changeID).Scan(&count); err != nil {
		t.Fatalf("count concurrent sync changes: %v", err)
	}
	if count != 1 {
		t.Fatalf("concurrent duplicate sync change count = %d, want 1", count)
	}
	collision := input
	collision.Operation = steward.SyncDelete
	if _, err := node.service.CreateSyncChange(ctx, collision); !errors.Is(err, steward.ErrSyncChangeInvalid) {
		t.Fatalf("immutable sync change id collision error = %v, want ErrSyncChangeInvalid", err)
	}
	registerPeer(t, ctx, node, "macbook-main", "MacBook Main", "darwin", "http://127.0.0.1:28081/api")
	remoteCollision := collision
	remoteCollision.OriginDeviceID = "macbook-main"
	result, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: steward.RegisterDeviceInput{
			ID: "macbook-main", DeviceName: "MacBook Main", Platform: "darwin", Role: steward.DeviceRolePeer,
			SyncEnabled: boolPointerValue(true), PermissionLevel: steward.PermissionA3, APIBaseURL: "http://127.0.0.1:28081/api",
		},
		Changes: []steward.CreateSyncChangeInput{remoteCollision},
	})
	if err != nil || result.Denied != 1 || result.Skipped != 1 || result.Imported != 0 {
		t.Fatalf("remote immutable id collision did not use skip semantics: result=%+v err=%v", result, err)
	}
}

func TestStewardSyncSkipsMalformedPeerChangesWithoutCursorStall(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the malformed sync change contract test")
	}
	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-malformed-change-contract")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	windows := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "change_contract_windows"), "windows-main")
	mac := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "change_contract_mac"), "macbook-main")
	registerPeer(t, ctx, windows, "macbook-main", "MacBook Main", "darwin", mac.peerAPIBase)
	registerPeer(t, ctx, mac, "windows-main", "Windows Main", "windows", windows.peerAPIBase)

	malformed := []struct {
		id        string
		operation string
		origin    string
		version   int
		dataLevel string
		payload   map[string]any
	}{
		{id: uuid.NewString(), operation: "upsert", origin: "windows-main", version: 1, dataLevel: steward.DataD0, payload: map[string]any{}},
		{id: uuid.NewString(), operation: steward.SyncUpdate, origin: "windows-main", version: 1, dataLevel: "secret", payload: map[string]any{}},
		{id: uuid.NewString(), operation: steward.SyncUpdate, origin: "windows-main", version: 0, dataLevel: steward.DataD0, payload: map[string]any{}},
		{id: uuid.NewString(), operation: steward.SyncUpdate, origin: "windows-main", version: 1, dataLevel: steward.DataD0, payload: map[string]any{"permission_level": "root"}},
	}
	var lastMalformedSequence int64
	for _, item := range malformed {
		payload, err := json.Marshal(item.payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.pool.QueryRow(ctx, `
			insert into steward_sync_changes (
				id, entity_type, entity_id, operation, origin_device_id, version, data_level,
				payload, payload_hash, sync_status, created_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,now())
			returning sequence
		`, item.id, steward.EntityTask, uuid.NewString(), item.operation, item.origin, item.version, item.dataLevel,
			string(payload), testPayloadHash(payload), steward.SyncPending).Scan(&lastMalformedSequence); err != nil {
			t.Fatalf("insert malformed sync change: %v", err)
		}
	}
	senderStatus, err := windows.service.GetSyncStatus(ctx)
	if err != nil {
		t.Fatalf("load sender sync contract: %v", err)
	}
	if senderStatus.ChangeContract.Healthy || senderStatus.ChangeContract.InvalidChanges != len(malformed) {
		t.Fatalf("sender historical contract did not expose malformed rows: %+v", senderStatus.ChangeContract)
	}

	validTask, err := windows.service.CreateTask(ctx, steward.CreateTaskInput{
		Title: "valid task after malformed peer changes", Source: "verification",
		DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3, RiskLevel: "low",
	})
	if err != nil {
		t.Fatalf("create valid task: %v", err)
	}
	first, err := windows.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("sync malformed window: %+v: %v", first, err)
	}
	if first.Denied != len(malformed) || first.Pushed < len(malformed)+1 {
		t.Fatalf("malformed window result = %+v", first)
	}
	assertTaskVisibleThroughHTTP(t, mac, validTask.ID, validTask.Title)

	for _, item := range malformed {
		var count int
		if err := mac.pool.QueryRow(ctx, `select count(*) from steward_sync_changes where id = $1`, item.id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("malformed change %s persisted: count=%d err=%v", item.id, count, err)
		}
	}
	var invalidAudits int
	if err := mac.pool.QueryRow(ctx, `select count(*) from steward_audit_logs where action = 'sync.change.invalid'`).Scan(&invalidAudits); err != nil || invalidAudits != len(malformed) {
		t.Fatalf("invalid change audits = %d, want %d: %v", invalidAudits, len(malformed), err)
	}

	second, err := windows.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("repeat sync after malformed window: %+v: %v", second, err)
	}
	if second.Denied != 0 {
		t.Fatalf("malformed changes were replayed after cursor advance: %+v", second)
	}
	var lastSentSequence int64
	if err := windows.pool.QueryRow(ctx, `select last_sent_sequence from steward_devices where id = 'macbook-main'`).Scan(&lastSentSequence); err != nil {
		t.Fatalf("load peer cursor: %v", err)
	}
	if lastSentSequence < lastMalformedSequence {
		t.Fatalf("last_sent_sequence=%d did not advance beyond malformed sequence %d", lastSentSequence, lastMalformedSequence)
	}
	status, err := mac.service.GetSyncStatus(ctx)
	if err != nil {
		t.Fatalf("load receiver sync status: %v", err)
	}
	if !status.ChangeContract.Healthy || status.ChangeContract.InvalidChanges != 0 {
		t.Fatalf("receiver contract was poisoned: %+v", status.ChangeContract)
	}
}

func testPayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func TestStewardThreeNodeMeshConvergesAfterOfflineReplayAndPropagatesRevocation(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed three-node mesh test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-three-node-mesh")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	windows := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mesh_windows"), "windows-main")
	mac := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mesh_mac"), "macbook-main")
	linux := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mesh_linux"), "linux-lab")

	registerPeer(t, ctx, windows, "macbook-main", "MacBook Main", "darwin", mac.peerAPIBase)
	registerPeer(t, ctx, windows, "linux-lab", "Linux Lab", "linux", linux.peerAPIBase)
	registerPeer(t, ctx, mac, "windows-main", "Windows Main", "windows", windows.peerAPIBase)
	registerPeer(t, ctx, mac, "linux-lab", "Linux Lab", "linux", linux.peerAPIBase)
	registerPeer(t, ctx, linux, "windows-main", "Windows Main", "windows", windows.peerAPIBase)
	registerPeer(t, ctx, linux, "macbook-main", "MacBook Main", "darwin", mac.peerAPIBase)

	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	windowsTask := createMeshTask(t, ctx, windows, "windows mesh task "+nonce)
	macTask := createMeshTask(t, ctx, mac, "mac mesh task "+nonce)
	linuxTask := createMeshTask(t, ctx, linux, "linux mesh task "+nonce)

	syncMeshPeer(t, ctx, windows, "macbook-main")
	syncMeshPeer(t, ctx, mac, "linux-lab")
	syncMeshPeer(t, ctx, linux, "windows-main")
	for _, node := range []stewardHTTPNode{windows, mac, linux} {
		assertTaskVisibleThroughHTTP(t, node, windowsTask.ID, windowsTask.Title)
		assertTaskVisibleThroughHTTP(t, node, macTask.ID, macTask.Title)
		assertTaskVisibleThroughHTTP(t, node, linuxTask.ID, linuxTask.Title)
	}

	// Linux remains offline while the other two nodes continue syncing, then
	// rejoins and its backlog is relayed through macOS to Windows.
	offlineTask := createMeshTask(t, ctx, linux, "linux offline replay task "+nonce)
	syncMeshPeer(t, ctx, windows, "macbook-main")
	visible, _, err := taskVisibleThroughHTTP(windows, offlineTask.ID, offlineTask.Title)
	if err != nil {
		t.Fatalf("check offline task before rejoin: %v", err)
	}
	if visible {
		t.Fatalf("offline Linux task appeared before Linux rejoined")
	}
	syncMeshPeer(t, ctx, linux, "macbook-main")
	syncMeshPeer(t, ctx, mac, "windows-main")
	for _, node := range []stewardHTTPNode{windows, mac, linux} {
		assertTaskVisibleThroughHTTP(t, node, offlineTask.ID, offlineTask.Title)
	}

	if _, err := windows.service.RevokeDevice(ctx, "linux-lab"); err != nil {
		t.Fatalf("revoke Linux from Windows: %v", err)
	}
	syncMeshPeer(t, ctx, windows, "macbook-main")
	var macLinuxTrust string
	var macLinuxSyncEnabled bool
	if err := mac.pool.QueryRow(ctx, `select trust_status, sync_enabled from steward_devices where id = 'linux-lab'`).Scan(&macLinuxTrust, &macLinuxSyncEnabled); err != nil {
		t.Fatalf("read propagated Linux revocation on macOS: %v", err)
	}
	if macLinuxTrust != steward.DeviceRevoked || macLinuxSyncEnabled {
		t.Fatalf("Linux revocation did not propagate to macOS: trust=%s enabled=%t", macLinuxTrust, macLinuxSyncEnabled)
	}
	if _, err := linux.service.ProbeDeviceSyncEntity(ctx, "macbook-main", steward.SyncEntityProbeInput{
		EntityType: steward.EntityTask,
		EntityID:   offlineTask.ID,
	}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("revoked Linux node retained access to macOS peer API: %v", err)
	}
}

func TestStewardDevicePermissionsFilterBothSyncDirectionsAndAdvanceCursor(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed device permission test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-device-permissions")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	windows := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "permission_windows"), "windows-main")
	mac := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "permission_mac"), "macbook-main")
	registerPeer(t, ctx, windows, "macbook-main", "MacBook Main", "darwin", mac.peerAPIBase)
	registerPeer(t, ctx, mac, "windows-main", "Windows Main", "windows", windows.peerAPIBase)
	if _, err := windows.service.UpdateDevicePermission(ctx, "macbook-main", "sync.memory", steward.UpdateDevicePermissionInput{
		Policy: "deny", MaxPermissionLevel: steward.PermissionA3, ScopeSummary: "test memory isolation",
	}); err != nil {
		t.Fatalf("deny macOS memory sync on Windows: %v", err)
	}

	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	windowsTask := createMeshTask(t, ctx, windows, "permission windows task "+nonce)
	macTask := createMeshTask(t, ctx, mac, "permission mac task "+nonce)
	windowsMemory := createMeshMemory(t, ctx, windows, "permission windows memory "+nonce)
	macMemory := createMeshMemory(t, ctx, mac, "permission mac memory "+nonce)
	var windowsSequenceBeforeSync int64
	if err := windows.pool.QueryRow(ctx, `select coalesce(max(sequence), 0) from steward_sync_changes`).Scan(&windowsSequenceBeforeSync); err != nil {
		t.Fatalf("read Windows pre-sync sequence: %v", err)
	}

	first := syncMeshPeer(t, ctx, mac, "windows-main")
	if first.Denied == 0 {
		t.Fatalf("incoming macOS memory was not denied: %+v", first)
	}
	if first.RemoteLastSequence < windowsSequenceBeforeSync {
		t.Fatalf("filtered pull cursor stopped at %d before scanned sequence %d", first.RemoteLastSequence, windowsSequenceBeforeSync)
	}
	assertTaskVisibleThroughHTTP(t, mac, windowsTask.ID, windowsTask.Title)
	assertTaskVisibleThroughHTTP(t, windows, macTask.ID, macTask.Title)
	assertMemoryAbsent(t, ctx, mac, windowsMemory.ID)
	assertMemoryAbsent(t, ctx, windows, macMemory.ID)

	second := syncMeshPeer(t, ctx, mac, "windows-main")
	third := syncMeshPeer(t, ctx, mac, "windows-main")
	if second.Denied != 0 || third.Denied != 0 || third.Pulled != 0 {
		t.Fatalf("denied changes were replayed after watermarks advanced: second=%+v third=%+v", second, third)
	}
	var deniedAudits int
	if err := windows.pool.QueryRow(ctx, `
		select count(*) from steward_audit_logs
		where action = 'sync.change.denied' and result_status = $1
	`, steward.ResultBlocked).Scan(&deniedAudits); err != nil {
		t.Fatalf("count denied sync audits: %v", err)
	}
	if deniedAudits == 0 {
		t.Fatalf("denied incoming sync change was not audited")
	}
}

func TestStewardDeviceRegistrationProtectsLocalIdentityThroughHTTP(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed device registration policy test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "device_registration_policy"), "windows-main")
	tests := []struct {
		name string
		body string
	}{
		{name: "overwrite local identity", body: `{"id":"windows-main","role":"peer","sync_enabled":false,"permission_level":"A1"}`},
		{name: "claim local role", body: `{"id":"macbook-main","role":"local"}`},
		{name: "unknown platform", body: `{"id":"macbook-main","platform":"macos"}`},
		{name: "invalid permission", body: `{"id":"macbook-main","permission_level":"root"}`},
		{name: "URL credentials", body: `{"id":"macbook-main","api_base_url":"https://user:pass@peer.example/api"}`},
		{name: "non peer API path", body: `{"id":"macbook-main","api_base_url":"https://peer.example/management"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := node.server.Client().Post(node.apiBase+"/steward/devices", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %s, want 400", response.Status)
			}
		})
	}

	var role, permission string
	var syncEnabled bool
	if err := node.pool.QueryRow(ctx, `
		select role, sync_enabled, permission_level from steward_devices where id = 'windows-main'
	`).Scan(&role, &syncEnabled, &permission); err != nil {
		t.Fatalf("read local device after rejected registrations: %v", err)
	}
	if role != steward.DeviceRoleLocal || !syncEnabled || permission != steward.PermissionA3 {
		t.Fatalf("rejected registration changed local identity: role=%s enabled=%t permission=%s", role, syncEnabled, permission)
	}
	var deviceCount int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_devices`).Scan(&deviceCount); err != nil {
		t.Fatalf("count devices after rejected registrations: %v", err)
	}
	if deviceCount != 1 {
		t.Fatalf("rejected registrations inserted devices: count=%d", deviceCount)
	}

	response, err := node.server.Client().Post(node.apiBase+"/steward/devices", "application/json", strings.NewReader(
		`{"id":"macbook-main","platform":"DARWIN","permission_level":"a2","api_base_url":"https://peer.example/api/"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("valid peer registration status = %s, want 201", response.Status)
	}
	var payload struct {
		Device domain.StewardDevice `json:"device"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode registered peer: %v", err)
	}
	if payload.Device.Role != steward.DeviceRolePeer || payload.Device.Platform != "darwin" ||
		payload.Device.PermissionLevel != steward.PermissionA2 || payload.Device.APIBaseURL != "https://peer.example/api" {
		t.Fatalf("registered peer was not canonicalized: %+v", payload.Device)
	}
}

func TestStewardSyncHeartbeatCannotExpandDeviceAuthorization(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward heartbeat authorization test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "heartbeat_auth"), "windows-main")
	disabled := false
	if _, err := node.service.RegisterDevice(ctx, steward.RegisterDeviceInput{
		ID: "linux-lab", DeviceName: "Linux Lab", Platform: "linux", Role: steward.DeviceRolePeer,
		SyncEnabled: &disabled, PermissionLevel: steward.PermissionA1, APIBaseURL: "http://old-peer.invalid/api",
	}); err != nil {
		t.Fatalf("register restricted peer: %v", err)
	}
	enabled := true
	if _, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: steward.RegisterDeviceInput{
			ID: "linux-lab", DeviceName: "Spoofed Linux", Platform: "linux", Role: steward.DeviceRoleLocal,
			SyncEnabled: &enabled, PermissionLevel: steward.PermissionA4, APIBaseURL: "http://new-peer.invalid/api",
		},
		Changes: []steward.CreateSyncChangeInput{},
	}); err != nil {
		t.Fatalf("import peer heartbeat: %v", err)
	}

	var role, trust, permission, apiBase string
	var syncEnabled bool
	if err := node.pool.QueryRow(ctx, `
		select role, trust_status, sync_enabled, permission_level, api_base_url
		from steward_devices where id = 'linux-lab'
	`).Scan(&role, &trust, &syncEnabled, &permission, &apiBase); err != nil {
		t.Fatalf("read observed peer: %v", err)
	}
	if role != steward.DeviceRolePeer || trust != steward.DeviceTrusted || syncEnabled || permission != steward.PermissionA1 {
		t.Fatalf("heartbeat expanded peer authorization: role=%s trust=%s enabled=%t permission=%s", role, trust, syncEnabled, permission)
	}
	if apiBase != "http://new-peer.invalid/api" {
		t.Fatalf("heartbeat did not refresh descriptive API metadata: %s", apiBase)
	}
}

func TestStewardSyncHTTPFailsClosedWithoutAuthenticationByDefault(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward fail-closed sync test")
	}

	t.Setenv("STEWARD_SYNC_SECRET", "")
	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "")
	t.Setenv("STEWARD_SYNC_ALLOW_INSECURE", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "sync_fail_closed"), "windows-main")

	response, err := node.peerServer.Client().Get(node.peerAPIBase + "/steward/sync/changes")
	if err != nil {
		t.Fatalf("request unsigned peer changes: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned peer request status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	probeResponse, err := node.peerServer.Client().Get(node.peerAPIBase + "/steward/sync/probe?entity_type=task&entity_id=missing")
	if err != nil {
		t.Fatalf("request unsigned peer probe: %v", err)
	}
	probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unsigned peer probe status = %d, want %d", probeResponse.StatusCode, http.StatusUnauthorized)
	}
	managementResponse, err := node.server.Client().Get(node.apiBase + "/steward/sync/status")
	if err != nil {
		t.Fatalf("request local sync status: %v", err)
	}
	managementResponse.Body.Close()
	if managementResponse.StatusCode != http.StatusOK {
		t.Fatalf("local sync status was blocked with %d", managementResponse.StatusCode)
	}

	t.Setenv("STEWARD_SYNC_ALLOW_INSECURE", "true")
	insecureResponse, err := node.peerServer.Client().Get(node.peerAPIBase + "/steward/sync/changes")
	if err != nil {
		t.Fatalf("request explicit insecure peer changes: %v", err)
	}
	insecureResponse.Body.Close()
	if insecureResponse.StatusCode != http.StatusOK {
		t.Fatalf("explicit insecure peer request status = %d, want %d", insecureResponse.StatusCode, http.StatusOK)
	}
}

func TestStewardSyncReplicatesSanitizedAuditSummaryWithoutEcho(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward audit summary test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-audit-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	windowsNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "windows_audit"), "windows-main")
	macNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mac_audit"), "macbook-main")
	registerPeer(t, ctx, windowsNode, "macbook-main", "MacBook Main", "darwin", macNode.peerAPIBase)
	registerPeer(t, ctx, macNode, "windows-main", "Windows Main", "windows", windowsNode.peerAPIBase)

	task, err := windowsNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title: "private local title must not enter audit summary input", Description: "local details",
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3, RiskLevel: "low",
	})
	if err != nil || task.AuditID == nil {
		t.Fatalf("create audited task: task=%+v err=%v", task, err)
	}
	if _, err := windowsNode.service.SyncDevice(ctx, "macbook-main"); err != nil {
		t.Fatalf("sync audit summary to peer: %v", err)
	}

	var action, inputSummary, outputSummary, deviceID string
	var syncable bool
	if err := macNode.pool.QueryRow(ctx, `
		select action, input_summary, output_summary, device_id, syncable
		from steward_audit_logs where id = $1
	`, *task.AuditID).Scan(&action, &inputSummary, &outputSummary, &deviceID, &syncable); err != nil {
		t.Fatalf("load remote audit summary: %v", err)
	}
	if action != "task.create" || inputSummary != "" || outputSummary != "task created" || deviceID != "windows-main" || syncable {
		t.Fatalf("remote audit summary is not sanitized: action=%s input=%q output=%q device=%s syncable=%t", action, inputSummary, outputSummary, deviceID, syncable)
	}
	var echoCount int
	if err := macNode.pool.QueryRow(ctx, `
		select count(*) from steward_sync_changes
		where entity_type = $1 and entity_id = $2 and origin_device_id = 'macbook-main'
	`, steward.EntityAuditSummary, *task.AuditID).Scan(&echoCount); err != nil || echoCount != 0 {
		t.Fatalf("remote audit summary produced a sync echo: count=%d err=%v", echoCount, err)
	}
}

func TestStewardSyncReplicatesDeviceCapabilitiesAndEnforcesOriginOwnership(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward device capability test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-capability-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	windowsNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "windows_capability"), "windows-main")
	macNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mac_capability"), "macbook-main")
	registerPeer(t, ctx, windowsNode, "macbook-main", "MacBook Main", "darwin", macNode.peerAPIBase)
	registerPeer(t, ctx, macNode, "windows-main", "Windows Main", "windows", windowsNode.peerAPIBase)

	if _, err := windowsNode.service.SyncDevice(ctx, "macbook-main"); err != nil {
		t.Fatalf("sync device capabilities to peer: %v", err)
	}
	status, err := macNode.service.GetSyncStatus(ctx)
	if err != nil {
		t.Fatalf("load sync status with capabilities: %v", err)
	}
	remoteCapabilityCount := 0
	for _, capability := range status.Capabilities {
		if capability.DeviceID == "windows-main" {
			remoteCapabilityCount++
		}
	}
	if remoteCapabilityCount != 6 {
		t.Fatalf("expected six Windows executor capabilities on Mac, got %d: %+v", remoteCapabilityCount, status.Capabilities)
	}
	staleSeenAt := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := macNode.pool.Exec(ctx, `update steward_devices set last_seen_at = $1 where id = 'windows-main'`, staleSeenAt); err != nil {
		t.Fatalf("make remote device heartbeat stale: %v", err)
	}
	idleSync, err := windowsNode.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("run idle peer sync heartbeat: %+v: %v", idleSync, err)
	}
	if idleSync.Pushed != 0 {
		t.Fatalf("idle heartbeat must not count as a business change push: %+v", idleSync)
	}
	var refreshedSeenAt time.Time
	if err := macNode.pool.QueryRow(ctx, `select last_seen_at from steward_devices where id = 'windows-main'`).Scan(&refreshedSeenAt); err != nil {
		t.Fatalf("load refreshed remote heartbeat: %v", err)
	}
	if !refreshedSeenAt.After(staleSeenAt) {
		t.Fatalf("idle sync did not refresh remote last_seen_at: stale=%s refreshed=%s", staleSeenAt, refreshedSeenAt)
	}

	capabilityName := "diagnostics.read"
	entityID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("steward-device-capability:windows-main:"+capabilityName)).String()
	spoofedPayload := map[string]any{
		"device_id": "linux-lab", "capability": capabilityName, "description": "read local diagnostics",
		"target_type": "diagnostics", "risk_level": "low", "max_permission_level": steward.PermissionA1,
		"version": 1, "updated_at": time.Now().UTC(),
	}
	imported, err := macNode.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: steward.RegisterDeviceInput{ID: "windows-main", DeviceName: "Windows Main", Platform: "windows"},
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityDeviceCapability, EntityID: entityID,
			Operation: steward.SyncUpdate, OriginDeviceID: "windows-main", Version: 1,
			DataLevel: steward.DataD0, Payload: spoofedPayload,
		}},
	})
	if err != nil || imported.Applied != 1 {
		t.Fatalf("import origin-owned capability: result=%+v err=%v", imported, err)
	}
	var owner string
	if err := macNode.pool.QueryRow(ctx, `
		select device_id from steward_device_capabilities where capability = $1
	`, capabilityName).Scan(&owner); err != nil {
		t.Fatalf("load imported capability owner: %v", err)
	}
	if owner != "windows-main" {
		t.Fatalf("payload device_id must not override origin ownership, got %s", owner)
	}
	var spoofedRows int
	if err := macNode.pool.QueryRow(ctx, `
		select count(*) from steward_device_capabilities where device_id = 'linux-lab' and capability = $1
	`, capabilityName).Scan(&spoofedRows); err != nil || spoofedRows != 0 {
		t.Fatalf("spoofed device capability was persisted: count=%d err=%v", spoofedRows, err)
	}

	spoofedPayload["description"] = "divergent declaration"
	conflicted, err := macNode.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: steward.RegisterDeviceInput{ID: "windows-main", DeviceName: "Windows Main", Platform: "windows"},
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityDeviceCapability, EntityID: entityID,
			Operation: steward.SyncUpdate, OriginDeviceID: "windows-main", Version: 1,
			DataLevel: steward.DataD0, Payload: spoofedPayload,
		}},
	})
	if err != nil || len(conflicted.Conflicts) != 1 {
		t.Fatalf("expected equal-version divergent capability conflict: result=%+v err=%v", conflicted, err)
	}
}

func TestStewardDaemonSyncsTrustedPeersInBackground(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward daemon sync integration test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-daemon-sync-e2e")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	windowsConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "windows_daemon")
	macConfig := temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mac_daemon")

	windowsNode := newStewardHTTPNode(t, ctx, windowsConfig, "windows-main")
	macNode := newStewardHTTPNode(t, ctx, macConfig, "macbook-main")

	registerPeer(t, ctx, windowsNode, "macbook-main", "MacBook Main", "darwin", macNode.peerAPIBase)
	registerPeer(t, ctx, macNode, "windows-main", "Windows Main", "windows", windowsNode.peerAPIBase)

	daemonCtx, stopDaemon := context.WithCancel(ctx)
	defer stopDaemon()
	daemon := steward.NewDaemon(windowsNode.service, steward.DaemonOptions{
		HeartbeatInterval: time.Hour,
		SyncInterval:      100 * time.Millisecond,
	})
	daemon.Start(daemonCtx)
	t.Cleanup(daemon.Stop)
	if !daemon.IsRunning() {
		t.Fatalf("expected steward daemon to be running")
	}

	nonce := strconv.FormatInt(time.Now().UnixNano(), 36)
	windowsTaskTitle := "steward daemon sync from windows " + nonce
	windowsTask, err := windowsNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           windowsTaskTitle,
		Description:     "created by S3 daemon background sync integration test",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create daemon windows task: %v", err)
	}
	waitForTaskVisibleThroughHTTP(t, ctx, macNode, windowsTask.ID, windowsTaskTitle)

	macTaskTitle := "steward daemon catch-up from mac " + nonce
	macTask, err := macNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           macTaskTitle,
		Description:     "created on peer while the windows daemon keeps running",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create daemon mac task: %v", err)
	}
	waitForTaskVisibleThroughHTTP(t, ctx, windowsNode, macTask.ID, macTaskTitle)
}

func TestStewardDaemonSyncFailureIsolatedFromLocalWorkAndRecovers(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward daemon failure isolation test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-daemon-isolation")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	windows := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "daemon_isolation_windows"), "windows-main")
	mac := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "daemon_isolation_mac"), "macbook-main")
	linux := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "daemon_isolation_linux"), "linux-lab")

	registerPeer(t, ctx, windows, "linux-lab", "Linux Lab", "linux", "http://127.0.0.1:1/api")
	registerPeer(t, ctx, windows, "macbook-main", "MacBook Main", "darwin", mac.peerAPIBase)
	registerPeer(t, ctx, mac, "windows-main", "Windows Main", "windows", windows.peerAPIBase)
	registerPeer(t, ctx, linux, "windows-main", "Windows Main", "windows", windows.peerAPIBase)

	daemonCtx, stopDaemon := context.WithCancel(ctx)
	defer stopDaemon()
	daemon := steward.NewDaemon(windows.service, steward.DaemonOptions{
		HeartbeatInterval: 25 * time.Millisecond,
		SyncInterval:      50 * time.Millisecond,
		AutonomyInterval:  50 * time.Millisecond,
	})
	daemon.Start(daemonCtx)
	t.Cleanup(daemon.Stop)

	task, err := windows.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           "sync failure isolation task " + strconv.FormatInt(time.Now().UnixNano(), 36),
		Description:     "healthy peer and local work must continue while another peer is offline",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create local task while peer is unavailable: %v", err)
	}
	if _, err := windows.service.CreateEvent(ctx, steward.CreateEventInput{
		Title:           "local event during sync degradation",
		Summary:         "local management writes remain available",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
	}); err != nil {
		t.Fatalf("create local event while peer is unavailable: %v", err)
	}
	waitForTaskVisibleThroughHTTP(t, ctx, mac, task.ID, task.Title)

	waitForStewardCondition(t, ctx, "degraded sync loop with healthy local loops", func() (bool, string) {
		agent, err := windows.service.GetAgentStatus(ctx)
		if err != nil {
			return false, err.Error()
		}
		loops := daemonLoopsByName(agent.BackgroundLoops)
		syncLoop := loops["sync"]
		heartbeatLoop := loops["heartbeat"]
		autonomyLoop := loops["autonomy"]
		ok := agent.Status == steward.StatusRunning && agent.LastError == nil &&
			syncLoop.Running && syncLoop.ConsecutiveFailures > 0 && syncLoop.LastError != nil &&
			heartbeatLoop.LastSuccessAt != nil && autonomyLoop.LastSuccessAt != nil
		return ok, fmt.Sprintf("agent=%+v loops=%+v", agent, loops)
	})
	devices, err := windows.service.ListDevices(ctx)
	if err != nil {
		t.Fatalf("list devices during sync degradation: %v", err)
	}
	linuxDevice := findStewardDevice(devices, "linux-lab")
	if linuxDevice == nil || linuxDevice.LastSyncError == nil {
		t.Fatalf("unavailable peer did not retain last_sync_error: %+v", linuxDevice)
	}

	registerPeer(t, ctx, windows, "linux-lab", "Linux Lab", "linux", linux.peerAPIBase)
	waitForTaskVisibleThroughHTTP(t, ctx, linux, task.ID, task.Title)
	waitForStewardCondition(t, ctx, "sync loop recovery", func() (bool, string) {
		agent, err := windows.service.GetAgentStatus(ctx)
		if err != nil {
			return false, err.Error()
		}
		devices, err := windows.service.ListDevices(ctx)
		if err != nil {
			return false, err.Error()
		}
		linuxDevice := findStewardDevice(devices, "linux-lab")
		syncLoop := daemonLoopsByName(agent.BackgroundLoops)["sync"]
		ok := linuxDevice != nil && linuxDevice.LastSyncError == nil && syncLoop.LastSuccessAt != nil && syncLoop.ConsecutiveFailures == 0 && syncLoop.LastError == nil
		return ok, fmt.Sprintf("device=%+v sync_loop=%+v", linuxDevice, syncLoop)
	})
}

func TestStewardDaemonStartIsIdempotentAndStopTerminatesAllLoops(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward daemon lifecycle test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "daemon_lifecycle"), "windows-main")
	parent, stopParent := context.WithCancel(ctx)
	defer stopParent()
	daemon := steward.NewDaemon(node.service, steward.DaemonOptions{HeartbeatInterval: 10 * time.Millisecond})
	daemon.Start(parent)
	daemon.Start(parent)
	if !daemon.IsRunning() {
		t.Fatalf("daemon did not report running after idempotent start")
	}

	stopped := make(chan struct{})
	go func() {
		daemon.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		stopParent()
		<-stopped
		t.Fatalf("daemon stop did not terminate all loops after duplicate start")
	}
	if daemon.IsRunning() {
		t.Fatalf("daemon still reports running after stop")
	}
}

func TestStewardSyncDetectsEqualVersionDivergenceWithoutOverwrite(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward conflict integration test")
	}

	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_SYNC_SECRET", "test-shared-secret-for-steward-conflict-e2e")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	windowsNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "windows_conflict"), "windows-main")
	macNode := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "mac_conflict"), "macbook-main")
	registerPeer(t, ctx, windowsNode, "macbook-main", "MacBook Main", "darwin", macNode.peerAPIBase)
	registerPeer(t, ctx, macNode, "windows-main", "Windows Main", "windows", windowsNode.peerAPIBase)

	task, err := windowsNode.service.CreateTask(ctx, steward.CreateTaskInput{
		Title:           "shared task before divergence",
		Description:     "same starting point",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
		RiskLevel:       "low",
	})
	if err != nil {
		t.Fatalf("create shared task: %v", err)
	}
	if _, err := windowsNode.service.SyncDevice(ctx, "macbook-main"); err != nil {
		t.Fatalf("seed task to peer: %v", err)
	}

	windowsTitle := "windows divergent title"
	macTitle := "mac divergent title"
	windowsTask, err := windowsNode.service.UpdateTask(ctx, task.ID, steward.UpdateTaskInput{Title: &windowsTitle})
	if err != nil {
		t.Fatalf("update windows task: %v", err)
	}
	macTask, err := macNode.service.UpdateTask(ctx, task.ID, steward.UpdateTaskInput{Title: &macTitle})
	if err != nil {
		t.Fatalf("update mac task: %v", err)
	}
	if windowsTask.Version != macTask.Version {
		t.Fatalf("test requires equal versions, windows=%d mac=%d", windowsTask.Version, macTask.Version)
	}

	result, err := windowsNode.service.SyncDevice(ctx, "macbook-main")
	if err != nil {
		t.Fatalf("sync divergent tasks: %+v: %v", result, err)
	}
	if len(result.Conflicts) == 0 || !strings.Contains(result.Conflicts[0].Reason, "same version but different content") {
		t.Fatalf("expected equal-version conflict, got %+v", result)
	}

	windowsTasks, err := windowsNode.service.ListTasks(ctx, 20)
	if err != nil {
		t.Fatalf("list windows tasks after conflict: %v", err)
	}
	if !hasTaskWithTitle(windowsTasks, task.ID, windowsTitle) {
		t.Fatalf("local task was overwritten after conflict: %+v", windowsTasks)
	}
	conflicts, err := windowsNode.service.ListSyncConflicts(ctx, steward.StatusOpen, 20)
	if err != nil || len(conflicts) == 0 {
		t.Fatalf("conflict queue is empty: conflicts=%+v err=%v", conflicts, err)
	}
}

func TestStewardSyncResolvesOutOfOrderTimelineEventLinks(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward out-of-order relation test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "timeline_relation"), "windows-main")
	segmentID := uuid.NewString()
	eventID := uuid.NewString()
	syncEnabled := true
	device := steward.RegisterDeviceInput{
		ID:              "macbook-main",
		DeviceName:      "MacBook Main",
		Platform:        "darwin",
		Role:            steward.DeviceRolePeer,
		SyncEnabled:     &syncEnabled,
		PermissionLevel: steward.PermissionA3,
	}

	timelineResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID:             uuid.NewString(),
			EntityType:     steward.EntityTimeline,
			EntityID:       segmentID,
			Operation:      steward.SyncCreate,
			OriginDeviceID: device.ID,
			Version:        1,
			DataLevel:      steward.DataD0,
			Payload: map[string]any{
				"type": "remote_cluster", "title": "out-of-order timeline", "summary": "event arrives later",
				"status": steward.StatusActive, "permission_level": steward.PermissionA3, "confidence": 1,
				"user_confirmed": true, "event_ids": []any{eventID},
			},
		}},
	})
	if err != nil || timelineResult.Applied != 1 {
		t.Fatalf("import timeline before event: result=%+v err=%v", timelineResult, err)
	}
	status, err := node.service.GetSyncStatus(ctx)
	if err != nil || status.PendingRelations != 1 {
		t.Fatalf("expected one pending timeline relation: status=%+v err=%v", status, err)
	}

	eventResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID:             uuid.NewString(),
			EntityType:     steward.EntityEvent,
			EntityID:       eventID,
			Operation:      steward.SyncCreate,
			OriginDeviceID: device.ID,
			Version:        1,
			DataLevel:      steward.DataD0,
			Payload: map[string]any{
				"type": "activity", "title": "late event", "summary": "resolves pending relation",
				"status": steward.StatusActive, "permission_level": steward.PermissionA3, "user_confirmed": true,
			},
		}},
	})
	if err != nil || eventResult.Applied != 1 {
		t.Fatalf("import late event: result=%+v err=%v", eventResult, err)
	}
	status, err = node.service.GetSyncStatus(ctx)
	if err != nil || status.PendingRelations != 0 {
		t.Fatalf("pending timeline relation was not resolved: status=%+v err=%v", status, err)
	}
	var linkedCount int
	if err := node.pool.QueryRow(ctx, `
		select count(*) from steward_timeline_segment_events where segment_id = $1 and event_id = $2
	`, segmentID, eventID).Scan(&linkedCount); err != nil || linkedCount != 1 {
		t.Fatalf("timeline event link was not created: count=%d err=%v", linkedCount, err)
	}
}

func TestStewardSyncMergesSameNameTagsByAliasAndConflictsOnMetadata(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward tag merge test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "tag_merge"), "windows-main")
	localTag, err := node.service.CreateDataTag(ctx, steward.CreateDataTagInput{
		Name: "project-alpha", Type: "project", Color: "#336699", Description: "shared project tag",
	})
	if err != nil {
		t.Fatalf("create local canonical tag: %v", err)
	}
	remoteTagID := uuid.NewString()
	entityID := uuid.NewString()
	syncEnabled := true
	device := steward.RegisterDeviceInput{
		ID: "macbook-main", DeviceName: "MacBook Main", Platform: "darwin", Role: steward.DeviceRolePeer,
		SyncEnabled: &syncEnabled, PermissionLevel: steward.PermissionA3,
	}

	mergeResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityDataTag, EntityID: remoteTagID, Operation: steward.SyncCreate,
			OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
			Payload: map[string]any{"name": localTag.Name, "type": localTag.Type, "color": localTag.Color, "description": localTag.Description},
		}},
	})
	if err != nil || mergeResult.Applied != 1 || len(mergeResult.Conflicts) != 0 {
		t.Fatalf("merge same-name tag: result=%+v err=%v", mergeResult, err)
	}
	var canonicalID string
	if err := node.pool.QueryRow(ctx, `select tag_id::text from steward_data_tag_aliases where alias_id = $1`, remoteTagID).Scan(&canonicalID); err != nil || canonicalID != localTag.ID {
		t.Fatalf("tag alias was not stored: canonical=%s err=%v", canonicalID, err)
	}

	entityTagResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityEntityTag, EntityID: uuid.NewString(), Operation: steward.SyncCreate,
			OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
			Payload: map[string]any{
				"entity_type": "task", "entity_id": entityID, "tag_id": remoteTagID, "tag_name": localTag.Name,
				"tag_type": localTag.Type, "tag_color": localTag.Color, "tag_description": localTag.Description,
				"source": "sync", "confidence": 1,
			},
		}},
	})
	if err != nil || entityTagResult.Applied != 1 {
		t.Fatalf("apply entity tag through alias: result=%+v err=%v", entityTagResult, err)
	}
	var assignedTagID string
	if err := node.pool.QueryRow(ctx, `
		select tag_id::text from steward_entity_tags where entity_type = 'task' and entity_id = $1
	`, entityID).Scan(&assignedTagID); err != nil || assignedTagID != localTag.ID {
		t.Fatalf("entity tag did not resolve to canonical id: id=%s err=%v", assignedTagID, err)
	}

	conflictTagID := uuid.NewString()
	conflictResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityDataTag, EntityID: conflictTagID, Operation: steward.SyncCreate,
			OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
			Payload: map[string]any{"name": localTag.Name, "type": localTag.Type, "color": "#ff0000", "description": localTag.Description},
		}},
	})
	if err != nil || len(conflictResult.Conflicts) != 1 || conflictResult.Applied != 0 {
		t.Fatalf("expected metadata conflict for same-name tag: result=%+v err=%v", conflictResult, err)
	}
	var localColor string
	if err := node.pool.QueryRow(ctx, `select color from steward_data_tags where id = $1`, localTag.ID).Scan(&localColor); err != nil || localColor != localTag.Color {
		t.Fatalf("canonical tag metadata was overwritten: color=%s err=%v", localColor, err)
	}
}

func TestStewardSyncReplaysSourceRefsAndEntityTagsAcrossOutOfOrderDeletes(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward relation replay test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "source_tag_replay"), "windows-main")
	syncEnabled := true
	device := steward.RegisterDeviceInput{
		ID: "macbook-main", DeviceName: "MacBook Main", Platform: "darwin", Role: steward.DeviceRolePeer,
		SyncEnabled: &syncEnabled, PermissionLevel: steward.PermissionA3,
	}
	taskID := uuid.NewString()
	sourceRefID := uuid.NewString()
	remoteTagID := uuid.NewString()
	entityTagID := uuid.NewString()

	relationResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{
			{
				ID: uuid.NewString(), EntityType: steward.EntitySourceRef, EntityID: sourceRefID, Operation: steward.SyncCreate,
				OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
				Payload: map[string]any{
					"target_type": "task", "target_id": taskID, "source_type": "remote_doc", "source_id": "doc-1",
					"location": "p1", "summary": "remote source before target", "confidence": 0.9,
					"sensitive": false, "displayable": true,
				},
			},
			{
				ID: uuid.NewString(), EntityType: steward.EntityEntityTag, EntityID: entityTagID, Operation: steward.SyncCreate,
				OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
				Payload: map[string]any{
					"entity_type": "task", "entity_id": taskID, "tag_id": remoteTagID, "tag_name": "remote-project",
					"tag_type": "project", "tag_color": "#336699", "tag_description": "remote project tag",
					"source": "sync", "confidence": 0.8,
				},
			},
		},
	})
	if err != nil || relationResult.Applied != 2 {
		t.Fatalf("import relation changes before target: result=%+v err=%v", relationResult, err)
	}

	taskResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{{
			ID: uuid.NewString(), EntityType: steward.EntityTask, EntityID: taskID, Operation: steward.SyncCreate,
			OriginDeviceID: device.ID, Version: 1, DataLevel: steward.DataD0,
			Payload: map[string]any{
				"type": "manual", "title": "target arrives after refs", "description": "out-of-order replay",
				"status": steward.StatusOpen, "priority": "normal", "permission_level": steward.PermissionA3,
				"risk_level": "low", "user_confirmed": true,
			},
		}},
	})
	if err != nil || taskResult.Applied != 1 {
		t.Fatalf("import target task: result=%+v err=%v", taskResult, err)
	}

	sourceRefs, err := node.service.ListSourceRefs(ctx, "task", taskID, 10)
	if err != nil || len(sourceRefs) != 1 || sourceRefs[0].ID != sourceRefID {
		t.Fatalf("source ref was not replayed against target: refs=%+v err=%v", sourceRefs, err)
	}
	var assignedTagID string
	if err := node.pool.QueryRow(ctx, `
		select tag_id::text from steward_entity_tags where entity_type = 'task' and entity_id = $1
	`, taskID).Scan(&assignedTagID); err != nil || assignedTagID != remoteTagID {
		t.Fatalf("entity tag was not replayed with auto-created tag: tag=%s err=%v", assignedTagID, err)
	}

	deleteResult, err := node.service.ImportSyncChanges(ctx, steward.ImportSyncChangesInput{
		Device: device,
		Changes: []steward.CreateSyncChangeInput{
			{
				ID: uuid.NewString(), EntityType: steward.EntitySourceRef, EntityID: sourceRefID, Operation: steward.SyncDelete,
				OriginDeviceID: device.ID, Version: 2, DataLevel: steward.DataD0,
				Payload: map[string]any{"target_type": "task", "target_id": taskID},
			},
			{
				ID: uuid.NewString(), EntityType: steward.EntityEntityTag, EntityID: entityTagID, Operation: steward.SyncDelete,
				OriginDeviceID: device.ID, Version: 2, DataLevel: steward.DataD0,
				Payload: map[string]any{"entity_type": "task", "entity_id": taskID, "tag_id": remoteTagID, "tag_name": "remote-project"},
			},
		},
	})
	if err != nil || deleteResult.Applied != 2 {
		t.Fatalf("delete replayed relations: result=%+v err=%v", deleteResult, err)
	}
	sourceRefs, err = node.service.ListSourceRefs(ctx, "task", taskID, 10)
	if err != nil || len(sourceRefs) != 0 {
		t.Fatalf("source ref delete was not applied: refs=%+v err=%v", sourceRefs, err)
	}
	var entityTagCount int
	if err := node.pool.QueryRow(ctx, `
		select count(*) from steward_entity_tags where entity_type = 'task' and entity_id = $1
	`, taskID).Scan(&entityTagCount); err != nil || entityTagCount != 0 {
		t.Fatalf("entity tag delete was not applied: count=%d err=%v", entityTagCount, err)
	}
}

func TestStewardAutonomyProposalScoresPersistAndOrderThroughHTTP(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward autonomy integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_score"), "windows-main")

	overview, err := node.service.GetAutonomyOverview(ctx)
	if err != nil || len(overview.Rules) == 0 {
		t.Fatalf("load autonomy rules: rules=%d err=%v", len(overview.Rules), err)
	}
	ruleID := overview.Rules[0].ID
	event, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Title:           "scored event",
		Summary:         "event context",
		Source:          "verification",
		DataLevel:       steward.DataD0,
		PermissionLevel: steward.PermissionA3,
	})
	if err != nil {
		t.Fatalf("create score source event: %v", err)
	}

	sparse, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{Title: "sparse candidate"})
	if err != nil {
		t.Fatalf("create sparse proposal: %v", err)
	}
	rich, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		RuleID:           &ruleID,
		SourceEntityType: "event",
		SourceEntityID:   &event.ID,
		Title:            "rich candidate",
		Summary:          "candidate context",
		TriggerReason:    "event needs a follow-up",
		SuggestedAction:  "create a local follow-up task",
		RiskLevel:        "low",
		PermissionLevel:  steward.PermissionA3,
		DataLevel:        steward.DataD0,
		Policy:           steward.AutonomyPolicyConfirm,
	})
	if err != nil {
		t.Fatalf("create rich proposal: %v", err)
	}
	if rich.Score <= sparse.Score || strings.TrimSpace(rich.ScoreReason) == "" {
		t.Fatalf("unexpected proposal scores rich=%+v sparse=%+v", rich, sparse)
	}

	response, err := node.server.Client().Get(node.apiBase + "/steward/autonomy")
	if err != nil {
		t.Fatalf("get autonomy overview: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("autonomy status = %s", response.Status)
	}
	var payload struct {
		Autonomy domain.StewardAutonomyOverview `json:"autonomy"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode autonomy overview: %v", err)
	}
	if len(payload.Autonomy.Proposals) < 2 || payload.Autonomy.Proposals[0].ID != rich.ID {
		t.Fatalf("proposals are not ordered by score: %+v", payload.Autonomy.Proposals)
	}
}

func TestStewardAutonomyControlRejectsInvalidValuesThroughHTTP(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward autonomy validation test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_validation"), "windows-main")
	settingsBefore, err := node.service.GetAutonomySettings(ctx)
	if err != nil {
		t.Fatalf("load autonomy settings: %v", err)
	}
	rulesBefore, err := node.service.ListAutonomyRules(ctx)
	if err != nil || len(rulesBefore) == 0 {
		t.Fatalf("load autonomy rules: rules=%d err=%v", len(rulesBefore), err)
	}
	ruleBefore := rulesBefore[0]
	var proposalsBefore int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_autonomy_proposals`).Scan(&proposalsBefore); err != nil {
		t.Fatalf("count autonomy proposals: %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "unknown mode", method: http.MethodPatch, path: "/steward/autonomy/settings", body: `{"mode":"automatic"}`},
		{name: "unknown automatic permission", method: http.MethodPatch, path: "/steward/autonomy/settings", body: `{"max_auto_permission":"A10"}`},
		{name: "unknown rule policy", method: http.MethodPatch, path: "/steward/autonomy/rules/" + ruleBefore.ID, body: `{"policy":"allow"}`},
		{name: "unknown proposal risk", method: http.MethodPost, path: "/steward/autonomy/proposals", body: `{"title":"invalid","risk_level":"unknown"}`},
		{name: "unknown proposal data level", method: http.MethodPost, path: "/steward/autonomy/proposals", body: `{"title":"invalid","data_level":"secret"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(ctx, tt.method, node.apiBase+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := node.server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %s, want 400", response.Status)
			}
		})
	}

	settingsAfter, err := node.service.GetAutonomySettings(ctx)
	if err != nil {
		t.Fatalf("reload autonomy settings: %v", err)
	}
	if settingsAfter.Mode != settingsBefore.Mode || settingsAfter.MaxAutoPermission != settingsBefore.MaxAutoPermission || settingsAfter.UpdatedAt != settingsBefore.UpdatedAt {
		t.Fatalf("invalid settings request mutated state: before=%+v after=%+v", settingsBefore, settingsAfter)
	}
	rulesAfter, err := node.service.ListAutonomyRules(ctx)
	if err != nil {
		t.Fatalf("reload autonomy rules: %v", err)
	}
	var ruleAfter domain.StewardAutonomyRule
	for _, rule := range rulesAfter {
		if rule.ID == ruleBefore.ID {
			ruleAfter = rule
			break
		}
	}
	if ruleAfter.ID == "" {
		t.Fatalf("autonomy rule %s disappeared", ruleBefore.ID)
	}
	if ruleAfter.Policy != ruleBefore.Policy || ruleAfter.MaxPermissionLevel != ruleBefore.MaxPermissionLevel || ruleAfter.UpdatedAt != ruleBefore.UpdatedAt {
		t.Fatalf("invalid rule request mutated state: before=%+v after=%+v", ruleBefore, ruleAfter)
	}
	var proposalsAfter int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_autonomy_proposals`).Scan(&proposalsAfter); err != nil {
		t.Fatalf("count autonomy proposals after rejection: %v", err)
	}
	if proposalsAfter != proposalsBefore {
		t.Fatalf("invalid proposal request inserted rows: before=%d after=%d", proposalsBefore, proposalsAfter)
	}

	mediumBody := `{"title":"medium-risk plan","action":"create_local_task","risk_level":"medium","permission_level":"A3","data_level":"D0","policy":"auto"}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, node.apiBase+"/steward/autonomy/proposals", strings.NewReader(mediumBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := node.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("medium-risk proposal status = %s, want 201", response.Status)
	}
	var mediumPayload struct {
		Proposal domain.StewardAutonomyProposal `json:"proposal"`
	}
	if err := json.NewDecoder(response.Body).Decode(&mediumPayload); err != nil {
		t.Fatalf("decode medium-risk proposal: %v", err)
	}
	if mediumPayload.Proposal.Status != steward.ProposalBlocked {
		t.Fatalf("medium-risk auto proposal was not blocked: %+v", mediumPayload.Proposal)
	}
	var approvalCount int
	if err := node.pool.QueryRow(ctx, `
		select count(*) from steward_approval_requests
		where proposal_id = $1 and status = $2
	`, mediumPayload.Proposal.ID, steward.ApprovalPending).Scan(&approvalCount); err != nil {
		t.Fatalf("count medium-risk approval requests: %v", err)
	}
	if approvalCount != 1 {
		t.Fatalf("medium-risk proposal approval count = %d, want 1", approvalCount)
	}

	legacyProposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Title: "legacy invalid policy", Action: steward.AutonomyActionCreateLocalTask,
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0,
		Policy: steward.AutonomyPolicyConfirm,
	})
	if err != nil {
		t.Fatalf("create legacy policy probe: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `
		update steward_autonomy_proposals set policy = 'allow', status = $1 where id = $2
	`, steward.ProposalApproved, legacyProposal.ID); err != nil {
		t.Fatalf("inject legacy invalid proposal policy: %v", err)
	}
	legacyRun := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+legacyProposal.ID+"/execute")
	if legacyRun.Status != steward.RunBlocked || !strings.Contains(legacyRun.ImpactSummary, "invalid persisted") {
		t.Fatalf("legacy invalid proposal was not blocked: %+v", legacyRun)
	}
	var legacyStatus, legacyTargetID string
	if err := node.pool.QueryRow(ctx, `
		select status, execution_target_id from steward_autonomy_proposals where id = $1
	`, legacyProposal.ID).Scan(&legacyStatus, &legacyTargetID); err != nil {
		t.Fatalf("read legacy invalid proposal result: %v", err)
	}
	if legacyStatus != steward.ProposalBlocked || legacyTargetID != "" {
		t.Fatalf("legacy invalid proposal execution state: status=%s target=%s", legacyStatus, legacyTargetID)
	}
}

func TestStewardAutonomyActionExecutorRunsThroughHTTP(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed steward action executor integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_executor"), "windows-main")

	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action:          steward.AutonomyActionCreateReviewChecklist,
		Title:           "review project status",
		Summary:         "create a local review checklist",
		TriggerReason:   "project has not been reviewed",
		SuggestedAction: "create local checklist",
		RiskLevel:       "low",
		PermissionLevel: steward.PermissionA3,
		DataLevel:       steward.DataD0,
		Policy:          steward.AutonomyPolicyAuto,
		ImpactSummary:   "only creates a local task",
	})
	if err != nil {
		t.Fatalf("create executor proposal: %v", err)
	}
	if proposal.Action != steward.AutonomyActionCreateReviewChecklist || proposal.Status != steward.ProposalCandidate {
		t.Fatalf("unexpected proposal action snapshot: %+v", proposal)
	}

	simulated := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+proposal.ID+"/simulate")
	if simulated.Status != steward.RunSuccess || !strings.Contains(simulated.ImpactSummary, "local") {
		t.Fatalf("unexpected simulation run: %+v", simulated)
	}
	executed := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+proposal.ID+"/execute")
	if executed.Status != steward.RunSuccess {
		t.Fatalf("unexpected execution run: %+v", executed)
	}
	var firstTargetID string
	if err := node.pool.QueryRow(ctx, `select execution_target_id from steward_autonomy_proposals where id = $1`, proposal.ID).Scan(&firstTargetID); err != nil {
		t.Fatalf("load first execution target: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `
		update steward_autonomy_proposals
		set status = $1, created_task_id = null, execution_target_type = '', execution_target_id = ''
		where id = $2
	`, steward.ProposalApproved, proposal.ID); err != nil {
		t.Fatalf("simulate lost proposal execution update: %v", err)
	}
	retried := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+proposal.ID+"/execute")
	if retried.Status != steward.RunSuccess {
		t.Fatalf("unexpected recovered execution run: %+v", retried)
	}
	var recoveredTargetID string
	var targetTaskCount int
	if err := node.pool.QueryRow(ctx, `select execution_target_id from steward_autonomy_proposals where id = $1`, proposal.ID).Scan(&recoveredTargetID); err != nil {
		t.Fatalf("load recovered execution target: %v", err)
	}
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tasks where id = $1`, firstTargetID).Scan(&targetTaskCount); err != nil {
		t.Fatalf("count idempotent target tasks: %v", err)
	}
	if recoveredTargetID != firstTargetID || targetTaskCount != 1 {
		t.Fatalf("execution retry was not idempotent: first=%s recovered=%s count=%d", firstTargetID, recoveredTargetID, targetTaskCount)
	}

	overview, err := node.service.GetAutonomyOverview(ctx)
	if err != nil {
		t.Fatalf("load autonomy overview after execution: %v", err)
	}
	if !hasAutonomyActionCapability(overview.Actions, steward.AutonomyActionCreateReviewChecklist) {
		t.Fatalf("review checklist executor capability is not published: %+v", overview.Actions)
	}
	var executedProposal *domain.StewardAutonomyProposal
	for index := range overview.Proposals {
		if overview.Proposals[index].ID == proposal.ID {
			executedProposal = &overview.Proposals[index]
			break
		}
	}
	if executedProposal == nil || executedProposal.Status != steward.ProposalExecuted || executedProposal.ExecutionTargetType != "task" || executedProposal.ExecutionTargetID == "" || executedProposal.CreatedTaskID == nil {
		t.Fatalf("execution target was not persisted: %+v", executedProposal)
	}
	tasks, err := node.service.ListTasks(ctx, 20)
	if err != nil {
		t.Fatalf("list tasks after autonomy execution: %v", err)
	}
	if !hasTaskID(tasks, executedProposal.ExecutionTargetID) {
		t.Fatalf("executed target task %s is not present", executedProposal.ExecutionTargetID)
	}
}

func TestStewardAutonomyAutomaticFailureBacksOffAndManualRetryRecovers(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed autonomy retry integration test")
	}

	t.Setenv("STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS", "2")
	t.Setenv("STEWARD_AUTONOMY_RETRY_BACKOFF", "1h")
	t.Setenv("STEWARD_AUTONOMY_RETRY_MAX_BACKOFF", "1h")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	executor := &flakyAutonomyExecutor{failUntil: 2}
	node := newStewardHTTPNode(
		t,
		ctx,
		temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_retry"),
		"windows-main",
		steward.WithAutonomyActionExecutor(executor),
	)

	overview, err := node.service.GetAutonomyOverview(ctx)
	if err != nil {
		t.Fatalf("load autonomy overview: %v", err)
	}
	rule := findAutonomyRuleByName(overview.Rules, "event-follow-up-candidate")
	if rule == nil {
		t.Fatal("event follow-up rule not found")
	}
	autoPolicy := steward.AutonomyPolicyAuto
	enabled := true
	if _, err := node.service.UpdateAutonomyRule(ctx, rule.ID, steward.UpdateAutonomyRuleInput{Policy: &autoPolicy, Enabled: &enabled}); err != nil {
		t.Fatalf("enable automatic rule: %v", err)
	}
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Mode: steward.AutonomyModeControlled}); err != nil {
		t.Fatalf("enable controlled autonomy mode: %v", err)
	}
	sourceID := uuid.NewString()
	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		RuleID:           &rule.ID,
		SourceEntityType: "verification",
		SourceEntityID:   &sourceID,
		Action:           steward.AutonomyActionCreateFollowUpTask,
		Title:            "retry recovery probe",
		TriggerReason:    "verify bounded automatic retries",
		RiskLevel:        "low",
		PermissionLevel:  steward.PermissionA3,
		DataLevel:        steward.DataD0,
		Policy:           steward.AutonomyPolicyAuto,
		ImpactSummary:    "test executor only",
	})
	if err != nil {
		t.Fatalf("create retry proposal: %v", err)
	}

	if _, err := node.service.RunAutonomyCycle(ctx, 10); err != nil {
		t.Fatalf("run first autonomy cycle: %v", err)
	}
	if executor.Calls() != 1 {
		t.Fatalf("first autonomy cycle executor calls = %d, want 1", executor.Calls())
	}
	proposals, err := node.service.ListAutonomyProposals(ctx, "", 20)
	if err != nil {
		t.Fatalf("list proposals after first failure: %v", err)
	}
	retrying := findAutonomyProposalByID(proposals, proposal.ID)
	if retrying == nil || retrying.FailedAttempts != 1 || !retrying.RetryEligible || retrying.RetryExhausted || retrying.AutoRetryAt == nil || retrying.Status != steward.ProposalCandidate {
		t.Fatalf("unexpected first retry state: %+v", retrying)
	}

	if _, err := node.service.RunAutonomyCycle(ctx, 10); err != nil {
		t.Fatalf("run backoff autonomy cycle: %v", err)
	}
	if executor.Calls() != 1 {
		t.Fatalf("backoff cycle retried executor, calls = %d", executor.Calls())
	}

	failedRetry := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+proposal.ID+"/retry")
	if failedRetry.Status != steward.RunFailed || executor.Calls() != 2 {
		t.Fatalf("unexpected exhausted retry: run=%+v calls=%d", failedRetry, executor.Calls())
	}
	proposals, err = node.service.ListAutonomyProposals(ctx, "", 20)
	if err != nil {
		t.Fatalf("list proposals after exhausted retry: %v", err)
	}
	exhausted := findAutonomyProposalByID(proposals, proposal.ID)
	if exhausted == nil || exhausted.Status != steward.ProposalBlocked || exhausted.FailedAttempts != 2 || !exhausted.RetryExhausted || !exhausted.RetryEligible || exhausted.AutoRetryAt != nil {
		t.Fatalf("unexpected exhausted retry state: %+v", exhausted)
	}

	executor.SetFailUntil(2)
	recovered := postAutonomyRun(t, node, "/steward/autonomy/proposals/"+proposal.ID+"/retry")
	if recovered.Status != steward.RunSuccess || executor.Calls() != 3 {
		t.Fatalf("unexpected manual recovery: run=%+v calls=%d", recovered, executor.Calls())
	}
	proposals, err = node.service.ListAutonomyProposals(ctx, "", 20)
	if err != nil {
		t.Fatalf("list proposals after recovery: %v", err)
	}
	completed := findAutonomyProposalByID(proposals, proposal.ID)
	if completed == nil || completed.Status != steward.ProposalExecuted || completed.RetryEligible {
		t.Fatalf("unexpected recovered proposal state: %+v", completed)
	}
	var retryAudits int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_audit_logs where action = 'autonomy.retry' and target_id = $1`, proposal.ID).Scan(&retryAudits); err != nil {
		t.Fatalf("count manual retry audits: %v", err)
	}
	if retryAudits != 2 {
		t.Fatalf("manual retry audit count = %d, want 2", retryAudits)
	}
}

func TestStewardAutonomyExecutionLeaseSerializesConcurrentAttempts(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed autonomy concurrency test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_concurrency"), "windows-main")
	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: steward.AutonomyActionCreateLocalTask, Title: "concurrent autonomy execution",
		TriggerReason: "verify exactly-once proposal execution", SuggestedAction: "create one local task",
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0,
		Policy: steward.AutonomyPolicyAuto, ImpactSummary: "creates one deterministic local task",
	})
	if err != nil {
		t.Fatalf("create concurrent proposal: %v", err)
	}

	const attempts = 8
	start := make(chan struct{})
	runs := make(chan domain.StewardAutonomousRun, attempts)
	errors := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			run, err := node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
			if err != nil {
				errors <- err
				return
			}
			runs <- run
		}()
	}
	close(start)
	wait.Wait()
	close(runs)
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent execution failed: %v", err)
	}

	success, blocked := 0, 0
	for run := range runs {
		switch run.Status {
		case steward.RunSuccess:
			success++
		case steward.RunBlocked:
			blocked++
		default:
			t.Fatalf("unexpected concurrent run status: %+v", run)
		}
	}
	if success != 1 || blocked != attempts-1 {
		t.Fatalf("concurrent runs success=%d blocked=%d, want 1/%d", success, blocked, attempts-1)
	}

	var successfulRuns, targetTasks, successAudits int
	var targetID string
	if err := node.pool.QueryRow(ctx, `
		select execution_target_id from steward_autonomy_proposals where id = $1 and status = $2
	`, proposal.ID, steward.ProposalExecuted).Scan(&targetID); err != nil {
		t.Fatalf("load concurrently executed proposal: %v", err)
	}
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_autonomous_runs where proposal_id = $1 and status = $2`, proposal.ID, steward.RunSuccess).Scan(&successfulRuns); err != nil {
		t.Fatalf("count successful concurrent runs: %v", err)
	}
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_tasks where id = $1`, targetID).Scan(&targetTasks); err != nil {
		t.Fatalf("count concurrent target tasks: %v", err)
	}
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_audit_logs where action = 'autonomy.execute' and target_id = $1`, targetID).Scan(&successAudits); err != nil {
		t.Fatalf("count successful autonomy audits: %v", err)
	}
	if successfulRuns != 1 || targetTasks != 1 || successAudits != 1 {
		t.Fatalf("exactly-once evidence runs=%d tasks=%d audits=%d", successfulRuns, targetTasks, successAudits)
	}
}

func TestStewardConcurrentHighRiskProposalKeepsSinglePendingApproval(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed approval concurrency test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "approval_concurrency"), "windows-main")
	rules, err := node.service.ListAutonomyRules(ctx)
	if err != nil {
		t.Fatalf("list autonomy rules: %v", err)
	}
	var guardrailRuleID string
	for _, rule := range rules {
		if rule.Name == "high-risk-guardrail" {
			guardrailRuleID = rule.ID
			break
		}
	}
	if guardrailRuleID == "" {
		t.Fatalf("high-risk guardrail rule is missing")
	}
	sourceID := uuid.NewString()
	input := steward.CreateAutonomyProposalInput{
		RuleID: &guardrailRuleID, SourceEntityType: "event", SourceEntityID: &sourceID,
		Action: steward.AutonomyActionCreateLocalTask, Title: "high-risk concurrent candidate",
		TriggerReason: "requires manual high-risk review", RiskLevel: "high",
		PermissionLevel: steward.PermissionA4, DataLevel: steward.DataD2,
		Policy: steward.AutonomyPolicyNever, ImpactSummary: "plan only",
	}

	const attempts = 8
	start := make(chan struct{})
	proposalIDs := make(chan string, attempts)
	errors := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			proposal, err := node.service.CreateAutonomyProposal(ctx, input)
			if err != nil {
				errors <- err
				return
			}
			proposalIDs <- proposal.ID
		}()
	}
	close(start)
	wait.Wait()
	close(proposalIDs)
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent high-risk proposal failed: %v", err)
	}
	uniqueIDs := map[string]struct{}{}
	for id := range proposalIDs {
		uniqueIDs[id] = struct{}{}
	}
	if len(uniqueIDs) != 1 {
		t.Fatalf("concurrent upsert produced %d proposal IDs: %#v", len(uniqueIDs), uniqueIDs)
	}
	var proposalID string
	for id := range uniqueIDs {
		proposalID = id
	}
	var pendingApprovals int
	if err := node.pool.QueryRow(ctx, `
		select count(*) from steward_approval_requests
		where proposal_id = $1 and requested_action = 'review blocked autonomous proposal' and status = $2
	`, proposalID, steward.ApprovalPending).Scan(&pendingApprovals); err != nil {
		t.Fatalf("count concurrent pending approvals: %v", err)
	}
	if pendingApprovals != 1 {
		t.Fatalf("pending approval count = %d, want 1", pendingApprovals)
	}
	if _, err := node.service.ApproveAutonomyProposal(ctx, proposalID); err == nil {
		t.Fatalf("blocked high-risk proposal was directly approved")
	}
	approvals, err := node.service.ListApprovalRequests(ctx, steward.ApprovalPending, 20)
	if err != nil {
		t.Fatalf("list high-risk approval requests: %v", err)
	}
	var reviewApprovalID string
	for _, approval := range approvals {
		if approval.ProposalID != nil && *approval.ProposalID == proposalID {
			reviewApprovalID = approval.ID
			break
		}
	}
	if reviewApprovalID == "" {
		t.Fatalf("high-risk review approval is missing")
	}
	if _, err := node.service.ApproveRequest(ctx, reviewApprovalID, steward.DecideApprovalInput{DecisionReason: "reviewed plan only"}); err != nil {
		t.Fatalf("approve high-risk manual review: %v", err)
	}
	highRiskProposal, err := node.service.GetAutonomyOverview(ctx)
	if err != nil {
		t.Fatalf("load high-risk proposal after review: %v", err)
	}
	var reviewedStatus string
	for _, proposal := range highRiskProposal.Proposals {
		if proposal.ID == proposalID {
			reviewedStatus = proposal.Status
			break
		}
	}
	if reviewedStatus != steward.ProposalBlocked {
		t.Fatalf("manual high-risk review changed proposal to %q, want blocked", reviewedStatus)
	}
	run, err := node.service.ExecuteAutonomyProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("execute blocked high-risk proposal: %v", err)
	}
	if run.Status != steward.RunBlocked {
		t.Fatalf("high-risk execution status = %s, want blocked", run.Status)
	}
	var executionTarget string
	if err := node.pool.QueryRow(ctx, `select execution_target_id from steward_autonomy_proposals where id = $1`, proposalID).Scan(&executionTarget); err != nil {
		t.Fatalf("read high-risk execution target: %v", err)
	}
	if executionTarget != "" {
		t.Fatalf("high-risk review created execution target %s", executionTarget)
	}
}

func TestStewardAutonomyPauseBlocksScanningAndExecution(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed autonomy pause test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_pause"), "windows-main")
	paused := true
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Paused: &paused}); err != nil {
		t.Fatalf("pause autonomy: %v", err)
	}
	event, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Title: "paused autonomy event", Summary: "must not create proposals while paused", Source: "verification",
		DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3, UserConfirmed: boolPointerValue(true),
	})
	if err != nil {
		t.Fatalf("create paused event: %v", err)
	}
	overview, err := node.service.RunAutonomyCycle(ctx, 20)
	if err != nil {
		t.Fatalf("run paused autonomy cycle: %v", err)
	}
	for _, candidate := range overview.Proposals {
		if candidate.SourceEntityID != nil && *candidate.SourceEntityID == event.ID {
			t.Fatalf("paused cycle created proposal %+v", candidate)
		}
	}

	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: steward.AutonomyActionCreateLocalTask, Title: "paused execution candidate",
		TriggerReason: "verify pause gate", RiskLevel: "low", PermissionLevel: steward.PermissionA3,
		DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto, ImpactSummary: "creates one local task",
	})
	if err != nil {
		t.Fatalf("create manual pause candidate: %v", err)
	}
	run, err := node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
	if err != nil {
		t.Fatalf("execute while paused: %v", err)
	}
	if run.Status != steward.RunBlocked || !strings.Contains(run.ImpactSummary, "paused") {
		t.Fatalf("paused execution was not blocked: %+v", run)
	}
	var targetID string
	if err := node.pool.QueryRow(ctx, `select execution_target_id from steward_autonomy_proposals where id = $1`, proposal.ID).Scan(&targetID); err != nil {
		t.Fatalf("read paused execution target: %v", err)
	}
	if targetID != "" {
		t.Fatalf("paused execution created target %s", targetID)
	}

	paused = false
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Paused: &paused}); err != nil {
		t.Fatalf("resume autonomy: %v", err)
	}
	run, err = node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
	if err != nil || run.Status != steward.RunSuccess {
		t.Fatalf("execute after resume: run=%+v err=%v", run, err)
	}
}

func TestStewardAutonomyPolicyGateLinearizesPauseWithInFlightWork(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the autonomy policy gate test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	discoverer := newBlockingAutonomyDiscoverer()
	executor := newBlockingAutonomyExecutor()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_policy_gate"), "windows-main",
		steward.WithAutonomyProposalDiscoverer(discoverer),
		steward.WithAutonomyActionExecutor(executor),
	)

	cycleDone := make(chan error, 1)
	go func() {
		_, err := node.service.RunAutonomyCycle(ctx, 10)
		cycleDone <- err
	}()
	waitForSignal(t, ctx, discoverer.started, "autonomy discoverer start")
	pauseDone := make(chan error, 1)
	paused := true
	go func() {
		_, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Paused: &paused})
		pauseDone <- err
	}()
	assertNoSignal(t, pauseDone, 150*time.Millisecond, "pause returned while discovery was still active")
	close(discoverer.release)
	if err := <-cycleDone; err != nil {
		t.Fatalf("finish in-flight autonomy cycle: %v", err)
	}
	if err := <-pauseDone; err != nil {
		t.Fatalf("pause after autonomy cycle: %v", err)
	}

	paused = false
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Paused: &paused}); err != nil {
		t.Fatalf("resume autonomy before execution gate test: %v", err)
	}
	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: executor.Capability().Action, Title: "policy gate execution probe", TriggerReason: "verify linearized pause",
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto,
	})
	if err != nil {
		t.Fatalf("create policy gate proposal: %v", err)
	}
	executionDone := make(chan struct {
		run domain.StewardAutonomousRun
		err error
	}, 1)
	go func() {
		run, err := node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
		executionDone <- struct {
			run domain.StewardAutonomousRun
			err error
		}{run: run, err: err}
	}()
	waitForSignal(t, ctx, executor.started, "autonomy executor start")
	pauseDone = make(chan error, 1)
	paused = true
	go func() {
		_, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Paused: &paused})
		pauseDone <- err
	}()
	rules, err := node.service.ListAutonomyRules(ctx)
	if err != nil || len(rules) == 0 {
		t.Fatalf("load rule for policy gate update: rules=%d err=%v", len(rules), err)
	}
	ruleUpdateDone := make(chan error, 1)
	scope := rules[0].ScopeSummary
	go func() {
		_, err := node.service.UpdateAutonomyRule(ctx, rules[0].ID, steward.UpdateAutonomyRuleInput{ScopeSummary: &scope})
		ruleUpdateDone <- err
	}()
	assertNoSignal(t, pauseDone, 150*time.Millisecond, "pause returned while an executor was still active")
	assertNoSignal(t, ruleUpdateDone, 150*time.Millisecond, "rule update returned while an executor was still active")
	close(executor.release)
	executed := <-executionDone
	if executed.err != nil || executed.run.Status != steward.RunSuccess {
		t.Fatalf("finish in-flight execution: run=%+v err=%v", executed.run, executed.err)
	}
	if err := <-pauseDone; err != nil {
		t.Fatalf("pause after execution: %v", err)
	}
	if err := <-ruleUpdateDone; err != nil {
		t.Fatalf("rule update after execution: %v", err)
	}

	blocked, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: executor.Capability().Action, Title: "post-pause execution probe", TriggerReason: "must remain blocked",
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto,
	})
	if err != nil {
		t.Fatalf("create post-pause proposal: %v", err)
	}
	run, err := node.service.ExecuteAutonomyProposal(ctx, blocked.ID)
	if err != nil || run.Status != steward.RunBlocked {
		t.Fatalf("post-pause execution was not blocked: run=%+v err=%v", run, err)
	}
	if calls := executor.callCount(); calls != 1 {
		t.Fatalf("executor call count=%d after pause, want 1", calls)
	}
}

func TestStewardAutonomyExecutionRevalidatesCurrentRule(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the current autonomy rule execution test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "autonomy_current_rule"), "windows-main")
	rules, err := node.service.ListAutonomyRules(ctx)
	if err != nil {
		t.Fatalf("list autonomy rules: %v", err)
	}
	var followUpRule *domain.StewardAutonomyRule
	for index := range rules {
		if rules[index].Action == steward.AutonomyActionCreateFollowUpTask {
			followUpRule = &rules[index]
			break
		}
	}
	if followUpRule == nil {
		t.Fatalf("follow-up autonomy rule is missing")
	}
	auto := steward.AutonomyPolicyAuto
	enabled := true
	if _, err := node.service.UpdateAutonomyRule(ctx, followUpRule.ID, steward.UpdateAutonomyRuleInput{Policy: &auto, Enabled: &enabled}); err != nil {
		t.Fatalf("enable automatic follow-up rule: %v", err)
	}
	event, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Type: "manual_note", Title: "current rule revalidation source", Summary: "rule changes must revoke stale auto authority",
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3,
	})
	if err != nil {
		t.Fatalf("create rule revalidation event: %v", err)
	}
	proposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		RuleID: &followUpRule.ID, SourceEntityType: steward.EntityEvent, SourceEntityID: &event.ID,
		Action: steward.AutonomyActionCreateFollowUpTask, Title: "stale auto authority probe", TriggerReason: "rule was auto when proposed",
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto,
	})
	if err != nil {
		t.Fatalf("create rule-bound proposal: %v", err)
	}
	approvalEvent, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Type: "manual_note", Title: "current rule approval source", Summary: "approval must revalidate current policy",
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3,
	})
	if err != nil {
		t.Fatalf("create approval revalidation event: %v", err)
	}
	confirmProposal, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		RuleID: &followUpRule.ID, SourceEntityType: steward.EntityEvent, SourceEntityID: &approvalEvent.ID,
		Action: steward.AutonomyActionCreateFollowUpTask, Title: "stale approval authority probe", TriggerReason: "requires explicit approval",
		RiskLevel: "low", PermissionLevel: steward.PermissionA3, DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyConfirm,
	})
	if err != nil {
		t.Fatalf("create rule-bound confirm proposal: %v", err)
	}
	if run, err := node.service.ExecuteAutonomyProposal(ctx, confirmProposal.ID); err != nil || run.Status != steward.RunBlocked {
		t.Fatalf("create approval request before rule revocation: run=%+v err=%v", run, err)
	}
	approvals, err := node.service.ListApprovalRequests(ctx, steward.ApprovalPending, 20)
	if err != nil {
		t.Fatalf("list pending approval before rule revocation: %v", err)
	}
	approvalID := ""
	for _, approval := range approvals {
		if approval.ProposalID != nil && *approval.ProposalID == confirmProposal.ID && approval.RequestedAction == "approve autonomous execution" {
			approvalID = approval.ID
			break
		}
	}
	if approvalID == "" {
		t.Fatalf("rule-bound approval request is missing")
	}
	never := steward.AutonomyPolicyNever
	if _, err := node.service.UpdateAutonomyRule(ctx, followUpRule.ID, steward.UpdateAutonomyRuleInput{Policy: &never}); err != nil {
		t.Fatalf("revoke rule auto authority: %v", err)
	}
	if _, err := node.service.ApproveAutonomyProposal(ctx, proposal.ID); err == nil || !strings.Contains(err.Error(), "current autonomy rule") {
		t.Fatalf("direct approval ignored revoked current rule: %v", err)
	}
	if _, err := node.service.ApproveRequest(ctx, approvalID, steward.DecideApprovalInput{DecisionReason: "must be rejected"}); err == nil || !strings.Contains(err.Error(), "current autonomy rule") {
		t.Fatalf("approval request ignored revoked current rule: %v", err)
	}
	var deniedApprovalAudits int
	if err := node.pool.QueryRow(ctx, `
		select count(*) from steward_audit_logs
		where action = 'autonomy.approval.current_rule_denied' and result_status = $1
	`, steward.ResultBlocked).Scan(&deniedApprovalAudits); err != nil || deniedApprovalAudits != 2 {
		t.Fatalf("current-rule approval denial audits=%d, want 2: %v", deniedApprovalAudits, err)
	}
	run, err := node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
	if err != nil || run.Status != steward.RunBlocked || !strings.Contains(run.RecoveryHint, "never") {
		t.Fatalf("stale auto proposal ignored current never rule: run=%+v err=%v", run, err)
	}
	var targetID string
	if err := node.pool.QueryRow(ctx, `select execution_target_id from steward_autonomy_proposals where id = $1`, proposal.ID).Scan(&targetID); err != nil || targetID != "" {
		t.Fatalf("revoked rule proposal created target=%q err=%v", targetID, err)
	}
	if _, err := node.service.UpdateAutonomyRule(ctx, followUpRule.ID, steward.UpdateAutonomyRuleInput{Policy: &auto}); err != nil {
		t.Fatalf("restore automatic follow-up rule: %v", err)
	}
	run, err = node.service.ExecuteAutonomyProposal(ctx, proposal.ID)
	if err != nil || run.Status != steward.RunSuccess {
		t.Fatalf("restored rule did not allow original proposal: run=%+v err=%v", run, err)
	}
}

type blockingAutonomyDiscoverer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAutonomyDiscoverer() *blockingAutonomyDiscoverer {
	return &blockingAutonomyDiscoverer{started: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingAutonomyDiscoverer) Name() string { return "policy-gate-blocking-discoverer" }

func (d *blockingAutonomyDiscoverer) Discover(ctx context.Context, _ int) error {
	d.once.Do(func() { close(d.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.release:
		return nil
	}
}

type blockingAutonomyExecutor struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
}

func newBlockingAutonomyExecutor() *blockingAutonomyExecutor {
	return &blockingAutonomyExecutor{started: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action: "policy_gate_test_action", Description: "policy gate test executor", TargetType: "policy_gate_artifact",
		RiskLevel: "low", MaxPermissionLevel: steward.PermissionA3,
	}
}

func (e *blockingAutonomyExecutor) Simulate(context.Context, domain.StewardAutonomyProposal) (steward.AutonomyExecutionResult, error) {
	return steward.AutonomyExecutionResult{TargetType: "policy_gate_artifact", ImpactSummary: "simulation only"}, nil
}

func (e *blockingAutonomyExecutor) Execute(ctx context.Context, _ domain.StewardAutonomyProposal) (steward.AutonomyExecutionResult, error) {
	e.once.Do(func() { close(e.started) })
	select {
	case <-ctx.Done():
		return steward.AutonomyExecutionResult{}, ctx.Err()
	case <-e.release:
	}
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return steward.AutonomyExecutionResult{TargetType: "policy_gate_artifact", TargetID: uuid.NewString(), ImpactSummary: "policy gate execution completed"}, nil
}

func (e *blockingAutonomyExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func waitForSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s: %v", label, ctx.Err())
	case <-signal:
	}
}

func assertNoSignal(t *testing.T, signal <-chan error, duration time.Duration, message string) {
	t.Helper()
	select {
	case err := <-signal:
		t.Fatalf("%s: %v", message, err)
	case <-time.After(duration):
	}
}

func TestStewardControlledAutonomyExecutesOnlyPreapprovedLowRiskRules(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed controlled autonomy integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "controlled_autonomy"), "windows-main")
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Mode: steward.AutonomyModeControlled}); err != nil {
		t.Fatalf("enable controlled autonomy: %v", err)
	}
	rules, err := node.service.ListAutonomyRules(ctx)
	if err != nil {
		t.Fatalf("list autonomy rules: %v", err)
	}
	auto := steward.AutonomyPolicyAuto
	enabled := true
	summaryRuleID := ""
	for _, name := range []string{"event-knowledge-summary", "due-task-reminder"} {
		rule := findAutonomyRuleByName(rules, name)
		if rule == nil {
			t.Fatalf("default autonomy rule %s is missing: %+v", name, rules)
		}
		if _, err := node.service.UpdateAutonomyRule(ctx, rule.ID, steward.UpdateAutonomyRuleInput{Policy: &auto, Enabled: &enabled}); err != nil {
			t.Fatalf("preapprove autonomy rule %s: %v", name, err)
		}
		if name == "event-knowledge-summary" {
			summaryRuleID = rule.ID
		}
	}

	dueAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	sourceTask, err := node.service.CreateTask(ctx, steward.CreateTaskInput{
		Title: "source task requiring reminder", Description: "keep the original due time", DueAt: &dueAt,
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3, RiskLevel: "low",
	})
	if err != nil {
		t.Fatalf("create due source task: %v", err)
	}
	sourceEvent, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Type: "manual_note", Title: "source event for summary", Summary: "compact local summary content",
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3,
	})
	if err != nil {
		t.Fatalf("create source event: %v", err)
	}
	manualAuto, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: steward.AutonomyActionCreateLocalTask, Title: "manual auto must not be background executed",
		TriggerReason: "no preapproved rule", RiskLevel: "low", PermissionLevel: steward.PermissionA3,
		DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto,
	})
	if err != nil {
		t.Fatalf("create manual auto proposal: %v", err)
	}

	overview, err := node.service.RunAutonomyCycle(ctx, 20)
	if err != nil {
		t.Fatalf("run controlled autonomy cycle: %v", err)
	}
	summaryProposal := findAutonomyProposal(overview.Proposals, steward.AutonomyActionCreateKnowledgeSummary, sourceEvent.ID)
	reminderProposal := findAutonomyProposal(overview.Proposals, steward.AutonomyActionCreateReminderTask, sourceTask.ID)
	if summaryProposal == nil || summaryProposal.Status != steward.ProposalExecuted || summaryProposal.ExecutionTargetType != "knowledge_item" {
		t.Fatalf("preapproved knowledge summary was not executed: %+v", summaryProposal)
	}
	if reminderProposal == nil || reminderProposal.Status != steward.ProposalExecuted || reminderProposal.ExecutionTargetType != "task" {
		t.Fatalf("preapproved reminder was not executed: %+v", reminderProposal)
	}
	manualProposal := findAutonomyProposalByID(overview.Proposals, manualAuto.ID)
	if manualProposal == nil || manualProposal.Status != steward.ProposalCandidate {
		t.Fatalf("manual policy=auto proposal bypassed rule preapproval: %+v", manualProposal)
	}

	var summaryType, importMethod string
	var summaryConfirmed bool
	if err := node.pool.QueryRow(ctx, `
		select type, import_method, user_confirmed from steward_knowledge_items where id = $1
	`, summaryProposal.ExecutionTargetID).Scan(&summaryType, &importMethod, &summaryConfirmed); err != nil {
		t.Fatalf("load generated knowledge summary: %v", err)
	}
	if summaryType != "autonomy_summary" || importMethod != "autonomy_summary" || summaryConfirmed {
		t.Fatalf("unexpected automatic summary metadata: type=%s method=%s confirmed=%t", summaryType, importMethod, summaryConfirmed)
	}
	var summaryAuditActor, summaryAuditDataLevel, summaryAuditPermission string
	if err := node.pool.QueryRow(ctx, `
		select actor, data_level, permission_level
		from steward_audit_logs
		where action = 'knowledge_item.create' and target_id = $1
		order by occurred_at desc limit 1
	`, summaryProposal.ExecutionTargetID).Scan(&summaryAuditActor, &summaryAuditDataLevel, &summaryAuditPermission); err != nil {
		t.Fatalf("load automatic summary creation audit: %v", err)
	}
	if summaryAuditActor != "autonomy" || summaryAuditDataLevel != steward.DataD0 || summaryAuditPermission != steward.PermissionA3 {
		t.Fatalf("automatic summary audit lost execution identity: actor=%s data=%s permission=%s", summaryAuditActor, summaryAuditDataLevel, summaryAuditPermission)
	}
	var reminderType string
	var reminderDueAt *time.Time
	var reminderConfirmed bool
	if err := node.pool.QueryRow(ctx, `
		select type, due_at, user_confirmed from steward_tasks where id = $1
	`, reminderProposal.ExecutionTargetID).Scan(&reminderType, &reminderDueAt, &reminderConfirmed); err != nil {
		t.Fatalf("load generated reminder task: %v", err)
	}
	if reminderType != "autonomous_reminder" || reminderDueAt == nil || !reminderDueAt.Equal(dueAt) || reminderConfirmed {
		t.Fatalf("reminder did not preserve source due time: type=%s due=%v source=%v confirmed=%t", reminderType, reminderDueAt, dueAt, reminderConfirmed)
	}

	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Mode: steward.AutonomyModeSuggestOnly}); err != nil {
		t.Fatalf("switch to suggest-only autonomy: %v", err)
	}
	suggestOnlyEvent, err := node.service.CreateEvent(ctx, steward.CreateEventInput{
		Type: "manual_note", Title: "suggest-only source event", Summary: "must remain a candidate",
		Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3,
	})
	if err != nil {
		t.Fatalf("create suggest-only source event: %v", err)
	}
	overview, err = node.service.RunAutonomyCycle(ctx, 20)
	if err != nil {
		t.Fatalf("run suggest-only autonomy cycle: %v", err)
	}
	suggestOnlyProposal := findAutonomyProposal(overview.Proposals, steward.AutonomyActionCreateKnowledgeSummary, suggestOnlyEvent.ID)
	if suggestOnlyProposal == nil || suggestOnlyProposal.Status != steward.ProposalCandidate {
		t.Fatalf("suggest-only mode executed an auto rule: %+v", suggestOnlyProposal)
	}
	if _, err := node.service.UpdateAutonomySettings(ctx, steward.UpdateAutonomySettingsInput{Mode: steward.AutonomyModeControlled}); err != nil {
		t.Fatalf("restore controlled autonomy: %v", err)
	}
	disabled := false
	if _, err := node.service.UpdateAutonomyRule(ctx, summaryRuleID, steward.UpdateAutonomyRuleInput{Enabled: &disabled}); err != nil {
		t.Fatalf("disable preapproved summary rule: %v", err)
	}
	overview, err = node.service.RunAutonomyCycle(ctx, 20)
	if err != nil {
		t.Fatalf("run controlled cycle with disabled rule: %v", err)
	}
	suggestOnlyProposal = findAutonomyProposalByID(overview.Proposals, suggestOnlyProposal.ID)
	if suggestOnlyProposal == nil || suggestOnlyProposal.Status != steward.ProposalCandidate {
		t.Fatalf("disabled rule still authorized background execution: %+v", suggestOnlyProposal)
	}

	enabled = true
	if _, err := node.service.UpdateAutonomyRule(ctx, summaryRuleID, steward.UpdateAutonomyRuleInput{Enabled: &enabled}); err != nil {
		t.Fatalf("re-enable summary rule for idempotent recovery: %v", err)
	}
	if _, err := node.pool.Exec(ctx, `
		update steward_autonomy_proposals
		set status = $1, execution_target_type = '', execution_target_id = ''
		where id = $2
	`, steward.ProposalApproved, summaryProposal.ID); err != nil {
		t.Fatalf("simulate lost summary proposal update: %v", err)
	}
	if run, err := node.service.ExecuteAutonomyProposal(ctx, summaryProposal.ID); err != nil || run.Status != steward.RunSuccess {
		t.Fatalf("retry idempotent knowledge summary: run=%+v err=%v", run, err)
	}
	var summaryCount int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_knowledge_items where id = $1`, summaryProposal.ExecutionTargetID).Scan(&summaryCount); err != nil || summaryCount != 1 {
		t.Fatalf("knowledge summary retry duplicated target: count=%d err=%v", summaryCount, err)
	}

	diagnostic, err := node.service.CreateAutonomyProposal(ctx, steward.CreateAutonomyProposalInput{
		Action: steward.AutonomyActionRunReadOnlyDiagnostics, Title: "read-only steward diagnostics",
		TriggerReason: "verification", RiskLevel: "low", PermissionLevel: steward.PermissionA3,
		DataLevel: steward.DataD0, Policy: steward.AutonomyPolicyAuto,
	})
	if err != nil {
		t.Fatalf("create diagnostics proposal: %v", err)
	}
	if run, err := node.service.ExecuteAutonomyProposal(ctx, diagnostic.ID); err != nil || run.Status != steward.RunSuccess || !strings.Contains(run.ImpactSummary, "open_tasks=") {
		t.Fatalf("execute read-only diagnostics: run=%+v err=%v", run, err)
	}
	var diagnosticMethod string
	if err := node.pool.QueryRow(ctx, `
		select k.import_method
		from steward_knowledge_items k
		join steward_autonomy_proposals p on p.execution_target_id = k.id::text
		where p.id = $1
	`, diagnostic.ID).Scan(&diagnosticMethod); err != nil || diagnosticMethod != "readonly_diagnostics" {
		t.Fatalf("diagnostic report was not stored: method=%s err=%v", diagnosticMethod, err)
	}
}

func findAutonomyRuleByName(rules []domain.StewardAutonomyRule, name string) *domain.StewardAutonomyRule {
	for index := range rules {
		if rules[index].Name == name {
			return &rules[index]
		}
	}
	return nil
}

func findAutonomyProposal(proposals []domain.StewardAutonomyProposal, action string, sourceID string) *domain.StewardAutonomyProposal {
	for index := range proposals {
		if proposals[index].Action == action && proposals[index].SourceEntityID != nil && *proposals[index].SourceEntityID == sourceID {
			return &proposals[index]
		}
	}
	return nil
}

func findAutonomyProposalByID(proposals []domain.StewardAutonomyProposal, id string) *domain.StewardAutonomyProposal {
	for index := range proposals {
		if proposals[index].ID == id {
			return &proposals[index]
		}
	}
	return nil
}

func postAutonomyRun(t *testing.T, node stewardHTTPNode, path string) domain.StewardAutonomousRun {
	t.Helper()
	response, err := node.server.Client().Post(node.apiBase+path, "application/json", nil)
	if err != nil {
		t.Fatalf("post autonomy run %s: %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure map[string]any
		_ = json.NewDecoder(response.Body).Decode(&failure)
		t.Fatalf("autonomy run %s status = %s payload=%v", path, response.Status, failure)
	}
	var payload struct {
		Run domain.StewardAutonomousRun `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode autonomy run %s: %v", path, err)
	}
	return payload.Run
}

func hasAutonomyActionCapability(capabilities []domain.StewardAutonomyActionCapability, action string) bool {
	for _, capability := range capabilities {
		if capability.Action == action {
			return true
		}
	}
	return false
}

func hasTaskID(tasks []domain.StewardTask, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

func hasTaskWithTitle(tasks []domain.StewardTask, id string, title string) bool {
	for _, task := range tasks {
		if task.ID == id && task.Title == title {
			return true
		}
	}
	return false
}

func daemonLoopsByName(loops []domain.StewardBackgroundLoopStatus) map[string]domain.StewardBackgroundLoopStatus {
	result := make(map[string]domain.StewardBackgroundLoopStatus, len(loops))
	for _, loop := range loops {
		result[loop.Name] = loop
	}
	return result
}

func findStewardDevice(devices []domain.StewardDevice, id string) *domain.StewardDevice {
	for index := range devices {
		if devices[index].ID == id {
			return &devices[index]
		}
	}
	return nil
}

func waitForStewardCondition(t *testing.T, ctx context.Context, label string, condition func() (bool, string)) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	lastDetail := ""
	for {
		ok, detail := condition()
		if ok {
			return
		}
		lastDetail = detail
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s: %s", label, lastDetail)
		case <-ticker.C:
		}
	}
}

type stewardHTTPNode struct {
	service     *steward.Service
	server      *httptest.Server
	peerServer  *httptest.Server
	apiBase     string
	peerAPIBase string
	pool        *pgxpool.Pool
}

func temporaryPostgresDatabaseConfig(t *testing.T, ctx context.Context, baseDSN string, label string) *pgxpool.Config {
	t.Helper()

	dbName := fmt.Sprintf("steward_e2e_%d_%s", time.Now().UnixNano(), label)
	adminConfig, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminConfig.ConnConfig.Database = "postgres"
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}

	quotedName := pgx.Identifier{dbName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "create database "+quotedName); err != nil {
		adminPool.Close()
		t.Fatalf("create temporary database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(dropCtx, `select pg_terminate_backend(pid) from pg_stat_activity where datname = $1 and pid <> pg_backend_pid()`, dbName)
		_, _ = adminPool.Exec(dropCtx, "drop database if exists "+quotedName)
		adminPool.Close()
	})

	nodeConfig, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL for node: %v", err)
	}
	nodeConfig.ConnConfig.Database = dbName
	return nodeConfig
}

func newStewardHTTPNode(t *testing.T, ctx context.Context, dbConfig *pgxpool.Config, agentID string, options ...steward.ServiceOption) stewardHTTPNode {
	t.Helper()

	pool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		t.Fatalf("connect node database %s: %v", agentID, err)
	}
	db := &database.DB{Pool: pool}
	t.Cleanup(db.Close)

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("ping node database %s: %v", agentID, err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate node database %s: %v", agentID, err)
	}

	serviceOptions := []steward.ServiceOption{
		steward.WithAgentID(agentID),
		steward.WithStorageDir(t.TempDir()),
		steward.WithAutonomyAdvisor(steward.DisabledAutonomyAdvisor("test")),
	}
	serviceOptions = append(serviceOptions, options...)
	service := steward.NewService(
		db,
		serviceOptions...,
	)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatalf("ensure defaults for %s: %v", agentID, err)
	}

	deps := Dependencies{
		StewardService: service,
		Readiness: func(context.Context) (map[string]string, error) {
			return map[string]string{"database": "ok", "steward": "ok"}, nil
		},
	}
	managementRouter := chi.NewRouter()
	RegisterManagementRoutes(managementRouter, deps)
	server := httptest.NewServer(managementRouter)
	t.Cleanup(server.Close)
	peerRouter := chi.NewRouter()
	RegisterPeerRoutes(peerRouter, PeerDependencies{
		StewardService: service,
		Readiness:      deps.Readiness,
	})
	peerServer := httptest.NewServer(peerRouter)
	t.Cleanup(peerServer.Close)

	return stewardHTTPNode{
		service:     service,
		server:      server,
		peerServer:  peerServer,
		apiBase:     server.URL + "/api",
		peerAPIBase: peerServer.URL + "/api",
		pool:        pool,
	}
}

type flakyAutonomyExecutor struct {
	mu        sync.Mutex
	calls     int
	failUntil int
}

func (e *flakyAutonomyExecutor) Capability() domain.StewardAutonomyActionCapability {
	return domain.StewardAutonomyActionCapability{
		Action:             steward.AutonomyActionCreateFollowUpTask,
		Description:        "deterministic retry test executor",
		TargetType:         "test_result",
		RiskLevel:          "low",
		MaxPermissionLevel: steward.PermissionA3,
	}
}

func (e *flakyAutonomyExecutor) Simulate(context.Context, domain.StewardAutonomyProposal) (steward.AutonomyExecutionResult, error) {
	return steward.AutonomyExecutionResult{TargetType: "test_result", ImpactSummary: "test simulation"}, nil
}

func (e *flakyAutonomyExecutor) Execute(context.Context, domain.StewardAutonomyProposal) (steward.AutonomyExecutionResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= e.failUntil {
		return steward.AutonomyExecutionResult{}, fmt.Errorf("deterministic executor failure %d", e.calls)
	}
	return steward.AutonomyExecutionResult{
		TargetType:    "test_result",
		TargetID:      "recovered",
		ImpactSummary: "manual retry recovered the executor",
		RecoveryHint:  "dismiss the created test target if needed",
	}, nil
}

func (e *flakyAutonomyExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *flakyAutonomyExecutor) SetFailUntil(value int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failUntil = value
}

func createMeshTask(t *testing.T, ctx context.Context, node stewardHTTPNode, title string) domain.StewardTask {
	t.Helper()
	task, err := node.service.CreateTask(ctx, steward.CreateTaskInput{
		Title: title, Description: "created by three-node S3 mesh verification", Source: "verification",
		DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3, RiskLevel: "low", UserConfirmed: boolPointerValue(true),
	})
	if err != nil {
		t.Fatalf("create mesh task %q: %v", title, err)
	}
	return task
}

func createMeshMemory(t *testing.T, ctx context.Context, node stewardHTTPNode, title string) domain.StewardMemory {
	t.Helper()
	memory, err := node.service.CreateMemory(ctx, steward.CreateMemoryInput{
		Type: "project_fact", Title: title, Summary: "cross-device permission probe", Content: "private memory content",
		Scope: "global", Source: "verification", DataLevel: steward.DataD0, PermissionLevel: steward.PermissionA3,
		Confidence: 1, UserConfirmed: boolPointerValue(true),
	})
	if err != nil {
		t.Fatalf("create mesh memory %q: %v", title, err)
	}
	return memory
}

func assertMemoryAbsent(t *testing.T, ctx context.Context, node stewardHTTPNode, memoryID string) {
	t.Helper()
	memories, err := node.service.ListMemories(ctx, 100)
	if err != nil {
		t.Fatalf("list memories for permission assertion: %v", err)
	}
	for _, memory := range memories {
		if memory.ID == memoryID {
			t.Fatalf("memory %s crossed denied device permission", memoryID)
		}
	}
}

func syncMeshPeer(t *testing.T, ctx context.Context, node stewardHTTPNode, peerID string) steward.SyncDeviceResult {
	t.Helper()
	result, err := node.service.SyncDevice(ctx, peerID)
	if err != nil {
		t.Fatalf("sync with peer %s: %+v: %v", peerID, result, err)
	}
	return result
}

func boolPointerValue(value bool) *bool {
	return &value
}

func registerPeer(t *testing.T, ctx context.Context, node stewardHTTPNode, id string, name string, platform string, apiBase string) {
	t.Helper()
	syncEnabled := true
	if _, err := node.service.RegisterDevice(ctx, steward.RegisterDeviceInput{
		ID:              id,
		DeviceName:      name,
		Platform:        platform,
		Role:            steward.DeviceRolePeer,
		SyncEnabled:     &syncEnabled,
		PermissionLevel: steward.PermissionA3,
		APIBaseURL:      apiBase,
	}); err != nil {
		t.Fatalf("register peer %s: %v", id, err)
	}
}

func assertTaskVisibleThroughHTTP(t *testing.T, node stewardHTTPNode, taskID string, title string) {
	t.Helper()

	visible, results, err := taskVisibleThroughHTTP(node, taskID, title)
	if err != nil {
		t.Fatalf("search task through HTTP: %v", err)
	}
	if !visible {
		t.Fatalf("task %s was not visible through HTTP search results: %+v", taskID, results)
	}
}

func waitForTaskVisibleThroughHTTP(t *testing.T, ctx context.Context, node stewardHTTPNode, taskID string, title string) {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastResults []domain.StewardSearchResult
	var lastErr error
	for {
		visible, results, err := taskVisibleThroughHTTP(node, taskID, title)
		if visible {
			return
		}
		lastResults = results
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("task %s did not become visible through HTTP before timeout; lastErr=%v results=%+v", taskID, lastErr, lastResults)
		case <-ticker.C:
		}
	}
}

func taskVisibleThroughHTTP(node stewardHTTPNode, taskID string, title string) (bool, []domain.StewardSearchResult, error) {
	query := url.Values{}
	query.Set("entity_type", "task")
	query.Set("q", title)
	query.Set("limit", "20")
	endpoint := node.apiBase + "/steward/search?" + query.Encode()

	resp, err := node.server.Client().Get(endpoint)
	if err != nil {
		return false, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil, fmt.Errorf("search task through HTTP got %s", resp.Status)
	}

	var payload struct {
		Results []domain.StewardSearchResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false, nil, fmt.Errorf("decode search response: %w", err)
	}
	for _, item := range payload.Results {
		if item.EntityType == "task" && item.ID == taskID {
			return true, payload.Results, nil
		}
	}
	return false, payload.Results, nil
}
