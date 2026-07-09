package steward

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

func syncDevicePrivateKeyFromEnv() []byte {
	value := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PRIVATE_KEY", ""))
	if value == "" {
		return nil
	}
	privateKey, err := parseSyncPrivateKey(value)
	if err != nil {
		log.Printf("invalid STEWARD_DEVICE_PRIVATE_KEY: %v", err)
		return nil
	}
	return privateKey
}

func syncDevicePublicKeyFromEnv() string {
	value := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PUBLIC_KEY", ""))
	if value != "" {
		publicKey, err := normalizeSyncPublicKey(value)
		if err != nil {
			log.Printf("invalid STEWARD_DEVICE_PUBLIC_KEY: %v", err)
			return ""
		}
		return publicKey
	}

	privateKey := syncDevicePrivateKeyFromEnv()
	if len(privateKey) == 0 {
		return ""
	}
	publicKey, ok := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	if !ok {
		return ""
	}
	return base64.StdEncoding.EncodeToString(publicKey)
}

func normalizeSyncPublicKey(value string) (string, error) {
	publicKey, err := parseSyncPublicKey(value)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func parseSyncPublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := decodeSyncKeyMaterial(value, "public key")
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(decoded))
	}
	return ed25519.PublicKey(decoded), nil
}

func parseSyncPrivateKey(value string) (ed25519.PrivateKey, error) {
	decoded, err := decodeSyncKeyMaterial(value, "private key")
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(decoded))
	}
	return ed25519.PrivateKey(decoded), nil
}

func decodeSyncKeyMaterial(value string, label string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, SyncKeyAlgorithmEd25519+":")
	value = strings.TrimPrefix(value, "base64:")
	if value == "" {
		return nil, fmt.Errorf("%s is empty", label)
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("%s must be base64-encoded", label)
}

func syncDeviceKeySignature(privateKey []byte, method string, path string, rawQuery string, timestamp string, bodyHash string, deviceID string) string {
	signature := ed25519.Sign(ed25519.PrivateKey(privateKey), []byte(syncCanonicalString(method, path, rawQuery, timestamp, bodyHash, deviceID)))
	return base64.StdEncoding.EncodeToString(signature)
}

func verifyDeviceKeyRequestSignature(publicKeyValue string, now time.Time, req *http.Request, body []byte) error {
	publicKey, err := parseSyncPublicKey(publicKeyValue)
	if err != nil {
		return err
	}
	material, err := syncRequestSigningMaterial(now, req, body)
	if err != nil {
		return err
	}
	algorithm := strings.TrimSpace(req.Header.Get(SyncHeaderKeyAlgorithm))
	if algorithm != "" && !strings.EqualFold(algorithm, SyncKeyAlgorithmEd25519) {
		return fmt.Errorf("unsupported sync key algorithm %q", algorithm)
	}
	signatureValue := strings.TrimSpace(req.Header.Get(SyncHeaderKeySignature))
	if signatureValue == "" {
		return fmt.Errorf("missing sync key signature")
	}
	signature, err := decodeSyncKeyMaterial(signatureValue, "key signature")
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("key signature must be %d bytes, got %d", ed25519.SignatureSize, len(signature))
	}
	if !ed25519.Verify(publicKey, []byte(material.Canonical), signature) {
		return fmt.Errorf("invalid sync key signature")
	}
	return nil
}

func (s *Service) verifyDeviceKeySyncRequest(ctx context.Context, now time.Time, req *http.Request, body []byte) error {
	deviceID := strings.TrimSpace(req.Header.Get(SyncHeaderDeviceID))
	if deviceID == "" {
		return fmt.Errorf("missing sync device id")
	}
	device, err := s.requireAuthorizedSyncDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(device.PublicKey) == "" {
		return fmt.Errorf("sync device %q has no registered public key", deviceID)
	}
	return verifyDeviceKeyRequestSignature(device.PublicKey, now, req, body)
}

func (s *Service) requireAuthorizedSyncDevice(ctx context.Context, deviceID string) (domain.StewardDevice, error) {
	if s == nil || s.db == nil {
		return domain.StewardDevice{}, fmt.Errorf("steward service is not available for sync device authorization")
	}
	deviceID = strings.TrimSpace(deviceID)
	device, err := s.getDevice(ctx, deviceID)
	if err != nil {
		return domain.StewardDevice{}, fmt.Errorf("sync device %q is not registered", deviceID)
	}
	if device.TrustStatus != DeviceTrusted {
		return domain.StewardDevice{}, fmt.Errorf("sync device %q is not trusted", deviceID)
	}
	if !device.SyncEnabled {
		return domain.StewardDevice{}, fmt.Errorf("sync device %q is disabled", deviceID)
	}
	return device, nil
}

type syncSigningMaterial struct {
	DeviceID  string
	Timestamp string
	BodyHash  string
	Canonical string
}

func syncRequestSigningMaterial(now time.Time, req *http.Request, body []byte) (syncSigningMaterial, error) {
	deviceID := strings.TrimSpace(req.Header.Get(SyncHeaderDeviceID))
	timestamp := strings.TrimSpace(req.Header.Get(SyncHeaderTimestamp))
	bodyHash := strings.TrimSpace(req.Header.Get(SyncHeaderBodyHash))
	if deviceID == "" || timestamp == "" || bodyHash == "" {
		return syncSigningMaterial{}, fmt.Errorf("missing sync signing headers")
	}
	signedAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return syncSigningMaterial{}, fmt.Errorf("invalid sync signature timestamp")
	}
	if delta := now.UTC().Sub(signedAt.UTC()); delta > 5*time.Minute || delta < -5*time.Minute {
		return syncSigningMaterial{}, fmt.Errorf("sync signature timestamp outside allowed window")
	}
	actualBodyHash := hashBytes(body)
	if bodyHash != actualBodyHash {
		return syncSigningMaterial{}, fmt.Errorf("sync body hash mismatch")
	}
	path := ""
	rawQuery := ""
	if req.URL != nil {
		path = req.URL.EscapedPath()
		rawQuery = req.URL.RawQuery
	}
	return syncSigningMaterial{
		DeviceID:  deviceID,
		Timestamp: timestamp,
		BodyHash:  bodyHash,
		Canonical: syncCanonicalString(req.Method, path, rawQuery, timestamp, bodyHash, deviceID),
	}, nil
}
