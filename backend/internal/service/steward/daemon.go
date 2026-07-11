package steward

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultHeartbeatInterval = time.Minute
)

type DaemonOptions struct {
	HeartbeatInterval time.Duration
	SyncInterval      time.Duration
	AutonomyInterval  time.Duration
	AutonomyLimit     int
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
		HeartbeatInterval: durationEnv("STEWARD_HEARTBEAT_INTERVAL", DefaultHeartbeatInterval),
		SyncInterval:      durationEnv("STEWARD_SYNC_INTERVAL", 0),
		AutonomyInterval:  durationEnv("STEWARD_AUTONOMY_INTERVAL", 0),
		AutonomyLimit:     intEnv("STEWARD_AUTONOMY_LIMIT", 12),
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
	for _, loop := range []struct {
		name     string
		interval time.Duration
	}{
		{name: "heartbeat", interval: d.options.HeartbeatInterval},
		{name: "sync", interval: d.options.SyncInterval},
		{name: "autonomy", interval: d.options.AutonomyInterval},
	} {
		if err := d.service.configureDaemonLoop(ctx, loop.name, loop.interval, loop.interval > 0); err != nil {
			log.Printf("steward daemon %s loop status initialization failed: %v", loop.name, err)
		}
	}

	started := false
	started = d.startLoop(ctx, "heartbeat", d.options.HeartbeatInterval, func(ctx context.Context) error {
		return d.service.Heartbeat(ctx, "")
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
	if out.AutonomyInterval < 0 {
		out.AutonomyInterval = 0
	}
	if out.AutonomyLimit <= 0 || out.AutonomyLimit > 50 {
		out.AutonomyLimit = 12
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
