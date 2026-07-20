package steward

import (
	"testing"
	"time"
)

func TestApplyReminderPolicyScheduleMovesQuietHourAndCooldown(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 14, 30, 0, 0, time.UTC) // 22:30 local
	last := time.Date(2026, 7, 20, 14, 25, 0, 0, time.UTC)
	result := applyReminderPolicySchedule(reminderPolicyScheduleInput{
		Now: now, Requested: now, LastScheduled: last, Location: location,
		Policy: map[string]any{
			"quiet_hours":      map[string]any{"start": "23:00", "end": "08:00", "mode": "soft"},
			"cooldown_seconds": 3600,
		},
	})
	want := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC) // 08:00 local
	if !result.ScheduledAt.Equal(want) {
		t.Fatalf("scheduled=%s want=%s adjustments=%v", result.ScheduledAt, want, result.Adjustments)
	}
	if len(result.Adjustments) != 2 || result.Adjustments[0] != "cooldown" || result.Adjustments[1] != "quiet_hours" {
		t.Fatalf("adjustments=%v", result.Adjustments)
	}
}

func TestApplyReminderPolicyScheduleDefersSoftBudgetAndAllowsOverride(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	policy := map[string]any{"daily_soft_budget": 2, "category_soft_budget": 1, "quiet_hours": map[string]any{"start": "23:00", "end": "07:30", "mode": "soft"}}
	result := applyReminderPolicySchedule(reminderPolicyScheduleInput{
		Now: now, Requested: now, Location: location, Policy: policy, DailyCount: 2, CategoryCount: 1,
	})
	want := time.Date(2026, 7, 21, 7, 30, 0, 0, time.UTC)
	if !result.ScheduledAt.Equal(want) {
		t.Fatalf("scheduled=%s want=%s", result.ScheduledAt, want)
	}
	overridden := applyReminderPolicySchedule(reminderPolicyScheduleInput{
		Now: now, Requested: now, Location: location, Policy: policy, DailyCount: 99, CategoryCount: 99, Override: true,
	})
	if !overridden.ScheduledAt.Equal(now) || len(overridden.Adjustments) != 0 {
		t.Fatalf("explicit model override was ignored: %#v", overridden)
	}
}

func TestApplyReminderPolicyScheduleUsesModelPreferredWindow(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 7, 20, 8, 15, 0, 0, location)
	result := applyReminderPolicySchedule(reminderPolicyScheduleInput{
		Now: now, Requested: now, Location: location,
		Policy: map[string]any{
			"preferred_windows": []any{
				map[string]any{"start": "18:00", "end": "20:00"},
				map[string]any{"start": "09:30", "end": "11:30", "weekdays": []any{float64(time.Monday)}},
			},
		},
	})
	want := time.Date(2026, 7, 20, 9, 30, 0, 0, location)
	if !result.ScheduledAt.Equal(want) || len(result.Adjustments) != 1 || result.Adjustments[0] != "preferred_window" {
		t.Fatalf("preferred window result=%+v want=%s", result, want)
	}
	inside := applyReminderPolicySchedule(reminderPolicyScheduleInput{
		Now: want.Add(15 * time.Minute), Requested: want.Add(15 * time.Minute), Location: location,
		Policy: map[string]any{"preferred_windows": []any{"09:30-11:30"}},
	})
	if !inside.ScheduledAt.Equal(want.Add(15*time.Minute)) || len(inside.Adjustments) != 0 {
		t.Fatalf("time already in preferred window was changed: %+v", inside)
	}
}
