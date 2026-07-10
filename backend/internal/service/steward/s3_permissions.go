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

var ErrSyncPermissionDenied = errors.New("sync permission denied")

type defaultDevicePermission struct {
	Capability         string
	Policy             string
	MaxPermissionLevel string
	ScopeSummary       string
}

var defaultSyncPermissions = []defaultDevicePermission{
	{Capability: "sync.metadata", Policy: "allow", MaxPermissionLevel: PermissionA1, ScopeSummary: "同步设备心跳、能力摘要和索引"},
	{Capability: "sync.tasks", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步任务和意图"},
	{Capability: "sync.timeline", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步事件和时间线摘要"},
	{Capability: "sync.memory", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步记忆条目"},
	{Capability: "sync.knowledge", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步知识元数据和来源引用"},
	{Capability: "sync.tags", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步标签和实体标签"},
	{Capability: "sync.audit", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步脱敏审计摘要"},
	{Capability: "sync.devices", Policy: "allow", MaxPermissionLevel: PermissionA3, ScopeSummary: "同步设备能力和撤销状态"},
}

func (s *Service) ensureDefaultDevicePermissions(ctx context.Context, deviceID string, now time.Time) error {
	permissions := append([]defaultDevicePermission{}, defaultSyncPermissions...)
	permissions = append(permissions,
		defaultDevicePermission{Capability: "remote.execute", Policy: "deny", MaxPermissionLevel: PermissionA3, ScopeSummary: "远程设备默认不能触发本机执行"},
		defaultDevicePermission{Capability: "autonomy.execute", Policy: "confirm", MaxPermissionLevel: PermissionA3, ScopeSummary: "自主执行默认需要确认"},
	)
	capabilities := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		capabilities = append(capabilities, permission.Capability)
	}
	var existing int
	if err := s.db.Pool.QueryRow(ctx, `
		select count(*) from steward_device_permissions
		where device_id = $1 and capability = any($2)
	`, deviceID, capabilities).Scan(&existing); err != nil {
		return fmt.Errorf("count steward device permissions: %w", err)
	}
	if existing == len(permissions) {
		return nil
	}

	batch := &pgx.Batch{}
	for _, permission := range permissions {
		batch.Queue(`
			insert into steward_device_permissions (
				id, device_id, capability, policy, max_permission_level, scope_summary, created_at, updated_at
			)
			values ($1,$2,$3,$4,$5,$6,$7,$7)
			on conflict (device_id, capability) do nothing
		`, uuid.NewString(), deviceID, permission.Capability, permission.Policy,
			permission.MaxPermissionLevel, permission.ScopeSummary, now)
	}
	results := s.db.Pool.SendBatch(ctx, batch)
	for _, permission := range permissions {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("ensure steward device permission %s: %w", permission.Capability, err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close steward device permission batch: %w", err)
	}
	return nil
}

func validDeviceCapability(capability string) bool {
	capability = strings.TrimSpace(capability)
	if capability == "remote.execute" || capability == "autonomy.execute" {
		return true
	}
	for _, permission := range defaultSyncPermissions {
		if permission.Capability == capability {
			return true
		}
	}
	return false
}

func validDevicePermissionPolicy(policy string) bool {
	switch strings.TrimSpace(policy) {
	case "allow", "confirm", "deny":
		return true
	default:
		return false
	}
}

func validPermissionLevel(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	return len(value) == 2 && value[0] == 'A' && value[1] >= '0' && value[1] <= '9'
}

func syncChangePermissionLevel(adapter SyncEntityAdapter, payload map[string]any) string {
	if value := strings.TrimSpace(stringPayload(payload, "permission_level", "")); value != "" {
		return value
	}
	if adapter == nil {
		return PermissionA3
	}
	return adapter.DefaultPermissionLevel()
}

func (s *Service) authorizeDeviceSyncChange(ctx context.Context, deviceID string, entityType string, payload map[string]any) error {
	device, err := s.requireAuthorizedSyncDevice(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSyncPermissionDenied, err)
	}
	adapter, ok := s.syncEntities.resolve(entityType)
	if !ok {
		return fmt.Errorf("%w: unsupported entity type %q", ErrSyncPermissionDenied, entityType)
	}
	capability := strings.TrimSpace(adapter.SyncCapability())
	if !validDeviceCapability(capability) {
		return fmt.Errorf("%w: entity type %q declares unsupported capability %q", ErrSyncPermissionDenied, entityType, capability)
	}
	var policy, maxPermission string
	err = s.db.Pool.QueryRow(ctx, `
		select policy, max_permission_level
		from steward_device_permissions
		where device_id = $1 and capability = $2
	`, deviceID, capability).Scan(&policy, &maxPermission)
	if err != nil {
		return fmt.Errorf("%w: device %q has no %s permission", ErrSyncPermissionDenied, deviceID, capability)
	}
	if strings.TrimSpace(policy) != "allow" {
		return fmt.Errorf("%w: device %q policy for %s is %s", ErrSyncPermissionDenied, deviceID, capability, policy)
	}
	requiredPermission := syncChangePermissionLevel(adapter, payload)
	if permissionRank(requiredPermission) > permissionRank(maxPermission) {
		return fmt.Errorf("%w: %s requires %s but device %q allows up to %s", ErrSyncPermissionDenied, capability, requiredPermission, deviceID, maxPermission)
	}
	if permissionRank(requiredPermission) > permissionRank(device.PermissionLevel) {
		return fmt.Errorf("%w: change requires %s but device %q ceiling is %s", ErrSyncPermissionDenied, requiredPermission, deviceID, device.PermissionLevel)
	}
	return nil
}

func (s *Service) filterOutboundSyncChanges(ctx context.Context, deviceID string, changes []domain.StewardSyncChange) ([]domain.StewardSyncChange, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return changes, nil
	}
	filtered := make([]domain.StewardSyncChange, 0, len(changes))
	for _, change := range changes {
		if err := s.authorizeDeviceSyncChange(ctx, deviceID, change.EntityType, change.Payload); err != nil {
			if errors.Is(err, ErrSyncPermissionDenied) {
				continue
			}
			return nil, err
		}
		filtered = append(filtered, change)
	}
	return filtered, nil
}

func (s *Service) recordSyncPermissionDenied(ctx context.Context, deviceID string, change CreateSyncChangeInput, permissionErr error) {
	userConfirmed := false
	syncable := false
	var targetID *string
	if _, err := uuid.Parse(strings.TrimSpace(change.EntityID)); err == nil {
		targetID = stringPtr(change.EntityID)
	}
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "sync",
		Action:          "sync.change.denied",
		TargetType:      defaultString(change.EntityType, "sync_change"),
		TargetID:        targetID,
		Source:          "peer",
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    "device=" + strings.TrimSpace(deviceID) + "; entity_type=" + strings.TrimSpace(change.EntityType),
		OutputSummary:   "incoming sync change skipped by device permission",
		Reason:          permissionErr.Error(),
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    ResultBlocked,
		ErrorSummary:    stringPtr(permissionErr.Error()),
	})
}
