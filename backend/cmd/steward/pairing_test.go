package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/nacl/box"
)

func TestBuildPairingBundleDerivesPublicKeyAndRequiresExplicitSecrets(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                       "windows-main",
		DeviceName:               "Windows Main",
		Platform:                 "windows",
		APIBaseURL:               "http://192.168.1.10:18080/api/",
		PrivateKey:               base64.StdEncoding.EncodeToString(privateKey),
		PermissionLevel:          "A3",
		SyncSecret:               "shared-secret",
		SyncEncryptionKey:        syncKey,
		SyncEncryptionKeyID:      "home-sync-v1",
		IncludeSyncEncryptionKey: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	if bundle.Schema != pairingSchema || bundle.Version != 1 {
		t.Fatalf("unexpected schema/version: %#v", bundle)
	}
	if bundle.Device.PublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		t.Fatalf("public key was not derived from private key")
	}
	if bundle.Device.APIBaseURL != "http://192.168.1.10:18080/api" {
		t.Fatalf("api base url was not normalized: %q", bundle.Device.APIBaseURL)
	}
	if bundle.SharedSync == nil || bundle.SharedSync.SyncSecret != "" {
		t.Fatalf("sync secret should not be included without explicit include flag: %#v", bundle.SharedSync)
	}
	if bundle.SharedSync.SyncEncryptionKey != syncKey || bundle.SuggestedEnv["STEWARD_SYNC_ENCRYPTION_KEY"] != syncKey {
		t.Fatalf("sync encryption key was not included explicitly: %#v %#v", bundle.SharedSync, bundle.SuggestedEnv)
	}
	if bundle.Signature == nil || bundle.Signature.Algorithm != pairingBundleSignatureAlgorithm {
		t.Fatalf("expected bundle to be signed when private key is available: %#v", bundle.Signature)
	}
}

func TestBuildPairingBundleIncludesPreviousSyncKeysExplicitly(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previousKeys := "home-sync-v0:" + base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                         "windows-main",
		DeviceName:                 "Windows Main",
		Platform:                   "windows",
		APIBaseURL:                 "http://192.168.1.10:18080/api",
		PublicKey:                  base64.StdEncoding.EncodeToString(publicKey),
		IncludeSyncPreviousKeys:    true,
		SyncEncryptionPreviousKeys: previousKeys,
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if bundle.SharedSync == nil || bundle.SharedSync.SyncEncryptionPreviousKeys != previousKeys {
		t.Fatalf("previous sync keys were not included explicitly: %#v", bundle.SharedSync)
	}
	if bundle.SuggestedEnv["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != previousKeys {
		t.Fatalf("previous sync keys missing from suggested env: %#v", bundle.SuggestedEnv)
	}
}

func TestPairingBundleSignatureVerifiesAndRejectsTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                       "windows-main",
		DeviceName:               "Windows Main",
		Platform:                 "windows",
		APIBaseURL:               "http://192.168.1.10:18080/api",
		PublicKey:                base64.StdEncoding.EncodeToString(publicKey),
		PrivateKey:               base64.StdEncoding.EncodeToString(privateKey),
		IncludeSyncEncryptionKey: true,
		SyncEncryptionKey:        syncKey,
		SyncEncryptionKeyID:      "home-sync-v1",
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Signature == nil {
		t.Fatalf("expected signed bundle")
	}

	payload, env, err := pairingImportPayload(bundle, true, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if payload["id"] != "windows-main" || env["STEWARD_SYNC_ENCRYPTION_KEY"] != syncKey {
		t.Fatalf("unexpected signed import result: payload=%#v env=%#v", payload, env)
	}

	tampered := bundle
	tampered.Device.APIBaseURL = "http://attacker.invalid/api"
	_, _, err = pairingImportPayload(tampered, true, "", "", true)
	if err == nil || !strings.Contains(err.Error(), "invalid pairing bundle signature") {
		t.Fatalf("error = %v, want invalid signature", err)
	}
}

func TestPairingBundleSignatureRequirementRejectsUnsignedBundle(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:         "windows-main",
		APIBaseURL: "http://192.168.1.10:18080/api",
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Signature != nil {
		t.Fatalf("expected unsigned bundle when no private key was supplied")
	}

	_, _, err = pairingImportPayload(bundle, true, "", "", true)
	if err == nil || !strings.Contains(err.Error(), "signature is required") {
		t.Fatalf("error = %v, want signature requirement", err)
	}
}

func TestBuildPairingBundleRejectsMismatchedSigningKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPairingBundle(pairingExportOptions{
		ID:         "windows-main",
		APIBaseURL: "http://192.168.1.10:18080/api",
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		PrivateKey: base64.StdEncoding.EncodeToString(otherPrivateKey),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v, want public/private mismatch", err)
	}
}

func TestBuildPairingBundleRejectsInvalidPreviousSyncKeys(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPairingBundle(pairingExportOptions{
		ID:                         "windows-main",
		DeviceName:                 "Windows Main",
		Platform:                   "windows",
		APIBaseURL:                 "http://192.168.1.10:18080/api",
		PublicKey:                  base64.StdEncoding.EncodeToString(publicKey),
		IncludeSyncPreviousKeys:    true,
		SyncEncryptionPreviousKeys: "missing-key-material",
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatalf("expected invalid previous sync keys to fail")
	}
}

func TestPairingImportPayloadBuildsDeviceRegistrationAndSuggestedEnv(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	previousKeys := "home-sync-v0:" + base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	payload, env, err := pairingImportPayload(pairingBundle{
		Schema:  pairingSchema,
		Version: 1,
		Device: pairingDevice{
			ID:              "macbook-main",
			DeviceName:      "MacBook Main",
			Platform:        "darwin",
			APIBaseURL:      "http://192.168.1.12:18080/api/",
			PublicKey:       base64.StdEncoding.EncodeToString(publicKey),
			PermissionLevel: "A3",
		},
		SharedSync: &pairingSharedSync{
			SyncSecret:                 "shared-secret",
			SyncEncryptionKey:          syncKey,
			SyncEncryptionKeyID:        "home-sync-v1",
			SyncEncryptionPreviousKeys: previousKeys,
		},
	}, true, "A2", "", false)
	if err != nil {
		t.Fatal(err)
	}

	if payload["id"] != "macbook-main" || payload["permission_level"] != "A2" || payload["api_base_url"] != "http://192.168.1.12:18080/api" {
		t.Fatalf("unexpected registration payload: %#v", payload)
	}
	if env["STEWARD_SYNC_SECRET"] != "shared-secret" || env["STEWARD_SYNC_ENCRYPTION_KEY"] != syncKey || env["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "home-sync-v1" {
		t.Fatalf("unexpected suggested env: %#v", env)
	}
	if env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != previousKeys {
		t.Fatalf("previous keys missing from suggested env: %#v", env)
	}
}

func TestBuildPairingBundleEncryptsSharedSyncMaterial(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientPublicKey, recipientPrivateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                       "windows-main",
		DeviceName:               "Windows Main",
		Platform:                 "windows",
		APIBaseURL:               "http://192.168.1.10:18080/api",
		PublicKey:                base64.StdEncoding.EncodeToString(publicKey),
		IncludeSyncSecret:        true,
		SyncSecret:               "shared-secret-material",
		IncludeSyncEncryptionKey: true,
		SyncEncryptionKey:        syncKey,
		SyncEncryptionKeyID:      "home-sync-v1",
		EncryptSharedSyncFor:     base64.StdEncoding.EncodeToString(recipientPublicKey[:]),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	if bundle.SharedSync != nil || len(bundle.SuggestedEnv) != 0 {
		t.Fatalf("encrypted bundle should not expose plaintext sync env: %#v %#v", bundle.SharedSync, bundle.SuggestedEnv)
	}
	if bundle.SharedSyncEncrypted == nil || bundle.SharedSyncEncrypted.Algorithm != pairingSharedSyncEncryptionAlgorithm {
		t.Fatalf("missing encrypted shared sync envelope: %#v", bundle.SharedSyncEncrypted)
	}
	encoded, _ := jsonMarshalString(bundle)
	if strings.Contains(encoded, syncKey) || strings.Contains(encoded, "shared-secret-material") {
		t.Fatalf("encrypted bundle leaked plaintext shared sync material: %s", encoded)
	}

	_, env, err := pairingImportPayload(bundle, true, "", base64.StdEncoding.EncodeToString(recipientPrivateKey[:]), false)
	if err != nil {
		t.Fatal(err)
	}
	if env["STEWARD_SYNC_SECRET"] != "shared-secret-material" || env["STEWARD_SYNC_ENCRYPTION_KEY"] != syncKey || env["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "home-sync-v1" {
		t.Fatalf("unexpected decrypted suggested env: %#v", env)
	}
}

func TestPairingImportEncryptedSharedSyncRequiresRecipientPrivateKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientPublicKey, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                       "windows-main",
		APIBaseURL:               "http://192.168.1.10:18080/api",
		PublicKey:                base64.StdEncoding.EncodeToString(publicKey),
		IncludeSyncEncryptionKey: true,
		SyncEncryptionKey:        syncKey,
		SyncEncryptionKeyID:      "home-sync-v1",
		EncryptSharedSyncFor:     base64.StdEncoding.EncodeToString(recipientPublicKey[:]),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = pairingImportPayload(bundle, true, "", "", false)
	if err == nil || !strings.Contains(err.Error(), "decrypt-shared-sync-key") {
		t.Fatalf("error = %v, want decrypt key requirement", err)
	}

	_, wrongPrivateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = pairingImportPayload(bundle, true, "", base64.StdEncoding.EncodeToString(wrongPrivateKey[:]), false)
	if err == nil || !strings.Contains(err.Error(), "recipient key mismatch") {
		t.Fatalf("error = %v, want recipient key mismatch", err)
	}
}

func TestBuildPairingBundleRejectsEncryptWithoutIncludedSharedSync(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientPublicKey, _, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPairingBundle(pairingExportOptions{
		ID:                   "windows-main",
		APIBaseURL:           "http://192.168.1.10:18080/api",
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		EncryptSharedSyncFor: base64.StdEncoding.EncodeToString(recipientPublicKey[:]),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "requires at least one included shared sync secret flag") {
		t.Fatalf("error = %v, want include flag requirement", err)
	}
}

func TestBuildPairingBootstrapPlanDecryptsAndRedactsServiceEnvPlan(t *testing.T) {
	devicePublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientPublicKey, recipientPrivateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncSecret := "shared-secret-material-for-bootstrap"
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	previousKeys := "home-sync-v0:" + base64.StdEncoding.EncodeToString([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                         "windows-main",
		DeviceName:                 "Windows Main",
		Platform:                   "windows",
		APIBaseURL:                 "http://192.168.1.10:18081/api",
		PublicKey:                  base64.StdEncoding.EncodeToString(devicePublicKey),
		IncludeSyncSecret:          true,
		SyncSecret:                 syncSecret,
		IncludeSyncEncryptionKey:   true,
		SyncEncryptionKey:          syncKey,
		SyncEncryptionKeyID:        "home-sync-v1",
		IncludeSyncPreviousKeys:    true,
		SyncEncryptionPreviousKeys: previousKeys,
		EncryptSharedSyncFor:       base64.StdEncoding.EncodeToString(recipientPublicKey[:]),
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	output, err := buildPairingBootstrapPlan(bundle, pairingBootstrapOptions{
		File:                 "peer-pairing.encrypted.json",
		ServiceName:          "MongojsonSteward",
		DecryptSharedSyncKey: base64.StdEncoding.EncodeToString(recipientPrivateKey[:]),
		SyncEnabled:          true,
		CurrentEnvFile:       "current-service-env.json",
	}, map[string]string{
		"HTTP_ADDR":               "127.0.0.1:18080",
		"STEWARD_PEER_HTTP_ADDR":  "127.0.0.1:18081",
		"STEWARD_AGENT_ID":        "macbook-main",
		"STEWARD_PUBLIC_API_BASE": "http://127.0.0.1:18081/api",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !output.OK || output.Device["id"] != "windows-main" {
		t.Fatalf("unexpected bootstrap output: %#v", output)
	}
	if output.SuggestedEnv["STEWARD_SYNC_SECRET"] != "<redacted>" ||
		output.SuggestedEnv["STEWARD_SYNC_ENCRYPTION_KEY"] != "<redacted>" ||
		output.SuggestedEnv["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] != "<redacted>" ||
		output.SuggestedEnv["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "home-sync-v1" {
		t.Fatalf("suggested env was not redacted correctly: %#v", output.SuggestedEnv)
	}
	if output.ServiceEnvPlan == nil || output.Verification == nil {
		t.Fatalf("expected service env plan and verification advice: %#v", output)
	}
	if !stringSliceContains(output.Commands.ServicePlan, "--current-env-file") ||
		!stringSliceContains(output.Commands.ServiceApply, "--verify") ||
		!stringSliceContains(output.Commands.ServiceApply, "<recipient pairing private_key>") {
		t.Fatalf("unexpected command advice: %#v", output.Commands)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), syncSecret) ||
		strings.Contains(string(encoded), syncKey) ||
		strings.Contains(string(encoded), previousKeys) ||
		strings.Contains(string(encoded), base64.StdEncoding.EncodeToString(recipientPrivateKey[:])) {
		t.Fatalf("bootstrap output leaked sensitive material: %s", string(encoded))
	}
}

func TestBuildPairingBootstrapPlanWithoutCurrentEnvDoesNotPlanServicePatch(t *testing.T) {
	devicePublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	syncKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	bundle, err := buildPairingBundle(pairingExportOptions{
		ID:                       "windows-main",
		APIBaseURL:               "http://192.168.1.10:18081/api",
		PublicKey:                base64.StdEncoding.EncodeToString(devicePublicKey),
		IncludeSyncEncryptionKey: true,
		SyncEncryptionKey:        syncKey,
		SyncEncryptionKeyID:      "home-sync-v1",
	}, time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	output, err := buildPairingBootstrapPlan(bundle, pairingBootstrapOptions{
		File:        "peer-pairing.json",
		ServiceName: "MongojsonSteward",
		SyncEnabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if output.ServiceEnvPlan != nil || output.Verification != nil {
		t.Fatalf("did not expect service env plan without current env: %#v", output)
	}
	if !stringSliceContains(output.Commands.ServicePlan, "--from-pairing") ||
		!stringSliceContains(output.Commands.ServiceApply, "--confirm") {
		t.Fatalf("expected next-step service commands: %#v", output.Commands)
	}
}

func stringSliceContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func jsonMarshalString(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}
