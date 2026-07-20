package steward

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	DefaultHeartbeatInterval              = time.Minute
	DefaultCollectionInterval             = 5 * time.Minute
	DefaultActivitySampleInterval         = 15 * time.Second
	DefaultContinuousIntelligenceInterval = time.Minute
	DefaultProactiveInterval              = 5 * time.Minute
	DefaultProactiveToolsmithInterval     = 30 * time.Minute
	DefaultModelDispatchInterval          = time.Minute
	DefaultRuntimeInterval                = time.Second
	DefaultRuntimeWatchdogInterval        = 2 * time.Second
	DefaultNotificationInterval           = 5 * time.Second
	DefaultRuntimeReadinessGrace          = 30 * time.Second
	maxRuntimeV2StepTimeout               = time.Hour
	criticalLoopFailureThreshold          = 3
)

type DaemonOptions struct {
	HeartbeatInterval              time.Duration
	CollectionInterval             time.Duration
	ActivitySampleInterval         time.Duration
	ContinuousIntelligenceInterval time.Duration
	ContinuousIntelligenceLimit    int
	ProactiveInterval              time.Duration
	ProactiveToolsmithInterval     time.Duration
	SyncInterval                   time.Duration
	AutonomyInterval               time.Duration
	AutonomyLimit                  int
	ModelDispatchInterval          time.Duration
	ModelDispatchLimit             int
	RuntimeInterval                time.Duration
	RuntimeLimit                   int
	RuntimeWatchdogInterval        time.Duration
	RuntimeWatchdogLimit           int
	NotificationInterval           time.Duration
	NotificationLimit              int
}

type Daemon struct {
	service    *Service
	options    DaemonOptions
	mu         sync.Mutex
	cancel     context.CancelFunc
	generation uint64
	wg         sync.WaitGroup
	running    atomic.Bool
}

func NewDaemon(service *Service, options DaemonOptions) *Daemon {
	return &Daemon{
		service: service,
		options: normalizeDaemonOptions(options),
	}
}

func DaemonOptionsFromEnv() DaemonOptions {
	return normalizeDaemonOptions(DaemonOptions{
		HeartbeatInterval:              durationEnv("STEWARD_HEARTBEAT_INTERVAL", DefaultHeartbeatInterval),
		CollectionInterval:             durationEnv("STEWARD_COLLECTION_INTERVAL", DefaultCollectionInterval),
		ActivitySampleInterval:         durationEnv("STEWARD_ACTIVITY_SAMPLE_INTERVAL", DefaultActivitySampleInterval),
		ContinuousIntelligenceInterval: durationEnv("STEWARD_INTELLIGENCE_INTERVAL", DefaultContinuousIntelligenceInterval),
		ContinuousIntelligenceLimit:    intEnv("STEWARD_INTELLIGENCE_LIMIT", 4),
		ProactiveInterval:              durationEnv("STEWARD_PROACTIVE_INTERVAL", DefaultProactiveInterval),
		ProactiveToolsmithInterval:     durationEnv("STEWARD_PROACTIVE_TOOLSMITH_INTERVAL", DefaultProactiveToolsmithInterval),
		SyncInterval:                   durationEnv("STEWARD_SYNC_INTERVAL", 0),
		AutonomyInterval:               durationEnv("STEWARD_AUTONOMY_INTERVAL", 0),
		AutonomyLimit:                  intEnv("STEWARD_AUTONOMY_LIMIT", 12),
		ModelDispatchInterval:          durationEnv("STEWARD_MODEL_DISPATCH_INTERVAL", DefaultModelDispatchInterval),
		ModelDispatchLimit:             intEnv("STEWARD_MODEL_DISPATCH_LIMIT", 20),
		RuntimeInterval:                durationEnv("STEWARD_RUNTIME_INTERVAL", DefaultRuntimeInterval),
		RuntimeLimit:                   intEnv("STEWARD_RUNTIME_LIMIT", 10),
		RuntimeWatchdogInterval:        durationEnv("STEWARD_RUNTIME_WATCHDOG_INTERVAL", DefaultRuntimeWatchdogInterval),
		RuntimeWatchdogLimit:           intEnv("STEWARD_RUNTIME_WATCHDOG_LIMIT", 20),
		NotificationInterval:           durationEnv("STEWARD_NOTIFICATION_INTERVAL", DefaultNotificationInterval),
		NotificationLimit:              intEnv("STEWARD_NOTIFICATION_LIMIT", 40),
	})
}

func (d *Daemon) Start(parent context.Context) {
	if d == nil || d.service == nil {
		return
	}
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	d.generation++
	generation := d.generation
	runtimeInterval := d.options.RuntimeInterval
	runtimeWatchdogInterval := d.options.RuntimeWatchdogInterval
	if !d.service.runtimeV2 {
		runtimeInterval = 0
		runtimeWatchdogInterval = 0
	}
	for _, loop := range []struct {
		name     string
		interval time.Duration
	}{
		{name: "heartbeat", interval: d.options.HeartbeatInterval},
		{name: "collection", interval: d.options.CollectionInterval},
		{name: "activity-sample", interval: d.options.ActivitySampleInterval},
		{name: "continuous-intelligence", interval: d.options.ContinuousIntelligenceInterval},
		{name: "proactive", interval: d.options.ProactiveInterval},
		{name: "proactive-toolsmith", interval: d.options.ProactiveToolsmithInterval},
		{name: "sync", interval: d.options.SyncInterval},
		{name: "autonomy", interval: d.options.AutonomyInterval},
		{name: "model-dispatch", interval: d.options.ModelDispatchInterval},
		{name: "runtime-v2", interval: runtimeInterval},
		{name: "runtime-watchdog", interval: runtimeWatchdogInterval},
		{name: "notifications", interval: d.options.NotificationInterval},
	} {
		if err := d.service.configureDaemonLoop(ctx, loop.name, loop.interval, loop.interval > 0); err != nil {
			log.Printf("steward daemon %s loop status initialization failed: %v", loop.name, err)
		}
	}

	started := false
	started = d.startLoop(ctx, "heartbeat", d.options.HeartbeatInterval, func(ctx context.Context) error {
		return d.service.Heartbeat(ctx, "")
	}) || started
	started = d.startLoop(ctx, "collection", d.options.CollectionInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		lifecycleErr := d.service.RunLifecycleMaintenance(ctx)
		collectorErr := d.service.RunEnabledCollectors(ctx)
		return errors.Join(collectorErr, lifecycleErr)
	}) || started
	started = d.startLoop(ctx, "activity-sample", d.options.ActivitySampleInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil || !enabled {
			return err
		}
		return d.service.RunRealtimeCollectors(ctx)
	}) || started
	started = d.startLoop(ctx, "continuous-intelligence", d.options.ContinuousIntelligenceInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil || !enabled {
			return err
		}
		workerID := defaultString(strings.TrimSpace(d.service.runtimeWorkerID), d.service.agentIDValue()) + ":continuous"
		_, err = d.service.RunContinuousIntelligenceCycle(ctx, time.Now().UTC(), workerID, d.options.ContinuousIntelligenceLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "proactive", d.options.ProactiveInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil || !enabled {
			return err
		}
		batchEnabled, err := d.service.intelligenceBatchEnabled(ctx)
		if err != nil || batchEnabled {
			return err
		}
		_, err = d.service.RunProactiveCycle(ctx, RunProactiveInput{})
		return err
	}) || started
	started = d.startPersistedLoop(ctx, "proactive-toolsmith", d.options.ProactiveToolsmithInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil || !enabled {
			return err
		}
		return d.service.RunProactiveToolsmithCycle(ctx)
	}) || started
	started = d.startLoop(ctx, "sync", d.options.SyncInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		_, err = d.service.SyncTrustedPeerDevices(ctx)
		return err
	}) || started
	started = d.startLoop(ctx, "autonomy", d.options.AutonomyInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		_, err = d.service.RunAutonomyCycle(ctx, d.options.AutonomyLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "model-dispatch", d.options.ModelDispatchInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		batchEnabled, err := d.service.intelligenceBatchEnabled(ctx)
		if err != nil || batchEnabled {
			return err
		}
		_, err = d.service.RunModelDispatches(ctx, d.options.ModelDispatchLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "runtime-watchdog", runtimeWatchdogInterval, func(ctx context.Context) error {
		_, err := d.service.RunAgentRuntimeWatchdog(ctx, d.options.RuntimeWatchdogLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "notifications", d.options.NotificationInterval, func(ctx context.Context) error {
		enabled, err := d.service.BackgroundWorkEnabled(ctx)
		if err != nil || !enabled {
			return err
		}
		_, err = d.service.RunNotificationDeliveryCycle(ctx, d.options.NotificationLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "runtime-v2", runtimeInterval, func(ctx context.Context) error {
		var recoveryErr, agentBeforeErr, conversationBeforeErr, refreshBeforeErr, beforeErr, remoteBeforeErr, runtimeErr, conversationAfterErr, refreshAfterErr, agentAfterErr, remoteAfterErr, afterErr error
		_, recoveryErr = d.service.RecoverSystemChangeTransactions(ctx, d.options.RuntimeLimit)
		_, agentBeforeErr = d.service.RunAgentEpisodeCycle(ctx, d.options.RuntimeLimit)
		if d.service.orchestrationR4 {
			_, conversationBeforeErr = d.service.RunConversationExecutionCycle(ctx, d.options.RuntimeLimit)
		}
		_, refreshBeforeErr = d.service.RunConversationExecutionRefreshCycle(ctx, d.options.RuntimeLimit)
		if d.service.orchestrationR4 {
			_, beforeErr = d.service.RunOrchestrationCycle(ctx, d.options.RuntimeLimit)
		}
		if d.service.orchestrationRemote {
			_, remoteBeforeErr = d.service.RunRemoteExecutionCycle(ctx, d.options.RuntimeLimit)
		}
		_, runtimeErr = d.service.RunAgentRuntimeCycle(ctx, d.options.RuntimeLimit)
		if d.service.orchestrationR4 {
			_, conversationAfterErr = d.service.RunConversationExecutionCycle(ctx, d.options.RuntimeLimit)
		}
		_, refreshAfterErr = d.service.RunConversationExecutionRefreshCycle(ctx, d.options.RuntimeLimit)
		_, agentAfterErr = d.service.RunAgentEpisodeCycle(ctx, d.options.RuntimeLimit)
		if d.service.orchestrationRemote {
			_, remoteAfterErr = d.service.RunRemoteExecutionCycle(ctx, d.options.RuntimeLimit)
		}
		if d.service.orchestrationR4 {
			_, afterErr = d.service.RunOrchestrationCycle(ctx, d.options.RuntimeLimit)
		}
		return errors.Join(recoveryErr, agentBeforeErr, conversationBeforeErr, refreshBeforeErr, beforeErr, remoteBeforeErr, runtimeErr, conversationAfterErr, refreshAfterErr, agentAfterErr, remoteAfterErr, afterErr)
	}) || started

	if !started {
		cancel()
		d.cancel = nil
		d.mu.Unlock()
		return
	}
	d.running.Store(true)
	d.mu.Unlock()
	go func() {
		d.wg.Wait()
		d.mu.Lock()
		markStopped := false
		if d.generation == generation {
			d.cancel = nil
			d.running.Store(false)
			markStopped = true
		}
		d.mu.Unlock()
		if markStopped {
			d.markLoopStatusesStopped()
		}
	}()
}

func (d *Daemon) IsRunning() bool {
	return d != nil && d.running.Load()
}

// Readiness verifies that the daemon is not merely alive, but can run the
// durable execution loop that backs conversations and orchestration. Optional
// model-driven loops remain observable without taking the whole service out of
// readiness when their provider is temporarily unavailable.
func (d *Daemon) Readiness(ctx context.Context) error {
	if d == nil || d.service == nil {
		return fmt.Errorf("steward daemon is not configured")
	}
	if !d.IsRunning() {
		return fmt.Errorf("steward daemon is not running")
	}
	if d.service.orchestrationR4 {
		if err := d.service.orchestrationEnabled(); err != nil {
			return fmt.Errorf("orchestration configuration: %w", err)
		}
	}
	if !d.service.runtimeV2 {
		return nil
	}
	statuses, err := d.service.listDaemonLoopStatuses(ctx)
	if err != nil {
		return err
	}
	executionBudget, err := d.runtimeReadinessExecutionBudget(ctx)
	if err != nil {
		return err
	}
	return criticalDaemonLoopReadiness(
		statuses,
		"runtime-v2",
		criticalLoopFailureThreshold,
		d.options.RuntimeInterval,
		DefaultRuntimeReadinessGrace,
		executionBudget,
		time.Now().UTC(),
	)
}

func (d *Daemon) runtimeReadinessExecutionBudget(ctx context.Context) (time.Duration, error) {
	values, err := d.service.loadModelSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("load runtime-v2 execution budget: %w", err)
	}
	return runtimeLoopExecutionBudget(d.options.RuntimeLimit, values.timeoutSeconds, values.agentMaxDurationSeconds, d.options.RuntimeInterval), nil
}

func runtimeLoopExecutionBudget(runtimeLimit, modelTimeoutSeconds, agentMaxDurationSeconds int, interval time.Duration) time.Duration {
	modelTimeout := time.Duration(modelTimeoutSeconds) * time.Second
	if modelTimeout <= 0 {
		modelTimeout = 30 * time.Second
	}
	limit := runtimeLimit
	if limit <= 0 {
		limit = 1
	}
	// A runtime-v2 cycle advances agent episodes before and after tool/runtime
	// execution. Both passes can make one bounded model request per claimed
	// episode, so account for both instead of treating interval as execution
	// time.
	budget := time.Duration(2*limit) * modelTimeout
	// Runtime V2 permits a step timeout of up to one hour and applies the same
	// bound again while verifying the postcondition. A legitimate long-running
	// system tool must not make readiness report a stuck daemon halfway through
	// either phase.
	if stepBudget := 2 * maxRuntimeV2StepTimeout; stepBudget > budget {
		budget = stepBudget
	}
	if agentMaxDurationSeconds > 0 {
		episodeBudget := time.Duration(agentMaxDurationSeconds) * time.Second
		if episodeBudget > budget {
			budget = episodeBudget
		}
	}
	if budget < interval {
		budget = interval
	}
	return budget
}

func criticalDaemonLoopReadiness(
	statuses []domain.StewardBackgroundLoopStatus,
	name string,
	failureThreshold int,
	interval time.Duration,
	grace time.Duration,
	executionBudget time.Duration,
	now time.Time,
) error {
	if failureThreshold < 1 {
		failureThreshold = 1
	}
	if interval <= 0 {
		return fmt.Errorf("critical loop %s has invalid interval %s", name, interval)
	}
	if grace < 0 {
		grace = 0
	}
	if executionBudget < interval {
		executionBudget = interval
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	for _, status := range statuses {
		if status.Name != name {
			continue
		}
		if !status.Enabled {
			return fmt.Errorf("critical loop %s is disabled", name)
		}
		if !status.Running {
			return fmt.Errorf("critical loop %s is not running", name)
		}
		if status.LastCompletedAt == nil {
			return fmt.Errorf("critical loop %s has not completed its first iteration", name)
		}
		if status.LastSuccessAt == nil {
			return fmt.Errorf("critical loop %s has not completed successfully", name)
		}
		if status.ConsecutiveFailures >= failureThreshold {
			detail := ""
			if status.LastError != nil {
				detail = strings.TrimSpace(*status.LastError)
			}
			if len(detail) > 512 {
				detail = detail[:512] + "..."
			}
			if detail != "" {
				return fmt.Errorf("critical loop %s failed %d consecutive times: %s", name, status.ConsecutiveFailures, detail)
			}
			return fmt.Errorf("critical loop %s failed %d consecutive times", name, status.ConsecutiveFailures)
		}
		inFlight := status.LastStartedAt != nil && status.LastStartedAt.After(status.LastCompletedAt.UTC())
		if inFlight {
			if age := now.Sub(status.LastStartedAt.UTC()); age > executionBudget+grace {
				return fmt.Errorf("critical loop %s has been in flight for %s, exceeding execution budget %s plus grace %s", name, age.Round(time.Millisecond), executionBudget, grace)
			}
			return nil
		}
		completionWindow := interval + grace
		if age := now.Sub(status.LastCompletedAt.UTC()); age > completionWindow {
			return fmt.Errorf("critical loop %s last completed %s ago, exceeding interval %s plus grace %s", name, age.Round(time.Millisecond), interval, grace)
		}
		successWindow := time.Duration(failureThreshold)*interval + grace
		if age := now.Sub(status.LastSuccessAt.UTC()); age > successWindow {
			return fmt.Errorf("critical loop %s last succeeded %s ago, exceeding %d intervals plus grace %s", name, age.Round(time.Millisecond), failureThreshold, grace)
		}
		return nil
	}
	return fmt.Errorf("critical loop %s has no status record", name)
}

func (d *Daemon) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	cancel := d.cancel
	generation := d.generation
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.wg.Wait()
	d.mu.Lock()
	if d.generation == generation {
		d.cancel = nil
		d.running.Store(false)
	}
	d.mu.Unlock()
	d.markLoopStatusesStopped()
}

func (d *Daemon) markLoopStatusesStopped() {
	if d == nil || d.service == nil {
		return
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	if err := d.service.stopDaemonLoops(stopCtx); err != nil {
		log.Printf("steward daemon loop status stop failed: %v", err)
	}
}

func (d *Daemon) startLoop(ctx context.Context, name string, interval time.Duration, run func(context.Context) error) bool {
	if interval <= 0 || run == nil {
		return false
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		d.runOnce(ctx, name, run)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.runOnce(ctx, name, run)
			}
		}
	}()
	return true
}

// startPersistedLoop preserves the cadence across service restarts. This is
// used for expensive model-driven maintenance so deployments do not trigger a
// duplicate run and an unnecessary model call on every restart.
func (d *Daemon) startPersistedLoop(ctx context.Context, name string, interval time.Duration, run func(context.Context) error) bool {
	if interval <= 0 || run == nil {
		return false
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		delay, err := d.service.daemonLoopInitialDelay(ctx, name, interval, time.Now().UTC())
		if err != nil {
			log.Printf("steward daemon %s persisted schedule unavailable; running now: %v", name, err)
			delay = 0
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				d.runOnce(ctx, name, run)
				timer.Reset(interval)
			}
		}
	}()
	return true
}

func (d *Daemon) runOnce(ctx context.Context, name string, run func(context.Context) error) {
	startedAt := time.Now().UTC()
	if err := d.service.recordDaemonLoopStarted(ctx, name, startedAt); err != nil && ctx.Err() == nil {
		log.Printf("steward daemon %s loop start status update failed: %v", name, err)
	}
	err := run(ctx)
	if ctx.Err() == nil {
		if statusErr := d.service.recordDaemonLoopResult(ctx, name, startedAt, err); statusErr != nil {
			log.Printf("steward daemon %s loop status update failed: %v", name, statusErr)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("steward daemon %s loop failed: %v", name, err)
	}
}

func normalizeDaemonOptions(input DaemonOptions) DaemonOptions {
	out := input
	if out.HeartbeatInterval < 0 {
		out.HeartbeatInterval = 0
	}
	if out.HeartbeatInterval == 0 {
		out.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if out.SyncInterval < 0 {
		out.SyncInterval = 0
	}
	if out.CollectionInterval < 0 {
		out.CollectionInterval = 0
	}
	if out.ActivitySampleInterval < 0 {
		out.ActivitySampleInterval = 0
	}
	if out.ActivitySampleInterval == 0 {
		out.ActivitySampleInterval = DefaultActivitySampleInterval
	}
	if out.ContinuousIntelligenceInterval < 0 {
		out.ContinuousIntelligenceInterval = 0
	}
	if out.ContinuousIntelligenceInterval == 0 {
		out.ContinuousIntelligenceInterval = DefaultContinuousIntelligenceInterval
	}
	if out.ContinuousIntelligenceLimit <= 0 || out.ContinuousIntelligenceLimit > 32 {
		out.ContinuousIntelligenceLimit = 4
	}
	if out.ProactiveInterval < 0 {
		out.ProactiveInterval = 0
	}
	if out.ProactiveInterval == 0 {
		out.ProactiveInterval = DefaultProactiveInterval
	}
	if out.ProactiveToolsmithInterval < 0 {
		out.ProactiveToolsmithInterval = 0
	}
	if out.ProactiveToolsmithInterval == 0 {
		out.ProactiveToolsmithInterval = DefaultProactiveToolsmithInterval
	}
	if out.CollectionInterval == 0 {
		out.CollectionInterval = DefaultCollectionInterval
	}
	if out.AutonomyInterval < 0 {
		out.AutonomyInterval = 0
	}
	if out.AutonomyLimit <= 0 || out.AutonomyLimit > 50 {
		out.AutonomyLimit = 12
	}
	if out.ModelDispatchInterval < 0 {
		out.ModelDispatchInterval = 0
	}
	if out.ModelDispatchInterval == 0 {
		out.ModelDispatchInterval = DefaultModelDispatchInterval
	}
	if out.ModelDispatchLimit <= 0 || out.ModelDispatchLimit > 100 {
		out.ModelDispatchLimit = 20
	}
	if out.RuntimeInterval < 0 {
		out.RuntimeInterval = 0
	}
	if out.RuntimeLimit <= 0 || out.RuntimeLimit > 50 {
		out.RuntimeLimit = 10
	}
	if out.RuntimeWatchdogInterval < 0 {
		out.RuntimeWatchdogInterval = 0
	}
	if out.RuntimeWatchdogInterval == 0 {
		out.RuntimeWatchdogInterval = DefaultRuntimeWatchdogInterval
	}
	if out.RuntimeWatchdogLimit <= 0 || out.RuntimeWatchdogLimit > 100 {
		out.RuntimeWatchdogLimit = 20
	}
	if out.NotificationInterval < 0 {
		out.NotificationInterval = 0
	}
	if out.NotificationInterval == 0 {
		out.NotificationInterval = DefaultNotificationInterval
	}
	if out.NotificationLimit <= 0 || out.NotificationLimit > 200 {
		out.NotificationLimit = 40
	}
	return out
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(envOrDefault(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		log.Printf("invalid %s=%q, using %s", key, value, fallback)
		return fallback
	}
	return parsed
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(envOrDefault(key, ""))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q, using %d", key, value, fallback)
		return fallback
	}
	return parsed
}
