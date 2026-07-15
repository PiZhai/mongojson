package stewardcompanion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mongojson/backend/internal/service/steward"
)

func TestBufferEncryptsBlocksD5AndFlushes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "companion.db")
	buffer, err := Open(ctx, Options{Path: path, Key: bytes.Repeat([]byte{7}, 32), MaxPending: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()

	secret := steward.CreateObservationInput{DataLevel: steward.DataD3, Summary: "password=never-store-this-value"}
	if _, err := buffer.Enqueue(ctx, secret); !errors.Is(err, steward.ErrCredentialDataBlocked) {
		t.Fatalf("expected D5 block before SQLite, got %v", err)
	}
	input := steward.CreateObservationInput{
		Source: "test", Type: "window", DataLevel: steward.DataD2,
		Summary: "plaintext-must-not-appear-in-sqlite", Payload: map[string]any{"application": "code"},
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Enqueue(ctx, input); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected bounded queue, got %v", err)
	}
	for _, candidate := range []string{path, path + "-wal"} {
		content, err := os.ReadFile(candidate)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(input.Summary)) {
			t.Fatalf("plaintext observation leaked into %s", candidate)
		}
	}

	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got steward.CreateObservationInput
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode flushed payload: %v", err)
		}
		if got.Summary != input.Summary {
			t.Errorf("unexpected decrypted payload: %q", got.Summary)
		}
		received++
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	result, err := buffer.Flush(ctx, server.URL, server.Client(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Submitted != 2 || result.Pending != 0 || received != 2 {
		t.Fatalf("unexpected flush result: %#v received=%d", result, received)
	}
}

func TestBufferAllowsD5OnlyWhenExplicitlyConfigured(t *testing.T) {
	ctx := context.Background()
	buffer, err := Open(ctx, Options{
		Path: filepath.Join(t.TempDir(), "companion.db"), Key: bytes.Repeat([]byte{8}, 32),
		AllowedDataLevels: []string{steward.DataD5},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Close()

	input := steward.CreateObservationInput{DataLevel: steward.DataD2, Summary: "password=explicitly-authorized-secret"}
	if _, err := buffer.Enqueue(ctx, input); err != nil {
		t.Fatalf("explicitly configured D5 observation was rejected: %v", err)
	}
	if got := buffer.AllowedDataLevels(); len(got) != 1 || got[0] != steward.DataD5 {
		t.Fatalf("allowed levels = %#v", got)
	}
}
