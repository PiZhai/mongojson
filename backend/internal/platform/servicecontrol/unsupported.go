//go:build !windows && !linux && !darwin

package servicecontrol

import (
	"context"
	"fmt"
	"runtime"
)

func protectServicePathsPlatform(_ string, _ []string) error { return nil }

func runPlatform(_ string, run func(context.Context) error) error {
	return runWithSignals(run)
}

func installPlatform(_ context.Context, input InstallOptions) (Result, error) {
	return Result{Platform: runtime.GOOS, Name: defaultString(input.Name, DefaultName()), Scope: defaultString(input.Scope, DefaultScope())}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func envPatchPlatform(_ context.Context, input EnvPatchOptions) (Result, error) {
	return Result{Platform: runtime.GOOS, Name: defaultString(input.Name, DefaultName()), Scope: defaultString(input.Scope, DefaultScope())}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func uninstallPlatform(_ context.Context, name string, scope string, _ bool) (Result, error) {
	return Result{Platform: runtime.GOOS, Name: name, Scope: scope}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func startPlatform(_ context.Context, name string, scope string, _ bool) (Result, error) {
	return Result{Platform: runtime.GOOS, Name: name, Scope: scope}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func stopPlatform(_ context.Context, name string, scope string, _ bool) (Result, error) {
	return Result{Platform: runtime.GOOS, Name: name, Scope: scope}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func restartPlatform(_ context.Context, name string, scope string, dryRun bool) (Result, error) {
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope}
	if dryRun {
		result.Message = "dry run: unsupported platform service would be restarted"
		return result, nil
	}
	return result, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}

func statusPlatform(_ context.Context, name string, scope string) (StatusResult, error) {
	return StatusResult{Platform: runtime.GOOS, Name: name, Scope: scope, Status: "unsupported"}, fmt.Errorf("system service control is not implemented on %s", runtime.GOOS)
}
