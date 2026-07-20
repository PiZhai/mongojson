package stewardcompanion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"mongojson/backend/internal/service/steward"
)

const (
	DefaultCaptureInterval = 10 * time.Second
	DefaultAFKThreshold    = 5 * time.Minute
	DefaultSegmentDuration = 30 * time.Minute
)

// ActivitySnapshot is the platform-neutral result of one interactive-session
// sample. Native API access lives behind ActivitySampler so the state machine
// remains deterministic and testable on every build platform.
type ActivitySnapshot struct {
	CapturedAt  time.Time
	Application string
	WindowTitle string
	ProcessID   uint32
	SessionID   string
	IdleFor     time.Duration
}

type ActivitySampler interface {
	Sample(context.Context) (ActivitySnapshot, error)
}

type ObservationSink interface {
	Enqueue(context.Context, steward.CreateObservationInput) (string, error)
}

type CaptureOptions struct {
	Interval        time.Duration
	AFKThreshold    time.Duration
	SegmentDuration time.Duration
	Timezone        string
	Logger          *log.Logger
}

type CaptureStatus struct {
	Enabled       bool       `json:"enabled"`
	Running       bool       `json:"running"`
	SessionID     string     `json:"session_id,omitempty"`
	LastSampleAt  *time.Time `json:"last_sample_at,omitempty"`
	LastEnqueueAt *time.Time `json:"last_enqueue_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type CaptureLoop struct {
	sampler ActivitySampler
	sink    ObservationSink
	options CaptureOptions
	now     func() time.Time

	// captureMu serializes sampling/enqueue with control transitions. Once a
	// disabling ApplyControl call returns, no in-flight sample can append to an
	// old segment and both active streams have been terminated.
	captureMu sync.Mutex
	mu        sync.RWMutex
	status    CaptureStatus
	window    captureSegment
	afk       captureSegment
	sequence  uint64
}

type captureSegment struct {
	eventKey string
	context  string
	started  time.Time
	revision int64
	last     steward.CreateObservationInput
}

func NewCaptureLoop(sampler ActivitySampler, sink ObservationSink, options CaptureOptions) *CaptureLoop {
	if options.Interval <= 0 {
		options.Interval = DefaultCaptureInterval
	}
	if options.AFKThreshold <= 0 {
		options.AFKThreshold = DefaultAFKThreshold
	}
	if options.SegmentDuration <= 0 {
		options.SegmentDuration = DefaultSegmentDuration
	}
	if strings.TrimSpace(options.Timezone) == "" {
		options.Timezone = time.Local.String()
	}
	return &CaptureLoop{
		sampler: sampler,
		sink:    sink,
		options: options,
		now:     time.Now,
		status:  CaptureStatus{Enabled: sampler != nil && sink != nil},
	}
}

func (l *CaptureLoop) Status() CaptureStatus {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status
}

// ApplyControl updates the live capture switch and sampling parameters without
// restarting the login-session Companion. The main service periodically sends
// its durable intelligence, collector and emergency-stop state through this
// method so a UI pause is not merely cosmetic.
func (l *CaptureLoop) ApplyControl(enabled bool, options CaptureOptions) {
	if l == nil {
		return
	}
	l.captureMu.Lock()
	defer l.captureMu.Unlock()
	l.mu.Lock()
	wasEnabled := l.status.Enabled
	nextEnabled := enabled && l.sampler != nil && l.sink != nil
	l.status.Enabled = nextEnabled
	if options.Interval > 0 {
		l.options.Interval = options.Interval
	}
	if options.AFKThreshold > 0 {
		l.options.AFKThreshold = options.AFKThreshold
	}
	if options.SegmentDuration > 0 {
		l.options.SegmentDuration = options.SegmentDuration
	}
	if strings.TrimSpace(options.Timezone) != "" {
		l.options.Timezone = strings.TrimSpace(options.Timezone)
	}
	now := l.now
	l.mu.Unlock()

	if !nextEnabled {
		// Even a repeated disable clears any stale segment state recovered from
		// an interrupted transition. When capture was live, write one terminal
		// revision so the durable session ends exactly at the pause boundary.
		if wasEnabled {
			endedAt := time.Now().UTC()
			if now != nil {
				endedAt = now().UTC()
			}
			l.terminateSegment(&l.window, endedAt)
			l.terminateSegment(&l.afk, endedAt)
		}
		l.window = captureSegment{}
		l.afk = captureSegment{}
	}
}

func (l *CaptureLoop) controlSnapshot() (bool, CaptureOptions) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status.Enabled, l.options
}

func (l *CaptureLoop) Run(ctx context.Context) {
	if l == nil || l.sampler == nil || l.sink == nil {
		return
	}
	l.mu.Lock()
	l.status.Running = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.status.Running = false
		l.mu.Unlock()
	}()

	// Capture immediately so a newly logged-in session becomes visible without
	// waiting for the first timer edge.
	l.captureOnce(ctx)
	for {
		_, options := l.controlSnapshot()
		interval := options.Interval
		if interval <= 0 {
			interval = DefaultCaptureInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			l.captureOnce(ctx)
		}
	}
}

func (l *CaptureLoop) captureOnce(ctx context.Context) {
	l.captureMu.Lock()
	defer l.captureMu.Unlock()
	enabled, options := l.controlSnapshot()
	if !enabled {
		return
	}
	snapshot, err := l.sampler.Sample(ctx)
	if err != nil {
		l.setError(err)
		return
	}
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now().UTC()
	} else {
		snapshot.CapturedAt = snapshot.CapturedAt.UTC()
	}
	snapshot.SessionID = strings.TrimSpace(snapshot.SessionID)
	l.mu.Lock()
	seenAt := snapshot.CapturedAt
	l.status.LastSampleAt = &seenAt
	l.status.SessionID = snapshot.SessionID
	l.status.LastError = ""
	l.mu.Unlock()

	windowContext := strings.ToLower(strings.TrimSpace(snapshot.Application)) + "|" + strings.TrimSpace(snapshot.WindowTitle)
	if strings.Trim(windowContext, "|") != "" {
		input := l.nextWindowObservation(snapshot, windowContext, options)
		if _, err := l.sink.Enqueue(ctx, input); err != nil {
			l.setError(fmt.Errorf("enqueue foreground activity: %w", err))
			return
		}
	}
	afkState := "active"
	if snapshot.IdleFor >= options.AFKThreshold {
		afkState = "afk"
	}
	input := l.nextAFKObservation(snapshot, afkState, options)
	if _, err := l.sink.Enqueue(ctx, input); err != nil {
		l.setError(fmt.Errorf("enqueue AFK activity: %w", err))
		return
	}
	l.mu.Lock()
	enqueuedAt := time.Now().UTC()
	l.status.LastEnqueueAt = &enqueuedAt
	l.status.LastError = ""
	l.mu.Unlock()
}

func (l *CaptureLoop) nextWindowObservation(snapshot ActivitySnapshot, contextKey string, options CaptureOptions) steward.CreateObservationInput {
	segment := l.advanceSegment(&l.window, "foreground", snapshot.SessionID, contextKey, snapshot.CapturedAt, options.SegmentDuration)
	endedAt := snapshot.CapturedAt.Add(options.Interval)
	summary := strings.TrimSpace(strings.Join(nonEmptyCaptureStrings(snapshot.Application, snapshot.WindowTitle), " · "))
	input := steward.CreateObservationInput{
		Source: "companion:windows-activity", Type: "foreground_window", Summary: summary,
		SourceEventKey: segment.eventKey, SourceRevision: segment.revision,
		InteractiveSessionID: snapshot.SessionID, SourceTimezone: options.Timezone,
		ContextKey: contextKey, Fingerprint: captureFingerprint(contextKey),
		Payload: map[string]any{
			"application": snapshot.Application, "window_title": snapshot.WindowTitle,
			"process_id": snapshot.ProcessID, "idle_seconds": snapshot.IdleFor.Seconds(),
		},
		EntityHints: []steward.ObservationEntityHint{{
			Type: "application", CanonicalKey: strings.ToLower(snapshot.Application), DisplayName: snapshot.Application,
		}},
		OccurredAt: &segment.started, EndedAt: &endedAt,
		Metadata: map[string]any{
			"adapter": "windows-companion", "duration_seconds": endedAt.Sub(segment.started).Seconds(),
			"capture_interval_seconds": options.Interval.Seconds(),
			"segment_duration_seconds": options.SegmentDuration.Seconds(),
			"source_event_key":         segment.eventKey, "source_revision": segment.revision,
			"interactive_session_id": snapshot.SessionID, "source_timezone": options.Timezone,
		},
	}
	l.window.last = input
	return input
}

func (l *CaptureLoop) nextAFKObservation(snapshot ActivitySnapshot, state string, options CaptureOptions) steward.CreateObservationInput {
	segment := l.advanceSegment(&l.afk, "afk", snapshot.SessionID, state, snapshot.CapturedAt, options.SegmentDuration)
	endedAt := snapshot.CapturedAt.Add(options.Interval)
	input := steward.CreateObservationInput{
		Source: "companion:windows-activity", Type: "afk_status", Summary: state,
		SourceEventKey: segment.eventKey, SourceRevision: segment.revision,
		InteractiveSessionID: snapshot.SessionID, SourceTimezone: options.Timezone,
		ContextKey: state, Fingerprint: captureFingerprint(state),
		Payload:    map[string]any{"status": state, "idle_seconds": snapshot.IdleFor.Seconds()},
		OccurredAt: &segment.started, EndedAt: &endedAt,
		Metadata: map[string]any{
			"adapter": "windows-companion", "duration_seconds": endedAt.Sub(segment.started).Seconds(),
			"capture_interval_seconds": options.Interval.Seconds(),
			"segment_duration_seconds": options.SegmentDuration.Seconds(),
			"source_event_key":         segment.eventKey, "source_revision": segment.revision,
			"interactive_session_id": snapshot.SessionID, "source_timezone": options.Timezone,
		},
	}
	l.afk.last = input
	return input
}

func (l *CaptureLoop) terminateSegment(segment *captureSegment, endedAt time.Time) {
	if segment == nil || segment.eventKey == "" || strings.TrimSpace(segment.last.SourceEventKey) == "" {
		return
	}
	input := segment.last
	if input.OccurredAt != nil && endedAt.Before(input.OccurredAt.UTC()) {
		endedAt = input.OccurredAt.UTC()
	}
	input.SourceRevision = segment.revision + 1
	input.EndedAt = &endedAt
	input.Metadata = cloneCaptureMetadata(input.Metadata)
	if input.OccurredAt != nil {
		input.Metadata["duration_seconds"] = endedAt.Sub(input.OccurredAt.UTC()).Seconds()
	}
	input.Metadata["source_revision"] = input.SourceRevision
	input.Metadata["segment_terminated"] = "capture_disabled"
	if _, err := l.sink.Enqueue(context.Background(), input); err != nil {
		l.setError(fmt.Errorf("terminate %s activity segment: %w", input.Type, err))
	}
}

func cloneCaptureMetadata(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (l *CaptureLoop) advanceSegment(segment *captureSegment, kind, sessionID, contextKey string, now time.Time, segmentDuration time.Duration) captureSegment {
	if segment.eventKey == "" || segment.context != contextKey || now.Sub(segment.started) >= segmentDuration {
		l.sequence++
		segment.context = contextKey
		segment.started = now
		segment.revision = 1
		segment.eventKey = fmt.Sprintf("windows:%s:%s:%d:%d", kind, defaultCaptureString(sessionID, "unknown"), now.UnixNano(), l.sequence)
	} else {
		segment.revision++
	}
	return *segment
}

func (l *CaptureLoop) setError(err error) {
	if l.options.Logger != nil {
		l.options.Logger.Printf("activity capture failed: %v", err)
	}
	l.mu.Lock()
	l.status.LastError = err.Error()
	l.mu.Unlock()
}

func captureFingerprint(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func nonEmptyCaptureStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func defaultCaptureString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
