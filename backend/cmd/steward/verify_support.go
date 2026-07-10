package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c cli) getJSON(path string) (map[string]any, error) {
	return c.getJSONURL(c.apiBase + path)
}

func (c cli) getJSONURL(endpoint string) (map[string]any, error) {
	body, err := c.requestURL(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	return decodeObject(body)
}

func (c cli) postJSON(path string, payload any) (map[string]any, error) {
	body, err := c.request(http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	return decodeObject(body)
}

func decodeObject(body []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func missingStrictSecurityItems(security map[string]any) []string {
	checks := map[string]string{
		"auth_required":                "sync request authentication",
		"peer_api_enabled":             "restricted peer API listener",
		"peer_api_advertised":          "advertised peer API base URL",
		"device_signing_ready":         "device private-key signing",
		"device_identity_advertisable": "device public identity",
		"sync_encryption_configured":   "peer sync payload encryption",
		"local_encryption_configured":  "local sync payload at-rest encryption",
	}
	missing := []string{}
	for key, label := range checks {
		if !boolAt(security, key) {
			missing = append(missing, label)
		}
	}
	if boolAt(security, "insecure_mode_active") {
		missing = append(missing, "insecure sync compatibility disabled")
	}
	if boolAt(security, "management_remote_access") {
		missing = append(missing, "management API bound to loopback")
	}
	return missing
}

func addRuntimeExpectedSyncSecurityChecks(opts runtimeVerifyOptions, security map[string]any, add func(string, string, string, any)) {
	if strings.TrimSpace(opts.ExpectSyncKeyID) != "" {
		addExpectedStringCheck(add, "s3.sync.security.expected_sync_key", "sync encryption key id", opts.ExpectSyncKeyID, stringAt(security, "sync_encryption_key_id"))
	}
	if strings.TrimSpace(opts.ExpectLocalKeyID) != "" {
		addExpectedStringCheck(add, "s3.sync.security.expected_local_key", "local encryption key id", opts.ExpectLocalKeyID, stringAt(security, "local_encryption_key_id"))
	}
	if opts.ExpectSyncPreviousKeyCount != nil {
		addExpectedIntCheck(add, "s3.sync.security.expected_sync_previous_keys", "previous sync encryption key count", *opts.ExpectSyncPreviousKeyCount, intAt(security, "sync_previous_key_count"))
	}
	if opts.ExpectLocalPreviousKeyCount != nil {
		addExpectedIntCheck(add, "s3.sync.security.expected_local_previous_keys", "previous local encryption key count", *opts.ExpectLocalPreviousKeyCount, intAt(security, "local_previous_key_count"))
	}
}

func addRuntimeExpectedAdvisorChecks(opts runtimeVerifyOptions, advisor map[string]any, add func(string, string, string, any)) {
	if strings.TrimSpace(opts.ExpectAdvisorProvider) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_provider", "advisor provider", opts.ExpectAdvisorProvider, stringAt(advisor, "provider"))
	}
	if strings.TrimSpace(opts.ExpectAdvisorModel) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_model", "advisor model", opts.ExpectAdvisorModel, stringAt(advisor, "model"))
	}
	if strings.TrimSpace(opts.ExpectAdvisorMaxDataLevel) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_max_data_level", "advisor max data level", opts.ExpectAdvisorMaxDataLevel, stringAt(advisor, "max_data_level"))
	}
}

func strictAdvisorRuntimeIssues(advisor map[string]any) []string {
	if !boolAt(advisor, "enabled") {
		return nil
	}
	issues := []string{}
	provider := strings.ToLower(strings.TrimSpace(stringAt(advisor, "provider")))
	if provider != "openai-compatible" && provider != "openai" {
		issues = append(issues, "advisor provider must be openai-compatible or openai")
	}
	if strings.TrimSpace(stringAt(advisor, "model")) == "" {
		issues = append(issues, "advisor model must be visible")
	}
	baseURL := strings.TrimSpace(stringAt(advisor, "base_url"))
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			issues = append(issues, "advisor base_url must be an http or https URL with a host")
		}
	}
	maxDataLevel := strings.ToUpper(strings.TrimSpace(stringAt(advisor, "max_data_level")))
	if maxDataLevel == "" {
		maxDataLevel = "D1"
	}
	if maxDataLevel != "D0" && maxDataLevel != "D1" {
		issues = append(issues, "advisor max_data_level must be D0 or D1")
	}
	return issues
}

func addExpectedStringCheck(add func(string, string, string, any), id string, label string, expected string, actual string) {
	expected = strings.TrimSpace(expected)
	if actual == expected {
		add(id, "ok", "runtime reports the expected "+label, map[string]string{"expected": expected, "actual": actual})
		return
	}
	add(id, "error", "runtime "+label+" does not match expected value", map[string]string{"expected": expected, "actual": actual})
}

func addExpectedIntCheck(add func(string, string, string, any), id string, label string, expected int, actual int) {
	if actual == expected {
		add(id, "ok", "runtime reports the expected "+label, map[string]int{"expected": expected, "actual": actual})
		return
	}
	add(id, "error", "runtime "+label+" does not match expected value", map[string]int{"expected": expected, "actual": actual})
}

func compactSyncStatus(sync map[string]any) map[string]any {
	return map[string]any{
		"local_device":      mapAt(sync, "local_device"),
		"pending_changes":   valueAt(sync, "pending_changes"),
		"pending_relations": valueAt(sync, "pending_relations"),
		"capability_count":  len(sliceAt(sync, "capabilities")),
		"conflict_count":    valueAt(sync, "conflict_count"),
		"last_change_at":    valueAt(sync, "last_change_at"),
		"discovery":         mapAt(sync, "discovery"),
		"discovered_peers":  len(sliceAt(sync, "discovered_peers")),
	}
}

func compactRuntimeVerification(result runtimeVerificationResult) map[string]any {
	return map[string]any{
		"ok":      result.OK,
		"api":     result.APIBase,
		"checks":  len(result.Checks),
		"options": result.Options,
	}
}

func compactAdvisorProbe(probe map[string]any) map[string]any {
	status := mapAt(probe, "status")
	suggestion := mapAt(probe, "suggestion")
	return map[string]any{
		"provider":       stringAt(status, "provider"),
		"model":          stringAt(status, "model"),
		"max_data_level": stringAt(status, "max_data_level"),
		"data_level":     stringAt(probe, "data_level"),
		"duration_ms":    valueAt(probe, "duration_ms"),
		"title":          stringAt(suggestion, "title"),
		"probed_at":      stringAt(probe, "probed_at"),
	}
}

func compactPeerSyncResult(sync map[string]any) map[string]any {
	return map[string]any{
		"pulled":               valueAt(sync, "pulled"),
		"imported":             valueAt(sync, "imported"),
		"applied":              valueAt(sync, "applied"),
		"skipped":              valueAt(sync, "skipped"),
		"pushed":               valueAt(sync, "pushed"),
		"denied":               valueAt(sync, "denied"),
		"remote_last_sequence": valueAt(sync, "remote_last_sequence"),
		"local_sent_sequence":  valueAt(sync, "local_sent_sequence"),
		"errors":               valueAt(sync, "errors"),
	}
}

func devicesFromSyncStatus(sync map[string]any) []peerDevice {
	devices := []peerDevice{}
	for _, item := range sliceAt(sync, "devices") {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		devices = append(devices, peerDevice{
			ID:          stringAt(raw, "id"),
			Name:        stringAt(raw, "device_name"),
			Platform:    strings.ToLower(strings.TrimSpace(stringAt(raw, "platform"))),
			Role:        stringAt(raw, "role"),
			TrustStatus: stringAt(raw, "trust_status"),
			SyncEnabled: boolAt(raw, "sync_enabled"),
			PublicKey:   stringAt(raw, "public_key"),
			APIBaseURL:  stringAt(raw, "api_base_url"),
		})
	}
	return devices
}

func peerVerificationSkipReason(device peerDevice) string {
	if strings.EqualFold(device.TrustStatus, "revoked") {
		return "peer is revoked"
	}
	if !device.SyncEnabled {
		return "peer sync is disabled"
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return "peer api_base_url is not configured"
	}
	if strings.TrimSpace(device.PublicKey) == "" {
		return "peer public_key is not configured"
	}
	return ""
}

func syncRecentChangesContain(sync map[string]any, entityType string, entityID string) bool {
	for _, item := range sliceAt(sync, "recent_changes") {
		change, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringAt(change, "entity_type") == entityType && stringAt(change, "entity_id") == entityID {
			return true
		}
	}
	return false
}

func proposalForSourceEntity(autonomy map[string]any, sourceEntityID string) string {
	for _, item := range sliceAt(autonomy, "proposals") {
		proposal, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringAt(proposal, "source_entity_id") == sourceEntityID {
			return stringAt(proposal, "id")
		}
	}
	return ""
}

func parseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func mapAt(input map[string]any, key string) map[string]any {
	value, _ := input[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func sliceAt(input map[string]any, key string) []any {
	value, _ := input[key].([]any)
	if value == nil {
		return []any{}
	}
	return value
}

func stringSliceAt(input map[string]any, key string) []string {
	values := []string{}
	for _, item := range sliceAt(input, key) {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" {
			values = append(values, text)
		}
	}
	return values
}

func valueAt(input map[string]any, key string) any {
	if input == nil {
		return nil
	}
	return input[key]
}

func stringAt(input map[string]any, key string) string {
	value := valueAt(input, key)
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolAt(input map[string]any, key string) bool {
	value := valueAt(input, key)
	asBool, _ := value.(bool)
	return asBool
}

func intAt(input map[string]any, key string) int {
	value := valueAt(input, key)
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		var parsed int
		_, _ = fmt.Sscanf(strings.TrimSpace(fmt.Sprint(value)), "%d", &parsed)
		return parsed
	}
}
