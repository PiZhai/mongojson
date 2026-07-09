package steward

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/platform/netpolicy"
)

const (
	DeviceRoleLocal    = "local"
	DeviceRolePeer     = "peer"
	DeviceTrusted      = "trusted"
	DeviceRevoked      = "revoked"
	SyncPending        = "pending"
	SyncApplied        = "applied"
	SyncStored         = "stored"
	SyncConflictStatus = "conflict"
	SyncCreate         = "create"
	SyncUpdate         = "update"
	SyncDelete         = "delete"

	SyncHeaderDeviceID  = "X-Steward-Device-ID"
	SyncHeaderTimestamp = "X-Steward-Timestamp"
	SyncHeaderBodyHash  = "X-Steward-Body-SHA256"
	SyncHeaderSignature = "X-Steward-Signature"

	SyncHeaderKeyAlgorithm  = "X-Steward-Key-Algorithm"
	SyncHeaderKeySignature  = "X-Steward-Key-Signature"
	SyncKeyAlgorithmEd25519 = "ed25519"

	syncChangeWindowLimit = 200
	syncMaxWindowsPerRun  = 25
)

type RegisterDeviceInput struct {
	ID              string `json:"id"`
	DeviceName      string `json:"device_name"`
	Platform        string `json:"platform"`
	Role            string `json:"role"`
	SyncEnabled     *bool  `json:"sync_enabled"`
	PermissionLevel string `json:"permission_level"`
	PublicKey       string `json:"public_key"`
	APIBaseURL      string `json:"api_base_url"`
}

type UpdateDevicePermissionInput struct {
	Policy             string `json:"policy"`
	MaxPermissionLevel string `json:"max_permission_level"`
	ScopeSummary       string `json:"scope_summary"`
}

type CreateSyncChangeInput struct {
	ID             string         `json:"id"`
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id"`
	Operation      string         `json:"operation"`
	OriginDeviceID string         `json:"origin_device_id"`
	Version        int            `json:"version"`
	DataLevel      string         `json:"data_level"`
	Payload        map[string]any `json:"payload"`
}

type ImportSyncChangesInput struct {
	Device  RegisterDeviceInput     `json:"device"`
	Changes []CreateSyncChangeInput `json:"changes"`
}

type ImportSyncChangesResult struct {
	Imported  int                          `json:"imported"`
	Applied   int                          `json:"applied"`
	Skipped   int                          `json:"skipped"`
	Denied    int                          `json:"denied"`
	Conflicts []domain.StewardSyncConflict `json:"conflicts"`
	Changes   []domain.StewardSyncChange   `json:"changes"`
}

type PeerSyncChangesResult struct {
	Changes      []domain.StewardSyncChange `json:"changes"`
	NextSequence int64                      `json:"next_sequence"`
	HasMore      bool                       `json:"has_more"`
}

type ResolveSyncConflictInput struct {
	Resolution string `json:"resolution"`
}

type SyncDeviceResult struct {
	Device             domain.StewardDevice         `json:"device"`
	Pulled             int                          `json:"pulled"`
	Imported           int                          `json:"imported"`
	Applied            int                          `json:"applied"`
	Skipped            int                          `json:"skipped"`
	Pushed             int                          `json:"pushed"`
	Denied             int                          `json:"denied"`
	RemoteLastSequence int64                        `json:"remote_last_sequence"`
	LocalSentSequence  int64                        `json:"local_sent_sequence"`
	Conflicts          []domain.StewardSyncConflict `json:"conflicts"`
	Errors             []string                     `json:"errors"`
}

type SyncEntityProbeInput struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

type SyncEntityProbeResult struct {
	DeviceID   string `json:"device_id"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Exists     bool   `json:"exists"`
	Detail     any    `json:"detail,omitempty"`
}

type syncChangeListMode string

const (
	syncChangeListReplay syncChangeListMode = "replay"
	syncChangeListRecent syncChangeListMode = "recent"
)

type syncPullWindow struct {
	Input              ImportSyncChangesInput
	RemoteLastSequence int64
	Skipped            int
}

type syncPushWindow struct {
	Input             ImportSyncChangesInput
	LocalSentSequence int64
}

func (s *Service) ensureS3Defaults(ctx context.Context, hostname string, platform string, now time.Time) error {
	agentID := s.agentIDValue()
	publicKey := syncDevicePublicKeyFromEnv()
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_devices (
			id, device_name, platform, role, trust_status, sync_enabled, permission_level, public_key, api_base_url,
			last_seen_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,true,$6,$7,'',$8,$8,$8)
		on conflict (id) do update
		set device_name = excluded.device_name,
		    platform = excluded.platform,
		    role = excluded.role,
		    public_key = case when excluded.public_key <> '' then excluded.public_key else steward_devices.public_key end,
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at
	`, agentID, hostname, platform, DeviceRoleLocal, DeviceTrusted, PermissionA3, publicKey, now); err != nil {
		return fmt.Errorf("ensure local steward device: %w", err)
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_devices
		set role = $1, updated_at = $2
		where id <> $3 and role = $4
	`, DeviceRolePeer, now, agentID, DeviceRoleLocal); err != nil {
		return fmt.Errorf("normalize remote steward device roles: %w", err)
	}

	return s.ensureDefaultDevicePermissions(ctx, agentID, now)
}

func (s *Service) GetSyncStatus(ctx context.Context) (domain.StewardSyncStatus, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return domain.StewardSyncStatus{}, err
	}
	permissions, err := s.ListDevicePermissions(ctx, "")
	if err != nil {
		return domain.StewardSyncStatus{}, err
	}
	capabilities, err := s.ListDeviceCapabilities(ctx, "")
	if err != nil {
		return domain.StewardSyncStatus{}, err
	}
	changes, err := s.ListRecentSyncChanges(ctx, 12)
	if err != nil {
		return domain.StewardSyncStatus{}, err
	}
	conflicts, err := s.ListSyncConflicts(ctx, StatusOpen, 12)
	if err != nil {
		return domain.StewardSyncStatus{}, err
	}

	var local domain.StewardDevice
	for _, device := range devices {
		if device.ID == s.agentIDValue() {
			local = device
			break
		}
	}
	var pending int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_sync_changes where sync_status = $1`, SyncPending).Scan(&pending); err != nil {
		return domain.StewardSyncStatus{}, fmt.Errorf("count pending sync changes: %w", err)
	}
	var conflictCount int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_sync_conflicts where status = $1`, StatusOpen).Scan(&conflictCount); err != nil {
		return domain.StewardSyncStatus{}, fmt.Errorf("count sync conflicts: %w", err)
	}
	var pendingRelations int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_timeline_pending_events`).Scan(&pendingRelations); err != nil {
		return domain.StewardSyncStatus{}, fmt.Errorf("count pending timeline relations: %w", err)
	}
	var lastChangeAt *time.Time
	_ = s.db.Pool.QueryRow(ctx, `select max(created_at) from steward_sync_changes`).Scan(&lastChangeAt)

	return domain.StewardSyncStatus{
		LocalDevice:      local,
		Devices:          devices,
		Permissions:      permissions,
		Capabilities:     capabilities,
		Security:         syncSecurityStatusFromEnv(),
		PendingChanges:   pending,
		PendingRelations: pendingRelations,
		ConflictCount:    conflictCount,
		LastChangeAt:     lastChangeAt,
		RecentChanges:    changes,
		Conflicts:        conflicts,
	}, nil
}

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

func (s *Service) ListDevices(ctx context.Context) ([]domain.StewardDevice, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, device_name, platform, role, trust_status, sync_enabled, permission_level,
		       public_key, api_base_url, last_sync_sequence, last_sent_sequence, last_seen_at, last_sync_at, last_sync_error,
		       revoked_at, created_at, updated_at
		from steward_devices
		order by case when id = $1 then 0 else 1 end, updated_at desc
	`, s.agentIDValue())
	if err != nil {
		return nil, fmt.Errorf("list steward devices: %w", err)
	}
	defer rows.Close()

	devices := []domain.StewardDevice{}
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (s *Service) RegisterDevice(ctx context.Context, input RegisterDeviceInput) (domain.StewardDevice, error) {
	now := time.Now().UTC()
	id := defaultString(input.ID, uuid.NewString())
	role := defaultString(input.Role, DeviceRolePeer)
	syncEnabled := defaultBool(input.SyncEnabled, true)
	permission := defaultString(input.PermissionLevel, PermissionA3)
	publicKey := strings.TrimSpace(input.PublicKey)
	if publicKey != "" {
		normalizedPublicKey, err := normalizeSyncPublicKey(publicKey)
		if err != nil {
			return domain.StewardDevice{}, err
		}
		publicKey = normalizedPublicKey
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_devices (
			id, device_name, platform, role, trust_status, sync_enabled, permission_level, public_key, api_base_url,
			last_seen_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10,$10)
		on conflict (id) do update
		set device_name = excluded.device_name,
		    platform = excluded.platform,
		    role = excluded.role,
		    sync_enabled = excluded.sync_enabled,
		    permission_level = excluded.permission_level,
		    public_key = case when excluded.public_key <> '' then excluded.public_key else steward_devices.public_key end,
		    api_base_url = case when excluded.api_base_url <> '' then excluded.api_base_url else steward_devices.api_base_url end,
		    trust_status = case when steward_devices.trust_status = $11 then steward_devices.trust_status else excluded.trust_status end,
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at
	`, id, defaultString(input.DeviceName, "remote-device"), defaultString(input.Platform, "unknown"),
		role, DeviceTrusted, syncEnabled, permission, publicKey,
		strings.TrimRight(strings.TrimSpace(input.APIBaseURL), "/"), now, DeviceRevoked); err != nil {
		return domain.StewardDevice{}, fmt.Errorf("register steward device: %w", err)
	}
	if err := s.ensureDefaultDevicePermissions(ctx, id, now); err != nil {
		return domain.StewardDevice{}, err
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "device.register",
		TargetType:      "device",
		TargetID:        nil,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   "device registered for private sync",
		ResultStatus:    ResultOK,
	})
	return s.getDevice(ctx, id)
}

// observeSyncPeer refreshes peer-owned descriptive metadata without allowing a
// sync payload or heartbeat to change local trust, enablement, or permission
// decisions. HTTP imports have already authenticated an existing device; the
// insert path exists for direct internal imports and migration tests.
func (s *Service) observeSyncPeer(ctx context.Context, input RegisterDeviceInput) error {
	id := strings.TrimSpace(input.ID)
	if id == "" || id == s.agentIDValue() {
		return nil
	}
	publicKey := strings.TrimSpace(input.PublicKey)
	if publicKey != "" {
		normalized, err := normalizeSyncPublicKey(publicKey)
		if err != nil {
			return err
		}
		publicKey = normalized
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_devices (
			id, device_name, platform, role, trust_status, sync_enabled, permission_level,
			public_key, api_base_url, last_seen_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,true,$6,$7,$8,$9,$9,$9)
		on conflict (id) do update
		set device_name = excluded.device_name,
		    platform = excluded.platform,
		    role = $4,
		    public_key = case when steward_devices.public_key = '' then excluded.public_key else steward_devices.public_key end,
		    api_base_url = case when excluded.api_base_url <> '' then excluded.api_base_url else steward_devices.api_base_url end,
		    last_seen_at = excluded.last_seen_at,
		    updated_at = excluded.updated_at
	`, id, defaultString(input.DeviceName, "remote-device"), defaultString(input.Platform, "unknown"),
		DeviceRolePeer, DeviceTrusted, defaultString(input.PermissionLevel, PermissionA3), publicKey,
		strings.TrimRight(strings.TrimSpace(input.APIBaseURL), "/"), now); err != nil {
		return fmt.Errorf("observe sync peer: %w", err)
	}
	return s.ensureDefaultDevicePermissions(ctx, id, now)
}

func (s *Service) RevokeDevice(ctx context.Context, id string) (domain.StewardDevice, error) {
	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_devices
		set trust_status = $1, sync_enabled = false, revoked_at = $2, updated_at = $2
		where id = $3 and id <> $4
	`, DeviceRevoked, now, id, s.agentIDValue())
	if err != nil {
		return domain.StewardDevice{}, fmt.Errorf("revoke steward device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.StewardDevice{}, fmt.Errorf("device not found or local device cannot be revoked")
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "device.revoke",
		TargetType:      "device",
		TargetID:        nil,
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   "device sync permission revoked",
		ResultStatus:    ResultOK,
	})
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return domain.StewardDevice{}, err
	}
	if err := s.recordDeviceRevocationSyncChange(ctx, device, now); err != nil {
		return domain.StewardDevice{}, err
	}
	return device, nil
}

func (s *Service) recordDeviceRevocationSyncChange(ctx context.Context, device domain.StewardDevice, revokedAt time.Time) error {
	if strings.TrimSpace(device.ID) == "" || device.ID == s.agentIDValue() {
		return nil
	}
	payload := map[string]any{
		"device_id":        device.ID,
		"device_name":      device.DeviceName,
		"platform":         device.Platform,
		"role":             DeviceRolePeer,
		"trust_status":     DeviceRevoked,
		"sync_enabled":     false,
		"permission_level": device.PermissionLevel,
		"public_key":       device.PublicKey,
		"api_base_url":     device.APIBaseURL,
		"revoked_at":       revokedAt,
	}
	if device.LastSeenAt != nil {
		payload["last_seen_at"] = device.LastSeenAt
	}
	_, err := s.CreateSyncChange(ctx, CreateSyncChangeInput{
		EntityType:     EntityDeviceRevoke,
		EntityID:       deviceRevocationSyncEntityID(device.ID),
		Operation:      SyncUpdate,
		OriginDeviceID: s.agentIDValue(),
		Version:        1,
		DataLevel:      DataD2,
		Payload:        payload,
	})
	return err
}

func (s *Service) ListDevicePermissions(ctx context.Context, deviceID string) ([]domain.StewardDevicePermission, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, device_id, capability, policy, max_permission_level, scope_summary, created_at, updated_at
		from steward_device_permissions
		where ($1 = '' or device_id = $1)
		order by device_id, capability
	`, strings.TrimSpace(deviceID))
	if err != nil {
		return nil, fmt.Errorf("list steward device permissions: %w", err)
	}
	defer rows.Close()

	permissions := []domain.StewardDevicePermission{}
	for rows.Next() {
		var item domain.StewardDevicePermission
		if err := rows.Scan(&item.ID, &item.DeviceID, &item.Capability, &item.Policy,
			&item.MaxPermissionLevel, &item.ScopeSummary, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		permissions = append(permissions, item)
	}
	return permissions, rows.Err()
}

func (s *Service) UpdateDevicePermission(ctx context.Context, deviceID string, capability string, input UpdateDevicePermissionInput) (domain.StewardDevicePermission, error) {
	if _, err := s.getDevice(ctx, deviceID); err != nil {
		return domain.StewardDevicePermission{}, err
	}
	capability = strings.TrimSpace(capability)
	if !validDeviceCapability(capability) {
		return domain.StewardDevicePermission{}, fmt.Errorf("unsupported device capability %q", capability)
	}
	now := time.Now().UTC()
	policy := defaultString(input.Policy, "confirm")
	if !validDevicePermissionPolicy(policy) {
		return domain.StewardDevicePermission{}, fmt.Errorf("unsupported device permission policy %q", policy)
	}
	maxPermission := defaultString(input.MaxPermissionLevel, PermissionA3)
	if !validPermissionLevel(maxPermission) {
		return domain.StewardDevicePermission{}, fmt.Errorf("invalid max permission level %q", maxPermission)
	}
	scope := strings.TrimSpace(input.ScopeSummary)
	var item domain.StewardDevicePermission
	if err := s.db.Pool.QueryRow(ctx, `
		insert into steward_device_permissions (
			id, device_id, capability, policy, max_permission_level, scope_summary, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$7)
		on conflict (device_id, capability) do update
		set policy = excluded.policy,
		    max_permission_level = excluded.max_permission_level,
		    scope_summary = excluded.scope_summary,
		    updated_at = excluded.updated_at
		returning id, device_id, capability, policy, max_permission_level, scope_summary, created_at, updated_at
	`, uuid.NewString(), deviceID, capability, policy, maxPermission, scope, now).Scan(
		&item.ID, &item.DeviceID, &item.Capability, &item.Policy, &item.MaxPermissionLevel,
		&item.ScopeSummary, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return domain.StewardDevicePermission{}, fmt.Errorf("update steward device permission: %w", err)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "device.permission.update",
		TargetType:      "device",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    deviceID + ":" + item.Capability,
		OutputSummary:   item.Policy + " up to " + item.MaxPermissionLevel,
		ResultStatus:    ResultOK,
	})
	return item, nil
}

func (s *Service) CreateSyncChange(ctx context.Context, input CreateSyncChangeInput) (domain.StewardSyncChange, error) {
	change, _, err := s.createSyncChange(ctx, input)
	return change, err
}

func (s *Service) createSyncChange(ctx context.Context, input CreateSyncChangeInput) (domain.StewardSyncChange, bool, error) {
	if strings.TrimSpace(input.ID) != "" {
		existing, err := s.getSyncChange(ctx, strings.TrimSpace(input.ID))
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardSyncChange{}, false, err
		}
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.Version <= 0 {
		input.Version = 1
	}
	origin := defaultString(input.OriginDeviceID, s.agentIDValue())
	id := defaultString(input.ID, uuid.NewString())
	operation := normalizeOperation(input.Operation)
	dataLevel := defaultString(input.DataLevel, DataD0)
	if _, err := s.getDevice(ctx, origin); err != nil {
		if _, registerErr := s.RegisterDevice(ctx, RegisterDeviceInput{ID: origin, DeviceName: origin, Platform: "unknown"}); registerErr != nil {
			return domain.StewardSyncChange{}, false, registerErr
		}
	}
	plainPayload, err := json.Marshal(input.Payload)
	if err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("marshal sync payload: %w", err)
	}
	hash := hashBytes(plainPayload)
	input.ID = id
	input.Operation = operation
	input.OriginDeviceID = origin
	input.DataLevel = dataLevel
	storagePayload, err := prepareSyncPayloadForStorage(input)
	if err != nil {
		return domain.StewardSyncChange{}, false, err
	}
	payload, err := json.Marshal(storagePayload)
	if err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("marshal sync payload for storage: %w", err)
	}
	var change domain.StewardSyncChange
	if err := s.db.Pool.QueryRow(ctx, `
		insert into steward_sync_changes (
			id, entity_type, entity_id, operation, origin_device_id, version, data_level,
			payload, payload_hash, sync_status, created_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11)
		returning id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		          payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
	`, id, input.EntityType, input.EntityID, operation, origin, input.Version,
		dataLevel, string(payload), hash, SyncPending, time.Now().UTC()).Scan(
		&change.ID, &change.Sequence, &change.EntityType, &change.EntityID, &change.Operation,
		&change.OriginDeviceID, &change.Version, &change.DataLevel, newPayloadScanner(&change.Payload),
		&change.PayloadHash, &change.SyncStatus, &change.ErrorSummary, &change.CreatedAt, &change.AppliedAt,
	); err != nil {
		return domain.StewardSyncChange{}, false, fmt.Errorf("create steward sync change: %w", err)
	}
	if change.Payload, err = decryptStoredSyncPayload(change); err != nil {
		return domain.StewardSyncChange{}, false, err
	}
	return change, true, nil
}

func (s *Service) ListSyncChanges(ctx context.Context, sinceSequence int64, limit int) ([]domain.StewardSyncChange, error) {
	return s.listSyncChanges(ctx, sinceSequence, limit, syncChangeListReplay)
}

func (s *Service) ListRecentSyncChanges(ctx context.Context, limit int) ([]domain.StewardSyncChange, error) {
	return s.listSyncChanges(ctx, 0, limit, syncChangeListRecent)
}

func (s *Service) listSyncChanges(ctx context.Context, sinceSequence int64, limit int, mode syncChangeListMode) ([]domain.StewardSyncChange, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, syncChangeListQuery(mode), sinceSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward sync changes: %w", err)
	}
	defer rows.Close()

	changes := []domain.StewardSyncChange{}
	for rows.Next() {
		change, err := scanSyncChange(rows)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

func syncChangeListQuery(mode syncChangeListMode) string {
	return fmt.Sprintf(`
		select id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		       payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
		from steward_sync_changes
		where sequence > $1
		order by sequence %s
		limit $2
	`, syncChangeListOrder(mode))
}

func syncChangeListOrder(mode syncChangeListMode) string {
	if mode == syncChangeListRecent {
		return "desc"
	}
	return "asc"
}

func (s *Service) ImportSyncChanges(ctx context.Context, input ImportSyncChangesInput) (ImportSyncChangesResult, error) {
	preparedInput, err := s.PrepareImportSyncChanges(ctx, input)
	if err != nil {
		return ImportSyncChangesResult{}, err
	}
	input = preparedInput
	if strings.TrimSpace(input.Device.ID) != "" && strings.TrimSpace(input.Device.ID) != s.agentIDValue() {
		if err := s.observeSyncPeer(ctx, input.Device); err != nil {
			return ImportSyncChangesResult{}, err
		}
	}
	result := ImportSyncChangesResult{
		Conflicts: []domain.StewardSyncConflict{},
		Changes:   []domain.StewardSyncChange{},
	}
	for _, item := range input.Changes {
		if strings.TrimSpace(input.Device.ID) != "" && strings.TrimSpace(input.Device.ID) != s.agentIDValue() {
			if err := s.authorizeDeviceSyncChange(ctx, input.Device.ID, item.EntityType, item.Payload); err != nil {
				if errors.Is(err, ErrSyncPermissionDenied) {
					result.Denied++
					result.Skipped++
					s.recordSyncPermissionDenied(ctx, input.Device.ID, item, err)
					continue
				}
				return ImportSyncChangesResult{}, err
			}
		}
		change, created, err := s.createSyncChange(ctx, item)
		if err != nil {
			return result, err
		}
		if created {
			result.Imported++
		} else {
			result.Skipped++
			if change.SyncStatus != SyncPending {
				result.Changes = append(result.Changes, change)
				continue
			}
		}
		applied, conflict, err := s.applySyncChange(ctx, change)
		if err != nil {
			return result, err
		}
		if applied {
			result.Applied++
			updated, _ := s.getSyncChange(ctx, change.ID)
			result.Changes = append(result.Changes, updated)
		} else {
			result.Changes = append(result.Changes, change)
		}
		if conflict.ID != "" {
			result.Conflicts = append(result.Conflicts, conflict)
		}
	}
	return result, nil
}

func (s *Service) SyncDevice(ctx context.Context, id string) (SyncDeviceResult, error) {
	return s.syncDevice(ctx, id, "manual")
}

func (s *Service) ProbeLocalSyncEntity(ctx context.Context, input SyncEntityProbeInput) (SyncEntityProbeResult, error) {
	input.EntityType = strings.TrimSpace(input.EntityType)
	input.EntityID = strings.TrimSpace(input.EntityID)
	if input.EntityType == "" || input.EntityID == "" {
		return SyncEntityProbeResult{}, fmt.Errorf("entity_type and entity_id are required")
	}

	result := SyncEntityProbeResult{
		DeviceID:   s.agentIDValue(),
		EntityType: input.EntityType,
		EntityID:   input.EntityID,
	}
	switch input.EntityType {
	case EntityTask:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_tasks where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local task: %w", err)
		}
	case EntityEvent:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_events where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local event: %w", err)
		}
	case EntityIntent:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_intents where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local intent: %w", err)
		}
	case EntityMemory:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_memories where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local memory: %w", err)
		}
	case EntityKnowledgeItem:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_knowledge_items where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local knowledge item: %w", err)
		}
	case EntitySourceRef:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_source_refs where id = $1)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local source ref: %w", err)
		}
	case EntityDataTag:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_data_tags where id = $1)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local data tag: %w", err)
		}
	case EntityEntityTag:
		exists, detail, err := s.probeLocalEntityTag(ctx, input.EntityID)
		if err != nil {
			return SyncEntityProbeResult{}, err
		}
		result.Exists = exists
		result.Detail = detail
	case EntityTimeline:
		var linkedEvents int
		if err := s.db.Pool.QueryRow(ctx, `
			select exists(select 1 from steward_timeline_segments where id = $1 and deleted_at is null),
			       (select count(*) from steward_timeline_segment_events where segment_id = $1)
		`, input.EntityID).Scan(&result.Exists, &linkedEvents); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local timeline segment: %w", err)
		}
		result.Detail = map[string]any{"linked_event_count": linkedEvents}
	default:
		return SyncEntityProbeResult{}, fmt.Errorf("unsupported sync probe entity_type %q", input.EntityType)
	}
	return result, nil
}

func (s *Service) probeLocalEntityTag(ctx context.Context, entityTagID string) (bool, any, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select et.entity_type, et.entity_id::text, et.tag_id::text, coalesce(alias.alias_id::text, '')
		from steward_entity_tags et
		left join steward_data_tag_aliases alias on alias.tag_id = et.tag_id
	`)
	if err != nil {
		return false, nil, fmt.Errorf("probe local entity tag: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entityType string
		var entityID string
		var tagID string
		var aliasID string
		if err := rows.Scan(&entityType, &entityID, &tagID, &aliasID); err != nil {
			return false, nil, err
		}
		if entityTagSyncEntityID(entityType, entityID, tagID) == entityTagID ||
			(aliasID != "" && entityTagSyncEntityID(entityType, entityID, aliasID) == entityTagID) {
			return true, map[string]any{
				"entity_type": entityType,
				"entity_id":   entityID,
				"tag_id":      tagID,
			}, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	return false, nil, nil
}

func (s *Service) ProbeDeviceSyncEntity(ctx context.Context, id string, input SyncEntityProbeInput) (SyncEntityProbeResult, error) {
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return SyncEntityProbeResult{}, err
	}
	if device.ID == s.agentIDValue() {
		return s.ProbeLocalSyncEntity(ctx, input)
	}
	if device.TrustStatus == DeviceRevoked || !device.SyncEnabled {
		return SyncEntityProbeResult{}, fmt.Errorf("device is revoked or sync is disabled")
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return SyncEntityProbeResult{}, fmt.Errorf("device api_base_url is required before peer probe")
	}

	query := url.Values{
		"entity_type": []string{strings.TrimSpace(input.EntityType)},
		"entity_id":   []string{strings.TrimSpace(input.EntityID)},
	}
	endpoint, err := stewardAPIEndpoint(device.APIBaseURL, "/steward/sync/probe", query)
	if err != nil {
		return SyncEntityProbeResult{}, err
	}
	var response struct {
		Probe SyncEntityProbeResult `json:"probe"`
	}
	client := &http.Client{Timeout: 12 * time.Second}
	if err := requestPeerJSON(ctx, client, http.MethodGet, endpoint, nil, &response, s.syncAuth()); err != nil {
		return SyncEntityProbeResult{}, err
	}
	if response.Probe.DeviceID != device.ID || response.Probe.EntityType != strings.TrimSpace(input.EntityType) || response.Probe.EntityID != strings.TrimSpace(input.EntityID) {
		return SyncEntityProbeResult{}, fmt.Errorf("peer probe response identity mismatch")
	}
	return response.Probe, nil
}

func (s *Service) SyncTrustedPeerDevices(ctx context.Context) ([]SyncDeviceResult, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	results := []SyncDeviceResult{}
	failures := []string{}
	for _, device := range devices {
		if device.ID == s.agentIDValue() ||
			device.Role != DeviceRolePeer ||
			device.TrustStatus == DeviceRevoked ||
			!device.SyncEnabled ||
			strings.TrimSpace(device.APIBaseURL) == "" {
			continue
		}
		result, err := s.syncDevice(ctx, device.ID, "daemon")
		results = append(results, result)
		if err != nil {
			failures = append(failures, device.ID+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return results, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}

func (s *Service) syncDevice(ctx context.Context, id string, triggerSource string) (result SyncDeviceResult, err error) {
	defer func() {
		s.recordSyncDeviceAudit(ctx, id, triggerSource, result, err)
	}()
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return SyncDeviceResult{}, err
	}
	result = SyncDeviceResult{
		Device:             device,
		RemoteLastSequence: device.LastSyncSequence,
		LocalSentSequence:  device.LastSentSequence,
		Conflicts:          []domain.StewardSyncConflict{},
		Errors:             []string{},
	}
	if device.ID == s.agentIDValue() {
		return result, fmt.Errorf("local device cannot be synced as a peer")
	}
	if device.TrustStatus == DeviceRevoked || !device.SyncEnabled {
		return result, fmt.Errorf("device is revoked or sync is disabled")
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return result, fmt.Errorf("device api_base_url is required before peer sync")
	}

	client := &http.Client{Timeout: 12 * time.Second}
	auth := s.syncAuth()
	if err := s.pullPeerSyncWindows(ctx, client, device, auth, &result); err != nil {
		_ = s.updateDeviceSyncProgress(ctx, device.ID, result.RemoteLastSequence, result.LocalSentSequence, err.Error(), false)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	s.pushPeerSyncWindows(ctx, client, device, auth, &result)

	errorSummary := ""
	if len(result.Errors) > 0 {
		errorSummary = strings.Join(result.Errors, "; ")
	}
	if err := s.updateDeviceSyncProgress(ctx, device.ID, result.RemoteLastSequence, result.LocalSentSequence, errorSummary, true); err != nil {
		return result, err
	}
	result.Device, _ = s.getDevice(ctx, device.ID)
	return result, nil
}

func (s *Service) pullPeerSyncWindows(ctx context.Context, client *http.Client, device domain.StewardDevice, auth syncAuth, result *SyncDeviceResult) error {
	for window := 0; window < syncMaxWindowsPerRun; window++ {
		remoteWindow, err := getPeerSyncChanges(ctx, client, device.APIBaseURL, result.RemoteLastSequence, auth)
		if err != nil {
			return err
		}
		result.Pulled += len(remoteWindow.Changes)
		if len(remoteWindow.Changes) == 0 && remoteWindow.NextSequence == result.RemoteLastSequence {
			return nil
		}

		pullWindow := buildPullSyncWindow(s.agentIDValue(), device, remoteWindow.Changes, result.RemoteLastSequence)
		if len(pullWindow.Input.Changes) > 0 {
			imported, err := s.ImportSyncChanges(ctx, pullWindow.Input)
			if err != nil {
				return err
			}
			result.Imported += imported.Imported
			result.Applied += imported.Applied
			result.Skipped += imported.Skipped
			result.Denied += imported.Denied
			result.Conflicts = append(result.Conflicts, imported.Conflicts...)
		}
		result.RemoteLastSequence = max(pullWindow.RemoteLastSequence, remoteWindow.NextSequence)
		result.Skipped += pullWindow.Skipped
		if !remoteWindow.HasMore {
			return nil
		}
	}
	return nil
}

func (s *Service) pushPeerSyncWindows(ctx context.Context, client *http.Client, device domain.StewardDevice, auth syncAuth, result *SyncDeviceResult) {
	localDevice := s.localDeviceRegistration(ctx)
	posted := false
	defer func() {
		if posted || len(result.Errors) > 0 {
			return
		}
		heartbeat := ImportSyncChangesInput{Device: localDevice, Changes: []CreateSyncChangeInput{}}
		if _, err := postPeerSyncChanges(ctx, client, device.APIBaseURL, heartbeat, auth); err != nil {
			result.Errors = append(result.Errors, "send device heartbeat: "+err.Error())
		}
	}()
	for window := 0; window < syncMaxWindowsPerRun; window++ {
		localChanges, err := s.ListSyncChanges(ctx, result.LocalSentSequence, syncChangeWindowLimit)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			return
		}
		if len(localChanges) == 0 {
			return
		}

		pushWindow := buildPushSyncWindow(localDevice, device.ID, localChanges, result.LocalSentSequence)
		if len(pushWindow.Input.Changes) > 0 {
			imported, err := postPeerSyncChanges(ctx, client, device.APIBaseURL, pushWindow.Input, auth)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
				return
			}
			posted = true
			result.Pushed += len(pushWindow.Input.Changes)
			result.Denied += imported.Denied
		}
		result.LocalSentSequence = pushWindow.LocalSentSequence
		if len(localChanges) < syncChangeWindowLimit {
			return
		}
	}
}

func (s *Service) recordSyncDeviceAudit(ctx context.Context, deviceID string, triggerSource string, result SyncDeviceResult, syncErr error) {
	if s == nil || s.db == nil {
		return
	}
	status := ResultOK
	var errorSummary *string
	output := fmt.Sprintf("pulled=%d imported=%d applied=%d skipped=%d pushed=%d denied=%d conflicts=%d",
		result.Pulled, result.Imported, result.Applied, result.Skipped, result.Pushed, result.Denied, len(result.Conflicts))
	if syncErr != nil {
		status = "failed"
		value := syncErr.Error()
		errorSummary = &value
	}
	if len(result.Errors) > 0 {
		status = "failed"
		value := strings.Join(result.Errors, "; ")
		errorSummary = &value
	}
	userConfirmed := triggerSource != "daemon"
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "sync",
		Action:          "sync.device",
		TargetType:      "device",
		Source:          defaultString(triggerSource, "manual"),
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    deviceID,
		OutputSummary:   output,
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    status,
		ErrorSummary:    errorSummary,
	})
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

func (s *Service) ListSyncConflicts(ctx context.Context, status string, limit int) ([]domain.StewardSyncConflict, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, entity_type, entity_id::text,
		       coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		       reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
		from steward_sync_conflicts
		where ($1 = '' or status = $1)
		order by updated_at desc
		limit $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("list steward sync conflicts: %w", err)
	}
	defer rows.Close()

	conflicts := []domain.StewardSyncConflict{}
	for rows.Next() {
		conflict, err := scanSyncConflict(rows)
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, conflict)
	}
	return conflicts, rows.Err()
}

func (s *Service) ResolveSyncConflict(ctx context.Context, id string, input ResolveSyncConflictInput) (domain.StewardSyncConflict, error) {
	now := time.Now().UTC()
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_sync_conflicts
		set status = $1, resolution = $2, resolved_at = $3, updated_at = $3
		where id = $4 and status = $5
	`, StatusResolved, defaultString(input.Resolution, "manual resolution"), now, id, StatusOpen)
	if err != nil {
		return domain.StewardSyncConflict{}, fmt.Errorf("resolve sync conflict: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.StewardSyncConflict{}, fmt.Errorf("sync conflict not found")
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "user",
		Action:          "sync.conflict.resolve",
		TargetType:      "sync_conflict",
		Source:          "manual",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    id,
		OutputSummary:   defaultString(input.Resolution, "manual resolution"),
		ResultStatus:    ResultOK,
	})
	return s.getSyncConflict(ctx, id)
}

func (s *Service) applySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if change.EntityType != EntityTask {
		return s.applyS2EntitySyncChange(ctx, change)
	}
	if conflict, ok, err := s.checkIncomingEntityVersion(ctx, change); err != nil || ok {
		return false, conflict, err
	}

	if change.Operation == SyncDelete {
		now := time.Now().UTC()
		_, err := s.db.Pool.Exec(ctx, `
			update steward_tasks
			set status = $1, deleted_at = $2, updated_at = $2, version = greatest(version, $3)
			where id = $4
		`, StatusDeleted, now, change.Version, change.EntityID)
		if err != nil {
			msg := err.Error()
			_ = s.markSyncChange(ctx, change.ID, SyncPending, &msg, false)
			return false, domain.StewardSyncConflict{}, fmt.Errorf("apply task delete sync change: %w", err)
		}
		if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
			return false, domain.StewardSyncConflict{}, err
		}
		return true, domain.StewardSyncConflict{}, nil
	}

	now := time.Now().UTC()
	createdAt := timePayload(change.Payload, "created_at", now)
	updatedAt := timePayload(change.Payload, "updated_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_tasks (
			id, type, title, description, status, priority, due_at, source, data_level, permission_level,
			device_id, risk_level, user_confirmed, version, created_at, updated_at, completed_at, canceled_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		on conflict (id) do update
		set type = excluded.type,
		    title = excluded.title,
		    description = excluded.description,
		    status = excluded.status,
		    priority = excluded.priority,
		    due_at = excluded.due_at,
		    source = excluded.source,
		    data_level = excluded.data_level,
		    permission_level = excluded.permission_level,
		    device_id = excluded.device_id,
		    risk_level = excluded.risk_level,
		    user_confirmed = excluded.user_confirmed,
		    version = greatest(steward_tasks.version, excluded.version),
		    completed_at = excluded.completed_at,
		    canceled_at = excluded.canceled_at,
		    deleted_at = null,
		    updated_at = excluded.updated_at
	`, change.EntityID, stringPayload(change.Payload, "type", "remote"),
		stringPayload(change.Payload, "title", "同步任务"),
		stringPayload(change.Payload, "description", stringPayload(change.Payload, "summary", "")),
		stringPayload(change.Payload, "status", StatusOpen),
		stringPayload(change.Payload, "priority", "normal"),
		nullableTimePayload(change.Payload, "due_at"),
		"sync", defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "permission_level", PermissionA3),
		change.OriginDeviceID,
		stringPayload(change.Payload, "risk_level", "low"),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version, createdAt, updatedAt,
		nullableTimePayload(change.Payload, "completed_at"),
		nullableTimePayload(change.Payload, "canceled_at"))
	if err != nil {
		msg := err.Error()
		_ = s.markSyncChange(ctx, change.ID, SyncPending, &msg, false)
		return false, domain.StewardSyncConflict{}, fmt.Errorf("apply task sync change: %w", err)
	}
	if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
		return false, domain.StewardSyncConflict{}, err
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "sync",
		Action:          "sync.change.apply",
		TargetType:      change.EntityType,
		TargetID:        &change.EntityID,
		Source:          change.OriginDeviceID,
		PermissionLevel: PermissionA3,
		DataLevel:       change.DataLevel,
		InputSummary:    change.Operation + " " + change.EntityID,
		OutputSummary:   "sync change applied",
		ResultStatus:    ResultOK,
	})
	return true, domain.StewardSyncConflict{}, nil
}

func (s *Service) recordTaskSyncChange(ctx context.Context, task domain.StewardTask, operation string) error {
	if task.ID == "" {
		return nil
	}
	payload := map[string]any{
		"type":             task.Type,
		"title":            task.Title,
		"description":      task.Description,
		"status":           task.Status,
		"priority":         task.Priority,
		"source":           task.Source,
		"data_level":       task.DataLevel,
		"permission_level": task.PermissionLevel,
		"risk_level":       task.RiskLevel,
		"user_confirmed":   task.UserConfirmed,
		"version":          task.Version,
		"updated_at":       task.UpdatedAt,
	}
	if task.DueAt != nil {
		payload["due_at"] = task.DueAt
	}
	if task.CompletedAt != nil {
		payload["completed_at"] = task.CompletedAt
	}
	if task.CanceledAt != nil {
		payload["canceled_at"] = task.CanceledAt
	}
	if task.DeletedAt != nil {
		payload["deleted_at"] = task.DeletedAt
	}
	_, err := s.CreateSyncChange(ctx, CreateSyncChangeInput{
		EntityType:     "task",
		EntityID:       task.ID,
		Operation:      operation,
		OriginDeviceID: s.agentIDValue(),
		Version:        task.Version,
		DataLevel:      task.DataLevel,
		Payload:        payload,
	})
	return err
}

func (s *Service) localDeviceRegistration(ctx context.Context) RegisterDeviceInput {
	device, err := s.getDevice(ctx, s.agentIDValue())
	if err != nil {
		return RegisterDeviceInput{
			ID:              s.agentIDValue(),
			DeviceName:      s.agentIDValue(),
			Platform:        "unknown",
			Role:            DeviceRolePeer,
			SyncEnabled:     boolPtr(true),
			PermissionLevel: PermissionA3,
			PublicKey:       syncDevicePublicKeyFromEnv(),
			APIBaseURL:      strings.TrimRight(strings.TrimSpace(envOrDefault("STEWARD_PUBLIC_API_BASE", "")), "/"),
		}
	}
	return RegisterDeviceInput{
		ID:              device.ID,
		DeviceName:      device.DeviceName,
		Platform:        device.Platform,
		Role:            DeviceRolePeer,
		SyncEnabled:     boolPtr(device.SyncEnabled),
		PermissionLevel: device.PermissionLevel,
		PublicKey:       device.PublicKey,
		APIBaseURL:      strings.TrimRight(strings.TrimSpace(envOrDefault("STEWARD_PUBLIC_API_BASE", "")), "/"),
	}
}

func (s *Service) updateDeviceSyncProgress(ctx context.Context, id string, lastSequence int64, lastSentSequence int64, errorSummary string, contacted bool) error {
	now := time.Now().UTC()
	var errValue *string
	if strings.TrimSpace(errorSummary) != "" {
		value := strings.TrimSpace(errorSummary)
		errValue = &value
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_devices
		set last_sync_sequence = greatest(last_sync_sequence, $1),
		    last_sent_sequence = greatest(last_sent_sequence, $2),
		    last_sync_at = $3,
		    last_sync_error = $4,
		    last_seen_at = case when $5 then $3 else last_seen_at end,
		    updated_at = $3
		where id = $6
	`, lastSequence, lastSentSequence, now, errValue, contacted, id); err != nil {
		return fmt.Errorf("update device sync progress: %w", err)
	}
	return nil
}

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

func (s *Service) createSyncConflict(ctx context.Context, change domain.StewardSyncChange, reason string) (domain.StewardSyncConflict, error) {
	now := time.Now().UTC()
	row := s.db.Pool.QueryRow(ctx, `
		insert into steward_sync_conflicts (
			id, entity_type, entity_id, remote_change_id, reason, status, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$7)
		returning id, entity_type, entity_id::text,
		          coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		          reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
	`, uuid.NewString(), change.EntityType, change.EntityID, change.ID, reason, StatusOpen, now)
	conflict, err := scanSyncConflict(row)
	if err != nil {
		return domain.StewardSyncConflict{}, fmt.Errorf("create sync conflict: %w", err)
	}
	return conflict, nil
}

func (s *Service) markSyncChange(ctx context.Context, id string, status string, errorSummary *string, applied bool) error {
	var appliedAt *time.Time
	if applied {
		now := time.Now().UTC()
		appliedAt = &now
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_sync_changes
		set sync_status = $1, error_summary = $2, applied_at = $3
		where id = $4
	`, status, errorSummary, appliedAt, id); err != nil {
		return fmt.Errorf("mark sync change: %w", err)
	}
	return nil
}

func (s *Service) getDevice(ctx context.Context, id string) (domain.StewardDevice, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, device_name, platform, role, trust_status, sync_enabled, permission_level,
		       public_key, api_base_url, last_sync_sequence, last_sent_sequence, last_seen_at, last_sync_at, last_sync_error,
		       revoked_at, created_at, updated_at
		from steward_devices
		where id = $1
	`, id)
	device, err := scanDevice(row)
	if err != nil {
		return domain.StewardDevice{}, fmt.Errorf("get steward device: %w", err)
	}
	return device, nil
}

func (s *Service) getSyncChange(ctx context.Context, id string) (domain.StewardSyncChange, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		       payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
		from steward_sync_changes
		where id = $1
	`, id)
	return scanSyncChange(row)
}

func (s *Service) getSyncConflict(ctx context.Context, id string) (domain.StewardSyncConflict, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id, entity_type, entity_id::text,
		       coalesce(local_change_id::text, ''), coalesce(remote_change_id::text, ''),
		       reason, status, coalesce(resolution, ''), created_at, updated_at, resolved_at
		from steward_sync_conflicts
		where id = $1
	`, id)
	return scanSyncConflict(row)
}

func scanDevice(row scanner) (domain.StewardDevice, error) {
	var device domain.StewardDevice
	err := row.Scan(&device.ID, &device.DeviceName, &device.Platform, &device.Role,
		&device.TrustStatus, &device.SyncEnabled, &device.PermissionLevel, &device.PublicKey,
		&device.APIBaseURL, &device.LastSyncSequence, &device.LastSentSequence, &device.LastSeenAt, &device.LastSyncAt,
		&device.LastSyncError, &device.RevokedAt, &device.CreatedAt, &device.UpdatedAt)
	return device, err
}

func scanSyncChange(row scanner) (domain.StewardSyncChange, error) {
	var change domain.StewardSyncChange
	err := row.Scan(&change.ID, &change.Sequence, &change.EntityType, &change.EntityID,
		&change.Operation, &change.OriginDeviceID, &change.Version, &change.DataLevel,
		newPayloadScanner(&change.Payload), &change.PayloadHash, &change.SyncStatus,
		&change.ErrorSummary, &change.CreatedAt, &change.AppliedAt)
	if err != nil {
		return change, err
	}
	change.Payload, err = decryptStoredSyncPayload(change)
	return change, err
}

func scanSyncConflict(row scanner) (domain.StewardSyncConflict, error) {
	var conflict domain.StewardSyncConflict
	var localChangeID string
	var remoteChangeID string
	err := row.Scan(&conflict.ID, &conflict.EntityType, &conflict.EntityID, &localChangeID,
		&remoteChangeID, &conflict.Reason, &conflict.Status, &conflict.Resolution,
		&conflict.CreatedAt, &conflict.UpdatedAt, &conflict.ResolvedAt)
	conflict.LocalChangeID = stringPtrIfPresent(localChangeID)
	conflict.RemoteChangeID = stringPtrIfPresent(remoteChangeID)
	return conflict, err
}

type payloadScanner struct {
	value *map[string]any
}

func newPayloadScanner(value *map[string]any) *payloadScanner {
	return &payloadScanner{value: value}
}

func (p *payloadScanner) Scan(src any) error {
	if p.value == nil {
		return nil
	}
	switch value := src.(type) {
	case string:
		return json.Unmarshal([]byte(value), p.value)
	case []byte:
		return json.Unmarshal(value, p.value)
	default:
		*p.value = map[string]any{}
		return nil
	}
}

func normalizeOperation(value string) string {
	switch strings.TrimSpace(value) {
	case SyncCreate, SyncUpdate, SyncDelete:
		return strings.TrimSpace(value)
	default:
		return SyncUpdate
	}
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func stringPayload(payload map[string]any, key string, fallback string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return fallback
	}
	text, ok := value.(string)
	if !ok {
		return fallback
	}
	return defaultString(text, fallback)
}

func stringPtrIfPresent(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
