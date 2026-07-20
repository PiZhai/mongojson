package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/service/stewardcompanion"
)

func TestReadOptionalSingleLineSecret(t *testing.T) {
	t.Run("normal trailing newline", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "management-access-token.txt")
		if err := os.WriteFile(path, []byte("companion-secret\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readOptionalSingleLineSecret(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != "companion-secret" {
			t.Fatalf("secret = %q", got)
		}
	})

	t.Run("missing optional development file", func(t *testing.T) {
		got, err := readOptionalSingleLineSecret(filepath.Join(t.TempDir(), "missing.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Fatalf("secret = %q, want empty", got)
		}
	})

	t.Run("missing required production file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.txt")
		if _, err := readSingleLineSecret(path, true); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("empty required path", func(t *testing.T) {
		if _, err := readSingleLineSecret("", true); err == nil || !strings.Contains(err.Error(), "is required") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("empty configured file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "management-access-token.txt")
		if err := os.WriteFile(path, []byte(" \r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOptionalSingleLineSecret(path); err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("multiple lines", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "management-access-token.txt")
		if err := os.WriteFile(path, []byte("first\nsecond"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readOptionalSingleLineSecret(path); err == nil || !strings.Contains(err.Error(), "must contain one line") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCacheAuthenticatedCaptureControlRejectsUnauthenticatedState(t *testing.T) {
	ctx := context.Background()
	buffer, err := stewardcompanion.Open(ctx, stewardcompanion.Options{
		Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{19}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()
	binding, err := stewardcompanion.CaptureControlCacheBinding("http://127.0.0.1:18080/api", "token")
	if err != nil {
		t.Fatal(err)
	}
	control := stewardcompanion.CaptureControl{CaptureEnabled: true, FlushEnabled: true, Interval: 10 * time.Second}
	if err := cacheAuthenticatedCaptureControl(ctx, buffer, binding, false, control, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.LoadAuthenticatedCaptureControl(ctx, binding); !errors.Is(err, stewardcompanion.ErrNoCachedCaptureControl) {
		t.Fatalf("unauthenticated control was cached: %v", err)
	}
	if err := cacheAuthenticatedCaptureControl(ctx, buffer, binding, true, control, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.LoadAuthenticatedCaptureControl(ctx, binding); err != nil {
		t.Fatalf("authenticated control was not cached: %v", err)
	}
}

func TestCompanionRuntimeHealthReportsAuthenticationAndDeliveryFailures(t *testing.T) {
	health := newCompanionRuntimeHealth(true, true)
	health.recordControl(errors.New("401 Unauthorized: invalid management bearer token"))
	health.recordFlush(stewardcompanion.FlushResult{Failed: 1, Pending: 4, LastError: "HTTP 401: unauthorized"}, nil)

	snapshot := health.snapshot(false, stewardcompanion.CaptureStatus{Enabled: false})
	if snapshot.Status != "degraded" || snapshot.ManagementAuth.Healthy || snapshot.Control.Healthy || snapshot.Flush.Healthy {
		t.Fatalf("unexpected degraded health snapshot: %#v", snapshot)
	}
	if snapshot.ManagementAuth.LastError == "" || snapshot.Control.LastError == "" || snapshot.Flush.LastError == "" {
		t.Fatalf("health snapshot omitted actionable errors: %#v", snapshot)
	}

	health.recordControl(nil)
	health.recordFlush(stewardcompanion.FlushResult{}, nil)
	snapshot = health.snapshot(true, stewardcompanion.CaptureStatus{Enabled: true, Running: true})
	if snapshot.Status != "ready" || !snapshot.ManagementAuth.Healthy || !snapshot.Control.Healthy || !snapshot.Flush.Healthy || !snapshot.CaptureHealthy {
		t.Fatalf("unexpected recovered health snapshot: %#v", snapshot)
	}
	if snapshot.ManagementAuth.LastFailure == "" || snapshot.Control.LastFailure == "" || snapshot.Flush.LastFailure == "" {
		t.Fatalf("recovery discarded the most recent diagnostic failure: %#v", snapshot)
	}
}

func TestCompanionRuntimeHealthTreatsCaptureErrorAsDegraded(t *testing.T) {
	health := newCompanionRuntimeHealth(false, false)
	health.recordControl(nil)
	snapshot := health.snapshot(true, stewardcompanion.CaptureStatus{Enabled: true, Running: true, LastError: "native sampler failed"})
	if snapshot.Status != "degraded" || snapshot.CaptureHealthy {
		t.Fatalf("capture failure was not reflected in health: %#v", snapshot)
	}
}

func TestCompanionStatusPayloadExposesOperationalHealthAndAPIBase(t *testing.T) {
	health := newCompanionRuntimeHealth(true, true)
	health.recordControl(nil)
	health.recordFlush(stewardcompanion.FlushResult{}, nil)
	payload := health.statusPayload(true, stewardcompanion.CaptureStatus{Enabled: true, Running: true}, 3, 100, []string{"D0"}, "http://127.0.0.1:19090/api/")
	for _, key := range []string{"management_auth", "control", "flush", "activity_capture", "capture_healthy", "flush_healthy", "flush_enabled"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("status payload omitted %q: %#v", key, payload)
		}
	}
	if payload["status"] != "ready" || payload["api_base"] != "http://127.0.0.1:19090/api" {
		t.Fatalf("unexpected status payload: %#v", payload)
	}
}
