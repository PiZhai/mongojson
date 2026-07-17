package steward

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"mongojson/backend/internal/domain"
)

const runtimeCommandOutputLimit = 1 << 20

type runtimeShellExecTool struct{ service *Service }

func newRuntimeShellExecTool(service *Service) RuntimeTool {
	return runtimeShellExecTool{service: service}
}

func (runtimeShellExecTool) Spec() domain.StewardToolSpec {
	description := "Execute one explicitly allowlisted executable directly, without cmd/sh/PowerShell interpretation or secret-bearing service environment variables."
	if ownerModeEnabled() {
		description = "Execute any installed executable directly on the owner's device with structured arguments and a minimal process environment."
	}
	return domain.StewardToolSpec{
		Name: "shell.exec", Version: "2.0.0",
		Description: description,
		InputSchema: map[string]any{"type": "object", "required": []string{"command"}, "properties": map[string]any{
			"command": map[string]any{"type": "string"}, "args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "working_directory": map[string]any{"type": "string"},
		}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"command", "args", "working_directory", "exit_code", "stdout", "stderr"}},
		PermissionLevel: PermissionA3, RiskLevel: "medium", SideEffect: RuntimeSideEffectProcess,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyNonIdempotent,
		Deterministic: false, SupportsCancel: true, DefaultTimeoutSec: 60,
	}
}

func (t runtimeShellExecTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "command", "args", "working_directory"); err != nil {
		return err
	}
	command, err := runtimeRequiredString(input, "command")
	if err != nil {
		return err
	}
	if _, err := t.service.resolveRuntimeExecutable(command); err != nil {
		return err
	}
	arguments, err := runtimeStringSlice(input, "args")
	if err != nil {
		return err
	}
	if len(arguments) > 100 {
		return fmt.Errorf("args must not exceed 100 entries")
	}
	for _, argument := range arguments {
		if len([]rune(argument)) > 4000 {
			return fmt.Errorf("one command argument exceeds 4000 characters")
		}
		switch strings.TrimSpace(argument) {
		case "&&", "||", ";", "|", ">", ">>", "<", "2>", "2>&1":
			return fmt.Errorf("shell operator %q is not accepted; shell interpretation is disabled", argument)
		}
	}
	workingDirectory, err := runtimeOptionalString(input, "working_directory")
	if err != nil {
		return err
	}
	if workingDirectory == "" {
		if len(t.service.runtimeAllowedRoots) == 0 {
			return fmt.Errorf("no allowed working directory is configured")
		}
		workingDirectory = t.service.runtimeAllowedRoots[0]
	}
	resolved, err := t.service.resolveRuntimePath(workingDirectory, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("working_directory must be an existing allowlisted directory")
	}
	return nil
}

func (t runtimeShellExecTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	command, _ := runtimeRequiredString(input, "command")
	resolvedCommand, _ := t.service.resolveRuntimeExecutable(command)
	arguments, _ := runtimeStringSlice(input, "args")
	workingDirectory, _ := runtimeOptionalString(input, "working_directory")
	if workingDirectory == "" {
		workingDirectory = t.service.runtimeAllowedRoots[0]
	}
	workingDirectory, _ = t.service.resolveRuntimePath(workingDirectory, true)
	process := exec.Command(resolvedCommand, arguments...)
	process.Dir = workingDirectory
	process.Env = sanitizedRuntimeEnvironment()
	stdout := &runtimeLimitedBuffer{limit: runtimeCommandOutputLimit}
	stderr := &runtimeLimitedBuffer{limit: runtimeCommandOutputLimit}
	process.Stdout = stdout
	process.Stderr = stderr
	err := runRuntimeCommand(ctx, process)
	exitCode := 0
	if process.ProcessState != nil {
		exitCode = process.ProcessState.ExitCode()
	}
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("command exited with code %d: %s", exitCode, truncateAdvisorText(stderr.String(), 2000))
	}
	output := map[string]any{
		"command": resolvedCommand, "args": arguments, "working_directory": workingDirectory,
		"exit_code": exitCode, "stdout": stdout.String(), "stderr": stderr.String(),
		"stdout_truncated": stdout.truncated, "stderr_truncated": stderr.truncated,
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "process_exit", Summary: fmt.Sprintf("allowlisted process exited with code %d", exitCode), Payload: map[string]any{
		"command": resolvedCommand, "working_directory": workingDirectory, "exit_code": exitCode,
		"stdout_truncated": stdout.truncated, "stderr_truncated": stderr.truncated,
	}}}}, nil
}

func (runtimeShellExecTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	remaining := make(map[string]any, len(expected))
	for key, value := range expected {
		remaining[key] = value
	}
	if fmt.Sprint(output["exit_code"]) != "0" {
		return fmt.Errorf("process exit code is not zero")
	}
	if contains, ok := remaining["stdout_contains"].(string); ok {
		stdout, _ := output["stdout"].(string)
		if !strings.Contains(stdout, contains) {
			return fmt.Errorf("stdout does not contain expected text")
		}
		delete(remaining, "stdout_contains")
	}
	if contains, ok := remaining["stderr_contains"].(string); ok {
		stderr, _ := output["stderr"].(string)
		if !strings.Contains(stderr, contains) {
			return fmt.Errorf("stderr does not contain expected text")
		}
		delete(remaining, "stderr_contains")
	}
	return runtimeOutputMatchesExpected(output, remaining)
}

func (s *Service) resolveRuntimeExecutable(command string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("%w: STEWARD_RUNTIME_EXECUTABLES is empty", ErrRuntimeCommandDenied)
	}
	resolved := strings.TrimSpace(command)
	if !filepath.IsAbs(resolved) {
		path, err := exec.LookPath(resolved)
		if err != nil {
			return "", fmt.Errorf("%w: %s was not found", ErrRuntimeCommandDenied, command)
		}
		resolved = path
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if evaluated, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(evaluated)
	}
	if ownerModeEnabled() {
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("%w: %s is not an executable file", ErrRuntimeCommandDenied, absolute)
		}
		return absolute, nil
	}
	if len(s.runtimeExecutables) == 0 {
		return "", fmt.Errorf("%w: STEWARD_RUNTIME_EXECUTABLES is empty", ErrRuntimeCommandDenied)
	}
	allowed, ok := s.runtimeExecutables[strings.ToLower(absolute)]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrRuntimeCommandDenied, absolute)
	}
	return allowed, nil
}

func sanitizedRuntimeEnvironment() []string {
	allowed := []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "TEMP", "TMP", "HOME", "USERPROFILE", "LANG", "LC_ALL", "TERM"}
	result := []string{}
	for _, key := range allowed {
		if value := os.Getenv(key); value != "" {
			result = append(result, key+"="+value)
		}
	}
	if runtime.GOOS == "windows" && os.Getenv("SYSTEMROOT") == "" {
		result = append(result, `SYSTEMROOT=C:\Windows`)
	}
	return result
}

type runtimeLimitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *runtimeLimitedBuffer) Write(payload []byte) (int, error) {
	originalLength := len(payload)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(payload) > remaining {
		payload = payload[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(payload)
	return originalLength, nil
}

func (b *runtimeLimitedBuffer) String() string { return b.buffer.String() }
