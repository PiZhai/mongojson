package steward

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyncRequestSignatureVerifies(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	body := []byte(`{"changes":[]}`)
	req := httptest.NewRequest("POST", "http://peer.local/api/steward/sync/changes/import?limit=10", strings.NewReader(string(body)))

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", Secret: "shared-secret"}, now)

	if err := verifySyncRequestSignature("shared-secret", now.Add(30*time.Second), req, body); err != nil {
		t.Fatalf("expected signature to verify: %v", err)
	}
}

func TestSyncRequestSignatureRejectsBodyMismatch(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	body := []byte(`{"changes":[]}`)
	req := httptest.NewRequest("POST", "http://peer.local/api/steward/sync/changes/import", strings.NewReader(string(body)))

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", Secret: "shared-secret"}, now)

	if err := verifySyncRequestSignature("shared-secret", now, req, []byte(`{"changes":[{}]}`)); err == nil {
		t.Fatalf("expected body mismatch to fail")
	}
}

func TestSyncRequestSignatureRejectsExpiredTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	body := []byte{}
	req := httptest.NewRequest("GET", "http://peer.local/api/steward/sync/changes?since_sequence=1", nil)

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", Secret: "shared-secret"}, now)

	if err := verifySyncRequestSignature("shared-secret", now.Add(6*time.Minute), req, body); err == nil {
		t.Fatalf("expected expired timestamp to fail")
	}
}

func TestSyncDeviceKeySignatureVerifies(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"changes":[]}`)
	req := httptest.NewRequest("POST", "http://peer.local/api/steward/sync/changes/import", strings.NewReader(string(body)))

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", DevicePrivateKey: privateKey}, now)

	publicKeyText, err := normalizeSyncPublicKey("ed25519:" + encodeTestKey(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDeviceKeyRequestSignature(publicKeyText, now.Add(10*time.Second), req, body); err != nil {
		t.Fatalf("expected device key signature to verify: %v", err)
	}
}

func TestSyncDeviceKeySignatureRejectsWrongKey(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"changes":[]}`)
	req := httptest.NewRequest("POST", "http://peer.local/api/steward/sync/changes/import", strings.NewReader(string(body)))

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", DevicePrivateKey: privateKey}, now)

	if err := verifyDeviceKeyRequestSignature(encodeTestKey(wrongPublicKey), now, req, body); err == nil {
		t.Fatalf("expected wrong public key to fail")
	}
}

func TestSyncRequestSigningMaterialRejectsBodyMismatch(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"changes":[]}`)
	req := httptest.NewRequest("POST", "http://peer.local/api/steward/sync/changes/import", strings.NewReader(string(body)))

	signSyncRequest(req, body, syncAuth{DeviceID: "windows-main", DevicePrivateKey: privateKey}, now)

	if _, err := syncRequestSigningMaterial(now, req, []byte(`{"changes":[{}]}`)); err == nil {
		t.Fatalf("expected signing material to reject body mismatch")
	}
}

func encodeTestKey(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}
