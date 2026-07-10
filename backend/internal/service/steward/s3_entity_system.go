package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func (s *Service) applyAuditSummarySyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	var targetID *string
	if rawTargetID := stringPayload(change.Payload, "target_id", ""); rawTargetID != "" {
		if _, err := uuid.Parse(rawTargetID); err == nil {
			targetID = &rawTargetID
		}
	}
	var errorSummary *string
	if rawError := stringPayload(change.Payload, "error_summary", ""); rawError != "" {
		errorSummary = &rawError
	}
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_audit_logs (
			id, occurred_at, actor, action, target_type, target_id, source, permission_level, data_level,
			input_summary, output_summary, before_summary, after_summary, reason, user_confirmed, syncable,
			version, device_id, result_status, error_summary
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,'',$10,'','',$11,$12,false,$13,$14,$15,$16)
		on conflict (id) do nothing
	`, change.EntityID,
		timePayload(change.Payload, "occurred_at", change.CreatedAt),
		stringPayload(change.Payload, "actor", "remote"),
		stringPayload(change.Payload, "action", "remote.audit"),
		stringPayload(change.Payload, "target_type", "unknown"),
		targetID,
		stringPayload(change.Payload, "source", "sync"),
		stringPayload(change.Payload, "permission_level", PermissionA1),
		defaultString(change.DataLevel, DataD0),
		stringPayload(change.Payload, "output_summary", ""),
		stringPayload(change.Payload, "reason", ""),
		boolPayload(change.Payload, "user_confirmed", true),
		change.Version,
		change.OriginDeviceID,
		stringPayload(change.Payload, "result_status", ResultOK),
		errorSummary)
	if err != nil {
		return s.markApplyError(ctx, change, "apply audit summary sync change", err)
	}
	if err := s.markSyncChange(ctx, change.ID, SyncApplied, nil, true); err != nil {
		return false, domain.StewardSyncConflict{}, err
	}
	return true, domain.StewardSyncConflict{}, nil
}

func (s *Service) applyDeviceRevocationSyncChange(ctx context.Context, change domain.StewardSyncChange) (bool, domain.StewardSyncConflict, error) {
	deviceID := strings.TrimSpace(stringPayload(change.Payload, "device_id", ""))
	if deviceID == "" {
		deviceID = strings.TrimSpace(stringPayload(change.Payload, "revoked_device_id", ""))
	}
	if deviceID == "" {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("device_id is required"))
	}
	if deviceID == s.agentIDValue() {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("remote revocation cannot disable local device"))
	}
	if trustStatus := strings.TrimSpace(stringPayload(change.Payload, "trust_status", DeviceRevoked)); trustStatus != DeviceRevoked {
		return s.markApplyError(ctx, change, "apply device revocation sync change", fmt.Errorf("device revocation sync only accepts revoked status"))
	}

	now := time.Now().UTC()
	revokedAt := timePayload(change.Payload, "revoked_at", now)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_devices (
			id, device_name, platform, role, trust_status, sync_enabled, permission_level, public_key, api_base_url,
			last_seen_at, revoked_at, created_at, updated_at
		)
		values ($1,$2,$3,$4,$5,false,$6,$7,$8,$9,$10,$11,$11)
		on conflict (id) do update
		set trust_status = $5,
		    sync_enabled = false,
		    revoked_at = coalesce(steward_devices.revoked_at, excluded.revoked_at),
		    updated_at = excluded.updated_at
	`, deviceID,
		defaultString(stringPayload(change.Payload, "device_name", ""), deviceID),
		defaultString(stringPayload(change.Payload, "platform", ""), "unknown"),
		DeviceRolePeer,
		DeviceRevoked,
		defaultString(stringPayload(change.Payload, "permission_level", ""), PermissionA3),
		stringPayload(change.Payload, "public_key", ""),
		strings.TrimRight(strings.TrimSpace(stringPayload(change.Payload, "api_base_url", "")), "/"),
		nullableTimePayload(change.Payload, "last_seen_at"),
		revokedAt,
		now)
	if err != nil {
		return s.markApplyError(ctx, change, "apply device revocation sync change", err)
	}
	return s.finishAppliedSyncChange(ctx, change)
}

func (s *Service) recordAuditSummarySyncChange(ctx context.Context, audit domain.StewardAuditLog) error {
	if !audit.Syncable || strings.EqualFold(audit.Actor, "sync") || dataLevelRank(audit.DataLevel) > dataLevelRank(DataD1) {
		return nil
	}
	targetID := ""
	if audit.TargetID != nil {
		targetID = *audit.TargetID
	}
	errorSummary := ""
	if audit.ErrorSummary != nil {
		errorSummary = truncateSyncSummary(*audit.ErrorSummary, 500)
	}
	payload := map[string]any{
		"occurred_at":      audit.OccurredAt,
		"actor":            audit.Actor,
		"action":           audit.Action,
		"target_type":      audit.TargetType,
		"target_id":        targetID,
		"source":           audit.Source,
		"permission_level": audit.PermissionLevel,
		"data_level":       audit.DataLevel,
		"output_summary":   truncateSyncSummary(audit.OutputSummary, 500),
		"reason":           truncateSyncSummary(audit.Reason, 500),
		"user_confirmed":   audit.UserConfirmed,
		"result_status":    audit.ResultStatus,
		"error_summary":    errorSummary,
	}
	return s.recordEntitySyncChange(ctx, EntityAuditSummary, audit.ID, SyncCreate, audit.Version, audit.DataLevel, payload)
}

func deviceRevocationSyncEntityID(deviceID string) string {
	key := strings.Join([]string{
		"steward_device_revoke",
		strings.TrimSpace(deviceID),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}
