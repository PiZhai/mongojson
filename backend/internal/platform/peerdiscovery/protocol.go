package peerdiscovery

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	protocolVersion     = "steward-peer-discovery/v1"
	maxAnnouncementSize = 16 << 10
	maxClockSkew        = 30 * time.Second
)

type announcementPayload struct {
	Protocol    string `json:"protocol"`
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	Platform    string `json:"platform"`
	PeerAPIBase string `json:"peer_api_base"`
	PublicKey   string `json:"public_key"`
	IssuedAtMS  int64  `json:"issued_at_ms"`
	Nonce       string `json:"nonce"`
}

type signedAnnouncement struct {
	announcementPayload
	Signature string `json:"signature"`
}

type normalizedIdentity struct {
	DeviceID      string
	DeviceName    string
	Platform      string
	PeerAPIBase   string
	PublicKeyText string
	PublicKey     ed25519.PublicKey
	PrivateKey    ed25519.PrivateKey
}

func buildSignedAnnouncement(identity normalizedIdentity, now time.Time, random io.Reader) ([]byte, error) {
	if random == nil {
		random = rand.Reader
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, fmt.Errorf("generate discovery nonce: %w", err)
	}
	payload := announcementPayload{
		Protocol:    protocolVersion,
		DeviceID:    identity.DeviceID,
		DeviceName:  identity.DeviceName,
		Platform:    identity.Platform,
		PeerAPIBase: identity.PeerAPIBase,
		PublicKey:   identity.PublicKeyText,
		IssuedAtMS:  now.UTC().UnixMilli(),
		Nonce:       base64.RawURLEncoding.EncodeToString(nonce),
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal discovery payload: %w", err)
	}
	announcement := signedAnnouncement{
		announcementPayload: payload,
		Signature:           base64.StdEncoding.EncodeToString(ed25519.Sign(identity.PrivateKey, canonical)),
	}
	packet, err := json.Marshal(announcement)
	if err != nil {
		return nil, fmt.Errorf("marshal signed discovery announcement: %w", err)
	}
	if len(packet) > maxAnnouncementSize {
		return nil, fmt.Errorf("discovery announcement exceeds %d bytes", maxAnnouncementSize)
	}
	return packet, nil
}

func verifyAnnouncement(packet []byte, receivedAt time.Time, ttl time.Duration) (domain.StewardDiscoveredPeer, error) {
	if len(packet) == 0 || len(packet) > maxAnnouncementSize {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("invalid discovery announcement size")
	}
	decoder := json.NewDecoder(bytes.NewReader(packet))
	decoder.DisallowUnknownFields()
	var announcement signedAnnouncement
	if err := decoder.Decode(&announcement); err != nil {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("decode discovery announcement: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return domain.StewardDiscoveredPeer{}, err
	}
	if err := validateAnnouncementPayload(announcement.announcementPayload); err != nil {
		return domain.StewardDiscoveredPeer{}, err
	}
	publicKey, err := decodePublicKey(announcement.PublicKey)
	if err != nil {
		return domain.StewardDiscoveredPeer{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(announcement.Signature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("invalid discovery signature")
	}
	canonical, err := json.Marshal(announcement.announcementPayload)
	if err != nil {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("marshal discovery verification payload: %w", err)
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("discovery signature verification failed")
	}

	receivedAt = receivedAt.UTC()
	issuedAt := time.UnixMilli(announcement.IssuedAtMS).UTC()
	if issuedAt.After(receivedAt.Add(maxClockSkew)) {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("discovery announcement is from the future")
	}
	expiresAt := issuedAt.Add(ttl)
	if !expiresAt.After(receivedAt) {
		return domain.StewardDiscoveredPeer{}, fmt.Errorf("discovery announcement expired")
	}
	digest := sha256.Sum256(publicKey)
	return domain.StewardDiscoveredPeer{
		DeviceID:             announcement.DeviceID,
		DeviceName:           announcement.DeviceName,
		Platform:             announcement.Platform,
		PeerAPIBase:          announcement.PeerAPIBase,
		PublicKey:            base64.StdEncoding.EncodeToString(publicKey),
		PublicKeyFingerprint: hex.EncodeToString(digest[:]),
		IssuedAt:             issuedAt,
		LastSeenAt:           receivedAt,
		ExpiresAt:            expiresAt,
		SignatureVerified:    true,
	}, nil
}

func validateAnnouncementPayload(payload announcementPayload) error {
	if payload.Protocol != protocolVersion {
		return fmt.Errorf("unsupported discovery protocol %q", payload.Protocol)
	}
	if err := validateTextField("device_id", payload.DeviceID, 128); err != nil {
		return err
	}
	if err := validateTextField("device_name", payload.DeviceName, 256); err != nil {
		return err
	}
	if err := validateTextField("platform", payload.Platform, 32); err != nil {
		return err
	}
	if err := validateTextField("nonce", payload.Nonce, 128); err != nil {
		return err
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(payload.Nonce))
	if err != nil || len(nonce) != 16 {
		return fmt.Errorf("discovery nonce must contain 16 random bytes")
	}
	if payload.IssuedAtMS <= 0 {
		return fmt.Errorf("discovery issued_at_ms is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(payload.PeerAPIBase))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("discovery peer_api_base must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if len(payload.PeerAPIBase) > 2048 {
		return fmt.Errorf("discovery peer_api_base is too long")
	}
	return nil
}

func validateTextField(name string, value string, max int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("discovery %s is required", name)
	}
	if len(value) > max {
		return fmt.Errorf("discovery %s exceeds %d bytes", name, max)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("discovery %s contains control characters", name)
		}
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("discovery announcement contains trailing JSON")
		}
		return fmt.Errorf("decode discovery announcement trailer: %w", err)
	}
	return nil
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid discovery public key")
	}
	return ed25519.PublicKey(decoded), nil
}
