package steward

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

type companionNotificationRequest struct {
	ID               string                        `json:"id"`
	Title            string                        `json:"title"`
	Body             string                        `json:"body"`
	Category         string                        `json:"category"`
	Priority         string                        `json:"priority"`
	ScheduleRevision int                           `json:"schedule_revision"`
	Actions          []companionNotificationAction `json:"actions"`
	ExpiresAt        *time.Time                    `json:"expires_at,omitempty"`
}

type companionNotificationAction struct {
	domain.StewardNotificationAction
	CallbackToken string `json:"callback_token"`
}

type NotificationCallbackClaims struct {
	NotificationID   string `json:"notification_id"`
	ScheduleRevision int    `json:"schedule_revision"`
	ActionID         string `json:"action_id"`
	Action           string `json:"action"`
	ActionValue      string `json:"action_value,omitempty"`
	SnoozeSeconds    int    `json:"snooze_seconds,omitempty"`
	ExpiresAt        int64  `json:"expires_at"`
}

func (s *Service) sendNotification(ctx context.Context, endpoint notificationEndpointRecord, notification domain.StewardNotification) (string, error) {
	switch endpoint.Channel {
	case "system":
		return s.sendCompanionNotification(ctx, notification)
	case "linux_desktop":
		return sendLinuxDesktopNotification(ctx, notification)
	case "ntfy":
		return sendNtfyNotification(ctx, endpoint, notification)
	case "email":
		return sendEmailNotification(ctx, endpoint, notification)
	default:
		return "", fmt.Errorf("unsupported notification channel %q", endpoint.Channel)
	}
}

func (s *Service) sendCompanionNotification(ctx context.Context, notification domain.StewardNotification) (string, error) {
	key, err := sessionToolKey()
	if err != nil {
		return "", err
	}
	actions := make([]companionNotificationAction, 0, len(notification.Actions))
	for _, action := range notification.Actions {
		expiresAt := time.Now().UTC().Add(48 * time.Hour)
		if notification.ExpiresAt != nil && notification.ExpiresAt.Before(expiresAt) {
			expiresAt = notification.ExpiresAt.UTC()
		}
		normalized, normalizeErr := normalizeReminderFeedbackAction(action.Kind)
		if normalizeErr != nil {
			normalized = action.Kind
		}
		claims := newNotificationCallbackClaims(notification.ID, notification.ScheduleRevision, action, normalized, expiresAt)
		token, tokenErr := signNotificationCallbackToken(key, claims)
		if tokenErr != nil {
			return "", tokenErr
		}
		actions = append(actions, companionNotificationAction{StewardNotificationAction: action, CallbackToken: token})
	}
	payload, _ := json.Marshal(companionNotificationRequest{
		ID: notification.ID, Title: notification.Title, Body: notification.Body,
		Category: notification.Category, Priority: notification.Priority,
		ScheduleRevision: notification.ScheduleRevision, Actions: actions, ExpiresAt: notification.ExpiresAt,
	})
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("STEWARD_COMPANION_URL")), "/")
	client := &http.Client{Timeout: 15 * time.Second}
	if base == "" {
		base = "http://steward-companion"
		client = sessionCompanionHTTPClient(15 * time.Second)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/notifications", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Steward-Tool-Timestamp", timestamp)
	request.Header.Set("X-Steward-Tool-Signature", signSessionToolPayload(key, timestamp, payload))
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("interactive session companion unavailable: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("interactive session companion returned %s: %s", response.Status, truncateAdvisorText(string(body), 1000))
	}
	var result struct {
		ProviderMessageID string `json:"provider_message_id"`
	}
	_ = json.Unmarshal(body, &result)
	return defaultString(result.ProviderMessageID, notification.ID), nil
}

func signNotificationCallbackToken(key []byte, claims NotificationCallbackClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + signSessionToolPayload(key, "notification-action", payload), nil
}

func verifyNotificationCallbackToken(key []byte, token string, now time.Time) (NotificationCallbackClaims, error) {
	var claims NotificationCallbackClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claims, fmt.Errorf("invalid notification callback token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, fmt.Errorf("decode notification callback token: %w", err)
	}
	want := signSessionToolPayload(key, "notification-action", payload)
	if !constantTimeStringEqual(parts[1], want) {
		return claims, fmt.Errorf("notification callback token signature is invalid")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, fmt.Errorf("decode notification callback claims: %w", err)
	}
	// Revision zero is retained for notifications created before the versioned
	// scheduling migration. New notifications start at revision one.
	if claims.NotificationID == "" || claims.ActionID == "" || claims.Action == "" || claims.ScheduleRevision < 0 {
		return claims, fmt.Errorf("notification callback token claims are incomplete")
	}
	if claims.ExpiresAt <= now.UTC().Unix() {
		return claims, fmt.Errorf("notification callback token expired")
	}
	if claims.SnoozeSeconds < 0 {
		return claims, fmt.Errorf("notification callback token snooze_seconds is invalid")
	}
	return claims, nil
}

func notificationActionSnoozeSeconds(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return seconds
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		seconds := duration / time.Second
		if seconds > 0 && seconds <= time.Duration(maxInt()) {
			return int(seconds)
		}
	}
	return 0
}

func newNotificationCallbackClaims(notificationID string, scheduleRevision int, action domain.StewardNotificationAction, normalized string, expiresAt time.Time) NotificationCallbackClaims {
	claims := NotificationCallbackClaims{
		NotificationID: notificationID, ScheduleRevision: scheduleRevision,
		ActionID: action.ID, Action: normalized, ActionValue: action.Value, ExpiresAt: expiresAt.UTC().Unix(),
	}
	if normalized == ReminderFeedbackSnoozed {
		claims.SnoozeSeconds = notificationActionSnoozeSeconds(action.Value)
	}
	return claims
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var diff byte
	for index := range left {
		diff |= left[index] ^ right[index]
	}
	return diff == 0
}

func (s *Service) RecordNotificationCallback(ctx context.Context, token, deviceID, channel string, metadata map[string]any) (StewardReminderFeedback, error) {
	key, err := sessionToolKey()
	if err != nil {
		return StewardReminderFeedback{}, err
	}
	claims, err := verifyNotificationCallbackToken(key, strings.TrimSpace(token), time.Now().UTC())
	if err != nil {
		return StewardReminderFeedback{}, err
	}
	current, err := s.GetNotification(ctx, claims.NotificationID)
	if err != nil {
		return StewardReminderFeedback{}, err
	}
	if current.ScheduleRevision != claims.ScheduleRevision {
		return StewardReminderFeedback{}, fmt.Errorf("notification callback belongs to superseded schedule revision %d", claims.ScheduleRevision)
	}
	callbackMetadata := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		callbackMetadata[key] = value
	}
	callbackMetadata["signed_action_id"] = claims.ActionID
	if claims.ActionValue != "" {
		callbackMetadata["signed_action_value"] = claims.ActionValue
	}
	var occurredAt *time.Time
	if raw, ok := callbackMetadata["reported_occurred_at"].(string); ok {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw)); parseErr == nil {
			parsed = parsed.UTC()
			occurredAt = &parsed
		}
	}
	return s.RecordReminderFeedback(ctx, claims.NotificationID, NotificationDecisionInput{
		Decision: claims.Action, SnoozeSeconds: claims.SnoozeSeconds, DeviceID: deviceID, Channel: channel,
		IdempotencyKey: "callback:" + strings.TrimSpace(token), OccurredAt: occurredAt, Metadata: callbackMetadata,
	})
}

func sendNtfyNotification(ctx context.Context, endpoint notificationEndpointRecord, notification domain.StewardNotification) (string, error) {
	base := strings.TrimRight(configString(endpoint.Config, "url"), "/")
	topic := strings.Trim(configString(endpoint.Config, "topic"), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("STEWARD_NTFY_URL")), "/")
	}
	if topic == "" {
		topic = strings.Trim(strings.TrimSpace(os.Getenv("STEWARD_NTFY_TOPIC")), "/")
	}
	if base == "" || topic == "" {
		return "", fmt.Errorf("ntfy endpoint requires url and topic")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return "", fmt.Errorf("invalid ntfy url: %w", err)
	}
	actions := []map[string]any{}
	for _, action := range notification.Actions {
		if action.Kind == "view" && strings.TrimSpace(action.Value) != "" {
			actions = append(actions, map[string]any{"action": "view", "label": action.Label, "url": action.Value, "clear": true})
		}
	}
	payload := map[string]any{
		"topic": topic, "title": notification.Title, "message": notification.Body,
		"priority": ntfyPriority(notification.Priority), "tags": []string{notification.Category},
		"sequence_id": notification.ID,
	}
	if len(actions) > 0 {
		payload["actions"] = actions
	}
	data, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	token := defaultString(configString(endpoint.Secret, "token"), strings.TrimSpace(os.Getenv("STEWARD_NTFY_TOKEN")))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("ntfy returned %s: %s", response.Status, truncateAdvisorText(string(body), 1000))
	}
	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &result)
	return defaultString(result.ID, notification.ID), nil
}

func sendEmailNotification(ctx context.Context, endpoint notificationEndpointRecord, notification domain.StewardNotification) (string, error) {
	host := defaultString(configString(endpoint.Config, "host"), strings.TrimSpace(os.Getenv("STEWARD_SMTP_HOST")))
	port := configInt(endpoint.Config, "port", intEnv("STEWARD_SMTP_PORT", 587))
	from := defaultString(configString(endpoint.Config, "from"), strings.TrimSpace(os.Getenv("STEWARD_NOTIFICATION_EMAIL_FROM")))
	to := defaultString(configString(endpoint.Config, "to"), strings.TrimSpace(os.Getenv("STEWARD_NOTIFICATION_EMAIL_TO")))
	username := defaultString(configString(endpoint.Config, "username"), strings.TrimSpace(os.Getenv("STEWARD_SMTP_USERNAME")))
	password := defaultString(configString(endpoint.Secret, "password"), strings.TrimSpace(os.Getenv("STEWARD_SMTP_PASSWORD")))
	if host == "" || from == "" || to == "" {
		return "", fmt.Errorf("email endpoint requires SMTP host, from, and to")
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var client *smtp.Client
	var err error
	if port == 465 {
		connection, dialErr := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if dialErr != nil {
			return "", dialErr
		}
		client, err = smtp.NewClient(connection, host)
	} else {
		connection, dialErr := dialer.DialContext(ctx, "tcp", address)
		if dialErr != nil {
			return "", dialErr
		}
		client, err = smtp.NewClient(connection, host)
		if err == nil {
			if ok, _ := client.Extension("STARTTLS"); ok {
				err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			}
		}
	}
	if err != nil {
		return "", err
	}
	defer client.Close()
	if username != "" {
		if password == "" {
			return "", fmt.Errorf("SMTP username is configured but password is missing")
		}
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			return "", err
		}
	}
	if err := client.Mail(from); err != nil {
		return "", err
	}
	if err := client.Rcpt(to); err != nil {
		return "", err
	}
	wc, err := client.Data()
	if err != nil {
		return "", err
	}
	messageID := notification.ID + "@steward.local"
	subject := encodeEmailHeader(notification.Title)
	message := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\n" +
		"Message-ID: <" + messageID + ">\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + notification.Body + "\r\n"
	if _, err := io.WriteString(wc, message); err != nil {
		_ = wc.Close()
		return "", err
	}
	if err := wc.Close(); err != nil {
		return "", err
	}
	if err := client.Quit(); err != nil {
		return "", err
	}
	return messageID, nil
}

func sendLinuxDesktopNotification(ctx context.Context, notification domain.StewardNotification) (string, error) {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return "", fmt.Errorf("notify-send is not installed: %w", err)
	}
	urgency := "normal"
	if notification.Priority == "urgent" || notification.Priority == "high" {
		urgency = "critical"
	} else if notification.Priority == "low" {
		urgency = "low"
	}
	command := exec.CommandContext(ctx, path, "--app-name=Steward", "--urgency="+urgency, notification.Title, notification.Body)
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("notify-send failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return notification.ID, nil
}

func configString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[key]))
}

func configInt(values map[string]any, key string, fallback int) int {
	value := configString(values, key)
	if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func ntfyPriority(priority string) int {
	switch priority {
	case "low":
		return 2
	case "high":
		return 4
	case "urgent":
		return 5
	default:
		return 3
	}
}

func encodeEmailHeader(value string) string {
	return "=?UTF-8?B?" + base64Encode([]byte(value)) + "?="
}

func base64Encode(value []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	if len(value) == 0 {
		return ""
	}
	result := make([]byte, 0, (len(value)+2)/3*4)
	for i := 0; i < len(value); i += 3 {
		var n uint32 = uint32(value[i]) << 16
		remaining := len(value) - i
		if remaining > 1 {
			n |= uint32(value[i+1]) << 8
		}
		if remaining > 2 {
			n |= uint32(value[i+2])
		}
		result = append(result, alphabet[(n>>18)&63], alphabet[(n>>12)&63])
		if remaining > 1 {
			result = append(result, alphabet[(n>>6)&63])
		} else {
			result = append(result, '=')
		}
		if remaining > 2 {
			result = append(result, alphabet[n&63])
		} else {
			result = append(result, '=')
		}
	}
	return string(result)
}
