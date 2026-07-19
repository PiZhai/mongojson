package steward

import (
	"slices"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestBuildAgentToolContextHydrationResultGate(t *testing.T) {
	tests := []struct {
		name       string
		result     domain.StewardAgentToolResult
		wantLoaded bool
	}{
		{
			name:       "successful describe",
			result:     domain.StewardAgentToolResult{ToolName: "tool.describe", Output: map[string]any{"name": "screen.capture"}},
			wantLoaded: true,
		},
		{
			name:       "successful create",
			result:     domain.StewardAgentToolResult{ToolName: "tool.create", Output: map[string]any{"name": "screen.capture"}},
			wantLoaded: true,
		},
		{
			name:       "successful update",
			result:     domain.StewardAgentToolResult{ToolName: "tool.update", Output: map[string]any{"name": "screen.capture"}},
			wantLoaded: true,
		},
		{
			name:   "failed describe with name",
			result: domain.StewardAgentToolResult{ToolName: "tool.describe", Output: map[string]any{"name": "screen.capture"}, Error: "describe failed"},
		},
		{
			name:   "failed create with name",
			result: domain.StewardAgentToolResult{ToolName: "tool.create", Output: map[string]any{"name": "screen.capture"}, Error: "validation failed"},
		},
		{
			name:   "failed update with name",
			result: domain.StewardAgentToolResult{ToolName: "tool.update", Output: map[string]any{"name": "screen.capture"}, Error: "update failed"},
		},
		{
			name:   "empty name",
			result: domain.StewardAgentToolResult{ToolName: "tool.describe", Output: map[string]any{"name": ""}},
		},
		{
			name:   "non-string name",
			result: domain.StewardAgentToolResult{ToolName: "tool.create", Output: map[string]any{"name": 42}},
		},
		{
			name:   "missing name",
			result: domain.StewardAgentToolResult{ToolName: "tool.update", Output: map[string]any{}},
		},
		{
			name:   "unrelated tool result",
			result: domain.StewardAgentToolResult{ToolName: "fs.read_text", Output: map[string]any{"name": "screen.capture"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := []domain.StewardTool{{Name: "screen.capture", Enabled: true, ActiveVersion: "1.0.0"}}
			allSpecs := []domain.StewardToolSpec{{Name: "screen.capture", Version: "1.0.0"}}
			episode := &domain.StewardAgentEpisode{Turns: []domain.StewardAgentTurn{{ToolResults: []domain.StewardAgentToolResult{test.result}}}}

			specs, catalog, versions, hydratedNames := buildAgentToolContext(items, allSpecs, episode)

			if len(catalog) != 1 || catalog[0].Name != "screen.capture" {
				t.Fatalf("catalog = %+v, want enabled tool retained", catalog)
			}
			if versions["screen.capture"] != "1.0.0" {
				t.Fatalf("versions = %+v, want catalog version retained", versions)
			}
			if test.wantLoaded {
				if len(specs) != 1 || specs[0].Name != "screen.capture" {
					t.Fatalf("specs = %+v, want successful result hydrated", specs)
				}
				if !slices.Equal(hydratedNames, []string{"screen.capture"}) {
					t.Fatalf("hydrated names = %v, want screen.capture", hydratedNames)
				}
				return
			}
			if len(specs) != 0 {
				t.Fatalf("specs = %+v, want result rejected by hydration gate", specs)
			}
			if len(hydratedNames) != 0 {
				t.Fatalf("hydrated names = %v, want none", hydratedNames)
			}
		})
	}
}

func TestBuildAgentToolContextRequiresCatalogRegistryIntersection(t *testing.T) {
	items := []domain.StewardTool{
		{Name: "capture.eligible", Enabled: true, ActiveVersion: "1.0.0"},
		{Name: "capture.catalog_only", Enabled: true, ActiveVersion: "2.0.0"},
		{Name: "capture.persisted_without_runtime", Enabled: true, ActiveVersion: "3.0.0"},
		{Name: "capture.not_hydrated", Enabled: true, ActiveVersion: "4.0.0"},
		{Name: "runtime.echo", Enabled: true, ActiveVersion: "5.0.0"},
	}
	allSpecs := []domain.StewardToolSpec{
		{Name: "capture.eligible", Version: "1.0.0"},
		{Name: "capture.registry_only", Version: "2.0.0"},
		{Name: "capture.not_hydrated", Version: "4.0.0"},
		{Name: "runtime.echo", Version: "5.0.0"},
	}
	episode := &domain.StewardAgentEpisode{
		HydratedToolNames: []string{"capture.persisted_without_runtime"},
		Turns: []domain.StewardAgentTurn{{ToolResults: []domain.StewardAgentToolResult{
			{ToolName: "tool.describe", Output: map[string]any{"name": "capture.eligible"}},
			{ToolName: "tool.create", Output: map[string]any{"name": "capture.catalog_only"}},
			{ToolName: "tool.update", Output: map[string]any{"name": "capture.registry_only"}},
		}}},
	}

	specs, catalog, versions, hydratedNames := buildAgentToolContext(items, allSpecs, episode)

	if got := toolContextSpecNames(specs); !slices.Equal(got, []string{"capture.eligible", "runtime.echo"}) {
		t.Fatalf("model specs = %v, want hydrated intersection plus common registered tool", got)
	}
	if !slices.Equal(hydratedNames, []string{"capture.eligible"}) {
		t.Fatalf("hydrated names = %v, want only enabled catalog and runtime registry intersection", hydratedNames)
	}
	if got := toolContextCatalogNames(catalog); !slices.Equal(got, []string{
		"capture.eligible",
		"capture.catalog_only",
		"capture.persisted_without_runtime",
		"capture.not_hydrated",
		"runtime.echo",
	}) {
		t.Fatalf("compact catalog = %v, want all enabled catalog items", got)
	}
	if len(versions) != len(items) || versions["capture.catalog_only"] != "2.0.0" || versions["capture.persisted_without_runtime"] != "3.0.0" {
		t.Fatalf("versions = %+v, want every enabled catalog item", versions)
	}
}

func toolContextSpecNames(specs []domain.StewardToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func toolContextCatalogNames(catalog []AgentToolCatalogEntry) []string {
	names := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.Name)
	}
	return names
}
