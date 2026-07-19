package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

func (s *Service) ensureToolCatalog(ctx context.Context, now time.Time) error {
	if s == nil || s.db == nil || s.db.Pool == nil || s.runtimeTools == nil {
		return nil
	}
	if err := s.migrateLegacyToolDefinitions(ctx, now); err != nil {
		return err
	}
	if err := s.reconcileInterruptedToolValidations(ctx); err != nil {
		return err
	}
	for _, spec := range s.runtimeTools.specs() {
		if registered, ok := s.runtimeTools.get(spec.Name); ok {
			if _, dynamic := registered.(*packageRuntimeTool); dynamic {
				continue
			}
		}
		manifest := ToolPackageManifest{
			Name: spec.Name, Version: spec.Version, Title: spec.Name, Description: spec.Description,
			Origin: "builtin", Runtime: toolRuntimeBuiltin, ExecutionTarget: toolTargetAuto,
			InputSchema: spec.InputSchema, OutputSchema: spec.OutputSchema,
			DefaultTimeoutSec: spec.DefaultTimeoutSec, SupportsCancel: spec.SupportsCancel,
			IdempotencyMode: spec.IdempotencyMode, SideEffect: spec.SideEffect,
			DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "compiled built-in tool"},
		}
		manifestJSON, _ := json.Marshal(manifest)
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_tools (
				name,title,description,origin,enabled,active_version,execution_target,
				health_status,health_summary,catalog_generation,created_at,updated_at
			) values ($1,$2,$3,'builtin',true,$4,$5,'healthy','compiled runtime tool',$6,$7,$7)
			on conflict (name) do update set
				title=excluded.title,description=excluded.description,
				active_version=case when steward_tools.origin='builtin' then excluded.active_version else steward_tools.active_version end,
				health_status=case when steward_tools.origin='builtin' then 'healthy' else steward_tools.health_status end,
				updated_at=excluded.updated_at
		`, spec.Name, spec.Name, spec.Description, spec.Version, toolTargetAuto, s.runtimeTools.generationValue(), now); err != nil {
			return fmt.Errorf("ensure tool catalog entry %s: %w", spec.Name, err)
		}
		if _, err := s.db.Pool.Exec(ctx, `
			insert into steward_tool_versions (
				tool_name,version,runtime,status,manifest,content_sha256,validation_summary,created_at,validated_at
			) values ($1,$2,'builtin','enabled',$3::jsonb,'','compiled runtime tool',$4,$4)
			on conflict (tool_name,version) do update set manifest=excluded.manifest,status='enabled',validated_at=excluded.validated_at
		`, spec.Name, spec.Version, string(manifestJSON), now); err != nil {
			return fmt.Errorf("ensure built-in tool version %s: %w", spec.Name, err)
		}
	}
	return s.reloadDynamicTools(ctx)
}

func (s *Service) reconcileInterruptedToolValidations(ctx context.Context) error {
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_tool_versions version set
			status='failed',
			validation_summary=coalesce((
				select nullif(test.error_summary,'') from steward_tool_test_runs test
				where test.tool_name=version.tool_name and test.tool_version=version.version and test.status='failed'
				order by test.started_at desc limit 1
			),nullif(version.validation_summary,'package validation in progress'),'tool validation was interrupted before publication')
		where version.status='validating'
	`); err != nil {
		return fmt.Errorf("reconcile interrupted tool versions: %w", err)
	}
	if _, err := s.db.Pool.Exec(ctx, `
		update steward_tools tool set
			health_status='failed',
			health_summary=coalesce((
				select nullif(version.validation_summary,'') from steward_tool_versions version
				where version.tool_name=tool.name and version.status='failed'
				order by version.created_at desc limit 1
			),'generated tool validation failed'),
			updated_at=now()
		where tool.origin='model' and tool.enabled=false and tool.active_version='' and tool.health_status<>'retired'
	`); err != nil {
		return fmt.Errorf("reconcile interrupted tool health: %w", err)
	}
	return nil
}

func (s *Service) migrateLegacyToolDefinitions(ctx context.Context, now time.Time) error {
	rows, err := s.db.Pool.Query(ctx, `select action,name,description,executable,arguments,working_directory,timeout_seconds from steward_tool_definitions order by action`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var action, title, description, executable, workingDirectory string
		var arguments []byte
		var timeout int
		if err := rows.Scan(&action, &title, &description, &executable, &arguments, &workingDirectory, &timeout); err != nil {
			return err
		}
		name := "legacy." + strings.NewReplacer(":", "_", "-", "_").Replace(strings.TrimPrefix(strings.ToLower(action), "tool:"))
		manifest := map[string]any{"name": name, "version": "1.0.0", "title": title, "description": description,
			"runtime": "legacy-executable", "execution_target": "system", "legacy": map[string]any{"action": action, "executable": executable, "arguments": json.RawMessage(arguments), "working_directory": workingDirectory, "timeout_seconds": timeout},
			"migration_note": "retained for audit only; steward_tool_definitions is not an R5 execution source"}
		raw, _ := json.Marshal(manifest)
		if _, err := s.db.Pool.Exec(ctx, `insert into steward_tools (name,title,description,origin,enabled,active_version,execution_target,health_status,health_summary,created_at,updated_at)
			values ($1,$2,$3,'legacy-executable',false,'1.0.0','system','retired','migrated for audit; not executable',$4,$4) on conflict (name) do nothing`, name, title, description, now); err != nil {
			return err
		}
		if _, err := s.db.Pool.Exec(ctx, `insert into steward_tool_versions (tool_name,version,runtime,status,manifest,validation_summary,created_at)
			values ($1,'1.0.0','legacy-executable','retired',$2::jsonb,'migrated for audit; not executable',$3) on conflict (tool_name,version) do nothing`, name, string(raw), now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) reloadDynamicTools(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `
		select t.name,v.manifest
		from steward_tools t
		join steward_tool_versions v on v.tool_name=t.name and v.version=t.active_version
		where t.enabled=true and t.origin<>'builtin' and v.status='enabled'
	`)
	if err != nil {
		return fmt.Errorf("load dynamic tools: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			return err
		}
		var manifest ToolPackageManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("decode dynamic tool %s: %w", name, err)
		}
		if manifest.Runtime == toolRuntimeBuiltin {
			continue
		}
		s.runtimeTools.register(newPackageRuntimeTool(s, manifest))
	}
	return rows.Err()
}

func (s *Service) ListTools(ctx context.Context, query, origin, status string) ([]domain.StewardTool, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	origin = strings.ToLower(strings.TrimSpace(origin))
	status = strings.ToLower(strings.TrimSpace(status))
	rows, err := s.db.Pool.Query(ctx, `
		select name,title,description,origin,enabled,active_version,execution_target,
		       health_status,health_summary,catalog_generation,coalesce(created_by_episode_id::text,''),
		       coalesce(created_by_turn_id::text,''),created_by_model,invocation_count,last_used_at,created_at,updated_at
		from steward_tools
		where ($1='' or lower(name) like '%'||$1||'%' or lower(title) like '%'||$1||'%' or lower(description) like '%'||$1||'%')
		  and ($2='' or origin=$2)
		  and ($3='' or health_status=$3 or ($3='enabled' and enabled=true) or ($3='disabled' and enabled=false))
		order by name
	`, query, origin, status)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardTool{}
	for rows.Next() {
		item, err := scanStewardTool(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// agentToolContext keeps every enabled tool visible in a compact catalog while
// only sending full JSON Schemas for common, Toolsmith, and explicitly hydrated
// tools. This keeps large Windows catalogs within model context limits without
// hiding capabilities from the agent.
func (s *Service) agentToolContext(ctx context.Context, episode *domain.StewardAgentEpisode) ([]domain.StewardToolSpec, []AgentToolCatalogEntry, error) {
	items, err := s.ListTools(ctx, "", "", "enabled")
	if err != nil {
		return nil, nil, err
	}
	specs, catalog, versions, hydratedNames := buildAgentToolContext(items, s.runtimeTools.specs(), episode)
	if episode != nil {
		episode.HydratedToolNames = hydratedNames
		episode.CurrentToolVersions = versions
		episode.CatalogGeneration = s.runtimeTools.generationValue()
		_, _ = s.db.Pool.Exec(ctx, `update steward_agent_episodes set hydrated_tool_names=$2::jsonb,catalog_generation=$3,current_tool_versions=$4::jsonb,updated_at=now() where id=$1`,
			episode.ID, encodeAgentJSON(hydratedNames, "[]"), episode.CatalogGeneration, encodeAgentJSON(versions, "{}"))
	}
	return specs, catalog, nil
}

func buildAgentToolContext(items []domain.StewardTool, allSpecs []domain.StewardToolSpec, episode *domain.StewardAgentEpisode) ([]domain.StewardToolSpec, []AgentToolCatalogEntry, map[string]string, []string) {
	hydrated := map[string]bool{}
	if episode != nil {
		for _, name := range episode.HydratedToolNames {
			hydrated[name] = true
		}
		for _, turn := range episode.Turns {
			for _, result := range turn.ToolResults {
				if result.ToolName != "tool.describe" && result.ToolName != "tool.create" && result.ToolName != "tool.update" {
					continue
				}
				if result.Error != "" {
					continue
				}
				if name, _ := result.Output["name"].(string); name != "" {
					hydrated[name] = true
				}
			}
		}
	}
	common := func(name string) bool {
		return strings.HasPrefix(name, "tool.") || strings.HasPrefix(name, "steward.") || name == "runtime.echo" ||
			name == "shell.exec" || name == "web.fetch_text" || name == "browser.open_url" ||
			name == "fs.exists" || name == "fs.stat" || name == "fs.list" || name == "fs.search" ||
			name == "fs.get_known_folders" || name == "fs.read_text" || name == "fs.create_directory" ||
			name == "fs.write_text" || name == "application.open" || name == "process.list" || name == "system.info"
	}
	byName := make(map[string]domain.StewardToolSpec, len(allSpecs))
	for _, spec := range allSpecs {
		byName[spec.Name] = spec
	}
	specs := make([]domain.StewardToolSpec, 0, 32)
	catalog := make([]AgentToolCatalogEntry, 0, len(items))
	versions := map[string]string{}
	hydratedNames := make([]string, 0, len(hydrated))
	for _, item := range items {
		catalog = append(catalog, AgentToolCatalogEntry{Name: item.Name, Description: item.Description, Version: item.ActiveVersion, ExecutionTarget: item.ExecutionTarget, HealthStatus: item.HealthStatus})
		versions[item.Name] = item.ActiveVersion
		spec, registered := byName[item.Name]
		if !registered {
			continue
		}
		if common(item.Name) || hydrated[item.Name] {
			specs = append(specs, spec)
		}
		if hydrated[item.Name] {
			hydratedNames = append(hydratedNames, item.Name)
		}
	}
	return specs, catalog, versions, hydratedNames
}

func (s *Service) GetTool(ctx context.Context, name string) (domain.StewardTool, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select name,title,description,origin,enabled,active_version,execution_target,
		       health_status,health_summary,catalog_generation,coalesce(created_by_episode_id::text,''),
		       coalesce(created_by_turn_id::text,''),created_by_model,invocation_count,last_used_at,created_at,updated_at
		from steward_tools where name=$1
	`, strings.TrimSpace(name))
	item, err := scanStewardTool(row)
	if err != nil {
		return item, err
	}
	item.Versions, err = s.ListToolVersions(ctx, item.Name)
	if err != nil {
		return item, err
	}
	for index := range item.Versions {
		if item.Versions[index].Version == item.ActiveVersion {
			active := item.Versions[index]
			item.Active = &active
			break
		}
	}
	item.RecentTests, _ = s.ListToolTestRuns(ctx, item.Name, 20)
	item.DependencyChanges, _ = s.listToolDependencies(ctx, item.Name, item.ActiveVersion)
	return item, nil
}

func (s *Service) ListToolVersions(ctx context.Context, name string) ([]domain.StewardToolVersion, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select tool_name,version,runtime,status,manifest,package_path,content_sha256,sbom,provenance,
		       validation_summary,created_at,validated_at
		from steward_tool_versions where tool_name=$1 order by created_at desc
	`, strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardToolVersion{}
	for rows.Next() {
		var item domain.StewardToolVersion
		var manifest, sbom, provenance []byte
		if err := rows.Scan(&item.ToolName, &item.Version, &item.Runtime, &item.Status, &manifest,
			&item.PackagePath, &item.ContentSHA256, &sbom, &provenance, &item.ValidationSummary,
			&item.CreatedAt, &item.ValidatedAt); err != nil {
			return nil, err
		}
		item.Manifest = decodeRuntimeMap(manifest)
		item.SBOM = decodeRuntimeMap(sbom)
		item.Provenance = decodeRuntimeMap(provenance)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListToolTestRuns(ctx context.Context, name string, limit int) ([]domain.StewardToolTestRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,tool_name,tool_version,test_name,status,input,output,error_summary,evidence,started_at,completed_at
		from steward_tool_test_runs where tool_name=$1 order by started_at desc limit $2
	`, strings.TrimSpace(name), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardToolTestRun{}
	for rows.Next() {
		var item domain.StewardToolTestRun
		var input, output, evidence []byte
		if err := rows.Scan(&item.ID, &item.ToolName, &item.ToolVersion, &item.TestName, &item.Status,
			&input, &output, &item.ErrorSummary, &evidence, &item.StartedAt, &item.CompletedAt); err != nil {
			return nil, err
		}
		item.Input = decodeRuntimeMap(input)
		item.Output = decodeRuntimeMap(output)
		_ = json.Unmarshal(evidence, &item.Evidence)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) DecideTool(ctx context.Context, name string, input ToolCatalogDecisionInput) (domain.StewardTool, error) {
	name = strings.TrimSpace(name)
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	tool, err := s.GetTool(ctx, name)
	if err != nil {
		return tool, err
	}
	now := time.Now().UTC()
	switch decision {
	case "enable":
		version := defaultString(strings.TrimSpace(input.Version), tool.ActiveVersion)
		if version == "" {
			return tool, fmt.Errorf("tool has no version to enable")
		}
		var status string
		if err := s.db.Pool.QueryRow(ctx, `select status from steward_tool_versions where tool_name=$1 and version=$2`, name, version).Scan(&status); err != nil {
			return tool, err
		}
		if status != "enabled" && status != "validated" {
			return tool, fmt.Errorf("tool version %s is not validated", version)
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_tools set enabled=true,active_version=$2,health_status='healthy',updated_at=$3,catalog_generation=catalog_generation+1 where name=$1`, name, version, now)
		if err == nil {
			var manifest ToolPackageManifest
			raw, queryErr := s.toolManifestJSON(ctx, name, version)
			if queryErr == nil && json.Unmarshal(raw, &manifest) == nil && manifest.Runtime != toolRuntimeBuiltin {
				s.runtimeTools.register(newPackageRuntimeTool(s, manifest))
			}
		}
	case "disable":
		_, err = s.db.Pool.Exec(ctx, `update steward_tools set enabled=false,health_status='disabled',updated_at=$2,catalog_generation=catalog_generation+1 where name=$1`, name, now)
		if err == nil && tool.Origin != "builtin" {
			s.runtimeTools.unregister(name)
		}
	case "rollback":
		version := strings.TrimSpace(input.Version)
		if version == "" {
			versions, listErr := s.ListToolVersions(ctx, name)
			if listErr != nil {
				return tool, listErr
			}
			for _, candidate := range versions {
				if candidate.Version != tool.ActiveVersion && (candidate.Status == "enabled" || candidate.Status == "validated") {
					version = candidate.Version
					break
				}
			}
		}
		if version == "" {
			return tool, fmt.Errorf("no validated previous version is available")
		}
		return s.DecideTool(ctx, name, ToolCatalogDecisionInput{Decision: "enable", Version: version})
	case "test":
		version := defaultString(strings.TrimSpace(input.Version), tool.ActiveVersion)
		if err := s.runStoredToolTests(ctx, name, version); err != nil {
			return s.GetTool(ctx, name)
		}
	case "delete":
		if tool.Origin == "builtin" || tool.Origin == "platform" {
			return tool, fmt.Errorf("built-in and platform tools can be disabled but not deleted")
		}
		s.runtimeTools.unregister(name)
		if removeErr := s.removeGeneratedToolFiles(name); removeErr != nil {
			return tool, removeErr
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_tools set enabled=false,health_status='retired',active_version='',updated_at=$2,catalog_generation=catalog_generation+1 where name=$1`, name, now)
	default:
		return tool, fmt.Errorf("decision must be enable, disable, test, rollback, or delete")
	}
	if err != nil {
		return tool, err
	}
	_ = s.recordToolCatalogEvent(ctx, name, input.Version, decision, "user", "tool catalog decision", nil)
	return s.GetTool(ctx, name)
}

func (s *Service) removeGeneratedToolFiles(name string) error {
	root, err := filepath.Abs(s.toolRootDir())
	if err != nil {
		return err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("refusing to remove tool files outside the tool root")
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove generated tool files: %w", err)
	}
	return nil
}

func scanStewardTool(row rowScanner) (domain.StewardTool, error) {
	var item domain.StewardTool
	err := row.Scan(&item.Name, &item.Title, &item.Description, &item.Origin, &item.Enabled,
		&item.ActiveVersion, &item.ExecutionTarget, &item.HealthStatus, &item.HealthSummary,
		&item.CatalogGeneration, &item.CreatedByEpisodeID, &item.CreatedByTurnID,
		&item.CreatedByModel, &item.InvocationCount, &item.LastUsedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (s *Service) toolManifestJSON(ctx context.Context, name, version string) ([]byte, error) {
	var raw []byte
	err := s.db.Pool.QueryRow(ctx, `select manifest from steward_tool_versions where tool_name=$1 and version=$2`, name, version).Scan(&raw)
	return raw, err
}

func (s *Service) listToolDependencies(ctx context.Context, name, version string) ([]domain.StewardToolDependency, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,tool_name,tool_version,ecosystem,package_name,requested_version,resolved_version,
		       install_scope,status,preexisting,previous_version,install_command,rollback_command,evidence,created_at,completed_at
		from steward_tool_dependency_changes where tool_name=$1 and ($2='' or tool_version=$2) order by created_at
	`, name, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardToolDependency{}
	for rows.Next() {
		var item domain.StewardToolDependency
		var evidence []byte
		if err := rows.Scan(&item.ID, &item.ToolName, &item.ToolVersion, &item.Ecosystem, &item.PackageName,
			&item.RequestedVersion, &item.ResolvedVersion, &item.InstallScope, &item.Status, &item.Preexisting,
			&item.PreviousVersion, &item.InstallCommand, &item.RollbackCommand, &evidence, &item.CreatedAt,
			&item.CompletedAt); err != nil {
			return nil, err
		}
		item.Evidence = decodeRuntimeMap(evidence)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) recordToolCatalogEvent(ctx context.Context, name, version, action, actor, summary string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	raw, _ := json.Marshal(details)
	_, err := s.db.Pool.Exec(ctx, `insert into steward_tool_catalog_events (tool_name,tool_version,action,actor,summary,details) values ($1,$2,$3,$4,$5,$6::jsonb)`, name, version, action, actor, summary, string(raw))
	return err
}

func toolNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func newToolTestRunID() string { return uuid.NewString() }
