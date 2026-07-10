package steward

import (
	"context"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestSyncEntityAdapterRegistryResolvesReplacement(t *testing.T) {
	called := ""
	registry := newSyncEntityAdapterRegistry(
		newSyncEntityAdapter(EntityTask, "sync.tasks", PermissionA3, func(context.Context, domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
			called = "old"
			return true, domain.StewardSyncConflict{}, nil
		}),
	)
	registry.register(newSyncEntityAdapter(EntityTask, "sync.timeline", PermissionA1, func(_ context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
		called = change.EntityID
		return true, domain.StewardSyncConflict{}, nil
	}))

	adapter, ok := registry.resolve(EntityTask)
	if !ok {
		t.Fatalf("expected task adapter")
	}
	if _, _, err := adapter.Apply(context.Background(), domain.StewardSyncChange{EntityID: "replacement"}); err != nil {
		t.Fatalf("apply replacement adapter: %v", err)
	}
	if called != "replacement" {
		t.Fatalf("expected replacement adapter, got %q", called)
	}
	if adapter.SyncCapability() != "sync.timeline" || adapter.DefaultPermissionLevel() != PermissionA1 {
		t.Fatalf("replacement metadata not retained: capability=%q permission=%q", adapter.SyncCapability(), adapter.DefaultPermissionLevel())
	}
	if _, ok := registry.resolve("unknown"); ok {
		t.Fatalf("unexpected adapter for unknown entity")
	}
}

func TestSyncEntityAdapterRejectsMissingApplyFunction(t *testing.T) {
	adapter := newSyncEntityAdapter("broken", "sync.metadata", PermissionA1, nil)
	_, _, err := adapter.Apply(context.Background(), domain.StewardSyncChange{})
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("expected adapter initialization error, got %v", err)
	}
}

func TestDefaultSyncEntityAdaptersDeclarePermissionMetadata(t *testing.T) {
	service := NewService(nil)
	expected := map[string]struct {
		capability string
		permission string
	}{
		EntityTask:             {capability: "sync.tasks", permission: PermissionA3},
		EntityEvent:            {capability: "sync.timeline", permission: PermissionA3},
		EntityIntent:           {capability: "sync.tasks", permission: PermissionA3},
		EntityMemory:           {capability: "sync.memory", permission: PermissionA3},
		EntityKnowledgeItem:    {capability: "sync.knowledge", permission: PermissionA3},
		EntitySourceRef:        {capability: "sync.knowledge", permission: PermissionA3},
		EntityDataTag:          {capability: "sync.tags", permission: PermissionA3},
		EntityEntityTag:        {capability: "sync.tags", permission: PermissionA3},
		EntityTimeline:         {capability: "sync.timeline", permission: PermissionA3},
		EntityAuditSummary:     {capability: "sync.audit", permission: PermissionA3},
		EntityDeviceRevoke:     {capability: "sync.devices", permission: PermissionA3},
		EntityDeviceCapability: {capability: "sync.devices", permission: PermissionA1},
	}
	for entityType, want := range expected {
		adapter, ok := service.syncEntities.resolve(entityType)
		if !ok {
			t.Fatalf("missing default adapter for %s", entityType)
		}
		if adapter.SyncCapability() != want.capability || adapter.DefaultPermissionLevel() != want.permission {
			t.Fatalf("unexpected metadata for %s: capability=%q permission=%q", entityType, adapter.SyncCapability(), adapter.DefaultPermissionLevel())
		}
	}
	if len(service.syncEntities.adapters) != len(expected) {
		t.Fatalf("unexpected default adapter count: got %d want %d", len(service.syncEntities.adapters), len(expected))
	}
}

func TestSyncChangePermissionLevelUsesAdapterDefaultAndPayloadOverride(t *testing.T) {
	adapter := newSyncEntityAdapter("custom", "sync.metadata", PermissionA1, nil)
	if got := syncChangePermissionLevel(adapter, map[string]any{}); got != PermissionA1 {
		t.Fatalf("expected adapter default permission, got %s", got)
	}
	if got := syncChangePermissionLevel(adapter, map[string]any{"permission_level": PermissionA4}); got != PermissionA4 {
		t.Fatalf("expected payload permission override, got %s", got)
	}
}
