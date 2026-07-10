package steward

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

func (s *Service) syncAuth() syncAuth {
	encryptionKeyConfigured := strings.TrimSpace(envOrDefault("STEWARD_SYNC_ENCRYPTION_KEY", "")) != ""
	return syncAuth{
		DeviceID:                s.agentIDValue(),
		Secret:                  strings.TrimSpace(envOrDefault("STEWARD_SYNC_SECRET", "")),
		DevicePrivateKey:        syncDevicePrivateKeyFromEnv(),
		SyncEncryptionRequested: encryptionKeyConfigured,
	}
}

type syncAuth struct {
	DeviceID                string
	Secret                  string
	DevicePrivateKey        []byte
	SyncEncryptionRequested bool
}

func (a syncAuth) enabled() bool {
	return strings.TrimSpace(a.DeviceID) != "" &&
		(strings.TrimSpace(a.Secret) != "" || len(a.DevicePrivateKey) > 0)
}

func (a syncAuth) encryptionEnabled() bool {
	return a.SyncEncryptionRequested
}

func getPeerSyncChanges(ctx context.Context, client *http.Client, apiBase string, sinceSequence int64, auth syncAuth) (PeerSyncChangesResult, error) {
	query := url.Values{
		"since_sequence": []string{fmt.Sprintf("%d", sinceSequence)},
		"limit":          []string{"200"},
	}
	if auth.encryptionEnabled() {
		query.Set(SyncEncryptionQueryParam, "true")
	}
	endpoint, err := stewardAPIEndpoint(apiBase, "/steward/sync/changes", query)
	if err != nil {
		return PeerSyncChangesResult{}, err
	}
	var response PeerSyncChangesResult
	if err := requestPeerJSON(ctx, client, http.MethodGet, endpoint, nil, &response, auth); err != nil {
		return PeerSyncChangesResult{}, err
	}
	if response.Changes == nil {
		response.Changes = []domain.StewardSyncChange{}
	}
	if response.NextSequence == 0 {
		response.NextSequence = sinceSequence
		for _, change := range response.Changes {
			if change.Sequence > response.NextSequence {
				response.NextSequence = change.Sequence
			}
		}
		if len(response.Changes) == syncChangeWindowLimit {
			response.HasMore = true
		}
	}
	if response.NextSequence < sinceSequence {
		return PeerSyncChangesResult{}, fmt.Errorf("peer sync cursor moved backwards from %d to %d", sinceSequence, response.NextSequence)
	}
	return response, nil
}

func postPeerSyncChanges(ctx context.Context, client *http.Client, apiBase string, payload ImportSyncChangesInput, auth syncAuth) (ImportSyncChangesResult, error) {
	endpoint, err := stewardAPIEndpoint(apiBase, "/steward/sync/changes/import", nil)
	if err != nil {
		return ImportSyncChangesResult{}, err
	}
	payload, err = prepareImportSyncChangesForTransport(payload, auth)
	if err != nil {
		return ImportSyncChangesResult{}, err
	}
	var response struct {
		Result ImportSyncChangesResult `json:"result"`
	}
	if err := requestPeerJSON(ctx, client, http.MethodPost, endpoint, payload, &response, auth); err != nil {
		return ImportSyncChangesResult{}, err
	}
	return response.Result, nil
}

func requestPeerJSON(ctx context.Context, client *http.Client, method string, endpoint string, payload any, target any, auth syncAuth) error {
	var body io.Reader
	var encoded []byte
	if payload != nil {
		var err error
		encoded, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth.enabled() {
		signSyncRequest(req, encoded, auth, time.Now().UTC())
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("peer request failed with %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode peer response: %w", err)
	}
	return nil
}

func signSyncRequest(req *http.Request, body []byte, auth syncAuth, now time.Time) {
	timestamp := now.UTC().Format(time.RFC3339)
	bodyHash := hashBytes(body)
	req.Header.Set(SyncHeaderDeviceID, auth.DeviceID)
	req.Header.Set(SyncHeaderTimestamp, timestamp)
	req.Header.Set(SyncHeaderBodyHash, bodyHash)
	if strings.TrimSpace(auth.Secret) != "" {
		signature := syncSignature(auth.Secret, req.Method, req.URL.EscapedPath(), req.URL.RawQuery, timestamp, bodyHash, auth.DeviceID)
		req.Header.Set(SyncHeaderSignature, signature)
	}
	if len(auth.DevicePrivateKey) > 0 {
		req.Header.Set(SyncHeaderKeyAlgorithm, SyncKeyAlgorithmEd25519)
		req.Header.Set(SyncHeaderKeySignature, syncDeviceKeySignature(auth.DevicePrivateKey, req.Method, req.URL.EscapedPath(), req.URL.RawQuery, timestamp, bodyHash, auth.DeviceID))
	}
}

func verifySyncRequestSignature(secret string, now time.Time, req *http.Request, body []byte) error {
	deviceID := strings.TrimSpace(req.Header.Get(SyncHeaderDeviceID))
	timestamp := strings.TrimSpace(req.Header.Get(SyncHeaderTimestamp))
	bodyHash := strings.TrimSpace(req.Header.Get(SyncHeaderBodyHash))
	signature := strings.TrimSpace(req.Header.Get(SyncHeaderSignature))
	if deviceID == "" || timestamp == "" || bodyHash == "" || signature == "" {
		return fmt.Errorf("missing sync signature headers")
	}
	signedAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return fmt.Errorf("invalid sync signature timestamp")
	}
	if delta := now.UTC().Sub(signedAt.UTC()); delta > 5*time.Minute || delta < -5*time.Minute {
		return fmt.Errorf("sync signature timestamp outside allowed window")
	}
	actualBodyHash := hashBytes(body)
	if subtle.ConstantTimeCompare([]byte(bodyHash), []byte(actualBodyHash)) != 1 {
		return fmt.Errorf("sync body hash mismatch")
	}
	expected := syncSignature(secret, req.Method, req.URL.EscapedPath(), req.URL.RawQuery, timestamp, bodyHash, deviceID)
	decodedExpected, err := hex.DecodeString(expected)
	if err != nil {
		return fmt.Errorf("invalid expected sync signature")
	}
	decodedActual, err := hex.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("invalid sync signature encoding")
	}
	if !hmac.Equal(decodedActual, decodedExpected) {
		return fmt.Errorf("invalid sync signature")
	}
	return nil
}

func syncSignature(secret string, method string, path string, rawQuery string, timestamp string, bodyHash string, deviceID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(syncCanonicalString(method, path, rawQuery, timestamp, bodyHash, deviceID)))
	return hex.EncodeToString(mac.Sum(nil))
}

func syncCanonicalString(method string, path string, rawQuery string, timestamp string, bodyHash string, deviceID string) string {
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		rawQuery,
		timestamp,
		bodyHash,
		deviceID,
	}, "\n")
}

func stewardAPIEndpoint(apiBase string, path string, query url.Values) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		return "", fmt.Errorf("api_base_url is required")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse api_base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("api_base_url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("api_base_url must include host")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func syncChangeToInput(change domain.StewardSyncChange) CreateSyncChangeInput {
	payload := change.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	return CreateSyncChangeInput{
		ID:             change.ID,
		EntityType:     change.EntityType,
		EntityID:       change.EntityID,
		Operation:      change.Operation,
		OriginDeviceID: change.OriginDeviceID,
		Version:        change.Version,
		DataLevel:      change.DataLevel,
		Payload:        payload,
	}
}

func buildPullSyncWindow(localDeviceID string, device domain.StewardDevice, remoteChanges []domain.StewardSyncChange, currentRemoteLastSequence int64) syncPullWindow {
	window := syncPullWindow{
		Input: ImportSyncChangesInput{
			Device: RegisterDeviceInput{
				ID:              device.ID,
				DeviceName:      device.DeviceName,
				Platform:        device.Platform,
				Role:            DeviceRolePeer,
				SyncEnabled:     boolPtr(device.SyncEnabled),
				PermissionLevel: device.PermissionLevel,
				PublicKey:       device.PublicKey,
				APIBaseURL:      device.APIBaseURL,
			},
		},
		RemoteLastSequence: currentRemoteLastSequence,
	}
	for _, change := range remoteChanges {
		if change.Sequence > window.RemoteLastSequence {
			window.RemoteLastSequence = change.Sequence
		}
		if change.OriginDeviceID == localDeviceID {
			window.Skipped++
			continue
		}
		window.Input.Changes = append(window.Input.Changes, syncChangeToInput(change))
	}
	return window
}

func buildPushSyncWindow(localDevice RegisterDeviceInput, peerDeviceID string, localChanges []domain.StewardSyncChange, currentLocalSentSequence int64) syncPushWindow {
	window := syncPushWindow{
		Input:             ImportSyncChangesInput{Device: localDevice},
		LocalSentSequence: currentLocalSentSequence,
	}
	for _, change := range localChanges {
		if change.Sequence > window.LocalSentSequence {
			window.LocalSentSequence = change.Sequence
		}
		if change.OriginDeviceID == peerDeviceID {
			continue
		}
		window.Input.Changes = append(window.Input.Changes, syncChangeToInput(change))
	}
	return window
}
