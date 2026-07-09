package steward

import (
	"reflect"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestIncomingSyncEntitySnapshotsMatchLocalBusinessFields(t *testing.T) {
	when := time.Date(2026, 7, 5, 12, 30, 0, 123000000, time.UTC)
	tests := []struct {
		name     string
		local    map[string]any
		incoming domain.StewardSyncChange
	}{
		{
			name: "task",
			local: taskSyncSnapshot(domain.StewardTask{
				Type: "manual", Title: "task", Description: "details", Status: StatusOpen, Priority: "high", DueAt: &when,
				DataLevel: DataD1, PermissionLevel: PermissionA3, RiskLevel: "low", UserConfirmed: true,
			}),
			incoming: domain.StewardSyncChange{EntityType: EntityTask, Operation: SyncUpdate, DataLevel: DataD1, Payload: map[string]any{
				"type": "manual", "title": "task", "description": "details", "status": StatusOpen, "priority": "high",
				"due_at": when.Format(time.RFC3339Nano), "permission_level": PermissionA3, "risk_level": "low", "user_confirmed": true,
			}},
		},
		{
			name: "event",
			local: eventSyncSnapshot(domain.StewardEvent{
				Type: "activity", Title: "event", Summary: "summary", Status: StatusActive, DataLevel: DataD0,
				PermissionLevel: PermissionA3, UserConfirmed: true,
			}),
			incoming: domain.StewardSyncChange{EntityType: EntityEvent, Operation: SyncUpdate, DataLevel: DataD0, Payload: map[string]any{
				"type": "activity", "title": "event", "summary": "summary", "status": StatusActive,
				"permission_level": PermissionA3, "user_confirmed": true,
			}},
		},
		{
			name: "intent",
			local: intentSyncSnapshot(domain.StewardIntent{
				Type: "follow_up", Title: "intent", Summary: "summary", Reason: "reason", SuggestedAction: "act", RiskLevel: "low",
				Status: StatusCandidate, DataLevel: DataD0, PermissionLevel: PermissionA3, Confidence: 0.8, UserConfirmed: true,
			}),
			incoming: domain.StewardSyncChange{EntityType: EntityIntent, Operation: SyncUpdate, DataLevel: DataD0, Payload: map[string]any{
				"type": "follow_up", "title": "intent", "summary": "summary", "reason": "reason", "suggested_action": "act",
				"risk_level": "low", "status": StatusCandidate, "permission_level": PermissionA3, "confidence": 0.8, "user_confirmed": true,
			}},
		},
		{
			name: "memory",
			local: memorySyncSnapshot(domain.StewardMemory{
				Type: "project_fact", Title: "memory", Summary: "summary", Content: "content", Scope: "global", Status: StatusActive,
				DataLevel: DataD1, PermissionLevel: PermissionA3, Confidence: 0.9, UserConfirmed: true, LastVerifiedAt: &when,
			}),
			incoming: domain.StewardSyncChange{EntityType: EntityMemory, Operation: SyncUpdate, DataLevel: DataD1, Payload: map[string]any{
				"type": "project_fact", "title": "memory", "summary": "summary", "content": "content", "scope": "global",
				"status": StatusActive, "permission_level": PermissionA3, "confidence": 0.9, "user_confirmed": true,
				"last_verified_at": when.Format(time.RFC3339Nano),
			}},
		},
		{
			name: "knowledge",
			local: knowledgeSyncSnapshot(domain.StewardKnowledgeItem{
				Type: "note", Title: "knowledge", Summary: "summary", OriginalURI: "local://note", ImportMethod: "manual",
				ContentHash: "hash", Status: StatusActive, DataLevel: DataD1, PermissionLevel: PermissionA3, AllowIndex: true, UserConfirmed: true,
			}),
			incoming: domain.StewardSyncChange{EntityType: EntityKnowledgeItem, Operation: SyncUpdate, DataLevel: DataD1, Payload: map[string]any{
				"type": "note", "title": "knowledge", "summary": "summary", "original_uri": "local://note", "import_method": "manual",
				"content_hash": "hash", "status": StatusActive, "permission_level": PermissionA3, "allow_index": true, "user_confirmed": true,
			}},
		},
		{
			name: "timeline",
			local: timelineSyncSnapshot(domain.StewardTimelineSegment{
				Type: "remote_cluster", Title: "timeline", Summary: "summary", Status: StatusActive, DataLevel: DataD0,
				PermissionLevel: PermissionA3, StartAt: &when, Confidence: 0.7, UserConfirmed: true,
			}, []string{"event-b", "event-a"}),
			incoming: domain.StewardSyncChange{EntityType: EntityTimeline, Operation: SyncUpdate, DataLevel: DataD0, Payload: map[string]any{
				"type": "remote_cluster", "title": "timeline", "summary": "summary", "status": StatusActive,
				"permission_level": PermissionA3, "start_at": when.Format(time.RFC3339Nano), "confidence": 0.7,
				"user_confirmed": true, "event_ids": []any{"event-a", "event-b"},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			incoming, err := incomingSyncEntitySnapshot(test.incoming)
			if err != nil {
				t.Fatalf("build incoming snapshot: %v", err)
			}
			if !reflect.DeepEqual(test.local, incoming) {
				t.Fatalf("snapshot mismatch\nlocal: %#v\nincoming: %#v", test.local, incoming)
			}
		})
	}
}

func TestIncomingSyncEntitySnapshotDetectsDivergentContent(t *testing.T) {
	local := eventSyncSnapshot(domain.StewardEvent{
		Type: "activity", Title: "local title", Status: StatusActive, DataLevel: DataD0,
		PermissionLevel: PermissionA3, UserConfirmed: true,
	})
	incoming, err := incomingSyncEntitySnapshot(domain.StewardSyncChange{
		EntityType: EntityEvent, Operation: SyncUpdate, DataLevel: DataD0,
		Payload: map[string]any{"type": "activity", "title": "remote title", "status": StatusActive, "permission_level": PermissionA3, "user_confirmed": true},
	})
	if err != nil {
		t.Fatalf("build incoming snapshot: %v", err)
	}
	if reflect.DeepEqual(local, incoming) {
		t.Fatalf("divergent snapshots should not match")
	}
}
