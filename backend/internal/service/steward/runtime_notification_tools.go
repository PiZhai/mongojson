package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

type runtimeNotificationTool struct {
	service *Service
	action  string
}

func newRuntimeNotificationTool(service *Service, action string) RuntimeTool {
	return runtimeNotificationTool{service: service, action: action}
}

func (t runtimeNotificationTool) Spec() domain.StewardToolSpec {
	properties := map[string]any{}
	required := []string{}
	description := ""
	sideEffect := RuntimeSideEffectWrite
	switch t.action {
	case "notify.send", "notify.schedule":
		description = "Create a durable notification that is delivered outside the web page through system, cross-device push, or email channels. The delivery plane handles retries, fallback, and deduplication."
		required = []string{"title", "body"}
		properties = map[string]any{
			"title": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"},
			"category": map[string]any{"type": "string"}, "priority": map[string]any{"type": "string", "enum": []string{"low", "normal", "high", "urgent"}},
			"scheduled_at": map[string]any{"type": "string"}, "expires_at": map[string]any{"type": "string"},
			"dedupe_key": map[string]any{"type": "string"},
			"channels":   map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"system", "linux_desktop", "ntfy", "email"}}},
		}
	case "notify.list":
		description = "List durable notifications and their actual channel delivery states."
		properties = map[string]any{"status": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}
		sideEffect = RuntimeSideEffectNone
	case "notify.cancel":
		description = "Cancel a durable notification and every pending channel delivery."
		required = []string{"notification_id"}
		properties = map[string]any{"notification_id": map[string]any{"type": "string"}}
	case "notify.snooze":
		description = "Snooze a notification and reschedule all of its pending delivery channels."
		required = []string{"notification_id"}
		properties = map[string]any{"notification_id": map[string]any{"type": "string"}, "seconds": map[string]any{"type": "integer"}}
	case "notify.acknowledge":
		description = "Acknowledge a notification and suppress pending escalation deliveries."
		required = []string{"notification_id"}
		properties = map[string]any{"notification_id": map[string]any{"type": "string"}}
	case "notify.endpoint_test":
		description = "Send a real test notification through one configured endpoint."
		required = []string{"endpoint_id"}
		properties = map[string]any{"endpoint_id": map[string]any{"type": "string"}}
	}
	return domain.StewardToolSpec{
		Name: t.action, Version: "5.2.0", Description: description,
		InputSchema:     map[string]any{"type": "object", "required": required, "additionalProperties": false, "properties": properties},
		OutputSchema:    map[string]any{"type": "object"},
		PermissionLevel: PermissionA3, RiskLevel: "low", SideEffect: sideEffect,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyKeyed,
		Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 30,
	}
}

func (t runtimeNotificationTool) Validate(input map[string]any) error {
	allowed := map[string][]string{
		"notify.send":     {"title", "body", "category", "priority", "scheduled_at", "expires_at", "dedupe_key", "channels"},
		"notify.schedule": {"title", "body", "category", "priority", "scheduled_at", "expires_at", "dedupe_key", "channels"},
		"notify.list":     {"status", "limit"}, "notify.cancel": {"notification_id"},
		"notify.snooze": {"notification_id", "seconds"}, "notify.acknowledge": {"notification_id"},
		"notify.endpoint_test": {"endpoint_id"},
	}
	if err := runtimeRejectUnknownFields(input, allowed[t.action]...); err != nil {
		return err
	}
	if t.action == "notify.send" || t.action == "notify.schedule" {
		if _, err := runtimeRequiredString(input, "title"); err != nil {
			return err
		}
		if _, err := runtimeRequiredString(input, "body"); err != nil {
			return err
		}
		for _, field := range []string{"scheduled_at", "expires_at"} {
			if value, _ := runtimeOptionalString(input, field); value != "" {
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return fmt.Errorf("%s must be RFC3339: %w", field, err)
				}
			}
		}
		if _, err := runtimeStringSlice(input, "channels"); err != nil {
			return err
		}
		return nil
	}
	if t.action == "notify.list" {
		_, err := runtimeInt(input, "limit", 50)
		return err
	}
	key := "notification_id"
	if t.action == "notify.endpoint_test" {
		key = "endpoint_id"
	}
	_, err := runtimeRequiredString(input, key)
	return err
}

func (t runtimeNotificationTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	switch t.action {
	case "notify.send", "notify.schedule":
		title, _ := runtimeRequiredString(input, "title")
		body, _ := runtimeRequiredString(input, "body")
		category, _ := runtimeOptionalString(input, "category")
		priority, _ := runtimeOptionalString(input, "priority")
		dedupeKey, _ := runtimeOptionalString(input, "dedupe_key")
		channels, _ := runtimeStringSlice(input, "channels")
		var scheduledAt, expiresAt *time.Time
		if value, _ := runtimeOptionalString(input, "scheduled_at"); value != "" {
			parsed, _ := time.Parse(time.RFC3339, value)
			scheduledAt = &parsed
		}
		if value, _ := runtimeOptionalString(input, "expires_at"); value != "" {
			parsed, _ := time.Parse(time.RFC3339, value)
			expiresAt = &parsed
		}
		item, err := t.service.CreateNotification(ctx, CreateNotificationInput{
			SourceType: "agent", Title: title, Body: body, Category: category, Priority: priority,
			ScheduledAt: scheduledAt, ExpiresAt: expiresAt, DedupeKey: dedupeKey, Channels: channels,
			Actions: []domain.StewardNotificationAction{{ID: "acknowledge", Label: "知道了", Kind: "acknowledge"}, {ID: "snooze", Label: "稍后提醒", Kind: "snooze", Value: "1800"}},
		})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output := structToMap(item)
		return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "notification_queued", Summary: "durable notification queued for out-of-page delivery", Payload: map[string]any{"id": item.ID, "status": item.Status, "channels": channels}}}}, nil
	case "notify.list":
		status, _ := runtimeOptionalString(input, "status")
		limit, _ := runtimeInt(input, "limit", 50)
		items, err := t.service.ListNotifications(ctx, status, limit)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return RuntimeToolResult{Output: map[string]any{"notifications": items, "count": len(items)}}, nil
	case "notify.endpoint_test":
		id, _ := runtimeRequiredString(input, "endpoint_id")
		item, err := t.service.TestNotificationEndpoint(ctx, id)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return RuntimeToolResult{Output: structToMap(item)}, nil
	default:
		id, _ := runtimeRequiredString(input, "notification_id")
		decision := strings.TrimPrefix(t.action, "notify.")
		decisionInput := NotificationDecisionInput{Decision: decision}
		if decision == "snooze" {
			decisionInput.SnoozeSeconds, _ = runtimeInt(input, "seconds", 1800)
		}
		item, err := t.service.DecideNotification(ctx, id, decisionInput)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return RuntimeToolResult{Output: structToMap(item)}, nil
	}
}

func (runtimeNotificationTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	return runtimeOutputMatchesExpected(output, expected)
}
