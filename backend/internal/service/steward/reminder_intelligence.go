package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	ReminderFeedbackOpened       = "opened"
	ReminderFeedbackActed        = "acted"
	ReminderFeedbackAcknowledged = "acknowledged"
	ReminderFeedbackSnoozed      = "snoozed"
	ReminderFeedbackDismissed    = "dismissed"
	ReminderFeedbackIgnored      = "ignored"
	ReminderFeedbackCancelled    = "cancelled"
	ReminderFeedbackAutoResolved = "auto_resolved"
)

type StewardReminderFeedback struct {
	ID               string         `json:"id"`
	NotificationID   string         `json:"notification_id"`
	ScheduleRevision int            `json:"schedule_revision"`
	PolicyID         *string        `json:"policy_id,omitempty"`
	Action           string         `json:"action"`
	DeviceID         string         `json:"device_id"`
	Channel          string         `json:"channel"`
	Category         string         `json:"category"`
	Timezone         string         `json:"timezone"`
	ActivityContext  string         `json:"activity_context"`
	ResponseSeconds  *int           `json:"response_seconds,omitempty"`
	SnoozeSeconds    *int           `json:"snooze_seconds,omitempty"`
	NewScheduledAt   *time.Time     `json:"new_scheduled_at,omitempty"`
	IdempotencyKey   string         `json:"idempotency_key"`
	Metadata         map[string]any `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
}

type StewardReceptivityWindow struct {
	ID                  string    `json:"id"`
	ProfileScope        string    `json:"profile_scope"`
	Category            string    `json:"category"`
	Weekday             int       `json:"weekday"`
	TimeSlot            int       `json:"time_slot"`
	ActivityContext     string    `json:"activity_context"`
	DeviceID            string    `json:"device_id"`
	Channel             string    `json:"channel"`
	SampleCount         int       `json:"sample_count"`
	OpenedCount         int       `json:"opened_count"`
	ActedCount          int       `json:"acted_count"`
	AcknowledgedCount   int       `json:"acknowledged_count"`
	SnoozedCount        int       `json:"snoozed_count"`
	DismissedCount      int       `json:"dismissed_count"`
	IgnoredCount        int       `json:"ignored_count"`
	CancelledCount      int       `json:"cancelled_count"`
	AutoResolvedCount   int       `json:"auto_resolved_count"`
	MeanResponseSeconds float64   `json:"mean_response_seconds"`
	Confidence          float64   `json:"confidence"`
	Score               float64   `json:"score"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type StewardReminderPolicy struct {
	ID               string         `json:"id"`
	ProfileScope     string         `json:"profile_scope"`
	Category         string         `json:"category"`
	Version          int64          `json:"version"`
	Status           string         `json:"status"`
	Policy           map[string]any `json:"policy"`
	Rationale        string         `json:"rationale"`
	EvidenceManifest []string       `json:"evidence_manifest"`
	SourceEpisodeID  *string        `json:"source_episode_id,omitempty"`
	SupersedesID     *string        `json:"supersedes_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type UpdateReminderPolicyInput struct {
	ProfileScope     string         `json:"profile_scope"`
	Category         string         `json:"category"`
	Policy           map[string]any `json:"policy"`
	Rationale        string         `json:"rationale"`
	EvidenceManifest []string       `json:"evidence_manifest"`
	SourceEpisodeID  *string        `json:"source_episode_id"`
	IdempotencyKey   string         `json:"-"`
}

func defaultReminderPolicy() map[string]any {
	return map[string]any{
		"quiet_hours":          map[string]any{"start": "23:00", "end": "08:00", "mode": "soft"},
		"daily_soft_budget":    8,
		"category_soft_budget": 3,
		"cooldown_seconds":     20 * 60,
		"preferred_windows":    []any{},
		"preferred_channels":   []any{"system"},
	}
}

// mergeIntelligenceReminderPolicy applies user-facing schedule controls to a
// model-authored policy without replacing any unrelated learned fields. The
// nested quiet-hours map is copied as well, so callers never mutate the active
// version in memory while preparing its successor.
func mergeIntelligenceReminderPolicy(base map[string]any, settings IntelligenceSettings) map[string]any {
	merged := map[string]any{}
	if encoded, err := json.Marshal(base); err == nil {
		_ = json.Unmarshal(encoded, &merged)
	}
	if len(merged) == 0 {
		merged = defaultReminderPolicy()
	}
	quiet := map[string]any{}
	if existing, ok := merged["quiet_hours"].(map[string]any); ok {
		for key, value := range existing {
			quiet[key] = value
		}
	}
	if _, ok := quiet["mode"]; !ok {
		quiet["mode"] = "soft"
	}
	quiet["start"] = settings.QuietStartLocal
	quiet["end"] = settings.QuietEndLocal
	merged["quiet_hours"] = quiet
	merged["daily_soft_budget"] = settings.ReminderDailySoftBudget
	merged["category_soft_budget"] = settings.ReminderCategorySoftBudget
	merged["cooldown_seconds"] = settings.ReminderCooldownSeconds
	return merged
}

func normalizeReminderFeedbackAction(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "open", "view", ReminderFeedbackOpened:
		return ReminderFeedbackOpened, nil
	case "act", ReminderFeedbackActed, "complete", "completed":
		return ReminderFeedbackActed, nil
	case "ack", "acknowledge", ReminderFeedbackAcknowledged:
		return ReminderFeedbackAcknowledged, nil
	case "snooze", ReminderFeedbackSnoozed:
		return ReminderFeedbackSnoozed, nil
	case "dismiss", ReminderFeedbackDismissed:
		return ReminderFeedbackDismissed, nil
	case ReminderFeedbackIgnored:
		return ReminderFeedbackIgnored, nil
	case "cancel", ReminderFeedbackCancelled:
		return ReminderFeedbackCancelled, nil
	case "resolve", ReminderFeedbackAutoResolved:
		return ReminderFeedbackAutoResolved, nil
	default:
		return "", fmt.Errorf("decision must be opened, acted, acknowledged, snoozed, dismissed, ignored, cancelled, auto_resolved, or resend")
	}
}

func isTerminalNotificationStatus(status string) bool {
	switch status {
	case notificationStatusAcknowledged, notificationStatusDismissed, notificationStatusAutoResolved, notificationStatusCancelled:
		return true
	default:
		return false
	}
}

func feedbackPolarity(action string) (positive, snoozed, negative int) {
	switch action {
	case ReminderFeedbackOpened, ReminderFeedbackActed, ReminderFeedbackAcknowledged, ReminderFeedbackAutoResolved:
		return 1, 0, 0
	case ReminderFeedbackSnoozed:
		return 0, 1, 0
	case ReminderFeedbackDismissed, ReminderFeedbackIgnored, ReminderFeedbackCancelled:
		return 0, 0, 1
	default:
		return 0, 0, 0
	}
}

func receptivityScore(positive, snoozed, negative int) float64 {
	total := positive + snoozed + negative
	if total == 0 {
		return 0
	}
	// A snooze is useful scheduling evidence, not a rejection. It contributes
	// a small negative weight so preferred windows drift away from it without
	// treating it like a dismissal.
	return (float64(positive) - float64(negative) - 0.25*float64(snoozed)) / float64(total)
}

func (s *Service) RecordReminderFeedback(ctx context.Context, notificationID string, input NotificationDecisionInput) (StewardReminderFeedback, error) {
	action, err := normalizeReminderFeedbackAction(input.Decision)
	if err != nil {
		return StewardReminderFeedback{}, err
	}
	now := time.Now().UTC()
	if input.OccurredAt != nil {
		now = input.OccurredAt.UTC()
	}
	metadata := cloneStringAnyMap(input.Metadata)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return StewardReminderFeedback{}, err
	}
	defer tx.Rollback(ctx)

	var category string
	var scheduleRevision int
	var policyID *string
	var scheduledAt time.Time
	var acceptedAt *time.Time
	var decisionContextJSON []byte
	if err := tx.QueryRow(ctx, `
		select n.category,n.schedule_revision,n.policy_id::text,n.scheduled_at,n.decision_context,
		       (select min(d.accepted_at) from steward_notification_deliveries d
		        where d.notification_id=n.id and d.schedule_revision=n.schedule_revision and d.accepted_at is not null)
		from steward_notifications n where n.id=$1 for update
	`, notificationID).Scan(&category, &scheduleRevision, &policyID, &scheduledAt, &decisionContextJSON, &acceptedAt); err != nil {
		return StewardReminderFeedback{}, err
	}
	decisionContext := map[string]any{}
	_ = json.Unmarshal(decisionContextJSON, &decisionContext)
	channel := strings.TrimSpace(input.Channel)
	if channel == "" {
		_ = tx.QueryRow(ctx, `
			select channel from steward_notification_deliveries
			where notification_id=$1 and schedule_revision=$2 and accepted_at is not null
			order by accepted_at limit 1
		`, notificationID, scheduleRevision).Scan(&channel)
	}
	responseFrom := scheduledAt.UTC()
	if acceptedAt != nil {
		responseFrom = acceptedAt.UTC()
	}
	deviceID, timezone, activityContext := strings.TrimSpace(input.DeviceID), strings.TrimSpace(input.Timezone), strings.TrimSpace(input.ActivityContext)
	if deviceID == "" {
		deviceID = firstContextString(metadata, decisionContext, "device_id", "target_device_id")
	}
	if timezone == "" {
		timezone = firstContextString(metadata, decisionContext, "timezone")
	}
	if activityContext == "" {
		activityContext = firstContextString(metadata, decisionContext, "activity_context", "canonical_context")
	}
	if timezone == "" {
		_ = tx.QueryRow(ctx, `select timezone from steward_intelligence_settings where id=$1`, defaultIntelligenceSettingsID).Scan(&timezone)
	}
	if deviceID == "" || activityContext == "" {
		var observedDeviceID, observedContext string
		if err := tx.QueryRow(ctx, `
			select device_id,coalesce(nullif(canonical_context,''),nullif(context_key,''),nullif(title,''),type)
			from steward_activity_sessions
			where started_at <= $1::timestamptz + interval '5 minutes'
			  and ended_at >= $1::timestamptz - interval '30 minutes'
			order by case when started_at <= $1 and ended_at >= $1 then 0 else 1 end,
			         abs(extract(epoch from ($1::timestamptz-ended_at))),updated_at desc
			limit 1
		`, responseFrom).Scan(&observedDeviceID, &observedContext); err == nil {
			if deviceID == "" {
				deviceID = strings.TrimSpace(observedDeviceID)
			}
			if activityContext == "" {
				activityContext = strings.TrimSpace(observedContext)
			}
		}
	}
	if deviceID == "" {
		deviceID = s.agentIDValue()
	}
	timezone = defaultString(timezone, time.Local.String())
	metadata["context_enriched"] = true
	metadataJSON, _ := json.Marshal(metadata)
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("notification:%s:%d:%s:%s:%s", notificationID, scheduleRevision, action,
			deviceID, channel)
	}
	responseSeconds := max(0, int(now.Sub(responseFrom).Seconds()))
	feedback := StewardReminderFeedback{
		ID: uuid.NewString(), NotificationID: notificationID, ScheduleRevision: scheduleRevision,
		PolicyID: policyID,
		Action:   action, DeviceID: deviceID, Channel: channel,
		Category: defaultString(strings.TrimSpace(category), "general"), Timezone: timezone,
		ActivityContext: activityContext, IdempotencyKey: idempotencyKey,
		Metadata: metadata, ResponseSeconds: &responseSeconds, CreatedAt: now,
	}
	if action == ReminderFeedbackSnoozed {
		seconds := input.SnoozeSeconds
		if seconds <= 0 {
			seconds = 30 * 60
		}
		feedback.SnoozeSeconds = &seconds
		when := now.Add(time.Duration(seconds) * time.Second)
		feedback.NewScheduledAt = &when
	}
	inserted := false
	err = tx.QueryRow(ctx, `
		insert into steward_reminder_feedback(
			id,notification_id,schedule_revision,policy_id,action,category,channel,device_id,timezone,
			activity_context,response_seconds,snooze_seconds,new_scheduled_at,metadata,idempotency_key,created_at
		) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		on conflict(idempotency_key) do nothing
		returning created_at
	`, feedback.ID, feedback.NotificationID, feedback.ScheduleRevision, feedback.PolicyID, feedback.Action,
		feedback.Category, feedback.Channel, feedback.DeviceID, feedback.Timezone, feedback.ActivityContext,
		feedback.ResponseSeconds, feedback.SnoozeSeconds, feedback.NewScheduledAt, metadataJSON,
		feedback.IdempotencyKey, feedback.CreatedAt).Scan(&feedback.CreatedAt)
	if err == nil {
		inserted = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StewardReminderFeedback{}, err
	}
	if !inserted {
		if err := scanReminderFeedback(tx.QueryRow(ctx, `
			select id::text,notification_id::text,schedule_revision,policy_id::text,action,category,channel,
			       device_id,timezone,activity_context,response_seconds,snooze_seconds,new_scheduled_at,
			       idempotency_key,metadata,created_at
			from steward_reminder_feedback where idempotency_key=$1
		`, idempotencyKey), &feedback); err != nil {
			return StewardReminderFeedback{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return StewardReminderFeedback{}, err
		}
		return feedback, nil
	}

	if _, err := tx.Exec(ctx, `
		insert into steward_notification_interactions(id,notification_id,action,device_id,metadata)
		values($1,$2,$3,$4,$5)
	`, uuid.NewString(), notificationID, action, feedback.DeviceID, metadataJSON); err != nil {
		return StewardReminderFeedback{}, err
	}
	if err := applyFeedbackNotificationState(ctx, tx, notificationID, action, input.SnoozeSeconds, now); err != nil {
		return StewardReminderFeedback{}, err
	}
	if err := upsertReceptivityWindow(ctx, tx, feedback); err != nil {
		return StewardReminderFeedback{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StewardReminderFeedback{}, err
	}
	return feedback, nil
}

func applyFeedbackNotificationState(ctx context.Context, tx pgx.Tx, notificationID, action string, snoozeSeconds int, now time.Time) error {
	switch action {
	case ReminderFeedbackOpened:
		_, err := tx.Exec(ctx, `update steward_notifications set updated_at=$2 where id=$1`, notificationID, now)
		return err
	case ReminderFeedbackSnoozed:
		if snoozeSeconds <= 0 {
			snoozeSeconds = 30 * 60
		}
		return rescheduleNotificationTx(ctx, tx, notificationID, now.Add(time.Duration(snoozeSeconds)*time.Second), now)
	case ReminderFeedbackActed, ReminderFeedbackAcknowledged:
		return terminateNotificationTx(ctx, tx, notificationID, notificationStatusAcknowledged, now, true)
	case ReminderFeedbackAutoResolved:
		return terminateNotificationTx(ctx, tx, notificationID, notificationStatusAutoResolved, now, true)
	case ReminderFeedbackDismissed, ReminderFeedbackIgnored:
		return terminateNotificationTx(ctx, tx, notificationID, notificationStatusDismissed, now, false)
	case ReminderFeedbackCancelled:
		return terminateNotificationTx(ctx, tx, notificationID, notificationStatusCancelled, now, false)
	default:
		return nil
	}
}

func terminateNotificationTx(ctx context.Context, tx pgx.Tx, id, status string, now time.Time, acknowledged bool) error {
	if acknowledged {
		if _, err := tx.Exec(ctx, `update steward_notifications set status=$2,acknowledged_at=$3,updated_at=$3 where id=$1`, id, status, now); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `update steward_notifications set status=$2,cancelled_at=$3,updated_at=$3 where id=$1`, id, status, now); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `
		update steward_notification_deliveries set status=$2,lease_owner='',lease_expires_at=null,updated_at=$3
		where notification_id=$1 and status in ('queued','retrying','sending')
	`, id, deliveryStatusCancelled, now)
	return err
}

func (s *Service) rescheduleNotification(ctx context.Context, id string, when time.Time, action string, input NotificationDecisionInput) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	if err := rescheduleNotificationTx(ctx, tx, id, when.UTC(), now); err != nil {
		return err
	}
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, _ := json.Marshal(metadata)
	if _, err := tx.Exec(ctx, `insert into steward_notification_interactions(id,notification_id,action,device_id,metadata) values($1,$2,$3,$4,$5)`, uuid.NewString(), id, action, strings.TrimSpace(input.DeviceID), metadataJSON); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	var deliveryCount int
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) from steward_notification_deliveries d
		join steward_notifications n on n.id=d.notification_id
		where n.id=$1 and d.schedule_revision=n.schedule_revision
	`, id).Scan(&deliveryCount); err != nil || deliveryCount > 0 {
		return err
	}
	notification, err := s.GetNotification(ctx, id)
	if err != nil {
		return err
	}
	scheduledAt := notification.ScheduledAt
	return s.createNotificationDeliveries(ctx, id, CreateNotificationInput{
		Priority: notification.Priority, ScheduledAt: &scheduledAt, ExpiresAt: notification.ExpiresAt,
	})
}

func rescheduleNotificationTx(ctx context.Context, tx pgx.Tx, id string, when, now time.Time) error {
	var currentRevision int
	if err := tx.QueryRow(ctx, `select schedule_revision from steward_notifications where id=$1 for update`, id).Scan(&currentRevision); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update steward_notification_deliveries set status=$3,lease_owner='',lease_expires_at=null,updated_at=$4
		where notification_id=$1 and schedule_revision=$2 and status in ('queued','retrying','sending')
	`, id, currentRevision, deliveryStatusCancelled, now); err != nil {
		return err
	}
	nextRevision := currentRevision + 1
	if _, err := tx.Exec(ctx, `
		insert into steward_notification_deliveries(
			id,notification_id,endpoint_id,channel,status,schedule_revision,attempt_count,max_attempts,next_attempt_at
		)
		select gen_random_uuid(),notification_id,endpoint_id,channel,$3,$4,0,max_attempts,$5
		from steward_notification_deliveries
		where notification_id=$1 and schedule_revision=$2
	`, id, currentRevision, deliveryStatusQueued, nextRevision, when); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		update steward_notifications
		set status=$2,schedule_revision=$3,scheduled_at=$4,acknowledged_at=null,cancelled_at=null,updated_at=$5
		where id=$1
	`, id, notificationStatusQueued, nextRevision, when, now)
	return err
}

func upsertReceptivityWindow(ctx context.Context, tx pgx.Tx, feedback StewardReminderFeedback) error {
	// Receptivity belongs to the presentation window, not the later moment at
	// which the user acted or an ignored signal was derived.
	presentedAt := feedback.CreatedAt
	if feedback.ResponseSeconds != nil && *feedback.ResponseSeconds >= 0 {
		presentedAt = presentedAt.Add(-time.Duration(*feedback.ResponseSeconds) * time.Second)
	}
	localTime := presentedAt
	if feedback.Timezone == "Local" {
		localTime = presentedAt.In(time.Local)
	} else if location, err := time.LoadLocation(feedback.Timezone); err == nil {
		localTime = presentedAt.In(location)
	}
	weekday := int(localTime.Weekday())
	timeSlot := localTime.Hour()*2 + localTime.Minute()/30
	counters := map[string]int{}
	counters[feedback.Action] = 1
	responseSeconds := 0.0
	if feedback.ResponseSeconds != nil {
		responseSeconds = float64(*feedback.ResponseSeconds)
	}
	_, err := tx.Exec(ctx, `
		insert into steward_receptivity_windows(
			id,profile_scope,category,weekday,time_slot,activity_context,device_id,channel,sample_count,
			opened_count,acted_count,acknowledged_count,snoozed_count,dismissed_count,ignored_count,
			cancelled_count,auto_resolved_count,mean_response_seconds,confidence,created_at,updated_at
		) values($1,'default',$2,$3,$4,$5,$6,$7,1,$8,$9,$10,$11,$12,$13,$14,$15,$16,0.1,$17,$17)
		on conflict(profile_scope,category,weekday,time_slot,activity_context,device_id,channel) do update
		set sample_count=steward_receptivity_windows.sample_count+1,
		    opened_count=steward_receptivity_windows.opened_count+excluded.opened_count,
		    acted_count=steward_receptivity_windows.acted_count+excluded.acted_count,
		    acknowledged_count=steward_receptivity_windows.acknowledged_count+excluded.acknowledged_count,
		    snoozed_count=steward_receptivity_windows.snoozed_count+excluded.snoozed_count,
		    dismissed_count=steward_receptivity_windows.dismissed_count+excluded.dismissed_count,
		    ignored_count=steward_receptivity_windows.ignored_count+excluded.ignored_count,
		    cancelled_count=steward_receptivity_windows.cancelled_count+excluded.cancelled_count,
		    auto_resolved_count=steward_receptivity_windows.auto_resolved_count+excluded.auto_resolved_count,
		    mean_response_seconds=case when $18 then
		      (steward_receptivity_windows.mean_response_seconds*steward_receptivity_windows.sample_count
		       +excluded.mean_response_seconds)/(steward_receptivity_windows.sample_count+1)
		      else steward_receptivity_windows.mean_response_seconds end,
		    confidence=least(1.0,(steward_receptivity_windows.sample_count+1)::double precision/10.0),
		    updated_at=excluded.updated_at
	`, uuid.NewString(), feedback.Category, weekday, timeSlot, feedback.ActivityContext, feedback.DeviceID,
		feedback.Channel, counters[ReminderFeedbackOpened], counters[ReminderFeedbackActed],
		counters[ReminderFeedbackAcknowledged], counters[ReminderFeedbackSnoozed],
		counters[ReminderFeedbackDismissed], counters[ReminderFeedbackIgnored],
		counters[ReminderFeedbackCancelled], counters[ReminderFeedbackAutoResolved], responseSeconds,
		feedback.CreatedAt, feedback.ResponseSeconds != nil)
	return err
}

func firstContextString(primary, secondary map[string]any, keys ...string) string {
	for _, values := range []map[string]any{primary, secondary} {
		for _, key := range keys {
			if values == nil {
				continue
			}
			value, ok := values[key]
			if !ok || value == nil {
				continue
			}
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func (s *Service) ListReminderFeedback(ctx context.Context, limit int) ([]StewardReminderFeedback, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,notification_id::text,schedule_revision,policy_id::text,action,category,channel,
		       device_id,timezone,activity_context,response_seconds,snooze_seconds,new_scheduled_at,
		       idempotency_key,metadata,created_at
		from steward_reminder_feedback order by created_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StewardReminderFeedback{}
	for rows.Next() {
		var item StewardReminderFeedback
		if err := scanReminderFeedback(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ReconcileReminderFeedbackEpisodes turns every persisted reminder outcome
// into an idempotent background Agent Episode. The controller does not encode
// a business rule such as "three snoozes means move to the evening"; it gives
// the model the real feedback, learned windows and current policy tools, then
// lets the model decide whether to keep the policy, change it, ask the user or
// stay silent. Persisting the Episode before the provider call makes this loop
// recoverable across service and model outages.
func (s *Service) ReconcileReminderFeedbackEpisodes(ctx context.Context, limit int) (int, error) {
	limit = normalizeLimit(limit, 4, 50)
	rows, err := s.db.Pool.Query(ctx, `
		select f.id::text,f.notification_id::text,f.schedule_revision,f.policy_id::text,f.action,
		       f.category,f.channel,f.device_id,f.timezone,f.activity_context,f.response_seconds,
		       f.snooze_seconds,f.new_scheduled_at,f.idempotency_key,f.metadata,f.created_at
		from steward_reminder_feedback f
		where not exists (
			select 1 from steward_agent_episodes e
			where e.idempotency_key='reminder-feedback:' || f.id::text
		)
		order by f.created_at,f.id
		limit $1
	`, limit)
	if err != nil {
		return 0, err
	}
	items := []StewardReminderFeedback{}
	for rows.Next() {
		var item StewardReminderFeedback
		if err := scanReminderFeedback(rows, &item); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	if len(items) == 0 {
		return 0, nil
	}
	conversation, err := s.ensureProactiveConversation(ctx)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, item := range items {
		key := "reminder-feedback:" + item.ID
		payload, _ := json.Marshal(item)
		prompt := strings.Join([]string{
			"这是一次持久化提醒反馈后的后台学习回合。不要套用固定的 snooze 次数规则。",
			"先调用 steward.reminder.context 或 steward.reminder_feedback.query 查看完整反馈和已学习的接收窗口。",
			"由你判断是否需要更新频率、时间窗口、渠道、内容或保持当前策略；需要变更时调用 steward.reminder_policy.update，不需要变更时调用 steward.stay_silent。",
			"任何策略更新都要引用真实 feedback ID 或可验证证据，并说明理由。",
			"本次新反馈：" + truncateAdvisorText(string(payload), 8000),
		}, "\n")
		trigger, insertErr := s.insertConversationMessage(ctx, conversation.ID, conversationRoleSystem, prompt, DataD2,
			s.autonomyAdvisor().Status().Model, key)
		if insertErr != nil {
			return started, insertErr
		}
		if _, enqueueErr := s.enqueueBackgroundAgentEpisode(ctx, conversation, trigger,
			"根据真实提醒反馈自主调整提醒策略", DataD2, "reminder_policy_learning", "reminder_feedback", item.ID, key); enqueueErr != nil {
			return started, enqueueErr
		}
		started++
	}
	return started, nil
}

func scanReminderFeedback(row interface{ Scan(...any) error }, item *StewardReminderFeedback) error {
	var metadataJSON []byte
	if err := row.Scan(&item.ID, &item.NotificationID, &item.ScheduleRevision, &item.PolicyID, &item.Action,
		&item.Category, &item.Channel, &item.DeviceID, &item.Timezone, &item.ActivityContext,
		&item.ResponseSeconds, &item.SnoozeSeconds, &item.NewScheduledAt, &item.IdempotencyKey,
		&metadataJSON, &item.CreatedAt); err != nil {
		return err
	}
	item.Metadata = map[string]any{}
	_ = json.Unmarshal(metadataJSON, &item.Metadata)
	return nil
}

func (s *Service) ListReceptivityWindows(ctx context.Context, limit int) ([]StewardReceptivityWindow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,profile_scope,category,weekday,time_slot,activity_context,device_id,channel,
		       sample_count,opened_count,acted_count,acknowledged_count,snoozed_count,dismissed_count,
		       ignored_count,cancelled_count,auto_resolved_count,mean_response_seconds,confidence,
		       ((opened_count+acted_count+acknowledged_count+auto_resolved_count)::double precision
		        -(dismissed_count+ignored_count+cancelled_count)::double precision
		        -0.25*snoozed_count::double precision)/greatest(sample_count,1)::double precision as score,
		       created_at,updated_at
		from steward_receptivity_windows order by score desc,confidence desc,sample_count desc,updated_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StewardReceptivityWindow{}
	for rows.Next() {
		var item StewardReceptivityWindow
		if err := rows.Scan(&item.ID, &item.ProfileScope, &item.Category, &item.Weekday, &item.TimeSlot,
			&item.ActivityContext, &item.DeviceID, &item.Channel, &item.SampleCount, &item.OpenedCount,
			&item.ActedCount, &item.AcknowledgedCount, &item.SnoozedCount, &item.DismissedCount,
			&item.IgnoredCount, &item.CancelledCount, &item.AutoResolvedCount, &item.MeanResponseSeconds,
			&item.Confidence, &item.Score, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetReminderPolicy(ctx context.Context) (StewardReminderPolicy, error) {
	return s.GetReminderPolicyFor(ctx, "default", "*")
}

// GetReminderPolicyFor resolves the active category policy and falls back to
// the global policy. This keeps category-specific model learning effective for
// newly created notifications without requiring every category to duplicate a
// complete policy on first use.
func (s *Service) GetReminderPolicyFor(ctx context.Context, profileScope, category string) (StewardReminderPolicy, error) {
	profileScope = defaultString(strings.TrimSpace(profileScope), "default")
	category = defaultString(strings.TrimSpace(category), "*")
	item, err := scanReminderPolicy(s.db.Pool.QueryRow(ctx, `
		select id::text,profile_scope,category,version,status,policy,rationale,evidence_manifest,
		       source_episode_id::text,supersedes_id::text,created_at,updated_at
		from steward_reminder_policies
		where profile_scope=$1 and category=$2 and status='active'
		order by version desc limit 1
	`, profileScope, category))
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StewardReminderPolicy{}, err
	}
	if category != "*" {
		return s.GetReminderPolicyFor(ctx, profileScope, "*")
	}
	return s.UpdateReminderPolicy(ctx, UpdateReminderPolicyInput{
		ProfileScope: profileScope, Category: "*",
		Policy: defaultReminderPolicy(), Rationale: "初始软策略；模型可根据真实反馈持续调整。",
	})
}

func (s *Service) ListActiveReminderPolicies(ctx context.Context, profileScope string) ([]StewardReminderPolicy, error) {
	profileScope = defaultString(strings.TrimSpace(profileScope), "default")
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,profile_scope,category,version,status,policy,rationale,evidence_manifest,
		       source_episode_id::text,supersedes_id::text,created_at,updated_at
		from steward_reminder_policies
		where profile_scope=$1 and status='active'
		order by category,version desc
	`, profileScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StewardReminderPolicy{}
	for rows.Next() {
		item, scanErr := scanReminderPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) reminderContextSnapshot(ctx context.Context) (map[string]any, error) {
	policy, err := s.GetReminderPolicy(ctx)
	if err != nil {
		return nil, err
	}
	windows, err := s.ListReceptivityWindows(ctx, 24)
	if err != nil {
		return nil, err
	}
	feedback, err := s.ListReminderFeedback(ctx, 30)
	if err != nil {
		return nil, err
	}
	var created24h, active int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) filter(where created_at>=now()-interval '24 hours'),
		count(*) filter(where status in ('queued','sent')) from steward_notifications`).Scan(&created24h, &active); err != nil {
		return nil, err
	}
	policies, err := s.ListActiveReminderPolicies(ctx, "default")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"policy": policy, "active_policies": policies, "receptivity_windows": windows, "recent_feedback": feedback,
		"notification_counts": map[string]int{"created_last_24_hours": created24h, "currently_active": active},
		"guidance":            "These are soft preferences and learned evidence. The model decides whether to stay silent, ask, remind, schedule, or help directly.",
		"checked_at":          time.Now().UTC(),
	}, nil
}

func (s *Service) UpdateReminderPolicy(ctx context.Context, input UpdateReminderPolicyInput) (StewardReminderPolicy, error) {
	if len(input.Policy) == 0 {
		return StewardReminderPolicy{}, fmt.Errorf("reminder policy is required")
	}
	if err := validateReminderPolicy(input.Policy); err != nil {
		return StewardReminderPolicy{}, err
	}
	input.ProfileScope = defaultString(strings.TrimSpace(input.ProfileScope), "default")
	input.Category = defaultString(strings.TrimSpace(input.Category), "*")
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.SourceEpisodeID != nil && strings.TrimSpace(*input.SourceEpisodeID) == "" {
		input.SourceEpisodeID = nil
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return StewardReminderPolicy{}, err
	}
	defer tx.Rollback(ctx)
	var version int64
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "steward-reminder-policy:"+input.ProfileScope+":"+input.Category); err != nil {
		return StewardReminderPolicy{}, err
	}
	if input.IdempotencyKey != "" {
		existing, existingErr := scanReminderPolicy(tx.QueryRow(ctx, `
			select id::text,profile_scope,category,version,status,policy,rationale,evidence_manifest,
			       source_episode_id::text,supersedes_id::text,created_at,updated_at
			from steward_reminder_policies where idempotency_key=$1
		`, input.IdempotencyKey))
		if existingErr == nil {
			if err := tx.Commit(ctx); err != nil {
				return StewardReminderPolicy{}, err
			}
			return existing, nil
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return StewardReminderPolicy{}, existingErr
		}
	}
	if err := tx.QueryRow(ctx, `select coalesce(max(version),0) from steward_reminder_policies where profile_scope=$1 and category=$2`, input.ProfileScope, input.Category).Scan(&version); err != nil {
		return StewardReminderPolicy{}, err
	}
	now := time.Now().UTC()
	var supersedesID *string
	if err := tx.QueryRow(ctx, `
		select id::text from steward_reminder_policies
		where profile_scope=$1 and category=$2 and status='active'
		order by version desc limit 1 for update
	`, input.ProfileScope, input.Category).Scan(&supersedesID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StewardReminderPolicy{}, err
	}
	if _, err := tx.Exec(ctx, `
		update steward_reminder_policies set status='superseded',updated_at=$3
		where profile_scope=$1 and category=$2 and status='active'
	`, input.ProfileScope, input.Category, now); err != nil {
		return StewardReminderPolicy{}, err
	}
	id := uuid.NewString()
	policyJSON, _ := json.Marshal(input.Policy)
	evidenceJSON, _ := json.Marshal(normalizeStringSlice(input.EvidenceManifest))
	if _, err := tx.Exec(ctx, `
		insert into steward_reminder_policies(
			id,profile_scope,category,version,status,policy,rationale,evidence_manifest,
			source_episode_id,supersedes_id,idempotency_key,created_at,updated_at
		) values($1,$2,$3,$4,'active',$5,$6,$7,nullif($8,'')::uuid,nullif($9,'')::uuid,$10,$11,$11)
	`, id, input.ProfileScope, input.Category, version+1, policyJSON, strings.TrimSpace(input.Rationale),
		evidenceJSON, stringValue(input.SourceEpisodeID), stringValue(supersedesID), input.IdempotencyKey, now); err != nil {
		return StewardReminderPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StewardReminderPolicy{}, err
	}
	return scanReminderPolicy(s.db.Pool.QueryRow(ctx, `
		select id::text,profile_scope,category,version,status,policy,rationale,evidence_manifest,
		       source_episode_id::text,supersedes_id::text,created_at,updated_at
		from steward_reminder_policies where id=$1
	`, id))
}

// syncReminderPolicyFromIntelligenceSettingsTx creates a normal immutable
// policy version in the same transaction as the settings revision. This keeps
// delivery behavior and the settings page consistent after the update returns,
// while retaining every model-learned key that the user did not override.
func (s *Service) syncReminderPolicyFromIntelligenceSettingsTx(
	ctx context.Context,
	tx pgx.Tx,
	settings IntelligenceSettings,
	settingsRevision int64,
) error {
	const profileScope = "default"
	const category = "*"
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "steward-reminder-policy:"+profileScope+":"+category); err != nil {
		return err
	}

	var (
		activeID     string
		activePolicy []byte
		evidenceJSON []byte
	)
	err := tx.QueryRow(ctx, `
		select id::text,policy,evidence_manifest
		from steward_reminder_policies
		where profile_scope=$1 and category=$2 and status='active'
		order by version desc limit 1 for update
	`, profileScope, category).Scan(&activeID, &activePolicy, &evidenceJSON)
	base := defaultReminderPolicy()
	if err == nil {
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal(activePolicy, &decoded); decodeErr != nil {
			return fmt.Errorf("decode active reminder policy: %w", decodeErr)
		}
		base = decoded
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	policy := mergeIntelligenceReminderPolicy(base, settings)
	if err := validateReminderPolicy(policy); err != nil {
		return err
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(version),0)
		from steward_reminder_policies
		where profile_scope=$1 and category=$2
	`, profileScope, category).Scan(&version); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		update steward_reminder_policies set status='superseded',updated_at=$3
		where profile_scope=$1 and category=$2 and status='active'
	`, profileScope, category, now); err != nil {
		return err
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if len(evidenceJSON) == 0 {
		evidenceJSON = []byte("[]")
	}
	rationale := fmt.Sprintf(
		"用户更新持续智能设置（revision %d）；同步安静时段、软预算和冷却时间，并继承上一版模型策略的其他字段。",
		settingsRevision,
	)
	_, err = tx.Exec(ctx, `
		insert into steward_reminder_policies(
			id,profile_scope,category,version,status,policy,rationale,evidence_manifest,
			source_episode_id,supersedes_id,created_at,updated_at
		) values($1,$2,$3,$4,'active',$5,$6,$7,null,nullif($8,'')::uuid,$9,$9)
	`, uuid.NewString(), profileScope, category, version+1, policyJSON, rationale, evidenceJSON, activeID, now)
	return err
}

func scanReminderPolicy(row interface{ Scan(...any) error }) (StewardReminderPolicy, error) {
	var item StewardReminderPolicy
	var policyJSON, evidenceJSON []byte
	err := row.Scan(&item.ID, &item.ProfileScope, &item.Category, &item.Version, &item.Status, &policyJSON,
		&item.Rationale, &evidenceJSON, &item.SourceEpisodeID, &item.SupersedesID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	item.Policy = map[string]any{}
	item.EvidenceManifest = []string{}
	_ = json.Unmarshal(policyJSON, &item.Policy)
	_ = json.Unmarshal(evidenceJSON, &item.EvidenceManifest)
	return item, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func validateReminderPolicy(policy map[string]any) error {
	for _, key := range []string{"daily_soft_budget", "category_soft_budget", "cooldown_seconds"} {
		if value, ok := policy[key]; ok {
			number, ok := numericValue(value)
			if !ok || number < 0 {
				return fmt.Errorf("reminder policy %s must be a non-negative number", key)
			}
		}
	}
	if quiet, ok := policy["quiet_hours"].(map[string]any); ok {
		for _, key := range []string{"start", "end"} {
			value := strings.TrimSpace(fmt.Sprint(quiet[key]))
			if _, err := time.Parse("15:04", value); err != nil {
				return fmt.Errorf("reminder policy quiet_hours.%s must use HH:MM", key)
			}
		}
	}
	if value, exists := policy["preferred_windows"]; exists && len(parsePreferredReminderWindows(value)) == 0 {
		if items, ok := value.([]any); !ok || len(items) > 0 {
			return fmt.Errorf("reminder policy preferred_windows must contain HH:MM or HH:MM-HH:MM windows")
		}
	}
	if value, exists := policy["preferred_channels"]; exists {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("reminder policy preferred_channels must be an array")
		}
		for _, item := range items {
			channel := strings.ToLower(strings.TrimSpace(fmt.Sprint(item)))
			if !validNotificationChannel(channel) {
				return fmt.Errorf("reminder policy preferred_channels contains unsupported channel %q", channel)
			}
		}
	}
	return nil
}

func numericValue(value any) (float64, bool) {
	switch item := value.(type) {
	case int:
		return float64(item), true
	case int64:
		return float64(item), true
	case float64:
		return item, true
	case json.Number:
		value, err := item.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func normalizeStringSlice(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// InferIgnoredReminderFeedback derives an ignored signal only for delivery
// channels that declare reliable feedback support. Absence of an email open or
// a best-effort push receipt is never treated as user rejection.
func (s *Service) InferIgnoredReminderFeedback(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select distinct n.id::text,d.channel,n.priority,d.accepted_at
		from steward_notifications n
		join steward_notification_deliveries d on d.notification_id=n.id and d.schedule_revision=n.schedule_revision
		left join steward_notification_endpoints e on e.id=d.endpoint_id
		where n.status='sent' and d.status='accepted' and d.accepted_at is not null
		  and jsonb_array_length(n.actions) > 0
		  and lower(coalesce(e.config->>'feedback_capable','false')) in ('true','1','yes','on')
		  and not exists(select 1 from steward_reminder_feedback f where f.notification_id=n.id and f.schedule_revision=n.schedule_revision)
		  and d.accepted_at <= $1::timestamptz - case n.priority
		    when 'urgent' then interval '30 minutes' when 'high' then interval '2 hours'
		    when 'low' then interval '24 hours' else interval '8 hours' end
		order by d.accepted_at limit $2
	`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		id, channel, priority string
		acceptedAt            time.Time
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.channel, &item.priority, &item.acceptedAt); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	processed := 0
	for _, item := range candidates {
		_, err := s.RecordReminderFeedback(ctx, item.id, NotificationDecisionInput{
			Decision: ReminderFeedbackIgnored, Channel: item.channel, OccurredAt: &now,
			Metadata: map[string]any{"derived": true},
		})
		if err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}
