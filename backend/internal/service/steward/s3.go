package steward

import (
	"context"
	"fmt"
	"time"

	"mongojson/backend/internal/domain"
)

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
	changeContract, err := s.GetSyncChangeContract(ctx)
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
	discovery, discoveredPeers := s.peerDiscoverySnapshot()

	return domain.StewardSyncStatus{
		LocalDevice:      local,
		Devices:          devices,
		Permissions:      permissions,
		Capabilities:     capabilities,
		Security:         syncSecurityStatusFromEnv(),
		Discovery:        discovery,
		DiscoveredPeers:  discoveredPeers,
		PendingChanges:   pending,
		PendingRelations: pendingRelations,
		ConflictCount:    conflictCount,
		LastChangeAt:     lastChangeAt,
		RecentChanges:    changes,
		Conflicts:        conflicts,
		ChangeContract:   changeContract,
	}, nil
}
