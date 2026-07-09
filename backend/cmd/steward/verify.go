package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/platform/servicecontrol"
)

type runtimeVerifyOptions struct {
	WriteProbes                 bool          `json:"write_probes"`
	StrictSecurity              bool          `json:"strict_security"`
	AdvisorProbe                bool          `json:"advisor_probe"`
	AdvisorProbeEachSample      bool          `json:"advisor_probe_each_sample"`
	AdvisorPrivacyProbe         bool          `json:"advisor_privacy_probe"`
	AutonomyLimit               int           `json:"autonomy_limit"`
	ExpectAgentID               string        `json:"expect_agent_id,omitempty"`
	ExpectAgentVersion          string        `json:"expect_agent_version,omitempty"`
	ExpectAgentPlatform         string        `json:"expect_agent_platform,omitempty"`
	ExpectAdvisorProvider       string        `json:"expect_advisor_provider,omitempty"`
	ExpectAdvisorModel          string        `json:"expect_advisor_model,omitempty"`
	ExpectAdvisorMaxDataLevel   string        `json:"expect_advisor_max_data_level,omitempty"`
	ExpectSyncKeyID             string        `json:"expect_sync_key_id,omitempty"`
	ExpectLocalKeyID            string        `json:"expect_local_key_id,omitempty"`
	ExpectSyncPreviousKeyCount  *int          `json:"expect_sync_previous_key_count,omitempty"`
	ExpectLocalPreviousKeyCount *int          `json:"expect_local_previous_key_count,omitempty"`
	WatchDuration               time.Duration `json:"watch_duration"`
	WatchInterval               time.Duration `json:"watch_interval"`
}

type runtimeVerificationCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

type runtimeVerificationResult struct {
	OK        bool                        `json:"ok"`
	APIBase   string                      `json:"api_base"`
	Options   runtimeVerifyOptions        `json:"options"`
	Checks    []runtimeVerificationCheck  `json:"checks"`
	Artifacts map[string]string           `json:"artifacts,omitempty"`
	Samples   []runtimeVerificationSample `json:"samples,omitempty"`
}

type runtimeVerificationSample struct {
	Index       int                        `json:"index"`
	StartedAt   time.Time                  `json:"started_at"`
	CompletedAt time.Time                  `json:"completed_at"`
	OK          bool                       `json:"ok"`
	Checks      []runtimeVerificationCheck `json:"checks"`
	Artifacts   map[string]string          `json:"artifacts,omitempty"`
}

type peerVerifyOptions struct {
	Sync         bool `json:"sync"`
	Strict       bool `json:"strict"`
	RequirePeers bool `json:"require_peers"`
	WriteProbes  bool `json:"write_probes"`
}

type peerWriteProbe struct {
	TaskID   string            `json:"task_id"`
	Title    string            `json:"title"`
	Entities []peerEntityProbe `json:"entities"`
}

type peerEntityProbe struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Label      string `json:"label,omitempty"`
}

type peerVerificationResult struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Platform     string `json:"platform,omitempty"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	Verified     bool   `json:"verified"`
	Synced       bool   `json:"synced"`
	ProbeVisible bool   `json:"probe_visible,omitempty"`
	SyncSummary  any    `json:"sync_summary,omitempty"`
	ProbeSummary any    `json:"probe_summary,omitempty"`
	Error        string `json:"error,omitempty"`
}

type peersVerificationResult struct {
	OK          bool                       `json:"ok"`
	APIBase     string                     `json:"api_base"`
	Options     peerVerifyOptions          `json:"options"`
	LocalDevice *peerVerificationDevice    `json:"local_device,omitempty"`
	Probe       *peerWriteProbe            `json:"probe,omitempty"`
	Peers       []peerVerificationResult   `json:"peers"`
	Checks      []runtimeVerificationCheck `json:"checks,omitempty"`
}

type peerVerificationDevice struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"device_name,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type meshVerifyOptions struct {
	NodeAPIs                  []string      `json:"node_apis"`
	StrictSecurity            bool          `json:"strict_security"`
	StrictPeers               bool          `json:"strict_peers"`
	RequirePeers              bool          `json:"require_peers"`
	Sync                      bool          `json:"sync"`
	WriteProbes               bool          `json:"write_probes"`
	AdvisorProbe              bool          `json:"advisor_probe"`
	AdvisorProbeEachSample    bool          `json:"advisor_probe_each_sample"`
	AdvisorPrivacyProbe       bool          `json:"advisor_privacy_probe"`
	ExpectAgentIDs            []string      `json:"expect_agent_ids,omitempty"`
	ExpectAgentVersions       []string      `json:"expect_agent_versions,omitempty"`
	ExpectAgentPlatforms      []string      `json:"expect_agent_platforms,omitempty"`
	ExpectSyncKeyIDs          []string      `json:"expect_sync_key_ids,omitempty"`
	ExpectLocalKeyIDs         []string      `json:"expect_local_key_ids,omitempty"`
	ExpectAdvisorProvider     string        `json:"expect_advisor_provider,omitempty"`
	ExpectAdvisorModel        string        `json:"expect_advisor_model,omitempty"`
	ExpectAdvisorMaxDataLevel string        `json:"expect_advisor_max_data_level,omitempty"`
	AutonomyLimit             int           `json:"autonomy_limit"`
	WatchDuration             time.Duration `json:"watch_duration"`
	WatchInterval             time.Duration `json:"watch_interval"`
}

type meshNodeVerificationResult struct {
	OK      bool                      `json:"ok"`
	APIBase string                    `json:"api_base"`
	Runtime runtimeVerificationResult `json:"runtime"`
	Peers   peersVerificationResult   `json:"peers"`
}

type meshVerificationResult struct {
	OK      bool                         `json:"ok"`
	Options meshVerifyOptions            `json:"options"`
	Nodes   []meshNodeVerificationResult `json:"nodes"`
	Checks  []runtimeVerificationCheck   `json:"checks,omitempty"`
	Samples []meshVerificationSample     `json:"samples,omitempty"`
}

type meshVerificationSample struct {
	Index       int                          `json:"index"`
	StartedAt   time.Time                    `json:"started_at"`
	CompletedAt time.Time                    `json:"completed_at"`
	OK          bool                         `json:"ok"`
	Nodes       []meshNodeVerificationResult `json:"nodes"`
}

type serviceVerifyOptions struct {
	Name           string               `json:"name"`
	Scope          string               `json:"scope"`
	StrictSecurity bool                 `json:"strict_security"`
	WriteProbes    bool                 `json:"write_probes"`
	AutonomyLimit  int                  `json:"autonomy_limit"`
	WatchDuration  time.Duration        `json:"watch_duration"`
	WatchInterval  time.Duration        `json:"watch_interval"`
	Runtime        runtimeVerifyOptions `json:"runtime"`
}

type serviceVerificationResult struct {
	OK      bool                        `json:"ok"`
	APIBase string                      `json:"api_base"`
	Options serviceVerifyOptions        `json:"options"`
	Service servicecontrol.StatusResult `json:"service"`
	Runtime runtimeVerificationResult   `json:"runtime"`
	Checks  []runtimeVerificationCheck  `json:"checks"`
	Samples []serviceVerificationSample `json:"samples,omitempty"`
}

type serviceVerificationSample struct {
	Index       int                         `json:"index"`
	StartedAt   time.Time                   `json:"started_at"`
	CompletedAt time.Time                   `json:"completed_at"`
	OK          bool                        `json:"ok"`
	Service     servicecontrol.StatusResult `json:"service"`
	Runtime     runtimeVerificationResult   `json:"runtime"`
	Checks      []runtimeVerificationCheck  `json:"checks"`
}

type optionalIntFlag struct {
	value int
	set   bool
}

func (f *optionalIntFlag) String() string {
	if f == nil || !f.set {
		return ""
	}
	return fmt.Sprintf("%d", f.value)
}

func (f *optionalIntFlag) Set(value string) error {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil {
		return fmt.Errorf("invalid integer %q", value)
	}
	if parsed < 0 {
		return fmt.Errorf("expected count must be >= 0")
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalIntFlag) ptr() *int {
	if f == nil || !f.set {
		return nil
	}
	value := f.value
	return &value
}

type peerDevice struct {
	ID          string
	Name        string
	Platform    string
	Role        string
	TrustStatus string
	SyncEnabled bool
	PublicKey   string
	APIBaseURL  string
}

func (c cli) verify(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printVerifyUsage()
		return nil
	}
	if len(args) == 0 {
		return c.verifyRuntime(args)
	}
	switch args[0] {
	case "runtime", "s3s4":
		return c.verifyRuntime(args[1:])
	case "service":
		return c.verifyService(args[1:])
	case "peers":
		return c.verifyPeers(args[1:])
	case "mesh":
		return c.verifyMesh(args[1:])
	case "evidence":
		return c.verifyEvidence(args[1:])
	default:
		return fmt.Errorf("unknown verify command %q", args[0])
	}
}

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

func (c cli) verifyService(args []string) error {
	fs := flag.NewFlagSet("steward verify service", flag.ExitOnError)
	opts := serviceVerifyOptions{Name: servicecontrol.DefaultName(), Scope: servicecontrol.DefaultScope()}
	var expectSyncPreviousKeyCount optionalIntFlag
	var expectLocalPreviousKeyCount optionalIntFlag
	evidenceDir := fs.String("evidence-dir", "", "Write a timestamped verification evidence JSON file to this directory")
	fs.StringVar(&opts.Name, "name", opts.Name, "Service name, launchd label, or systemd unit name")
	fs.StringVar(&opts.Scope, "scope", opts.Scope, "Service manager scope: user or system; Windows supports system only")
	fs.BoolVar(&opts.StrictSecurity, "strict-security", false, "Fail when runtime sync security or enabled S4 advisor safety is incomplete")
	fs.BoolVar(&opts.WriteProbes, "write-probes", false, "Create low-risk event/task probes after the service API is reachable")
	fs.BoolVar(&opts.Runtime.AdvisorProbe, "advisor-probe", false, "Call the configured S4 autonomy advisor with a D0 live probe")
	fs.BoolVar(&opts.Runtime.AdvisorProbeEachSample, "advisor-probe-each-sample", false, "When used with --advisor-probe and --watch-duration, call the S4 advisor in every watch sample")
	fs.BoolVar(&opts.Runtime.AdvisorPrivacyProbe, "advisor-privacy-probe", false, "Verify the S4 autonomy advisor rejects a D2 privacy probe before model submission")
	fs.IntVar(&opts.AutonomyLimit, "autonomy-limit", 5, "Autonomy scan limit when write probes are enabled")
	fs.StringVar(&opts.Runtime.ExpectAgentID, "expect-agent-id", "", "Fail unless the runtime reports this local steward agent id")
	fs.StringVar(&opts.Runtime.ExpectAgentVersion, "expect-agent-version", "", "Fail unless the runtime reports this steward agent version")
	fs.StringVar(&opts.Runtime.ExpectAgentPlatform, "expect-agent-platform", "", "Fail unless the runtime reports this steward agent platform")
	fs.StringVar(&opts.Runtime.ExpectAdvisorProvider, "expect-advisor-provider", "", "Fail unless the S4 advisor reports this provider")
	fs.StringVar(&opts.Runtime.ExpectAdvisorModel, "expect-advisor-model", "", "Fail unless the S4 advisor reports this model")
	fs.StringVar(&opts.Runtime.ExpectAdvisorMaxDataLevel, "expect-advisor-max-data-level", "", "Fail unless the S4 advisor reports this max data level")
	fs.StringVar(&opts.Runtime.ExpectSyncKeyID, "expect-sync-key-id", "", "Fail unless S3 sync security reports this sync encryption key id")
	fs.StringVar(&opts.Runtime.ExpectLocalKeyID, "expect-local-key-id", "", "Fail unless S3 sync security reports this local encryption key id")
	fs.Var(&expectSyncPreviousKeyCount, "expect-sync-previous-key-count", "Fail unless this many previous sync encryption keys are loaded")
	fs.Var(&expectLocalPreviousKeyCount, "expect-local-previous-key-count", "Fail unless this many previous local encryption keys are loaded")
	fs.DurationVar(&opts.WatchDuration, "watch-duration", 0, "Repeat service verification for this duration; 0 runs once")
	fs.DurationVar(&opts.WatchInterval, "watch-interval", time.Minute, "Interval between service verification samples when --watch-duration is set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.Runtime.ExpectSyncPreviousKeyCount = expectSyncPreviousKeyCount.ptr()
	opts.Runtime.ExpectLocalPreviousKeyCount = expectLocalPreviousKeyCount.ptr()
	if opts.AutonomyLimit <= 0 || opts.AutonomyLimit > 50 {
		opts.AutonomyLimit = 5
	}
	if opts.WatchInterval <= 0 {
		opts.WatchInterval = time.Minute
	}
	if err := validateAdvisorProbeEachSample(opts.Runtime.AdvisorProbeEachSample, opts.Runtime.AdvisorProbe, opts.WatchDuration, "verify service"); err != nil {
		return err
	}
	runtimeOptions := opts.Runtime
	runtimeOptions.WriteProbes = opts.WriteProbes
	runtimeOptions.StrictSecurity = opts.StrictSecurity
	runtimeOptions.AutonomyLimit = opts.AutonomyLimit
	opts.Runtime = runtimeOptions

	result := c.runServiceVerification(opts)
	if err := printVerificationResult("service", *evidenceDir, result, result.OK); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("service verification failed")
	}
	return nil
}

func (c cli) runServiceVerification(opts serviceVerifyOptions) serviceVerificationResult {
	if opts.WatchDuration > 0 {
		return c.runServiceVerificationWatch(opts)
	}
	return c.runSingleServiceVerification(opts)
}

func (c cli) runSingleServiceVerification(opts serviceVerifyOptions) serviceVerificationResult {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, statusErr := servicecontrol.Status(ctx, opts.Name, opts.Scope)
	runtime := c.runRuntimeVerification(opts.Runtime)
	return buildServiceVerificationResult(c.apiBase, opts, status, statusErr, runtime)
}

func (c cli) runServiceVerificationWatch(opts serviceVerifyOptions) serviceVerificationResult {
	deadline := time.Now().Add(opts.WatchDuration)
	samples := []serviceVerificationSample{}
	for {
		startedAt := time.Now().UTC()
		sampleOptions := serviceWatchSampleOptions(opts, len(samples)+1)
		result := c.runSingleServiceVerification(sampleOptions)
		completedAt := time.Now().UTC()
		samples = append(samples, serviceVerificationSample{
			Index:       len(samples) + 1,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			OK:          result.OK,
			Service:     result.Service,
			Runtime:     result.Runtime,
			Checks:      result.Checks,
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
	return buildServiceWatchVerificationResult(c.apiBase, opts, samples)
}

func serviceWatchSampleOptions(opts serviceVerifyOptions, sampleIndex int) serviceVerifyOptions {
	if sampleIndex > 1 {
		opts.WriteProbes = false
		opts.Runtime = runtimeWatchSampleOptions(opts.Runtime, sampleIndex)
	}
	return opts
}

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

func (c cli) verifyEvidence(args []string) error {
	fs := flag.NewFlagSet("steward verify evidence", flag.ExitOnError)
	options := evidenceManifestOptions{}
	var requireKinds stringListFlag
	var requirePlatforms stringListFlag
	var requireAgentIDs stringListFlag
	var requireKindPlatforms stringListFlag
	var requirePlatformAgents stringListFlag
	var requireKindPlatformAgents stringListFlag
	var requireServiceScopes stringListFlag
	var requirePlatformServiceScopes stringListFlag
	var requireKindPlatformServiceScopes stringListFlag
	var requireServiceNames stringListFlag
	var requirePlatformServiceNames stringListFlag
	var requireKindPlatformServiceNames stringListFlag
	var requireAdvisorProviders stringListFlag
	var requirePlatformAdvisorProviders stringListFlag
	var requireKindPlatformAdvisorProviders stringListFlag
	var requireAdvisorModels stringListFlag
	var requirePlatformAdvisorModels stringListFlag
	var requireKindPlatformAdvisorModels stringListFlag
	var requireAdvisorMaxDataLevels stringListFlag
	var requirePlatformAdvisorMaxDataLevels stringListFlag
	var requireKindPlatformAdvisorMaxDataLevels stringListFlag
	var requireChecks stringListFlag
	var requireCheckPlatforms stringListFlag
	var requireKindCheckPlatforms stringListFlag
	fs.StringVar(&options.Dir, "dir", "", "Directory containing steward verification evidence JSON files")
	fs.StringVar(&options.Output, "output", "", "Optional path to write the evidence manifest JSON")
	fs.StringVar(&options.Preset, "preset", "", "Apply a named coverage preset, for example s3s4-final or s3s4-final-system")
	fs.BoolVar(&options.RequirePassing, "require-passing", false, "Fail if any evidence file is failing or unreadable")
	fs.Var(&requireKinds, "require-kind", "Require at least one evidence file of this kind; repeat for runtime/service/peers/mesh/service-install/service-env")
	fs.Var(&requirePlatforms, "require-platform", "Require evidence mentioning this platform; repeat for windows/darwin/linux")
	fs.Var(&requireAgentIDs, "require-agent-id", "Require evidence mentioning this steward agent id; repeat as needed")
	fs.Var(&requireKindPlatforms, "require-kind-platform", "Require at least one evidence file whose kind and platform match KIND:PLATFORM; repeat as needed")
	fs.Var(&requirePlatformAgents, "require-platform-agent", "Require evidence mentioning this platform and agent id as PLATFORM:AGENT_ID; repeat as needed")
	fs.Var(&requireKindPlatformAgents, "require-kind-platform-agent", "Require evidence whose kind, platform, and agent id match KIND:PLATFORM:AGENT_ID; repeat as needed")
	fs.Var(&requireServiceScopes, "require-service-scope", "Require evidence mentioning this service manager scope; repeat for user/system")
	fs.Var(&requirePlatformServiceScopes, "require-platform-service-scope", "Require evidence mentioning this platform and service scope as PLATFORM:SCOPE; repeat as needed")
	fs.Var(&requireKindPlatformServiceScopes, "require-kind-platform-service-scope", "Require evidence whose kind, platform, and service scope match KIND:PLATFORM:SCOPE; repeat as needed")
	fs.Var(&requireServiceNames, "require-service-name", "Require evidence mentioning this service name; repeat as needed")
	fs.Var(&requirePlatformServiceNames, "require-platform-service-name", "Require evidence mentioning this platform and service name as PLATFORM:NAME; repeat as needed")
	fs.Var(&requireKindPlatformServiceNames, "require-kind-platform-service-name", "Require evidence whose kind, platform, and service name match KIND:PLATFORM:NAME; repeat as needed")
	fs.Var(&requireAdvisorProviders, "require-advisor-provider", "Require passing advisor evidence mentioning this provider; repeat as needed")
	fs.Var(&requirePlatformAdvisorProviders, "require-platform-advisor-provider", "Require passing advisor evidence mentioning this platform and provider as PLATFORM:PROVIDER; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorProviders, "require-kind-platform-advisor-provider", "Require passing advisor evidence whose kind, platform, and provider match KIND:PLATFORM:PROVIDER; repeat as needed")
	fs.Var(&requireAdvisorModels, "require-advisor-model", "Require passing advisor evidence mentioning this model; repeat as needed")
	fs.Var(&requirePlatformAdvisorModels, "require-platform-advisor-model", "Require passing advisor evidence mentioning this platform and model as PLATFORM:MODEL; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorModels, "require-kind-platform-advisor-model", "Require passing advisor evidence whose kind, platform, and model match KIND:PLATFORM:MODEL; repeat as needed")
	fs.Var(&requireAdvisorMaxDataLevels, "require-advisor-max-data-level", "Require passing advisor evidence mentioning this max data level; repeat as needed")
	fs.Var(&requirePlatformAdvisorMaxDataLevels, "require-platform-advisor-max-data-level", "Require passing advisor evidence mentioning this platform and max data level as PLATFORM:LEVEL; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorMaxDataLevels, "require-kind-platform-advisor-max-data-level", "Require passing advisor evidence whose kind, platform, and max data level match KIND:PLATFORM:LEVEL; repeat as needed")
	fs.Var(&requireChecks, "require-check", "Require at least one passing verification check with this id; repeat as needed")
	fs.Var(&requireCheckPlatforms, "require-check-platform", "Require at least one passing verification check for a platform as CHECK:PLATFORM; repeat as needed")
	fs.Var(&requireKindCheckPlatforms, "require-kind-check-platform", "Require at least one passing verification check for an evidence kind and platform as KIND:CHECK:PLATFORM; repeat as needed")
	fs.BoolVar(&options.LatestPerKind, "latest-per-kind", false, "Only include the latest evidence file for each kind when evaluating coverage")
	fs.DurationVar(&options.MinWatchDuration, "min-watch-duration", 0, "Fail unless evidence covers at least this watch span")
	fs.BoolVar(&options.MinWatchDurationPerPlatform, "min-watch-duration-per-platform", false, "Apply --min-watch-duration to each required platform instead of only the best evidence file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(options.Dir) == "" {
		return fmt.Errorf("verify evidence requires --dir")
	}
	options.RequireKinds = []string(requireKinds)
	options.RequirePlatforms = []string(requirePlatforms)
	options.RequireAgentIDs = []string(requireAgentIDs)
	options.RequireKindPlatforms = []string(requireKindPlatforms)
	options.RequirePlatformAgents = []string(requirePlatformAgents)
	options.RequireKindPlatformAgents = []string(requireKindPlatformAgents)
	options.RequireServiceScopes = []string(requireServiceScopes)
	options.RequirePlatformServiceScopes = []string(requirePlatformServiceScopes)
	options.RequireKindPlatformServiceScopes = []string(requireKindPlatformServiceScopes)
	options.RequireServiceNames = []string(requireServiceNames)
	options.RequirePlatformServiceNames = []string(requirePlatformServiceNames)
	options.RequireKindPlatformServiceNames = []string(requireKindPlatformServiceNames)
	options.RequireAdvisorProviders = []string(requireAdvisorProviders)
	options.RequirePlatformAdvisorProviders = []string(requirePlatformAdvisorProviders)
	options.RequireKindPlatformAdvisorProviders = []string(requireKindPlatformAdvisorProviders)
	options.RequireAdvisorModels = []string(requireAdvisorModels)
	options.RequirePlatformAdvisorModels = []string(requirePlatformAdvisorModels)
	options.RequireKindPlatformAdvisorModels = []string(requireKindPlatformAdvisorModels)
	options.RequireAdvisorMaxDataLevels = []string(requireAdvisorMaxDataLevels)
	options.RequirePlatformAdvisorMaxDataLevels = []string(requirePlatformAdvisorMaxDataLevels)
	options.RequireKindPlatformAdvisorMaxDataLevels = []string(requireKindPlatformAdvisorMaxDataLevels)
	options.RequireChecks = []string(requireChecks)
	options.RequireCheckPlatforms = []string(requireCheckPlatforms)
	options.RequireKindCheckPlatforms = []string(requireKindCheckPlatforms)
	var err error
	options, err = applyEvidenceManifestPreset(options)
	if err != nil {
		return err
	}
	if options.MinWatchDurationPerPlatform && options.MinWatchDuration <= 0 {
		return fmt.Errorf("verify evidence --min-watch-duration-per-platform requires --min-watch-duration")
	}

	manifest := buildVerificationEvidenceManifest(options)
	manifestPath, err := writeEvidenceManifest(options.Output, manifest)
	if err != nil {
		return err
	}
	payload := map[string]any{"manifest": manifest}
	if manifestPath != "" {
		payload["manifest_path"] = manifestPath
	}
	if err := printJSON(payload); err != nil {
		return err
	}
	if !manifest.OK {
		return fmt.Errorf("verification evidence manifest failed")
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

func buildServiceVerificationResult(apiBase string, opts serviceVerifyOptions, serviceStatus servicecontrol.StatusResult, serviceErr error, runtime runtimeVerificationResult) serviceVerificationResult {
	result := serviceVerificationResult{
		OK:      true,
		APIBase: apiBase,
		Options: opts,
		Service: serviceStatus,
		Runtime: runtime,
		Checks:  []runtimeVerificationCheck{},
	}
	add := func(id string, status string, message string, detail any) {
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: id, Status: status, Message: message, Detail: detail})
		if status == "error" {
			result.OK = false
		}
	}
	if serviceErr != nil {
		add("service.status", "error", serviceErr.Error(), serviceStatus)
	} else if serviceStatusIsActive(serviceStatus.Platform, serviceStatus.Status) {
		add("service.status", "ok", "service manager reports the steward service active", serviceStatus)
	} else {
		add("service.status", "error", "service manager does not report the steward service active", serviceStatus)
	}
	if runtime.OK {
		add("service.runtime", "ok", "runtime verification passed through the service API", compactRuntimeVerification(runtime))
	} else {
		add("service.runtime", "error", "runtime verification failed through the service API", compactRuntimeVerification(runtime))
	}
	result.OK = result.OK && runtime.OK
	return result
}

func buildServiceWatchVerificationResult(apiBase string, opts serviceVerifyOptions, samples []serviceVerificationSample) serviceVerificationResult {
	result := serviceVerificationResult{
		OK:      len(samples) > 0,
		APIBase: apiBase,
		Options: opts,
		Samples: samples,
		Checks:  []runtimeVerificationCheck{},
	}
	if len(samples) == 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{ID: "service.watch", Status: "error", Message: "no service verification samples were collected"})
		return result
	}

	last := samples[len(samples)-1]
	result.Service = last.Service
	result.Runtime = last.Runtime
	failures := 0
	for _, sample := range samples {
		if !sample.OK {
			failures++
		}
	}
	if failures > 0 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "service.watch",
			Status:  "error",
			Message: fmt.Sprintf("%d of %d service verification samples failed", failures, len(samples)),
			Detail:  map[string]int{"samples": len(samples), "failures": failures},
		})
		return result
	}
	addServiceWatchHeartbeatCheck(&result, samples)
	result.Checks = append(result.Checks, runtimeVerificationCheck{
		ID:      "service.watch",
		Status:  "ok",
		Message: fmt.Sprintf("%d service verification samples passed", len(samples)),
		Detail:  map[string]int{"samples": len(samples), "failures": 0},
	})
	return result
}

func addServiceWatchHeartbeatCheck(result *serviceVerificationResult, samples []serviceVerificationSample) {
	if len(samples) < 2 {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "service.watch.heartbeat",
			Status:  "error",
			Message: "at least two service verification samples are required to prove agent heartbeat advance",
			Detail:  map[string]int{"samples": len(samples)},
		})
		return
	}
	first, ok := sampleHeartbeatAt(samples[0])
	if !ok {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "service.watch.heartbeat",
			Status:  "error",
			Message: "agent heartbeat timestamp was not visible in the first watch sample",
			Detail:  map[string]int{"sample": samples[0].Index},
		})
		return
	}
	lastSample := samples[len(samples)-1]
	last, ok := sampleHeartbeatAt(lastSample)
	if !ok {
		result.OK = false
		result.Checks = append(result.Checks, runtimeVerificationCheck{
			ID:      "service.watch.heartbeat",
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
			ID:      "service.watch.heartbeat",
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
		ID:      "service.watch.heartbeat",
		Status:  "ok",
		Message: "agent heartbeat advanced during the watch window",
		Detail: map[string]any{
			"first":    first.Format(time.RFC3339Nano),
			"last":     last.Format(time.RFC3339Nano),
			"delta_ms": delta.Milliseconds(),
		},
	})
}

func sampleHeartbeatAt(sample serviceVerificationSample) (time.Time, bool) {
	return runtimeHeartbeatAt(sample.Runtime)
}

func runtimeHeartbeatAt(runtime runtimeVerificationResult) (time.Time, bool) {
	return heartbeatFromRuntimeChecks(runtime.Checks)
}

func runtimeSampleHeartbeatAt(sample runtimeVerificationSample) (time.Time, bool) {
	return heartbeatFromRuntimeChecks(sample.Checks)
}

func heartbeatFromRuntimeChecks(checks []runtimeVerificationCheck) (time.Time, bool) {
	for _, check := range checks {
		if check.ID != "steward.agent" {
			continue
		}
		detail, ok := check.Detail.(map[string]any)
		if !ok {
			return time.Time{}, false
		}
		return parseTimestamp(stringAt(detail, "last_heartbeat_at"))
	}
	return time.Time{}, false
}

func serviceStatusIsActive(platform string, status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "windows":
		return normalized == "running"
	case "linux":
		return normalized == "active"
	case "darwin":
		return normalized == "loaded"
	default:
		return normalized == "running" || normalized == "active" || normalized == "loaded"
	}
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

func (c cli) getJSON(path string) (map[string]any, error) {
	return c.getJSONURL(c.apiBase + path)
}

func (c cli) getJSONURL(endpoint string) (map[string]any, error) {
	body, err := c.requestURL(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	return decodeObject(body)
}

func (c cli) postJSON(path string, payload any) (map[string]any, error) {
	body, err := c.request(http.MethodPost, path, payload)
	if err != nil {
		return nil, err
	}
	return decodeObject(body)
}

func decodeObject(body []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func missingStrictSecurityItems(security map[string]any) []string {
	checks := map[string]string{
		"auth_required":                "sync request authentication",
		"peer_api_enabled":             "restricted peer API listener",
		"peer_api_advertised":          "advertised peer API base URL",
		"device_signing_ready":         "device private-key signing",
		"device_identity_advertisable": "device public identity",
		"sync_encryption_configured":   "peer sync payload encryption",
		"local_encryption_configured":  "local sync payload at-rest encryption",
	}
	missing := []string{}
	for key, label := range checks {
		if !boolAt(security, key) {
			missing = append(missing, label)
		}
	}
	if boolAt(security, "insecure_mode_active") {
		missing = append(missing, "insecure sync compatibility disabled")
	}
	if boolAt(security, "management_remote_access") {
		missing = append(missing, "management API bound to loopback")
	}
	return missing
}

func addRuntimeExpectedSyncSecurityChecks(opts runtimeVerifyOptions, security map[string]any, add func(string, string, string, any)) {
	if strings.TrimSpace(opts.ExpectSyncKeyID) != "" {
		addExpectedStringCheck(add, "s3.sync.security.expected_sync_key", "sync encryption key id", opts.ExpectSyncKeyID, stringAt(security, "sync_encryption_key_id"))
	}
	if strings.TrimSpace(opts.ExpectLocalKeyID) != "" {
		addExpectedStringCheck(add, "s3.sync.security.expected_local_key", "local encryption key id", opts.ExpectLocalKeyID, stringAt(security, "local_encryption_key_id"))
	}
	if opts.ExpectSyncPreviousKeyCount != nil {
		addExpectedIntCheck(add, "s3.sync.security.expected_sync_previous_keys", "previous sync encryption key count", *opts.ExpectSyncPreviousKeyCount, intAt(security, "sync_previous_key_count"))
	}
	if opts.ExpectLocalPreviousKeyCount != nil {
		addExpectedIntCheck(add, "s3.sync.security.expected_local_previous_keys", "previous local encryption key count", *opts.ExpectLocalPreviousKeyCount, intAt(security, "local_previous_key_count"))
	}
}

func addRuntimeExpectedAdvisorChecks(opts runtimeVerifyOptions, advisor map[string]any, add func(string, string, string, any)) {
	if strings.TrimSpace(opts.ExpectAdvisorProvider) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_provider", "advisor provider", opts.ExpectAdvisorProvider, stringAt(advisor, "provider"))
	}
	if strings.TrimSpace(opts.ExpectAdvisorModel) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_model", "advisor model", opts.ExpectAdvisorModel, stringAt(advisor, "model"))
	}
	if strings.TrimSpace(opts.ExpectAdvisorMaxDataLevel) != "" {
		addExpectedStringCheck(add, "s4.advisor.expected_max_data_level", "advisor max data level", opts.ExpectAdvisorMaxDataLevel, stringAt(advisor, "max_data_level"))
	}
}

func strictAdvisorRuntimeIssues(advisor map[string]any) []string {
	if !boolAt(advisor, "enabled") {
		return nil
	}
	issues := []string{}
	provider := strings.ToLower(strings.TrimSpace(stringAt(advisor, "provider")))
	if provider != "openai-compatible" && provider != "openai" {
		issues = append(issues, "advisor provider must be openai-compatible or openai")
	}
	if strings.TrimSpace(stringAt(advisor, "model")) == "" {
		issues = append(issues, "advisor model must be visible")
	}
	baseURL := strings.TrimSpace(stringAt(advisor, "base_url"))
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			issues = append(issues, "advisor base_url must be an http or https URL with a host")
		}
	}
	maxDataLevel := strings.ToUpper(strings.TrimSpace(stringAt(advisor, "max_data_level")))
	if maxDataLevel == "" {
		maxDataLevel = "D1"
	}
	if maxDataLevel != "D0" && maxDataLevel != "D1" {
		issues = append(issues, "advisor max_data_level must be D0 or D1")
	}
	return issues
}

func addExpectedStringCheck(add func(string, string, string, any), id string, label string, expected string, actual string) {
	expected = strings.TrimSpace(expected)
	if actual == expected {
		add(id, "ok", "runtime reports the expected "+label, map[string]string{"expected": expected, "actual": actual})
		return
	}
	add(id, "error", "runtime "+label+" does not match expected value", map[string]string{"expected": expected, "actual": actual})
}

func addExpectedIntCheck(add func(string, string, string, any), id string, label string, expected int, actual int) {
	if actual == expected {
		add(id, "ok", "runtime reports the expected "+label, map[string]int{"expected": expected, "actual": actual})
		return
	}
	add(id, "error", "runtime "+label+" does not match expected value", map[string]int{"expected": expected, "actual": actual})
}

func compactSyncStatus(sync map[string]any) map[string]any {
	return map[string]any{
		"local_device":      mapAt(sync, "local_device"),
		"pending_changes":   valueAt(sync, "pending_changes"),
		"pending_relations": valueAt(sync, "pending_relations"),
		"capability_count":  len(sliceAt(sync, "capabilities")),
		"conflict_count":    valueAt(sync, "conflict_count"),
		"last_change_at":    valueAt(sync, "last_change_at"),
	}
}

func compactRuntimeVerification(result runtimeVerificationResult) map[string]any {
	return map[string]any{
		"ok":      result.OK,
		"api":     result.APIBase,
		"checks":  len(result.Checks),
		"options": result.Options,
	}
}

func compactAdvisorProbe(probe map[string]any) map[string]any {
	status := mapAt(probe, "status")
	suggestion := mapAt(probe, "suggestion")
	return map[string]any{
		"provider":       stringAt(status, "provider"),
		"model":          stringAt(status, "model"),
		"max_data_level": stringAt(status, "max_data_level"),
		"data_level":     stringAt(probe, "data_level"),
		"duration_ms":    valueAt(probe, "duration_ms"),
		"title":          stringAt(suggestion, "title"),
		"probed_at":      stringAt(probe, "probed_at"),
	}
}

func compactPeerSyncResult(sync map[string]any) map[string]any {
	return map[string]any{
		"pulled":               valueAt(sync, "pulled"),
		"imported":             valueAt(sync, "imported"),
		"applied":              valueAt(sync, "applied"),
		"skipped":              valueAt(sync, "skipped"),
		"pushed":               valueAt(sync, "pushed"),
		"denied":               valueAt(sync, "denied"),
		"remote_last_sequence": valueAt(sync, "remote_last_sequence"),
		"local_sent_sequence":  valueAt(sync, "local_sent_sequence"),
		"errors":               valueAt(sync, "errors"),
	}
}

func devicesFromSyncStatus(sync map[string]any) []peerDevice {
	devices := []peerDevice{}
	for _, item := range sliceAt(sync, "devices") {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		devices = append(devices, peerDevice{
			ID:          stringAt(raw, "id"),
			Name:        stringAt(raw, "device_name"),
			Platform:    strings.ToLower(strings.TrimSpace(stringAt(raw, "platform"))),
			Role:        stringAt(raw, "role"),
			TrustStatus: stringAt(raw, "trust_status"),
			SyncEnabled: boolAt(raw, "sync_enabled"),
			PublicKey:   stringAt(raw, "public_key"),
			APIBaseURL:  stringAt(raw, "api_base_url"),
		})
	}
	return devices
}

func peerVerificationSkipReason(device peerDevice) string {
	if strings.EqualFold(device.TrustStatus, "revoked") {
		return "peer is revoked"
	}
	if !device.SyncEnabled {
		return "peer sync is disabled"
	}
	if strings.TrimSpace(device.APIBaseURL) == "" {
		return "peer api_base_url is not configured"
	}
	if strings.TrimSpace(device.PublicKey) == "" {
		return "peer public_key is not configured"
	}
	return ""
}

func syncRecentChangesContain(sync map[string]any, entityType string, entityID string) bool {
	for _, item := range sliceAt(sync, "recent_changes") {
		change, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringAt(change, "entity_type") == entityType && stringAt(change, "entity_id") == entityID {
			return true
		}
	}
	return false
}

func proposalForSourceEntity(autonomy map[string]any, sourceEntityID string) string {
	for _, item := range sliceAt(autonomy, "proposals") {
		proposal, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringAt(proposal, "source_entity_id") == sourceEntityID {
			return stringAt(proposal, "id")
		}
	}
	return ""
}

func parseTimestamp(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func mapAt(input map[string]any, key string) map[string]any {
	value, _ := input[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

func sliceAt(input map[string]any, key string) []any {
	value, _ := input[key].([]any)
	if value == nil {
		return []any{}
	}
	return value
}

func stringSliceAt(input map[string]any, key string) []string {
	values := []string{}
	for _, item := range sliceAt(input, key) {
		text := strings.TrimSpace(fmt.Sprint(item))
		if text != "" {
			values = append(values, text)
		}
	}
	return values
}

func valueAt(input map[string]any, key string) any {
	if input == nil {
		return nil
	}
	return input[key]
}

func stringAt(input map[string]any, key string) string {
	value := valueAt(input, key)
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolAt(input map[string]any, key string) bool {
	value := valueAt(input, key)
	asBool, _ := value.(bool)
	return asBool
}

func intAt(input map[string]any, key string) int {
	value := valueAt(input, key)
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		var parsed int
		_, _ = fmt.Sscanf(strings.TrimSpace(fmt.Sprint(value)), "%d", &parsed)
		return parsed
	}
}
