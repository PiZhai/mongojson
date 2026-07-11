package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

var ErrSyncChangeInvalid = errors.New("invalid sync change")

func (s *Service) normalizeLocalSyncChange(input CreateSyncChangeInput) (CreateSyncChangeInput, error) {
	input.ID = defaultString(strings.TrimSpace(input.ID), uuid.NewString())
	input.EntityType = strings.TrimSpace(input.EntityType)
	input.EntityID = strings.TrimSpace(input.EntityID)
	input.OriginDeviceID = defaultString(strings.TrimSpace(input.OriginDeviceID), s.agentIDValue())
	input.Operation = defaultString(strings.TrimSpace(input.Operation), SyncUpdate)
	input.DataLevel = defaultString(strings.ToUpper(strings.TrimSpace(input.DataLevel)), DataD0)
	if input.Version == 0 {
		input.Version = 1
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.OriginDeviceID != s.agentIDValue() {
		return CreateSyncChangeInput{}, syncChangeInvalid("origin_device_id must match the local device")
	}
	if err := s.validateSyncChangeShape(input); err != nil {
		return CreateSyncChangeInput{}, err
	}
	return input, nil
}

func (s *Service) normalizeImportedSyncChange(ctx context.Context, senderDeviceID string, input CreateSyncChangeInput) (CreateSyncChangeInput, error) {
	senderDeviceID = strings.TrimSpace(senderDeviceID)
	input.ID = strings.TrimSpace(input.ID)
	input.EntityType = strings.TrimSpace(input.EntityType)
	input.EntityID = strings.TrimSpace(input.EntityID)
	input.OriginDeviceID = strings.TrimSpace(input.OriginDeviceID)
	input.Operation = strings.TrimSpace(input.Operation)
	input.DataLevel = strings.ToUpper(strings.TrimSpace(input.DataLevel))
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if senderDeviceID == "" || senderDeviceID == s.agentIDValue() {
		return CreateSyncChangeInput{}, syncChangeInvalid("sender device must identify a remote peer")
	}
	if input.ID == "" || input.Operation == "" || input.OriginDeviceID == "" || input.DataLevel == "" || input.Version <= 0 {
		return CreateSyncChangeInput{}, syncChangeInvalid("id, operation, origin_device_id, positive version, and data_level are required")
	}
	if input.OriginDeviceID == s.agentIDValue() {
		return CreateSyncChangeInput{}, syncChangeInvalid("origin_device_id cannot claim the receiving device")
	}
	if err := s.validateSyncChangeShape(input); err != nil {
		return CreateSyncChangeInput{}, err
	}
	origin, err := s.getDevice(ctx, input.OriginDeviceID)
	if err != nil {
		return CreateSyncChangeInput{}, syncChangeInvalid("origin_device_id is not a registered peer")
	}
	if origin.Role != DeviceRolePeer || origin.TrustStatus != DeviceTrusted || !origin.SyncEnabled {
		return CreateSyncChangeInput{}, syncChangeInvalid("origin_device_id is not an active trusted peer")
	}
	return input, nil
}

func (s *Service) validateSyncChangeShape(input CreateSyncChangeInput) error {
	if _, err := uuid.Parse(input.ID); err != nil {
		return syncChangeInvalid("id must be a UUID")
	}
	if _, err := uuid.Parse(input.EntityID); err != nil {
		return syncChangeInvalid("entity_id must be a UUID")
	}
	if _, ok := s.syncEntities.resolve(input.EntityType); !ok {
		return syncChangeInvalid(fmt.Sprintf("unsupported entity_type %q", input.EntityType))
	}
	if !validSyncOperation(input.Operation) {
		return syncChangeInvalid(fmt.Sprintf("unsupported operation %q", input.Operation))
	}
	if input.Version <= 0 {
		return syncChangeInvalid("version must be positive")
	}
	if !validDataLevel(input.DataLevel) {
		return syncChangeInvalid(fmt.Sprintf("unsupported data_level %q", input.DataLevel))
	}
	if err := validateSyncPayloadPolicy(input.Payload, input.DataLevel); err != nil {
		return err
	}
	return nil
}

func validateSyncPayloadPolicy(payload map[string]any, envelopeDataLevel string) error {
	if value, exists := payload["permission_level"]; exists && value != nil {
		permission, ok := value.(string)
		if !ok || !validPermissionLevel(permission) {
			return syncChangeInvalid("payload permission_level must be A0-A9")
		}
		payload["permission_level"] = strings.ToUpper(strings.TrimSpace(permission))
	}
	if value, exists := payload["data_level"]; exists && value != nil {
		dataLevel, ok := value.(string)
		dataLevel = strings.ToUpper(strings.TrimSpace(dataLevel))
		if !ok || !validDataLevel(dataLevel) {
			return syncChangeInvalid("payload data_level must be D0-D6")
		}
		if dataLevel != envelopeDataLevel {
			return syncChangeInvalid("payload data_level must match the sync envelope")
		}
		payload["data_level"] = dataLevel
	}
	return nil
}

func validSyncOperation(value string) bool {
	switch value {
	case SyncCreate, SyncUpdate, SyncDelete:
		return true
	default:
		return false
	}
}

func validDataLevel(value string) bool {
	switch value {
	case DataD0, DataD1, DataD2, DataD3, DataD4, DataD5, DataD6:
		return true
	default:
		return false
	}
}

func syncChangeInvalid(message string) error {
	return fmt.Errorf("%w: %s", ErrSyncChangeInvalid, message)
}

func syncChangeMatchesInput(change domain.StewardSyncChange, input CreateSyncChangeInput) bool {
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return false
	}
	return change.ID == input.ID &&
		change.EntityType == input.EntityType &&
		change.EntityID == input.EntityID &&
		change.Operation == input.Operation &&
		change.OriginDeviceID == input.OriginDeviceID &&
		change.Version == input.Version &&
		change.DataLevel == input.DataLevel &&
		change.PayloadHash == hashBytes(payload)
}

func storedSyncChangeIssues(change domain.StewardSyncChange, knownDevices map[string]domain.StewardDevice, registry *syncEntityAdapterRegistry) []string {
	issues := []string{}
	if _, err := uuid.Parse(change.ID); err != nil {
		issues = append(issues, "id is not a UUID")
	}
	if _, err := uuid.Parse(change.EntityID); err != nil {
		issues = append(issues, "entity_id is not a UUID")
	}
	if _, ok := registry.resolve(change.EntityType); !ok {
		issues = append(issues, "entity_type is unsupported")
	}
	if !validSyncOperation(change.Operation) {
		issues = append(issues, "operation is invalid")
	}
	if change.Version <= 0 {
		issues = append(issues, "version is not positive")
	}
	if !validDataLevel(change.DataLevel) {
		issues = append(issues, "data_level is invalid")
	}
	if _, ok := knownDevices[change.OriginDeviceID]; !ok {
		issues = append(issues, "origin_device_id is unknown")
	}
	if err := validateSyncPayloadPolicy(change.Payload, change.DataLevel); err != nil {
		issues = append(issues, strings.TrimPrefix(err.Error(), ErrSyncChangeInvalid.Error()+": "))
	}
	payload, err := json.Marshal(change.Payload)
	if err != nil || hashBytes(payload) != change.PayloadHash {
		issues = append(issues, "payload_hash does not match payload")
	}
	switch change.SyncStatus {
	case SyncPending, SyncApplied, SyncStored, SyncConflictStatus:
	default:
		issues = append(issues, "sync_status is invalid")
	}
	return issues
}

func (s *Service) GetSyncChangeContract(ctx context.Context) (domain.StewardSyncChangeContract, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return domain.StewardSyncChangeContract{}, err
	}
	knownDevices := make(map[string]domain.StewardDevice, len(devices))
	for _, device := range devices {
		knownDevices[device.ID] = device
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, sequence, entity_type, entity_id::text, operation, origin_device_id, version, data_level,
		       payload::text, payload_hash, sync_status, error_summary, created_at, applied_at
		from steward_sync_changes
		order by sequence
	`)
	if err != nil {
		return domain.StewardSyncChangeContract{}, fmt.Errorf("scan sync change contract: %w", err)
	}
	defer rows.Close()
	result := domain.StewardSyncChangeContract{Healthy: true, Issues: []string{}}
	for rows.Next() {
		change, err := scanSyncChange(rows)
		if err != nil {
			return domain.StewardSyncChangeContract{}, err
		}
		result.CheckedChanges++
		issues := storedSyncChangeIssues(change, knownDevices, s.syncEntities)
		if len(issues) == 0 {
			continue
		}
		result.Healthy = false
		result.InvalidChanges++
		if len(result.Issues) < 20 {
			result.Issues = append(result.Issues, change.ID+": "+strings.Join(issues, "; "))
		}
	}
	if err := rows.Err(); err != nil {
		return domain.StewardSyncChangeContract{}, err
	}
	return result, nil
}
