package stewardcompanion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBrowserLaunchURLUsesAuthenticatedLoopbackEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/browser-tickets" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer companion-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ticket":"abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"}`))
	}))
	defer server.Close()
	client, err := NewManagementHTTPClient("companion-secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	launchURL, err := RequestBrowserLaunchURL(context.Background(), server.URL+"/api", client)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(launchURL, server.URL+"/api/auth/browser-tickets/") {
		t.Fatalf("launch URL = %q", launchURL)
	}
}

func TestRequestBrowserLaunchURLRejectsNonLoopbackAPI(t *testing.T) {
	if _, err := RequestBrowserLaunchURL(context.Background(), "https://example.com/api", http.DefaultClient); err == nil {
		t.Fatal("non-loopback API was accepted")
	}
}
