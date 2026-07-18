package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadRejectsRemoteManagementWithoutExplicitOptIn(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":19080")
	t.Setenv("STEWARD_PEER_HTTP_ADDR", ":19081")
	t.Setenv("STEWARD_ALLOW_REMOTE_MANAGEMENT", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STEWARD_ALLOW_REMOTE_MANAGEMENT") {
		t.Fatalf("Load() error = %v, want remote-management boundary error", err)
	}
}

func TestLoadAcceptsSplitManagementAndPeerListeners(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:19080")
	t.Setenv("STEWARD_PEER_HTTP_ADDR", ":19081")
	t.Setenv("STEWARD_UI_DIR", `C:\steward-ui`)
	t.Setenv("STEWARD_ALLOW_REMOTE_MANAGEMENT", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:19080" || cfg.PeerHTTPAddr != ":19081" {
		t.Fatalf("listener config = management %q peer %q", cfg.HTTPAddr, cfg.PeerHTTPAddr)
	}
	if cfg.StewardUIDir != `C:\steward-ui` {
		t.Fatalf("ui dir = %q", cfg.StewardUIDir)
	}
}

func TestLoadUsesStewardManagementDefault(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("STEWARD_PEER_HTTP_ADDR", "")
	t.Setenv("STEWARD_ALLOW_REMOTE_MANAGEMENT", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, DefaultHTTPAddr)
	}
}

func TestLoadRejectsInvalidRemoteManagementSwitch(t *testing.T) {
	t.Setenv("STEWARD_ALLOW_REMOTE_MANAGEMENT", "sometimes")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STEWARD_ALLOW_REMOTE_MANAGEMENT") {
		t.Fatalf("Load() error = %v, want invalid boolean error", err)
	}
}

func TestLoadRequiresManagementTokenInHardenedMode(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "true")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STEWARD_MANAGEMENT_AUTH_TOKEN") {
		t.Fatalf("Load() error = %v, want missing management token error", err)
	}
}

func TestLoadRequiresManagementTokenForRestrictedService(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "false")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "")
	t.Setenv("STEWARD_RESTRICTED_SERVICE", "true")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STEWARD_MANAGEMENT_AUTH_TOKEN") {
		t.Fatalf("restricted-service Load() error = %v, want missing management token error", err)
	}
}

func TestLoadRequiresManagementTokenForRemoteManagement(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":19080")
	t.Setenv("STEWARD_ALLOW_REMOTE_MANAGEMENT", "true")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "false")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "STEWARD_MANAGEMENT_AUTH_TOKEN") {
		t.Fatalf("remote-management Load() error = %v, want missing management token error", err)
	}
}

func TestLoadEnablesManagementAuthenticationWhenTokenConfigured(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "false")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", strings.Repeat("m", 32))
	t.Setenv("STEWARD_MANAGEMENT_ALLOWED_ORIGINS", "https://console.example.test, HTTPS://CONSOLE.EXAMPLE.TEST, http://127.0.0.1:4174")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.ManagementAuthRequired || cfg.ManagementAuthToken != strings.Repeat("m", 32) {
		t.Fatalf("management auth config = required:%t token:%q", cfg.ManagementAuthRequired, cfg.ManagementAuthToken)
	}
	wantOrigins := []string{"https://console.example.test", "http://127.0.0.1:4174"}
	if !reflect.DeepEqual(cfg.ManagementAllowedOrigins, wantOrigins) {
		t.Fatalf("allowed origins = %#v, want %#v", cfg.ManagementAllowedOrigins, wantOrigins)
	}
}

func TestLoadRejectsWeakManagementTokenAndWildcardOrigin(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "short")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "at least 32") {
		t.Fatalf("weak token Load() error = %v", err)
	}

	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", strings.Repeat("m", 32))
	t.Setenv("STEWARD_MANAGEMENT_ALLOWED_ORIGINS", "*")
	_, err = Load()
	if err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("wildcard origin Load() error = %v", err)
	}
}
