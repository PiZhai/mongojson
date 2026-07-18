package steward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func (s *Service) CreateToolPackage(ctx context.Context, input CreateToolPackageInput) (domain.StewardTool, error) {
	s.toolCatalogMu.Lock()
	defer s.toolCatalogMu.Unlock()
	manifest, err := normalizeToolPackageManifest(input.Manifest)
	if err != nil {
		return domain.StewardTool{}, err
	}
	if manifest.Runtime == toolRuntimeBuiltin {
		return domain.StewardTool{}, fmt.Errorf("dynamic packages cannot use builtin runtime")
	}
	if len(manifest.Tests) == 0 {
		return domain.StewardTool{}, fmt.Errorf("generated tools require at least one executable test")
	}
	var existingOrigin string
	err = s.db.Pool.QueryRow(ctx, `select origin from steward_tools where name=$1`, manifest.Name).Scan(&existingOrigin)
	if err == nil && existingOrigin == "builtin" {
		return domain.StewardTool{}, fmt.Errorf("compiled tool %s cannot be replaced", manifest.Name)
	}
	if err != nil && !toolNotFound(err) {
		return domain.StewardTool{}, err
	}
	var versionExists bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_tool_versions where tool_name=$1 and version=$2)`, manifest.Name, manifest.Version).Scan(&versionExists); err != nil {
		return domain.StewardTool{}, err
	}
	if versionExists {
		return domain.StewardTool{}, fmt.Errorf("tool version %s@%s is immutable and already exists", manifest.Name, manifest.Version)
	}

	root := s.toolRootDir()
	if err := os.MkdirAll(root, 0o750); err != nil {
		return domain.StewardTool{}, fmt.Errorf("create tool root: %w", err)
	}
	staging := filepath.Join(root, ".staging-"+uuid.NewString())
	if err := writeToolPackageFiles(staging, manifest.Files); err != nil {
		return domain.StewardTool{}, err
	}
	digest := toolPackageDigest(manifest)
	sbom := buildToolSBOM(manifest, digest)
	provenance := buildToolProvenance(manifest, digest, input)
	manifestJSON, _ := json.Marshal(manifest)
	sbomJSON, _ := json.Marshal(sbom)
	provenanceJSON, _ := json.Marshal(provenance)
	now := time.Now().UTC()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return domain.StewardTool{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		insert into steward_tools (
			name,title,description,origin,enabled,active_version,execution_target,health_status,health_summary,
			catalog_generation,created_by_episode_id,created_by_turn_id,created_by_model,created_at,updated_at
		) values ($1,$2,$3,$4,false,'',$5,'validating','package validation in progress',1,nullif($6,'')::uuid,nullif($7,'')::uuid,$8,$9,$9)
		on conflict (name) do update set title=excluded.title,description=excluded.description,execution_target=excluded.execution_target,
			created_by_episode_id=coalesce(steward_tools.created_by_episode_id,excluded.created_by_episode_id),
			created_by_turn_id=coalesce(steward_tools.created_by_turn_id,excluded.created_by_turn_id),
			created_by_model=case when steward_tools.created_by_model='' then excluded.created_by_model else steward_tools.created_by_model end,
			health_status='validating',health_summary='package validation in progress',updated_at=excluded.updated_at
	`, manifest.Name, manifest.Title, manifest.Description, manifest.Origin, manifest.ExecutionTarget,
		input.CreatedByEpisodeID, input.CreatedByTurnID, input.CreatedByModel, now)
	if err != nil {
		return domain.StewardTool{}, err
	}
	_, err = tx.Exec(ctx, `
		insert into steward_tool_versions (
			tool_name,version,runtime,status,manifest,package_path,content_sha256,sbom,provenance,created_at
		) values ($1,$2,$3,'validating',$4::jsonb,$5,$6,$7::jsonb,$8::jsonb,$9)
	`, manifest.Name, manifest.Version, manifest.Runtime, string(manifestJSON), staging, digest, string(sbomJSON), string(provenanceJSON), now)
	if err != nil {
		return domain.StewardTool{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardTool{}, err
	}

	finalDir := s.toolPackageDir(manifest.Name, manifest.Version)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o750); err != nil {
		return s.failToolPackage(ctx, manifest, staging, err)
	}
	if err := os.Rename(staging, finalDir); err != nil {
		return s.failToolPackage(ctx, manifest, staging, fmt.Errorf("publish immutable package directory: %w", err))
	}
	_, _ = s.db.Pool.Exec(ctx, `update steward_tool_versions set package_path=$3 where tool_name=$1 and version=$2`, manifest.Name, manifest.Version, finalDir)

	if err := s.prepareToolDependencies(ctx, manifest, finalDir); err != nil {
		_ = s.rollbackToolDependencies(ctx, manifest.Name, manifest.Version)
		return s.failToolPackage(ctx, manifest, finalDir, err)
	}
	if err := s.runToolPackageTests(ctx, manifest); err != nil {
		_ = s.rollbackToolDependencies(ctx, manifest.Name, manifest.Version)
		return s.failToolPackage(ctx, manifest, finalDir, err)
	}
	autoEnable := input.AutoEnable == nil || *input.AutoEnable
	status := "validated"
	if autoEnable {
		status = "enabled"
	}
	validatedAt := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `
		update steward_tool_versions set status=$3,validation_summary='all package tests passed',validated_at=$4 where tool_name=$1 and version=$2;
	`, manifest.Name, manifest.Version, status, validatedAt)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `update steward_tools set enabled=$3,active_version=case when $3 then $2 else active_version end,
			health_status=case when $3 then 'healthy' else 'disabled' end,
			health_summary='all package tests passed',catalog_generation=catalog_generation+1,updated_at=$4 where name=$1`,
			manifest.Name, manifest.Version, autoEnable, validatedAt)
	}
	if err != nil {
		return domain.StewardTool{}, err
	}
	if autoEnable {
		s.runtimeTools.register(newPackageRuntimeTool(s, manifest))
	}
	_ = s.recordToolCatalogEvent(ctx, manifest.Name, manifest.Version, "publish", defaultString(input.CreatedByModel, "model"), "tool package validated and published", map[string]any{"auto_enabled": autoEnable, "sha256": digest})
	return s.GetTool(ctx, manifest.Name)
}

func writeToolPackageFiles(root string, files []ToolPackageFile) error {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create package staging directory: %w", err)
	}
	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(file.Content), 0o640); err != nil {
			return fmt.Errorf("write package file %s: %w", file.Path, err)
		}
	}
	return nil
}

func buildToolSBOM(manifest ToolPackageManifest, digest string) map[string]any {
	components := make([]map[string]any, 0, len(manifest.Dependencies)+1)
	components = append(components, map[string]any{"type": "application", "name": manifest.Name, "version": manifest.Version, "hashes": []map[string]string{{"alg": "SHA-256", "content": digest}}})
	for _, dependency := range manifest.Dependencies {
		component := map[string]any{"type": "library", "group": dependency.Ecosystem, "name": dependency.Name, "version": dependency.Version}
		if dependency.SHA256 != "" {
			component["hashes"] = []map[string]string{{"alg": "SHA-256", "content": dependency.SHA256}}
		}
		components = append(components, component)
	}
	return map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1, "components": components}
}

func buildToolProvenance(manifest ToolPackageManifest, digest string, input CreateToolPackageInput) map[string]any {
	return map[string]any{
		"_type": "https://in-toto.io/Statement/v1", "predicateType": "https://slsa.dev/provenance/v1",
		"subject": []map[string]any{{"name": manifest.Name + "@" + manifest.Version, "digest": map[string]string{"sha256": digest}}},
		"predicate": map[string]any{
			"buildDefinition": map[string]any{"buildType": "https://mongojson.local/steward/tool-package/v1", "externalParameters": map[string]any{
				"runtime": manifest.Runtime, "execution_target": manifest.ExecutionTarget, "dependency_strategy": manifest.DependencyStrategy,
			}},
			"runDetails": map[string]any{"builder": map[string]string{"id": "mongojson-steward-toolsmith"}, "metadata": map[string]any{
				"invocationId": input.CreatedByTurnID, "startedOn": time.Now().UTC(), "model": input.CreatedByModel,
			}},
		},
	}
}

func (s *Service) failToolPackage(ctx context.Context, manifest ToolPackageManifest, packagePath string, cause error) (domain.StewardTool, error) {
	summary := truncateAdvisorText(cause.Error(), 2000)
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_tool_versions set status='failed',validation_summary=$3 where tool_name=$1 and version=$2;
		update steward_tools set health_status='failed',health_summary=$3,updated_at=now() where name=$1
	`, manifest.Name, manifest.Version, summary)
	_ = s.recordToolCatalogEvent(ctx, manifest.Name, manifest.Version, "validation_failed", "toolsmith", summary, map[string]any{"package_path": packagePath})
	tool, _ := s.GetTool(ctx, manifest.Name)
	return tool, cause
}

type dependencyOperation struct {
	id            string
	executable    string
	args          []string
	rollbackExec  string
	rollbackArgs  []string
	display       string
	rollbackLabel string
}

func (s *Service) prepareToolDependencies(ctx context.Context, manifest ToolPackageManifest, packageDir string) error {
	if len(manifest.Dependencies) == 0 {
		return nil
	}
	if manifest.Runtime == toolRuntimePython && manifest.DependencyStrategy.Selected == "isolated" {
		if err := ensurePythonVenv(ctx, packageDir); err != nil {
			return err
		}
		if hasToolDependency(manifest.Dependencies, "pip") {
			python := filepath.Join(packageDir, ".venv", "Scripts", "python.exe")
			if runtime.GOOS != "windows" {
				python = filepath.Join(packageDir, ".venv", "bin", "python")
			}
			return s.runLockedDependencyInstall(ctx, manifest, packageDir, "pip", python,
				[]string{"-m", "pip", "install", "--disable-pip-version-check", "--require-hashes", "-r", filepath.Join(packageDir, "requirements.lock")},
				python+" -m pip install --require-hashes -r requirements.lock")
		}
	}
	if manifest.Runtime == toolRuntimeNode && manifest.DependencyStrategy.Selected == "isolated" && hasToolDependency(manifest.Dependencies, "npm") {
		return s.runLockedDependencyInstall(ctx, manifest, packageDir, "npm", "npm", []string{"ci", "--ignore-scripts"}, "npm ci --ignore-scripts")
	}
	for _, dependency := range manifest.Dependencies {
		op, err := dependencyInstallOperation(manifest, dependency, packageDir)
		if err != nil {
			return err
		}
		started := time.Now().UTC()
		changeID := uuid.NewString()
		_, err = s.db.Pool.Exec(ctx, `
			insert into steward_tool_dependency_changes (
				id,tool_name,tool_version,ecosystem,package_name,requested_version,install_scope,status,
				install_command,rollback_command,created_at
			) values ($1,$2,$3,$4,$5,$6,$7,'installing',$8,$9,$10)
		`, changeID, manifest.Name, manifest.Version, dependency.Ecosystem, dependency.Name, dependency.Version,
			dependency.Scope, op.display, op.rollbackLabel, started)
		if err != nil {
			return err
		}
		stdout, stderr, runErr := runToolSetupCommand(ctx, op.executable, op.args, packageDir)
		status := "installed"
		if runErr != nil {
			status = "failed"
		}
		evidence, _ := json.Marshal(map[string]any{"stdout": truncateAdvisorText(stdout, 8000), "stderr": truncateAdvisorText(stderr, 8000)})
		_, _ = s.db.Pool.Exec(ctx, `update steward_tool_dependency_changes set status=$2,resolved_version=$3,evidence=$4::jsonb,completed_at=now() where id=$1`, changeID, status, dependency.Version, string(evidence))
		if runErr != nil {
			return fmt.Errorf("install dependency %s:%s: %w", dependency.Ecosystem, dependency.Name, runErr)
		}
	}
	return nil
}

func (s *Service) runLockedDependencyInstall(ctx context.Context, manifest ToolPackageManifest, packageDir, ecosystem, executable string, args []string, display string) error {
	started := time.Now().UTC()
	stdout, stderr, runErr := runToolSetupCommand(ctx, executable, args, packageDir)
	status := "installed"
	if runErr != nil {
		status = "failed"
	}
	evidence, _ := json.Marshal(map[string]any{"stdout": truncateAdvisorText(stdout, 8000), "stderr": truncateAdvisorText(stderr, 8000), "lockfile": map[string]string{"pip": "requirements.lock", "npm": "package-lock.json"}[ecosystem]})
	for _, dependency := range manifest.Dependencies {
		if dependency.Ecosystem != ecosystem {
			continue
		}
		_, _ = s.db.Pool.Exec(ctx, `insert into steward_tool_dependency_changes (
			id,tool_name,tool_version,ecosystem,package_name,requested_version,resolved_version,install_scope,status,
			install_command,rollback_command,evidence,created_at,completed_at
		) values ($1,$2,$3,$4,$5,$6,$6,'isolated',$7,$8,$9,$10::jsonb,$11,now())`,
			uuid.NewString(), manifest.Name, manifest.Version, ecosystem, dependency.Name, dependency.Version, status, display,
			"delete package-local dependency directory", string(evidence), started)
	}
	if runErr != nil {
		return fmt.Errorf("restore locked %s dependencies: %w", ecosystem, runErr)
	}
	return nil
}

func ensurePythonVenv(ctx context.Context, packageDir string) error {
	venv := filepath.Join(packageDir, ".venv")
	if _, err := os.Stat(venv); err == nil {
		return nil
	}
	python := "py"
	args := []string{"-m", "venv", venv}
	if runtime.GOOS != "windows" {
		python, args = "python3", []string{"-m", "venv", venv}
	}
	_, stderr, err := runToolSetupCommand(ctx, python, args, packageDir)
	if err != nil {
		return fmt.Errorf("create isolated Python environment: %s: %w", stderr, err)
	}
	return nil
}

func dependencyInstallOperation(manifest ToolPackageManifest, dependency ToolDependency, packageDir string) (dependencyOperation, error) {
	scope := defaultString(dependency.Scope, manifest.DependencyStrategy.Selected)
	exact := dependency.Name + "==" + dependency.Version
	switch dependency.Ecosystem {
	case "pip":
		python := "py"
		if scope == "isolated" {
			python = filepath.Join(packageDir, ".venv", "Scripts", "python.exe")
			if runtime.GOOS != "windows" {
				python = filepath.Join(packageDir, ".venv", "bin", "python")
			}
		}
		return dependencyOperation{executable: python, args: []string{"-m", "pip", "install", "--disable-pip-version-check", exact}, rollbackExec: python, rollbackArgs: []string{"-m", "pip", "uninstall", "-y", dependency.Name}, display: python + " -m pip install " + exact, rollbackLabel: python + " -m pip uninstall -y " + dependency.Name}, nil
	case "pipx":
		return dependencyOperation{executable: "pipx", args: []string{"install", dependency.Name + "==" + dependency.Version}, rollbackExec: "pipx", rollbackArgs: []string{"uninstall", dependency.Name}, display: "pipx install " + dependency.Name + "==" + dependency.Version, rollbackLabel: "pipx uninstall " + dependency.Name}, nil
	case "npm":
		if scope == "global" || scope == "shared" {
			return dependencyOperation{executable: "npm", args: []string{"install", "-g", dependency.Name + "@" + dependency.Version}, rollbackExec: "npm", rollbackArgs: []string{"uninstall", "-g", dependency.Name}, display: "npm install -g " + dependency.Name + "@" + dependency.Version, rollbackLabel: "npm uninstall -g " + dependency.Name}, nil
		}
		return dependencyOperation{executable: "npm", args: []string{"install", "--save-exact", dependency.Name + "@" + dependency.Version}, rollbackExec: "npm", rollbackArgs: []string{"uninstall", dependency.Name}, display: "npm install --save-exact " + dependency.Name + "@" + dependency.Version, rollbackLabel: "npm uninstall " + dependency.Name}, nil
	case "powershell":
		pwsh := "pwsh"
		if _, err := exec.LookPath(pwsh); err != nil {
			pwsh = "powershell"
		}
		if scope == "global" || scope == "shared" {
			script := fmt.Sprintf("Install-PSResource -Name %s -Version %s -Scope AllUsers -AuthenticodeCheck -TrustRepository -AcceptLicense", quotePowerShell(dependency.Name), quotePowerShell(dependency.Version))
			rollback := fmt.Sprintf("Uninstall-PSResource -Name %s -Version %s -Scope AllUsers -ErrorAction SilentlyContinue", quotePowerShell(dependency.Name), quotePowerShell(dependency.Version))
			return dependencyOperation{executable: pwsh, args: []string{"-NoProfile", "-NonInteractive", "-Command", script}, rollbackExec: pwsh, rollbackArgs: []string{"-NoProfile", "-NonInteractive", "-Command", rollback}, display: script, rollbackLabel: rollback}, nil
		}
		moduleDir := filepath.Join(packageDir, "modules")
		script := fmt.Sprintf("Save-PSResource -Name %s -Version %s -Path %s -AuthenticodeCheck -TrustRepository -AcceptLicense", quotePowerShell(dependency.Name), quotePowerShell(dependency.Version), quotePowerShell(moduleDir))
		return dependencyOperation{executable: pwsh, args: []string{"-NoProfile", "-NonInteractive", "-Command", script}, display: script, rollbackLabel: "remove package-local modules directory"}, nil
	case "winget":
		args := []string{"install", "--id", dependency.Name, "--exact", "--version", dependency.Version, "--source", defaultString(dependency.Source, "winget"), "--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity"}
		if scope == "global" || scope == "shared" {
			args = append(args, "--scope", "machine")
		}
		return dependencyOperation{executable: "winget", args: args, rollbackExec: "winget", rollbackArgs: []string{"uninstall", "--id", dependency.Name, "--exact", "--disable-interactivity"}, display: "winget " + strings.Join(args, " "), rollbackLabel: "winget uninstall --id " + dependency.Name + " --exact"}, nil
	default:
		return dependencyOperation{}, fmt.Errorf("unsupported dependency ecosystem %s", dependency.Ecosystem)
	}
}

func runToolSetupCommand(ctx context.Context, command string, args []string, dir string) (string, string, error) {
	resolved, err := exec.LookPath(command)
	if err != nil && filepath.IsAbs(command) {
		resolved, err = command, nil
	}
	if err != nil {
		return "", "", err
	}
	process := exec.Command(resolved, args...)
	process.Dir = dir
	process.Env = sanitizedRuntimeEnvironment()
	stdout, stderr := &runtimeLimitedBuffer{limit: 4 << 20}, &runtimeLimitedBuffer{limit: 4 << 20}
	process.Stdout, process.Stderr = stdout, stderr
	err = runRuntimeCommand(ctx, process)
	return stdout.String(), stderr.String(), err
}

func quotePowerShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func (s *Service) rollbackToolDependencies(ctx context.Context, name, version string) error {
	dependencies, err := s.listToolDependencies(ctx, name, version)
	if err != nil {
		return err
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].CreatedAt.After(dependencies[j].CreatedAt) })
	var failures []string
	for _, dependency := range dependencies {
		if dependency.Status != "installed" || dependency.Preexisting {
			continue
		}
		var manifest ToolPackageManifest
		raw, _ := s.toolManifestJSON(ctx, name, version)
		_ = json.Unmarshal(raw, &manifest)
		var declared ToolDependency
		for _, item := range manifest.Dependencies {
			if item.Ecosystem == dependency.Ecosystem && item.Name == dependency.PackageName {
				declared = item
				break
			}
		}
		op, opErr := dependencyInstallOperation(manifest, declared, s.toolPackageDir(name, version))
		if opErr != nil || op.rollbackExec == "" {
			continue
		}
		_, stderr, runErr := runToolSetupCommand(ctx, op.rollbackExec, op.rollbackArgs, s.toolPackageDir(name, version))
		if runErr != nil {
			failures = append(failures, dependency.PackageName+": "+stderr)
			continue
		}
		_, _ = s.db.Pool.Exec(ctx, `update steward_tool_dependency_changes set status='rolled_back',completed_at=now() where id=$1`, dependency.ID)
	}
	if len(failures) > 0 {
		return fmt.Errorf("dependency rollback failures: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) runToolPackageTests(ctx context.Context, manifest ToolPackageManifest) error {
	tool := newPackageRuntimeTool(s, manifest)
	for _, test := range manifest.Tests {
		started := time.Now().UTC()
		result, runErr := tool.Execute(ctx, test.Input)
		if runErr == nil {
			runErr = tool.Verify(ctx, test.Input, result.Output, test.Expected)
		}
		status, errorSummary := "passed", ""
		if runErr != nil {
			status, errorSummary = "failed", truncateAdvisorText(runErr.Error(), 2000)
		}
		inputJSON, _ := json.Marshal(test.Input)
		outputJSON, _ := json.Marshal(result.Output)
		evidence := make([]map[string]any, 0, len(result.Evidence))
		for _, item := range result.Evidence {
			evidence = append(evidence, map[string]any{"kind": item.Kind, "summary": item.Summary, "payload": item.Payload})
		}
		evidenceJSON, _ := json.Marshal(evidence)
		completed := time.Now().UTC()
		_, _ = s.db.Pool.Exec(ctx, `
			insert into steward_tool_test_runs (id,tool_name,tool_version,test_name,status,input,output,error_summary,evidence,started_at,completed_at)
			values ($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8,$9::jsonb,$10,$11)
		`, newToolTestRunID(), manifest.Name, manifest.Version, defaultString(test.Name, "smoke"), status,
			string(inputJSON), string(outputJSON), errorSummary, string(evidenceJSON), started, completed)
		if runErr != nil {
			return fmt.Errorf("tool test %s failed: %w", defaultString(test.Name, "smoke"), runErr)
		}
	}
	return nil
}

func (s *Service) runStoredToolTests(ctx context.Context, name, version string) error {
	if version == "" {
		return fmt.Errorf("tool version is required")
	}
	raw, err := s.toolManifestJSON(ctx, name, version)
	if err != nil {
		return err
	}
	var manifest ToolPackageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.Runtime == toolRuntimeBuiltin {
		return nil
	}
	return s.runToolPackageTests(ctx, manifest)
}

func toolVersionExists(ctx context.Context, query func(context.Context, string, ...any) pgx.Row, name, version string) bool {
	var exists bool
	_ = query(ctx, `select exists(select 1 from steward_tool_versions where tool_name=$1 and version=$2)`, name, version).Scan(&exists)
	return exists
}

func bytesString(buffer *bytes.Buffer) string { return buffer.String() }
