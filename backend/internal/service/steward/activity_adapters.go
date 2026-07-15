package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var activityAdapterHTTPClient = &http.Client{Timeout: 20 * time.Second}

func validateLocalAdapterEndpoint(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("adapter endpoint must be an HTTP URL")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("adapter endpoint must use localhost or a loopback address")
		}
	}
	return nil
}

func (s *Service) collectScreenpipe(ctx context.Context, settings map[string]any) error {
	endpoint := strings.TrimRight(strings.TrimSpace(fmt.Sprint(settings["endpoint"])), "/")
	if err := validateLocalAdapterEndpoint(endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(fmt.Sprint(settings["pinned_version"])) == "" {
		return fmt.Errorf("screenpipe pinned_version is required")
	}
	if collectorBool(settings["keyboard_content"], false) {
		return fmt.Errorf("screenpipe keyboard content collection is permanently disabled")
	}
	limit := collectorInt(settings["limit"], 100)
	query := url.Values{}
	query.Set("content_type", "all")
	query.Set("limit", strconv.Itoa(limit))
	query.Set("start_time", time.Now().UTC().Add(-10*time.Minute).Format(time.RFC3339))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/search?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	response, err := activityAdapterHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("screenpipe sidecar unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("screenpipe sidecar returned %s", response.Status)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode screenpipe response: %w", err)
	}
	for _, raw := range body.Data {
		content := nestedMap(raw, "content")
		if len(content) == 0 {
			content = raw
		}
		text := firstText(content, "text", "transcription", "ocr_text")
		application := firstText(content, "app_name", "application_name", "app")
		window := firstText(content, "window_name", "window_title")
		kind := defaultString(firstText(raw, "type", "content_type"), "screen")
		summary := strings.TrimSpace(strings.Join(nonEmptyStrings(application, window, truncateAdvisorText(text, 280)), " · "))
		if summary == "" {
			continue
		}
		occurredAt := parseAdapterTime(firstText(content, "timestamp", "captured_at"), time.Now().UTC())
		payload := map[string]any{"text": text, "application": application, "window": window}
		hints := []ObservationEntityHint{}
		if application != "" {
			hints = append(hints, ObservationEntityHint{Type: "application", CanonicalKey: application, DisplayName: application})
		}
		_, err := s.CreateObservation(ctx, CreateObservationInput{
			Source: "adapter:screenpipe", Type: kind, Summary: summary, ContextKey: application + "|" + window,
			DataLevel: DataD3, PermissionLevel: PermissionA1, Payload: payload, EntityHints: hints,
			OccurredAt: &occurredAt, Metadata: map[string]any{"adapter": "screenpipe", "source_version": fmt.Sprint(settings["pinned_version"]), "redacted": true},
		})
		if err != nil && !errors.Is(err, ErrCredentialDataBlocked) {
			return err
		}
	}
	return nil
}

func (s *Service) collectActivityWatch(ctx context.Context, settings map[string]any) error {
	endpoint := strings.TrimRight(strings.TrimSpace(fmt.Sprint(settings["endpoint"])), "/")
	if err := validateLocalAdapterEndpoint(endpoint); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/0/buckets", nil)
	if err != nil {
		return err
	}
	response, err := activityAdapterHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("ActivityWatch unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ActivityWatch returned %s", response.Status)
	}
	var buckets map[string]struct {
		Type     string `json:"type"`
		Client   string `json:"client"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(response.Body).Decode(&buckets); err != nil {
		return err
	}
	limit := collectorInt(settings["limit"], 100)
	for bucketID, bucket := range buckets {
		bucketType := strings.ToLower(bucket.Type)
		if !strings.Contains(bucketType, "window") && !strings.Contains(bucketType, "afk") && !strings.Contains(bucketType, "web") {
			continue
		}
		query := url.Values{"limit": {strconv.Itoa(limit)}, "start": {time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)}}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			endpoint+"/api/0/buckets/"+url.PathEscape(bucketID)+"/events?"+query.Encode(), nil)
		if err != nil {
			return err
		}
		response, err := activityAdapterHTTPClient.Do(request)
		if err != nil {
			return err
		}
		var events []struct {
			ID        int64          `json:"id"`
			Timestamp string         `json:"timestamp"`
			Duration  float64        `json:"duration"`
			Data      map[string]any `json:"data"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&events)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("ActivityWatch events returned %s", response.Status)
		}
		if decodeErr != nil {
			return decodeErr
		}
		for _, event := range events {
			application := firstText(event.Data, "app", "application")
			title := firstText(event.Data, "title", "status")
			rawURL := firstText(event.Data, "url")
			domainName := safeURLDomain(rawURL)
			summary := strings.Join(nonEmptyStrings(application, title, domainName), " · ")
			if summary == "" {
				continue
			}
			occurredAt := parseAdapterTime(event.Timestamp, time.Now().UTC())
			endedAt := occurredAt.Add(time.Duration(event.Duration * float64(time.Second)))
			hints := []ObservationEntityHint{}
			if application != "" {
				hints = append(hints, ObservationEntityHint{Type: "application", CanonicalKey: application, DisplayName: application})
			}
			if domainName != "" {
				hints = append(hints, ObservationEntityHint{Type: "website", CanonicalKey: domainName, DisplayName: domainName})
			}
			_, err := s.CreateObservation(ctx, CreateObservationInput{
				Source: "adapter:activitywatch", Type: bucketType, Summary: summary,
				ContextKey:  strings.Join(nonEmptyStrings(application, domainName, title), "|"),
				Fingerprint: fmt.Sprintf("%s:%d:%s", bucketID, event.ID, event.Timestamp),
				DataLevel:   DataD2, PermissionLevel: PermissionA1,
				Payload:     map[string]any{"application": application, "title": title, "domain": domainName, "duration_seconds": event.Duration},
				EntityHints: hints, OccurredAt: &occurredAt, EndedAt: &endedAt,
				Metadata: map[string]any{"adapter": "activitywatch", "bucket_id": bucketID, "duration_seconds": event.Duration, "redacted": true},
			})
			if err != nil && !errors.Is(err, ErrCredentialDataBlocked) {
				return err
			}
		}
	}
	return nil
}

func nestedMap(value map[string]any, key string) map[string]any {
	if nested, ok := value[key].(map[string]any); ok {
		return nested
	}
	return nil
}

func firstText(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func parseAdapterTime(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	return fallback
}

func safeURLDomain(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func nonEmptyStrings(values ...string) []string {
	result := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}
