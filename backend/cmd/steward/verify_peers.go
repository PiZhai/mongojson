package main

import (
	"flag"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (c cli) verifyPeers(args []string) error {
	fs := flag.NewFlagSet("steward verify peers", flag.ExitOnError)
	opts := peerVerifyOptions{}
	evidenceDir := fs.String("evidence-dir", "", "Write a timestamped verification evidence JSON file to this directory")
	fs.BoolVar(&opts.Sync, "sync", false, "Run one peer sync after a successful trust verification")
	fs.BoolVar(&opts.Strict, "strict", false, "Fail when any registered peer is not verifiable")
	fs.BoolVar(&opts.RequirePeers, "require-peers", false, "Fail when no peer devices are registered")
	fs.BoolVar(&opts.WriteProbes, "write-probes", false, "Create low-risk local relation probes and verify they appear on synced peers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.WriteProbes && !opts.Sync {
		return fmt.Errorf("verify peers --write-probes requires --sync")
	}

	result := c.runPeersVerification(opts)
	if err := printVerificationResult("peers", *evidenceDir, result, result.OK); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("peer verification failed")
	}
	return nil
}

func (c cli) runPeersVerification(opts peerVerifyOptions) peersVerificationResult {
	result := peersVerificationResult{
		OK:      true,
		APIBase: c.apiBase,
		Options: opts,
		Peers:   []peerVerificationResult{},
		Checks:  []runtimeVerificationCheck{},
	}
	add := func(id string, status string, message string, detail any) {
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: id, Status: status, Message: message, Detail: detail})
		if status == "error" {
			result.OK = false
		}
	}

	syncStatus, err := c.getJSON("/steward/sync/status")
	if err != nil {
		add("s3.peers.status", "error", "load sync status", nil)
		result.Peers = append(result.Peers, peerVerificationResult{Status: "error", Message: "load sync status", Error: err.Error()})
		return result
	}
	syncPayload := mapAt(syncStatus, "sync")
	localID := stringAt(mapAt(syncPayload, "local_device"), "id")
	localDevice := peerVerificationDeviceFromMap(mapAt(syncPayload, "local_device"))
	result.LocalDevice = &localDevice
	devices := devicesFromSyncStatus(syncPayload)
	peerCount := 0
	syncablePeerCount := 0
	syncablePeerIDs := []string{}
	for _, device := range devices {
		if device.ID == "" || device.ID == localID || device.Role == "local" {
			continue
		}
		peerCount++
		if peerVerificationSkipReason(device) == "" {
			syncablePeerCount++
			syncablePeerIDs = append(syncablePeerIDs, device.ID)
		}
	}
	add("s3.peers.status", "ok", "peer sync status is visible", map[string]any{
		"agent_id":            localDevice.AgentID,
		"platform":            localDevice.Platform,
		"peer_count":          peerCount,
		"syncable_peer_count": syncablePeerCount,
	})

	var probe *peerWriteProbe
	if opts.WriteProbes && syncablePeerCount > 0 {
		createdProbe, err := c.createPeerSyncWriteProbe()
		if err != nil {
			add("s3.peer_probe.create", "error", err.Error(), peerVerificationCheckDetail(localDevice, map[string]any{
				"syncable_peer_count": syncablePeerCount,
			}))
			result.Peers = append(result.Peers, peerVerificationResult{
				Status:  "error",
				Message: "create peer sync write probe",
				Error:   err.Error(),
			})
			return result
		}
		probe = &createdProbe
		result.Probe = probe
	}
	if opts.WriteProbes && syncablePeerCount == 0 {
		add("s3.peer_probe.relations", "error", "no syncable peer devices are available for write probes", peerVerificationCheckDetail(localDevice, map[string]any{
			"peer_count":          peerCount,
			"syncable_peer_count": syncablePeerCount,
		}))
		result.Peers = append(result.Peers, peerVerificationResult{
			Status:  "error",
			Message: "no syncable peer devices are available for write probes",
			Error:   "no syncable peer devices are available for write probes",
		})
	}

	for _, device := range devices {
		if device.ID == "" || device.ID == localID || device.Role == "local" {
			continue
		}
		peerResult := peerVerificationResult{
			ID:       device.ID,
			Name:     device.Name,
			Platform: device.Platform,
		}
		if reason := peerVerificationSkipReason(device); reason != "" {
			peerResult.Status = "skipped"
			peerResult.Message = reason
			if opts.Strict {
				peerResult.Status = "error"
				peerResult.Error = reason
				result.OK = false
			}
			result.Peers = append(result.Peers, peerResult)
			continue
		}

		verifyResponse, err := c.postJSON("/steward/devices/"+url.PathEscape(device.ID)+"/verify", nil)
		if err != nil {
			peerResult.Status = "error"
			peerResult.Message = "trust verification failed"
			peerResult.Error = err.Error()
			result.OK = false
			result.Peers = append(result.Peers, peerResult)
			continue
		}
		verification := mapAt(verifyResponse, "verification")
		peerResult.Verified = boolAt(verification, "verified")
		if !peerResult.Verified {
			peerResult.Status = "error"
			peerResult.Message = "trust verification did not return verified=true"
			peerResult.SyncSummary = verification
			result.OK = false
			result.Peers = append(result.Peers, peerResult)
			continue
		}

		if opts.Sync {
			syncResponse, err := c.postJSON("/steward/devices/"+url.PathEscape(device.ID)+"/sync", nil)
			if err != nil {
				peerResult.Status = "error"
				peerResult.Message = "trust verified, sync failed"
				peerResult.Error = err.Error()
				result.OK = false
				result.Peers = append(result.Peers, peerResult)
				continue
			}
			peerResult.Synced = true
			peerResult.SyncSummary = compactPeerSyncResult(mapAt(syncResponse, "sync"))
			peerResult.Status = "ok"
			peerResult.Message = "trust verified and sync completed"
			if probe != nil {
				visible, summary, err := c.peerWriteProbeVisible(device, *probe)
				peerResult.ProbeVisible = visible
				peerResult.ProbeSummary = summary
				if err != nil {
					peerResult.Status = "error"
					peerResult.Message = "trust verified and sync completed, probe visibility check failed"
					peerResult.Error = err.Error()
					result.OK = false
					result.Peers = append(result.Peers, peerResult)
					continue
				}
				if !visible {
					peerResult.Status = "error"
					peerResult.Message = "trust verified and sync completed, one or more relation probes are not visible on peer"
					peerResult.Error = "peer probe did not return every synced relation entity"
					result.OK = false
					result.Peers = append(result.Peers, peerResult)
					continue
				}
				peerResult.Message = "trust verified, sync completed, and relation probes are visible on peer"
			}
		} else {
			peerResult.Status = "ok"
			peerResult.Message = "trust verified"
		}
		result.Peers = append(result.Peers, peerResult)
	}

	if peerCount == 0 && opts.RequirePeers {
		add("s3.peers.present", "error", "no peer devices are registered", peerVerificationCheckDetail(localDevice, nil))
		result.Peers = append(result.Peers, peerVerificationResult{
			Status:  "error",
			Message: "no peer devices are registered",
			Error:   "no peer devices are registered",
		})
	} else if opts.RequirePeers {
		add("s3.peers.present", "ok", "peer devices are registered", peerVerificationCheckDetail(localDevice, map[string]any{"peer_count": peerCount}))
	}
	if probe != nil {
		addPeerWriteProbeChecks(&result, localDevice, *probe, syncablePeerIDs)
	}
	return result
}

func (c cli) createPeerSyncWriteProbe() (peerWriteProbe, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	title := "S3 peer sync verification probe " + stamp
	taskPayload := map[string]any{
		"title":            title,
		"description":      "created by steward verify peers --sync --write-probes",
		"source":           "verification",
		"data_level":       "D0",
		"permission_level": "A3",
		"risk_level":       "low",
		"user_confirmed":   true,
	}
	taskResponse, err := c.postJSON("/steward/tasks", taskPayload)
	if err != nil {
		return peerWriteProbe{}, err
	}
	taskID := stringAt(mapAt(taskResponse, "task"), "id")
	if taskID == "" {
		return peerWriteProbe{}, fmt.Errorf("task probe response did not include an id")
	}
	probe := peerWriteProbe{TaskID: taskID, Title: title}
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "task", EntityID: taskID, Label: "task"})

	sourceRefResponse, err := c.postJSON("/steward/source-refs", map[string]any{
		"target_type": "task",
		"target_id":   taskID,
		"source_type": "verification",
		"source_id":   "peer-write-probe",
		"location":    "verify peers --write-probes",
		"summary":     "S3 relation source ref probe " + stamp,
		"confidence":  1,
		"sensitive":   false,
		"displayable": true,
	})
	if err != nil {
		return peerWriteProbe{}, err
	}
	sourceRefID := stringAt(mapAt(sourceRefResponse, "source_ref"), "id")
	if sourceRefID == "" {
		return peerWriteProbe{}, fmt.Errorf("source ref probe response did not include an id")
	}
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "source_ref", EntityID: sourceRefID, Label: "task source reference"})

	tagResponse, err := c.postJSON("/steward/tags", map[string]any{
		"name":        "s3-peer-probe-" + stamp,
		"type":        "verification",
		"color":       "#336699",
		"description": "S3 relation tag probe",
	})
	if err != nil {
		return peerWriteProbe{}, err
	}
	tagID := stringAt(mapAt(tagResponse, "tag"), "id")
	if tagID == "" {
		return peerWriteProbe{}, fmt.Errorf("data tag probe response did not include an id")
	}
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "data_tag", EntityID: tagID, Label: "data tag"})

	if _, err := c.postJSON("/steward/tags/assign", map[string]any{
		"entity_type": "task",
		"entity_id":   taskID,
		"tag_id":      tagID,
		"source":      "verification",
		"confidence":  1,
	}); err != nil {
		return peerWriteProbe{}, err
	}
	entityTagID := verificationEntityTagID("task", taskID, tagID)
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "entity_tag", EntityID: entityTagID, Label: "task tag assignment"})

	eventResponse, err := c.postJSON("/steward/events", map[string]any{
		"title":            "S3 timeline relation probe " + stamp,
		"summary":          "created by steward verify peers --sync --write-probes",
		"source":           "verification",
		"data_level":       "D0",
		"permission_level": "A3",
		"user_confirmed":   true,
	})
	if err != nil {
		return peerWriteProbe{}, err
	}
	eventID := stringAt(mapAt(eventResponse, "event"), "id")
	if eventID == "" {
		return peerWriteProbe{}, fmt.Errorf("event probe response did not include an id")
	}
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "event", EntityID: eventID, Label: "timeline source event"})

	convertResponse, err := c.postJSON("/steward/events/"+url.PathEscape(eventID)+"/convert", map[string]any{
		"target_type": "timeline",
	})
	if err != nil {
		return peerWriteProbe{}, err
	}
	segmentID := stringAt(mapAt(convertResponse, "timeline_segment"), "id")
	if segmentID == "" {
		return peerWriteProbe{}, fmt.Errorf("timeline segment probe response did not include an id")
	}
	probe.Entities = append(probe.Entities, peerEntityProbe{EntityType: "timeline_segment", EntityID: segmentID, Label: "timeline segment"})
	return probe, nil
}

func addPeerWriteProbeChecks(result *peersVerificationResult, local peerVerificationDevice, probe peerWriteProbe, expectedPeerIDs []string) {
	entityTypes := peerProbeEntityTypes(probe)
	allOK := true
	aggregate := map[string]any{
		"probe_task_id":       probe.TaskID,
		"entity_types":        entityTypes,
		"expected_peer_count": len(expectedPeerIDs),
	}
	for _, entityType := range entityTypes {
		visiblePeers := []string{}
		missing := []map[string]string{}
		for _, peerID := range expectedPeerIDs {
			peer, ok := peerVerificationResultByID(result.Peers, peerID)
			if !ok {
				missing = append(missing, map[string]string{
					"peer_device_id": peerID,
					"reason":         "peer verification result missing",
				})
				continue
			}
			if peerProbeSummaryEntityMatched(peer.ProbeSummary, entityType) {
				visiblePeers = append(visiblePeers, peerID)
				continue
			}
			reason := peer.Error
			if reason == "" {
				reason = "entity probe was not visible"
			}
			missing = append(missing, map[string]string{
				"peer_device_id": peer.ID,
				"reason":         reason,
			})
		}
		detail := peerVerificationCheckDetail(local, map[string]any{
			"probe_task_id":       probe.TaskID,
			"entity_type":         entityType,
			"expected_peer_count": len(expectedPeerIDs),
			"visible_peer_count":  len(visiblePeers),
			"visible_peers":       visiblePeers,
			"missing":             missing,
		})
		checkID := "s3.peer_probe." + entityType
		if len(expectedPeerIDs) > 0 && len(missing) == 0 {
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      checkID,
				Status:  "ok",
				Message: "relation probe entity is visible on every syncable peer",
				Detail:  detail,
			})
			continue
		}
		allOK = false
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      checkID,
			Status:  "error",
			Message: "relation probe entity is not visible on every syncable peer",
			Detail:  detail,
		})
	}
	aggregate = peerVerificationCheckDetail(local, aggregate)
	if len(entityTypes) > 0 && len(expectedPeerIDs) > 0 && allOK {
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "s3.peer_probe.relations",
			Status:  "ok",
			Message: "all relation probe entity types are visible on every syncable peer",
			Detail:  aggregate,
		})
		return
	}
	result.OK = false
	result.Checks = append(result.Checks, runtimeVerificationCheck{
		ID:      "s3.peer_probe.relations",
		Status:  "error",
		Message: "one or more relation probe entity types are not visible on every syncable peer",
		Detail:  aggregate,
	})
}

func peerProbeEntityTypes(probe peerWriteProbe) []string {
	types := make([]string, 0, len(probe.Entities))
	seen := map[string]struct{}{}
	for _, entity := range probe.Entities {
		entityType := strings.TrimSpace(entity.EntityType)
		if entityType == "" {
			continue
		}
		if _, ok := seen[entityType]; ok {
			continue
		}
		seen[entityType] = struct{}{}
		types = append(types, entityType)
	}
	return types
}

func peerVerificationResultByID(peers []peerVerificationResult, id string) (peerVerificationResult, bool) {
	for _, peer := range peers {
		if peer.ID == id {
			return peer, true
		}
	}
	return peerVerificationResult{}, false
}

func peerProbeSummaryEntityMatched(summary any, entityType string) bool {
	for _, item := range peerProbeSummaryEntities(summary) {
		if stringAt(item, "entity_type") == entityType && boolAt(item, "matched") {
			return true
		}
	}
	return false
}

func peerProbeSummaryEntities(summary any) []map[string]any {
	rawSummary, ok := summary.(map[string]any)
	if !ok {
		return nil
	}
	rawEntities, ok := rawSummary["entities"]
	if !ok {
		return nil
	}
	switch entities := rawEntities.(type) {
	case []map[string]any:
		return entities
	case []any:
		out := make([]map[string]any, 0, len(entities))
		for _, raw := range entities {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func peerVerificationCheckDetail(local peerVerificationDevice, extra map[string]any) map[string]any {
	detail := map[string]any{}
	if local.AgentID != "" {
		detail["agent_id"] = local.AgentID
	}
	if local.Platform != "" {
		detail["platform"] = local.Platform
	}
	if local.Name != "" {
		detail["device_name"] = local.Name
	}
	for key, value := range extra {
		detail[key] = value
	}
	return detail
}

func peerVerificationDeviceFromMap(raw map[string]any) peerVerificationDevice {
	return peerVerificationDevice{
		AgentID:  stringAt(raw, "id"),
		Name:     stringAt(raw, "device_name"),
		Platform: strings.ToLower(strings.TrimSpace(stringAt(raw, "platform"))),
	}
}

func verificationEntityTagID(entityType string, entityID string, tagID string) string {
	key := strings.Join([]string{
		"steward_entity_tag",
		strings.TrimSpace(entityType),
		strings.TrimSpace(entityID),
		strings.TrimSpace(tagID),
	}, ":")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func (c cli) peerWriteProbeVisible(device peerDevice, probe peerWriteProbe) (bool, map[string]any, error) {
	endpoint := c.apiBase + "/steward/devices/" + url.PathEscape(device.ID) + "/probe"
	summary := map[string]any{
		"task_id":  probe.TaskID,
		"title":    probe.Title,
		"endpoint": endpoint,
	}
	if len(probe.Entities) == 0 {
		summary["matched"] = false
		return false, summary, fmt.Errorf("peer write probe did not include any entities")
	}
	items := []map[string]any{}
	allVisible := true
	for _, entity := range probe.Entities {
		visible, item, err := c.peerEntityProbeVisible(device, entity)
		items = append(items, item)
		if err != nil {
			summary["entities"] = items
			summary["matched"] = false
			return false, summary, err
		}
		if !visible {
			allVisible = false
		}
	}
	summary["entities"] = items
	summary["matched"] = allVisible
	return allVisible, summary, nil
}

func (c cli) peerEntityProbeVisible(device peerDevice, entity peerEntityProbe) (bool, map[string]any, error) {
	response, err := c.postJSON("/steward/devices/"+url.PathEscape(device.ID)+"/probe", map[string]string{
		"entity_type": entity.EntityType,
		"entity_id":   entity.EntityID,
	})
	item := map[string]any{
		"entity_type": entity.EntityType,
		"entity_id":   entity.EntityID,
		"label":       entity.Label,
	}
	if err != nil {
		item["matched"] = false
		return false, item, err
	}
	remoteProbe := mapAt(response, "probe")
	visible := boolAt(remoteProbe, "exists") &&
		stringAt(remoteProbe, "entity_type") == entity.EntityType &&
		stringAt(remoteProbe, "entity_id") == entity.EntityID &&
		stringAt(remoteProbe, "device_id") == device.ID
	item["matched"] = visible
	if detail := remoteProbe["detail"]; detail != nil {
		item["detail"] = detail
	}
	return visible, item, nil
}
