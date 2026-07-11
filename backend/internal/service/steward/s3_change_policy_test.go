package steward

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func TestNormalizeLocalSyncChangeAppliesOnlyDocumentedDefaults(t *testing.T) {
	service := testSyncChangePolicyService("windows-main")
	entityID := uuid.NewString()
	input, err := service.normalizeLocalSyncChange(CreateSyncChangeInput{
		EntityType: EntityTask,
		EntityID:   entityID,
		Payload:    map[string]any{"permission_level": " a3 ", "data_level": "d0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.ID == "" || input.OriginDeviceID != "windows-main" || input.Operation != SyncUpdate || input.Version != 1 || input.DataLevel != DataD0 {
		t.Fatalf("unexpected normalized change: %#v", input)
	}
	if input.Payload["permission_level"] != PermissionA3 || input.Payload["data_level"] != DataD0 {
		t.Fatalf("payload levels were not canonicalized: %#v", input.Payload)
	}
}

func TestNormalizeLocalSyncChangeRejectsUnsafeClaims(t *testing.T) {
	service := testSyncChangePolicyService("windows-main")
	base := CreateSyncChangeInput{ID: uuid.NewString(), EntityType: EntityTask, EntityID: uuid.NewString(), Operation: SyncCreate, Version: 1, DataLevel: DataD0}
	tests := []struct {
		name   string
		mutate func(*CreateSyncChangeInput)
	}{
		{name: "remote origin", mutate: func(input *CreateSyncChangeInput) { input.OriginDeviceID = "macbook-main" }},
		{name: "unknown operation", mutate: func(input *CreateSyncChangeInput) { input.Operation = "upsert" }},
		{name: "unknown entity", mutate: func(input *CreateSyncChangeInput) { input.EntityType = "credential" }},
		{name: "invalid entity id", mutate: func(input *CreateSyncChangeInput) { input.EntityID = "not-a-uuid" }},
		{name: "invalid data level", mutate: func(input *CreateSyncChangeInput) { input.DataLevel = "secret" }},
		{name: "negative version", mutate: func(input *CreateSyncChangeInput) { input.Version = -1 }},
		{name: "invalid permission", mutate: func(input *CreateSyncChangeInput) { input.Payload = map[string]any{"permission_level": "root"} }},
		{name: "mismatched data level", mutate: func(input *CreateSyncChangeInput) { input.Payload = map[string]any{"data_level": DataD1} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.Payload = map[string]any{}
			tt.mutate(&input)
			if _, err := service.normalizeLocalSyncChange(input); !errors.Is(err, ErrSyncChangeInvalid) {
				t.Fatalf("error = %v, want ErrSyncChangeInvalid", err)
			}
		})
	}
}

func TestStoredSyncChangeIssuesDetectsPoisonedHistory(t *testing.T) {
	service := testSyncChangePolicyService("windows-main")
	change := domain.StewardSyncChange{
		ID: uuid.NewString(), EntityType: EntityTask, EntityID: uuid.NewString(), Operation: "upsert",
		OriginDeviceID: "unknown", Version: 0, DataLevel: "secret", Payload: map[string]any{"permission_level": "root"},
		PayloadHash: "bad", SyncStatus: "mystery",
	}
	issues := storedSyncChangeIssues(change, map[string]domain.StewardDevice{}, service.syncEntities)
	if len(issues) < 6 {
		t.Fatalf("expected poisoned history issues, got %#v", issues)
	}
}

func TestImportSyncChangesRequiresRemotePayloadDevice(t *testing.T) {
	service := testSyncChangePolicyService("windows-main")
	if _, err := service.ImportSyncChanges(context.Background(), ImportSyncChangesInput{}); !errors.Is(err, ErrSyncPermissionDenied) {
		t.Fatalf("error = %v, want ErrSyncPermissionDenied", err)
	}
}

func testSyncChangePolicyService(agentID string) *Service {
	return &Service{
		agentID: agentID,
		syncEntities: newSyncEntityAdapterRegistry(
			newSyncEntityAdapter(EntityTask, "sync.tasks", PermissionA3, func(context.Context, domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
				return true, domain.StewardSyncConflict{}, nil
			}),
		),
	}
}
