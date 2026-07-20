package stewardcompanion

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CaptureControl is the main service's authoritative projection for the
// login-session capture loop. Keeping it outside the Companion database avoids
// a second independent business configuration while still allowing the
// Companion to buffer observations when the main service is temporarily down.
type CaptureControl struct {
	CaptureEnabled bool          `json:"capture_enabled"`
	FlushEnabled   bool          `json:"flush_enabled"`
	Interval       time.Duration `json:"-"`
	Timezone       string        `json:"timezone"`
	Revision       int64         `json:"revision"`
}

// CachedCaptureControl is the last control projection accepted from an
// authenticated management API. The payload is encrypted by Buffer before it
// reaches SQLite and is bound to the local API endpoint and bearer credential
// that authenticated it.
type CachedCaptureControl struct {
	Control         CaptureControl `json:"control"`
	AuthenticatedAt time.Time      `json:"authenticated_at"`
}

// CaptureControlCacheBinding returns a non-secret identity for the local API
// and bearer credential. A cache written by an old installation or credential
// cannot silently enable capture after either value changes.
func CaptureControlCacheBinding(apiBase, managementToken string) (string, error) {
	base, err := companionAPIBase(apiBase)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(managementToken)
	if token == "" {
		return "", fmt.Errorf("authenticated capture-control cache requires a management token")
	}
	digest := sha256.Sum256([]byte(base + "\x00" + token))
	return fmt.Sprintf("v1:%x", digest[:]), nil
}

func FetchCaptureControl(ctx context.Context, apiBase string, client *http.Client) (CaptureControl, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	base, err := companionAPIBase(apiBase)
	if err != nil {
		return CaptureControl{}, err
	}
	var settings struct {
		Settings struct {
			Enabled               bool   `json:"enabled"`
			Timezone              string `json:"timezone"`
			ActivitySampleSeconds int    `json:"activity_sample_seconds"`
			Revision              int64  `json:"revision"`
		} `json:"settings"`
	}
	if err := fetchControlJSON(ctx, client, base+"/steward/intelligence-settings", &settings); err != nil {
		return CaptureControl{}, fmt.Errorf("fetch intelligence settings: %w", err)
	}
	var collectors struct {
		Collectors []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"collectors"`
	}
	if err := fetchControlJSON(ctx, client, base+"/steward/collectors", &collectors); err != nil {
		return CaptureControl{}, fmt.Errorf("fetch collectors: %w", err)
	}
	var agent struct {
		Agent struct {
			Status string `json:"status"`
		} `json:"agent"`
	}
	if err := fetchControlJSON(ctx, client, base+"/steward/agent", &agent); err != nil {
		return CaptureControl{}, fmt.Errorf("fetch Agent state: %w", err)
	}
	var runtimeControl struct {
		Control struct {
			Paused     bool  `json:"paused"`
			Stopped    bool  `json:"stopped"`
			Generation int64 `json:"generation"`
		} `json:"control"`
	}
	if err := fetchControlJSON(ctx, client, base+"/steward/runtime/control", &runtimeControl); err != nil {
		return CaptureControl{}, fmt.Errorf("fetch global execution control: %w", err)
	}
	windowsActivityEnabled := false
	for _, collector := range collectors.Collectors {
		if strings.EqualFold(strings.TrimSpace(collector.Name), "windows-activity") {
			windowsActivityEnabled = collector.Enabled
			break
		}
	}
	agentRunning := strings.EqualFold(strings.TrimSpace(agent.Agent.Status), "running")
	executionAllowed := agentRunning && !runtimeControl.Control.Paused && !runtimeControl.Control.Stopped
	interval := time.Duration(settings.Settings.ActivitySampleSeconds) * time.Second
	if interval <= 0 {
		interval = DefaultCaptureInterval
	}
	return CaptureControl{
		CaptureEnabled: settings.Settings.Enabled && windowsActivityEnabled && executionAllowed,
		FlushEnabled:   executionAllowed,
		Interval:       interval,
		Timezone:       strings.TrimSpace(settings.Settings.Timezone),
		Revision:       settings.Settings.Revision + runtimeControl.Control.Generation,
	}, nil
}

func fetchControlJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}
