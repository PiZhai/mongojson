package steward

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	PairingChallengeAlgorithm = SyncKeyAlgorithmEd25519
	pairingChallengeVersion   = "mongojson.steward.pairing.challenge.v1"
)

type PairingChallengeInput struct {
	Challenge string `json:"challenge"`
}

type PairingChallengeResult struct {
	DeviceID  string    `json:"device_id"`
	PublicKey string    `json:"public_key"`
	Algorithm string    `json:"algorithm"`
	Challenge string    `json:"challenge"`
	Signature string    `json:"signature"`
	SignedAt  time.Time `json:"signed_at"`
}

type VerifyDeviceTrustResult struct {
	Device         domain.StewardDevice    `json:"device"`
	Verified       bool                    `json:"verified"`
	Challenge      string                  `json:"challenge"`
	Algorithm      string                  `json:"algorithm"`
	SignedAt       *time.Time              `json:"signed_at,omitempty"`
	PublicKeyMatch bool                    `json:"public_key_match"`
	Response       *PairingChallengeResult `json:"response,omitempty"`
}

func (s *Service) SignPairingChallenge(_ context.Context, input PairingChallengeInput) (PairingChallengeResult, error) {
	challenge := strings.TrimSpace(input.Challenge)
	if challenge == "" {
		return PairingChallengeResult{}, fmt.Errorf("pairing challenge is required")
	}
	if len(challenge) > 4096 {
		return PairingChallengeResult{}, fmt.Errorf("pairing challenge is too large")
	}

	privateKeyValue := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PRIVATE_KEY", ""))
	if privateKeyValue == "" {
		return PairingChallengeResult{}, fmt.Errorf("STEWARD_DEVICE_PRIVATE_KEY is required to sign pairing challenges")
	}
	privateKey, err := parseSyncPrivateKey(privateKeyValue)
	if err != nil {
		return PairingChallengeResult{}, err
	}
	derivedPublicKey, ok := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	if !ok {
		return PairingChallengeResult{}, fmt.Errorf("derive public key from private key")
	}
	derivedPublicKeyText := base64.StdEncoding.EncodeToString(derivedPublicKey)

	publicKeyText := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PUBLIC_KEY", ""))
	if publicKeyText != "" {
		publicKeyText, err = normalizeSyncPublicKey(publicKeyText)
		if err != nil {
			return PairingChallengeResult{}, err
		}
		if publicKeyText != derivedPublicKeyText {
			return PairingChallengeResult{}, fmt.Errorf("STEWARD_DEVICE_PUBLIC_KEY does not match STEWARD_DEVICE_PRIVATE_KEY")
		}
	} else {
		publicKeyText = derivedPublicKeyText
	}

	signedAt := time.Now().UTC()
	signedAtText := signedAt.Format(time.RFC3339Nano)
	canonical := pairingChallengeCanonical(s.agentIDValue(), publicKeyText, challenge, signedAtText)
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), []byte(canonical))
	return PairingChallengeResult{
		DeviceID:  s.agentIDValue(),
		PublicKey: publicKeyText,
		Algorithm: PairingChallengeAlgorithm,
		Challenge: challenge,
		Signature: base64.StdEncoding.EncodeToString(signature),
		SignedAt:  signedAt,
	}, nil
}

func (s *Service) VerifyDeviceTrust(ctx context.Context, id string) (VerifyDeviceTrustResult, error) {
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return VerifyDeviceTrustResult{}, err
	}
	result := VerifyDeviceTrustResult{
		Device:    device,
		Algorithm: PairingChallengeAlgorithm,
	}
	if device.ID == s.agentIDValue() {
		return result, fmt.Errorf("local device cannot be verified as a peer")
	}
	if device.TrustStatus == DeviceRevoked || !device.SyncEnabled {
		return result, fmt.Errorf("device is revoked or sync is disabled")
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return result, fmt.Errorf("device api_base_url is required before trust verification")
	}
	if strings.TrimSpace(device.PublicKey) == "" {
		return result, fmt.Errorf("device public_key is required before trust verification")
	}

	challenge, err := randomPairingChallenge()
	if err != nil {
		return result, err
	}
	result.Challenge = challenge
	response, err := requestPeerPairingChallenge(ctx, &http.Client{Timeout: 8 * time.Second}, device.APIBaseURL, challenge)
	if err != nil {
		return result, err
	}
	result.Response = &response
	result.SignedAt = &response.SignedAt
	if err := verifyPairingChallengeResult(device.ID, device.PublicKey, challenge, response, time.Now().UTC()); err != nil {
		return result, err
	}
	result.Verified = true
	result.PublicKeyMatch = true

	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_devices
		set last_seen_at = $1, last_sync_error = null, updated_at = $1
		where id = $2
	`, now, device.ID); err != nil {
		return result, fmt.Errorf("update verified device heartbeat: %w", err)
	}
	updated, err := s.getDevice(ctx, device.ID)
	if err == nil {
		result.Device = updated
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "device.trust.verify",
		TargetType:      "device",
		TargetID:        &device.ID,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD1,
		InputSummary:    "pairing challenge",
		OutputSummary:   "device private key possession verified",
		ResultStatus:    ResultOK,
	})
	return result, nil
}

func requestPeerPairingChallenge(ctx context.Context, client *http.Client, apiBase string, challenge string) (PairingChallengeResult, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/steward/pairing/challenge"
	payload, err := json.Marshal(PairingChallengeInput{Challenge: challenge})
	if err != nil {
		return PairingChallengeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return PairingChallengeResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return PairingChallengeResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return PairingChallengeResult{}, err
	}
	if resp.StatusCode >= 400 {
		return PairingChallengeResult{}, fmt.Errorf("pairing challenge failed with %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var wrapper struct {
		Challenge PairingChallengeResult `json:"challenge"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return PairingChallengeResult{}, fmt.Errorf("decode pairing challenge response: %w", err)
	}
	return wrapper.Challenge, nil
}

func verifyPairingChallengeResult(expectedDeviceID string, expectedPublicKey string, challenge string, response PairingChallengeResult, now time.Time) error {
	if strings.TrimSpace(response.DeviceID) != strings.TrimSpace(expectedDeviceID) {
		return fmt.Errorf("pairing challenge device mismatch: got %q want %q", response.DeviceID, expectedDeviceID)
	}
	if strings.TrimSpace(response.Algorithm) != PairingChallengeAlgorithm {
		return fmt.Errorf("unsupported pairing challenge algorithm %q", response.Algorithm)
	}
	if strings.TrimSpace(response.Challenge) != strings.TrimSpace(challenge) {
		return fmt.Errorf("pairing challenge response does not match request")
	}
	expectedPublicKey, err := normalizeSyncPublicKey(expectedPublicKey)
	if err != nil {
		return err
	}
	responsePublicKey, err := normalizeSyncPublicKey(response.PublicKey)
	if err != nil {
		return err
	}
	if responsePublicKey != expectedPublicKey {
		return fmt.Errorf("pairing challenge public key mismatch")
	}
	if response.SignedAt.IsZero() {
		return fmt.Errorf("pairing challenge signed_at is required")
	}
	delta := now.UTC().Sub(response.SignedAt.UTC())
	if delta < 0 {
		delta = -delta
	}
	if delta > 5*time.Minute {
		return fmt.Errorf("pairing challenge timestamp outside allowed window")
	}
	signature, err := decodeSyncKeyMaterial(response.Signature, "pairing challenge signature")
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("pairing challenge signature must be %d bytes, got %d", ed25519.SignatureSize, len(signature))
	}
	publicKey, err := parseSyncPublicKey(expectedPublicKey)
	if err != nil {
		return err
	}
	canonical := pairingChallengeCanonical(response.DeviceID, expectedPublicKey, response.Challenge, response.SignedAt.UTC().Format(time.RFC3339Nano))
	if !ed25519.Verify(publicKey, []byte(canonical), signature) {
		return fmt.Errorf("invalid pairing challenge signature")
	}
	return nil
}

func pairingChallengeCanonical(deviceID string, publicKey string, challenge string, signedAt string) string {
	return strings.Join([]string{
		pairingChallengeVersion,
		strings.TrimSpace(deviceID),
		strings.TrimSpace(publicKey),
		strings.TrimSpace(challenge),
		strings.TrimSpace(signedAt),
	}, "\n")
}

func randomPairingChallenge() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate pairing challenge: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}
