//go:build windows

package steward

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"mongojson/backend/internal/domain"
)

type runtimeWindowsPathEnsureTool struct{}

type windowsPathScope struct {
	Name string
	Root registry.Key
	Path string
}

var windowsPathScopes = map[string]windowsPathScope{
	"machine": {Name: "Machine", Root: registry.LOCAL_MACHINE, Path: `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	"user":    {Name: "User", Root: registry.CURRENT_USER, Path: `Environment`},
}

func newRuntimeWindowsPathEnsureTool() RuntimeTool { return &runtimeWindowsPathEnsureTool{} }

func (*runtimeWindowsPathEnsureTool) ChangeTransactionEnabled() bool { return true }

func (*runtimeWindowsPathEnsureTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "windows.path.ensure", Version: "1.0.0",
		Description: "Transactionally add an existing directory to Windows PATH. Tries scopes in order, verifies the registry and a fresh process environment, and cleans partial failed attempts before falling back.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"directory"},
			"properties": map[string]any{
				"directory":        map[string]any{"type": "string"},
				"scope_preference": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"Machine", "User"}}},
				"executable":       map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{"type": "object", "required": []string{"directory", "selected_scope", "verified", "attempts"}},
		SideEffect:   RuntimeSideEffectWrite, ApprovalMode: RuntimeApprovalAlways,
		IdempotencyMode: RuntimeIdempotencyKeyed, SupportsCancel: true, DefaultTimeoutSec: 30,
		PermissionLevel: PermissionA0, RiskLevel: "low",
	}
}

func (t *runtimeWindowsPathEnsureTool) Validate(input map[string]any) error {
	if _, err := runtimeRequiredString(input, "directory"); err != nil {
		return err
	}
	return runtimeRejectUnknownFields(input, "directory", "scope_preference", "executable")
}

func (t *runtimeWindowsPathEnsureTool) SnapshotChange(ctx context.Context, _ map[string]any) (RuntimeChangeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeChangeSnapshot{}, err
	}
	state := map[string]any{}
	for key, scope := range windowsPathScopes {
		value, valueType, exists, err := readWindowsPath(scope)
		if err != nil {
			state[key] = map[string]any{"readable": false, "error": sanitizeRuntimeError(err)}
			continue
		}
		state[key] = map[string]any{"readable": true, "exists": exists, "value": value, "value_type": valueType}
	}
	return RuntimeChangeSnapshot{Summary: "captured Machine and current-user PATH values", State: state}, nil
}

func (t *runtimeWindowsPathEnsureTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	directory, err := canonicalWindowsPath(input["directory"].(string))
	if err != nil {
		return RuntimeToolResult{}, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("PATH directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return RuntimeToolResult{}, fmt.Errorf("PATH target is not a directory: %s", directory)
	}
	scopeNames := requestedWindowsPathScopes(input["scope_preference"])
	attempts := make([]map[string]any, 0, len(scopeNames))
	var failures []string
	for _, scopeName := range scopeNames {
		if err := ctx.Err(); err != nil {
			return RuntimeToolResult{Output: map[string]any{"directory": directory, "attempts": attempts}}, err
		}
		scope := windowsPathScopes[strings.ToLower(scopeName)]
		attempt := map[string]any{"scope": scope.Name, "status": "started"}
		before, valueType, existed, readErr := readWindowsPath(scope)
		if readErr != nil {
			attempt["status"], attempt["error"] = "failed", sanitizeRuntimeError(readErr)
			attempts = append(attempts, attempt)
			failures = append(failures, scope.Name+": "+sanitizeRuntimeError(readErr))
			continue
		}
		if windowsPathContains(before, directory) {
			attempt["status"], attempt["changed"] = "already_present", false
			attempts = append(attempts, attempt)
			return t.verifiedResult(directory, scope, scopeNames, attempts, input, false)
		}
		next := appendWindowsPath(before, directory)
		if writeErr := writeWindowsPath(scope, next, valueType); writeErr != nil {
			cleanup := cleanupFailedWindowsPathAttempt(scope, before, valueType, existed, directory)
			attempt["status"], attempt["error"], attempt["cleanup"] = "failed", sanitizeRuntimeError(writeErr), cleanup
			attempts = append(attempts, attempt)
			failures = append(failures, scope.Name+": "+sanitizeRuntimeError(writeErr))
			continue
		}
		after, _, _, verifyErr := readWindowsPath(scope)
		if verifyErr != nil || !windowsPathContains(after, directory) {
			if verifyErr == nil {
				verifyErr = fmt.Errorf("registry value did not contain the requested directory after write")
			}
			cleanup := cleanupFailedWindowsPathAttempt(scope, before, valueType, existed, directory)
			attempt["status"], attempt["error"], attempt["cleanup"] = "verification_failed", sanitizeRuntimeError(verifyErr), cleanup
			attempts = append(attempts, attempt)
			failures = append(failures, scope.Name+": "+sanitizeRuntimeError(verifyErr))
			continue
		}
		attempt["status"], attempt["changed"] = "verified", true
		attempts = append(attempts, attempt)
		return t.verifiedResult(directory, scope, scopeNames, attempts, input, true)
	}
	return RuntimeToolResult{Output: map[string]any{"directory": directory, "attempts": attempts}, Evidence: []RuntimeEvidence{{
		Kind: "path_scope_fallback", Summary: "all requested PATH scopes failed and partial attempts were reconciled", Payload: map[string]any{"directory": directory, "attempts": attempts},
	}}}, fmt.Errorf("unable to add directory to Windows PATH: %s", strings.Join(failures, "; "))
}

func (t *runtimeWindowsPathEnsureTool) verifiedResult(directory string, scope windowsPathScope, requested []string, attempts []map[string]any, input map[string]any, changed bool) (RuntimeToolResult, error) {
	processPath := os.Getenv("PATH")
	if !windowsPathContains(processPath, directory) {
		processPath = appendWindowsPath(processPath, directory)
		_ = os.Setenv("PATH", processPath)
	}
	executable, _ := input["executable"].(string)
	resolved := ""
	if strings.TrimSpace(executable) != "" {
		path, err := exec.LookPath(executable)
		if err != nil {
			return RuntimeToolResult{Output: map[string]any{"directory": directory, "selected_scope": scope.Name, "attempts": attempts}}, fmt.Errorf("PATH registry write succeeded but executable verification failed: %w", err)
		}
		resolved = path
	}
	output := map[string]any{
		"directory": directory, "selected_scope": scope.Name, "requested_scopes": requested,
		"changed": changed, "verified": true, "attempts": attempts,
	}
	if resolved != "" {
		output["resolved_executable"] = resolved
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{
		Kind: "windows_path_verified", Summary: "Windows PATH scope was selected and independently reread", Payload: map[string]any{
			"directory": directory, "selected_scope": scope.Name, "changed": changed, "attempts": attempts, "resolved_executable": resolved,
		},
	}}}, nil
}

func (t *runtimeWindowsPathEnsureTool) Verify(ctx context.Context, input, output, expected map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, _ := output["directory"].(string)
	selected, _ := output["selected_scope"].(string)
	scope, ok := windowsPathScopes[strings.ToLower(selected)]
	if !ok || directory == "" {
		return fmt.Errorf("tool output omitted the selected PATH scope")
	}
	value, _, _, err := readWindowsPath(scope)
	if err != nil {
		return err
	}
	if !windowsPathContains(value, directory) {
		return fmt.Errorf("%s PATH no longer contains %s", scope.Name, directory)
	}
	if executable, _ := input["executable"].(string); strings.TrimSpace(executable) != "" {
		if _, err := exec.LookPath(executable); err != nil {
			return fmt.Errorf("fresh PATH lookup for %s: %w", executable, err)
		}
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (t *runtimeWindowsPathEnsureTool) VerifyChange(ctx context.Context, input map[string]any, _ RuntimeChangeSnapshot, result RuntimeToolResult) error {
	return t.Verify(ctx, input, result.Output, nil)
}

func (t *runtimeWindowsPathEnsureTool) RollbackChange(ctx context.Context, arguments map[string]any, snapshot RuntimeChangeSnapshot, _ RuntimeToolResult, _ error) (RuntimeToolResult, error) {
	if err := ctx.Err(); err != nil {
		return RuntimeToolResult{}, err
	}
	directory, err := canonicalWindowsPath(fmt.Sprint(arguments["directory"]))
	if err != nil {
		return RuntimeToolResult{}, err
	}
	items := []map[string]any{}
	var rollbackErrors []string
	ownedProcessEntry := true
	for key, scope := range windowsPathScopes {
		stored, _ := snapshot.State[key].(map[string]any)
		if readable, _ := stored["readable"].(bool); !readable {
			continue
		}
		before, _ := stored["value"].(string)
		if windowsPathContains(before, directory) {
			ownedProcessEntry = false
		}
		valueType := uint32(registry.SZ)
		if number, ok := stored["value_type"].(float64); ok {
			valueType = uint32(number)
		} else if number, ok := stored["value_type"].(uint32); ok {
			valueType = number
		}
		existed, _ := stored["exists"].(bool)
		current, _, _, readErr := readWindowsPath(scope)
		if readErr != nil {
			rollbackErrors = append(rollbackErrors, scope.Name+": "+sanitizeRuntimeError(readErr))
			continue
		}
		if windowsPathContains(before, directory) || !windowsPathContains(current, directory) {
			items = append(items, map[string]any{"scope": scope.Name, "changed": false, "reason": "no transaction-owned entry present"})
			continue
		}
		reconciled := removeWindowsPathEntry(current, directory)
		if err := restoreWindowsPath(scope, reconciled, valueType, existed || strings.TrimSpace(reconciled) != ""); err != nil {
			rollbackErrors = append(rollbackErrors, scope.Name+": "+sanitizeRuntimeError(err))
			continue
		}
		items = append(items, map[string]any{"scope": scope.Name, "changed": true, "restored_without_overwriting_concurrent_entries": true})
	}
	if ownedProcessEntry && windowsPathContains(os.Getenv("PATH"), directory) {
		if err := os.Setenv("PATH", removeWindowsPathEntry(os.Getenv("PATH"), directory)); err != nil {
			rollbackErrors = append(rollbackErrors, "Process: "+sanitizeRuntimeError(err))
		} else {
			items = append(items, map[string]any{"scope": "Process", "changed": true})
		}
	}
	output := map[string]any{"directory": directory, "scopes": items, "rolled_back": len(rollbackErrors) == 0}
	result := RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "windows_path_rollback", Summary: "removed only PATH entries owned by the failed transaction", Payload: output}}}
	if len(rollbackErrors) > 0 {
		return result, fmt.Errorf("PATH rollback incomplete: %s", strings.Join(rollbackErrors, "; "))
	}
	return result, nil
}

func requestedWindowsPathScopes(raw any) []string {
	values := []string{}
	switch typed := raw.(type) {
	case []any:
		for _, item := range typed {
			values = append(values, fmt.Sprint(item))
		}
	case []string:
		values = append(values, typed...)
	}
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if _, ok := windowsPathScopes[key]; ok && !seen[key] {
			seen[key] = true
			result = append(result, windowsPathScopes[key].Name)
		}
	}
	if len(result) == 0 {
		return []string{"Machine", "User"}
	}
	return result
}

func canonicalWindowsPath(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(os.ExpandEnv(value)), `"`)
	if value == "" {
		return "", fmt.Errorf("directory is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func readWindowsPath(scope windowsPathScope) (string, uint32, bool, error) {
	key, err := registry.OpenKey(scope.Root, scope.Path, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return "", registry.SZ, false, nil
		}
		return "", registry.SZ, false, err
	}
	defer key.Close()
	value, valueType, err := key.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return "", registry.SZ, false, nil
	}
	return value, valueType, err == nil, err
}

func writeWindowsPath(scope windowsPathScope, value string, valueType uint32) error {
	key, err := registry.OpenKey(scope.Root, scope.Path, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil && scope.Root == registry.CURRENT_USER {
		key, _, err = registry.CreateKey(scope.Root, scope.Path, registry.SET_VALUE|registry.QUERY_VALUE)
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if valueType == registry.EXPAND_SZ {
		return key.SetExpandStringValue("Path", value)
	}
	return key.SetStringValue("Path", value)
}

func restoreWindowsPath(scope windowsPathScope, value string, valueType uint32, existed bool) error {
	if !existed && strings.TrimSpace(value) == "" {
		key, err := registry.OpenKey(scope.Root, scope.Path, registry.SET_VALUE)
		if err == registry.ErrNotExist {
			return nil
		}
		if err != nil {
			return err
		}
		defer key.Close()
		err = key.DeleteValue("Path")
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	return writeWindowsPath(scope, value, valueType)
}

func cleanupFailedWindowsPathAttempt(scope windowsPathScope, before string, valueType uint32, existed bool, directory string) map[string]any {
	after, _, _, err := readWindowsPath(scope)
	if err != nil {
		return map[string]any{"verified": false, "error": sanitizeRuntimeError(err)}
	}
	if after == before || !windowsPathContains(after, directory) {
		return map[string]any{"verified": true, "changed": false}
	}
	reconciled := removeWindowsPathEntry(after, directory)
	err = restoreWindowsPath(scope, reconciled, valueType, existed || strings.TrimSpace(reconciled) != "")
	return map[string]any{"verified": err == nil, "changed": true, "error": sanitizeRuntimeError(err)}
}

func windowsPathContains(value, directory string) bool {
	target := strings.TrimRight(strings.ToLower(filepath.Clean(os.ExpandEnv(directory))), `\`)
	for _, entry := range filepath.SplitList(value) {
		candidate := strings.TrimRight(strings.ToLower(filepath.Clean(os.ExpandEnv(strings.Trim(entry, ` "`)))), `\`)
		if candidate != "" && candidate == target {
			return true
		}
	}
	return false
}

func appendWindowsPath(value, directory string) string {
	if windowsPathContains(value, directory) {
		return value
	}
	if strings.TrimSpace(value) == "" {
		return directory
	}
	return strings.TrimRight(value, ";") + ";" + directory
}

func removeWindowsPathEntry(value, directory string) string {
	entries := []string{}
	for _, entry := range filepath.SplitList(value) {
		if strings.TrimSpace(entry) != "" && !windowsPathContains(entry, directory) {
			entries = append(entries, entry)
		}
	}
	return strings.Join(entries, ";")
}
