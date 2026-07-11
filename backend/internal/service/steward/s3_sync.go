package steward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

func (s *Service) ImportSyncChanges(ctx context.Context, input ImportSyncChangesInput) (ImportSyncChangesResult, error) {
	senderDeviceID := strings.TrimSpace(input.Device.ID)
	if senderDeviceID == "" || senderDeviceID == s.agentIDValue() {
		return ImportSyncChangesResult{}, fmt.Errorf("%w: payload device must identify a remote peer", ErrSyncPermissionDenied)
	}
	preparedInput, err := s.PrepareImportSyncChanges(ctx, input)
	if err != nil {
		return ImportSyncChangesResult{}, err
	}
	input = preparedInput
	if err := s.observeSyncPeer(ctx, input.Device); err != nil {
		return ImportSyncChangesResult{}, err
	}
	result := ImportSyncChangesResult{
		Conflicts: []domain.StewardSyncConflict{},
		Changes:   []domain.StewardSyncChange{},
	}
	for _, item := range input.Changes {
		normalized, err := s.normalizeImportedSyncChange(ctx, senderDeviceID, item)
		if err != nil {
			if errors.Is(err, ErrSyncChangeInvalid) {
				result.Denied++
				result.Skipped++
				s.recordSyncChangeInvalid(ctx, senderDeviceID, item, err)
				continue
			}
			return ImportSyncChangesResult{}, err
		}
		item = normalized
		if err := s.authorizeDeviceSyncChange(ctx, senderDeviceID, item.EntityType, item.Payload); err != nil {
			if errors.Is(err, ErrSyncPermissionDenied) {
				result.Denied++
				result.Skipped++
				s.recordSyncPermissionDenied(ctx, senderDeviceID, item, err)
				continue
			}
			return ImportSyncChangesResult{}, err
		}
		change, created, err := s.createSyncChange(ctx, item)
		if err != nil {
			if errors.Is(err, ErrSyncChangeInvalid) {
				result.Denied++
				result.Skipped++
				s.recordSyncChangeInvalid(ctx, senderDeviceID, item, err)
				continue
			}
			return result, err
		}
		if created {
			result.Imported++
		} else {
			result.Skipped++
			if change.SyncStatus != SyncPending {
				result.Changes = append(result.Changes, change)
				continue
			}
		}
		applied, conflict, err := s.applySyncChange(ctx, change)
		if err != nil {
			return result, err
		}
		if applied {
			result.Applied++
			updated, _ := s.getSyncChange(ctx, change.ID)
			result.Changes = append(result.Changes, updated)
		} else {
			result.Changes = append(result.Changes, change)
		}
		if conflict.ID != "" {
			result.Conflicts = append(result.Conflicts, conflict)
		}
	}
	return result, nil
}

func (s *Service) SyncDevice(ctx context.Context, id string) (SyncDeviceResult, error) {
	return s.syncDevice(ctx, id, "manual")
}

func (s *Service) ProbeLocalSyncEntity(ctx context.Context, input SyncEntityProbeInput) (SyncEntityProbeResult, error) {
	input.EntityType = strings.TrimSpace(input.EntityType)
	input.EntityID = strings.TrimSpace(input.EntityID)
	if input.EntityType == "" || input.EntityID == "" {
		return SyncEntityProbeResult{}, fmt.Errorf("entity_type and entity_id are required")
	}

	result := SyncEntityProbeResult{
		DeviceID:   s.agentIDValue(),
		EntityType: input.EntityType,
		EntityID:   input.EntityID,
	}
	switch input.EntityType {
	case EntityTask:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_tasks where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local task: %w", err)
		}
	case EntityEvent:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_events where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local event: %w", err)
		}
	case EntityIntent:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_intents where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local intent: %w", err)
		}
	case EntityMemory:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_memories where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local memory: %w", err)
		}
	case EntityKnowledgeItem:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_knowledge_items where id = $1 and deleted_at is null)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local knowledge item: %w", err)
		}
	case EntitySourceRef:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_source_refs where id = $1)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local source ref: %w", err)
		}
	case EntityDataTag:
		if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_data_tags where id = $1)`, input.EntityID).Scan(&result.Exists); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local data tag: %w", err)
		}
	case EntityEntityTag:
		exists, detail, err := s.probeLocalEntityTag(ctx, input.EntityID)
		if err != nil {
			return SyncEntityProbeResult{}, err
		}
		result.Exists = exists
		result.Detail = detail
	case EntityTimeline:
		var linkedEvents int
		if err := s.db.Pool.QueryRow(ctx, `
			select exists(select 1 from steward_timeline_segments where id = $1 and deleted_at is null),
			       (select count(*) from steward_timeline_segment_events where segment_id = $1)
		`, input.EntityID).Scan(&result.Exists, &linkedEvents); err != nil {
			return SyncEntityProbeResult{}, fmt.Errorf("probe local timeline segment: %w", err)
		}
		result.Detail = map[string]any{"linked_event_count": linkedEvents}
	default:
		return SyncEntityProbeResult{}, fmt.Errorf("unsupported sync probe entity_type %q", input.EntityType)
	}
	return result, nil
}

func (s *Service) probeLocalEntityTag(ctx context.Context, entityTagID string) (bool, any, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select et.entity_type, et.entity_id::text, et.tag_id::text, coalesce(alias.alias_id::text, '')
		from steward_entity_tags et
		left join steward_data_tag_aliases alias on alias.tag_id = et.tag_id
	`)
	if err != nil {
		return false, nil, fmt.Errorf("probe local entity tag: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entityType string
		var entityID string
		var tagID string
		var aliasID string
		if err := rows.Scan(&entityType, &entityID, &tagID, &aliasID); err != nil {
			return false, nil, err
		}
		if entityTagSyncEntityID(entityType, entityID, tagID) == entityTagID ||
			(aliasID != "" && entityTagSyncEntityID(entityType, entityID, aliasID) == entityTagID) {
			return true, map[string]any{
				"entity_type": entityType,
				"entity_id":   entityID,
				"tag_id":      tagID,
			}, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	return false, nil, nil
}

func (s *Service) ProbeDeviceSyncEntity(ctx context.Context, id string, input SyncEntityProbeInput) (SyncEntityProbeResult, error) {
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return SyncEntityProbeResult{}, err
	}
	if device.ID == s.agentIDValue() {
		return s.ProbeLocalSyncEntity(ctx, input)
	}
	if device.TrustStatus == DeviceRevoked || !device.SyncEnabled {
		return SyncEntityProbeResult{}, fmt.Errorf("device is revoked or sync is disabled")
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return SyncEntityProbeResult{}, fmt.Errorf("device api_base_url is required before peer probe")
	}

	query := url.Values{
		"entity_type": []string{strings.TrimSpace(input.EntityType)},
		"entity_id":   []string{strings.TrimSpace(input.EntityID)},
	}
	endpoint, err := stewardAPIEndpoint(device.APIBaseURL, "/steward/sync/probe", query)
	if err != nil {
		return SyncEntityProbeResult{}, err
	}
	var response struct {
		Probe SyncEntityProbeResult `json:"probe"`
	}
	client := &http.Client{Timeout: 12 * time.Second}
	if err := requestPeerJSON(ctx, client, http.MethodGet, endpoint, nil, &response, s.syncAuth()); err != nil {
		return SyncEntityProbeResult{}, err
	}
	if response.Probe.DeviceID != device.ID || response.Probe.EntityType != strings.TrimSpace(input.EntityType) || response.Probe.EntityID != strings.TrimSpace(input.EntityID) {
		return SyncEntityProbeResult{}, fmt.Errorf("peer probe response identity mismatch")
	}
	return response.Probe, nil
}

func (s *Service) SyncTrustedPeerDevices(ctx context.Context) ([]SyncDeviceResult, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	results := []SyncDeviceResult{}
	failures := []string{}
	for _, device := range devices {
		if device.ID == s.agentIDValue() ||
			device.Role != DeviceRolePeer ||
			device.TrustStatus == DeviceRevoked ||
			!device.SyncEnabled ||
			strings.TrimSpace(device.APIBaseURL) == "" {
			continue
		}
		result, err := s.syncDevice(ctx, device.ID, "daemon")
		results = append(results, result)
		if err != nil {
			failures = append(failures, device.ID+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return results, errors.New(strings.Join(failures, "; "))
	}
	return results, nil
}

func (s *Service) syncDevice(ctx context.Context, id string, triggerSource string) (result SyncDeviceResult, err error) {
	defer func() {
		s.recordSyncDeviceAudit(ctx, id, triggerSource, result, err)
	}()
	device, err := s.getDevice(ctx, id)
	if err != nil {
		return SyncDeviceResult{}, err
	}
	result = SyncDeviceResult{
		Device:             device,
		RemoteLastSequence: device.LastSyncSequence,
		LocalSentSequence:  device.LastSentSequence,
		Conflicts:          []domain.StewardSyncConflict{},
		Errors:             []string{},
	}
	if device.ID == s.agentIDValue() {
		return result, fmt.Errorf("local device cannot be synced as a peer")
	}
	if device.TrustStatus == DeviceRevoked || !device.SyncEnabled {
		return result, fmt.Errorf("device is revoked or sync is disabled")
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return result, fmt.Errorf("device api_base_url is required before peer sync")
	}

	client := &http.Client{Timeout: 12 * time.Second}
	auth := s.syncAuth()
	if err := s.pullPeerSyncWindows(ctx, client, device, auth, &result); err != nil {
		_ = s.updateDeviceSyncProgress(ctx, device.ID, result.RemoteLastSequence, result.LocalSentSequence, err.Error(), false)
		result.Errors = append(result.Errors, err.Error())
		return result, err
	}
	s.pushPeerSyncWindows(ctx, client, device, auth, &result)

	errorSummary := ""
	if len(result.Errors) > 0 {
		errorSummary = strings.Join(result.Errors, "; ")
	}
	if err := s.updateDeviceSyncProgress(ctx, device.ID, result.RemoteLastSequence, result.LocalSentSequence, errorSummary, true); err != nil {
		return result, err
	}
	result.Device, _ = s.getDevice(ctx, device.ID)
	return result, nil
}

func (s *Service) pullPeerSyncWindows(ctx context.Context, client *http.Client, device domain.StewardDevice, auth syncAuth, result *SyncDeviceResult) error {
	for window := 0; window < syncMaxWindowsPerRun; window++ {
		remoteWindow, err := getPeerSyncChanges(ctx, client, device.APIBaseURL, result.RemoteLastSequence, auth)
		if err != nil {
			return err
		}
		result.Pulled += len(remoteWindow.Changes)
		if len(remoteWindow.Changes) == 0 && remoteWindow.NextSequence == result.RemoteLastSequence {
			return nil
		}

		pullWindow := buildPullSyncWindow(s.agentIDValue(), device, remoteWindow.Changes, result.RemoteLastSequence)
		if len(pullWindow.Input.Changes) > 0 {
			imported, err := s.ImportSyncChanges(ctx, pullWindow.Input)
			if err != nil {
				return err
			}
			result.Imported += imported.Imported
			result.Applied += imported.Applied
			result.Skipped += imported.Skipped
			result.Denied += imported.Denied
			result.Conflicts = append(result.Conflicts, imported.Conflicts...)
		}
		result.RemoteLastSequence = max(pullWindow.RemoteLastSequence, remoteWindow.NextSequence)
		result.Skipped += pullWindow.Skipped
		if !remoteWindow.HasMore {
			return nil
		}
	}
	return nil
}

func (s *Service) pushPeerSyncWindows(ctx context.Context, client *http.Client, device domain.StewardDevice, auth syncAuth, result *SyncDeviceResult) {
	localDevice := s.localDeviceRegistration(ctx)
	posted := false
	defer func() {
		if posted || len(result.Errors) > 0 {
			return
		}
		heartbeat := ImportSyncChangesInput{Device: localDevice, Changes: []CreateSyncChangeInput{}}
		if _, err := postPeerSyncChanges(ctx, client, device.APIBaseURL, heartbeat, auth); err != nil {
			result.Errors = append(result.Errors, "send device heartbeat: "+err.Error())
		}
	}()
	for window := 0; window < syncMaxWindowsPerRun; window++ {
		localChanges, err := s.ListSyncChanges(ctx, result.LocalSentSequence, syncChangeWindowLimit)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			return
		}
		if len(localChanges) == 0 {
			return
		}

		pushWindow := buildPushSyncWindow(localDevice, device.ID, localChanges, result.LocalSentSequence)
		if len(pushWindow.Input.Changes) > 0 {
			imported, err := postPeerSyncChanges(ctx, client, device.APIBaseURL, pushWindow.Input, auth)
			if err != nil {
				result.Errors = append(result.Errors, err.Error())
				return
			}
			posted = true
			result.Pushed += len(pushWindow.Input.Changes)
			result.Denied += imported.Denied
		}
		result.LocalSentSequence = pushWindow.LocalSentSequence
		if len(localChanges) < syncChangeWindowLimit {
			return
		}
	}
}

func (s *Service) recordSyncDeviceAudit(ctx context.Context, deviceID string, triggerSource string, result SyncDeviceResult, syncErr error) {
	if s == nil || s.db == nil {
		return
	}
	status := ResultOK
	var errorSummary *string
	output := fmt.Sprintf("pulled=%d imported=%d applied=%d skipped=%d pushed=%d denied=%d conflicts=%d",
		result.Pulled, result.Imported, result.Applied, result.Skipped, result.Pushed, result.Denied, len(result.Conflicts))
	if syncErr != nil {
		status = "failed"
		value := syncErr.Error()
		errorSummary = &value
	}
	if len(result.Errors) > 0 {
		status = "failed"
		value := strings.Join(result.Errors, "; ")
		errorSummary = &value
	}
	userConfirmed := triggerSource != "daemon"
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor:           "sync",
		Action:          "sync.device",
		TargetType:      "device",
		Source:          defaultString(triggerSource, "manual"),
		PermissionLevel: PermissionA3,
		DataLevel:       DataD2,
		InputSummary:    deviceID,
		OutputSummary:   output,
		UserConfirmed:   &userConfirmed,
		Syncable:        &syncable,
		ResultStatus:    status,
		ErrorSummary:    errorSummary,
	})
}
