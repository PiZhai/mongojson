package steward

import (
	"context"
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

type SyncEntityAdapter interface {
	EntityType() string
	SyncCapability() string
	DefaultPermissionLevel() string
	Apply(context.Context, domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error)
}

type syncEntityAdapter struct {
	entityType             string
	syncCapability         string
	defaultPermissionLevel string
	apply                  func(context.Context, domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error)
}

func newSyncEntityAdapter(entityType string, syncCapability string, defaultPermissionLevel string, apply func(context.Context, domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error)) SyncEntityAdapter {
	return syncEntityAdapter{
		entityType:             strings.TrimSpace(entityType),
		syncCapability:         strings.TrimSpace(syncCapability),
		defaultPermissionLevel: strings.TrimSpace(defaultPermissionLevel),
		apply:                  apply,
	}
}

func (a syncEntityAdapter) EntityType() string {
	return a.entityType
}

func (a syncEntityAdapter) SyncCapability() string {
	return a.syncCapability
}

func (a syncEntityAdapter) DefaultPermissionLevel() string {
	return defaultString(a.defaultPermissionLevel, PermissionA3)
}

func (a syncEntityAdapter) Apply(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	if a.apply == nil {
		return false, domain.StewardSyncConflict{}, fmt.Errorf("sync entity adapter %s is not initialized", a.entityType)
	}
	return a.apply(ctx, change)
}

type syncEntityAdapterRegistry struct {
	adapters map[string]SyncEntityAdapter
}

func newSyncEntityAdapterRegistry(adapters ...SyncEntityAdapter) *syncEntityAdapterRegistry {
	registry := &syncEntityAdapterRegistry{adapters: map[string]SyncEntityAdapter{}}
	for _, adapter := range adapters {
		registry.register(adapter)
	}
	return registry
}

func (r *syncEntityAdapterRegistry) register(adapter SyncEntityAdapter) {
	if r == nil || adapter == nil {
		return
	}
	entityType := strings.TrimSpace(adapter.EntityType())
	if entityType == "" {
		return
	}
	if r.adapters == nil {
		r.adapters = map[string]SyncEntityAdapter{}
	}
	r.adapters[entityType] = adapter
}

func (r *syncEntityAdapterRegistry) resolve(entityType string) (SyncEntityAdapter, bool) {
	if r == nil {
		return nil, false
	}
	adapter, ok := r.adapters[strings.TrimSpace(entityType)]
	return adapter, ok
}

func (s *Service) applySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	adapter, ok := s.syncEntities.resolve(change.EntityType)
	if !ok {
		if err := s.markSyncChange(ctx, change.ID, SyncStored, nil, false); err != nil {
			return false, domain.StewardSyncConflict{}, err
		}
		return false, domain.StewardSyncConflict{}, nil
	}
	return adapter.Apply(ctx, change)
}
