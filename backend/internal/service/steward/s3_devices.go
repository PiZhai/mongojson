package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

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

func scanDevice(row scanner) (domain.StewardDevice, error) {
	var device domain.StewardDevice
	err := row.Scan(&device.ID, &device.DeviceName, &device.Platform, &device.Role,
		&device.TrustStatus, &device.SyncEnabled, &device.PermissionLevel, &device.PublicKey,
		&device.APIBaseURL, &device.LastSyncSequence, &device.LastSentSequence, &device.LastSeenAt, &device.LastSyncAt,
		&device.LastSyncError, &device.RevokedAt, &device.CreatedAt, &device.UpdatedAt)
	return device, err
}
