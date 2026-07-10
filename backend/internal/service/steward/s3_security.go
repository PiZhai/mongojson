package steward

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/netpolicy"
)

func syncSecurityStatusFromEnv() domain.StewardSyncSecurityStatus {
	secret := strings.TrimSpace(envOrDefault("STEWARD_SYNC_SECRET", ""))
	authRequired := syncAuthenticationRequired(secret)
	managementAddr := strings.TrimSpace(envOrDefault("HTTP_ADDR", "127.0.0.1:8080"))
	peerAddr := strings.TrimSpace(envOrDefault("STEWARD_PEER_HTTP_ADDR", ""))
	publicAPIBase := strings.TrimRight(strings.TrimSpace(envOrDefault("STEWARD_PUBLIC_API_BASE", "")), "/")
	out := domain.StewardSyncSecurityStatus{
		ManagementAPIAddr:    managementAddr,
		PeerAPIAddr:          peerAddr,
		PeerAPIEnabled:       peerAddr != "",
		PublicAPIBase:        publicAPIBase,
		PeerAPIAdvertised:    publicAPIBase != "",
		AuthRequired:         authRequired,
		InsecureModeActive:   !authRequired,
		HMACSecretConfigured: secret != "",
		ConfigErrors:         []string{},
	}
	if parsed, err := netpolicy.ParseListenAddress(managementAddr); err != nil {
		out.ConfigErrors = append(out.ConfigErrors, "HTTP_ADDR: "+err.Error())
	} else {
		out.ManagementRemoteAccess = !parsed.IsLoopback
	}
	if peerAddr != "" {
		if _, err := netpolicy.ParseListenAddress(peerAddr); err != nil {
			out.ConfigErrors = append(out.ConfigErrors, "STEWARD_PEER_HTTP_ADDR: "+err.Error())
		}
	}

	privateKeyValue := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PRIVATE_KEY", ""))
	if privateKeyValue != "" {
		out.DevicePrivateKeyConfigured = true
		if _, err := parseSyncPrivateKey(privateKeyValue); err != nil {
			out.ConfigErrors = append(out.ConfigErrors, "STEWARD_DEVICE_PRIVATE_KEY: "+err.Error())
		} else {
			out.DevicePrivateKeyValid = true
		}
	}

	publicKeyValue := strings.TrimSpace(envOrDefault("STEWARD_DEVICE_PUBLIC_KEY", ""))
	if publicKeyValue != "" {
		out.DevicePublicKeyConfigured = true
		if _, err := parseSyncPublicKey(publicKeyValue); err != nil {
			out.ConfigErrors = append(out.ConfigErrors, "STEWARD_DEVICE_PUBLIC_KEY: "+err.Error())
		} else {
			out.DevicePublicKeyValid = true
		}
	}
	out.DeviceSigningReady = out.DevicePrivateKeyValid
	out.DeviceIdentityAdvertisable = out.DevicePublicKeyValid || out.DevicePrivateKeyValid

	syncKey, syncKeyID, err := syncAuthEncryptionKeyFromEnv()
	if err != nil {
		out.ConfigErrors = append(out.ConfigErrors, "STEWARD_SYNC_ENCRYPTION_KEY: "+err.Error())
	} else if len(syncKey) > 0 {
		out.SyncEncryptionConfigured = true
		out.SyncEncryptionKeyID = syncKeyID
	}
	syncPreviousKeys, err := syncAuthPreviousEncryptionKeysFromEnv()
	if err != nil {
		out.ConfigErrors = append(out.ConfigErrors, "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS: "+err.Error())
	} else {
		out.SyncPreviousKeyCount = len(syncPreviousKeys)
	}

	localKey, localKeyID, err := payloadEncryptionKeyFromEnv("STEWARD_LOCAL_ENCRYPTION_KEY", "STEWARD_LOCAL_ENCRYPTION_KEY_ID", "local encryption key")
	if err != nil {
		out.ConfigErrors = append(out.ConfigErrors, "STEWARD_LOCAL_ENCRYPTION_KEY: "+err.Error())
	} else if len(localKey) > 0 {
		out.LocalEncryptionConfigured = true
		out.LocalEncryptionKeyID = localKeyID
	}
	localPreviousKeys, err := previousPayloadEncryptionKeysFromEnv("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", "previous local encryption key")
	if err != nil {
		out.ConfigErrors = append(out.ConfigErrors, "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS: "+err.Error())
	} else {
		out.LocalPreviousKeyCount = len(localPreviousKeys)
	}

	return out
}

func (s *Service) VerifySyncRequest(r *http.Request, body []byte) error {
	secret := strings.TrimSpace(envOrDefault("STEWARD_SYNC_SECRET", ""))
	requireAuth := syncAuthenticationRequired(secret)
	hasHMACSignature := strings.TrimSpace(r.Header.Get(SyncHeaderSignature)) != ""
	hasKeySignature := strings.TrimSpace(r.Header.Get(SyncHeaderKeySignature)) != ""

	var lastErr error
	if secret != "" && hasHMACSignature {
		if err := verifySyncRequestSignature(secret, time.Now().UTC(), r, body); err == nil {
			if _, err := s.requireAuthorizedSyncDevice(r.Context(), r.Header.Get(SyncHeaderDeviceID)); err == nil {
				return nil
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}
	}
	if hasKeySignature {
		if err := s.verifyDeviceKeySyncRequest(r.Context(), time.Now().UTC(), r, body); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if !requireAuth {
		return nil
	}
	if secret != "" && !hasKeySignature {
		if err := verifySyncRequestSignature(secret, time.Now().UTC(), r, body); err != nil {
			return err
		}
		_, err := s.requireAuthorizedSyncDevice(r.Context(), r.Header.Get(SyncHeaderDeviceID))
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("missing sync authentication headers")
}

func syncAuthenticationRequired(secret string) bool {
	return strings.TrimSpace(secret) != "" ||
		boolEnv("STEWARD_SYNC_REQUIRE_AUTH", false) ||
		!boolEnv("STEWARD_SYNC_ALLOW_INSECURE", false)
}
