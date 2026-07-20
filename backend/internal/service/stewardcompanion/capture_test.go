package stewardcompanion

import (
	"context"
	"sync"
	"testing"
	"time"

	"mongojson/backend/internal/service/steward"
)

type captureTestSampler struct {
	mu        sync.Mutex
	snapshots []ActivitySnapshot
}

func (s *captureTestSampler) Sample(context.Context) (ActivitySnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) == 0 {
		return ActivitySnapshot{}, nil
	}
	result := s.snapshots[0]
	s.snapshots = s.snapshots[1:]
	return result, nil
}

type captureTestSink struct {
	items []steward.CreateObservationInput
}

func (s *captureTestSink) Enqueue(_ context.Context, input steward.CreateObservationInput) (string, error) {
	s.items = append(s.items, input)
	return input.SourceEventKey, nil
}

func TestCaptureLoopAdvancesStableRevisionsAndSplitsContext(t *testing.T) {
	base := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	sampler := &captureTestSampler{snapshots: []ActivitySnapshot{
		{CapturedAt: base, Application: "Code", WindowTitle: "one.go", SessionID: "windows-1", IdleFor: time.Second},
		{CapturedAt: base.Add(10 * time.Second), Application: "Code", WindowTitle: "one.go", SessionID: "windows-1", IdleFor: 2 * time.Second},
		{CapturedAt: base.Add(20 * time.Second), Application: "Chrome", WindowTitle: "Docs", SessionID: "windows-1", IdleFor: 6 * time.Minute},
	}}
	sink := &captureTestSink{}
	loop := NewCaptureLoop(sampler, sink, CaptureOptions{Interval: 10 * time.Second, AFKThreshold: 5 * time.Minute, Timezone: "Asia/Shanghai"})
	loop.captureOnce(context.Background())
	loop.captureOnce(context.Background())
	loop.captureOnce(context.Background())
	if len(sink.items) != 6 {
		t.Fatalf("captured %d items, want 6", len(sink.items))
	}
	firstWindow, secondWindow, thirdWindow := sink.items[0], sink.items[2], sink.items[4]
	if firstWindow.SourceEventKey != secondWindow.SourceEventKey || firstWindow.SourceRevision != 1 || secondWindow.SourceRevision != 2 {
		t.Fatalf("stable foreground revision mismatch: first=%#v second=%#v", firstWindow, secondWindow)
	}
	if thirdWindow.SourceEventKey == secondWindow.SourceEventKey || thirdWindow.SourceRevision != 1 {
		t.Fatalf("context change did not create a new segment: %#v", thirdWindow)
	}
	if got := sink.items[5].ContextKey; got != "afk" {
		t.Fatalf("AFK transition context=%q", got)
	}
	if firstWindow.InteractiveSessionID != "windows-1" || firstWindow.SourceTimezone != "Asia/Shanghai" {
		t.Fatalf("source identity missing: %#v", firstWindow)
	}
}

func TestCaptureLoopRotatesLongRunningContextIntoBatchableSegments(t *testing.T) {
	base := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	sampler := &captureTestSampler{snapshots: []ActivitySnapshot{
		{CapturedAt: base, Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
		{CapturedAt: base.Add(30 * time.Minute), Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
	}}
	sink := &captureTestSink{}
	loop := NewCaptureLoop(sampler, sink, CaptureOptions{
		Interval: 10 * time.Second, AFKThreshold: 5 * time.Minute, SegmentDuration: 30 * time.Minute,
	})
	loop.captureOnce(context.Background())
	loop.captureOnce(context.Background())
	if len(sink.items) != 4 {
		t.Fatalf("captured %d items, want 4", len(sink.items))
	}
	if sink.items[0].SourceEventKey == sink.items[2].SourceEventKey || sink.items[2].SourceRevision != 1 {
		t.Fatalf("long-running foreground context was not rotated: first=%#v next=%#v", sink.items[0], sink.items[2])
	}
	if sink.items[1].SourceEventKey == sink.items[3].SourceEventKey || sink.items[3].SourceRevision != 1 {
		t.Fatalf("long-running AFK stream was not rotated: first=%#v next=%#v", sink.items[1], sink.items[3])
	}
}

func TestCaptureLoopAppliesLiveDisableAndSettings(t *testing.T) {
	base := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	sampler := &captureTestSampler{snapshots: []ActivitySnapshot{
		{CapturedAt: base, Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
		{CapturedAt: base.Add(time.Minute), Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
	}}
	sink := &captureTestSink{}
	loop := NewCaptureLoop(sampler, sink, CaptureOptions{Interval: 10 * time.Second, Timezone: "UTC"})
	loop.ApplyControl(false, CaptureOptions{Interval: 25 * time.Second, Timezone: "Asia/Shanghai"})
	loop.captureOnce(context.Background())
	if len(sink.items) != 0 {
		t.Fatalf("disabled capture enqueued %d observations", len(sink.items))
	}
	loop.ApplyControl(true, CaptureOptions{})
	loop.captureOnce(context.Background())
	if len(sink.items) != 2 {
		t.Fatalf("enabled capture enqueued %d observations, want 2", len(sink.items))
	}
	if got := sink.items[0].SourceTimezone; got != "Asia/Shanghai" {
		t.Fatalf("timezone=%q", got)
	}
	if got := sink.items[0].Metadata["capture_interval_seconds"]; got != float64(25) {
		t.Fatalf("capture interval metadata=%v", got)
	}
}

func TestCaptureLoopPauseTerminatesSegmentsAndResumeStartsNewOnes(t *testing.T) {
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	pauseAt := base.Add(5 * time.Second)
	sampler := &captureTestSampler{snapshots: []ActivitySnapshot{
		{CapturedAt: base, Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
		{CapturedAt: base.Add(30 * time.Second), Application: "Code", WindowTitle: "one.go", SessionID: "windows-1"},
	}}
	sink := &captureTestSink{}
	loop := NewCaptureLoop(sampler, sink, CaptureOptions{Interval: 10 * time.Second, Timezone: "UTC"})
	loop.now = func() time.Time { return pauseAt }
	loop.captureOnce(context.Background())
	if len(sink.items) != 2 {
		t.Fatalf("initial capture count=%d", len(sink.items))
	}
	oldWindowKey, oldAFKKey := sink.items[0].SourceEventKey, sink.items[1].SourceEventKey

	loop.ApplyControl(false, CaptureOptions{})
	if len(sink.items) != 4 {
		t.Fatalf("pause did not emit two terminal revisions: %#v", sink.items)
	}
	for index, terminal := range sink.items[2:4] {
		if terminal.SourceRevision != 2 || terminal.EndedAt == nil || !terminal.EndedAt.Equal(pauseAt) {
			t.Fatalf("terminal observation %d=%#v", index, terminal)
		}
		if terminal.Metadata["segment_terminated"] != "capture_disabled" {
			t.Fatalf("terminal observation %d omitted boundary metadata: %#v", index, terminal.Metadata)
		}
	}
	loop.captureOnce(context.Background())
	if len(sink.items) != 4 {
		t.Fatalf("paused capture enqueued observations: %#v", sink.items[4:])
	}

	loop.ApplyControl(true, CaptureOptions{})
	loop.captureOnce(context.Background())
	if len(sink.items) != 6 {
		t.Fatalf("resumed capture count=%d", len(sink.items))
	}
	if sink.items[4].SourceEventKey == oldWindowKey || sink.items[5].SourceEventKey == oldAFKKey {
		t.Fatalf("resumed capture reused pre-pause segments: window=%q afk=%q", sink.items[4].SourceEventKey, sink.items[5].SourceEventKey)
	}
	if sink.items[4].SourceRevision != 1 || sink.items[5].SourceRevision != 1 {
		t.Fatalf("resumed segment did not start at revision 1: %#v %#v", sink.items[4], sink.items[5])
	}
}
