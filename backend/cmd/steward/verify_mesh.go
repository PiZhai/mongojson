package main

import (
	"flag"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func (c cli) verifyMesh(args []string) error {
	fs := flag.NewFlagSet("steward verify mesh", flag.ExitOnError)
	opts := meshVerifyOptions{AutonomyLimit: 5}
	var nodes stringListFlag
	var expectAgentIDs stringListFlag
	var expectAgentVersions stringListFlag
	var expectAgentPlatforms stringListFlag
	var expectSyncKeyIDs stringListFlag
	var expectLocalKeyIDs stringListFlag
	evidenceDir := fs.String("evidence-dir", "", "Write a timestamped verification evidence JSON file to this directory")
	fs.Var(&nodes, "node", "Steward API base URL for a node; repeat for Windows/macOS/Linux nodes")
	fs.BoolVar(&opts.StrictSecurity, "strict-security", false, "Fail when runtime sync security or enabled S4 advisor safety is incomplete")
	fs.BoolVar(&opts.StrictPeers, "strict", false, "Fail when any registered peer is not verifiable")
	fs.BoolVar(&opts.RequirePeers, "require-peers", false, "Fail each node when no peer devices are registered")
	fs.BoolVar(&opts.Sync, "sync", false, "Run one peer sync from each node after trust verification")
	fs.BoolVar(&opts.WriteProbes, "write-probes", false, "Create low-risk relation probes and verify cross-peer visibility")
	fs.BoolVar(&opts.AdvisorProbe, "advisor-probe", false, "Call each node's configured S4 autonomy advisor with a D0 live probe")
	fs.BoolVar(&opts.AdvisorProbeEachSample, "advisor-probe-each-sample", false, "When used with --advisor-probe and --watch-duration, call each node's S4 advisor in every watch sample")
	fs.BoolVar(&opts.AdvisorPrivacyProbe, "advisor-privacy-probe", false, "Verify each node rejects a D2 advisor privacy probe before model submission")
	fs.Var(&expectAgentIDs, "expect-agent-id", "Fail node unless it reports this agent id; repeat once per --node or once for all nodes")
	fs.Var(&expectAgentVersions, "expect-agent-version", "Fail node unless it reports this agent version; repeat once per --node or once for all nodes")
	fs.Var(&expectAgentPlatforms, "expect-agent-platform", "Fail node unless it reports this platform; repeat once per --node or once for all nodes")
	fs.Var(&expectSyncKeyIDs, "expect-sync-key-id", "Fail node unless it reports this sync encryption key id; repeat once per --node or once for all nodes")
	fs.Var(&expectLocalKeyIDs, "expect-local-key-id", "Fail node unless it reports this local encryption key id; repeat once per --node or once for all nodes")
	fs.StringVar(&opts.ExpectAdvisorProvider, "expect-advisor-provider", "", "Fail each node unless the S4 advisor reports this provider")
	fs.StringVar(&opts.ExpectAdvisorModel, "expect-advisor-model", "", "Fail each node unless the S4 advisor reports this model")
	fs.StringVar(&opts.ExpectAdvisorMaxDataLevel, "expect-advisor-max-data-level", "", "Fail each node unless the S4 advisor reports this max data level")
	fs.IntVar(&opts.AutonomyLimit, "autonomy-limit", 5, "Runtime autonomy scan limit if future mesh write probes need it")
	fs.DurationVar(&opts.WatchDuration, "watch-duration", 0, "Repeat mesh verification for this duration; 0 runs once")
	fs.DurationVar(&opts.WatchInterval, "watch-interval", time.Minute, "Interval between mesh verification samples when --watch-duration is set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.WriteProbes && !opts.Sync {
		return fmt.Errorf("verify mesh --write-probes requires --sync")
	}
	if opts.AutonomyLimit <= 0 || opts.AutonomyLimit > 50 {
		opts.AutonomyLimit = 5
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = time.Minute
	}
	if err := validateAdvisorProbeEachSample(opts.AdvisorProbeEachSample, opts.AdvisorProbe, opts.WatchDuration, "verify mesh"); err != nil {
		return err
	}
	opts.ExpectAgentIDs = []string(expectAgentIDs)
	opts.ExpectAgentVersions = []string(expectAgentVersions)
	opts.ExpectAgentPlatforms = []string(expectAgentPlatforms)
	opts.ExpectSyncKeyIDs = []string(expectSyncKeyIDs)
	opts.ExpectLocalKeyIDs = []string(expectLocalKeyIDs)
	rawNodes := []string(nodes)
	if len(rawNodes) == 0 {
		rawNodes = []string{c.apiBase}
	}
	normalizedNodes := make([]string, 0, len(rawNodes))
	for _, raw := range rawNodes {
		apiBase, err := normalizeMeshAPIBase(raw)
		if err != nil {
			return err
		}
		normalizedNodes = append(normalizedNodes, apiBase)
	}
	opts.NodeAPIs = normalizedNodes

	result := c.runMeshVerification(opts)
	if err := printVerificationResult("mesh", *evidenceDir, result, result.OK); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("mesh verification failed")
	}
	return nil
}

func (c cli) runMeshVerification(opts meshVerifyOptions) meshVerificationResult {
	if err := validateMeshExpectationCounts(opts); err != nil {
		return meshVerificationResult{
			OK:      false,
			Options: opts,
			Nodes:   []meshNodeVerificationResult{},
			Checks: []runtimeVerificationCheck{{
				ID:      "mesh.options",
				Status:  "error",
				Message: err.Error(),
			}},
		}
	}
	if opts.WatchDuration > 0 {
		return c.runMeshVerificationWatch(opts)
	}
	return c.runSingleMeshVerification(opts)
}

func (c cli) runSingleMeshVerification(opts meshVerifyOptions) meshVerificationResult {
	result := meshVerificationResult{
		OK:      len(opts.NodeAPIs) > 0,
		Options: opts,
		Nodes:   []meshNodeVerificationResult{},
	}
	peerOptions := peerVerifyOptions{
		Sync:         opts.Sync,
		Strict:       opts.StrictPeers,
		RequirePeers: opts.RequirePeers,
		WriteProbes:  opts.WriteProbes,
	}
	for index, apiBase := range opts.NodeAPIs {
		nodeCLI := cli{apiBase: apiBase, client: c.client}
		runtimeOptions := meshRuntimeOptionsForNode(opts, index)
		runtime := nodeCLI.runRuntimeVerification(runtimeOptions)
		peers := nodeCLI.runPeersVerification(peerOptions)
		node := meshNodeVerificationResult{
			OK:      runtime.OK && peers.OK,
			APIBase: apiBase,
			Runtime: runtime,
			Peers:   peers,
		}
		if !node.OK {
			result.OK = false
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

func (c cli) runMeshVerificationWatch(opts meshVerifyOptions) meshVerificationResult {
	deadline := time.Now().Add(opts.WatchDuration)
	samples := []meshVerificationSample{}
	for {
		startedAt := time.Now().UTC()
		sampleOptions := meshWatchSampleOptions(opts, len(samples)+1)
		result := c.runSingleMeshVerification(sampleOptions)
		completedAt := time.Now().UTC()
		samples = append(samples, meshVerificationSample{
			Index:       len(samples) + 1,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			OK:          result.OK,
			Nodes:       result.Nodes,
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
	return buildMeshWatchVerificationResult(opts, samples)
}

func meshWatchSampleOptions(opts meshVerifyOptions, sampleIndex int) meshVerifyOptions {
	opts.WatchDuration = 0
	if sampleIndex > 1 {
		keepAdvisorProbe := opts.AdvisorProbeEachSample && opts.AdvisorProbe
		opts.WriteProbes = false
		opts.AdvisorProbe = false
		opts.AdvisorPrivacyProbe = false
		if keepAdvisorProbe {
			opts.AdvisorProbe = true
		}
	}
	return opts
}

func validateMeshExpectationCounts(opts meshVerifyOptions) error {
	nodeCount := len(opts.NodeAPIs)
	for _, item := range []struct {
		flag   string
		values []string
	}{
		{flag: "--expect-agent-id", values: opts.ExpectAgentIDs},
		{flag: "--expect-agent-version", values: opts.ExpectAgentVersions},
		{flag: "--expect-agent-platform", values: opts.ExpectAgentPlatforms},
		{flag: "--expect-sync-key-id", values: opts.ExpectSyncKeyIDs},
		{flag: "--expect-local-key-id", values: opts.ExpectLocalKeyIDs},
	} {
		if len(item.values) == 0 || len(item.values) == 1 || len(item.values) == nodeCount {
			continue
		}
		return fmt.Errorf("verify mesh %s must be provided once for all nodes or once per node; got %d values for %d nodes", item.flag, len(item.values), nodeCount)
	}
	return nil
}

func meshRuntimeOptionsForNode(opts meshVerifyOptions, index int) runtimeVerifyOptions {
	return runtimeVerifyOptions{
		StrictSecurity:            opts.StrictSecurity,
		AdvisorProbe:              opts.AdvisorProbe,
		AdvisorProbeEachSample:    opts.AdvisorProbeEachSample,
		AdvisorPrivacyProbe:       opts.AdvisorPrivacyProbe,
		ExpectAgentID:             meshExpectedValueAt(opts.ExpectAgentIDs, index),
		ExpectAgentVersion:        meshExpectedValueAt(opts.ExpectAgentVersions, index),
		ExpectAgentPlatform:       meshExpectedValueAt(opts.ExpectAgentPlatforms, index),
		ExpectSyncKeyID:           meshExpectedValueAt(opts.ExpectSyncKeyIDs, index),
		ExpectLocalKeyID:          meshExpectedValueAt(opts.ExpectLocalKeyIDs, index),
		ExpectAdvisorProvider:     opts.ExpectAdvisorProvider,
		ExpectAdvisorModel:        opts.ExpectAdvisorModel,
		ExpectAdvisorMaxDataLevel: opts.ExpectAdvisorMaxDataLevel,
		AutonomyLimit:             opts.AutonomyLimit,
	}
}

func meshExpectedValueAt(values []string, index int) string {
	if len(values) == 0 {
		return ""
	}
	if len(values) == 1 {
		return strings.TrimSpace(values[0])
	}
	if index >= 0 && index < len(values) {
		return strings.TrimSpace(values[index])
	}
	return ""
}

func buildMeshWatchVerificationResult(opts meshVerifyOptions, samples []meshVerificationSample) meshVerificationResult {
	result := meshVerificationResult{
		OK:      len(samples) > 0,
		Options: opts,
		Nodes:   []meshNodeVerificationResult{},
		Checks:  []runtimeVerificationCheck{},
		Samples: samples,
	}
	if len(samples) == 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: "mesh.watch", Status: "error", Message: "no mesh verification samples were collected"})
		return result
	}
	last := samples[len(samples)-1]
	result.Nodes = last.Nodes
	failures := 0
	for _, sample := range samples {
		if !sample.OK {
			failures++
		}
	}
	if failures > 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "mesh.watch",
			Status:  "error",
			Message: fmt.Sprintf("%d of %d mesh verification samples failed", failures, len(samples)),
			Detail:  map[string]int{"samples": len(samples), "failures": failures},
		})
		return result
	}
	addMeshWatchHeartbeatChecks(&result, opts.NodeAPIs, samples)
	result.Checks = append(result.Checks, runtimeVerificationCheck{
		ID:      "mesh.watch",
		Status:  "ok",
		Message: fmt.Sprintf("%d mesh verification samples passed", len(samples)),
		Detail:  map[string]int{"samples": len(samples), "failures": 0},
	})
	return result
}

func addMeshWatchHeartbeatChecks(result *meshVerificationResult, nodeAPIs []string, samples []meshVerificationSample) {
	if len(samples) < 2 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "mesh.watch.heartbeat",
			Status:  "error",
			Message: "at least two mesh verification samples are required to prove agent heartbeat advance",
			Detail:  map[string]int{"samples": len(samples)},
		})
		return
	}
	expectedNodeAPIs := nodeAPIs
	if len(expectedNodeAPIs) == 0 {
		expectedNodeAPIs = meshSampleNodeAPIs(samples[0])
	}
	for _, apiBase := range expectedNodeAPIs {
		firstNode, ok := meshSampleNode(samples[0], apiBase)
		if !ok {
			result.OK = false
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "mesh.watch.nodes",
				Status:  "error",
				Message: "mesh node was not visible in the first watch sample",
				Detail:  map[string]any{"api_base": apiBase, "sample": samples[0].Index},
			})
			continue
		}
		lastSample := samples[len(samples)-1]
		lastNode, ok := meshSampleNode(lastSample, apiBase)
		if !ok {
			result.OK = false
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "mesh.watch.nodes",
				Status:  "error",
				Message: "mesh node was not visible in the last watch sample",
				Detail:  map[string]any{"api_base": apiBase, "sample": lastSample.Index},
			})
			continue
		}
		first, ok := runtimeHeartbeatAt(firstNode.Runtime)
		if !ok {
			result.OK = false
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "mesh.watch.heartbeat",
				Status:  "error",
				Message: "mesh node heartbeat timestamp was not visible in the first watch sample",
				Detail:  map[string]any{"api_base": apiBase, "sample": samples[0].Index},
			})
			continue
		}
		last, ok := runtimeHeartbeatAt(lastNode.Runtime)
		if !ok {
			result.OK = false
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "mesh.watch.heartbeat",
				Status:  "error",
				Message: "mesh node heartbeat timestamp was not visible in the last watch sample",
				Detail:  map[string]any{"api_base": apiBase, "sample": lastSample.Index},
			})
			continue
		}
		delta := last.Sub(first)
		if delta <= 0 {
			result.OK = false
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "mesh.watch.heartbeat",
				Status:  "error",
				Message: "mesh node heartbeat did not advance during the watch window",
				Detail: map[string]any{
					"api_base": apiBase,
					"first":    first.Format(time.RFC3339Nano),
					"last":     last.Format(time.RFC3339Nano),
				},
			})
			continue
		}
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "mesh.watch.heartbeat",
			Status:  "ok",
			Message: "mesh node heartbeat advanced during the watch window",
			Detail: map[string]any{
				"api_base": apiBase,
				"first":    first.Format(time.RFC3339Nano),
				"last":     last.Format(time.RFC3339Nano),
				"delta_ms": delta.Milliseconds(),
			},
		})
	}
}

func meshSampleNodeAPIs(sample meshVerificationSample) []string {
	apiBases := make([]string, 0, len(sample.Nodes))
	for _, node := range sample.Nodes {
		apiBase := strings.TrimSpace(node.APIBase)
		if apiBase != "" {
			apiBases = append(apiBases, apiBase)
		}
	}
	return apiBases
}

func meshSampleNode(sample meshVerificationSample, apiBase string) (meshNodeVerificationResult, bool) {
	for _, node := range sample.Nodes {
		if node.APIBase == apiBase {
			return node, true
		}
	}
	return meshNodeVerificationResult{}, false
}

func normalizeMeshAPIBase(value string) (string, error) {
	apiBase := strings.TrimRight(strings.TrimSpace(value), "/")
	if apiBase == "" {
		return "", fmt.Errorf("mesh node api base is required")
	}
	if _, err := url.ParseRequestURI(apiBase); err != nil {
		return "", fmt.Errorf("invalid mesh node api base %q: %w", value, err)
	}
	if !strings.HasSuffix(apiBase, "/api") {
		apiBase += "/api"
	}
	return apiBase, nil
}
