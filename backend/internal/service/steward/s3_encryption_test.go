package steward

import (
	"encoding/base64"
	"testing"

	"mongojson/backend/internal/domain"
)

func testSyncPayloadCipher(keyID string, key []byte) syncPayloadCipher {
	item := syncPayloadKey{key: key, keyID: keyID}
	return syncPayloadCipher{
		current: &item,
		keys:    []syncPayloadKey{item},
	}
}

func TestSyncPayloadEncryptionRoundTrip(t *testing.T) {
	config := testSyncPayloadCipher("sync-key-v1", []byte("0123456789abcdef0123456789abcdef"))
	payload := map[string]any{"title": "private task", "count": float64(2)}

	encrypted, err := encryptSyncPayload(config, "aad", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !isEncryptedSyncPayload(encrypted) {
		t.Fatalf("expected encrypted envelope")
	}
	if _, ok := encrypted["title"]; ok {
		t.Fatalf("encrypted envelope leaked plaintext title")
	}

	decrypted, err := decryptSyncPayload(config, "aad", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted["title"] != "private task" || decrypted["count"] != float64(2) {
		t.Fatalf("unexpected decrypted payload: %#v", decrypted)
	}
}

func TestSyncPayloadEncryptionRejectsAADMismatch(t *testing.T) {
	config := testSyncPayloadCipher("sync-key-v1", []byte("0123456789abcdef0123456789abcdef"))
	encrypted, err := encryptSyncPayload(config, "aad", map[string]any{"title": "private task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decryptSyncPayload(config, "other-aad", encrypted); err == nil {
		t.Fatalf("expected aad mismatch to fail")
	}
}

func TestSyncPayloadEncryptionDecryptsPreviousKey(t *testing.T) {
	oldKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	newKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	oldConfig := testSyncPayloadCipher("sync-key-v0", oldKey)
	rotatedConfig := syncPayloadCipher{
		current: &syncPayloadKey{key: newKey, keyID: "sync-key-v1"},
		keys: []syncPayloadKey{
			{key: newKey, keyID: "sync-key-v1"},
			{key: oldKey, keyID: "sync-key-v0"},
		},
	}

	encrypted, err := encryptSyncPayload(oldConfig, "aad", map[string]any{"title": "old private task"})
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := decryptSyncPayload(rotatedConfig, "aad", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted["title"] != "old private task" {
		t.Fatalf("unexpected decrypted payload: %#v", decrypted)
	}
}

func TestSyncPayloadEncryptionDecryptsKeysSharingID(t *testing.T) {
	const keyID = "sync-key-stable"
	oldKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	newKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	oldEncrypted, err := encryptSyncPayload(testSyncPayloadCipher(keyID, oldKey), "aad", map[string]any{"title": "old private task"})
	if err != nil {
		t.Fatal(err)
	}
	currentEncrypted, err := encryptSyncPayload(testSyncPayloadCipher(keyID, newKey), "aad", map[string]any{"title": "current private task"})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(newKey))
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY_ID", keyID)
	t.Setenv("STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", keyID+":"+base64.StdEncoding.EncodeToString(oldKey))
	rotatedConfig, err := syncPayloadKeyringFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		envelope  map[string]any
		wantTitle string
	}{
		{name: "previous key", envelope: oldEncrypted, wantTitle: "old private task"},
		{name: "current key", envelope: currentEncrypted, wantTitle: "current private task"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decrypted, err := decryptSyncPayload(rotatedConfig, "aad", tt.envelope)
			if err != nil {
				t.Fatal(err)
			}
			if decrypted["title"] != tt.wantTitle {
				t.Fatalf("unexpected decrypted payload: %#v", decrypted)
			}
		})
	}
}

func TestPrepareImportSyncChangesDecryptsTransportPayload(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY", key)
	t.Setenv("STEWARD_SYNC_ENCRYPTION_KEY_ID", "sync-key-v1")

	input := ImportSyncChangesInput{
		Changes: []CreateSyncChangeInput{
			{
				ID:             "11111111-1111-1111-1111-111111111111",
				EntityType:     EntityMemory,
				EntityID:       "22222222-2222-2222-2222-222222222222",
				Operation:      SyncCreate,
				OriginDeviceID: "windows-main",
				Version:        1,
				DataLevel:      DataD1,
				Payload:        map[string]any{"title": "private memory"},
			},
		},
	}
	encrypted, err := prepareImportSyncChangesForTransport(input, syncAuth{SyncEncryptionRequested: true})
	if err != nil {
		t.Fatal(err)
	}
	if !isEncryptedSyncPayload(encrypted.Changes[0].Payload) {
		t.Fatalf("expected transport payload to be encrypted")
	}

	decrypted, err := (&Service{}).PrepareImportSyncChanges(nil, encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Changes[0].Payload["title"] != "private memory" {
		t.Fatalf("unexpected decrypted payload: %#v", decrypted.Changes[0].Payload)
	}
}

func TestPrepareSyncPayloadForStorageEncryptsLocalPayload(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", key)
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local-key-v1")

	input := CreateSyncChangeInput{
		ID:             "11111111-1111-1111-1111-111111111111",
		EntityType:     EntityMemory,
		EntityID:       "22222222-2222-2222-2222-222222222222",
		Operation:      SyncCreate,
		OriginDeviceID: "windows-main",
		Version:        1,
		DataLevel:      DataD1,
		Payload:        map[string]any{"title": "private memory"},
	}

	stored, err := prepareSyncPayloadForStorage(input)
	if err != nil {
		t.Fatal(err)
	}
	if !isLocalEncryptedSyncPayload(stored) {
		t.Fatalf("expected local encrypted envelope")
	}
	if isEncryptedSyncPayload(stored) {
		t.Fatalf("local encrypted envelope must not be treated as transport encryption")
	}
	if _, ok := stored["title"]; ok {
		t.Fatalf("local encrypted envelope leaked plaintext title")
	}

	decrypted, err := decryptStoredSyncPayload(domain.StewardSyncChange{
		ID:             input.ID,
		EntityType:     input.EntityType,
		EntityID:       input.EntityID,
		Operation:      input.Operation,
		OriginDeviceID: input.OriginDeviceID,
		Version:        input.Version,
		DataLevel:      input.DataLevel,
		Payload:        stored,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decrypted["title"] != "private memory" {
		t.Fatalf("unexpected decrypted local payload: %#v", decrypted)
	}
}

func TestDecryptStoredSyncPayloadAcceptsPreviousLocalKey(t *testing.T) {
	oldKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	newKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	oldConfig := testSyncPayloadCipher("local-key-v0", oldKey)
	change := domain.StewardSyncChange{
		ID:             "11111111-1111-1111-1111-111111111111",
		EntityType:     EntityMemory,
		EntityID:       "22222222-2222-2222-2222-222222222222",
		Operation:      SyncCreate,
		OriginDeviceID: "windows-main",
		Version:        1,
		DataLevel:      DataD1,
	}
	encrypted, err := encryptPayloadEnvelope(oldConfig, syncChangeEncryptionAAD(change), map[string]any{"title": "old local memory"}, SyncEncryptionScopeLocalAtRest)
	if err != nil {
		t.Fatal(err)
	}
	change.Payload = encrypted

	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(newKey))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local-key-v1")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", "local-key-v0:"+base64.StdEncoding.EncodeToString(oldKey))

	decrypted, err := decryptStoredSyncPayload(change)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted["title"] != "old local memory" {
		t.Fatalf("unexpected decrypted local payload: %#v", decrypted)
	}
}

func TestDecryptStoredSyncPayloadAcceptsPreviousLocalKeySharingID(t *testing.T) {
	const keyID = "windows-local-v1"
	oldKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	newKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	change := domain.StewardSyncChange{
		ID:             "11111111-1111-1111-1111-111111111111",
		EntityType:     EntityMemory,
		EntityID:       "22222222-2222-2222-2222-222222222222",
		Operation:      SyncCreate,
		OriginDeviceID: "windows-main",
		Version:        1,
		DataLevel:      DataD1,
	}
	encrypted, err := encryptPayloadEnvelope(
		testSyncPayloadCipher(keyID, oldKey),
		syncChangeEncryptionAAD(change),
		map[string]any{"title": "old local memory"},
		SyncEncryptionScopeLocalAtRest,
	)
	if err != nil {
		t.Fatal(err)
	}
	change.Payload = encrypted

	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(newKey))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", keyID)
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", keyID+":"+base64.StdEncoding.EncodeToString(oldKey))

	decrypted, err := decryptStoredSyncPayload(change)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted["title"] != "old local memory" {
		t.Fatalf("unexpected decrypted local payload: %#v", decrypted)
	}
}
