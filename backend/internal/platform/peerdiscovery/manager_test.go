package peerdiscovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestManagersDiscoverEachOtherAndExpireStoppedPeer(t *testing.T) {
	firstAddr := reserveUDPAddr(t)
	secondAddr := reserveUDPAddr(t)
	targets := []string{firstAddr, secondAddr}
	first := testManager(t, "windows-main", firstAddr, targets, "http://127.0.0.1:19181/api")
	second := testManager(t, "linux-lab", secondAddr, targets, "http://127.0.0.1:19201/api")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := first.Start(ctx); err != nil {
		t.Fatalf("start first discovery manager: %v", err)
	}
	defer first.Stop()
	if err := second.Start(ctx); err != nil {
		t.Fatalf("start second discovery manager: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		firstCandidates := first.Candidates()
		secondCandidates := second.Candidates()
		return len(firstCandidates) == 1 && firstCandidates[0].DeviceID == "linux-lab" &&
			len(secondCandidates) == 1 && secondCandidates[0].DeviceID == "windows-main"
	})

	second.Stop()
	waitFor(t, 3*time.Second, func() bool { return len(first.Candidates()) == 0 })
	if status := first.Status(); !status.Enabled || !status.Running || status.CandidateCount != 0 || status.LastAnnouncementAt == nil {
		t.Fatalf("unexpected discovery status after peer expiry: %#v", status)
	}
}

func TestCandidateCatalogHasFixedCapacity(t *testing.T) {
	manager := &Manager{candidates: map[string]domain.StewardDiscoveredPeer{}}
	now := time.Now().UTC()
	for index := 0; index < maxCandidates; index++ {
		candidate := domain.StewardDiscoveredPeer{
			DeviceID: "device", PublicKeyFingerprint: fmt.Sprintf("%064d", index),
			IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Minute), SignatureVerified: true,
		}
		if !manager.storeCandidate(candidate) {
			t.Fatalf("candidate %d unexpectedly rejected", index)
		}
	}
	overflow := domain.StewardDiscoveredPeer{
		DeviceID: "overflow", PublicKeyFingerprint: strings.Repeat("f", 64),
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Minute), SignatureVerified: true,
	}
	if manager.storeCandidate(overflow) {
		t.Fatalf("expected candidate overflow to be rejected")
	}
	if len(manager.candidates) != maxCandidates || manager.rejectedAnnouncements != 1 {
		t.Fatalf("unexpected bounded catalog state: candidates=%d rejected=%d", len(manager.candidates), manager.rejectedAnnouncements)
	}

	updated := domain.StewardDiscoveredPeer{
		DeviceID: "device", PublicKeyFingerprint: fmt.Sprintf("%064d", 0),
		IssuedAt: now.Add(time.Second), LastSeenAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute), SignatureVerified: true,
	}
	if !manager.storeCandidate(updated) {
		t.Fatalf("expected an existing candidate to remain updatable at capacity")
	}
	key := updated.DeviceID + "\x00" + updated.PublicKeyFingerprint
	if got := manager.candidates[key]; !got.IssuedAt.Equal(updated.IssuedAt) {
		t.Fatalf("existing candidate was not updated at capacity: %#v", got)
	}
}

func TestCandidateCatalogRemovesExpiredEntriesBeforeCapacityCheck(t *testing.T) {
	now := time.Now().UTC()
	manager := &Manager{candidates: map[string]domain.StewardDiscoveredPeer{
		"expired": {
			DeviceID: "expired", PublicKeyFingerprint: strings.Repeat("e", 64),
			IssuedAt: now.Add(-time.Minute), LastSeenAt: now.Add(-time.Minute), ExpiresAt: now.Add(-time.Second), SignatureVerified: true,
		},
	}}
	for index := 0; index < maxCandidates; index++ {
		candidate := domain.StewardDiscoveredPeer{
			DeviceID: "device", PublicKeyFingerprint: fmt.Sprintf("%064d", index),
			IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Minute), SignatureVerified: true,
		}
		if !manager.storeCandidate(candidate) {
			t.Fatalf("candidate %d unexpectedly rejected after expired cleanup", index)
		}
	}
	if len(manager.candidates) != maxCandidates || manager.rejectedAnnouncements != 0 {
		t.Fatalf("unexpected catalog state after expired cleanup: candidates=%d rejected=%d", len(manager.candidates), manager.rejectedAnnouncements)
	}
}

func TestOptionsFromEnvRejectsInvalidBooleanAndDurations(t *testing.T) {
	t.Run("boolean", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_ENABLED", "sometimes")
		if _, err := OptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_ENABLED") {
			t.Fatalf("expected invalid enabled error, got %v", err)
		}
	})
	t.Run("interval", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_ENABLED", "true")
		t.Setenv("STEWARD_DISCOVERY_INTERVAL", "later")
		if _, err := OptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_INTERVAL") {
			t.Fatalf("expected invalid interval error, got %v", err)
		}
	})
	t.Run("ttl", func(t *testing.T) {
		t.Setenv("STEWARD_DISCOVERY_ENABLED", "true")
		t.Setenv("STEWARD_DISCOVERY_INTERVAL", "1s")
		t.Setenv("STEWARD_DISCOVERY_TTL", "0s")
		if _, err := OptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "STEWARD_DISCOVERY_TTL") {
			t.Fatalf("expected invalid ttl error, got %v", err)
		}
	})
}

func testManager(t *testing.T, deviceID string, listenAddr string, targets []string, peerAPIBase string) *Manager {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate manager identity: %v", err)
	}
	manager, err := New(Options{
		Enabled: true, ListenAddr: listenAddr, Targets: targets,
		Interval: 40 * time.Millisecond, TTL: 160 * time.Millisecond,
		DeviceID: deviceID, DeviceName: deviceID, Platform: "test", PeerAPIBase: peerAPIBase,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
	})
	if err != nil {
		t.Fatalf("create discovery manager: %v", err)
	}
	return manager
}

func reserveUDPAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("reserve UDP address: %v", err)
	}
	address := listener.LocalAddr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release UDP address: %v", err)
	}
	return address
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
