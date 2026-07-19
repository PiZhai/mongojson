package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestApplyServiceEnvKeyRotationPreservesCurrentKeyAsPrevious(t *testing.T) {
	currentKey := testBase64Key(1)
	previousKey := testBase64Key(2)
	current := map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY":           currentKey,
		"STEWARD_SYNC_ENCRYPTION_KEY_ID":        "sync-v1",
		"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS": "sync-v0:" + previousKey,
	}
	target := map[string]string{}

	err := applyServiceEnvKeyRotation(current, target, serviceEnvKeyRotation{
		NewKeyID:     "sync-v2",
		KeyName:      "STEWARD_SYNC_ENCRYPTION_KEY",
		KeyIDName:    "STEWARD_SYNC_ENCRYPTION_KEY_ID",
		PreviousName: "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
		Label:        "sync encryption",
	})
	if err != nil {
		t.Fatal(err)
	}

	if target["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-v2" {
		t.Fatalf("new key id was not set: %#v", target)
	}
	if target["STEWARD_SYNC_ENCRYPTION_KEY"] == "" || target["STEWARD_SYNC_ENCRYPTION_KEY"] == currentKey {
		t.Fatalf("new key was not generated: %#v", target)
	}
	decoded, err := base64.StdEncoding.DecodeString(target["STEWARD_SYNC_ENCRYPTION_KEY"])
	if err != nil || len(decoded) != 32 {
		t.Fatalf("generated key is not a 32-byte base64 key: len=%d err=%v", len(decoded), err)
	}
	previous := target["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"]
	if previous != "sync-v1:"+currentKey+",sync-v0:"+previousKey {
		t.Fatalf("previous keys = %q", previous)
	}
}

func TestApplyServiceEnvKeyRotationPreservesHistoricalKeysSharingID(t *testing.T) {
	currentKey := testBase64Key(1)
	oldSameIDKey := testBase64Key(2)
	olderKey := testBase64Key(3)
	current := map[string]string{
		"STEWARD_LOCAL_ENCRYPTION_KEY":    currentKey,
		"STEWARD_LOCAL_ENCRYPTION_KEY_ID": "windows-local-v1",
		"STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS": strings.Join([]string{
			"windows-local-v1:" + oldSameIDKey,
			"windows-local-v0:" + olderKey,
			"windows-local-v1:" + currentKey,
		}, ","),
	}
	target := map[string]string{}

	err := applyServiceEnvKeyRotation(current, target, serviceEnvKeyRotation{
		NewKeyID:     "windows-local-v2",
		KeyName:      "STEWARD_LOCAL_ENCRYPTION_KEY",
		KeyIDName:    "STEWARD_LOCAL_ENCRYPTION_KEY_ID",
		PreviousName: "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS",
		Label:        "local encryption",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"windows-local-v1:" + currentKey,
		"windows-local-v1:" + oldSameIDKey,
		"windows-local-v0:" + olderKey,
	}, ",")
	if got := target["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"]; got != want {
		t.Fatalf("previous keys = %q, want %q", got, want)
	}
}

func TestApplyServiceEnvKeyRotationRejectsUnsafeInputs(t *testing.T) {
	current := map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY":    testBase64Key(1),
		"STEWARD_SYNC_ENCRYPTION_KEY_ID": "sync-v1",
	}
	base := serviceEnvKeyRotation{
		KeyName:      "STEWARD_SYNC_ENCRYPTION_KEY",
		KeyIDName:    "STEWARD_SYNC_ENCRYPTION_KEY_ID",
		PreviousName: "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS",
		Label:        "sync encryption",
	}

	sameID := base
	sameID.NewKeyID = "sync-v1"
	if err := applyServiceEnvKeyRotation(current, map[string]string{}, sameID); err == nil {
		t.Fatalf("expected same key id rotation to fail")
	}

	missingCurrent := base
	missingCurrent.NewKeyID = "sync-v2"
	if err := applyServiceEnvKeyRotation(map[string]string{}, map[string]string{}, missingCurrent); err == nil {
		t.Fatalf("expected missing current key rotation to fail")
	}

	invalidPrevious := base
	invalidPrevious.NewKeyID = "sync-v2"
	badCurrent := map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY":           testBase64Key(1),
		"STEWARD_SYNC_ENCRYPTION_KEY_ID":        "sync-v1",
		"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS": "broken",
	}
	if err := applyServiceEnvKeyRotation(badCurrent, map[string]string{}, invalidPrevious); err == nil {
		t.Fatalf("expected invalid previous key list to fail")
	}
}

func TestValidateServiceEnvRotationConflictsWithManualPatch(t *testing.T) {
	rotations := serviceEnvRotationOptions{SyncKeyID: "sync-v2"}
	if err := validateServiceEnvRotationConflicts(map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY": "manual-key",
	}, nil, rotations); err == nil || !strings.Contains(err.Error(), "--set STEWARD_SYNC_ENCRYPTION_KEY") {
		t.Fatalf("expected set conflict, got %v", err)
	}
	if err := validateServiceEnvRotationConflicts(nil, []string{"STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"}, rotations); err == nil || !strings.Contains(err.Error(), "--remove STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS") {
		t.Fatalf("expected remove conflict, got %v", err)
	}
}

func TestServiceEnvRotationTransformRotatesBothKeyFamilies(t *testing.T) {
	current := map[string]string{
		"STEWARD_SYNC_ENCRYPTION_KEY":     testBase64Key(1),
		"STEWARD_SYNC_ENCRYPTION_KEY_ID":  "sync-v1",
		"STEWARD_LOCAL_ENCRYPTION_KEY":    testBase64Key(2),
		"STEWARD_LOCAL_ENCRYPTION_KEY_ID": "local-v1",
	}
	target := map[string]string{}
	transform := serviceEnvRotationOptions{SyncKeyID: "sync-v2", LocalKeyID: "local-v2"}.transform()
	if transform == nil {
		t.Fatalf("expected rotation transform")
	}
	if err := transform(current, target); err != nil {
		t.Fatal(err)
	}
	if target["STEWARD_SYNC_ENCRYPTION_KEY_ID"] != "sync-v2" ||
		target["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] != "local-v2" {
		t.Fatalf("rotation transform did not set both key ids: %#v", target)
	}
}

func testBase64Key(seed byte) string {
	key := make([]byte, 32)
	for index := range key {
		key[index] = seed
	}
	return base64.StdEncoding.EncodeToString(key)
}
