package steward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/database"
)

type unavailableCollectorTransport func(*http.Request) (*http.Response, error)

func (transport unavailableCollectorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestWindowsDeepCollectorDefaultsProbeOptionalActivityWatch(t *testing.T) {
	t.Setenv("STEWARD_ACTIVITYWATCH_ENDPOINT", "")
	defaults := defaultCollectorConfigs("windows")
	byName := map[string]domain.StewardCollectorConfig{}
	for _, collector := range defaults {
		byName[collector.Name] = collector
	}
	if !byName["windows-activity"].Enabled {
		t.Fatal("native Windows activity collection must be enabled by default")
	}
	if byName["watched-directory"].Enabled {
		t.Fatal("watched-directory must remain disabled until paths are configured")
	}
	activityWatch := byName["activitywatch-bridge"]
	if !activityWatch.Enabled {
		t.Fatal("Windows deep capture must probe ActivityWatch by default")
	}
	if got := strings.TrimSpace(fmt.Sprint(activityWatch.Settings["endpoint"])); got == "" || strings.Contains(got, ":5600") {
		t.Fatalf("ActivityWatch endpoint must be persisted from protected configuration, got %q", got)
	}
}

func TestOptionalActivityWatchFailureDoesNotFailCollectionLoop(t *testing.T) {
	if collectorFailureBlocksLoop("activitywatch-bridge", errors.New("ActivityWatch unavailable")) {
		t.Fatal("optional ActivityWatch outage should not fail the collection loop")
	}
	if !collectorFailureBlocksLoop("system-status", errors.New("database unavailable")) {
		t.Fatal("required collector failure must fail the collection loop")
	}
	if !collectorFailureBlocksLoop("activitywatch-bridge", context.Canceled) {
		t.Fatal("cancellation must propagate through optional collectors")
	}
}

func TestCollectionEntrypointsRespectIntelligenceDisableAndGlobalPause(t *testing.T) {
	ctx, db := openCollectorTestDatabase(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	assertNotRun := func(stage string) {
		t.Helper()
		var count int
		if err := db.Pool.QueryRow(ctx, `select count(*) from steward_collector_configs
			where name in ('system-status','windows-activity') and last_run_at is not null`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s updated %d collector run timestamps", stage, count)
		}
	}
	if _, err := db.Pool.Exec(ctx, `update steward_collector_configs set last_run_at=null
		where name in ('system-status','windows-activity')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_intelligence_settings set enabled=false where id='default'`); err != nil {
		t.Fatal(err)
	}
	if err := service.RunEnabledCollectors(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RunRealtimeCollectors(ctx); err != nil {
		t.Fatal(err)
	}
	assertNotRun("disabled intelligence")

	if _, err := db.Pool.Exec(ctx, `update steward_intelligence_settings set enabled=true where id='default'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_runtime_execution_control set paused=true where id='global'`); err != nil {
		t.Fatal(err)
	}
	if err := service.RunEnabledCollectors(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.RunRealtimeCollectors(ctx); err != nil {
		t.Fatal(err)
	}
	assertNotRun("global pause")
}

func TestUnavailableActivityWatchIsRecordedWithoutFailingCollectionLoop(t *testing.T) {
	ctx, db := openCollectorTestDatabase(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	previousClient := activityAdapterHTTPClient
	activityAdapterHTTPClient = &http.Client{Transport: unavailableCollectorTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("test sidecar unavailable")
	})}
	t.Cleanup(func() { activityAdapterHTTPClient = previousClient })
	if _, err := db.Pool.Exec(ctx, `update steward_collector_configs
		set enabled=true,settings='{"endpoint":"http://127.0.0.1:5600","limit":100}'::jsonb
		where name='activitywatch-bridge'`); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := db.Pool.QueryRow(ctx, `select enabled from steward_collector_configs where name='activitywatch-bridge'`).Scan(&enabled); err != nil || !enabled {
		t.Fatalf("enable ActivityWatch bridge: enabled=%v err=%v", enabled, err)
	}
	allowed, err := service.collectionCycleAllowed(ctx)
	if err != nil || !allowed {
		settings, settingsErr := service.GetIntelligenceSettings(ctx)
		control, controlErr := service.GetRuntimeExecutionControl(ctx)
		t.Fatalf("collection gate: allowed=%v err=%v settings_enabled=%v settings_err=%v paused=%v control_err=%v", allowed, err, settings.Enabled, settingsErr, control.Paused, controlErr)
	}
	if err := service.RunEnabledCollectors(ctx); err != nil {
		t.Fatalf("optional ActivityWatch outage failed collection loop: %v", err)
	}
	var collectorError, sourceStatus, sourceError string
	if err := db.Pool.QueryRow(ctx, `select coalesce(last_error,'') from steward_collector_configs
		where name='activitywatch-bridge'`).Scan(&collectorError); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select status,last_error from steward_collection_source_states
		where collector_name='activitywatch-bridge' and source_key='server'`).Scan(&sourceStatus, &sourceError); err != nil {
		t.Fatal(err)
	}
	if collectorError == "" || sourceError == "" || sourceStatus != "unavailable" {
		t.Fatalf("ActivityWatch state error=%q source_status=%q source_error=%q", collectorError, sourceStatus, sourceError)
	}
}

func TestCollectorDefaultsPersistConfiguredActivityWatchEndpointAndPreserveOverride(t *testing.T) {
	t.Setenv("STEWARD_ACTIVITYWATCH_ENDPOINT", "http://127.0.0.1:6100")
	ctx, db := openCollectorTestDatabase(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	var endpoint string
	if err := db.Pool.QueryRow(ctx, `select settings->>'endpoint' from steward_collector_configs
		where name='activitywatch-bridge'`).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:6100" {
		t.Fatalf("persisted default endpoint=%q", endpoint)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_collector_configs
		set settings='{"endpoint":"http://127.0.0.1:6123","limit":25}'::jsonb,user_overridden=true
		where name='activitywatch-bridge'`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_ACTIVITYWATCH_ENDPOINT", "http://127.0.0.1:6101")
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select settings->>'endpoint' from steward_collector_configs
		where name='activitywatch-bridge'`).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:6123" {
		t.Fatalf("user-overridden endpoint was rewritten to %q", endpoint)
	}
}

func TestWindowsDeepCaptureDefaultMigrationPreservesUserRevision(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific default migration")
	}
	ctx, db := openCollectorTestDatabase(t)
	service := NewService(db)
	settings, err := service.GetIntelligenceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.CaptureProfile != "deep" {
		t.Fatalf("new Windows capture profile = %q, want deep", settings.CaptureProfile)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_intelligence_settings
		set capture_profile='hybrid',settings_revision=2 where id='default'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select capture_profile from steward_intelligence_settings where id='default'`).Scan(&settings.CaptureProfile); err != nil {
		t.Fatal(err)
	}
	if settings.CaptureProfile != "hybrid" {
		t.Fatalf("user-overridden capture profile was rewritten to %q", settings.CaptureProfile)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_intelligence_settings
		set capture_profile='hybrid',settings_revision=1 where id='default'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `select capture_profile from steward_intelligence_settings where id='default'`).Scan(&settings.CaptureProfile); err != nil {
		t.Fatal(err)
	}
	if settings.CaptureProfile != "deep" {
		t.Fatalf("untouched legacy Windows default remained %q", settings.CaptureProfile)
	}
}

func openCollectorTestDatabase(t *testing.T) (context.Context, *database.DB) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run collector persistence tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return ctx, newProfileReportTestDatabase(t, ctx, baseDSN)
}
