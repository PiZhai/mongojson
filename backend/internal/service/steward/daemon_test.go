package steward

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDaemonOptionsFromEnvParsesIntervals(t *testing.T) {
	t.Setenv("STEWARD_HEARTBEAT_INTERVAL", "2s")
	t.Setenv("STEWARD_SYNC_INTERVAL", "5m")
	t.Setenv("STEWARD_AUTONOMY_INTERVAL", "15m")
	t.Setenv("STEWARD_AUTONOMY_LIMIT", "7")

	options := DaemonOptionsFromEnv()
	if options.HeartbeatInterval != 2*time.Second {
		t.Fatalf("heartbeat interval = %s, want 2s", options.HeartbeatInterval)
	}
	if options.SyncInterval != 5*time.Minute {
		t.Fatalf("sync interval = %s, want 5m", options.SyncInterval)
	}
	if options.AutonomyInterval != 15*time.Minute {
		t.Fatalf("autonomy interval = %s, want 15m", options.AutonomyInterval)
	}
	if options.AutonomyLimit != 7 {
		t.Fatalf("autonomy limit = %d, want 7", options.AutonomyLimit)
	}
}

func TestSanitizeDaemonLoopErrorBoundsAndFlattensSummary(t *testing.T) {
	value := sanitizeDaemonLoopError(errors.New("peer failed\n" + strings.Repeat("x", 600)))
	if strings.ContainsAny(value, "\r\n") || len([]rune(value)) != 500 {
		t.Fatalf("sanitized daemon loop error is not flat and bounded: length=%d value=%q", len([]rune(value)), value)
	}
}

func TestNormalizeDaemonOptionsKeepsSafeDefaults(t *testing.T) {
	options := normalizeDaemonOptions(DaemonOptions{
		HeartbeatInterval: -time.Second,
		SyncInterval:      -time.Second,
		AutonomyInterval:  -time.Second,
		AutonomyLimit:     99,
	})
	if options.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Fatalf("heartbeat interval = %s, want %s", options.HeartbeatInterval, DefaultHeartbeatInterval)
	}
	if options.SyncInterval != 0 {
		t.Fatalf("sync interval = %s, want disabled", options.SyncInterval)
	}
	if options.AutonomyInterval != 0 {
		t.Fatalf("autonomy interval = %s, want disabled", options.AutonomyInterval)
	}
	if options.AutonomyLimit != 12 {
		t.Fatalf("autonomy limit = %d, want 12", options.AutonomyLimit)
	}
}

func TestAgentAllowsBackgroundWorkOnlyWhenRunning(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: StatusRunning, want: true},
		{status: " running ", want: true},
		{status: StatusStopped, want: false},
		{status: "", want: false},
		{status: "degraded", want: false},
		{status: "unknown", want: false},
	}

	for _, tt := range tests {
		if got := agentAllowsBackgroundWork(tt.status); got != tt.want {
			t.Fatalf("agentAllowsBackgroundWork(%q) = %t, want %t", tt.status, got, tt.want)
		}
	}
}
