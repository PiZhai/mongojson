package peerdiscovery

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignedAnnouncementVerificationAndTamperDetection(t *testing.T) {
	identity := testIdentity(t, "windows-main", "http://192.0.2.10:18081/api")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	packet, err := buildSignedAnnouncement(identity, now, bytes.NewReader(bytes.Repeat([]byte{7}, 16)))
	if err != nil {
		t.Fatalf("build announcement: %v", err)
	}
	candidate, err := verifyAnnouncement(packet, now.Add(time.Second), 45*time.Second)
	if err != nil {
		t.Fatalf("verify announcement: %v", err)
	}
	if candidate.DeviceID != identity.DeviceID || candidate.PeerAPIBase != identity.PeerAPIBase || !candidate.SignatureVerified {
		t.Fatalf("unexpected verified candidate: %#v", candidate)
	}
	if candidate.ExpiresAt != now.Add(45*time.Second) {
		t.Fatalf("expiration must derive from signed issue time: %s", candidate.ExpiresAt)
	}

	var tampered signedAnnouncement
	if err := json.Unmarshal(packet, &tampered); err != nil {
		t.Fatalf("decode announcement for tamper test: %v", err)
	}
	tampered.PeerAPIBase = "http://198.51.100.20:18081/api"
	tamperedPacket, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal tampered announcement: %v", err)
	}
	if _, err := verifyAnnouncement(tamperedPacket, now.Add(time.Second), 45*time.Second); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature failure for tampered announcement, got %v", err)
	}
}

func TestAnnouncementRejectsExpiredAndFutureTimestamps(t *testing.T) {
	identity := testIdentity(t, "linux-lab", "http://192.0.2.30:18081/api")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	expired, err := buildSignedAnnouncement(identity, now.Add(-time.Minute), bytes.NewReader(bytes.Repeat([]byte{1}, 16)))
	if err != nil {
		t.Fatalf("build expired announcement: %v", err)
	}
	if _, err := verifyAnnouncement(expired, now, 45*time.Second); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired announcement error, got %v", err)
	}

	future, err := buildSignedAnnouncement(identity, now.Add(maxClockSkew+time.Second), bytes.NewReader(bytes.Repeat([]byte{2}, 16)))
	if err != nil {
		t.Fatalf("build future announcement: %v", err)
	}
	if _, err := verifyAnnouncement(future, now, 45*time.Second); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("expected future announcement error, got %v", err)
	}
}

func TestNewRejectsMismatchedDiscoveryKeys(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	_, err = New(Options{
		Enabled: true, ListenAddr: "127.0.0.1:18777", Targets: []string{"127.0.0.1:18778"},
		Interval: time.Second, TTL: 2 * time.Second, DeviceID: "device-a", DeviceName: "Device A",
		Platform: "windows", PeerAPIBase: "http://192.0.2.10:18081/api",
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), PrivateKey: base64.StdEncoding.EncodeToString(otherPrivate),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched key error, got %v", err)
	}
}

func testIdentity(t *testing.T, deviceID string, peerAPIBase string) normalizedIdentity {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	return normalizedIdentity{
		DeviceID:      deviceID,
		DeviceName:    deviceID,
		Platform:      "test",
		PeerAPIBase:   peerAPIBase,
		PublicKeyText: base64.StdEncoding.EncodeToString(publicKey),
		PublicKey:     publicKey,
		PrivateKey:    privateKey,
	}
}
