//go:build !windows

package steward

import (
	"context"
	"net"
	"net/http"
	"time"
)

func sessionCompanionHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:18182")
	}}
	return &http.Client{Transport: transport, Timeout: timeout}
}
