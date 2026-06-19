package jobs

import (
	"testing"
	"time"
)

func TestTryEnqueueReturnsFalseWhenQueueIsFull(t *testing.T) {
	service := NewService(nil, nil, time.Hour)

	for i := 0; i < 64; i += 1 {
		if !service.TryEnqueue("job") {
			t.Fatalf("expected enqueue %d to succeed", i)
		}
	}

	if service.TryEnqueue("overflow") {
		t.Fatal("expected enqueue to fail when the queue is full")
	}
}

func TestSupportsToolTypeIsDisabledUntilProcessorsAreRegistered(t *testing.T) {
	service := NewService(nil, nil, time.Hour)

	for _, toolType := range []string{"json", "mongodb_json", "visualize"} {
		if service.SupportsToolType(toolType) {
			t.Fatalf("expected %q to be disabled in this build", toolType)
		}
	}
}
