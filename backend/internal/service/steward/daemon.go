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
)

const (
	DefaultHeartbeatInterval       = time.Minute
	DefaultCollectionInterval      = 5 * time.Minute
	DefaultModelDispatchInterval   = time.Minute
	DefaultRuntimeInterval         = time.Second
	DefaultRuntimeWatchdogInterval = 2 * time.Second
)

type DaemonOptions struct {
	HeartbeatInterval       time.Duration
	CollectionInterval      time.Duration
	SyncInterval            time.Duration
	AutonomyInterval        time.Duration
	AutonomyLimit           int
	ModelDispatchInterval   time.Duration
	ModelDispatchLimit      int
	RuntimeInterval         time.Duration
	RuntimeLimit            int
	RuntimeWatchdogInterval time.Duration
	RuntimeWatchdogLimit    int
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
		HeartbeatInterval:       durationEnv("STEWARD_HEARTBEAT_INTERVAL", DefaultHeartbeatInterval),
		CollectionInterval:      durationEnv("STEWARD_COLLECTION_INTERVAL", DefaultCollectionInterval),
		SyncInterval:            durationEnv("STEWARD_SYNC_INTERVAL", 0),
		AutonomyInterval:        durationEnv("STEWARD_AUTONOMY_INTERVAL", 0),
		AutonomyLimit:           intEnv("STEWARD_AUTONOMY_LIMIT", 12),
		ModelDispatchInterval:   durationEnv("STEWARD_MODEL_DISPATCH_INTERVAL", DefaultModelDispatchInterval),
		ModelDispatchLimit:      intEnv("STEWARD_MODEL_DISPATCH_LIMIT", 20),
		RuntimeInterval:         durationEnv("STEWARD_RUNTIME_INTERVAL", DefaultRuntimeInterval),
		RuntimeLimit:            intEnv("STEWARD_RUNTIME_LIMIT", 10),
		RuntimeWatchdogInterval: durationEnv("STEWARD_RUNTIME_WATCHDOG_INTERVAL", DefaultRuntimeWatchdogInterval),
		RuntimeWatchdogLimit:    intEnv("STEWARD_RUNTIME_WATCHDOG_LIMIT", 20),
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
		{name: "sync", interval: d.options.SyncInterval},
		{name: "autonomy", interval: d.options.AutonomyInterval},
		{name: "model-dispatch", interval: d.options.ModelDispatchInterval},
		{name: "runtime-v2", interval: runtimeInterval},
		{name: "runtime-watchdog", interval: runtimeWatchdogInterval},
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
		_, err = d.service.RunModelDispatches(ctx, d.options.ModelDispatchLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "runtime-watchdog", runtimeWatchdogInterval, func(ctx context.Context) error {
		_, err := d.service.RunAgentRuntimeWatchdog(ctx, d.options.RuntimeWatchdogLimit)
		return err
	}) || started
	started = d.startLoop(ctx, "runtime-v2", runtimeInterval, func(ctx context.Context) error {
		_, err := d.service.RunAgentRuntimeCycle(ctx, d.options.RuntimeLimit)
		return err
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

func (d *Daemon) runOnce(ctx context.Context, name string, run func(context.Context) error) {
	startedAt := time.Now().UTC()
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
