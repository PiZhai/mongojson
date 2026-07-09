package steward

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestSignPairingChallengeAndVerifyResult(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey)
	t.Setenv("STEWARD_DEVICE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv("STEWARD_DEVICE_PUBLIC_KEY", publicKeyText)

	result, err := (&Service{agentID: "windows-main"}).SignPairingChallenge(context.Background(), PairingChallengeInput{Challenge: "probe"})
	if err != nil {
		t.Fatal(err)
	}

	if err := verifyPairingChallengeResult("windows-main", publicKeyText, "probe", result, time.Now().UTC()); err != nil {
		t.Fatalf("expected challenge response to verify: %v", err)
	}
}

func TestSignPairingChallengeRejectsMismatchedPublicKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_DEVICE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))
	t.Setenv("STEWARD_DEVICE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(wrongPublicKey))

	if _, err := (&Service{agentID: "windows-main"}).SignPairingChallenge(context.Background(), PairingChallengeInput{Challenge: "probe"}); err == nil {
		t.Fatalf("expected mismatched public key to fail")
	}
}

func TestVerifyPairingChallengeResultRejectsWrongChallenge(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey)
	t.Setenv("STEWARD_DEVICE_PRIVATE_KEY", base64.StdEncoding.EncodeToString(privateKey))

	result, err := (&Service{agentID: "windows-main"}).SignPairingChallenge(context.Background(), PairingChallengeInput{Challenge: "probe"})
	if err != nil {
		t.Fatal(err)
	}

	if err := verifyPairingChallengeResult("windows-main", publicKeyText, "other-probe", result, time.Now().UTC()); err == nil {
		t.Fatalf("expected wrong challenge to fail")
	}
}
