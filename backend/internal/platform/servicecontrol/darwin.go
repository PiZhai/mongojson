//go:build darwin

package servicecontrol

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
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
	plistPath, err := launchServicePath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	env := Environment(options)
	domain := launchctlDomain(options.Name, options.Scope)
	parentDomain := launchctlParentDomain(options.Scope)
	serviceKind := launchServiceKind(options.Scope)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Files:       []string{plistPath},
		Environment: redactedEnvironment(env),
		Commands: []string{
			commandString("launchctl", "bootstrap", parentDomain, plistPath),
			commandString("launchctl", "enable", domain),
		},
	}
	if options.DryRun {
		result.Message = "dry run: " + serviceKind + " plist would be written and bootstrapped"
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("create %s dir: %w", serviceKind, err)
	}
	if err := os.WriteFile(plistPath, []byte(launchAgentPlist(options, env)), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s plist: %w", serviceKind, err)
	}
	if err := runCommand(ctx, "launchctl", "bootstrap", parentDomain, plistPath); err != nil {
		return Result{}, err
	}
	if err := runCommand(ctx, "launchctl", "enable", domain); err != nil {
		return Result{}, err
	}
	result.Message = serviceKind + " installed"
	return result, nil
}

func envPatchPlatform(ctx context.Context, input EnvPatchOptions) (Result, error) {
	options, err := NormalizeEnvPatchOptions(input)
	if err != nil {
		return Result{}, err
	}
	plistPath, err := launchServicePath(options.Name, options.Scope)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return Result{}, fmt.Errorf("read LaunchAgent plist: %w", err)
	}
	current, err := launchAgentEnvironment(string(data))
	if err != nil {
		return Result{}, err
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	updated, err := replaceLaunchAgentEnvironment(string(data), next)
	if err != nil {
		return Result{}, err
	}
	domain := launchctlDomain(options.Name, options.Scope)
	serviceKind := launchServiceKind(options.Scope)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Files:       []string{plistPath},
		Environment: redactedEnvironment(next),
		Commands: []string{
			"update EnvironmentVariables in " + plistPath,
			commandString("launchctl", "kickstart", "-k", domain),
		},
	}
	if options.DryRun {
		result.Message = "dry run: " + serviceKind + " environment would be updated; kickstart the service for changes to take effect"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if err := os.WriteFile(plistPath, []byte(updated), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s plist: %w", serviceKind, err)
	}
	result.Message = serviceKind + " environment updated; kickstart the service for changes to take effect"
	return result, nil
}

func uninstallPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	plistPath, err := launchServicePath(name, scope)
	if err != nil {
		return Result{}, err
	}
	domain := launchctlDomain(name, scope)
	serviceKind := launchServiceKind(scope)
	result := Result{
		Platform: runtime.GOOS,
		Name:     name,
		Scope:    scope,
		Files:    []string{plistPath},
		Commands: []string{
			commandString("launchctl", "bootout", domain),
			commandString("rm", plistPath),
		},
	}
	if dryRun {
		result.Message = "dry run: " + serviceKind + " would be booted out and removed"
		return result, nil
	}
	_ = runCommand(ctx, "launchctl", "bootout", domain)
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove %s plist: %w", serviceKind, err)
	}
	result.Message = serviceKind + " removed"
	return result, nil
}

func startPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	domain := launchctlDomain(name, scope)
	serviceKind := launchServiceKind(scope)
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("launchctl", "kickstart", "-k", domain)}}
	if dryRun {
		result.Message = "dry run: " + serviceKind + " would be kicked"
		return result, nil
	}
	if err := runCommand(ctx, "launchctl", "kickstart", "-k", domain); err != nil {
		return Result{}, err
	}
	result.Message = serviceKind + " start requested"
	return result, nil
}

func stopPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	domain := launchctlDomain(name, scope)
	serviceKind := launchServiceKind(scope)
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("launchctl", "kill", "TERM", domain)}}
	if dryRun {
		result.Message = "dry run: " + serviceKind + " would receive TERM"
		return result, nil
	}
	if err := runCommand(ctx, "launchctl", "kill", "TERM", domain); err != nil {
		return Result{}, err
	}
	result.Message = serviceKind + " stop requested"
	return result, nil
}

func restartPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	domain := launchctlDomain(name, scope)
	serviceKind := launchServiceKind(scope)
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("launchctl", "kickstart", "-k", domain)}}
	if dryRun {
		result.Message = "dry run: " + serviceKind + " would be kickstarted"
		return result, nil
	}
	if err := runCommand(ctx, "launchctl", "kickstart", "-k", domain); err != nil {
		return Result{}, err
	}
	result.Message = serviceKind + " restart requested"
	return result, nil
}

func statusPlatform(ctx context.Context, name string, scope string) (StatusResult, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return StatusResult{}, err
	}
	domain := launchctlDomain(name, scope)
	cmd := exec.CommandContext(ctx, "launchctl", "print", domain)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return StatusResult{Platform: runtime.GOOS, Name: name, Scope: scope, Status: "not_loaded", Detail: strings.TrimSpace(string(output))}, nil
	}
	return StatusResult{Platform: runtime.GOOS, Name: name, Scope: scope, Status: "loaded", Detail: firstLine(string(output))}, nil
}

func launchServicePath(name string, scope string) (string, error) {
	name = defaultString(name, DefaultName())
	if scope == ScopeSystem {
		return filepath.Join("/Library", "LaunchDaemons", name+".plist"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", name+".plist"), nil
}

func launchctlParentDomain(scope string) string {
	if scope == ScopeSystem {
		return "system"
	}
	return "gui/" + strconv.Itoa(os.Getuid())
}

func launchctlDomain(name string, scope string) string {
	return launchctlParentDomain(scope) + "/" + defaultString(name, DefaultName())
}

func launchServiceKind(scope string) string {
	if scope == ScopeSystem {
		return "LaunchDaemon"
	}
	return "LaunchAgent"
}

func launchAgentPlist(options InstallOptions, env map[string]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	plistKeyValue(&builder, "Label", options.Name)
	builder.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range append([]string{options.BinaryPath}, serviceRunArgs(options)...) {
		builder.WriteString("    <string>" + escapeXML(arg) + "</string>\n")
	}
	builder.WriteString("  </array>\n")
	plistKeyValue(&builder, "WorkingDirectory", options.WorkDir)
	builder.WriteString(launchAgentEnvironmentBlock(env))
	builder.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	builder.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	if strings.TrimSpace(options.LogDir) != "" {
		plistKeyValue(&builder, "StandardOutPath", filepath.Join(options.LogDir, options.Name+".out.log"))
		plistKeyValue(&builder, "StandardErrorPath", filepath.Join(options.LogDir, options.Name+".err.log"))
	}
	builder.WriteString("</dict>\n</plist>\n")
	return builder.String()
}

func launchAgentEnvironmentBlock(env map[string]string) string {
	var builder strings.Builder
	builder.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	for _, item := range envList(env) {
		key, value, _ := strings.Cut(item, "=")
		plistKeyValue(&builder, key, value)
	}
	builder.WriteString("  </dict>\n")
	return builder.String()
}

func launchAgentEnvironment(content string) (map[string]string, error) {
	inner, err := launchAgentEnvironmentInner(content)
	if err != nil {
		return nil, err
	}
	return parsePlistStringDict(inner)
}

func replaceLaunchAgentEnvironment(content string, env map[string]string) (string, error) {
	start, end, err := launchAgentEnvironmentBounds(content)
	if err != nil {
		return "", err
	}
	return content[:start] + launchAgentEnvironmentBlock(env) + content[end:], nil
}

func launchAgentEnvironmentInner(content string) (string, error) {
	start, end, err := launchAgentEnvironmentBounds(content)
	if err != nil {
		return "", err
	}
	prefix := "  <key>EnvironmentVariables</key>\n  <dict>\n"
	suffix := "  </dict>\n"
	return content[start+len(prefix) : end-len(suffix)], nil
}

func launchAgentEnvironmentBounds(content string) (int, int, error) {
	prefix := "  <key>EnvironmentVariables</key>\n  <dict>\n"
	suffix := "  </dict>\n"
	start := strings.Index(content, prefix)
	if start < 0 {
		return 0, 0, fmt.Errorf("LaunchAgent plist does not contain EnvironmentVariables block")
	}
	innerStart := start + len(prefix)
	relativeEnd := strings.Index(content[innerStart:], suffix)
	if relativeEnd < 0 {
		return 0, 0, fmt.Errorf("LaunchAgent plist EnvironmentVariables block is not closed")
	}
	return start, innerStart + relativeEnd + len(suffix), nil
}

func parsePlistStringDict(fragment string) (map[string]string, error) {
	decoder := xml.NewDecoder(strings.NewReader("<dict>\n" + fragment + "</dict>"))
	env := map[string]string{}
	var key string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse EnvironmentVariables plist fragment: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, err
			}
			key = value
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, err
			}
			if strings.TrimSpace(key) != "" {
				env[key] = value
			}
			key = ""
		}
	}
	return env, nil
}

func plistKeyValue(builder *strings.Builder, key string, value string) {
	builder.WriteString("  <key>" + escapeXML(key) + "</key>\n")
	builder.WriteString("  <string>" + escapeXML(value) + "</string>\n")
}

func escapeXML(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w: %s", commandString(name, args...), err, strings.TrimSpace(string(output)))
	}
	return nil
}
