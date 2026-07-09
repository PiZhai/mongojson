package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func deviceCapabilitySyncEntityID(deviceID string, capability string) string {
	key := strings.Join([]string{"steward-device-capability", strings.TrimSpace(deviceID), strings.TrimSpace(capability)}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func deviceCapabilitySyncChangeID(deviceID string, capability string, version int) string {
	key := strings.Join([]string{
		"steward-device-capability-change",
		strings.TrimSpace(deviceID),
		strings.TrimSpace(capability),
		fmt.Sprintf("%d", version),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func (s *Service) ensureLocalDeviceCapabilities(ctx context.Context, now time.Time) error {
	deviceID := s.agentIDValue()
	for _, action := range s.autonomyActionCapabilities() {
		capability := domain.StewardDeviceCapability{
			DeviceID:           deviceID,
			Capability:         strings.TrimSpace(action.Action),
			Description:        strings.TrimSpace(action.Description),
			TargetType:         strings.TrimSpace(action.TargetType),
			RiskLevel:          defaultString(action.RiskLevel, "low"),
			MaxPermissionLevel: defaultString(action.MaxPermissionLevel, PermissionA3),
			UpdatedAt:          now,
		}
		if capability.Capability == "" {
			continue
		}
		if err := s.upsertLocalDeviceCapability(ctx, &capability); err != nil {
			return err
		}
		if err := s.recordDeviceCapabilitySyncChange(ctx, capability); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) upsertLocalDeviceCapability(ctx context.Context, capability *domain.StewardDeviceCapability) error {
	if capability == nil {
		return nil
	}
	if err := s.db.Pool.QueryRow(ctx, `
		insert into steward_device_capabilities (
			device_id, capability, description, target_type, risk_level, max_permission_level, version, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,1,$7)
		on conflict (device_id, capability) do update
		set description = excluded.description,
		    target_type = excluded.target_type,
		    risk_level = excluded.risk_level,
		    max_permission_level = excluded.max_permission_level,
		    version = case when (
		        steward_device_capabilities.description,
		        steward_device_capabilities.target_type,
		        steward_device_capabilities.risk_level,
		        steward_device_capabilities.max_permission_level
		    ) is distinct from (
		        excluded.description,
		        excluded.target_type,
		        excluded.risk_level,
		        excluded.max_permission_level
		    ) then steward_device_capabilities.version + 1 else steward_device_capabilities.version end,
		    updated_at = case when (
		        steward_device_capabilities.description,
		        steward_device_capabilities.target_type,
		        steward_device_capabilities.risk_level,
		        steward_device_capabilities.max_permission_level
		    ) is distinct from (
		        excluded.description,
		        excluded.target_type,
		        excluded.risk_level,
		        excluded.max_permission_level
		    ) then excluded.updated_at else steward_device_capabilities.updated_at end
		returning device_id, capability, description, target_type, risk_level, max_permission_level, version, updated_at
	`, capability.DeviceID, capability.Capability, capability.Description, capability.TargetType,
		capability.RiskLevel, capability.MaxPermissionLevel, capability.UpdatedAt).Scan(
		&capability.DeviceID, &capability.Capability, &capability.Description, &capability.TargetType,
		&capability.RiskLevel, &capability.MaxPermissionLevel, &capability.Version, &capability.UpdatedAt,
	); err != nil {
		return fmt.Errorf("publish local device capability %s: %w", capability.Capability, err)
	}
	return nil
}

func (s *Service) recordDeviceCapabilitySyncChange(ctx context.Context, capability domain.StewardDeviceCapability) error {
	payload := map[string]any{
		"device_id":            capability.DeviceID,
		"capability":           capability.Capability,
		"description":          capability.Description,
		"target_type":          capability.TargetType,
		"risk_level":           capability.RiskLevel,
		"max_permission_level": capability.MaxPermissionLevel,
		"version":              capability.Version,
		"updated_at":           capability.UpdatedAt,
	}
	_, err := s.CreateSyncChange(ctx, CreateSyncChangeInput{
		ID:             deviceCapabilitySyncChangeID(capability.DeviceID, capability.Capability, capability.Version),
		EntityType:     EntityDeviceCapability,
		EntityID:       deviceCapabilitySyncEntityID(capability.DeviceID, capability.Capability),
		Operation:      SyncUpdate,
		OriginDeviceID: capability.DeviceID,
		Version:        capability.Version,
		DataLevel:      DataD0,
		Payload:        payload,
	})
	return err
}

func (s *Service) ListDeviceCapabilities(ctx context.Context, deviceID string) ([]domain.StewardDeviceCapability, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select device_id, capability, description, target_type, risk_level, max_permission_level, version, updated_at
		from steward_device_capabilities
		where ($1 = '' or device_id = $1)
		order by device_id, capability
	`, strings.TrimSpace(deviceID))
	if err != nil {
		return nil, fmt.Errorf("list steward device capabilities: %w", err)
	}
	defer rows.Close()

	capabilities := []domain.StewardDeviceCapability{}
	for rows.Next() {
		var item domain.StewardDeviceCapability
		if err := rows.Scan(
			&item.DeviceID, &item.Capability, &item.Description, &item.TargetType,
			&item.RiskLevel, &item.MaxPermissionLevel, &item.Version, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		capabilities = append(capabilities, item)
	}
	return capabilities, rows.Err()
}

func (s *Service) applyDeviceCapabilitySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if change.Operation == SyncDelete {
		return s.markApplyError(ctx, change, "apply device capability sync change", fmt.Errorf("device capability deletion is not supported"))
	}
	capabilityName := strings.TrimSpace(stringPayload(change.Payload, "capability", stringPayload(change.Payload, "action", "")))
	if capabilityName == "" {
		return s.markApplyError(ctx, change, "apply device capability sync change", fmt.Errorf("capability is required"))
	}
	expectedEntityID := deviceCapabilitySyncEntityID(change.OriginDeviceID, capabilityName)
	if change.EntityID != expectedEntityID {
		return s.markApplyError(ctx, change, "apply device capability sync change", fmt.Errorf("entity_id does not match origin device capability"))
	}

	incoming := domain.StewardDeviceCapability{
		DeviceID:           change.OriginDeviceID,
		Capability:         capabilityName,
		Description:        strings.TrimSpace(stringPayload(change.Payload, "description", "")),
		TargetType:         strings.TrimSpace(stringPayload(change.Payload, "target_type", "")),
		RiskLevel:          defaultString(stringPayload(change.Payload, "risk_level", ""), "low"),
		MaxPermissionLevel: defaultString(stringPayload(change.Payload, "max_permission_level", ""), PermissionA3),
		Version:            change.Version,
		UpdatedAt:          timePayload(change.Payload, "updated_at", change.CreatedAt),
	}

	local, err := s.getDeviceCapability(ctx, incoming.DeviceID, incoming.Capability)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s.markApplyError(ctx, change, "load device capability before sync apply", err)
	}
	if err == nil {
		if local.Version > incoming.Version {
			return s.deviceCapabilityConflict(ctx, change, "incoming device capability version is older than local declaration")
		}
		if local.Version == incoming.Version {
			if sameDeviceCapability(local, incoming) {
				return s.finishAppliedSyncChange(ctx, change)
			}
			return s.deviceCapabilityConflict(ctx, change, "incoming device capability has the same version but different content")
		}
	}

	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_device_capabilities (
			device_id, capability, description, target_type, risk_level, max_permission_level, version, updated_at
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		on conflict (device_id, capability) do update
		set description = excluded.description,
		    target_type = excluded.target_type,
		    risk_level = excluded.risk_level,
		    max_permission_level = excluded.max_permission_level,
		    version = excluded.version,
		    updated_at = excluded.updated_at
	`, incoming.DeviceID, incoming.Capability, incoming.Description, incoming.TargetType,
		incoming.RiskLevel, incoming.MaxPermissionLevel, incoming.Version, incoming.UpdatedAt)
	if err != nil {
		return s.markApplyError(ctx, change, "apply device capability sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) deviceCapabilityConflict(ctx context.Context, change domain.StewardSyncChange, reason string) (bool, domain.StewardSyncConflict, error) {
	conflict, _, err := s.registerIncomingSyncConflict(ctx, change, reason)
	return false, conflict, err
}

func (s *Service) getDeviceCapability(ctx context.Context, deviceID string, capability string) (domain.StewardDeviceCapability, error) {
	var item domain.StewardDeviceCapability
	err := s.db.Pool.QueryRow(ctx, `
		select device_id, capability, description, target_type, risk_level, max_permission_level, version, updated_at
		from steward_device_capabilities
		where device_id = $1 and capability = $2
	`, deviceID, capability).Scan(
		&item.DeviceID, &item.Capability, &item.Description, &item.TargetType,
		&item.RiskLevel, &item.MaxPermissionLevel, &item.Version, &item.UpdatedAt,
	)
	return item, err
}

func sameDeviceCapability(left domain.StewardDeviceCapability, right domain.StewardDeviceCapability) bool {
	return left.DeviceID == right.DeviceID &&
		left.Capability == right.Capability &&
		left.Description == right.Description &&
		left.TargetType == right.TargetType &&
		left.RiskLevel == right.RiskLevel &&
		left.MaxPermissionLevel == right.MaxPermissionLevel
}
