package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/platform/peerdiscovery"
	"mongojson/backend/internal/platform/servicecontrol"
)

func TestServiceInstallDiscoveryEnvIncludesExplicitConfiguration(t *testing.T) {
	fs := flag.NewFlagSet("discovery", flag.ContinueOnError)
	enabled := fs.Bool("discovery-enabled", false, "")
	deviceName := fs.String("device-name", "", "")
	listenAddr := fs.String("discovery-listen-addr", peerdiscovery.DefaultListenAddr, "")
	targets := fs.String("discovery-targets", "", "")
	interval := fs.Duration("discovery-interval", peerdiscovery.DefaultInterval, "")
	ttl := fs.Duration("discovery-ttl", peerdiscovery.DefaultTTL, "")
	if err := fs.Parse([]string{
		"--discovery-enabled", "--device-name", "Windows Main",
		"--discovery-listen-addr", "127.0.0.1:18777",
		"--discovery-targets", "127.0.0.1:18778,127.0.0.1:18779",
		"--discovery-interval", "5s", "--discovery-ttl", "15s",
	}); err != nil {
		t.Fatalf("parse discovery flags: %v", err)
	}
	env := serviceInstallDiscoveryEnv(fs, serviceInstallDiscoveryFlagValues{
		Enabled: *enabled, DeviceName: *deviceName, ListenAddr: *listenAddr,
		Targets: *targets, Interval: *interval, TTL: *ttl,
	})
	if env["STEWARD_DISCOVERY_ENABLED"] != "true" || env["STEWARD_DEVICE_NAME"] != "Windows Main" ||
		env["STEWARD_DISCOVERY_INTERVAL"] != "5s" || env["STEWARD_DISCOVERY_TTL"] != "15s" {
		t.Fatalf("unexpected discovery service environment: %#v", env)
	}
}

func TestValidateServiceDiscoveryEnvironment(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate discovery keys: %v", err)
	}
	options := servicecontrol.InstallOptions{
		AgentID:          "windows-main",
		PublicAPIBase:    "http://192.0.2.10:18081/api",
		DevicePublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		DevicePrivateKey: base64.StdEncoding.EncodeToString(privateKey),
		ExtraEnv: map[string]string{
			"STEWARD_DISCOVERY_ENABLED":     "true",
			"STEWARD_DEVICE_NAME":           "Windows Main",
			"STEWARD_DISCOVERY_LISTEN_ADDR": "127.0.0.1:18777",
			"STEWARD_DISCOVERY_TARGETS":     "127.0.0.1:18778",
			"STEWARD_DISCOVERY_INTERVAL":    "5s",
			"STEWARD_DISCOVERY_TTL":         "15s",
		},
	}
	if err := validateServiceDiscoveryEnvironment(options); err != nil {
		t.Fatalf("validate discovery environment: %v", err)
	}
	options.ExtraEnv["STEWARD_DISCOVERY_TTL"] = (2 * time.Second).String()
	if err := validateServiceDiscoveryEnvironment(options); err == nil {
		t.Fatalf("expected TTL/interval validation error")
	}
}

func TestServiceInstallOptionsFromEnvPreservesDiscoveryValues(t *testing.T) {
	env := map[string]string{
		"STEWARD_AGENT_ID":              "linux-lab",
		"STEWARD_DISCOVERY_ENABLED":     "true",
		"STEWARD_DISCOVERY_LISTEN_ADDR": peerdiscovery.DefaultListenAddr,
		"STEWARD_DISCOVERY_INTERVAL":    "15s",
		"STEWARD_DISCOVERY_TTL":         "45s",
	}
	options := serviceInstallOptionsFromEnv("mongojson-steward", env)
	if options.ExtraEnv["STEWARD_DISCOVERY_ENABLED"] != "true" ||
		options.ExtraEnv["STEWARD_DISCOVERY_LISTEN_ADDR"] != peerdiscovery.DefaultListenAddr {
		t.Fatalf("discovery environment was not preserved: %#v", options.ExtraEnv)
	}
}

func TestServiceInstallDiscoveryDefaultsFromEnvAreStrict(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_ENABLED", "true")
		t.Setenv("STEWARD_DISCOVERY_INTERVAL", "5s")
		t.Setenv("STEWARD_DISCOVERY_TTL", "15s")
		defaults, err := serviceInstallDiscoveryDefaultsFromEnv("windows-main")
		if err != nil {
			t.Fatalf("load valid discovery defaults: %v", err)
		}
		if !defaults.Enabled || defaults.DeviceName != "windows-main" || defaults.Interval != 5*time.Second || defaults.TTL != 15*time.Second {
			t.Fatalf("unexpected discovery defaults: %#v", defaults)
		}
	})
	t.Run("invalid boolean", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_ENABLED", "sometimes")
		if _, err := serviceInstallDiscoveryDefaultsFromEnv("windows-main"); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_ENABLED") {
			t.Fatalf("expected invalid discovery boolean error, got %v", err)
		}
	})
	t.Run("invalid interval", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_INTERVAL", "later")
		if _, err := serviceInstallDiscoveryDefaultsFromEnv("windows-main"); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_INTERVAL") {
			t.Fatalf("expected invalid discovery interval error, got %v", err)
		}
	})
	t.Run("invalid ttl", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_TTL", "0s")
		if _, err := serviceInstallDiscoveryDefaultsFromEnv("windows-main"); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_TTL") {
			t.Fatalf("expected invalid discovery ttl error, got %v", err)
		}
	})
}
