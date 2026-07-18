package steward

import (
	"bytes"
	"context"
	"crypto/tls"
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
	ID        string                             `json:"id"`
	Title     string                             `json:"title"`
	Body      string                             `json:"body"`
	Category  string                             `json:"category"`
	Priority  string                             `json:"priority"`
	Actions   []domain.StewardNotificationAction `json:"actions"`
	ExpiresAt *time.Time                         `json:"expires_at,omitempty"`
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
	payload, _ := json.Marshal(companionNotificationRequest{
		ID: notification.ID, Title: notification.Title, Body: notification.Body,
		Category: notification.Category, Priority: notification.Priority,
		Actions: notification.Actions, ExpiresAt: notification.ExpiresAt,
	})
	key, err := sessionToolKey()
	if err != nil {
		return "", err
	}
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
