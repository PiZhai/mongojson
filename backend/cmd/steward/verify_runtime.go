package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

func (c cli) verifyRuntime(args []string) error {
	fs := flag.NewFlagSet("steward verify runtime", flag.ExitOnError)
	opts := runtimeVerifyOptions{}
	var expectSyncPreviousKeyCount optionalIntFlag
	var expectLocalPreviousKeyCount optionalIntFlag
	evidenceDir := fs.String("evidence-dir", "", "Write a timestamped verification evidence JSON file to this directory")
	fs.BoolVar(&opts.WriteProbes, "write-probes", false, "Create low-risk event/task probes to verify sync queue and autonomy paths")
	fs.BoolVar(&opts.StrictSecurity, "strict-security", false, "Fail when sync security or enabled S4 advisor runtime safety is incomplete")
	fs.BoolVar(&opts.AdvisorProbe, "advisor-probe", false, "Call the configured S4 autonomy advisor with a D0 live probe")
	fs.BoolVar(&opts.AdvisorProbeEachSample, "advisor-probe-each-sample", false, "When used with --advisor-probe and --watch-duration, call the S4 advisor in every watch sample")
	fs.BoolVar(&opts.AdvisorPrivacyProbe, "advisor-privacy-probe", false, "Verify the S4 autonomy advisor rejects a D2 privacy probe before model submission")
	fs.IntVar(&opts.AutonomyLimit, "autonomy-limit", 5, "Autonomy scan limit when write probes are enabled")
	fs.StringVar(&opts.ExpectAgentID, "expect-agent-id", "", "Fail unless the runtime reports this local steward agent id")
	fs.StringVar(&opts.ExpectAgentVersion, "expect-agent-version", "", "Fail unless the runtime reports this steward agent version")
	fs.StringVar(&opts.ExpectAgentPlatform, "expect-agent-platform", "", "Fail unless the runtime reports this steward agent platform")
	fs.StringVar(&opts.ExpectAdvisorProvider, "expect-advisor-provider", "", "Fail unless the S4 advisor reports this provider")
	fs.StringVar(&opts.ExpectAdvisorModel, "expect-advisor-model", "", "Fail unless the S4 advisor reports this model")
	fs.StringVar(&opts.ExpectAdvisorMaxDataLevel, "expect-advisor-max-data-level", "", "Fail unless the S4 advisor reports this max data level")
	fs.StringVar(&opts.ExpectSyncKeyID, "expect-sync-key-id", "", "Fail unless S3 sync security reports this sync encryption key id")
	fs.StringVar(&opts.ExpectLocalKeyID, "expect-local-key-id", "", "Fail unless S3 sync security reports this local encryption key id")
	fs.Var(&expectSyncPreviousKeyCount, "expect-sync-previous-key-count", "Fail unless this many previous sync encryption keys are loaded")
	fs.Var(&expectLocalPreviousKeyCount, "expect-local-previous-key-count", "Fail unless this many previous local encryption keys are loaded")
	fs.DurationVar(&opts.WatchDuration, "watch-duration", 0, "Repeat runtime verification for this duration; 0 runs once")
	fs.DurationVar(&opts.WatchInterval, "watch-interval", time.Minute, "Interval between runtime verification samples when --watch-duration is set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.ExpectSyncPreviousKeyCount = expectSyncPreviousKeyCount.ptr()
	opts.ExpectLocalPreviousKeyCount = expectLocalPreviousKeyCount.ptr()
	if opts.AutonomyLimit <= 0 || opts.AutonomyLimit > 50 {
		opts.AutonomyLimit = 5
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = time.Minute
	}
	if err := validateAdvisorProbeEachSample(opts.AdvisorProbeEachSample, opts.AdvisorProbe, opts.WatchDuration, "verify runtime"); err != nil {
		return err
	}

	result := c.runRuntimeVerificationCommand(opts)
	if err := printVerificationResult("runtime", *evidenceDir, result, result.OK); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("runtime verification failed")
	}
	return nil
}

func (c cli) runRuntimeVerificationCommand(opts runtimeVerifyOptions) runtimeVerificationResult {
	if opts.WatchDuration > 0 {
		return c.runRuntimeVerificationWatch(opts)
	}
	return c.runRuntimeVerification(opts)
}

func (c cli) runRuntimeVerificationWatch(opts runtimeVerifyOptions) runtimeVerificationResult {
	deadline := time.Now().Add(opts.WatchDuration)
	samples := []runtimeVerificationSample{}
	for {
		startedAt := time.Now().UTC()
		sampleOptions := runtimeWatchSampleOptions(opts, len(samples)+1)
		result := c.runRuntimeVerification(sampleOptions)
		completedAt := time.Now().UTC()
		samples = append(samples, runtimeVerificationSample{
			Index:       len(samples) + 1,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			OK:          result.OK,
			Checks:      result.Checks,
			Artifacts:   result.Artifacts,
		})
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleepFor := opts.WatchInterval
		if remaining < sleepFor {
			sleepFor = remaining
		}
		time.Sleep(sleepFor)
	}
	return buildRuntimeWatchVerificationResult(c.apiBase, opts, samples)
}

func runtimeWatchSampleOptions(opts runtimeVerifyOptions, sampleIndex int) runtimeVerifyOptions {
	opts.WatchDuration = 0
	if sampleIndex > 1 {
		keepAdvisorProbe := opts.AdvisorProbeEachSample && opts.AdvisorProbe
		opts = disableRuntimeActiveProbes(opts)
		if keepAdvisorProbe {
			opts.AdvisorProbe = true
		}
	}
	return opts
}

func buildRuntimeWatchVerificationResult(apiBase string, opts runtimeVerifyOptions, samples []runtimeVerificationSample) runtimeVerificationResult {
	result := runtimeVerificationResult{
		OK:        len(samples) > 0,
		APIBase:   apiBase,
		Options:   opts,
		Checks:    []runtimeVerificationCheck{},
		Artifacts: map[string]string{},
		Samples:   samples,
	}
	if len(samples) == 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: "runtime.watch", Status: "error", Message: "no runtime verification samples were collected"})
		return result
	}
	result.Artifacts = mergeRuntimeWatchArtifacts(samples)
	failures := 0
	for _, sample := range samples {
		if !sample.OK {
			failures++
		}
	}
	if failures > 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "runtime.watch",
			Status:  "error",
			Message: fmt.Sprintf("%d of %d runtime verification samples failed", failures, len(samples)),
			Detail:  map[string]int{"samples": len(samples), "failures": failures},
		})
		return result
	}
	addRuntimeWatchHeartbeatCheck(&result, samples)
	result.Checks = append(result.Checks, runtimeVerificationCheck{
		ID:      "runtime.watch",
		Status:  "ok",
		Message: fmt.Sprintf("%d runtime verification samples passed", len(samples)),
		Detail:  map[string]int{"samples": len(samples), "failures": 0},
	})
	return result
}

func mergeRuntimeWatchArtifacts(samples []runtimeVerificationSample) map[string]string {
	merged := map[string]string{}
	for _, sample := range samples {
		for key, value := range sample.Artifacts {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				continue
			}
			merged[key] = value
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func addRuntimeWatchHeartbeatCheck(result *runtimeVerificationResult, samples []runtimeVerificationSample) {
	if len(samples) < 2 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "runtime.watch.heartbeat",
			Status:  "error",
			Message: "at least two runtime verification samples are required to prove agent heartbeat advance",
			Detail:  map[string]int{"samples": len(samples)},
		})
		return
	}
	first, ok := runtimeSampleHeartbeatAt(samples[0])
	if !ok {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "runtime.watch.heartbeat",
			Status:  "error",
			Message: "agent heartbeat timestamp was not visible in the first watch sample",
			Detail:  map[string]int{"sample": samples[0].Index},
		})
		return
	}
	lastSample := samples[len(samples)-1]
	last, ok := runtimeSampleHeartbeatAt(lastSample)
	if !ok {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "runtime.watch.heartbeat",
			Status:  "error",
			Message: "agent heartbeat timestamp was not visible in the last watch sample",
			Detail:  map[string]int{"sample": lastSample.Index},
		})
		return
	}
	delta := last.Sub(first)
	if delta <= 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "runtime.watch.heartbeat",
			Status:  "error",
			Message: "agent heartbeat did not advance during the watch window",
			Detail: map[string]any{
				"first": first.Format(time.RFC3339Nano),
				"last":  last.Format(time.RFC3339Nano),
			},
		})
		return
	}
	result.Checks = append(result.Checks, runtimeVerificationCheck{
		ID:      "runtime.watch.heartbeat",
		Status:  "ok",
		Message: "agent heartbeat advanced during the watch window",
		Detail: map[string]any{
			"first":    first.Format(time.RFC3339Nano),
			"last":     last.Format(time.RFC3339Nano),
			"delta_ms": delta.Milliseconds(),
		},
	})
}

func validateAdvisorProbeEachSample(enabled bool, advisorProbe bool, watchDuration time.Duration, command string) error {
	if !enabled {
		return nil
	}
	if !advisorProbe {
		return fmt.Errorf("%s --advisor-probe-each-sample requires --advisor-probe", command)
	}
	if watchDuration <= 0 {
		return fmt.Errorf("%s --advisor-probe-each-sample requires --watch-duration", command)
	}
	return nil
}

func disableRuntimeActiveProbes(opts runtimeVerifyOptions) runtimeVerifyOptions {
	opts.WriteProbes = false
	opts.AdvisorProbe = false
	opts.AdvisorPrivacyProbe = false
	return opts
}

func (c cli) runRuntimeVerification(opts runtimeVerifyOptions) runtimeVerificationResult {
	result := runtimeVerificationResult{
		OK:        true,
		APIBase:   c.apiBase,
		Options:   opts,
		Checks:    []runtimeVerificationCheck{},
		Artifacts: map[string]string{},
	}
	add := func(id string, status string, message string, detail any) {
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: id, Status: status, Message: message, Detail: detail})
		if status == "error" {
			result.OK = false
		}
	}

	root := strings.TrimSuffix(c.apiBase, "/api")
	if payload, err := c.getJSONURL(root + "/healthz"); err != nil {
		add("http.healthz", "error", err.Error(), nil)
	} else {
		add("http.healthz", "ok", "health endpoint is reachable", payload)
	}
	if payload, err := c.getJSONURL(root + "/readyz"); err != nil {
		add("http.readyz", "error", err.Error(), nil)
	} else {
		add("http.readyz", "ok", "readiness endpoint is reachable", payload)
	}

	agent, err := c.getJSON("/steward/agent")
	if err != nil {
		add("steward.agent", "error", err.Error(), nil)
	} else {
		agentPayload := mapAt(agent, "agent")
		status := stringAt(agentPayload, "status")
		if status == "running" || status == "degraded" {
			add("steward.agent", "ok", "agent status is visible", agentPayload)
		} else {
			add("steward.agent", "error", "agent is not running", agentPayload)
		}
		if strings.TrimSpace(opts.ExpectAgentID) != "" {
			if got := stringAt(agentPayload, "agent_id"); got == strings.TrimSpace(opts.ExpectAgentID) {
				add("steward.agent.expected", "ok", "runtime reports the expected agent id", map[string]string{"agent_id": got})
			} else {
				add("steward.agent.expected", "error", "runtime agent id does not match expected value", map[string]string{
					"expected": strings.TrimSpace(opts.ExpectAgentID),
					"actual":   got,
				})
			}
		}
		if strings.TrimSpace(opts.ExpectAgentVersion) != "" {
			if got := stringAt(agentPayload, "version"); got == strings.TrimSpace(opts.ExpectAgentVersion) {
				add("steward.agent.expected_version", "ok", "runtime reports the expected agent version", map[string]string{"version": got})
			} else {
				add("steward.agent.expected_version", "error", "runtime agent version does not match expected value", map[string]string{
					"expected": strings.TrimSpace(opts.ExpectAgentVersion),
					"actual":   got,
				})
			}
		}
		if strings.TrimSpace(opts.ExpectAgentPlatform) != "" {
			addExpectedStringCheck(add, "steward.agent.expected_platform", "agent platform", opts.ExpectAgentPlatform, stringAt(agentPayload, "platform"))
		}
	}

	syncStatus, err := c.getJSON("/steward/sync/status")
	if err != nil {
		add("s3.sync.status", "error", err.Error(), nil)
	} else {
		syncPayload := mapAt(syncStatus, "sync")
		security := mapAt(syncPayload, "security")
		configErrors := stringSliceAt(security, "config_errors")
		if len(configErrors) > 0 {
			add("s3.sync.security.config", "error", "sync security configuration has errors", configErrors)
		} else {
			add("s3.sync.security.config", "ok", "sync security configuration is parseable", security)
		}
		if opts.StrictSecurity {
			strictMissing := missingStrictSecurityItems(security)
			if len(strictMissing) > 0 {
				add("s3.sync.security.strict", "error", "strict sync security requirements are not met", strictMissing)
			} else {
				add("s3.sync.security.strict", "ok", "strict sync security requirements are met", nil)
			}
		} else {
			add("s3.sync.status", "ok", "sync status is visible", compactSyncStatus(syncPayload))
		}
		discovery := mapAt(syncPayload, "discovery")
		discoveredPeers := sliceAt(syncPayload, "discovered_peers")
		if boolAt(discovery, "enabled") {
			invalidSignatures := 0
			for _, item := range discoveredPeers {
				peer, _ := item.(map[string]any)
				if !boolAt(peer, "signature_verified") {
					invalidSignatures++
				}
			}
			reportedCandidates := intAt(discovery, "candidate_count")
			lastError := strings.TrimSpace(stringAt(discovery, "last_error"))
			lastAnnouncementAt := strings.TrimSpace(stringAt(discovery, "last_announcement_at"))
			if !boolAt(discovery, "running") || invalidSignatures > 0 || reportedCandidates != len(discoveredPeers) || lastError != "" || lastAnnouncementAt == "" {
				add("s3.discovery.status", "error", "signed peer discovery is enabled but unhealthy", map[string]any{
					"discovery":                discovery,
					"invalid_signatures":       invalidSignatures,
					"reported_candidates":      reportedCandidates,
					"visible_candidates":       len(discoveredPeers),
					"last_error":               lastError,
					"last_announcement_at_set": lastAnnouncementAt != "",
				})
			} else {
				add("s3.discovery.status", "ok", "signed peer discovery is running and exposes only verified candidates", map[string]any{
					"discovery":  discovery,
					"candidates": len(discoveredPeers),
				})
			}
		} else {
			add("s3.discovery.status", "ok", "signed peer discovery is disabled", discovery)
		}
		addRuntimeExpectedSyncSecurityChecks(opts, security, add)
	}

	autonomy, err := c.getJSON("/steward/autonomy")
	if err != nil {
		add("s4.autonomy.status", "error", err.Error(), nil)
	} else {
		payload := mapAt(autonomy, "autonomy")
		settings := mapAt(payload, "settings")
		advisor := mapAt(payload, "advisor")
		rules := sliceAt(payload, "rules")
		if len(rules) == 0 {
			add("s4.autonomy.rules", "error", "no autonomy rules are configured", payload)
		} else {
			add("s4.autonomy.status", "ok", "autonomy settings and rules are visible", map[string]any{
				"settings": settings,
				"rules":    len(rules),
			})
		}
		if len(advisor) > 0 {
			add("s4.advisor.status", "ok", "autonomy advisor status is visible", advisor)
			if opts.StrictSecurity {
				strictIssues := strictAdvisorRuntimeIssues(advisor)
				if len(strictIssues) > 0 {
					add("s4.advisor.strict", "error", "strict advisor runtime requirements are not met", strictIssues)
				} else {
					add("s4.advisor.strict", "ok", "strict advisor runtime requirements are met", nil)
				}
			}
		}
		addRuntimeExpectedAdvisorChecks(opts, advisor, add)
	}

	if opts.WriteProbes {
		c.runRuntimeWriteProbes(opts, &result, add)
	}
	if opts.AdvisorProbe {
		c.runRuntimeAdvisorProbe(&result, add)
	}
	if opts.AdvisorPrivacyProbe {
		c.runRuntimeAdvisorPrivacyProbe(&result, add)
	}
	if len(result.Artifacts) == 0 {
		result.Artifacts = nil
	}
	return result
}

func (c cli) runRuntimeWriteProbes(opts runtimeVerifyOptions, result *runtimeVerificationResult, add func(string, string, string, any)) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	taskPayload := map[string]any{
		"title":            "S3 runtime verification probe " + stamp,
		"description":      "created by steward verify runtime --write-probes",
		"source":           "verification",
		"data_level":       "D0",
		"permission_level": "A3",
		"risk_level":       "low",
		"user_confirmed":   true,
	}
	taskResponse, err := c.postJSON("/steward/tasks", taskPayload)
	if err != nil {
		add("s3.write.task", "error", err.Error(), nil)
		return
	}
	task := mapAt(taskResponse, "task")
	taskID := stringAt(task, "id")
	if taskID == "" {
		add("s3.write.task", "error", "task probe response did not include an id", taskResponse)
		return
	}
	result.Artifacts["task_id"] = taskID
	add("s3.write.task", "ok", "low-risk task probe was created", map[string]string{"task_id": taskID})

	if syncStatus, err := c.getJSON("/steward/sync/status"); err != nil {
		add("s3.write.sync_queue", "error", err.Error(), nil)
	} else if syncRecentChangesContain(mapAt(syncStatus, "sync"), "task", taskID) {
		add("s3.write.sync_queue", "ok", "task probe produced a sync change", map[string]string{"task_id": taskID})
	} else {
		add("s3.write.sync_queue", "error", "task probe did not appear in recent sync changes", compactSyncStatus(mapAt(syncStatus, "sync")))
	}

	eventPayload := map[string]any{
		"title":            "S4 autonomy verification probe " + stamp,
		"summary":          "created by steward verify runtime --write-probes",
		"source":           "verification",
		"data_level":       "D0",
		"permission_level": "A3",
		"user_confirmed":   true,
	}
	eventResponse, err := c.postJSON("/steward/events", eventPayload)
	if err != nil {
		add("s4.write.event", "error", err.Error(), nil)
		return
	}
	event := mapAt(eventResponse, "event")
	eventID := stringAt(event, "id")
	if eventID == "" {
		add("s4.write.event", "error", "event probe response did not include an id", eventResponse)
		return
	}
	result.Artifacts["event_id"] = eventID
	add("s4.write.event", "ok", "low-risk event probe was created", map[string]string{"event_id": eventID})

	autonomyResponse, err := c.postJSON(fmt.Sprintf("/steward/autonomy/run?limit=%d", opts.AutonomyLimit), nil)
	if err != nil {
		add("s4.write.autonomy_run", "error", err.Error(), nil)
		return
	}
	proposalID := proposalForSourceEntity(mapAt(autonomyResponse, "autonomy"), eventID)
	if proposalID == "" {
		add("s4.write.autonomy_run", "error", "autonomy run did not create a proposal for the event probe", mapAt(autonomyResponse, "autonomy"))
		return
	}
	result.Artifacts["proposal_id"] = proposalID
	add("s4.write.autonomy_run", "ok", "autonomy run created a proposal for the event probe", map[string]string{"proposal_id": proposalID})

	if _, err := c.postJSON("/steward/autonomy/proposals/"+proposalID+"/dismiss", nil); err != nil {
		add("s4.write.proposal_cleanup", "error", err.Error(), map[string]string{"proposal_id": proposalID})
	} else {
		add("s4.write.proposal_cleanup", "ok", "event probe proposal was dismissed after verification", map[string]string{"proposal_id": proposalID})
	}
}

func (c cli) runRuntimeAdvisorProbe(result *runtimeVerificationResult, add func(string, string, string, any)) {
	probePayload := map[string]any{
		"kind":               "verification_probe",
		"source_entity_type": "verification",
		"title":              "S4 advisor live verification probe",
		"summary":            "D0 low-risk probe created by steward verify runtime --advisor-probe",
		"data_level":         "D0",
		"rule_name":          "advisor-live-probe",
		"rule_scope":         "local D0 verification only",
	}
	response, err := c.postJSON("/steward/autonomy/advisor/probe", probePayload)
	if err != nil {
		add("s4.advisor.probe", "error", err.Error(), nil)
		return
	}
	probe := mapAt(response, "probe")
	if !boolAt(probe, "ok") {
		add("s4.advisor.probe", "error", defaultString(stringAt(probe, "error"), "advisor probe failed"), probe)
		return
	}
	result.Artifacts["advisor_probe_at"] = stringAt(probe, "probed_at")
	add("s4.advisor.probe", "ok", "configured autonomy advisor responded to a D0 live probe", compactAdvisorProbe(probe))
}

func (c cli) runRuntimeAdvisorPrivacyProbe(result *runtimeVerificationResult, add func(string, string, string, any)) {
	probePayload := map[string]any{
		"kind":               "privacy_gate_probe",
		"source_entity_type": "verification",
		"title":              "S4 advisor privacy gate verification probe",
		"summary":            "D2 probe created by steward verify runtime --advisor-privacy-probe; it must be rejected before model submission.",
		"data_level":         "D2",
		"rule_name":          "advisor-privacy-probe",
		"rule_scope":         "privacy gate verification only",
	}
	response, err := c.postJSON("/steward/autonomy/advisor/probe", probePayload)
	if err != nil {
		add("s4.advisor.privacy_probe", "error", err.Error(), nil)
		return
	}
	probe := mapAt(response, "probe")
	if boolAt(probe, "ok") {
		add("s4.advisor.privacy_probe", "error", "advisor accepted a D2 privacy probe; lower STEWARD_LLM_MAX_DATA_LEVEL before enabling S4 model suggestions", compactAdvisorProbe(probe))
		return
	}
	errorText := stringAt(probe, "error")
	if strings.Contains(strings.ToLower(errorText), "exceeds advisor max") {
		result.Artifacts["advisor_privacy_probe_at"] = stringAt(probe, "probed_at")
		add("s4.advisor.privacy_probe", "ok", "advisor rejected D2 data before model submission", map[string]any{
			"data_level": stringAt(probe, "data_level"),
			"error":      errorText,
			"status":     mapAt(probe, "status"),
		})
		return
	}
	add("s4.advisor.privacy_probe", "error", defaultString(errorText, "advisor privacy probe did not fail with the expected data-level guardrail"), probe)
}
