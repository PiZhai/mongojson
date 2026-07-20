package steward

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func TestNotificationRoutingEscalatesWithoutDuplicatingLowPriority(t *testing.T) {
	endpoints := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("system")},
		{StewardNotificationEndpoint: endpoint("ntfy")},
		{StewardNotificationEndpoint: endpoint("email")},
	}
	low := routeNotificationEndpoints(endpoints, "low", false, nil)
	if len(low) != 1 || low[0].Endpoint.Channel != "system" || low[0].Delay != 0 {
		t.Fatalf("low-priority routes = %+v", low)
	}
	normal := routeNotificationEndpoints(endpoints, "normal", false, nil)
	if len(normal) != 3 || normal[0].Endpoint.Channel != "system" || normal[1].Delay != 10*time.Minute || normal[2].Delay != time.Hour {
		t.Fatalf("normal routes = %+v", normal)
	}
	urgent := routeNotificationEndpoints(endpoints, "urgent", false, nil)
	for _, route := range urgent {
		if route.Delay != 0 {
			t.Fatalf("urgent route has delay: %+v", route)
		}
	}
}

func TestClampNotificationDeliveryWindowKeepsPolicyInsideDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	requested := now.Add(time.Hour)
	policyDeferred := now.Add(6 * time.Hour)
	windowStart := now.Add(30 * time.Minute)
	windowEnd := now.Add(4 * time.Hour)
	expiresAt := now.Add(5 * time.Hour)
	input := CreateNotificationInput{
		ScheduledAt: &policyDeferred, AllowedWindowStart: &windowStart, AllowedWindowEnd: &windowEnd,
		ExpiresAt: &expiresAt, DecisionContext: map[string]any{},
	}
	if err := clampNotificationDeliveryWindow(&input, requested, now); err != nil {
		t.Fatal(err)
	}
	if !input.ScheduledAt.Equal(requested) || input.DecisionContext["allowed_window_adjustment"] != "policy_deferral_clamped" {
		t.Fatalf("clamped notification = %+v", input)
	}
	invalid := input
	invalid.AllowedWindowStart, invalid.AllowedWindowEnd = &windowEnd, &windowStart
	if err := clampNotificationDeliveryWindow(&invalid, requested, now); err == nil {
		t.Fatal("inverted allowed window must be rejected")
	}
}

func TestNotificationCallbackTokenIsSignedScopedAndExpiring(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	token, err := signNotificationCallbackToken(key, NotificationCallbackClaims{
		NotificationID: "notification-1", ScheduleRevision: 3, ActionID: "snooze-30m",
		Action: ReminderFeedbackSnoozed, ActionValue: "45m", SnoozeSeconds: 2700,
		ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifyNotificationCallbackToken(key, token, now)
	if err != nil || claims.NotificationID != "notification-1" || claims.ScheduleRevision != 3 ||
		claims.ActionValue != "45m" || claims.SnoozeSeconds != 2700 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	parts := strings.Split(token, ".")
	parts[1] = strings.Repeat("0", len(parts[1]))
	if _, err := verifyNotificationCallbackToken(key, strings.Join(parts, "."), now); err == nil {
		t.Fatal("tampered callback token must be rejected")
	}
	if _, err := verifyNotificationCallbackToken(key, token, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired callback token must be rejected")
	}
}

func TestNotificationCallbackClaimsCarryCustomSnoozeValue(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	claims := newNotificationCallbackClaims("notification-custom", 7, domain.StewardNotificationAction{
		ID: "later", Label: "15 分钟后", Kind: "snooze", Value: "15m",
	}, ReminderFeedbackSnoozed, expiresAt)
	if claims.ActionValue != "15m" || claims.SnoozeSeconds != 900 || claims.ExpiresAt != expiresAt.Unix() {
		t.Fatalf("custom snooze claims = %+v", claims)
	}
	if seconds := notificationActionSnoozeSeconds("900"); seconds != 900 {
		t.Fatalf("numeric snooze seconds = %d", seconds)
	}
	if seconds := notificationActionSnoozeSeconds("invalid"); seconds != 0 {
		t.Fatalf("invalid snooze seconds = %d", seconds)
	}
}

func TestRecordNotificationCallbackUsesSignedCustomSnoozeSeconds(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	now := time.Now().UTC().Truncate(time.Second)
	notification, err := service.CreateNotification(ctx, CreateNotificationInput{
		SourceType: "test", Title: "自定义稍后提醒", Body: "验证 Windows 回调保留 15 分钟",
		Category: "test", Priority: "normal", ScheduledAt: &now,
		Actions: []domain.StewardNotificationAction{{ID: "later", Label: "15 分钟后", Kind: "snooze", Value: "900"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(ctx, `delete from steward_notifications where id=$1`, notification.ID) })
	claims := newNotificationCallbackClaims(notification.ID, notification.ScheduleRevision, notification.Actions[0],
		ReminderFeedbackSnoozed, now.Add(time.Hour))
	token, err := signNotificationCallbackToken(key, claims)
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := service.RecordNotificationCallback(ctx, token, "windows-test", "system", map[string]any{
		"reported_occurred_at": now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	if feedback.SnoozeSeconds == nil || *feedback.SnoozeSeconds != 900 || feedback.NewScheduledAt == nil || !feedback.NewScheduledAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("custom snooze feedback = %+v", feedback)
	}
	if feedback.Metadata["signed_action_id"] != "later" || feedback.Metadata["signed_action_value"] != "900" {
		t.Fatalf("signed callback metadata = %+v", feedback.Metadata)
	}
	updated, err := service.GetNotification(ctx, notification.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ScheduleRevision != notification.ScheduleRevision+1 || !updated.ScheduledAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("rescheduled notification = %+v", updated)
	}
}

func TestReminderFeedbackEnrichesPersistedPresentationContext(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := service.GetIntelligenceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectedTimezone := defaultString(settings.Timezone, time.Local.String())
	presentedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	sessionID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,
			observation_count,confidence,value_score,started_at,ended_at,canonical_context
		) values($1,'window','Editor','Editing','test','editor.exe','device-feedback','D2','closed',1,1,1,$2,$3,'focused_work')
	`, sessionID, presentedAt.Add(-time.Minute), presentedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	notification, err := service.CreateNotification(ctx, CreateNotificationInput{
		SourceType: "test", Title: "Context feedback", Body: "Capture the presentation context",
		Category: "feedback-context", Priority: "normal", ScheduledAt: &presentedAt,
		Actions: []domain.StewardNotificationAction{{ID: "ack", Label: "OK", Kind: "acknowledge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_receptivity_windows where category='feedback-context' and device_id='device-feedback'`)
		_, _ = db.Pool.Exec(ctx, `delete from steward_notifications where id=$1`, notification.ID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_sessions where id=$1`, sessionID)
	})
	if _, err := db.Pool.Exec(ctx, `
		update steward_notification_deliveries set status='accepted',accepted_at=$2 where notification_id=$1
	`, notification.ID, presentedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `update steward_notifications set status='sent' where id=$1`, notification.ID); err != nil {
		t.Fatal(err)
	}
	actionAt := presentedAt.Add(75 * time.Second)
	feedback, err := service.RecordReminderFeedback(ctx, notification.ID, NotificationDecisionInput{
		Decision: ReminderFeedbackAcknowledged, OccurredAt: &actionAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if feedback.DeviceID != "device-feedback" || feedback.ActivityContext != "focused_work" || feedback.Timezone != expectedTimezone {
		t.Fatalf("enriched feedback = %+v; timezone=%q", feedback, settings.Timezone)
	}
	if feedback.ResponseSeconds == nil || *feedback.ResponseSeconds != 75 {
		t.Fatalf("response duration = %+v", feedback.ResponseSeconds)
	}
	location, err := time.LoadLocation(expectedTimezone)
	if err != nil {
		t.Fatal(err)
	}
	presentedLocal := presentedAt.In(location)
	var samples int
	if err := db.Pool.QueryRow(ctx, `
		select sample_count from steward_receptivity_windows
		where category='feedback-context' and device_id='device-feedback' and activity_context='focused_work'
		  and weekday=$1 and time_slot=$2
	`, int(presentedLocal.Weekday()), presentedLocal.Hour()*2+presentedLocal.Minute()/30).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if samples != 1 {
		t.Fatalf("presentation window samples = %d", samples)
	}
}

func TestCompletedTaskNotificationIsAutoResolvedOnce(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	taskID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_tasks(id,title,status,priority,source,completed_at,updated_at)
		values($1,'Completed elsewhere',$2,'normal','test',$3,$3)
	`, taskID, StatusDone, now); err != nil {
		t.Fatal(err)
	}
	notification, err := service.CreateNotification(ctx, CreateNotificationInput{
		SourceType: "task", SourceID: taskID, Title: "Task reminder", Body: "No longer needed",
		Category: "reminder", Priority: "normal", ScheduledAt: &now,
		Actions: []domain.StewardNotificationAction{{ID: "ack", Label: "Done", Kind: "acknowledge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_notifications where id=$1`, notification.ID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_tasks where id=$1`, taskID)
	})
	processed, err := service.ReconcileCompletedTaskNotifications(ctx, now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("first reconciliation processed %d notifications", processed)
	}
	updated, err := service.GetNotification(ctx, notification.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != notificationStatusAutoResolved {
		t.Fatalf("notification status = %q", updated.Status)
	}
	processed, err = service.ReconcileCompletedTaskNotifications(ctx, now.Add(2*time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 {
		t.Fatalf("idempotent reconciliation processed %d notifications", processed)
	}
}

func TestNormalNotificationDefersDuringPersistedFocusSession(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sessionID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_activity_sessions(
			id,type,title,summary,source,context_key,device_id,data_level,status,
			observation_count,confidence,value_score,started_at,ended_at,canonical_context
		) values($1,'focused_work','Focus session','Deep work','test','editor','focus-device','D2','closed',1,1,1,$2,$3,'focused_work')
	`, sessionID, now.Add(-time.Minute), now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	windowEnd := now.Add(time.Hour)
	notification, err := service.CreateNotification(ctx, CreateNotificationInput{
		SourceType: "test", Title: "Wait until focus ends", Body: "soft reminder",
		Category: "focus-deferral", Priority: "normal", ScheduledAt: &now, AllowedWindowEnd: &windowEnd,
		DecisionContext: map[string]any{"policy_override": true},
		Actions:         []domain.StewardNotificationAction{{ID: "ack", Label: "OK", Kind: "acknowledge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_notifications where id=$1`, notification.ID)
		_, _ = db.Pool.Exec(ctx, `delete from steward_activity_sessions where id=$1`, sessionID)
	})
	if len(notification.Deliveries) == 0 {
		t.Fatal("notification has no delivery")
	}
	deliveryID := notification.Deliveries[0].ID
	if _, err := db.Pool.Exec(ctx, `update steward_notification_deliveries set status='sending' where id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	if err := service.deliverClaimedNotification(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	var status string
	var nextAttempt time.Time
	if err := db.Pool.QueryRow(ctx, `select status,next_attempt_at from steward_notification_deliveries where id=$1`, deliveryID).Scan(&status, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if status != deliveryStatusQueued || !nextAttempt.After(now.Add(4*time.Minute)) || nextAttempt.After(windowEnd) {
		t.Fatalf("focus deferral status=%s next=%s", status, nextAttempt)
	}
	updated, err := service.GetNotification(ctx, notification.ID)
	if err != nil {
		t.Fatal(err)
	}
	deferral, ok := updated.DecisionContext["last_activity_deferral"].(map[string]any)
	if !ok || deferral["context"] != "focus" {
		t.Fatalf("activity deferral context = %#v", updated.DecisionContext)
	}
}

func TestNotificationRoutingHonorsExplicitChannel(t *testing.T) {
	endpoints := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("system")},
		{StewardNotificationEndpoint: endpoint("ntfy")},
	}
	routes := routeNotificationEndpoints(endpoints, "normal", true, map[string]bool{"ntfy": true})
	if len(routes) != 1 || routes[0].Endpoint.Channel != "ntfy" || routes[0].Delay != 0 {
		t.Fatalf("explicit routes = %+v", routes)
	}
}

func TestNotificationRoutingUsesLearnedPreferredChannel(t *testing.T) {
	endpoints := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("system")},
		{StewardNotificationEndpoint: endpoint("ntfy")},
		{StewardNotificationEndpoint: endpoint("email")},
	}
	routes := routeNotificationEndpoints(endpoints, "normal", false, nil, []string{"email", "system"})
	if len(routes) != 3 || routes[0].Endpoint.Channel != "email" || routes[0].Delay != 0 ||
		routes[1].Endpoint.Channel != "system" || routes[1].Delay != time.Minute ||
		routes[2].Endpoint.Channel != "ntfy" || routes[2].Delay != 25*time.Minute {
		t.Fatalf("preferred channel routes = %+v", routes)
	}
	urgent := routeNotificationEndpoints(endpoints, "urgent", false, nil, []string{"email"})
	for _, route := range urgent {
		if route.Delay != 0 {
			t.Fatalf("urgent notification was delayed by a soft preference: %+v", urgent)
		}
	}
}

func TestNotificationRoutingUsesCrossDeviceOrEmailWhenNoDesktopSessionExists(t *testing.T) {
	ntfyAndEmail := []notificationEndpointRecord{
		{StewardNotificationEndpoint: endpoint("ntfy")},
		{StewardNotificationEndpoint: endpoint("email")},
	}
	low := routeNotificationEndpoints(ntfyAndEmail, "low", false, nil)
	if len(low) != 1 || low[0].Endpoint.Channel != "ntfy" || low[0].Delay != 0 {
		t.Fatalf("headless low-priority routes = %+v", low)
	}
	normal := routeNotificationEndpoints(ntfyAndEmail, "normal", false, nil)
	if len(normal) != 2 || normal[0].Endpoint.Channel != "ntfy" || normal[0].Delay != 0 || normal[1].Delay != time.Hour {
		t.Fatalf("headless normal routes = %+v", normal)
	}
	emailOnly := routeNotificationEndpoints([]notificationEndpointRecord{{StewardNotificationEndpoint: endpoint("email")}}, "low", false, nil)
	if len(emailOnly) != 1 || emailOnly[0].Endpoint.Channel != "email" || emailOnly[0].Delay != 0 {
		t.Fatalf("email-only routes = %+v", emailOnly)
	}
}

func endpoint(channel string) domain.StewardNotificationEndpoint {
	return domain.StewardNotificationEndpoint{ID: channel, Channel: channel, Name: channel, Enabled: true}
}
