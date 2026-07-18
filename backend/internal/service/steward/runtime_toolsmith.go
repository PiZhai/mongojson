package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

const toolHostProtocolGuide = `Script tools use NDJSON protocol steward-tool/1. Read one request object from stdin: {"protocol":"steward-tool/1","invocation_id":"...","arguments":{},"context":{...}}. The final non-empty stdout line MUST be exactly one response envelope. Success: {"ok":true,"output":{...},"evidence":[]}. Failure: {"ok":false,"error":"actionable message","evidence":[]}. Do not print a bare business result. Tests pass the manifest input arguments directly; never nest test input under "arguments", because the Tool Host creates that envelope. In PowerShell use a variable such as $toolArguments, not the automatic $args variable, and treat null request.arguments as an empty object.`

type runtimeToolsmithTool struct {
	service *Service
	action  string
}

func newRuntimeToolsmithTool(service *Service, action string) RuntimeTool {
	return &runtimeToolsmithTool{service: service, action: action}
}

func (t *runtimeToolsmithTool) Spec() domain.StewardToolSpec {
	description := map[string]string{
		"tool.search":   "Search the complete persistent tool catalog by capability, name, or description before creating a new tool.",
		"tool.describe": "Load the full manifest, versions, dependency strategy, tests, and health of one tool.",
		"tool.create":   "Create, dependency-prepare, test, automatically publish, and hot-load an immutable PowerShell, Python, Node.js, or composite tool package.",
		"tool.update":   "Publish a new immutable version of an existing generated tool, test it, and atomically activate it.",
		"tool.test":     "Run the stored contract tests for one tool version.",
		"tool.enable":   "Enable a validated tool version and hot-load it.",
		"tool.disable":  "Disable a tool for future model turns without deleting history.",
		"tool.rollback": "Atomically switch a generated tool to a previous validated version.",
		"tool.delete":   "Retire a generated tool while retaining its catalog and execution evidence.",
	}[t.action]
	if t.action == "tool.create" || t.action == "tool.update" {
		description += " Follow Steward Tool Authoring Standard 1.1: search and compose first; prefer native APIs, standard libraries, then package-local locked dependencies; global installation is allowed only when it is the best governed choice and rejected isolated alternatives are recorded. Python isolated packages require a hash-locked requirements.lock; Node isolated packages require package-lock.json and npm ci. Mutating script tools should declare transaction.mode=automatic with package-local snapshot, verification, and rollback entrypoints; verification must reread real system state and rollback must preserve unrelated concurrent changes. " + toolHostProtocolGuide
	}
	properties := map[string]any{}
	required := []string{}
	switch t.action {
	case "tool.search":
		properties = map[string]any{"query": map[string]any{"type": "string"}, "origin": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}}
	case "tool.describe":
		properties = map[string]any{"name": map[string]any{"type": "string"}}
		required = []string{"name"}
	case "tool.create", "tool.update":
		properties = map[string]any{
			"manifest":    toolPackageManifestToolSchema(),
			"auto_enable": map[string]any{"type": "boolean"},
		}
		required = []string{"manifest"}
	default:
		properties = map[string]any{"name": map[string]any{"type": "string"}, "version": map[string]any{"type": "string"}}
		required = []string{"name"}
	}
	sideEffect, approval, idempotency := RuntimeSideEffectNone, RuntimeApprovalNever, RuntimeIdempotencyInherent
	timeoutSeconds := 30
	if t.action != "tool.search" && t.action != "tool.describe" {
		// ApprovalMode is retained as legacy registration metadata. Owner mode
		// bypasses the old approval policy and executes Toolsmith calls directly.
		sideEffect, approval, idempotency = RuntimeSideEffectWrite, RuntimeApprovalAlways, RuntimeIdempotencyNonIdempotent
	}
	switch t.action {
	case "tool.create", "tool.update", "tool.test":
		// Dependency preparation and real contract tests may install packages or
		// launch external runtimes, so these operations retain the long budget.
		timeoutSeconds = 1800
	case "tool.enable", "tool.disable", "tool.rollback", "tool.delete":
		timeoutSeconds = 120
	}
	return domain.StewardToolSpec{
		Name: t.action, Version: "1.0.0", Description: description,
		InputSchema:  map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
		OutputSchema: map[string]any{"type": "object"}, PermissionLevel: PermissionA0, RiskLevel: "low",
		SideEffect: sideEffect, ApprovalMode: approval, IdempotencyMode: idempotency,
		SupportsCancel: true, DefaultTimeoutSec: timeoutSeconds,
	}
}

func toolPackageManifestToolSchema() map[string]any {
	objectSchema := func(description string) map[string]any {
		return map[string]any{"type": "object", "description": description, "additionalProperties": true}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"description": "Immutable generated tool package. " + toolHostProtocolGuide,
		"properties": map[string]any{
			"name":             map[string]any{"type": "string", "description": "Namespaced lowercase tool name such as windows.startup_list."},
			"version":          map[string]any{"type": "string", "description": "New semantic version x.y.z. Published and failed versions are immutable; tool.update must use a version that has never existed."},
			"title":            map[string]any{"type": "string"},
			"description":      map[string]any{"type": "string"},
			"tags":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"runtime":          map[string]any{"type": "string", "enum": []string{toolRuntimePowerShell, toolRuntimePython, toolRuntimeNode, toolRuntimeComposite}},
			"execution_target": map[string]any{"type": "string", "enum": []string{toolTargetSystem, toolTargetSession, toolTargetAuto}},
			"entrypoint":       map[string]any{"type": "string", "description": "Entrypoint file for script runtimes."},
			"input_schema":     objectSchema("JSON Schema for the business arguments found inside the Tool Host request's arguments field."),
			"output_schema":    objectSchema("JSON Schema for the business object placed inside the Tool Host response's output field."),
			"files": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string", "description": toolHostProtocolGuide},
				}, "required": []string{"path", "content"},
			}},
			"dependencies": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"ecosystem": map[string]any{"type": "string", "enum": []string{"pip", "pipx", "npm", "powershell", "winget"}},
					"name":      map[string]any{"type": "string"}, "version": map[string]any{"type": "string"},
					"scope": map[string]any{"type": "string"}, "source": map[string]any{"type": "string"}, "sha256": map[string]any{"type": "string"},
				}, "required": []string{"ecosystem", "name", "version"},
			}},
			"dependency_strategy": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"requested": map[string]any{"type": "string"}, "selected": map[string]any{"type": "string"}, "selection_reason": map[string]any{"type": "string"},
					"alternatives": map[string]any{"type": "array", "items": objectSchema("Rejected dependency strategy and reason.")},
				}, "required": []string{"requested", "selected", "selection_reason"},
			},
			"tests": map[string]any{"type": "array", "description": "At least one executable test is required.", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"name":     map[string]any{"type": "string"},
					"input":    objectSchema("Business arguments passed directly to the tool. Do not add an arguments wrapper."),
					"expected": objectSchema("Optional expected subset of the business output object."),
				}, "required": []string{"name", "input"},
			}},
			"composite_steps":         map[string]any{"type": "array", "items": objectSchema("Composite DAG step.")},
			"default_timeout_seconds": map[string]any{"type": "integer"},
			"output_limit_bytes":      map[string]any{"type": "integer"},
			"supports_cancel":         map[string]any{"type": "boolean"},
			"idempotency_mode":        map[string]any{"type": "string"},
			"side_effect":             map[string]any{"type": "string"},
			"transaction":             objectSchema("Use mode=automatic plus snapshot_entrypoint, verification_entrypoint, and rollback_entrypoint for mutating script tools; otherwise mode=none."),
		},
		"required": []string{"name", "version", "title", "description", "runtime", "execution_target", "input_schema", "output_schema", "dependency_strategy", "tests", "supports_cancel", "side_effect"},
	}
}

func (t *runtimeToolsmithTool) Validate(input map[string]any) error {
	return validateToolInputSchema(t.Spec().InputSchema, input)
}

func (t *runtimeToolsmithTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	var output map[string]any
	switch t.action {
	case "tool.search":
		items, err := t.service.ListTools(ctx, stringArgument(input, "query"), stringArgument(input, "origin"), stringArgument(input, "status"))
		if err != nil {
			return RuntimeToolResult{}, err
		}
		compact := make([]map[string]any, 0, len(items))
		for _, item := range items {
			compact = append(compact, map[string]any{"name": item.Name, "title": item.Title, "description": item.Description, "origin": item.Origin, "enabled": item.Enabled, "version": item.ActiveVersion, "health": item.HealthStatus, "execution_target": item.ExecutionTarget})
		}
		output = map[string]any{"tools": compact, "count": len(compact), "catalog_generation": t.service.runtimeTools.generationValue()}
	case "tool.describe":
		item, err := t.service.GetTool(ctx, stringArgument(input, "name"))
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output = structToMap(item)
	case "tool.create", "tool.update":
		raw, _ := json.Marshal(input["manifest"])
		var manifest ToolPackageManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return RuntimeToolResult{}, fmt.Errorf("decode tool manifest: %w", err)
		}
		var autoEnable *bool
		if value, ok := input["auto_enable"].(bool); ok {
			autoEnable = &value
		}
		item, err := t.service.CreateToolPackage(ctx, CreateToolPackageInput{Manifest: manifest, CreatedByModel: "agent-loop", AutoEnable: autoEnable})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output = structToMap(item)
	default:
		decision := strings.TrimPrefix(t.action, "tool.")
		item, err := t.service.DecideTool(ctx, stringArgument(input, "name"), ToolCatalogDecisionInput{Decision: decision, Version: stringArgument(input, "version")})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output = structToMap(item)
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "tool_catalog", Summary: t.action + " completed", Payload: map[string]any{"action": t.action}}}}, nil
}

func (*runtimeToolsmithTool) Verify(_ context.Context, _ map[string]any, output map[string]any, _ map[string]any) error {
	if output == nil {
		return fmt.Errorf("toolsmith output is missing")
	}
	return nil
}

func stringArgument(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

func structToMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}
