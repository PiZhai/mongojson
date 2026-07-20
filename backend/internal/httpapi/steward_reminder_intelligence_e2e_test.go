package httpapi

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/steward"
)

func TestStewardReminderLearningAndBackgroundStatus(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the reminder intelligence integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "reminder_learning"), "reminder-test")

	scheduledAt := time.Now().UTC().Add(time.Hour)
	notification, err := node.service.CreateNotification(ctx, steward.CreateNotificationInput{
		SourceType: "test", Title: "休息一下", Body: "站起来活动两分钟", Category: "wellbeing",
		Priority: "normal", ScheduledAt: &scheduledAt,
		Actions: []domain.StewardNotificationAction{{ID: "later", Label: "稍后", Kind: "snooze"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if notification.ScheduleRevision != 1 || len(notification.Deliveries) == 0 {
		t.Fatalf("initial notification revision/deliveries = %d/%d", notification.ScheduleRevision, len(notification.Deliveries))
	}

	updated, err := node.service.DecideNotification(ctx, notification.ID, steward.NotificationDecisionInput{
		Decision: "snooze", SnoozeSeconds: 900, Channel: notification.Deliveries[0].Channel,
		DeviceID: "reminder-test", Timezone: "Asia/Shanghai", ActivityContext: "focused_work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScheduleRevision != 2 || updated.Status != "queued" {
		t.Fatalf("snoozed notification = revision %d status %s", updated.ScheduleRevision, updated.Status)
	}
	feedback, err := node.service.ListReminderFeedback(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 1 || feedback[0].Action != steward.ReminderFeedbackSnoozed || feedback[0].ScheduleRevision != 1 {
		t.Fatalf("feedback = %+v", feedback)
	}
	windows, err := node.service.ListReceptivityWindows(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].SampleCount != 1 || windows[0].SnoozedCount != 1 {
		t.Fatalf("receptivity windows = %+v", windows)
	}
	started, err := node.service.ReconcileReminderFeedbackEpisodes(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("reminder learning Episodes started=%d, want 1", started)
	}
	started, err = node.service.ReconcileReminderFeedbackEpisodes(ctx, 10)
	if err != nil || started != 0 {
		t.Fatalf("reminder learning reconciliation is not idempotent: started=%d err=%v", started, err)
	}
	var learningEpisodes int
	if err := node.pool.QueryRow(ctx, `select count(*) from steward_agent_episodes
		where trigger_kind='reminder_policy_learning' and context_ref_type='reminder_feedback'
		  and context_ref_id=$1 and visibility='background'`, feedback[0].ID).Scan(&learningEpisodes); err != nil {
		t.Fatal(err)
	}
	if learningEpisodes != 1 {
		t.Fatalf("durable reminder learning Episodes=%d", learningEpisodes)
	}

	policy, err := node.service.GetReminderPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Policy["daily_soft_budget"] = 6
	nextPolicy, err := node.service.UpdateReminderPolicy(ctx, steward.UpdateReminderPolicyInput{
		ProfileScope: policy.ProfileScope, Category: policy.Category, Policy: policy.Policy,
		Rationale: "integration test adjustment", EvidenceManifest: []string{feedback[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextPolicy.Version != policy.Version+1 || nextPolicy.SupersedesID == nil || *nextPolicy.SupersedesID != policy.ID {
		t.Fatalf("versioned reminder policy = %+v", nextPolicy)
	}
	categoryPolicy, err := node.service.UpdateReminderPolicy(ctx, steward.UpdateReminderPolicyInput{
		ProfileScope: "default", Category: "wellbeing",
		Policy:    map[string]any{"daily_soft_budget": 2, "cooldown_seconds": 3600},
		Rationale: "wellbeing reminders were repeatedly snoozed", EvidenceManifest: []string{feedback[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := node.service.GetReminderPolicyFor(ctx, "default", "wellbeing")
	if err != nil || selected.ID != categoryPolicy.ID {
		t.Fatalf("category reminder policy selection = %+v, %v", selected, err)
	}
	categoryScheduledAt := time.Now().UTC().Add(2 * time.Hour)
	categoryNotification, err := node.service.CreateNotification(ctx, steward.CreateNotificationInput{
		SourceType: "test", Title: "喝水", Body: "补充水分", Category: "wellbeing",
		Priority: "normal", ScheduledAt: &categoryScheduledAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if categoryNotification.PolicyID == nil || *categoryNotification.PolicyID != categoryPolicy.ID {
		t.Fatalf("notification did not retain selected category policy: %+v", categoryNotification)
	}

	background, err := node.service.GetBackgroundStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if background.State != "degraded" || len(background.Issues) == 0 {
		t.Fatalf("background status should truthfully report missing source/model: %+v", background)
	}
}
