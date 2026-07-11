package steward

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"mongojson/backend/internal/domain"
)

const (
	SyncEncryptionAlgorithmAESGCM  = "aes-256-gcm"
	SyncEncryptionQueryParam       = "encrypted"
	SyncEncryptionScopeLocalAtRest = "local-at-rest"
)

func (s *Service) PrepareOutboundSyncChanges(r *http.Request, changes []domain.StewardSyncChange) ([]domain.StewardSyncChange, error) {
	filtered, err := s.filterOutboundSyncChanges(r.Context(), r.Header.Get(SyncHeaderDeviceID), changes)
	if err != nil {
		return nil, err
	}
	changes = filtered
	if !syncRequestWantsEncryption(r) {
		return changes, nil
	}
	cipherConfig, err := syncPayloadCipherFromEnv()
	if err != nil {
		return nil, err
	}
	out := make([]domain.StewardSyncChange, len(changes))
	copy(out, changes)
	for i := range out {
		payload, err := encryptSyncPayload(cipherConfig, syncChangeEncryptionAAD(out[i]), out[i].Payload)
		if err != nil {
			return nil, err
		}
		out[i].Payload = payload
	}
	return out, nil
}

func (s *Service) PrepareImportSyncChanges(_ context.Context, input ImportSyncChangesInput) (ImportSyncChangesInput, error) {
	hasEncrypted := false
	for _, change := range input.Changes {
		if isEncryptedSyncPayload(change.Payload) {
			hasEncrypted = true
			break
		}
	}
	if !hasEncrypted {
		return input, nil
	}
	cipherConfig, err := syncPayloadKeyringFromEnv()
	if err != nil {
		return ImportSyncChangesInput{}, err
	}
	for i := range input.Changes {
		if !isEncryptedSyncPayload(input.Changes[i].Payload) {
			continue
		}
		payload, err := decryptSyncPayload(cipherConfig, syncChangeInputEncryptionAAD(input.Changes[i]), input.Changes[i].Payload)
		if err != nil {
			return ImportSyncChangesInput{}, err
		}
		input.Changes[i].Payload = payload
	}
	return input, nil
}

func prepareImportSyncChangesForTransport(input ImportSyncChangesInput, auth syncAuth) (ImportSyncChangesInput, error) {
	if !auth.encryptionEnabled() {
		return input, nil
	}
	cipherConfig, err := syncPayloadCipherFromEnv()
	if err != nil {
		return ImportSyncChangesInput{}, err
	}
	for i := range input.Changes {
		payload, err := encryptSyncPayload(cipherConfig, syncChangeInputEncryptionAAD(input.Changes[i]), input.Changes[i].Payload)
		if err != nil {
			return ImportSyncChangesInput{}, err
		}
		input.Changes[i].Payload = payload
	}
	return input, nil
}

func prepareSyncPayloadForStorage(input CreateSyncChangeInput) (map[string]any, error) {
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	cipherConfig, enabled, err := localPayloadCipherFromEnv()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return payload, nil
	}
	return encryptPayloadEnvelope(cipherConfig, syncChangeInputEncryptionAAD(input), payload, SyncEncryptionScopeLocalAtRest)
}

func decryptStoredSyncPayload(change domain.StewardSyncChange) (map[string]any, error) {
	if !isLocalEncryptedSyncPayload(change.Payload) {
		return change.Payload, nil
	}
	cipherConfig, err := localPayloadKeyringFromEnv()
	if err != nil {
		return nil, err
	}
	return decryptPayloadEnvelope(cipherConfig, syncChangeEncryptionAAD(change), change.Payload, "local stored sync payload")
}

func syncAuthEncryptionKeyFromEnv() ([]byte, string, error) {
	return payloadEncryptionKeyFromEnv("STEWARD_SYNC_ENCRYPTION_KEY", "STEWARD_SYNC_ENCRYPTION_KEY_ID", "sync encryption key")
}

func payloadEncryptionKeyFromEnv(keyEnv string, keyIDEnv string, label string) ([]byte, string, error) {
	value := strings.TrimSpace(envOrDefault(keyEnv, ""))
	if value == "" {
		return nil, "", nil
	}
	key, err := decodeSyncKeyMaterial(value, label)
	if err != nil {
		return nil, "", err
	}
	if len(key) != 32 {
		return nil, "", fmt.Errorf("%s must be 32 bytes, got %d", label, len(key))
	}
	keyID := defaultString(envOrDefault(keyIDEnv, ""), "default")
	return key, keyID, nil
}

func syncAuthPreviousEncryptionKeysFromEnv() ([]syncPayloadKey, error) {
	return previousPayloadEncryptionKeysFromEnv("STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", "previous sync encryption key")
}

func previousPayloadEncryptionKeysFromEnv(envName string, label string) ([]syncPayloadKey, error) {
	value := strings.TrimSpace(envOrDefault(envName, ""))
	if value == "" {
		return nil, nil
	}
	keys := []syncPayloadKey{}
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyID, keyValue, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("%s must use key_id:base64 format", label)
		}
		keyID = strings.TrimSpace(keyID)
		if keyID == "" {
			return nil, fmt.Errorf("%s id is required", label)
		}
		key, err := decodeSyncKeyMaterial(strings.TrimSpace(keyValue), label)
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s %q must be 32 bytes, got %d", label, keyID, len(key))
		}
		keys = append(keys, syncPayloadKey{key: key, keyID: keyID})
	}
	return keys, nil
}

type syncPayloadKey struct {
	key   []byte
	keyID string
}

type syncPayloadCipher struct {
	current *syncPayloadKey
	keys    []syncPayloadKey
}

func syncPayloadCipherFromEnv() (syncPayloadCipher, error) {
	keyring, err := syncPayloadKeyringFromEnv()
	if err != nil {
		return syncPayloadCipher{}, err
	}
	if keyring.current == nil {
		return syncPayloadCipher{}, fmt.Errorf("STEWARD_SYNC_ENCRYPTION_KEY is required for encrypted sync payloads")
	}
	return keyring, nil
}

func syncPayloadKeyringFromEnv() (syncPayloadCipher, error) {
	key, keyID, err := syncAuthEncryptionKeyFromEnv()
	if err != nil {
		return syncPayloadCipher{}, err
	}
	previous, err := syncAuthPreviousEncryptionKeysFromEnv()
	if err != nil {
		return syncPayloadCipher{}, err
	}
	out := syncPayloadCipher{keys: []syncPayloadKey{}}
	if len(key) > 0 {
		current := syncPayloadKey{key: key, keyID: keyID}
		out.current = &current
		out.keys = append(out.keys, current)
	}
	out.keys = append(out.keys, previous...)
	if len(out.keys) == 0 {
		return syncPayloadCipher{}, fmt.Errorf("STEWARD_SYNC_ENCRYPTION_KEY is required for encrypted sync payloads")
	}
	return out, nil
}

func localPayloadCipherFromEnv() (syncPayloadCipher, bool, error) {
	key, keyID, err := payloadEncryptionKeyFromEnv("STEWARD_LOCAL_ENCRYPTION_KEY", "STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local encryption key")
	if err != nil {
		return syncPayloadCipher{}, false, err
	}
	if len(key) == 0 {
		return syncPayloadCipher{}, false, nil
	}
	item := syncPayloadKey{key: key, keyID: keyID}
	return syncPayloadCipher{
		current: &item,
		keys:    []syncPayloadKey{item},
	}, true, nil
}

func localPayloadKeyringFromEnv() (syncPayloadCipher, error) {
	key, keyID, err := payloadEncryptionKeyFromEnv("STEWARD_LOCAL_ENCRYPTION_KEY", "STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local encryption key")
	if err != nil {
		return syncPayloadCipher{}, err
	}
	previous, err := previousPayloadEncryptionKeysFromEnv("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", "previous local encryption key")
	if err != nil {
		return syncPayloadCipher{}, err
	}
	out := syncPayloadCipher{keys: []syncPayloadKey{}}
	if len(key) > 0 {
		current := syncPayloadKey{key: key, keyID: keyID}
		out.current = &current
		out.keys = append(out.keys, current)
	}
	out.keys = append(out.keys, previous...)
	if len(out.keys) == 0 {
		return syncPayloadCipher{}, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY is required for local encrypted payloads")
	}
	return out, nil
}

func encryptSyncPayload(config syncPayloadCipher, additionalData string, payload map[string]any) (map[string]any, error) {
	return encryptPayloadEnvelope(config, additionalData, payload, "")
}

func encryptPayloadEnvelope(config syncPayloadCipher, additionalData string, payload map[string]any, scope string) (map[string]any, error) {
	if config.current == nil {
		return nil, fmt.Errorf("current sync encryption key is required")
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal sync payload for encryption: %w", err)
	}
	block, err := aes.NewCipher(config.current.key)
	if err != nil {
		return nil, fmt.Errorf("create sync payload cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create sync payload gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate sync payload nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plain, []byte(additionalData))
	envelope := map[string]any{
		"_encrypted":   true,
		"algorithm":    SyncEncryptionAlgorithmAESGCM,
		"key_id":       config.current.keyID,
		"nonce":        base64.StdEncoding.EncodeToString(nonce),
		"ciphertext":   base64.StdEncoding.EncodeToString(ciphertext),
		"payload_hash": hashBytes(plain),
	}
	if strings.TrimSpace(scope) != "" {
		envelope["scope"] = strings.TrimSpace(scope)
	}
	return envelope, nil
}

func decryptSyncPayload(config syncPayloadCipher, additionalData string, envelope map[string]any) (map[string]any, error) {
	if !isEncryptedSyncPayload(envelope) {
		return envelope, nil
	}
	return decryptPayloadEnvelope(config, additionalData, envelope, "sync payload")
}

func decryptPayloadEnvelope(config syncPayloadCipher, additionalData string, envelope map[string]any, label string) (map[string]any, error) {
	if algorithm := stringPayload(envelope, "algorithm", ""); algorithm != SyncEncryptionAlgorithmAESGCM {
		return nil, fmt.Errorf("unsupported sync payload encryption algorithm %q", algorithm)
	}
	keyID := stringPayload(envelope, "key_id", "")
	nonce, err := decodeSyncKeyMaterial(stringPayload(envelope, "nonce", ""), "sync payload nonce")
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeSyncKeyMaterial(stringPayload(envelope, "ciphertext", ""), "sync payload ciphertext")
	if err != nil {
		return nil, err
	}
	key, err := config.keyForDecrypt(keyID)
	if err != nil {
		return nil, err
	}
	plain, err := openSyncPayloadWithKey(key, nonce, ciphertext, []byte(additionalData))
	if err != nil {
		label = defaultString(label, "sync payload")
		if keyID != "" {
			return nil, fmt.Errorf("decrypt %s with key %q: %w", label, keyID, err)
		}
		return nil, fmt.Errorf("decrypt %s: %w", label, err)
	}
	if expectedHash := stringPayload(envelope, "payload_hash", ""); expectedHash != "" && expectedHash != hashBytes(plain) {
		return nil, fmt.Errorf("sync payload hash mismatch after decrypt")
	}
	var payload map[string]any
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, fmt.Errorf("decode decrypted sync payload: %w", err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func (c syncPayloadCipher) keyForDecrypt(keyID string) ([]byte, error) {
	if len(c.keys) == 0 {
		return nil, fmt.Errorf("no sync encryption keys configured")
	}
	if strings.TrimSpace(keyID) == "" {
		return c.keys[0].key, nil
	}
	for _, item := range c.keys {
		if item.keyID == keyID {
			return item.key, nil
		}
	}
	return nil, fmt.Errorf("sync payload key id %q is not configured", keyID)
}

func openSyncPayloadWithKey(key []byte, nonce []byte, ciphertext []byte, additionalData []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create sync payload cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create sync payload gcm: %w", err)
	}
	return aead.Open(nil, nonce, ciphertext, additionalData)
}

func isEncryptedSyncPayload(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	value, ok := payload["_encrypted"].(bool)
	return ok && value && stringPayload(payload, "scope", "") != SyncEncryptionScopeLocalAtRest
}

func isLocalEncryptedSyncPayload(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	value, ok := payload["_encrypted"].(bool)
	return ok && value && stringPayload(payload, "scope", "") == SyncEncryptionScopeLocalAtRest
}

func syncRequestWantsEncryption(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(SyncEncryptionQueryParam)))
	return value == "1" || value == "true" || value == "yes"
}

func syncChangeEncryptionAAD(change domain.StewardSyncChange) string {
	return syncEncryptionAAD(change.ID, change.EntityType, change.EntityID, change.Operation, change.OriginDeviceID, change.Version, change.DataLevel)
}

func syncChangeInputEncryptionAAD(change CreateSyncChangeInput) string {
	return syncEncryptionAAD(change.ID, change.EntityType, change.EntityID, change.Operation, change.OriginDeviceID, change.Version, change.DataLevel)
}

func syncEncryptionAAD(id string, entityType string, entityID string, operation string, originDeviceID string, version int, dataLevel string) string {
	return strings.Join([]string{
		strings.TrimSpace(id),
		strings.TrimSpace(entityType),
		strings.TrimSpace(entityID),
		strings.TrimSpace(operation),
		strings.TrimSpace(originDeviceID),
		fmt.Sprintf("%d", version),
		strings.TrimSpace(dataLevel),
	}, "\n")
}
