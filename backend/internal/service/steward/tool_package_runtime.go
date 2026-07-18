package steward

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

type packageRuntimeTool struct {
	service  *Service
	manifest ToolPackageManifest
}

func newPackageRuntimeTool(service *Service, manifest ToolPackageManifest) RuntimeTool {
	return &packageRuntimeTool{service: service, manifest: manifest}
}

func (t *packageRuntimeTool) Spec() domain.StewardToolSpec { return t.manifest.runtimeSpec() }

func (t *packageRuntimeTool) ChangeTransactionEnabled() bool {
	return t.manifest.Transaction.Mode == "automatic"
}

func (t *packageRuntimeTool) SnapshotChange(ctx context.Context, input map[string]any) (RuntimeChangeSnapshot, error) {
	if !t.ChangeTransactionEnabled() {
		return RuntimeChangeSnapshot{}, nil
	}
	result, err := t.executeTransactionPhase(ctx, t.manifest.Transaction.SnapshotEntrypoint, map[string]any{"arguments": input})
	if err != nil {
		return RuntimeChangeSnapshot{}, err
	}
	return RuntimeChangeSnapshot{Summary: "tool package captured its mutation pre-state", State: result.Output}, nil
}

func (t *packageRuntimeTool) VerifyChange(ctx context.Context, input map[string]any, snapshot RuntimeChangeSnapshot, result RuntimeToolResult) error {
	if !t.ChangeTransactionEnabled() {
		return nil
	}
	verification, err := t.executeTransactionPhase(ctx, t.manifest.Transaction.VerificationEntrypoint, map[string]any{
		"arguments": input, "snapshot": snapshot.State, "result": result.Output,
	})
	if err != nil {
		return err
	}
	verified, _ := verification.Output["verified"].(bool)
	if !verified {
		return fmt.Errorf("transaction verifier did not return verified=true")
	}
	return nil
}

func (t *packageRuntimeTool) RollbackChange(ctx context.Context, input map[string]any, snapshot RuntimeChangeSnapshot, result RuntimeToolResult, cause error) (RuntimeToolResult, error) {
	if !t.ChangeTransactionEnabled() {
		return RuntimeToolResult{}, nil
	}
	rollback, err := t.executeTransactionPhase(ctx, t.manifest.Transaction.RollbackEntrypoint, map[string]any{
		"arguments": input, "snapshot": snapshot.State, "result": result.Output,
		"failure": map[string]any{"summary": sanitizeRuntimeError(cause), "diagnosis": diagnoseRuntimeFailure(cause)},
	})
	if err != nil {
		return rollback, err
	}
	rolledBack, _ := rollback.Output["rolled_back"].(bool)
	if !rolledBack {
		return rollback, fmt.Errorf("transaction rollback did not return rolled_back=true")
	}
	return rollback, nil
}

func (t *packageRuntimeTool) executeTransactionPhase(ctx context.Context, entrypoint string, input map[string]any) (RuntimeToolResult, error) {
	phase := t.manifest
	phase.Entrypoint = entrypoint
	phase.InputSchema = map[string]any{"type": "object"}
	phase.OutputSchema = map[string]any{"type": "object"}
	phase.Transaction = ToolTransactionContract{}
	if phase.ExecutionTarget == toolTargetSession && runtime.GOOS == "windows" {
		return t.service.executeSessionTool(ctx, phase, input)
	}
	if restrictedWindowsMainService() && brokerSystemToolRequired(phase) {
		return t.executeBrokerSystemTool(ctx, phase, input)
	}
	packageDir := t.service.toolPackageDir(t.manifest.Name, t.manifest.Version)
	return executeToolPackageProcess(ctx, phase, packageDir, input, t.service.agentIDValue())
}

func (t *packageRuntimeTool) Validate(input map[string]any) error {
	return validateToolInputSchema(t.manifest.InputSchema, input)
}

func (t *packageRuntimeTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	if t.manifest.Runtime == toolRuntimeComposite {
		return t.executeComposite(ctx, input)
	}
	if t.manifest.ExecutionTarget == toolTargetSession && runtime.GOOS == "windows" {
		return t.service.executeSessionTool(ctx, t.manifest, input)
	}
	if restrictedWindowsMainService() && brokerSystemToolRequired(t.manifest) {
		result, err := t.executeBrokerSystemTool(ctx, t.manifest, input)
		t.recordUsage(ctx, err)
		return result, err
	}
	result, err := t.executeScript(ctx, input)
	t.recordUsage(ctx, err)
	return result, err
}

func restrictedWindowsMainService() bool {
	return runtime.GOOS == "windows" && strings.EqualFold(strings.TrimSpace(os.Getenv("STEWARD_RESTRICTED_SERVICE")), "true")
}

func restrictedSystemToolError(name string) error {
	return fmt.Errorf("system-target tool %s cannot execute inside the restricted main service; provision and invoke the fixed Broker capability tool:%s", name, strings.ToLower(strings.TrimSpace(name)))
}

func brokerSystemToolRequired(manifest ToolPackageManifest) bool {
	return manifest.ExecutionTarget == toolTargetSystem || strings.HasPrefix(strings.ToLower(strings.TrimSpace(manifest.Name)), "registry.")
}

func (t *packageRuntimeTool) executeBrokerSystemTool(ctx context.Context, manifest ToolPackageManifest, input map[string]any) (RuntimeToolResult, error) {
	if t.service == nil || !t.service.runtimeR3 || t.service.privilegeBroker == nil {
		return RuntimeToolResult{}, restrictedSystemToolError(manifest.Name)
	}
	if t.service.privilegeBrokerError != nil {
		return RuntimeToolResult{}, t.service.privilegeBrokerError
	}
	status, err := t.service.privilegeBroker.Status(ctx)
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("query Broker before system tool %s: %w", manifest.Name, err)
	}
	invocationID := runtimeInvocationIDFromContext(ctx)
	capability := "tool:" + strings.ToLower(strings.TrimSpace(manifest.Name))
	response, err := t.service.privilegeBroker.ExecuteTool(ctx, privilegebroker.ToolAuthorization{
		Capability: capability, Subject: "system-tool:" + t.service.agentIDValue(),
		InvocationID: invocationID, Arguments: input, ControlGeneration: status.Generation,
	})
	if err != nil {
		return RuntimeToolResult{}, err
	}
	hostResponse, err := decodeToolHostResponse([]byte(response.Stdout))
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("decode Broker system tool %s response: %w", manifest.Name, err)
	}
	if !hostResponse.OK {
		return RuntimeToolResult{}, fmt.Errorf("Broker system tool %s failed: %s", manifest.Name, defaultString(hostResponse.Error, "unknown tool error"))
	}
	if err := validateToolOutputSchema(manifest.OutputSchema, hostResponse.Output); err != nil {
		return RuntimeToolResult{}, fmt.Errorf("Broker system tool %s output contract: %w", manifest.Name, err)
	}
	evidence := make([]RuntimeEvidence, 0, len(hostResponse.Evidence)+1)
	for _, item := range hostResponse.Evidence {
		evidence = append(evidence, RuntimeEvidence{Kind: defaultString(item.Kind, "system_tool"), Summary: item.Summary, Payload: item.Payload})
	}
	evidence = append(evidence, RuntimeEvidence{Kind: "privilege_broker_receipt", Summary: "Broker executed a schema-bound system tool", Payload: map[string]any{
		"tool": manifest.Name, "capability": capability, "receipt": response.Receipt,
	}})
	return RuntimeToolResult{Output: hostResponse.Output, Evidence: evidence}, nil
}

func (t *packageRuntimeTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if err := validateToolOutputSchema(t.manifest.OutputSchema, output); err != nil {
		return err
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (t *packageRuntimeTool) executeScript(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	packageDir := t.service.toolPackageDir(t.manifest.Name, t.manifest.Version)
	return executeToolPackageProcess(ctx, t.manifest, packageDir, input, t.service.agentIDValue())
}

// ExecuteCompanionToolPackage runs one already-published package inside the
// logged-in user session. The companion performs the same protocol and output
// validation as the system host; only the Windows session is different.
func ExecuteCompanionToolPackage(ctx context.Context, manifest ToolPackageManifest, packageDir string, input map[string]any) (RuntimeToolResult, error) {
	normalized, err := normalizeToolPackageManifest(manifest)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	if normalized.ExecutionTarget != toolTargetSession && normalized.ExecutionTarget != toolTargetAuto {
		return RuntimeToolResult{}, fmt.Errorf("tool %s is not declared for session execution", normalized.Name)
	}
	if err := validateToolInputSchema(normalized.InputSchema, input); err != nil {
		return RuntimeToolResult{}, err
	}
	for _, file := range normalized.Files {
		path := filepath.Join(packageDir, filepath.FromSlash(file.Path))
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, []byte(file.Content)) {
			return RuntimeToolResult{}, fmt.Errorf("published tool package content does not match manifest: %s", file.Path)
		}
	}
	return executeToolPackageProcess(ctx, normalized, filepath.Clean(packageDir), input, "interactive-session")
}

// PrepareCompanionToolPackage materializes the signed-in user's private copy
// from the immutable manifest. The Companion must not read the main service's
// ProgramData tool tree because that would require broad cross-identity ACLs.
func PrepareCompanionToolPackage(manifest ToolPackageManifest, root string) (string, error) {
	normalized, err := normalizeToolPackageManifest(manifest)
	if err != nil {
		return "", err
	}
	if normalized.ExecutionTarget != toolTargetSession && normalized.ExecutionTarget != toolTargetAuto {
		return "", fmt.Errorf("tool %s is not declared for session execution", normalized.Name)
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("companion tool root must be absolute")
	}
	dir := filepath.Join(root, normalized.Name, normalized.Version, toolPackageDigest(normalized))
	if err := writeToolPackageFiles(dir, normalized.Files); err != nil {
		return "", err
	}
	return dir, nil
}

func executeToolPackageProcess(ctx context.Context, manifest ToolPackageManifest, packageDir string, input map[string]any, agentID string) (RuntimeToolResult, error) {
	entrypoint := filepath.Join(packageDir, filepath.FromSlash(manifest.Entrypoint))
	if info, err := os.Stat(entrypoint); err != nil || info.IsDir() {
		return RuntimeToolResult{}, fmt.Errorf("tool entrypoint is unavailable: %s", entrypoint)
	}
	command, args, err := scriptToolCommand(manifest, packageDir, entrypoint)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	invocationID := runtimeInvocationIDFromContext(ctx)
	toolRoot := filepath.Dir(filepath.Dir(packageDir))
	runDir := filepath.Join(filepath.Dir(toolRoot), "tool-runs", invocationID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return RuntimeToolResult{}, fmt.Errorf("create isolated tool run directory: %w", err)
	}
	envelope := map[string]any{
		"protocol": "steward-tool/1", "invocation_id": invocationID,
		"arguments": input, "context": map[string]any{
			"device_id": agentID, "known_folders": runtimeKnownFolders(), "working_directory": runDir,
		},
	}
	payload, _ := json.Marshal(envelope)
	payload = append(payload, '\n')
	process := exec.Command(command, args...)
	process.Dir = runDir
	process.Env = append(sanitizedRuntimeEnvironment(),
		"STEWARD_TOOL_NAME="+manifest.Name,
		"STEWARD_TOOL_VERSION="+manifest.Version,
		"STEWARD_TOOL_PROTOCOL=steward-tool/1",
	)
	if manifest.Runtime == toolRuntimePowerShell {
		moduleDir := filepath.Join(packageDir, "modules")
		process.Env = append(process.Env, "PSModulePath="+moduleDir+string(os.PathListSeparator)+os.Getenv("PSModulePath"))
	}
	process.Stdin = bytes.NewReader(payload)
	stdout := &runtimeLimitedBuffer{limit: manifest.OutputLimitBytes}
	stderr := &runtimeLimitedBuffer{limit: min(manifest.OutputLimitBytes, runtimeCommandOutputLimit)}
	process.Stdout, process.Stderr = stdout, stderr
	if err := runRuntimeCommand(ctx, process); err != nil {
		exitCode := -1
		if process.ProcessState != nil {
			exitCode = process.ProcessState.ExitCode()
		}
		return RuntimeToolResult{}, fmt.Errorf("tool %s exited with code %d: %s", manifest.Name, exitCode, truncateAdvisorText(stderr.String(), 4000))
	}
	response, err := decodeToolHostResponse([]byte(stdout.String()))
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("decode tool %s response: %w", manifest.Name, err)
	}
	if !response.OK {
		return RuntimeToolResult{}, fmt.Errorf("tool %s failed: %s", manifest.Name, defaultString(response.Error, "unknown tool error"))
	}
	if err := validateToolOutputSchema(manifest.OutputSchema, response.Output); err != nil {
		return RuntimeToolResult{}, fmt.Errorf("tool %s output contract: %w", manifest.Name, err)
	}
	evidence := make([]RuntimeEvidence, 0, len(response.Evidence)+1)
	for _, item := range response.Evidence {
		evidence = append(evidence, RuntimeEvidence{Kind: defaultString(item.Kind, "tool_output"), Summary: item.Summary, Payload: item.Payload})
	}
	evidence = append(evidence, RuntimeEvidence{Kind: "tool_package", Summary: manifest.Name + " completed", Payload: map[string]any{
		"tool": manifest.Name, "version": manifest.Version, "runtime": manifest.Runtime,
		"invocation_id": invocationID, "run_directory": runDir,
		"stdout_truncated": stdout.truncated, "stderr_truncated": stderr.truncated,
	}})
	return RuntimeToolResult{Output: response.Output, Evidence: evidence}, nil
}

func scriptToolCommand(manifest ToolPackageManifest, packageDir, entrypoint string) (string, []string, error) {
	switch manifest.Runtime {
	case toolRuntimePowerShell:
		path, err := resolvePowerShellExecutable()
		arguments := []string{"-NoLogo", "-NoProfile", "-NonInteractive"}
		if runtime.GOOS == "windows" {
			arguments = append(arguments, "-STA")
		}
		arguments = append(arguments, "-ExecutionPolicy", "Bypass", "-File", entrypoint)
		return path, arguments, err
	case toolRuntimePython:
		python := filepath.Join(packageDir, ".venv", "Scripts", "python.exe")
		if runtime.GOOS != "windows" {
			python = filepath.Join(packageDir, ".venv", "bin", "python")
		}
		if _, err := os.Stat(python); err != nil {
			python, err = exec.LookPath("python")
			if err != nil {
				return "", nil, err
			}
		}
		return python, []string{entrypoint}, nil
	case toolRuntimeNode:
		node, err := exec.LookPath("node")
		return node, []string{entrypoint}, err
	default:
		return "", nil, fmt.Errorf("runtime %s is not script-backed", manifest.Runtime)
	}
}

func resolvePowerShellExecutable() (string, error) {
	if path, err := exec.LookPath("pwsh"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		root := strings.TrimSpace(os.Getenv("SYSTEMROOT"))
		if root == "" {
			root = strings.TrimSpace(os.Getenv("WINDIR"))
		}
		if root == "" {
			root = `C:\Windows`
		}
		path := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return exec.LookPath("powershell")
}

type toolHostEvidence struct {
	Kind    string         `json:"kind"`
	Summary string         `json:"summary"`
	Payload map[string]any `json:"payload"`
}

type toolHostResponse struct {
	OK       bool               `json:"ok"`
	Output   map[string]any     `json:"output"`
	Error    string             `json:"error"`
	Evidence []toolHostEvidence `json:"evidence"`
}

func decodeToolHostResponse(payload []byte) (toolHostResponse, error) {
	var response toolHostResponse
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	scanner.Buffer(make([]byte, 1024), 64<<20)
	last := []byte(nil)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			last = append(last[:0], line...)
		}
	}
	if err := scanner.Err(); err != nil {
		return response, err
	}
	if len(last) == 0 {
		return response, fmt.Errorf("invalid steward-tool/1 response: stdout is empty; the final stdout line must be a JSON object such as {\"ok\":true,\"output\":{},\"evidence\":[]}")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(last, &fields); err != nil {
		return response, fmt.Errorf("invalid steward-tool/1 response JSON: %w", err)
	}
	if fields == nil {
		return response, fmt.Errorf("invalid steward-tool/1 response: final stdout line must be a JSON object")
	}
	okJSON, exists := fields["ok"]
	if !exists {
		return response, fmt.Errorf("invalid steward-tool/1 response: missing required boolean field \"ok\"; wrap business results as {\"ok\":true,\"output\":{...},\"evidence\":[]}")
	}
	if err := json.Unmarshal(okJSON, &response.OK); err != nil {
		return response, fmt.Errorf("invalid steward-tool/1 response: field \"ok\" must be boolean")
	}
	if errorJSON, exists := fields["error"]; exists {
		if err := json.Unmarshal(errorJSON, &response.Error); err != nil {
			return response, fmt.Errorf("invalid steward-tool/1 response: field \"error\" must be a string")
		}
	}
	if response.OK {
		outputJSON, exists := fields["output"]
		if !exists || bytes.Equal(bytes.TrimSpace(outputJSON), []byte("null")) {
			return response, fmt.Errorf("invalid steward-tool/1 response: successful responses require an object field \"output\"")
		}
		if err := json.Unmarshal(outputJSON, &response.Output); err != nil || response.Output == nil {
			return response, fmt.Errorf("invalid steward-tool/1 response: field \"output\" must be an object when ok=true")
		}
	} else {
		if strings.TrimSpace(response.Error) == "" {
			return response, fmt.Errorf("invalid steward-tool/1 response: failed responses require a non-empty string field \"error\"")
		}
		response.Output = map[string]any{}
	}
	evidenceJSON, exists := fields["evidence"]
	if !exists {
		return response, fmt.Errorf("invalid steward-tool/1 response: missing required array field \"evidence\"; use an empty array when there is no tool-supplied evidence")
	}
	if err := json.Unmarshal(evidenceJSON, &response.Evidence); err != nil || response.Evidence == nil {
		return response, fmt.Errorf("invalid steward-tool/1 response: field \"evidence\" must be an array")
	}
	return response, nil
}

func validateToolInputSchema(schema, input map[string]any) error {
	if input == nil {
		input = map[string]any{}
	}
	required, _ := schema["required"].([]any)
	if strings, ok := schema["required"].([]string); ok {
		for _, name := range strings {
			if _, exists := input[name]; !exists {
				return fmt.Errorf("missing required argument %s", name)
			}
		}
	} else {
		for _, value := range required {
			name := fmt.Sprint(value)
			if _, exists := input[name]; !exists {
				return fmt.Errorf("missing required argument %s", name)
			}
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	if additional, exists := schema["additionalProperties"].(bool); exists && !additional {
		for name := range input {
			if _, known := properties[name]; !known {
				return fmt.Errorf("unknown argument %s", name)
			}
		}
	}
	for name, value := range input {
		raw, exists := properties[name]
		if !exists {
			continue
		}
		property, _ := raw.(map[string]any)
		if expected, _ := property["type"].(string); expected != "" && !matchesJSONType(expected, value) {
			return fmt.Errorf("argument %s must be %s", name, expected)
		}
	}
	return nil
}

func validateToolOutputSchema(schema, output map[string]any) error {
	if output == nil {
		return fmt.Errorf("output must be an object")
	}
	return validateToolInputSchema(schema, output)
}

func matchesJSONType(expected string, value any) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "number", "integer":
		switch value.(type) {
		case float64, float32, int, int32, int64, uint, uint32, uint64:
			return true
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "null":
		return value == nil
	}
	return false
}

func (t *packageRuntimeTool) executeComposite(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	outputs := map[string]any{}
	allEvidence := []RuntimeEvidence{}
	pending := append([]ToolCompositeStep(nil), t.manifest.CompositeSteps...)
	for len(pending) > 0 {
		progress := false
		remaining := pending[:0]
		for _, step := range pending {
			ready := true
			for _, dependency := range step.DependsOn {
				if _, exists := outputs[dependency]; !exists {
					ready = false
					break
				}
			}
			if !ready {
				remaining = append(remaining, step)
				continue
			}
			tool, found := t.service.runtimeTools.get(step.ToolName)
			if !found || step.ToolName == t.manifest.Name {
				return RuntimeToolResult{}, fmt.Errorf("composite step %s references unavailable tool %s", step.Key, step.ToolName)
			}
			arguments := resolveCompositeValue(step.Arguments, input, outputs).(map[string]any)
			if validator, ok := tool.(RuntimeToolValidator); ok {
				if err := validator.Validate(arguments); err != nil {
					return RuntimeToolResult{}, fmt.Errorf("composite step %s: %w", step.Key, err)
				}
			}
			result, err := tool.Execute(ctx, arguments)
			if err != nil {
				return RuntimeToolResult{}, fmt.Errorf("composite step %s: %w", step.Key, err)
			}
			outputs[step.Key] = result.Output
			allEvidence = append(allEvidence, result.Evidence...)
			progress = true
		}
		if !progress {
			return RuntimeToolResult{}, fmt.Errorf("composite dependency graph contains a cycle or missing dependency")
		}
		pending = append([]ToolCompositeStep(nil), remaining...)
	}
	t.recordUsage(ctx, nil)
	return RuntimeToolResult{Output: map[string]any{"steps": outputs}, Evidence: allEvidence}, nil
}

func resolveCompositeValue(value any, input, outputs map[string]any) any {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "${") && strings.HasSuffix(typed, "}") {
			path := strings.TrimSuffix(strings.TrimPrefix(typed, "${"), "}")
			parts := strings.Split(path, ".")
			var current any
			if len(parts) > 1 && parts[0] == "input" {
				current = input
				parts = parts[1:]
			} else if len(parts) > 2 && parts[0] == "steps" {
				current = outputs
				parts = parts[1:]
			}
			for _, part := range parts {
				object, ok := current.(map[string]any)
				if !ok {
					return nil
				}
				current = object[part]
			}
			return current
		}
		return typed
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = resolveCompositeValue(item, input, outputs)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = resolveCompositeValue(item, input, outputs)
		}
		return result
	default:
		return typed
	}
}

func (t *packageRuntimeTool) recordUsage(ctx context.Context, cause error) {
	if t == nil || t.service == nil || t.service.db == nil || t.service.db.Pool == nil {
		return
	}
	status, summary := "healthy", "last invocation succeeded"
	if cause != nil {
		status, summary = "degraded", truncateAdvisorText(cause.Error(), 500)
	}
	_, _ = t.service.db.Pool.Exec(ctx, `update steward_tools set invocation_count=invocation_count+1,last_used_at=now(),health_status=$2,health_summary=$3,updated_at=now() where name=$1`, t.manifest.Name, status, summary)
}

func (s *Service) toolRootDir() string {
	if configured := strings.TrimSpace(os.Getenv("STEWARD_TOOL_ROOT")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(s.storageDir, "tools")
}

func (s *Service) toolPackageDir(name, version string) string {
	return filepath.Join(s.toolRootDir(), name, version)
}

func toolPackageDigest(manifest ToolPackageManifest) string {
	clone := manifest
	sort.Slice(clone.Files, func(i, j int) bool { return clone.Files[i].Path < clone.Files[j].Path })
	raw, _ := json.Marshal(clone)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func runtimeInvocationIDFromContext(ctx context.Context) string {
	if id, _ := ctx.Value(runtimeInvocationContextKey{}).(string); strings.TrimSpace(id) != "" {
		return id
	}
	return fmt.Sprintf("tool-%d", time.Now().UnixNano())
}

func copyLimited(dst io.Writer, src io.Reader, limit int64) error {
	_, err := io.Copy(dst, io.LimitReader(src, limit))
	return err
}
