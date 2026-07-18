package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
)

func TestPrepareOrchestrationServiceKeysSupportsVerifyOnlyWorker(t *testing.T) {
	verifyKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, ed25519.PublicKeySize))
	signingKey, err := prepareOrchestrationServiceKeys("", verifyKey)
	if err != nil {
		t.Fatalf("prepare verify-only orchestration key: %v", err)
	}
	if signingKey != "" {
		t.Fatalf("verify-only worker unexpectedly received a signing key")
	}
}

func TestServiceInstallRejectsVerifyOnlyOwnerConfiguration(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "false")
	verifyKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x41}, ed25519.PublicKeySize))
	err := serviceInstall([]string{"--dry-run", "--orchestration-verify-key", verifyKey})
	if err == nil || !strings.Contains(err.Error(), "owner service") || !strings.Contains(err.Error(), "verify-only workers are not supported") {
		t.Fatalf("verify-only owner install error = %v", err)
	}
}

func TestPrepareOrchestrationServiceKeysGeneratesOwnerKeyOnlyWhenBothKeysMissing(t *testing.T) {
	signingKey, err := prepareOrchestrationServiceKeys("", "")
	if err != nil {
		t.Fatalf("prepare default owner orchestration key: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(signingKey)
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatalf("generated signing key is not a %d-byte Ed25519 seed", ed25519.SeedSize)
	}
}

func TestPrepareOrchestrationServiceKeysRejectsInvalidVerifyOnlyKey(t *testing.T) {
	_, err := prepareOrchestrationServiceKeys("", "not-a-public-key")
	if err == nil || !strings.Contains(err.Error(), "32-byte Ed25519 public key") {
		t.Fatalf("invalid verify-only key error = %v", err)
	}
}
