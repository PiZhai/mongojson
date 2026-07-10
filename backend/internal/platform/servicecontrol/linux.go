//go:build linux

package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func runPlatform(_ string, run func(context.Context) error) error {
	return runWithSignals(run)
}

func installPlatform(ctx context.Context, input InstallOptions) (Result, error) {
	options, err := NormalizeInstallOptions(input)
	if err != nil {
		return Result{}, err
	}
	unitPath, unitName, err := linuxUnitPath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	envPath, err := linuxEnvironmentPath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	env := Environment(options)
	content := linuxUnitContent(options, envPath)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        unitName,
		Scope:       options.Scope,
		Files:       []string{unitPath, envPath},
		Environment: redactedEnvironment(env),
		Commands: []string{
			"write private service environment " + envPath + " mode=0600",
			linuxSystemctlCommand(options.Scope, "daemon-reload"),
			linuxSystemctlCommand(options.Scope, "enable", unitName),
		},
	}
	if options.DryRun {
		result.Message = "dry run: systemd " + options.Scope + " unit and private environment file would be written and enabled"
		return result, nil
	}
	if err := ensureServicePathAbsent(unitPath, "systemd unit"); err != nil {
		return Result{}, err
	}
	if err := ensureServicePathAbsent(envPath, "systemd environment file"); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("create systemd %s dir: %w", options.Scope, err)
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create private systemd environment dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(envPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("protect private systemd environment dir: %w", err)
	}
	if err := writeNewServiceFile(envPath, []byte(renderSystemdEnvironmentFile(env)), 0o600); err != nil {
		return Result{}, fmt.Errorf("write private systemd environment file: %w", err)
	}
	if err := writeNewServiceFile(unitPath, []byte(content), 0o644); err != nil {
		_ = os.Remove(envPath)
		return Result{}, fmt.Errorf("write systemd unit: %w", err)
	}
	if err := runSystemctl(ctx, options.Scope, "daemon-reload"); err != nil {
		return Result{}, errors.Join(err, rollbackLinuxInstall(ctx, options.Scope, unitName, unitPath, envPath))
	}
	if err := runSystemctl(ctx, options.Scope, "enable", unitName); err != nil {
		return Result{}, errors.Join(err, rollbackLinuxInstall(ctx, options.Scope, unitName, unitPath, envPath))
	}
	result.Message = "systemd " + options.Scope + " unit installed"
	return result, nil
}

func envPatchPlatform(ctx context.Context, input EnvPatchOptions) (Result, error) {
	options, err := NormalizeEnvPatchOptions(input)
	if err != nil {
		return Result{}, err
	}
	unitPath, unitName, err := linuxUnitPath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	envPath, err := linuxEnvironmentPath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	current, legacyUnit, err := readLinuxServiceEnvironment(unitPath, envPath)
	if err != nil {
		return Result{}, err
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform:    runtime.GOOS,
		Name:        unitName,
		Scope:       options.Scope,
		Files:       []string{unitPath, envPath},
		Environment: redactedEnvironment(next),
		Commands: []string{
			"update private service environment " + envPath + " mode=0600",
			linuxSystemctlCommand(options.Scope, "restart", unitName),
		},
	}
	if options.DryRun {
		result.Message = "dry run: systemd " + options.Scope + " unit environment would be updated; reload and restart the service for changes to take effect"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("create private systemd environment dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(envPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("protect private systemd environment dir: %w", err)
	}
	if len(legacyUnit) == 0 {
		if err := writeServiceFileAtomic(envPath, []byte(renderSystemdEnvironmentFile(next)), 0o600); err != nil {
			return Result{}, fmt.Errorf("update private systemd environment file: %w", err)
		}
	} else {
		updatedUnit, err := replaceSystemdEnvironmentDirectives(string(legacyUnit), envPath)
		if err != nil {
			return Result{}, err
		}
		if err := writeNewServiceFile(envPath, []byte(renderSystemdEnvironmentFile(next)), 0o600); err != nil {
			return Result{}, fmt.Errorf("write migrated private systemd environment file: %w", err)
		}
		if err := writeServiceFileAtomic(unitPath, []byte(updatedUnit), 0o644); err != nil {
			_ = os.Remove(envPath)
			return Result{}, fmt.Errorf("migrate systemd unit to EnvironmentFile: %w", err)
		}
		if err := runSystemctl(ctx, options.Scope, "daemon-reload"); err != nil {
			_ = writeServiceFileAtomic(unitPath, legacyUnit, 0o644)
			_ = os.Remove(envPath)
			_ = runSystemctl(context.Background(), options.Scope, "daemon-reload")
			return Result{}, fmt.Errorf("reload migrated systemd unit: %w", err)
		}
	}
	result.Message = "systemd " + options.Scope + " unit environment updated; restart the service for changes to take effect"
	return result, nil
}

func uninstallPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	unitPath, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return Result{}, err
	}
	envPath, err := linuxEnvironmentPath(name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform: runtime.GOOS,
		Name:     unitName,
		Scope:    scope,
		Files:    []string{unitPath, envPath},
		Commands: []string{
			linuxSystemctlCommand(scope, "disable", "--now", unitName),
			commandString("rm", unitPath),
			commandString("rm", envPath),
			linuxSystemctlCommand(scope, "daemon-reload"),
		},
	}
	if dryRun {
		result.Message = "dry run: systemd " + scope + " unit would be disabled and removed"
		return result, nil
	}
	_ = runSystemctl(ctx, scope, "disable", "--now", unitName)
	var removeErrors []error
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		removeErrors = append(removeErrors, fmt.Errorf("remove systemd unit: %w", err))
	}
	if err := os.Remove(envPath); err != nil && !os.IsNotExist(err) {
		removeErrors = append(removeErrors, fmt.Errorf("remove private systemd environment file: %w", err))
	}
	if err := runSystemctl(ctx, scope, "daemon-reload"); err != nil {
		removeErrors = append(removeErrors, err)
	}
	if err := errors.Join(removeErrors...); err != nil {
		return Result{}, err
	}
	result.Message = "systemd " + scope + " unit removed"
	return result, nil
}

func startPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	_, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: unitName, Scope: scope, Commands: []string{linuxSystemctlCommand(scope, "start", unitName)}}
	if dryRun {
		result.Message = "dry run: systemd " + scope + " unit would be started"
		return result, nil
	}
	if err := runSystemctl(ctx, scope, "start", unitName); err != nil {
		return Result{}, err
	}
	result.Message = "systemd " + scope + " unit start requested"
	return result, nil
}

func stopPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	_, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: unitName, Scope: scope, Commands: []string{linuxSystemctlCommand(scope, "stop", unitName)}}
	if dryRun {
		result.Message = "dry run: systemd " + scope + " unit would be stopped"
		return result, nil
	}
	if err := runSystemctl(ctx, scope, "stop", unitName); err != nil {
		return Result{}, err
	}
	result.Message = "systemd " + scope + " unit stop requested"
	return result, nil
}

func restartPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	_, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: unitName, Scope: scope, Commands: []string{linuxSystemctlCommand(scope, "restart", unitName)}}
	if dryRun {
		result.Message = "dry run: systemd " + scope + " unit would be restarted"
		return result, nil
	}
	if err := runSystemctl(ctx, scope, "restart", unitName); err != nil {
		return Result{}, err
	}
	result.Message = "systemd " + scope + " unit restart requested"
	return result, nil
}

func statusPlatform(ctx context.Context, name string, scope string) (StatusResult, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return StatusResult{}, err
	}
	_, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return StatusResult{}, err
	}
	cmd := exec.CommandContext(ctx, "systemctl", linuxSystemctlArgs(scope, "is-active", unitName)...)
	output, err := cmd.CombinedOutput()
	status := strings.TrimSpace(string(output))
	if status == "" && err != nil {
		status = "unknown"
	}
	return StatusResult{Platform: runtime.GOOS, Name: unitName, Scope: scope, Status: status}, nil
}

func linuxUnitPath(name string, scope string) (string, string, error) {
	name = defaultString(name, DefaultName())
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	if scope == ScopeSystem {
		return filepath.Join("/etc", "systemd", "system", name), name, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", name), name, nil
}

func linuxEnvironmentPath(name string, scope string) (string, error) {
	_, unitName, err := linuxUnitPath(name, scope)
	if err != nil {
		return "", err
	}
	if scope == ScopeSystem {
		return filepath.Join("/etc", "mongojson-steward", unitName+".env"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "mongojson-steward", unitName+".env"), nil
}

func linuxUnitContent(options InstallOptions, envFilePath string) string {
	args := append([]string{options.BinaryPath}, serviceRunArgs(options)...)
	wantedBy := "default.target"
	if options.Scope == ScopeSystem {
		wantedBy = "multi-user.target"
	}
	return strings.Join([]string{
		"[Unit]",
		"Description=" + options.Description,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"WorkingDirectory=" + strconv.Quote(options.WorkDir),
		"EnvironmentFile=" + strconv.Quote(envFilePath),
		"ExecStart=" + systemdCommand(args),
		"Restart=always",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=" + wantedBy,
		"",
	}, "\n")
}

func readLinuxServiceEnvironment(unitPath string, envPath string) (map[string]string, []byte, error) {
	data, err := os.ReadFile(envPath)
	if err == nil {
		env, parseErr := parseSystemdEnvironmentFile(string(data))
		return env, nil, parseErr
	}
	if !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("read private systemd environment file: %w", err)
	}
	legacyUnit, err := os.ReadFile(unitPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read systemd unit: %w", err)
	}
	env, err := parseInlineSystemdEnvironment(string(legacyUnit))
	if err != nil {
		return nil, nil, err
	}
	return env, legacyUnit, nil
}

func rollbackLinuxInstall(ctx context.Context, scope string, unitName string, unitPath string, envPath string) error {
	var rollbackErrors []error
	if err := runSystemctl(ctx, scope, "disable", unitName); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback systemd enablement: %w", err))
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback systemd unit: %w", err))
	}
	if err := os.Remove(envPath); err != nil && !os.IsNotExist(err) {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("rollback private systemd environment file: %w", err))
	}
	if err := runSystemctl(context.Background(), scope, "daemon-reload"); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("reload systemd after rollback: %w", err))
	}
	return errors.Join(rollbackErrors...)
}

func systemdCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return strings.Join(quoted, " ")
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", commandString(name, args...), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func linuxSystemctlArgs(scope string, args ...string) []string {
	if scope == ScopeSystem {
		return args
	}
	return append([]string{"--user"}, args...)
}

func linuxSystemctlCommand(scope string, args ...string) string {
	return commandString("systemctl", linuxSystemctlArgs(scope, args...)...)
}

func runSystemctl(ctx context.Context, scope string, args ...string) error {
	return runCommand(ctx, "systemctl", linuxSystemctlArgs(scope, args...)...)
}
