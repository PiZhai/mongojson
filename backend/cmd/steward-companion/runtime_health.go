package main

import (
	"strings"
	"sync"
	"time"

	"mongojson/backend/internal/service/stewardcompanion"
)

type companionOperationHealth struct {
	Healthy       bool       `json:"healthy"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastFailure   string     `json:"last_failure,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type companionAuthHealth struct {
	Required      bool       `json:"required"`
	Configured    bool       `json:"configured"`
	Healthy       bool       `json:"healthy"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastFailure   string     `json:"last_failure,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type companionRuntimeHealthSnapshot struct {
	Status         string                   `json:"status"`
	ManagementAuth companionAuthHealth      `json:"management_auth"`
	Control        companionOperationHealth `json:"control"`
	Flush          companionOperationHealth `json:"flush"`
	CaptureHealthy bool                     `json:"capture_healthy"`
	FlushEnabled   bool                     `json:"flush_enabled"`
}

type companionRuntimeHealth struct {
	mu      sync.RWMutex
	auth    companionAuthHealth
	control companionOperationHealth
	flush   companionOperationHealth
}

func newCompanionRuntimeHealth(required, configured bool) *companionRuntimeHealth {
	return &companionRuntimeHealth{
		auth: companionAuthHealth{
			Required: required, Configured: configured,
			Healthy: !required || configured,
		},
		flush: companionOperationHealth{Healthy: true},
	}
}

func (h *companionRuntimeHealth) recordControl(err error) {
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.control.LastAttemptAt = &now
	if err == nil {
		h.control.Healthy = true
		h.control.LastSuccessAt = &now
		h.control.LastError = ""
		if h.auth.Configured || !h.auth.Required {
			h.auth.Healthy = true
			h.auth.LastError = ""
		}
		return
	}
	h.control.Healthy = false
	h.control.LastFailureAt = &now
	h.control.LastFailure = err.Error()
	h.control.LastError = err.Error()
	if isCompanionAuthenticationError(err.Error()) {
		h.auth.Healthy = false
		h.auth.LastFailureAt = &now
		h.auth.LastFailure = err.Error()
		h.auth.LastError = err.Error()
	}
}

func (h *companionRuntimeHealth) recordFlush(result stewardcompanion.FlushResult, err error) {
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.flush.LastAttemptAt = &now
	message := ""
	if err != nil {
		message = err.Error()
	} else if result.Failed > 0 {
		message = strings.TrimSpace(result.LastError)
		if message == "" {
			message = "one or more buffered envelopes were rejected"
		}
	}
	if message == "" {
		h.flush.Healthy = true
		h.flush.LastSuccessAt = &now
		h.flush.LastError = ""
		return
	}
	h.flush.Healthy = false
	h.flush.LastFailureAt = &now
	h.flush.LastFailure = message
	h.flush.LastError = message
	if isCompanionAuthenticationError(message) {
		h.auth.Healthy = false
		h.auth.LastFailureAt = &now
		h.auth.LastFailure = message
		h.auth.LastError = message
	}
}

func (h *companionRuntimeHealth) snapshot(flushEnabled bool, capture stewardcompanion.CaptureStatus) companionRuntimeHealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	captureHealthy := strings.TrimSpace(capture.LastError) == "" && (!capture.Enabled || capture.Running)
	status := "ready"
	if !h.auth.Healthy || !h.control.Healthy || !h.flush.Healthy || !captureHealthy {
		status = "degraded"
	}
	return companionRuntimeHealthSnapshot{
		Status: status, ManagementAuth: h.auth, Control: h.control, Flush: h.flush,
		CaptureHealthy: captureHealthy, FlushEnabled: flushEnabled,
	}
}

func (h *companionRuntimeHealth) statusPayload(flushEnabled bool, capture stewardcompanion.CaptureStatus, pending, capacity int, allowedDataLevels []string, apiBase string) map[string]any {
	health := h.snapshot(flushEnabled, capture)
	return map[string]any{
		"status": health.Status, "pending": pending, "capacity": capacity,
		"allowed_data_levels": allowedDataLevels, "flush_enabled": health.FlushEnabled,
		"flush_healthy": health.Flush.Healthy, "capture_healthy": health.CaptureHealthy,
		"management_auth": health.ManagementAuth, "control": health.Control, "flush": health.Flush,
		"activity_capture": capture, "api_base": strings.TrimRight(strings.TrimSpace(apiBase), "/"),
	}
}

func isCompanionAuthenticationError(message string) bool {
	value := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(value, "401") || strings.Contains(value, "403") ||
		strings.Contains(value, "unauthorized") || strings.Contains(value, "forbidden") ||
		strings.Contains(value, "authentication")
}
