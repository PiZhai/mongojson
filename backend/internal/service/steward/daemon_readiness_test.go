package steward

import (
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestCriticalDaemonLoopReadiness(t *testing.T) {
	lastError := "configuration unavailable"
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	recentCompletion := now.Add(-time.Second)
	recentSuccess := now.Add(-10 * time.Second)
	transientSuccess := now.Add(-30 * time.Second)
	staleCompletion := now.Add(-16 * time.Second)
	staleSuccess := now.Add(-36 * time.Second)
	longRunningStarted := now.Add(-90 * time.Minute)
	longRunningCompletion := now.Add(-100 * time.Minute)
	longRunningSuccess := now.Add(-100 * time.Minute)
	stuckStarted := now.Add(-2*time.Hour - 6*time.Second)
	stuckCompletion := now.Add(-3 * time.Hour)
	stuckSuccess := now.Add(-3 * time.Hour)
	tests := []struct {
		name     string
		statuses []domain.StewardBackgroundLoopStatus
		wantErr  string
	}{
		{
			name: "healthy",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true,
				LastCompletedAt: &recentCompletion, LastSuccessAt: &recentSuccess,
			}},
		},
		{
			name: "transient failures stay ready",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true, ConsecutiveFailures: 2, LastError: &lastError,
				LastCompletedAt: &recentCompletion, LastSuccessAt: &transientSuccess,
			}},
		},
		{
			name: "normal long model cycle stays ready while in flight",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true,
				LastStartedAt: &longRunningStarted, LastCompletedAt: &longRunningCompletion, LastSuccessAt: &longRunningSuccess,
			}},
		},
		{
			name: "in-flight cycle beyond execution budget is stuck",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true,
				LastStartedAt: &stuckStarted, LastCompletedAt: &stuckCompletion, LastSuccessAt: &stuckSuccess,
			}},
			wantErr: "has been in flight",
		},
		{
			name: "first iteration has not completed",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true,
			}},
			wantErr: "has not completed its first iteration",
		},
		{
			name: "first iteration failed",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true, ConsecutiveFailures: 1,
				LastCompletedAt: &recentCompletion, LastError: &lastError,
			}},
			wantErr: "has not completed successfully",
		},
		{
			name: "stalled loop has stale completion",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true,
				LastCompletedAt: &staleCompletion, LastSuccessAt: &recentSuccess,
			}},
			wantErr: "last completed",
		},
		{
			name: "success is stale despite fresh completions",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true, ConsecutiveFailures: 2,
				LastCompletedAt: &recentCompletion, LastSuccessAt: &staleSuccess,
			}},
			wantErr: "last succeeded",
		},
		{
			name: "persistent failures are not ready",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "runtime-v2", Enabled: true, Running: true, ConsecutiveFailures: 3, LastError: &lastError,
				LastCompletedAt: &recentCompletion, LastSuccessAt: &transientSuccess,
			}},
			wantErr: "failed 3 consecutive times",
		},
		{
			name: "missing status",
			statuses: []domain.StewardBackgroundLoopStatus{{
				Name: "heartbeat", Enabled: true, Running: true,
			}},
			wantErr: "no status record",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := criticalDaemonLoopReadiness(tt.statuses, "runtime-v2", 3, 10*time.Second, 5*time.Second, 2*time.Hour, now)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected readiness error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("readiness error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestRuntimeLoopExecutionBudgetCoversLongModelRounds(t *testing.T) {
	if got, want := runtimeLoopExecutionBudget(10, 120, 1800, time.Second), 2*time.Hour; got != want {
		t.Fatalf("runtime execution budget = %s, want %s", got, want)
	}
	if got, want := runtimeLoopExecutionBudget(50, 120, 1800, time.Second), 3*time.Hour+20*time.Minute; got != want {
		t.Fatalf("model batch execution budget = %s, want %s", got, want)
	}
}
