//go:build windows

package steward

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
)

func sessionCompanionHTTPClient(timeout time.Duration) *http.Client {
	pipe := os.Getenv("STEWARD_COMPANION_PIPE")
	if pipe == "" {
		pipe = `\\.\pipe\MongojsonStewardCompanion`
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return winio.DialPipeContext(ctx, pipe)
	}}
	return &http.Client{Transport: transport, Timeout: timeout}
}
