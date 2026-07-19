package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

type serviceEnvRotationOptions struct {
	SyncKeyID  string
	LocalKeyID string
}

type serviceEnvKeyRotation struct {
	NewKeyID     string
	KeyName      string
	KeyIDName    string
	PreviousName string
	Label        string
}

type previousEncryptionKeyEntry struct {
	ID  string
	Key string
}

func (o serviceEnvRotationOptions) enabled() bool {
	return strings.TrimSpace(o.SyncKeyID) != "" || strings.TrimSpace(o.LocalKeyID) != ""
}

func (o serviceEnvRotationOptions) transform() func(current, target map[string]string) error {
	if !o.enabled() {
		return nil
	}
	return func(current, target map[string]string) error {
		if strings.TrimSpace(o.SyncKeyID) != "" {
			if err := applyServiceEnvKeyRotation(current, target, serviceEnvKeyRotation{
				NewKeyID:     o.SyncKeyID,
				KeyName:      "STEWARD_SYNC_ENCRYPTION_KEY",
				KeyIDName:    "STEWARD_SYNC_ENCRYPTION_KEY_ID",
				PreviousName: "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
				Label:        "sync encryption",
			}); err != nil {
				return err
			}
		}
		if strings.TrimSpace(o.LocalKeyID) != "" {
			if err := applyServiceEnvKeyRotation(current, target, serviceEnvKeyRotation{
				NewKeyID:     o.LocalKeyID,
				KeyName:      "STEWARD_LOCAL_ENCRYPTION_KEY",
				KeyIDName:    "STEWARD_LOCAL_ENCRYPTION_KEY_ID",
				PreviousName: "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
				Label:        "local encryption",
			}); err != nil {
				return err
			}
		}
		return nil
	}
}

func validateServiceEnvRotationConflicts(set map[string]string, remove []string, rotations serviceEnvRotationOptions) error {
	for _, rotation := range serviceEnvRotations(rotations) {
		for _, key := range []string{rotation.KeyName, rotation.KeyIDName, rotation.PreviousName} {
			if _, ok := set[key]; ok {
				return fmt.Errorf("--rotate-%s-key-id cannot be combined with --set %s", rotationFlagPrefix(rotation), key)
			}
			for _, removeKey := range remove {
				if strings.TrimSpace(removeKey) == key {
					return fmt.Errorf("--rotate-%s-key-id cannot be combined with --remove %s", rotationFlagPrefix(rotation), key)
				}
			}
		}
	}
	return nil
}

func serviceEnvRotations(options serviceEnvRotationOptions) []serviceEnvKeyRotation {
	rotations := []serviceEnvKeyRotation{}
	if strings.TrimSpace(options.SyncKeyID) != "" {
		rotations = append(rotations, serviceEnvKeyRotation{
			NewKeyID:     options.SyncKeyID,
			KeyName:      "STEWARD_SYNC_ENCRYPTION_KEY",
			KeyIDName:    "STEWARD_SYNC_ENCRYPTION_KEY_ID",
			PreviousName: "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
			Label:        "sync encryption",
		})
	}
	if strings.TrimSpace(options.LocalKeyID) != "" {
		rotations = append(rotations, serviceEnvKeyRotation{
			NewKeyID:     options.LocalKeyID,
			KeyName:      "STEWARD_LOCAL_ENCRYPTION_KEY",
			KeyIDName:    "STEWARD_LOCAL_ENCRYPTION_KEY_ID",
			PreviousName: "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
			Label:        "local encryption",
		})
	}
	return rotations
}

func rotationFlagPrefix(rotation serviceEnvKeyRotation) string {
	if rotation.KeyName == "STEWARD_LOCAL_ENCRYPTION_KEY" {
		return "local"
	}
	return "sync"
}

func applyServiceEnvKeyRotation(current map[string]string, target map[string]string, rotation serviceEnvKeyRotation) error {
	newKeyID := strings.TrimSpace(rotation.NewKeyID)
	if newKeyID == "" {
		return nil
	}
	currentKeyID := strings.TrimSpace(current[rotation.KeyIDName])
	currentKey := strings.TrimSpace(current[rotation.KeyName])
	if currentKeyID == "" || currentKey == "" {
		return fmt.Errorf("cannot rotate %s key: current %s and %s must both be present", rotation.Label, rotation.KeyIDName, rotation.KeyName)
	}
	if newKeyID == currentKeyID {
		return fmt.Errorf("cannot rotate %s key: new key id %q matches current key id", rotation.Label, newKeyID)
	}
	if _, err := decodeBase64Material(currentKey, 32, rotation.KeyName); err != nil {
		return fmt.Errorf("cannot rotate %s key: %w", rotation.Label, err)
	}
	previous, err := parsePreviousEncryptionKeyEntries(current[rotation.PreviousName], rotation.PreviousName)
	if err != nil {
		return err
	}
	nextKey, err := generateServiceAESKey()
	if err != nil {
		return err
	}
	target[rotation.KeyName] = nextKey
	target[rotation.KeyIDName] = newKeyID
	target[rotation.PreviousName] = formatPreviousEncryptionKeyEntries(prependPreviousEncryptionKey(previous, previousEncryptionKeyEntry{
		ID:  currentKeyID,
		Key: currentKey,
	}))
	return nil
}

func generateServiceAESKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate service AES key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func parsePreviousEncryptionKeyEntries(value string, label string) ([]previousEncryptionKeyEntry, error) {
	entries := []previousEncryptionKeyEntry{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		keyID, key, ok := strings.Cut(raw, ":")
		keyID = strings.TrimSpace(keyID)
		key = strings.TrimSpace(key)
		if !ok || keyID == "" || key == "" {
			return nil, fmt.Errorf("%s must use comma-separated key_id:base64 entries", label)
		}
		if _, err := decodeBase64Material(key, 32, label+" "+keyID); err != nil {
			return nil, err
		}
		entries = append(entries, previousEncryptionKeyEntry{ID: keyID, Key: key})
	}
	return entries, nil
}

func prependPreviousEncryptionKey(entries []previousEncryptionKeyEntry, current previousEncryptionKeyEntry) []previousEncryptionKeyEntry {
	out := make([]previousEncryptionKeyEntry, 0, len(entries)+1)
	seen := make(map[string]struct{}, len(entries)+1)
	appendUnique := func(entry previousEncryptionKeyEntry) {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Key = strings.TrimSpace(entry.Key)
		if entry.ID == "" || entry.Key == "" {
			return
		}
		fingerprint := entry.ID + "\x00" + entry.Key
		if _, ok := seen[fingerprint]; ok {
			return
		}
		seen[fingerprint] = struct{}{}
		out = append(out, entry)
	}
	appendUnique(current)
	for _, entry := range entries {
		appendUnique(entry)
	}
	return out
}

func formatPreviousEncryptionKeyEntries(entries []previousEncryptionKeyEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Key) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(entry.ID)+":"+strings.TrimSpace(entry.Key))
	}
	return strings.Join(parts, ",")
}
