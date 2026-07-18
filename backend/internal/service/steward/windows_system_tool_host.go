package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type WindowsSystemToolCatalogItem struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	InputSchema    map[string]any `json:"input_schema"`
	OutputSchema   map[string]any `json:"output_schema"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

type WindowsSystemToolHostRequest struct {
	Protocol     string            `json:"protocol"`
	InvocationID string            `json:"invocation_id"`
	Capability   string            `json:"capability"`
	Arguments    map[string]any    `json:"arguments"`
	InputSHA256  string            `json:"input_sha256"`
	Credentials  map[string]string `json:"credentials,omitempty"`
}

// WindowsSystemToolCatalog is compiled into the System Tool Host and used to
// generate Broker policy. Only tools explicitly declared target=system are
// included; session tools remain in the logged-in Companion.
func WindowsSystemToolCatalog() []WindowsSystemToolCatalogItem {
	items := make([]WindowsSystemToolCatalogItem, 0)
	for _, definition := range windowsFoundationToolDefinitions() {
		if definition.target != toolTargetSystem && !strings.HasPrefix(definition.name, "registry.") {
			continue
		}
		items = append(items, WindowsSystemToolCatalogItem{
			Name: definition.name, Description: definition.description,
			InputSchema: map[string]any{"type": "object", "properties": definition.properties,
				"required": definition.required, "additionalProperties": false},
			OutputSchema: map[string]any{"type": "object"}, TimeoutSeconds: 120,
		})
	}
	return items
}

// ExecuteWindowsSystemToolHost runs a single compiled-catalog tool. The
// PowerShell adapter is materialized from the binary's embedded source into a
// fresh private directory, so policy never trusts a mutable script on disk.
func ExecuteWindowsSystemToolHost(ctx context.Context, request WindowsSystemToolHostRequest) (RuntimeToolResult, error) {
	if runtime.GOOS != "windows" {
		return RuntimeToolResult{}, fmt.Errorf("Windows System Tool Host is only available on Windows")
	}
	if request.Protocol != "steward-system-tool/1" {
		return RuntimeToolResult{}, fmt.Errorf("unsupported system tool protocol")
	}
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(request.Capability)), "tool:")
	var selected *WindowsSystemToolCatalogItem
	for _, item := range WindowsSystemToolCatalog() {
		if item.Name == name {
			copy := item
			selected = &copy
			break
		}
	}
	if selected == nil {
		return RuntimeToolResult{}, fmt.Errorf("system tool %q is not compiled into this host", name)
	}
	if err := validateToolInputSchema(selected.InputSchema, request.Arguments); err != nil {
		return RuntimeToolResult{}, fmt.Errorf("system tool input: %w", err)
	}
	payload, _ := json.Marshal(request.Arguments)
	digest := sha256.Sum256(payload)
	if request.InputSHA256 != hex.EncodeToString(digest[:]) {
		return RuntimeToolResult{}, fmt.Errorf("system tool input digest mismatch")
	}
	root, err := os.MkdirTemp("", "mongojson-steward-system-tool-")
	if err != nil {
		return RuntimeToolResult{}, err
	}
	defer os.RemoveAll(root)
	packageDir := filepath.Join(root, "packages", name, windowsFoundationToolVersion)
	manifest := ToolPackageManifest{
		Name: name, Version: windowsFoundationToolVersion, Title: name,
		Description: selected.Description, Origin: "platform", Runtime: toolRuntimePowerShell,
		ExecutionTarget: toolTargetSystem, Entrypoint: "tool.ps1", InputSchema: selected.InputSchema,
		OutputSchema: selected.OutputSchema, Files: []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
		DefaultTimeoutSec: selected.TimeoutSeconds, OutputLimitBytes: 8 << 20, SupportsCancel: true,
	}
	if err := writeToolPackageFiles(packageDir, manifest.Files); err != nil {
		return RuntimeToolResult{}, err
	}
	ctx = withRuntimeInvocationID(ctx, request.InvocationID)
	return executeToolPackageProcess(ctx, manifest, packageDir, request.Arguments, "broker-system-host")
}
