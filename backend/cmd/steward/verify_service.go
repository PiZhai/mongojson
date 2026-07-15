package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

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
	fs.BoolVar(&opts.Runtime.AdvisorPrivacyProbe, "advisor-privacy-probe", false, "Verify the S4 autonomy advisor rejects an unsupported D7 privacy probe before model submission")
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
