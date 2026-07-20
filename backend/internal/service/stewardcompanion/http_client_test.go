package stewardcompanion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagementHTTPClientAddsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer companion-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewManagementHTTPClient(" companion-secret ", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestManagementHTTPClientRejectsHeaderInjection(t *testing.T) {
	if _, err := NewManagementHTTPClient("secret\r\nX-Bad: value", time.Second); err == nil {
		t.Fatal("expected newline-bearing token to be rejected")
	}
}

func TestManagementHTTPClientOverridesStaleAuthorizationWithoutMutatingRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer current-secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewManagementHTTPClient("current-secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer stale-secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := request.Header.Get("Authorization"); got != "Bearer stale-secret" {
		t.Fatalf("caller request was mutated: Authorization = %q", got)
	}
}

func TestManagementHTTPClientUsesSafeDefaultTimeout(t *testing.T) {
	client, err := NewManagementHTTPClient("secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 20*time.Second {
		t.Fatalf("Timeout = %s, want 20s", client.Timeout)
	}
}
