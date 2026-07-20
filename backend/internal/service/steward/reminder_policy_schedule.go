package steward

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type reminderPolicyScheduleInput struct {
	Now, Requested, LastScheduled time.Time
	Location                      *time.Location
	Policy                        map[string]any
	DailyCount, CategoryCount     int
	Priority                      string
	Override                      bool
}

type reminderPolicyScheduleResult struct {
	ScheduledAt time.Time
	Adjustments []string
}

// applyNotificationReminderPolicy turns a model-authored reminder policy into
// an effective schedule. The rules are deliberately generic: the model owns
// the policy values and may explicitly override soft preferences; this layer
// only executes the selected policy consistently and records every change.
func (s *Service) applyNotificationReminderPolicy(ctx context.Context, input *CreateNotificationInput, policy StewardReminderPolicy) error {
	if input == nil || input.ScheduledAt == nil || len(policy.Policy) == 0 {
		return nil
	}
	if preferredChannels := reminderPolicyPreferredChannels(policy.Policy); len(preferredChannels) > 0 {
		input.DecisionContext["preferred_channels"] = preferredChannels
	}
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return fmt.Errorf("load reminder timezone: %w", err)
	}
	location := time.Local
	if timezone := strings.TrimSpace(settings.Timezone); timezone != "" {
		if parsed, loadErr := time.LoadLocation(timezone); loadErr == nil {
			location = parsed
		} else {
			return fmt.Errorf("load reminder timezone %q: %w", timezone, loadErr)
		}
	}
	requested := input.ScheduledAt.UTC()
	localRequested := requested.In(location)
	dayStartLocal := time.Date(localRequested.Year(), localRequested.Month(), localRequested.Day(), 0, 0, 0, 0, location)
	dayEndLocal := dayStartLocal.AddDate(0, 0, 1)
	var dailyCount, categoryCount int
	var lastScheduled *time.Time
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) filter(where scheduled_at >= $1 and scheduled_at < $2),
		       count(*) filter(where category=$3 and scheduled_at >= $1 and scheduled_at < $2),
		       max(scheduled_at) filter(where category=$3 and scheduled_at <= $4)
		from steward_notifications
		where status not in ('cancelled','failed')
	`, dayStartLocal.UTC(), dayEndLocal.UTC(), input.Category, requested).Scan(&dailyCount, &categoryCount, &lastScheduled); err != nil {
		return fmt.Errorf("read reminder policy counters: %w", err)
	}
	override, _ := input.DecisionContext["policy_override"].(bool)
	values := reminderPolicyScheduleInput{
		Now: time.Now().UTC(), Requested: requested, Location: location, Policy: policy.Policy,
		DailyCount: dailyCount, CategoryCount: categoryCount, Priority: input.Priority, Override: override,
	}
	if lastScheduled != nil {
		values.LastScheduled = lastScheduled.UTC()
	}
	result := applyReminderPolicySchedule(values)
	if len(result.Adjustments) == 0 {
		input.DecisionContext["policy_applied"] = true
		return nil
	}
	input.DecisionContext["policy_applied"] = true
	input.DecisionContext["policy_adjustments"] = result.Adjustments
	input.DecisionContext["requested_scheduled_at"] = requested.Format(time.RFC3339Nano)
	input.DecisionContext["effective_scheduled_at"] = result.ScheduledAt.Format(time.RFC3339Nano)
	input.DecisionContext["daily_count_before"] = dailyCount
	input.DecisionContext["category_count_before"] = categoryCount
	adjusted := result.ScheduledAt.UTC()
	input.ScheduledAt = &adjusted
	return nil
}

func applyReminderPolicySchedule(input reminderPolicyScheduleInput) reminderPolicyScheduleResult {
	location := input.Location
	if location == nil {
		location = time.Local
	}
	scheduled := input.Requested
	if scheduled.IsZero() {
		scheduled = input.Now
	}
	if scheduled.Before(input.Now) {
		scheduled = input.Now
	}
	result := reminderPolicyScheduleResult{ScheduledAt: scheduled.UTC(), Adjustments: []string{}}
	if input.Override || strings.EqualFold(strings.TrimSpace(input.Priority), "urgent") {
		return result
	}
	budgetBase := result.ScheduledAt
	if dailyBudget := reminderPolicyInteger(input.Policy, "daily_soft_budget"); dailyBudget > 0 && input.DailyCount >= dailyBudget {
		result.ScheduledAt = nextReminderBudgetWindow(budgetBase, location, input.Policy)
		result.Adjustments = append(result.Adjustments, "daily_soft_budget")
	}
	if categoryBudget := reminderPolicyInteger(input.Policy, "category_soft_budget"); categoryBudget > 0 && input.CategoryCount >= categoryBudget {
		next := nextReminderBudgetWindow(budgetBase, location, input.Policy)
		if next.After(result.ScheduledAt) {
			result.ScheduledAt = next
		}
		result.Adjustments = append(result.Adjustments, "category_soft_budget")
	}
	if cooldown := reminderPolicyInteger(input.Policy, "cooldown_seconds"); cooldown > 0 && !input.LastScheduled.IsZero() {
		next := input.LastScheduled.Add(time.Duration(cooldown) * time.Second)
		if next.After(result.ScheduledAt) {
			result.ScheduledAt = next
			result.Adjustments = append(result.Adjustments, "cooldown")
		}
	}
	if next, adjusted := moveOutsideReminderQuietHours(result.ScheduledAt, location, input.Policy); adjusted {
		result.ScheduledAt = next
		result.Adjustments = append(result.Adjustments, "quiet_hours")
	}
	if next, adjusted := moveIntoPreferredReminderWindow(result.ScheduledAt, location, input.Policy); adjusted {
		result.ScheduledAt = next
		result.Adjustments = append(result.Adjustments, "preferred_window")
	}
	result.ScheduledAt = result.ScheduledAt.UTC()
	return result
}

type preferredReminderWindow struct {
	StartMinute int
	EndMinute   int
	Weekdays    map[time.Weekday]bool
}

func moveIntoPreferredReminderWindow(value time.Time, location *time.Location, policy map[string]any) (time.Time, bool) {
	windows := parsePreferredReminderWindows(policy["preferred_windows"])
	if len(windows) == 0 {
		return value, false
	}
	local := value.In(location)
	for dayOffset := 0; dayOffset <= 7; dayOffset++ {
		day := local.AddDate(0, 0, dayOffset)
		var earliest time.Time
		for _, window := range windows {
			if len(window.Weekdays) > 0 && !window.Weekdays[day.Weekday()] {
				continue
			}
			start := time.Date(day.Year(), day.Month(), day.Day(), window.StartMinute/60, window.StartMinute%60, 0, 0, location)
			end := time.Date(day.Year(), day.Month(), day.Day(), window.EndMinute/60, window.EndMinute%60, 0, 0, location)
			if dayOffset == 0 && !local.Before(start) && local.Before(end) {
				return value, false
			}
			if !start.Before(local) && (earliest.IsZero() || start.Before(earliest)) {
				earliest = start
			}
		}
		if !earliest.IsZero() {
			return earliest.UTC(), !earliest.Equal(local)
		}
	}
	return value, false
}

func parsePreferredReminderWindows(value any) []preferredReminderWindow {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]preferredReminderWindow, 0, len(items))
	for _, item := range items {
		startText, endText := "", ""
		weekdays := map[time.Weekday]bool{}
		switch typed := item.(type) {
		case string:
			parts := strings.Split(strings.TrimSpace(typed), "-")
			startText = strings.TrimSpace(parts[0])
			if len(parts) == 2 {
				endText = strings.TrimSpace(parts[1])
			}
		case map[string]any:
			startText = strings.TrimSpace(fmt.Sprint(typed["start"]))
			endText = strings.TrimSpace(fmt.Sprint(typed["end"]))
			if days, ok := typed["weekdays"].([]any); ok {
				for _, day := range days {
					if number, valid := numericValue(day); valid && number >= 0 && number <= 6 {
						weekdays[time.Weekday(int(number))] = true
					}
				}
			}
		default:
			continue
		}
		start, startErr := time.Parse("15:04", startText)
		if startErr != nil {
			continue
		}
		if endText == "" {
			endText = start.Add(30 * time.Minute).Format("15:04")
		}
		end, endErr := time.Parse("15:04", endText)
		startMinute, endMinute := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
		if endErr != nil || endMinute <= startMinute {
			continue
		}
		result = append(result, preferredReminderWindow{StartMinute: startMinute, EndMinute: endMinute, Weekdays: weekdays})
	}
	return result
}

func reminderPolicyPreferredChannels(policy map[string]any) []string {
	items, ok := policy["preferred_channels"].([]any)
	if !ok {
		return nil
	}
	result := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		channel := strings.ToLower(strings.TrimSpace(fmt.Sprint(item)))
		if channel == "" || seen[channel] || !validNotificationChannel(channel) {
			continue
		}
		seen[channel] = true
		result = append(result, channel)
	}
	return result
}

func reminderPolicyInteger(policy map[string]any, key string) int {
	value, ok := numericValue(policy[key])
	if !ok || value <= 0 {
		return 0
	}
	return int(value)
}

func moveOutsideReminderQuietHours(value time.Time, location *time.Location, policy map[string]any) (time.Time, bool) {
	quiet, ok := policy["quiet_hours"].(map[string]any)
	if !ok || strings.EqualFold(strings.TrimSpace(fmt.Sprint(quiet["mode"])), "off") {
		return value, false
	}
	start, startErr := time.Parse("15:04", strings.TrimSpace(fmt.Sprint(quiet["start"])))
	end, endErr := time.Parse("15:04", strings.TrimSpace(fmt.Sprint(quiet["end"])))
	if startErr != nil || endErr != nil {
		return value, false
	}
	local := value.In(location)
	minute := local.Hour()*60 + local.Minute()
	startMinute, endMinute := start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	inQuiet := false
	endDayOffset := 0
	if startMinute < endMinute {
		inQuiet = minute >= startMinute && minute < endMinute
	} else if startMinute > endMinute {
		inQuiet = minute >= startMinute || minute < endMinute
		if minute >= startMinute {
			endDayOffset = 1
		}
	}
	if !inQuiet {
		return value, false
	}
	next := time.Date(local.Year(), local.Month(), local.Day()+endDayOffset, end.Hour(), end.Minute(), 0, 0, location)
	return next.UTC(), true
}

func nextReminderBudgetWindow(value time.Time, location *time.Location, policy map[string]any) time.Time {
	local := value.In(location)
	hour, minute := 8, 0
	if quiet, ok := policy["quiet_hours"].(map[string]any); ok {
		if end, err := time.Parse("15:04", strings.TrimSpace(fmt.Sprint(quiet["end"]))); err == nil {
			hour, minute = end.Hour(), end.Minute()
		}
	}
	return time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, location).UTC()
}
