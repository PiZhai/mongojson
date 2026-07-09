package config

import (
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
