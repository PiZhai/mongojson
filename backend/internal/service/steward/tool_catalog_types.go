package steward

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mongojson/backend/internal/domain"
)

const (
	toolRuntimeBuiltin    = "builtin"
	toolRuntimePowerShell = "powershell"
	toolRuntimePython     = "python"
	toolRuntimeNode       = "node"
	toolRuntimeComposite  = "composite"

	toolTargetSystem  = "system"
	toolTargetSession = "session"
	toolTargetAuto    = "auto"
)

var (
	toolPackageNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$`)
	toolPackageVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
)

type ToolDependencyStrategy struct {
	Requested       string                      `json:"requested"`
	Selected        string                      `json:"selected"`
	Alternatives    []ToolDependencyAlternative `json:"alternatives,omitempty"`
	SelectionReason string                      `json:"selection_reason"`
}

type ToolDependencyAlternative struct {
	Strategy       string `json:"strategy"`
	RejectedReason string `json:"rejected_reason"`
}

type ToolDependency struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Scope     string `json:"scope,omitempty"`
	Source    string `json:"source,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type ToolPackageFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ToolPackageTest struct {
	Name     string         `json:"name"`
	Input    map[string]any `json:"input"`
	Expected map[string]any `json:"expected,omitempty"`
}

// ToolTransactionContract makes generated mutating tools participate in the
// same durable prepare/verify/rollback protocol as built-in Windows tools.
// Each entrypoint uses steward-tool/1 and returns its state in output.
type ToolTransactionContract struct {
	Mode                   string `json:"mode,omitempty"`
	SnapshotEntrypoint     string `json:"snapshot_entrypoint,omitempty"`
	VerificationEntrypoint string `json:"verification_entrypoint,omitempty"`
	RollbackEntrypoint     string `json:"rollback_entrypoint,omitempty"`
}

type ToolCompositeStep struct {
	Key       string         `json:"key"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments"`
	DependsOn []string       `json:"depends_on,omitempty"`
}

type ToolPackageManifest struct {
	Name               string                  `json:"name"`
	Version            string                  `json:"version"`
	Title              string                  `json:"title"`
	Description        string                  `json:"description"`
	Tags               []string                `json:"tags,omitempty"`
	Origin             string                  `json:"origin,omitempty"`
	Runtime            string                  `json:"runtime"`
	ExecutionTarget    string                  `json:"execution_target"`
	Entrypoint         string                  `json:"entrypoint,omitempty"`
	InputSchema        map[string]any          `json:"input_schema"`
	OutputSchema       map[string]any          `json:"output_schema"`
	Files              []ToolPackageFile       `json:"files,omitempty"`
	Dependencies       []ToolDependency        `json:"dependencies,omitempty"`
	DependencyStrategy ToolDependencyStrategy  `json:"dependency_strategy"`
	Tests              []ToolPackageTest       `json:"tests,omitempty"`
	CompositeSteps     []ToolCompositeStep     `json:"composite_steps,omitempty"`
	DefaultTimeoutSec  int                     `json:"default_timeout_seconds,omitempty"`
	OutputLimitBytes   int                     `json:"output_limit_bytes,omitempty"`
	SupportsCancel     bool                    `json:"supports_cancel"`
	IdempotencyMode    string                  `json:"idempotency_mode,omitempty"`
	SideEffect         string                  `json:"side_effect,omitempty"`
	Transaction        ToolTransactionContract `json:"transaction,omitempty"`
}

type CreateToolPackageInput struct {
	Manifest           ToolPackageManifest `json:"manifest"`
	CreatedByEpisodeID string              `json:"created_by_episode_id,omitempty"`
	CreatedByTurnID    string              `json:"created_by_turn_id,omitempty"`
	CreatedByModel     string              `json:"created_by_model,omitempty"`
	AutoEnable         *bool               `json:"auto_enable,omitempty"`
}

type ToolCatalogDecisionInput struct {
	Decision string `json:"decision"`
	Version  string `json:"version,omitempty"`
}

func normalizeToolPackageManifest(manifest ToolPackageManifest) (ToolPackageManifest, error) {
	manifest.Name = strings.ToLower(strings.TrimSpace(manifest.Name))
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Title = strings.TrimSpace(manifest.Title)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Runtime = strings.ToLower(strings.TrimSpace(manifest.Runtime))
	manifest.ExecutionTarget = strings.ToLower(defaultString(strings.TrimSpace(manifest.ExecutionTarget), toolTargetAuto))
	manifest.Entrypoint = filepath.ToSlash(strings.TrimSpace(manifest.Entrypoint))
	manifest.Origin = defaultString(strings.ToLower(strings.TrimSpace(manifest.Origin)), "model")
	manifest.IdempotencyMode = defaultString(strings.ToLower(strings.TrimSpace(manifest.IdempotencyMode)), RuntimeIdempotencyNonIdempotent)
	manifest.SideEffect = defaultString(strings.ToLower(strings.TrimSpace(manifest.SideEffect)), RuntimeSideEffectProcess)
	manifest.Transaction.Mode = defaultString(strings.ToLower(strings.TrimSpace(manifest.Transaction.Mode)), "none")
	manifest.Transaction.SnapshotEntrypoint = filepath.ToSlash(strings.TrimSpace(manifest.Transaction.SnapshotEntrypoint))
	manifest.Transaction.VerificationEntrypoint = filepath.ToSlash(strings.TrimSpace(manifest.Transaction.VerificationEntrypoint))
	manifest.Transaction.RollbackEntrypoint = filepath.ToSlash(strings.TrimSpace(manifest.Transaction.RollbackEntrypoint))
	if manifest.DefaultTimeoutSec <= 0 {
		manifest.DefaultTimeoutSec = 60
	}
	if manifest.OutputLimitBytes <= 0 {
		manifest.OutputLimitBytes = runtimeCommandOutputLimit
	}
	if manifest.InputSchema == nil {
		manifest.InputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if manifest.OutputSchema == nil {
		manifest.OutputSchema = map[string]any{"type": "object"}
	}
	manifest.DependencyStrategy.Requested = defaultString(strings.ToLower(strings.TrimSpace(manifest.DependencyStrategy.Requested)), "auto")
	manifest.DependencyStrategy.Selected = defaultString(strings.ToLower(strings.TrimSpace(manifest.DependencyStrategy.Selected)), recommendedDependencyStrategy(manifest))
	manifest.DependencyStrategy.SelectionReason = strings.TrimSpace(manifest.DependencyStrategy.SelectionReason)

	if !toolPackageNamePattern.MatchString(manifest.Name) {
		return manifest, fmt.Errorf("tool name must contain a namespace and use lowercase letters, digits, and underscores")
	}
	if !toolPackageVersionPattern.MatchString(manifest.Version) {
		return manifest, fmt.Errorf("tool version must be semantic version x.y.z")
	}
	if manifest.Title == "" || manifest.Description == "" {
		return manifest, fmt.Errorf("tool title and description are required")
	}
	switch manifest.Runtime {
	case toolRuntimeBuiltin, toolRuntimePowerShell, toolRuntimePython, toolRuntimeNode, toolRuntimeComposite:
	default:
		return manifest, fmt.Errorf("unsupported tool runtime %q", manifest.Runtime)
	}
	switch manifest.ExecutionTarget {
	case toolTargetSystem, toolTargetSession, toolTargetAuto:
	default:
		return manifest, fmt.Errorf("unsupported execution_target %q", manifest.ExecutionTarget)
	}
	if manifest.DefaultTimeoutSec > 24*60*60 {
		return manifest, fmt.Errorf("default_timeout_seconds must not exceed 86400")
	}
	if manifest.OutputLimitBytes > 64<<20 {
		return manifest, fmt.Errorf("output_limit_bytes must not exceed 64 MiB")
	}
	if schemaType, _ := manifest.InputSchema["type"].(string); schemaType != "object" {
		return manifest, fmt.Errorf("input_schema.type must be object")
	}
	if _, err := json.Marshal(manifest.OutputSchema); err != nil {
		return manifest, fmt.Errorf("invalid output_schema: %w", err)
	}

	fileNames := map[string]bool{}
	for index := range manifest.Files {
		name := filepath.ToSlash(strings.TrimSpace(manifest.Files[index].Path))
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "../") || filepath.IsAbs(name) {
			return manifest, fmt.Errorf("tool file path %q must stay inside the package", name)
		}
		manifest.Files[index].Path = name
		if fileNames[strings.ToLower(name)] {
			return manifest, fmt.Errorf("duplicate tool file %q", name)
		}
		fileNames[strings.ToLower(name)] = true
	}
	if manifest.Runtime != toolRuntimeBuiltin && manifest.Runtime != toolRuntimeComposite {
		if manifest.Entrypoint == "" || !fileNames[strings.ToLower(manifest.Entrypoint)] {
			return manifest, fmt.Errorf("entrypoint must reference one package file")
		}
	}
	if manifest.Runtime == toolRuntimeComposite && len(manifest.CompositeSteps) == 0 {
		return manifest, fmt.Errorf("composite tools require composite_steps")
	}
	switch manifest.Transaction.Mode {
	case "none":
	case "automatic":
		if manifest.Runtime == toolRuntimeBuiltin || manifest.Runtime == toolRuntimeComposite {
			return manifest, fmt.Errorf("automatic transaction entrypoints require a script runtime")
		}
		for label, entrypoint := range map[string]string{
			"snapshot_entrypoint":     manifest.Transaction.SnapshotEntrypoint,
			"verification_entrypoint": manifest.Transaction.VerificationEntrypoint,
			"rollback_entrypoint":     manifest.Transaction.RollbackEntrypoint,
		} {
			if entrypoint == "" || !fileNames[strings.ToLower(entrypoint)] {
				return manifest, fmt.Errorf("transaction.%s must reference one package file", label)
			}
		}
		if manifest.SideEffect == RuntimeSideEffectNone {
			return manifest, fmt.Errorf("automatic transactions require a mutating side_effect")
		}
	default:
		return manifest, fmt.Errorf("unsupported transaction mode %q", manifest.Transaction.Mode)
	}
	for index := range manifest.Dependencies {
		dependency := &manifest.Dependencies[index]
		dependency.Ecosystem = strings.ToLower(strings.TrimSpace(dependency.Ecosystem))
		dependency.Name = strings.TrimSpace(dependency.Name)
		dependency.Version = strings.TrimSpace(dependency.Version)
		dependency.Scope = defaultString(strings.ToLower(strings.TrimSpace(dependency.Scope)), manifest.DependencyStrategy.Selected)
		if dependency.Name == "" || dependency.Version == "" {
			return manifest, fmt.Errorf("every dependency requires an exact name and version")
		}
		switch dependency.Ecosystem {
		case "pip", "pipx", "npm", "powershell", "winget":
		default:
			return manifest, fmt.Errorf("unsupported dependency ecosystem %q", dependency.Ecosystem)
		}
	}
	if manifest.Runtime == toolRuntimePython && manifest.DependencyStrategy.Selected == "isolated" && hasToolDependency(manifest.Dependencies, "pip") {
		lock, ok := toolPackageFileContent(manifest.Files, "requirements.lock")
		if !ok || !strings.Contains(lock, "--hash=sha256:") {
			return manifest, fmt.Errorf("isolated Python tools require requirements.lock with sha256 hashes for every resolved dependency")
		}
	}
	if manifest.Runtime == toolRuntimeNode && manifest.DependencyStrategy.Selected == "isolated" && hasToolDependency(manifest.Dependencies, "npm") {
		if _, ok := toolPackageFileContent(manifest.Files, "package.json"); !ok {
			return manifest, fmt.Errorf("isolated Node tools require package.json")
		}
		if _, ok := toolPackageFileContent(manifest.Files, "package-lock.json"); !ok {
			return manifest, fmt.Errorf("isolated Node tools require package-lock.json and are restored with npm ci")
		}
	}
	if len(manifest.Dependencies) > 0 && manifest.DependencyStrategy.SelectionReason == "" {
		return manifest, fmt.Errorf("dependency_strategy.selection_reason is required when dependencies are declared")
	}
	if manifest.DependencyStrategy.Selected == "global" && len(manifest.DependencyStrategy.Alternatives) == 0 {
		return manifest, fmt.Errorf("global dependency strategy must document rejected alternatives")
	}
	for index := range manifest.Tags {
		manifest.Tags[index] = strings.ToLower(strings.TrimSpace(manifest.Tags[index]))
	}
	sort.Strings(manifest.Tags)
	return manifest, nil
}

func hasToolDependency(items []ToolDependency, ecosystem string) bool {
	for _, item := range items {
		if item.Ecosystem == ecosystem {
			return true
		}
	}
	return false
}

func toolPackageFileContent(files []ToolPackageFile, name string) (string, bool) {
	for _, file := range files {
		if strings.EqualFold(filepath.ToSlash(file.Path), name) {
			return file.Content, true
		}
	}
	return "", false
}

func recommendedDependencyStrategy(manifest ToolPackageManifest) string {
	if len(manifest.Dependencies) == 0 {
		return "none"
	}
	return "isolated"
}

func (manifest ToolPackageManifest) runtimeSpec() domain.StewardToolSpec {
	approval := RuntimeApprovalNever
	if manifest.SideEffect != "" && manifest.SideEffect != RuntimeSideEffectNone {
		// This is legacy Runtime R2 registration metadata. Device-owner mode
		// deliberately evaluates it as an unconditional allow; keeping the
		// marker preserves compatibility with the existing schema validator.
		approval = RuntimeApprovalAlways
	}
	return domain.StewardToolSpec{
		Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
		InputSchema: manifest.InputSchema, OutputSchema: manifest.OutputSchema,
		SideEffect: manifest.SideEffect, ApprovalMode: approval,
		IdempotencyMode: manifest.IdempotencyMode, SupportsCancel: manifest.SupportsCancel,
		DefaultTimeoutSec: manifest.DefaultTimeoutSec,
		PermissionLevel:   PermissionA0, RiskLevel: "low",
	}
}
