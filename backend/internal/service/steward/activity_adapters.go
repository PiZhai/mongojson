package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	endpoint, response, err := resolveActivityWatchEndpoint(ctx, strings.TrimRight(strings.TrimSpace(fmt.Sprint(settings["endpoint"])), "/"))
	if err != nil {
		_ = s.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
			Collector: "activitywatch-bridge", SourceKey: "server", Status: "unavailable",
			LastPollAt: time.Now().UTC(), LastError: err.Error(), MaxExpectedLagSeconds: 600,
		})
		return err
	}
	defer response.Body.Close()
	info, err := fetchActivityWatchInfo(ctx, endpoint)
	if err != nil {
		_ = s.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
			Collector: "activitywatch-bridge", SourceKey: "server", Status: "unavailable",
			LastPollAt: time.Now().UTC(), LastError: err.Error(), MaxExpectedLagSeconds: 600,
		})
		return err
	}
	var buckets map[string]activityWatchBucket
	if err := json.NewDecoder(response.Body).Decode(&buckets); err != nil {
		return err
	}
	limit := collectorInt(settings["limit"], 100)
	now := time.Now().UTC()
	clock := s.activityWatchSourceClock(ctx, settings, info, now)
	companionSessionID, err := s.latestCompanionInteractiveSessionID(ctx)
	if err != nil {
		return fmt.Errorf("resolve local Companion session for ActivityWatch: %w", err)
	}
	serverCapabilities := activityWatchCapabilities(endpoint, info, clock)
	serverCapabilities["bucket_count"] = len(buckets)
	_ = s.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
		Collector: "activitywatch-bridge", SourceKey: "server", Status: "healthy",
		Host: info.Hostname, APIVersion: info.Version, LastPollAt: now,
		LastSourceEventAt: nil, LastIngestedAt: nil, MaxExpectedLagSeconds: 600,
		Capabilities: serverCapabilities,
	})
	for bucketID, bucket := range buckets {
		bucketType := strings.ToLower(bucket.Type)
		if !strings.Contains(bucketType, "window") && !strings.Contains(bucketType, "afk") && !strings.Contains(bucketType, "web") {
			continue
		}
		bucketDeviceID := firstText(bucket.Data, "device_id")
		sourceHost := defaultString(strings.TrimSpace(bucket.Hostname), info.Hostname)
		if bucketDeviceID == "" && activityWatchHostsEqual(sourceHost, info.Hostname) {
			bucketDeviceID = info.DeviceID
		}
		interactiveSessionID := normalizeActivityWatchInteractiveSessionID(
			info.Hostname, info.DeviceID, sourceHost, bucketDeviceID, companionSessionID,
		)
		capabilities := activityWatchCapabilities(endpoint, info, clock)
		capabilities["bucket_type"] = bucketType
		capabilities["source_host"] = sourceHost
		capabilities["source_device_id"] = bucketDeviceID
		cursor, cursorErr := s.loadActivityWatchCursor(ctx, bucketID)
		if cursorErr != nil {
			return cursorErr
		}
		start := activityWatchFetchStart(cursor, now)
		events, fetchErr := fetchActivityWatchEvents(ctx, endpoint, bucketID, start, now, limit)
		if fetchErr != nil {
			_ = s.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
				Collector: "activitywatch-bridge", SourceKey: bucketID, Watcher: bucket.Client,
				Host: sourceHost, EventType: bucketType, InteractiveSessionID: interactiveSessionID,
				Status: "error", APIVersion: info.Version, LastPollAt: now,
				LastError: fetchErr.Error(), Cursor: cursor.asMap(), Capabilities: capabilities, MaxExpectedLagSeconds: 600,
			})
			return fetchErr
		}
		latest := cursor
		var latestSourceEventAt time.Time
		ingested := 0
		for _, event := range events {
			occurredAt := parseAdapterTime(event.Timestamp, now)
			if !cursor.Timestamp.IsZero() && occurredAt.Before(cursor.Timestamp.Add(-2*time.Minute)) {
				continue
			}
			endedAt := activityWatchEventEnd(occurredAt, event.Duration)
			if endedAt.After(latestSourceEventAt) {
				latestSourceEventAt = endedAt
			}
			if occurredAt.After(latest.Timestamp) || (occurredAt.Equal(latest.Timestamp) && event.ID > latest.EventID) {
				latest = activityWatchCursor{Timestamp: occurredAt, EventID: event.ID}
			}
			application := firstText(event.Data, "app", "application")
			title := firstText(event.Data, "title", "status")
			rawURL := firstText(event.Data, "url")
			domainName := safeURLDomain(rawURL)
			summary := strings.Join(nonEmptyStrings(application, title, domainName), " · ")
			if summary == "" {
				continue
			}
			hints := []ObservationEntityHint{}
			if application != "" {
				hints = append(hints, ObservationEntityHint{Type: "application", CanonicalKey: application, DisplayName: application})
			}
			if domainName != "" {
				hints = append(hints, ObservationEntityHint{Type: "website", CanonicalKey: domainName, DisplayName: domainName})
			}
			_, err := s.CreateObservation(ctx, CreateObservationInput{
				Source: "adapter:activitywatch", Type: bucketType, Summary: summary,
				SourceEventKey:       fmt.Sprintf("activitywatch:%s:%d", bucketID, event.ID),
				SourceRevision:       activityWatchEventRevision(event.Duration),
				InteractiveSessionID: interactiveSessionID,
				SourceTimezone:       clock.Timezone,
				ContextKey:           strings.Join(nonEmptyStrings(application, domainName, title), "|"),
				Fingerprint:          fmt.Sprintf("%s:%d:%s", bucketID, event.ID, event.Timestamp),
				DataLevel:            DataD2, PermissionLevel: PermissionA1,
				Payload: map[string]any{
					"application": application, "title": title, "domain": domainName, "duration_seconds": event.Duration,
					"source_device_id": bucketDeviceID, "source_utc_offset_seconds": clock.UTCOffsetSeconds,
				},
				EntityHints: hints, OccurredAt: &occurredAt, EndedAt: &endedAt,
				Metadata: map[string]any{"adapter": "activitywatch", "bucket_id": bucketID, "duration_seconds": event.Duration, "redacted": true,
					"source_host": sourceHost, "source_event_id": event.ID},
			})
			if err != nil && !errors.Is(err, ErrCredentialDataBlocked) {
				return err
			}
			ingested++
		}
		var lastSourceAt *time.Time
		if !latestSourceEventAt.IsZero() {
			value := latestSourceEventAt
			lastSourceAt = &value
		}
		var lastIngestedAt *time.Time
		if ingested > 0 {
			lastIngestedAt = &now
		}
		_ = s.updateCollectionSourceState(ctx, collectionSourceStateUpdate{
			Collector: "activitywatch-bridge", SourceKey: bucketID, Watcher: bucket.Client,
			Host: sourceHost, EventType: bucketType, InteractiveSessionID: interactiveSessionID,
			Status: "healthy", APIVersion: info.Version, LastPollAt: now,
			LastSourceEventAt: lastSourceAt, LastIngestedAt: lastIngestedAt, Cursor: latest.asMap(),
			BacklogCount: int64(maxActivityInt(0, len(events)-ingested)), MaxExpectedLagSeconds: 600,
			Capabilities: capabilities,
		})
	}
	return nil
}

type activityWatchInfo struct {
	Hostname         string         `json:"hostname"`
	Version          string         `json:"version"`
	Testing          bool           `json:"testing"`
	DeviceID         string         `json:"device_id"`
	Timezone         string         `json:"timezone"`
	UTCOffsetSeconds *int           `json:"utc_offset_seconds"`
	Capabilities     map[string]any `json:"capabilities"`
}

type activityWatchBucket struct {
	Type     string         `json:"type"`
	Client   string         `json:"client"`
	Hostname string         `json:"hostname"`
	Data     map[string]any `json:"data"`
}

type activityWatchSourceClock struct {
	Timezone         string
	UTCOffsetSeconds int
	Source           string
}

type activityWatchEvent struct {
	ID        int64          `json:"id"`
	Timestamp string         `json:"timestamp"`
	Duration  float64        `json:"duration"`
	Data      map[string]any `json:"data"`
}

type activityWatchCursor struct {
	Timestamp time.Time `json:"timestamp"`
	EventID   int64     `json:"event_id"`
}

func (c activityWatchCursor) asMap() map[string]any {
	if c.Timestamp.IsZero() {
		return map[string]any{}
	}
	return map[string]any{"timestamp": c.Timestamp.UTC().Format(time.RFC3339Nano), "event_id": c.EventID}
}

func activityWatchFetchStart(cursor activityWatchCursor, now time.Time) time.Time {
	if cursor.Timestamp.IsZero() {
		return now.UTC().Add(-10 * time.Minute)
	}
	// ActivityWatch heartbeat rows keep the same start timestamp while their
	// duration/revision grows. Preserve the overlap so those updates are seen.
	return cursor.Timestamp.UTC().Add(-2 * time.Minute)
}

func activityWatchEventEnd(start time.Time, durationSeconds float64) time.Time {
	if math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) || durationSeconds <= 0 {
		return start.UTC()
	}
	return start.UTC().Add(time.Duration(durationSeconds * float64(time.Second)))
}

func (s *Service) latestCompanionInteractiveSessionID(ctx context.Context) (string, error) {
	var sessionID string
	err := s.db.Pool.QueryRow(ctx, `
		select interactive_session_id from steward_collection_source_states
		where device_id=$1 and collector_name='companion:windows-activity' and interactive_session_id<>''
		order by coalesce(last_source_event_at,last_ingested_at,last_poll_at,updated_at) desc limit 1
	`, s.agentIDValue()).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(sessionID), err
}

func normalizeActivityWatchInteractiveSessionID(serverHost, serverDeviceID, bucketHost, bucketDeviceID, companionSessionID string) string {
	localHost := bucketHost == "" || serverHost == "" || activityWatchHostsEqual(bucketHost, serverHost)
	localDevice := bucketDeviceID == "" || serverDeviceID == "" || strings.EqualFold(strings.TrimSpace(bucketDeviceID), strings.TrimSpace(serverDeviceID))
	if localHost && localDevice && strings.TrimSpace(companionSessionID) != "" {
		return strings.ToLower(strings.TrimSpace(companionSessionID))
	}
	identity := defaultString(strings.TrimSpace(bucketDeviceID), strings.TrimSpace(bucketHost))
	identity = defaultString(identity, strings.TrimSpace(serverDeviceID))
	identity = defaultString(identity, strings.TrimSpace(serverHost))
	identity = canonicalActivityWatchIdentity(identity)
	return "activitywatch:" + defaultString(identity, "local")
}

func activityWatchHostsEqual(left, right string) bool {
	left = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(left), "."))
	right = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(right), "."))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return strings.SplitN(left, ".", 2)[0] == strings.SplitN(right, ".", 2)[0]
}

func canonicalActivityWatchIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, value)
	return strings.Trim(value, ".-_")
}

func (s *Service) activityWatchSourceClock(ctx context.Context, collectorSettings map[string]any, info activityWatchInfo, now time.Time) activityWatchSourceClock {
	timezone, source := strings.TrimSpace(info.Timezone), "activitywatch_info"
	if timezone == "" {
		timezone, source = firstText(collectorSettings, "source_timezone", "timezone"), "collector_setting"
	}
	if timezone == "" {
		if settings, err := s.GetIntelligenceSettings(ctx); err == nil {
			timezone, source = strings.TrimSpace(settings.Timezone), "intelligence_setting"
		}
	}
	location := time.Local
	if timezone != "" {
		if configured, err := time.LoadLocation(timezone); err == nil {
			location = configured
		} else {
			timezone, source = "", "system_local"
		}
	} else {
		source = "system_local"
	}
	_, offset := now.In(location).Zone()
	if info.UTCOffsetSeconds != nil {
		offset = *info.UTCOffsetSeconds
	}
	if timezone == "" {
		timezone = location.String()
		if timezone == "" || strings.EqualFold(timezone, "local") {
			timezone = formatUTCOffset(offset)
		}
	}
	return activityWatchSourceClock{Timezone: timezone, UTCOffsetSeconds: offset, Source: source}
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, offsetSeconds/3600, (offsetSeconds%3600)/60)
}

func activityWatchCapabilities(endpoint string, info activityWatchInfo, clock activityWatchSourceClock) map[string]any {
	capabilities := map[string]any{}
	for key, value := range info.Capabilities {
		capabilities[key] = value
	}
	capabilities["endpoint"] = endpoint
	capabilities["api_contract_version"] = "0"
	capabilities["server_version"] = info.Version
	capabilities["source_host"] = info.Hostname
	capabilities["source_device_id"] = info.DeviceID
	capabilities["source_timezone"] = clock.Timezone
	capabilities["source_utc_offset_seconds"] = clock.UTCOffsetSeconds
	capabilities["source_timezone_origin"] = clock.Source
	capabilities["buckets_api"] = true
	capabilities["events_api"] = true
	capabilities["pagination"] = true
	capabilities["heartbeat_duration_updates"] = true
	return capabilities
}

func (s *Service) loadActivityWatchCursor(ctx context.Context, bucketID string) (activityWatchCursor, error) {
	var raw json.RawMessage
	err := s.db.Pool.QueryRow(ctx, `
		select cursor from steward_collection_source_states
		where device_id=$1 and collector_name='activitywatch-bridge' and source_key=$2
	`, s.agentIDValue(), bucketID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return activityWatchCursor{}, nil
		}
		return activityWatchCursor{}, err
	}
	var wire struct {
		Timestamp string `json:"timestamp"`
		EventID   int64  `json:"event_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &wire); err != nil {
			return activityWatchCursor{}, fmt.Errorf("decode ActivityWatch cursor for %s: %w", bucketID, err)
		}
	}
	return activityWatchCursor{Timestamp: parseAdapterTime(wire.Timestamp, time.Time{}), EventID: wire.EventID}, nil
}

type collectionSourceStateUpdate struct {
	Collector, SourceKey, Watcher, Host, EventType, InteractiveSessionID string
	Status, APIVersion, LastError                                        string
	Cursor, Capabilities                                                 map[string]any
	LastPollAt                                                           time.Time
	LastSourceEventAt, LastIngestedAt                                    *time.Time
	BacklogCount                                                         int64
	MaxExpectedLagSeconds                                                int
}

func (s *Service) updateCollectionSourceState(ctx context.Context, input collectionSourceStateUpdate) error {
	if input.MaxExpectedLagSeconds <= 0 {
		input.MaxExpectedLagSeconds = 300
	}
	if input.Cursor == nil {
		input.Cursor = map[string]any{}
	}
	if input.Capabilities == nil {
		input.Capabilities = map[string]any{}
	}
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_collection_source_states (
			device_id,collector_name,source_key,execution_target,watcher,host,event_type,
			interactive_session_id,status,cursor,capabilities,api_version,last_poll_at,last_source_event_at,
			last_ingested_at,backlog_count,max_expected_lag_seconds,last_error,updated_at
		) values ($1,$2,$3,'companion',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,now())
		on conflict(device_id,collector_name,source_key) do update set
			watcher=case when excluded.watcher='' then steward_collection_source_states.watcher else excluded.watcher end,
			host=case when excluded.host='' then steward_collection_source_states.host else excluded.host end,
			event_type=case when excluded.event_type='' then steward_collection_source_states.event_type else excluded.event_type end,
			interactive_session_id=case when excluded.interactive_session_id='' then steward_collection_source_states.interactive_session_id else excluded.interactive_session_id end,
			status=excluded.status,
			cursor=case when excluded.cursor='{}'::jsonb then steward_collection_source_states.cursor else excluded.cursor end,
			capabilities=case when excluded.capabilities='{}'::jsonb then steward_collection_source_states.capabilities else excluded.capabilities end,
			api_version=case when excluded.api_version='' then steward_collection_source_states.api_version else excluded.api_version end,
			last_poll_at=case when excluded.last_poll_at is null then steward_collection_source_states.last_poll_at
			                  else greatest(excluded.last_poll_at,steward_collection_source_states.last_poll_at) end,
			last_source_event_at=case when excluded.last_source_event_at is null then steward_collection_source_states.last_source_event_at
			                          else greatest(excluded.last_source_event_at,steward_collection_source_states.last_source_event_at) end,
			last_ingested_at=case when excluded.last_ingested_at is null then steward_collection_source_states.last_ingested_at
			                     else greatest(excluded.last_ingested_at,steward_collection_source_states.last_ingested_at) end,
			backlog_count=excluded.backlog_count,max_expected_lag_seconds=excluded.max_expected_lag_seconds,
			last_error=excluded.last_error,updated_at=now()
	`, s.agentIDValue(), input.Collector, input.SourceKey, input.Watcher, input.Host, input.EventType,
		input.InteractiveSessionID, input.Status, input.Cursor, input.Capabilities, input.APIVersion,
		input.LastPollAt, input.LastSourceEventAt, input.LastIngestedAt, input.BacklogCount,
		input.MaxExpectedLagSeconds, truncateAdvisorText(input.LastError, 500))
	return err
}

func fetchActivityWatchInfo(ctx context.Context, endpoint string) (activityWatchInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/api/0/info", nil)
	if err != nil {
		return activityWatchInfo{}, err
	}
	response, err := activityAdapterHTTPClient.Do(request)
	if err != nil {
		return activityWatchInfo{}, fmt.Errorf("ActivityWatch info unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return activityWatchInfo{}, fmt.Errorf("ActivityWatch info returned %s", response.Status)
	}
	var info activityWatchInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return activityWatchInfo{}, fmt.Errorf("decode ActivityWatch info: %w", err)
	}
	info.Hostname = strings.TrimSpace(info.Hostname)
	info.Version = strings.TrimSpace(info.Version)
	info.DeviceID = strings.TrimSpace(info.DeviceID)
	info.Timezone = strings.TrimSpace(info.Timezone)
	return info, nil
}

func resolveActivityWatchEndpoint(ctx context.Context, configured string) (string, *http.Response, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(configured), "/")
	if endpoint == "" {
		return "", nil, fmt.Errorf("ActivityWatch endpoint is not configured")
	}
	if err := validateLocalAdapterEndpoint(endpoint); err != nil {
		return "", nil, fmt.Errorf("invalid configured ActivityWatch endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/0/buckets", nil)
	if err != nil {
		return "", nil, err
	}
	response, err := activityAdapterHTTPClient.Do(request)
	if err != nil {
		return "", nil, fmt.Errorf("ActivityWatch unavailable at %s: %w", endpoint, err)
	}
	if response.StatusCode != http.StatusOK {
		status := response.Status
		response.Body.Close()
		return "", nil, fmt.Errorf("ActivityWatch at %s returned %s", endpoint, status)
	}
	return endpoint, response, nil
}

func fetchActivityWatchEvents(ctx context.Context, endpoint, bucketID string, start, end time.Time, limit int) ([]activityWatchEvent, error) {
	if limit < 1 {
		limit = 100
	}
	all := []activityWatchEvent{}
	seen := map[string]bool{}
	pageEnd := end.UTC()
	for page := 0; page < 100; page++ {
		query := url.Values{
			"limit": {strconv.Itoa(limit)}, "start": {start.UTC().Format(time.RFC3339Nano)},
			"end": {pageEnd.Format(time.RFC3339Nano)},
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/0/buckets/"+url.PathEscape(bucketID)+"/events?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		response, err := activityAdapterHTTPClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("ActivityWatch bucket %s unavailable: %w", bucketID, err)
		}
		var events []activityWatchEvent
		decodeErr := json.NewDecoder(response.Body).Decode(&events)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ActivityWatch events returned %s", response.Status)
		}
		if decodeErr != nil {
			return nil, decodeErr
		}
		oldest := pageEnd
		for _, event := range events {
			key := fmt.Sprintf("%d|%s", event.ID, event.Timestamp)
			if !seen[key] {
				seen[key] = true
				all = append(all, event)
			}
			when := parseAdapterTime(event.Timestamp, pageEnd)
			if when.Before(oldest) {
				oldest = when
			}
		}
		if len(events) < limit || !oldest.After(start) || !oldest.Before(pageEnd) {
			break
		}
		pageEnd = oldest.Add(-time.Nanosecond)
	}
	sort.Slice(all, func(i, j int) bool {
		left := parseAdapterTime(all[i].Timestamp, time.Time{})
		right := parseAdapterTime(all[j].Timestamp, time.Time{})
		if left.Equal(right) {
			return all[i].ID < all[j].ID
		}
		return left.Before(right)
	})
	return all, nil
}

func activityWatchEventRevision(duration float64) int64 {
	if duration < 0 {
		duration = 0
	}
	return int64(math.Round(duration*1000)) + 1
}

func maxActivityInt(left, right int) int {
	if left > right {
		return left
	}
	return right
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
