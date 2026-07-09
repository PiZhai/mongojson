package steward

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSyncSecurityStatusFromEnvEmpty(t *testing.T) {
	clearSyncSecurityEnv(t)

	status := syncSecurityStatusFromEnv()
	if !status.AuthRequired || status.InsecureModeActive {
		t.Fatalf("empty config must fail closed: %+v", status)
	}
	if status.HMACSecretConfigured || status.DeviceSigningReady || status.SyncEncryptionConfigured || status.LocalEncryptionConfigured {
		t.Fatalf("unexpected configured security status: %+v", status)
	}
	if len(status.ConfigErrors) != 0 {
		t.Fatalf("config errors = %v, want none", status.ConfigErrors)
	}
}

func TestSyncSecurityStatusFromEnvAllowsExplicitInsecureDevelopmentMode(t *testing.T) {
	clearSyncSecurityEnv(t)
	t.Setenv("STEWARD_SYNC_ALLOW_INSECURE", "true")

	status := syncSecurityStatusFromEnv()
	if status.AuthRequired || !status.InsecureModeActive {
		t.Fatalf("explicit insecure development mode not reflected: %+v", status)
	}
}

func TestVerifySyncRequestFailsClosedByDefault(t *testing.T) {
	clearSyncSecurityEnv(t)
	request := httptest.NewRequest(http.MethodGet, "http://peer.test/api/steward/sync/changes", nil)

	if err := (&Service{}).VerifySyncRequest(request, nil); err == nil || !strings.Contains(err.Error(), "missing sync authentication") {
		t.Fatalf("unauthenticated sync request was not rejected: %v", err)
	}
}

func TestVerifySyncRequestAllowsExplicitInsecureDevelopmentMode(t *testing.T) {
	clearSyncSecurityEnv(t)
	t.Setenv("STEWARD_SYNC_ALLOW_INSECURE", "true")
	request := httptest.NewRequest(http.MethodGet, "http://peer.test/api/steward/sync/changes", nil)

	if err := (&Service{}).VerifySyncRequest(request, nil); err != nil {
		t.Fatalf("explicit insecure development request rejected: %v", err)
	}
}

func TestVerifySyncRequestRejectsValidHMACWithoutRegisteredDevice(t *testing.T) {
	clearSyncSecurityEnv(t)
	secret := "shared-secret-long-enough-for-test"
	t.Setenv("STEWARD_SYNC_SECRET", secret)
	request := httptest.NewRequest(http.MethodGet, "http://peer.test/api/steward/sync/changes?since_sequence=4", nil)
	signSyncRequest(request, nil, syncAuth{DeviceID: "peer-1", Secret: secret}, time.Now().UTC())

	if err := (&Service{}).VerifySyncRequest(request, nil); err == nil || !strings.Contains(err.Error(), "device authorization") {
		t.Fatalf("unregistered HMAC device was not rejected: %v", err)
	}
}

func TestSyncSecurityStatusFromEnvConfigured(t *testing.T) {
	clearSyncSecurityEnv(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString(bytesOf(32, 1))
	localKey := base64.StdEncoding.EncodeToString(bytesOf(32, 2))
	t.Setenv("STEWARD_SYNC_SECRET", "shared-secret")
	t.Setenv("STEWARD_DEVICE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv("STEWARD_DEVICE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY", syncKey)
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY_ID", "sync-v1")
	t.Setenv("STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", "sync-v0:"+base64.StdEncoding.EncodeToString(bytesOf(32, 3)))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", localKey)
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local-v1")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", "local-v0:"+base64.StdEncoding.EncodeToString(bytesOf(32, 4)))

	status := syncSecurityStatusFromEnv()
	if !status.AuthRequired || !status.HMACSecretConfigured {
		t.Fatalf("auth status not configured: %+v", status)
	}
	if !status.DevicePrivateKeyValid || !status.DevicePublicKeyValid || !status.DeviceSigningReady || !status.DeviceIdentityAdvertisable {
		t.Fatalf("device signing status not ready: %+v", status)
	}
	if !status.SyncEncryptionConfigured || status.SyncEncryptionKeyID != "sync-v1" || status.SyncPreviousKeyCount != 1 {
		t.Fatalf("sync encryption status mismatch: %+v", status)
	}
	if !status.LocalEncryptionConfigured || status.LocalEncryptionKeyID != "local-v1" || status.LocalPreviousKeyCount != 1 {
		t.Fatalf("local encryption status mismatch: %+v", status)
	}
	if len(status.ConfigErrors) != 0 {
		t.Fatalf("config errors = %v, want none", status.ConfigErrors)
	}
}

func TestSyncSecurityStatusFromEnvReportsInvalidConfig(t *testing.T) {
	clearSyncSecurityEnv(t)
	t.Setenv("STEWARD_SYNC_REQUIRE_AUTH", "true")
	t.Setenv("STEWARD_DEVICE_PRIVATE_KEY", "not-base64")
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytesOf(16, 1)))

	status := syncSecurityStatusFromEnv()
	if !status.AuthRequired {
		t.Fatalf("auth required = false, want true")
	}
	if status.DevicePrivateKeyValid || status.SyncEncryptionConfigured {
		t.Fatalf("invalid config should not be marked valid: %+v", status)
	}
	joined := strings.Join(status.ConfigErrors, "\n")
	if !strings.Contains(joined, "STEWARD_DEVICE_PRIVATE_KEY") || !strings.Contains(joined, "STEWARD_SYNC_ENCRYPTION_KEY") {
		t.Fatalf("config errors = %v, want private key and sync key errors", status.ConfigErrors)
	}
}

func clearSyncSecurityEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"STEWARD_SYNC_SECRET",
		"STEWARD_SYNC_REQUIRE_AUTH",
		"STEWARD_SYNC_ALLOW_INSECURE",
		"STEWARD_DEVICE_PRIVATE_KEY",
		"STEWARD_DEVICE_PUBLIC_KEY",
		"STEWARD_SYNC_ENCRYPTION_KEY",
		"STEWARD_SYNC_ENCRYPTION_KEY_ID",
		"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
		"STEWARD_LOCAL_ENCRYPTION_KEY",
		"STEWARD_LOCAL_ENCRYPTION_KEY_ID",
		"STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
	} {
		t.Setenv(key, "")
	}
}

func bytesOf(size int, value byte) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = value
	}
	return out
}
