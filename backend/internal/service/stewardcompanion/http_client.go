package stewardcompanion

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// NewManagementHTTPClient creates the client used by the logged-in Session
// Companion to submit its durable outbox to the locally protected management
// API. The token is applied at request time so every envelope kind (activity,
// notification feedback, and future Companion events) follows the same
// authentication contract.
func NewManagementHTTPClient(token string, timeout time.Duration) (*http.Client, error) {
	token = strings.TrimSpace(token)
	if strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("management access token must be a single line")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	transport := http.DefaultTransport
	if token != "" {
		transport = bearerTransport{token: token, next: transport}
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

type bearerTransport struct {
	token string
	next  http.RoundTripper
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(clone)
}
