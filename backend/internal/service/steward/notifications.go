package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	notificationStatusQueued       = "queued"
	notificationStatusSent         = "sent"
	notificationStatusAcknowledged = "acknowledged"
	notificationStatusDismissed    = "dismissed"
	notificationStatusAutoResolved = "auto_resolved"
	notificationStatusCancelled    = "cancelled"
	notificationStatusFailed       = "failed"

	deliveryStatusQueued    = "queued"
	deliveryStatusRetrying  = "retrying"
	deliveryStatusSending   = "sending"
	deliveryStatusAccepted  = "accepted"
	deliveryStatusFailed    = "failed"
	deliveryStatusExpired   = "expired"
	deliveryStatusCancelled = "cancelled"
)

type CreateNotificationInput struct {
	SourceType         string                             `json:"source_type"`
	SourceID           string                             `json:"source_id"`
	Title              string                             `json:"title"`
	Body               string                             `json:"body"`
	Category           string                             `json:"category"`
	Priority           string                             `json:"priority"`
	ScheduledAt        *time.Time                         `json:"scheduled_at"`
	AllowedWindowStart *time.Time                         `json:"allowed_window_start"`
	AllowedWindowEnd   *time.Time                         `json:"allowed_window_end"`
	ExpiresAt          *time.Time                         `json:"expires_at"`
	DedupeKey          string                             `json:"dedupe_key"`
	Channels           []string                           `json:"channels"`
	Actions            []domain.StewardNotificationAction `json:"actions"`
	Metadata           map[string]any                     `json:"metadata"`
	PolicyID           *string                            `json:"policy_id"`
	DecisionContext    map[string]any                     `json:"decision_context"`
}

type UpdateNotificationEndpointInput struct {
	Channel string         `json:"channel"`
	Name    string         `json:"name"`
	Enabled *bool          `json:"enabled"`
	Config  map[string]any `json:"config"`
	Secret  map[string]any `json:"secret"`
}

type NotificationDecisionInput struct {
	Decision        string         `json:"decision"`
	SnoozeSeconds   int            `json:"snooze_seconds"`
	DeviceID        string         `json:"device_id"`
	Channel         string         `json:"channel"`
	Timezone        string         `json:"timezone"`
	ActivityContext string         `json:"activity_context"`
	IdempotencyKey  string         `json:"idempotency_key"`
	OccurredAt      *time.Time     `json:"occurred_at"`
	Metadata        map[string]any `json:"metadata"`
}

type notificationEndpointRecord struct {
	domain.StewardNotificationEndpoint
	Secret map[string]any
}

func (s *Service) CreateNotification(ctx context.Context, input CreateNotificationInput) (domain.StewardNotification, error) {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	if input.Title == "" || input.Body == "" {
		return domain.StewardNotification{}, fmt.Errorf("notification title and body are required")
	}
	input.SourceType = defaultString(strings.TrimSpace(input.SourceType), "agent")
	input.Category = defaultString(strings.TrimSpace(input.Category), "general")
	input.Priority = normalizeNotificationPriority(input.Priority)
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	if input.DecisionContext == nil {
		input.DecisionContext = map[string]any{}
	}
	var selectedPolicy *StewardReminderPolicy
	if input.PolicyID == nil {
		policy, policyErr := s.GetReminderPolicyFor(ctx, "default", input.Category)
		if policyErr != nil {
			return domain.StewardNotification{}, fmt.Errorf("select reminder policy: %w", policyErr)
		}
		selectedPolicy = &policy
		input.PolicyID = &policy.ID
		input.DecisionContext["policy_version"] = policy.Version
		input.DecisionContext["policy_category"] = policy.Category
	}
	if input.ScheduledAt == nil {
		now := time.Now().UTC()
		input.ScheduledAt = &now
	}
	requestedAt := input.ScheduledAt.UTC()
	if selectedPolicy != nil {
		if err := s.applyNotificationReminderPolicy(ctx, &input, *selectedPolicy); err != nil {
			return domain.StewardNotification{}, err
		}
	}
	if err := clampNotificationDeliveryWindow(&input, requestedAt, time.Now().UTC()); err != nil {
		return domain.StewardNotification{}, err
	}
	if err := validateNotificationActions(input.Actions); err != nil {
		return domain.StewardNotification{}, err
	}
	if err := s.ensureDefaultNotificationEndpoints(ctx); err != nil {
		return domain.StewardNotification{}, err
	}

	id := uuid.NewString()
	actionsJSON, _ := json.Marshal(input.Actions)
	metadataJSON, _ := json.Marshal(input.Metadata)
	decisionContextJSON, _ := json.Marshal(input.DecisionContext)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_notifications (
			id,source_type,source_id,title,body,category,priority,status,schedule_revision,scheduled_at,
			allowed_window_start,allowed_window_end,expires_at,dedupe_key,actions,metadata,policy_id,decision_context
		) values ($1,$2,$3,$4,$5,$6,$7,$8,1,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		on conflict (dedupe_key) where dedupe_key <> '' do nothing
	`, id, input.SourceType, strings.TrimSpace(input.SourceID), input.Title, input.Body, input.Category, input.Priority,
		notificationStatusQueued, input.ScheduledAt, input.AllowedWindowStart, input.AllowedWindowEnd, input.ExpiresAt,
		strings.TrimSpace(input.DedupeKey), actionsJSON, metadataJSON, input.PolicyID, decisionContextJSON)
	if err != nil {
		return domain.StewardNotification{}, fmt.Errorf("create notification: %w", err)
	}
	if input.DedupeKey != "" {
		var existing string
		if err := s.db.Pool.QueryRow(ctx, `select id::text from steward_notifications where dedupe_key=$1`, strings.TrimSpace(input.DedupeKey)).Scan(&existing); err != nil {
			return domain.StewardNotification{}, err
		}
		id = existing
	}

	var deliveryCount int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_notification_deliveries where notification_id=$1`, id).Scan(&deliveryCount); err != nil {
		return domain.StewardNotification{}, err
	}
	if deliveryCount == 0 {
		if err := s.createNotificationDeliveries(ctx, id, input); err != nil {
			return domain.StewardNotification{}, err
		}
	}
	return s.GetNotification(ctx, id)
}

func (s *Service) ListNotifications(ctx context.Context, status string, limit int) ([]domain.StewardNotification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	status = strings.TrimSpace(status)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,source_type,source_id,title,body,category,priority,status,schedule_revision,scheduled_at,expires_at,dedupe_key,
		       allowed_window_start,allowed_window_end,actions,metadata,policy_id::text,decision_context,acknowledged_at,cancelled_at,created_at,updated_at
		from steward_notifications
		where ($1='' or status=$1)
		order by scheduled_at desc, created_at desc limit $2
	`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardNotification{}
	for rows.Next() {
		item, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		item.Deliveries, err = s.listNotificationDeliveries(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetNotification(ctx context.Context, id string) (domain.StewardNotification, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id::text,source_type,source_id,title,body,category,priority,status,schedule_revision,scheduled_at,expires_at,dedupe_key,
		       allowed_window_start,allowed_window_end,actions,metadata,policy_id::text,decision_context,acknowledged_at,cancelled_at,created_at,updated_at
		from steward_notifications where id=$1
	`, id)
	item, err := scanNotification(row)
	if err != nil {
		return domain.StewardNotification{}, err
	}
	item.Deliveries, err = s.listNotificationDeliveries(ctx, item.ID)
	return item, err
}

func (s *Service) DecideNotification(ctx context.Context, id string, input NotificationDecisionInput) (domain.StewardNotification, error) {
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	if decision == "resend" {
		if err := s.rescheduleNotification(ctx, id, time.Now().UTC(), "resend", input); err != nil {
			return domain.StewardNotification{}, err
		}
		return s.GetNotification(ctx, id)
	}
	action, err := normalizeReminderFeedbackAction(decision)
	if err != nil {
		return domain.StewardNotification{}, err
	}
	input.Decision = action
	if _, err := s.RecordReminderFeedback(ctx, id, input); err != nil {
		return domain.StewardNotification{}, err
	}
	return s.GetNotification(ctx, id)
}

func (s *Service) ListNotificationEndpoints(ctx context.Context) ([]domain.StewardNotificationEndpoint, error) {
	if err := s.ensureDefaultNotificationEndpoints(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `select id::text,channel,name,enabled,config,(secret_encrypted<>'{}'::jsonb),last_success_at,last_error,created_at,updated_at from steward_notification_endpoints order by channel,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardNotificationEndpoint{}
	for rows.Next() {
		var item domain.StewardNotificationEndpoint
		if err := rows.Scan(&item.ID, &item.Channel, &item.Name, &item.Enabled, &item.Config, &item.SecretSet, &item.LastSuccessAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpsertNotificationEndpoint(ctx context.Context, input UpdateNotificationEndpointInput) (domain.StewardNotificationEndpoint, error) {
	input.Channel = strings.ToLower(strings.TrimSpace(input.Channel))
	input.Name = defaultString(strings.TrimSpace(input.Name), input.Channel)
	if !validNotificationChannel(input.Channel) {
		return domain.StewardNotificationEndpoint{}, fmt.Errorf("unsupported notification channel %q", input.Channel)
	}
	if input.Config == nil {
		input.Config = map[string]any{}
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	id := uuid.NewString()
	var existingID string
	var existingSecret map[string]any
	err := s.db.Pool.QueryRow(ctx, `select id::text,secret_encrypted from steward_notification_endpoints where channel=$1 and name=$2`, input.Channel, input.Name).Scan(&existingID, &existingSecret)
	if err == nil {
		id = existingID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardNotificationEndpoint{}, err
	}
	secretEnvelope := existingSecret
	if len(input.Secret) > 0 {
		keyring, keyErr := localPayloadKeyringFromEnv()
		if keyErr != nil {
			return domain.StewardNotificationEndpoint{}, fmt.Errorf("notification endpoint secrets require STEWARD_LOCAL_ENCRYPTION_KEY: %w", keyErr)
		}
		secretEnvelope, err = encryptPayloadEnvelope(keyring, notificationEndpointAAD(id), input.Secret, SyncEncryptionScopeLocalAtRest)
		if err != nil {
			return domain.StewardNotificationEndpoint{}, err
		}
	}
	if secretEnvelope == nil {
		secretEnvelope = map[string]any{}
	}
	configJSON, _ := json.Marshal(input.Config)
	secretJSON, _ := json.Marshal(secretEnvelope)
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_notification_endpoints(id,channel,name,enabled,config,secret_encrypted)
		values($1,$2,$3,$4,$5,$6)
		on conflict(channel,name) do update set enabled=excluded.enabled,config=excluded.config,secret_encrypted=excluded.secret_encrypted,updated_at=now()
	`, id, input.Channel, input.Name, enabled, configJSON, secretJSON)
	if err != nil {
		return domain.StewardNotificationEndpoint{}, err
	}
	items, err := s.ListNotificationEndpoints(ctx)
	if err != nil {
		return domain.StewardNotificationEndpoint{}, err
	}
	for _, item := range items {
		if item.ID == id || (item.Channel == input.Channel && item.Name == input.Name) {
			return item, nil
		}
	}
	return domain.StewardNotificationEndpoint{}, pgx.ErrNoRows
}

func (s *Service) TestNotificationEndpoint(ctx context.Context, id string) (domain.StewardNotificationEndpoint, error) {
	endpoint, err := s.getNotificationEndpoint(ctx, id)
	if err != nil {
		return domain.StewardNotificationEndpoint{}, err
	}
	now := time.Now().UTC()
	notification := domain.StewardNotification{ID: uuid.NewString(), Title: "Steward 通知测试", Body: "通知渠道配置成功。", Category: "system", Priority: "normal", ScheduledAt: now, CreatedAt: now, UpdatedAt: now}
	_, sendErr := s.sendNotification(ctx, endpoint, notification)
	if sendErr != nil {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_endpoints set last_error=$2,updated_at=now() where id=$1`, id, truncateAdvisorText(sendErr.Error(), 2000))
		return domain.StewardNotificationEndpoint{}, sendErr
	}
	_, _ = s.db.Pool.Exec(ctx, `update steward_notification_endpoints set last_success_at=now(),last_error='',updated_at=now() where id=$1`, id)
	items, err := s.ListNotificationEndpoints(ctx)
	if err != nil {
		return domain.StewardNotificationEndpoint{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.StewardNotificationEndpoint{}, pgx.ErrNoRows
}

func (s *Service) RunNotificationDeliveryCycle(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	if err := s.ensureDefaultNotificationEndpoints(ctx); err != nil {
		return 0, err
	}
	processed := 0
	var joined error
	if _, err := s.ReconcileCompletedTaskNotifications(ctx, time.Now().UTC(), limit); err != nil {
		// A reconciliation failure must remain visible, but should not prevent
		// unrelated queued notifications from being delivered.
		joined = errors.Join(joined, err)
	}
	if err := s.enqueueDueTaskNotifications(ctx, limit); err != nil {
		return 0, errors.Join(joined, err)
	}
	if _, err := s.InferIgnoredReminderFeedback(ctx, time.Now().UTC(), limit); err != nil {
		// Feedback inference is analytics. It must never prevent a real queued
		// notification from being delivered.
		joined = errors.Join(joined, err)
	}
	for processed < limit {
		deliveryID, err := s.claimNotificationDelivery(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return processed, errors.Join(joined, err)
		}
		if err := s.deliverClaimedNotification(ctx, deliveryID); err != nil {
			joined = errors.Join(joined, err)
		}
		processed++
	}
	return processed, joined
}

// ReconcileCompletedTaskNotifications closes reminders whose source task was
// completed elsewhere (conversation tool, API, another device). The feedback
// row makes the transition durable and teaches the reminder model that the
// reminder succeeded without requiring a notification-button callback.
func (s *Service) ReconcileCompletedTaskNotifications(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select n.id::text,n.schedule_revision,t.id::text,coalesce(t.completed_at,t.updated_at)
		from steward_notifications n
		join steward_tasks t on n.source_type='task' and n.source_id=t.id::text
		where t.status=$1 and n.status in ($2,$3,$4)
		  and not exists(
			select 1 from steward_reminder_feedback f
			where f.notification_id=n.id and f.schedule_revision=n.schedule_revision
			  and f.action=$5
		  )
		order by coalesce(t.completed_at,t.updated_at),n.created_at
		limit $6
	`, StatusDone, notificationStatusQueued, notificationStatusSent, notificationStatusFailed, ReminderFeedbackAutoResolved, limit)
	if err != nil {
		return 0, err
	}
	type completedTaskNotification struct {
		notificationID string
		revision       int
		taskID         string
		completedAt    time.Time
	}
	items := []completedTaskNotification{}
	for rows.Next() {
		var item completedTaskNotification
		if err := rows.Scan(&item.notificationID, &item.revision, &item.taskID, &item.completedAt); err != nil {
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
	processed := 0
	for _, item := range items {
		occurredAt := item.completedAt.UTC()
		if occurredAt.IsZero() || occurredAt.After(now) {
			occurredAt = now.UTC()
		}
		_, err := s.RecordReminderFeedback(ctx, item.notificationID, NotificationDecisionInput{
			Decision:       ReminderFeedbackAutoResolved,
			OccurredAt:     &occurredAt,
			IdempotencyKey: fmt.Sprintf("task-auto-resolved:%s:%d", item.notificationID, item.revision),
			Metadata: map[string]any{
				"derived": true, "source": "task_completion", "task_id": item.taskID,
			},
		})
		if err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (s *Service) createNotificationDeliveries(ctx context.Context, notificationID string, input CreateNotificationInput) error {
	var scheduleRevision int
	if err := s.db.Pool.QueryRow(ctx, `select schedule_revision from steward_notifications where id=$1`, notificationID).Scan(&scheduleRevision); err != nil {
		return err
	}
	endpoints, err := s.listNotificationEndpointRecords(ctx, true)
	if err != nil {
		return err
	}
	requested := map[string]bool{}
	for _, channel := range input.Channels {
		channel = strings.ToLower(strings.TrimSpace(channel))
		if !validNotificationChannel(channel) {
			return fmt.Errorf("unsupported notification channel %q", channel)
		}
		requested[channel] = true
	}
	preferred := []string{}
	if raw, ok := input.DecisionContext["preferred_channels"].([]string); ok {
		preferred = raw
	} else if raw, ok := input.DecisionContext["preferred_channels"].([]any); ok {
		for _, value := range raw {
			preferred = append(preferred, strings.ToLower(strings.TrimSpace(fmt.Sprint(value))))
		}
	}
	selected := routeNotificationEndpoints(endpoints, input.Priority, len(requested) > 0, requested, preferred)
	created := 0
	deadline := earliestNotificationDeadline(input.ExpiresAt, input.AllowedWindowEnd)
	for _, route := range selected {
		next := input.ScheduledAt.UTC().Add(route.Delay)
		if deadline != nil && next.After(deadline.UTC()) {
			// Escalation routes that would only run after the notification is no
			// longer useful are omitted instead of being queued to expire.
			if route.Delay > 0 {
				continue
			}
			next = deadline.UTC()
		}
		_, err := s.db.Pool.Exec(ctx, `
			insert into steward_notification_deliveries(id,notification_id,endpoint_id,channel,status,schedule_revision,next_attempt_at)
			values($1,$2,$3,$4,$5,$6,$7) on conflict do nothing
		`, uuid.NewString(), notificationID, route.Endpoint.ID, route.Endpoint.Channel, deliveryStatusQueued, scheduleRevision, next)
		if err != nil {
			return err
		}
		created++
	}
	if created == 0 {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notifications set status='failed',updated_at=now() where id=$1`, notificationID)
		return fmt.Errorf("no enabled notification endpoint can deliver this notification")
	}
	return nil
}

type notificationRoute struct {
	Endpoint notificationEndpointRecord
	Delay    time.Duration
}

func routeNotificationEndpoints(endpoints []notificationEndpointRecord, priority string, explicit bool, requested map[string]bool, preferredChannels ...[]string) []notificationRoute {
	priority = normalizeNotificationPriority(priority)
	routes := []notificationRoute{}
	preferredRanks := map[string]int{}
	if len(preferredChannels) > 0 {
		for index, channel := range preferredChannels[0] {
			channel = strings.ToLower(strings.TrimSpace(channel))
			if channel != "" {
				if _, exists := preferredRanks[channel]; !exists {
					preferredRanks[channel] = index
				}
			}
		}
	}
	hasLocal := false
	hasNtfy := false
	for _, endpoint := range endpoints {
		if endpoint.Channel == "system" || endpoint.Channel == "linux_desktop" {
			hasLocal = true
		}
		if endpoint.Channel == "ntfy" {
			hasNtfy = true
		}
	}
	for _, endpoint := range endpoints {
		if explicit && !requested[endpoint.Channel] {
			continue
		}
		delay := time.Duration(0)
		if !explicit {
			switch endpoint.Channel {
			case "ntfy":
				if priority == "low" && hasLocal {
					continue
				}
				if priority == "normal" && hasLocal {
					delay = 10 * time.Minute
				}
			case "email":
				switch priority {
				case "low":
					if hasLocal || hasNtfy {
						continue
					}
				case "normal":
					if hasLocal || hasNtfy {
						delay = time.Hour
					}
				case "high":
					if hasLocal || hasNtfy {
						delay = 30 * time.Minute
					}
				}
			}
			if priority != "urgent" && len(preferredRanks) > 0 {
				if rank, preferred := preferredRanks[endpoint.Channel]; preferred {
					delay = time.Duration(rank) * time.Minute
				} else {
					delay += 15 * time.Minute
				}
			}
		}
		routes = append(routes, notificationRoute{Endpoint: endpoint, Delay: delay})
	}
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Delay < routes[j].Delay })
	return routes
}

func (s *Service) claimNotificationDelivery(ctx context.Context) (string, error) {
	leaseUntil := time.Now().UTC().Add(30 * time.Second)
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		with candidate as (
			select d.id from steward_notification_deliveries d
			join steward_notifications n on n.id=d.notification_id
			where d.status in ('queued','retrying','sending')
			  and d.next_attempt_at<=now()
			  and (d.lease_expires_at is null or d.lease_expires_at<now())
			  and d.schedule_revision=n.schedule_revision
			  and n.status in ('queued','sent') and n.scheduled_at<=now()
			order by d.next_attempt_at,d.created_at
			for update skip locked limit 1
		)
		update steward_notification_deliveries d
		set status='sending',lease_owner=$1,lease_expires_at=$2,updated_at=now()
		from candidate where d.id=candidate.id returning d.id::text
	`, s.runtimeWorkerID, leaseUntil).Scan(&id)
	return id, err
}

func (s *Service) deliverClaimedNotification(ctx context.Context, deliveryID string) error {
	var notificationID, endpointID string
	var expiresAt, allowedWindowStart, allowedWindowEnd *time.Time
	var priority string
	var decisionContextJSON []byte
	err := s.db.Pool.QueryRow(ctx, `
		select d.notification_id::text,coalesce(d.endpoint_id::text,''),n.expires_at,
		       n.allowed_window_start,n.allowed_window_end,n.priority,n.decision_context
		from steward_notification_deliveries d join steward_notifications n on n.id=d.notification_id where d.id=$1
	`, deliveryID).Scan(&notificationID, &endpointID, &expiresAt, &allowedWindowStart, &allowedWindowEnd, &priority, &decisionContextJSON)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if allowedWindowStart != nil && now.Before(allowedWindowStart.UTC()) {
		_, err := s.db.Pool.Exec(ctx, `
			update steward_notification_deliveries set status=$2,next_attempt_at=$3,lease_owner='',lease_expires_at=null,updated_at=$4 where id=$1
		`, deliveryID, deliveryStatusQueued, allowedWindowStart.UTC(), now)
		return err
	}
	if expiresAt != nil && now.After(expiresAt.UTC()) {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_deliveries set status=$2,lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, deliveryID, deliveryStatusExpired)
		return s.refreshNotificationStatus(ctx, notificationID)
	}
	if allowedWindowEnd != nil && now.After(allowedWindowEnd.UTC()) && priority != "high" && priority != "urgent" {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_deliveries set status=$2,lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, deliveryID, deliveryStatusExpired)
		return s.refreshNotificationStatus(ctx, notificationID)
	}
	decisionContext := map[string]any{}
	_ = json.Unmarshal(decisionContextJSON, &decisionContext)
	if priority != "high" && priority != "urgent" {
		if override, _ := decisionContext["activity_override"].(bool); !override {
			if contextName, deferDelivery := s.currentNotificationBlockingContext(ctx, now); deferDelivery {
				next := now.Add(5 * time.Minute)
				deadline := earliestNotificationDeadline(expiresAt, allowedWindowEnd)
				if deadline != nil && next.After(deadline.UTC()) {
					next = deadline.UTC()
				}
				if next.After(now) {
					decisionContext["last_activity_deferral"] = map[string]any{"context": contextName, "at": now.Format(time.RFC3339Nano), "next_attempt_at": next.Format(time.RFC3339Nano)}
					encoded, _ := json.Marshal(decisionContext)
					_, err := s.db.Pool.Exec(ctx, `
						update steward_notification_deliveries set status=$2,next_attempt_at=$3,lease_owner='',lease_expires_at=null,updated_at=$4 where id=$1;
					`, deliveryID, deliveryStatusQueued, next, now)
					if err == nil {
						_, _ = s.db.Pool.Exec(ctx, `update steward_notifications set decision_context=$2,updated_at=$3 where id=$1`, notificationID, encoded, now)
					}
					return err
				}
			}
		}
	}
	notification, err := s.GetNotification(ctx, notificationID)
	if err != nil {
		return err
	}
	if isTerminalNotificationStatus(notification.Status) {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_deliveries set status=$2,lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, deliveryID, deliveryStatusCancelled)
		return nil
	}
	endpoint, err := s.getNotificationEndpoint(ctx, endpointID)
	if err != nil {
		return s.failNotificationDelivery(ctx, deliveryID, notificationID, endpointID, err)
	}
	providerID, err := s.sendNotification(ctx, endpoint, notification)
	if err != nil {
		return s.failNotificationDelivery(ctx, deliveryID, notificationID, endpointID, err)
	}
	now = time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `update steward_notification_deliveries set status=$2,attempt_count=attempt_count+1,provider_message_id=$3,last_error='',accepted_at=$4,lease_owner='',lease_expires_at=null,updated_at=$4 where id=$1`, deliveryID, deliveryStatusAccepted, providerID, now)
	if err == nil {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_endpoints set last_success_at=$2,last_error='',updated_at=$2 where id=$1`, endpointID, now)
		err = s.refreshNotificationStatus(ctx, notificationID)
	}
	return err
}

func (s *Service) failNotificationDelivery(ctx context.Context, deliveryID, notificationID, endpointID string, failure error) error {
	var attempts, maxAttempts int
	_ = s.db.Pool.QueryRow(ctx, `select attempt_count+1,max_attempts from steward_notification_deliveries where id=$1`, deliveryID).Scan(&attempts, &maxAttempts)
	status := deliveryStatusRetrying
	if attempts >= maxAttempts {
		status = deliveryStatusFailed
	}
	backoff := time.Duration(1<<min(attempts, 8)) * time.Minute
	message := truncateAdvisorText(failure.Error(), 2000)
	_, _ = s.db.Pool.Exec(ctx, `update steward_notification_deliveries set status=$2,attempt_count=$3,next_attempt_at=$4,last_error=$5,lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, deliveryID, status, attempts, time.Now().UTC().Add(backoff), message)
	if endpointID != "" {
		_, _ = s.db.Pool.Exec(ctx, `update steward_notification_endpoints set last_error=$2,updated_at=now() where id=$1`, endpointID, message)
	}
	_ = s.refreshNotificationStatus(ctx, notificationID)
	return fmt.Errorf("notification delivery %s failed: %w", deliveryID, failure)
}

func (s *Service) refreshNotificationStatus(ctx context.Context, notificationID string) error {
	var accepted, active int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) filter(where status='accepted'),count(*) filter(where status in ('queued','retrying','sending')) from steward_notification_deliveries where notification_id=$1`, notificationID).Scan(&accepted, &active); err != nil {
		return err
	}
	status := notificationStatusFailed
	if accepted > 0 {
		status = notificationStatusSent
	} else if active > 0 {
		status = notificationStatusQueued
	}
	_, err := s.db.Pool.Exec(ctx, `update steward_notifications set status=$2,updated_at=now() where id=$1 and status not in ('acknowledged','dismissed','auto_resolved','cancelled')`, notificationID, status)
	return err
}

func (s *Service) enqueueDueTaskNotifications(ctx context.Context, limit int) error {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,title,description,priority,due_at
		from steward_tasks
		where deleted_at is null and status in ('open','in_progress','waiting') and due_at is not null and due_at<=now()
		order by due_at limit $1
	`, limit)
	if err != nil {
		return err
	}
	defer rows.Close()
	type dueTask struct {
		ID, Title, Description, Priority string
		DueAt                            time.Time
	}
	tasks := []dueTask{}
	for rows.Next() {
		var task dueTask
		if err := rows.Scan(&task.ID, &task.Title, &task.Description, &task.Priority, &task.DueAt); err != nil {
			return err
		}
		tasks = append(tasks, task)
	}
	for _, task := range tasks {
		_, err := s.CreateNotification(ctx, CreateNotificationInput{
			SourceType: "task", SourceID: task.ID, Title: task.Title,
			Body:     defaultString(strings.TrimSpace(task.Description), "任务已到期，请及时处理。"),
			Category: "reminder", Priority: normalizeNotificationPriority(task.Priority),
			DedupeKey: "task-due:" + task.ID + ":" + task.DueAt.UTC().Format(time.RFC3339Nano),
			Actions:   []domain.StewardNotificationAction{{ID: "acknowledge", Label: "知道了", Kind: "acknowledge"}, {ID: "snooze", Label: "30 分钟后提醒", Kind: "snooze", Value: "1800"}},
			Metadata:  map[string]any{"due_at": task.DueAt},
		})
		if err != nil && !strings.Contains(err.Error(), "no enabled notification endpoint") {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) ensureDefaultNotificationEndpoints(ctx context.Context) error {
	type defaultEndpoint struct {
		channel, name string
		config        map[string]any
	}
	defaults := []defaultEndpoint{}
	switch runtime.GOOS {
	case "windows":
		defaults = append(defaults, defaultEndpoint{channel: "system", name: "本机系统通知", config: map[string]any{"platform": runtime.GOOS, "feedback_capable": true}})
	case "darwin":
		defaults = append(defaults, defaultEndpoint{channel: "system", name: "本机系统通知", config: map[string]any{"platform": runtime.GOOS}})
	case "linux":
		if strings.TrimSpace(os.Getenv("DISPLAY")) != "" || strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")) != "" {
			defaults = append(defaults, defaultEndpoint{channel: "linux_desktop", name: "Linux 桌面通知", config: map[string]any{"platform": "linux"}})
		}
	}
	if strings.TrimSpace(os.Getenv("STEWARD_NTFY_URL")) != "" && strings.TrimSpace(os.Getenv("STEWARD_NTFY_TOPIC")) != "" {
		defaults = append(defaults, defaultEndpoint{channel: "ntfy", name: "ntfy", config: map[string]any{"url": os.Getenv("STEWARD_NTFY_URL"), "topic": os.Getenv("STEWARD_NTFY_TOPIC")}})
	}
	if strings.TrimSpace(os.Getenv("STEWARD_SMTP_HOST")) != "" && strings.TrimSpace(os.Getenv("STEWARD_NOTIFICATION_EMAIL_TO")) != "" {
		defaults = append(defaults, defaultEndpoint{channel: "email", name: "邮件", config: map[string]any{"host": os.Getenv("STEWARD_SMTP_HOST"), "port": intEnv("STEWARD_SMTP_PORT", 587), "from": os.Getenv("STEWARD_NOTIFICATION_EMAIL_FROM"), "to": os.Getenv("STEWARD_NOTIFICATION_EMAIL_TO"), "username": os.Getenv("STEWARD_SMTP_USERNAME"), "starttls": true}})
	}
	for _, endpoint := range defaults {
		config, _ := json.Marshal(endpoint.config)
		_, err := s.db.Pool.Exec(ctx, `
			insert into steward_notification_endpoints(id,channel,name,enabled,config)
			values($1,$2,$3,true,$4)
			on conflict(channel,name) do update
			set config=steward_notification_endpoints.config || excluded.config,updated_at=now()
		`, uuid.NewString(), endpoint.channel, endpoint.name, config)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) listNotificationEndpointRecords(ctx context.Context, enabledOnly bool) ([]notificationEndpointRecord, error) {
	rows, err := s.db.Pool.Query(ctx, `select id::text,channel,name,enabled,config,secret_encrypted,last_success_at,last_error,created_at,updated_at from steward_notification_endpoints where (not $1 or enabled) order by channel,name`, enabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []notificationEndpointRecord{}
	for rows.Next() {
		var item notificationEndpointRecord
		var envelope map[string]any
		if err := rows.Scan(&item.ID, &item.Channel, &item.Name, &item.Enabled, &item.Config, &envelope, &item.LastSuccessAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.SecretSet = len(envelope) > 0
		if len(envelope) > 0 {
			keyring, keyErr := localPayloadKeyringFromEnv()
			if keyErr != nil {
				return nil, keyErr
			}
			item.Secret, err = decryptPayloadEnvelope(keyring, notificationEndpointAAD(item.ID), envelope, "notification endpoint secret")
			if err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getNotificationEndpoint(ctx context.Context, id string) (notificationEndpointRecord, error) {
	items, err := s.listNotificationEndpointRecords(ctx, false)
	if err != nil {
		return notificationEndpointRecord{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return notificationEndpointRecord{}, pgx.ErrNoRows
}

func (s *Service) listNotificationDeliveries(ctx context.Context, notificationID string) ([]domain.StewardNotificationDelivery, error) {
	rows, err := s.db.Pool.Query(ctx, `select id::text,notification_id::text,endpoint_id::text,channel,status,schedule_revision,attempt_count,max_attempts,next_attempt_at,provider_message_id,last_error,accepted_at,created_at,updated_at from steward_notification_deliveries where notification_id=$1 order by schedule_revision,created_at`, notificationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardNotificationDelivery{}
	for rows.Next() {
		var item domain.StewardNotificationDelivery
		if err := rows.Scan(&item.ID, &item.NotificationID, &item.EndpointID, &item.Channel, &item.Status, &item.ScheduleRevision, &item.AttemptCount, &item.MaxAttempts, &item.NextAttemptAt, &item.ProviderMessageID, &item.LastError, &item.AcceptedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type notificationScanner interface{ Scan(...any) error }

func scanNotification(row notificationScanner) (domain.StewardNotification, error) {
	var item domain.StewardNotification
	var actionsJSON, metadataJSON, decisionContextJSON []byte
	err := row.Scan(&item.ID, &item.SourceType, &item.SourceID, &item.Title, &item.Body, &item.Category, &item.Priority, &item.Status, &item.ScheduleRevision, &item.ScheduledAt, &item.ExpiresAt, &item.DedupeKey, &item.AllowedWindowStart, &item.AllowedWindowEnd, &actionsJSON, &metadataJSON, &item.PolicyID, &decisionContextJSON, &item.AcknowledgedAt, &item.CancelledAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	item.Actions = []domain.StewardNotificationAction{}
	item.Metadata = map[string]any{}
	item.DecisionContext = map[string]any{}
	_ = json.Unmarshal(actionsJSON, &item.Actions)
	_ = json.Unmarshal(metadataJSON, &item.Metadata)
	_ = json.Unmarshal(decisionContextJSON, &item.DecisionContext)
	return item, nil
}

func clampNotificationDeliveryWindow(input *CreateNotificationInput, requestedAt, now time.Time) error {
	if input == nil || input.ScheduledAt == nil {
		return fmt.Errorf("notification scheduled_at is required")
	}
	if input.DecisionContext == nil {
		input.DecisionContext = map[string]any{}
	}
	if input.AllowedWindowStart != nil {
		value := input.AllowedWindowStart.UTC()
		input.AllowedWindowStart = &value
	}
	if input.AllowedWindowEnd != nil {
		value := input.AllowedWindowEnd.UTC()
		input.AllowedWindowEnd = &value
	}
	if input.ExpiresAt != nil {
		value := input.ExpiresAt.UTC()
		input.ExpiresAt = &value
	}
	if input.AllowedWindowStart != nil && input.AllowedWindowEnd != nil && input.AllowedWindowStart.After(*input.AllowedWindowEnd) {
		return fmt.Errorf("notification allowed_window_start must not be after allowed_window_end")
	}
	deadline := earliestNotificationDeadline(input.ExpiresAt, input.AllowedWindowEnd)
	if deadline != nil && deadline.Before(now) {
		return fmt.Errorf("notification delivery window has already ended")
	}
	scheduled := input.ScheduledAt.UTC()
	if input.AllowedWindowStart != nil && scheduled.Before(input.AllowedWindowStart.UTC()) {
		scheduled = input.AllowedWindowStart.UTC()
		input.DecisionContext["allowed_window_adjustment"] = "moved_to_start"
	}
	if deadline != nil && scheduled.After(deadline.UTC()) {
		fallback := requestedAt.UTC()
		if fallback.Before(now) {
			fallback = now
		}
		if input.AllowedWindowStart != nil && fallback.Before(input.AllowedWindowStart.UTC()) {
			fallback = input.AllowedWindowStart.UTC()
		}
		if fallback.After(deadline.UTC()) {
			return fmt.Errorf("notification schedule cannot fit inside the allowed delivery window")
		}
		scheduled = fallback
		input.DecisionContext["allowed_window_adjustment"] = "policy_deferral_clamped"
	}
	input.ScheduledAt = &scheduled
	return nil
}

func earliestNotificationDeadline(values ...*time.Time) *time.Time {
	var deadline *time.Time
	for _, value := range values {
		if value == nil {
			continue
		}
		candidate := value.UTC()
		if deadline == nil || candidate.Before(*deadline) {
			deadline = &candidate
		}
	}
	return deadline
}

func (s *Service) currentNotificationBlockingContext(ctx context.Context, now time.Time) (string, bool) {
	var kind, title, canonical, summary string
	var endedAt time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select type,title,canonical_context,summary,ended_at
		from steward_activity_sessions
		where ended_at >= $1::timestamptz - interval '2 minutes'
		order by ended_at desc,updated_at desc limit 1
	`, now.UTC()).Scan(&kind, &title, &canonical, &summary, &endedAt)
	if err != nil || endedAt.Before(now.Add(-2*time.Minute)) {
		return "", false
	}
	value := strings.ToLower(strings.Join([]string{kind, title, canonical, summary}, " "))
	if strings.Contains(value, "not-afk") || strings.Contains(value, "not_afk") || strings.Contains(value, "not afk") {
		return "", false
	}
	contexts := []struct {
		name   string
		tokens []string
	}{
		{name: "meeting", tokens: []string{"meeting", "会议", "zoom", "teams call"}},
		{name: "presentation", tokens: []string{"fullscreen", "full-screen", "presentation", "演示", "全屏"}},
		{name: "focus", tokens: []string{"focused", "focus session", "deep work", "专注"}},
		{name: "afk", tokens: []string{"afk", "away", "离开"}},
	}
	for _, candidate := range contexts {
		for _, token := range candidate.tokens {
			if strings.Contains(value, token) {
				return candidate.name, true
			}
		}
	}
	return "", false
}

func validateNotificationActions(actions []domain.StewardNotificationAction) error {
	seen := map[string]bool{}
	for _, action := range actions {
		id := strings.TrimSpace(action.ID)
		if id == "" || strings.TrimSpace(action.Label) == "" {
			return fmt.Errorf("notification action id and label are required")
		}
		if seen[id] {
			return fmt.Errorf("duplicate notification action %q", id)
		}
		seen[id] = true
	}
	return nil
}

func normalizeNotificationPriority(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "normal", "high", "urgent":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "normal"
	}
}

func validNotificationChannel(value string) bool {
	switch value {
	case "system", "linux_desktop", "ntfy", "email":
		return true
	default:
		return false
	}
}

func notificationEndpointAAD(id string) string { return "steward:notification-endpoint:" + id }
