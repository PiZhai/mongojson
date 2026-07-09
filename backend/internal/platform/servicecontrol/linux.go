//go:build linux

package servicecontrol

import (
	"context"
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
	env := Environment(options)
	content := linuxUnitContent(options, env)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        unitName,
		Scope:       options.Scope,
		Files:       []string{unitPath},
		Environment: redactedEnvironment(env),
		Commands: []string{
			linuxSystemctlCommand(options.Scope, "daemon-reload"),
			linuxSystemctlCommand(options.Scope, "enable", unitName),
		},
	}
	if options.DryRun {
		result.Message = "dry run: systemd " + options.Scope + " unit would be written and enabled"
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("create systemd %s dir: %w", options.Scope, err)
	}
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write systemd unit: %w", err)
	}
	if err := runSystemctl(ctx, options.Scope, "daemon-reload"); err != nil {
		return Result{}, err
	}
	if err := runSystemctl(ctx, options.Scope, "enable", unitName); err != nil {
		return Result{}, err
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
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return Result{}, fmt.Errorf("read systemd unit: %w", err)
	}
	current, err := linuxUnitEnvironment(string(data))
	if err != nil {
		return Result{}, err
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	updated, err := replaceLinuxUnitEnvironment(string(data), next)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform:    runtime.GOOS,
		Name:        unitName,
		Scope:       options.Scope,
		Files:       []string{unitPath},
		Environment: redactedEnvironment(next),
		Commands: []string{
			linuxSystemctlCommand(options.Scope, "daemon-reload"),
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
	if err := os.WriteFile(unitPath, []byte(updated), 0o644); err != nil {
		return Result{}, fmt.Errorf("write systemd unit: %w", err)
	}
	if err := runSystemctl(ctx, options.Scope, "daemon-reload"); err != nil {
		return Result{}, err
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
	result := Result{
		Platform: runtime.GOOS,
		Name:     unitName,
		Scope:    scope,
		Files:    []string{unitPath},
		Commands: []string{
			linuxSystemctlCommand(scope, "disable", "--now", unitName),
			commandString("rm", unitPath),
			linuxSystemctlCommand(scope, "daemon-reload"),
		},
	}
	if dryRun {
		result.Message = "dry run: systemd " + scope + " unit would be disabled and removed"
		return result, nil
	}
	_ = runSystemctl(ctx, scope, "disable", "--now", unitName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove systemd unit: %w", err)
	}
	if err := runSystemctl(ctx, scope, "daemon-reload"); err != nil {
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

func linuxUnitContent(options InstallOptions, env map[string]string) string {
	args := append([]string{options.BinaryPath}, serviceRunArgs(options)...)
	envLines := make([]string, 0, len(env))
	for _, item := range envList(env) {
		envLines = append(envLines, "Environment="+strconv.Quote(item))
	}
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
		strings.Join(envLines, "\n"),
		"ExecStart=" + systemdCommand(args),
		"Restart=always",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=" + wantedBy,
		"",
	}, "\n")
}

func linuxUnitEnvironment(content string) (map[string]string, error) {
	env := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Environment=") {
			continue
		}
		value := strings.TrimPrefix(line, "Environment=")
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			unquoted = value
		}
		key, envValue, ok := strings.Cut(unquoted, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid systemd Environment entry %q", line)
		}
		env[key] = envValue
	}
	return env, nil
}

func replaceLinuxUnitEnvironment(content string, env map[string]string) (string, error) {
	envLines := make([]string, 0, len(env))
	for _, item := range envList(env) {
		envLines = append(envLines, "Environment="+strconv.Quote(item))
	}
	lines := strings.Split(content, "\n")
	next := make([]string, 0, len(lines)+len(envLines))
	inService := false
	inserted := false
	sawService := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inService && !inserted {
				next = append(next, envLines...)
				inserted = true
			}
			inService = trimmed == "[Service]"
			if inService {
				sawService = true
			}
		}
		if inService && strings.HasPrefix(trimmed, "Environment=") {
			continue
		}
		if inService && !inserted && strings.HasPrefix(trimmed, "ExecStart=") {
			next = append(next, envLines...)
			inserted = true
		}
		next = append(next, line)
	}
	if !sawService {
		return "", fmt.Errorf("systemd unit does not contain a [Service] section")
	}
	if inService && !inserted {
		next = append(next, envLines...)
	}
	return strings.Join(next, "\n"), nil
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
