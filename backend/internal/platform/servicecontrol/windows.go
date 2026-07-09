//go:build windows

package servicecontrol

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func runPlatform(name string, run func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return runWithSignals(run)
	}
	return svc.Run(name, windowsService{run: run})
}

type windowsService struct {
	run func(context.Context) error
}

func (s windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-errCh:
			if err != nil {
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-errCh
				if err != nil {
					return false, 1
				}
				return false, 0
			default:
				changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			}
		}
	}
}

func installPlatform(ctx context.Context, input InstallOptions) (Result, error) {
	options, err := NormalizeInstallOptions(input)
	if err != nil {
		return Result{}, err
	}
	env := Environment(options)
	args := serviceRunArgs(options)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Environment: redactedEnvironment(env),
		Commands: []string{
			commandString("sc.exe", "create", options.Name, "binPath=", commandString(options.BinaryPath, args...), "start=", "auto", "DisplayName=", options.DisplayName),
			"registry Environment=" + fmt.Sprintf("%q", redactedEnvList(env)),
		},
	}
	if options.DryRun {
		result.Message = "dry run: Windows service would be created"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	manager, err := mgr.Connect()
	if err != nil {
		return Result{}, fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.CreateService(options.Name, options.BinaryPath, mgr.Config{
		DisplayName: options.DisplayName,
		Description: options.Description,
		StartType:   mgr.StartAutomatic,
	}, args...)
	if err != nil {
		return Result{}, fmt.Errorf("create Windows service: %w", err)
	}
	defer service.Close()
	if err := setWindowsServiceEnv(options.Name, env); err != nil {
		return Result{}, err
	}
	result.Message = "Windows service installed"
	return result, nil
}

func envPatchPlatform(ctx context.Context, input EnvPatchOptions) (Result, error) {
	options, err := NormalizeEnvPatchOptions(input)
	if err != nil {
		return Result{}, err
	}
	current, err := getWindowsServiceEnv(options.Name)
	if err != nil {
		return Result{}, err
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Environment: redactedEnvironment(next),
		Commands: []string{
			"registry Environment=" + fmt.Sprintf("%q", redactedEnvList(next)),
			commandString("sc.exe", "stop", options.Name),
			commandString("sc.exe", "start", options.Name),
		},
	}
	if options.DryRun {
		result.Message = "dry run: Windows service environment would be updated; restart the service for changes to take effect"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if err := setWindowsServiceEnv(options.Name, next); err != nil {
		return Result{}, err
	}
	result.Message = "Windows service environment updated; restart the service for changes to take effect"
	return result, nil
}

func uninstallPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform: runtime.GOOS,
		Name:     name,
		Scope:    scope,
		Commands: []string{
			commandString("sc.exe", "delete", name),
		},
	}
	if dryRun {
		result.Message = "dry run: Windows service would be deleted"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if err := service.Delete(); err != nil {
		return Result{}, fmt.Errorf("delete Windows service: %w", err)
	}
	result.Message = "Windows service deleted"
	return result, nil
}

func startPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("sc.exe", "start", name)}}
	if dryRun {
		result.Message = "dry run: Windows service would be started"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if err := service.Start(); err != nil {
		return Result{}, fmt.Errorf("start Windows service: %w", err)
	}
	result.Message = "Windows service start requested"
	return result, nil
}

func stopPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("sc.exe", "stop", name)}}
	if dryRun {
		result.Message = "dry run: Windows service would be stopped"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if _, err := service.Control(svc.Stop); err != nil {
		return Result{}, fmt.Errorf("stop Windows service: %w", err)
	}
	result.Message = "Windows service stop requested"
	return result, nil
}

func restartPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform: runtime.GOOS,
		Name:     name,
		Scope:    scope,
		Commands: []string{
			commandString("sc.exe", "stop", name),
			commandString("sc.exe", "start", name),
		},
	}
	if dryRun {
		result.Message = "dry run: Windows service would be restarted"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return Result{}, fmt.Errorf("query Windows service before restart: %w", err)
	}
	if status.State != svc.Stopped {
		if _, err := service.Control(svc.Stop); err != nil {
			return Result{}, fmt.Errorf("stop Windows service before restart: %w", err)
		}
		if err := waitWindowsServiceState(ctx, service, svc.Stopped); err != nil {
			return Result{}, err
		}
	}
	if err := service.Start(); err != nil {
		return Result{}, fmt.Errorf("start Windows service after restart: %w", err)
	}
	result.Message = "Windows service restart requested"
	return result, nil
}

func waitWindowsServiceState(ctx context.Context, service *mgr.Service, want svc.State) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("query Windows service state: %w", err)
		}
		if status.State == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func statusPlatform(ctx context.Context, name string, scope string) (StatusResult, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return StatusResult{}, err
	}
	select {
	case <-ctx.Done():
		return StatusResult{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return StatusResult{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return StatusResult{}, fmt.Errorf("query Windows service: %w", err)
	}
	return StatusResult{Platform: runtime.GOOS, Name: name, Scope: scope, Status: windowsServiceState(status.State), Detail: fmt.Sprintf("pid=%d", status.ProcessId)}, nil
}

func openWindowsService(name string) (*mgr.Service, *mgr.Mgr, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("connect service manager: %w", err)
	}
	service, err := manager.OpenService(name)
	if err != nil {
		manager.Disconnect()
		return nil, nil, fmt.Errorf("open Windows service: %w", err)
	}
	return service, manager, nil
}

func setWindowsServiceEnv(name string, env map[string]string) error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open service registry key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringsValue("Environment", envList(env)); err != nil {
		return fmt.Errorf("set service environment: %w", err)
	}
	return nil
}

func getWindowsServiceEnv(name string) (map[string]string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open service registry key: %w", err)
	}
	defer key.Close()
	values, _, err := key.GetStringsValue("Environment")
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read service environment: %w", err)
	}
	return parseEnvList(values), nil
}

func windowsServiceState(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown_%d", state)
	}
}

func waitBriefly() {
	time.Sleep(500 * time.Millisecond)
}
