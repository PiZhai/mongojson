package steward

import (
	"strings"
	"testing"
)

func TestCompactAgentToolOutputPreservesHydrationName(t *testing.T) {
	for _, toolName := range []string{"tool.describe", "tool.create", "tool.update"} {
		t.Run(toolName, func(t *testing.T) {
			output := map[string]any{
				"name":    "screen.capture",
				"details": strings.Repeat("x", 33000),
			}

			compacted := compactAgentToolOutput(toolName, true, output, map[string]any{"execution_id": "execution-1"})

			if compacted["truncated"] != true {
				t.Fatalf("truncated = %v, want true", compacted["truncated"])
			}
			if compacted["name"] != "screen.capture" {
				t.Fatalf("name = %v, want screen.capture", compacted["name"])
			}
		})
	}
}

func TestCompactAgentToolOutputDoesNotPreserveNameForOrdinaryLargeResult(t *testing.T) {
	output := map[string]any{
		"name":    "screen.capture",
		"details": strings.Repeat("x", 33000),
	}

	compacted := compactAgentToolOutput("screen.capture", true, output, nil)

	if compacted["truncated"] != true {
		t.Fatalf("truncated = %v, want true", compacted["truncated"])
	}
	if name, exists := compacted["name"]; exists {
		t.Fatalf("ordinary large result unexpectedly retained name %v", name)
	}
}

func TestCompactAgentToolOutputDoesNotInventHydrationName(t *testing.T) {
	for _, test := range []struct {
		name   string
		output map[string]any
	}{
		{name: "missing", output: map[string]any{"details": strings.Repeat("x", 33000)}},
		{name: "non-string", output: map[string]any{"name": 42, "details": strings.Repeat("x", 33000)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compacted := compactAgentToolOutput("tool.describe", true, test.output, nil)

			if compacted["truncated"] != true {
				t.Fatalf("truncated = %v, want true", compacted["truncated"])
			}
			if name, exists := compacted["name"]; exists {
				t.Fatalf("invalid hydration result unexpectedly produced name %v", name)
			}
		})
	}
}

func TestCompactAgentToolOutputDoesNotPreserveNameForFailedHydration(t *testing.T) {
	output := map[string]any{
		"name":    "screen.capture",
		"details": strings.Repeat("x", 33000),
	}

	compacted := compactAgentToolOutput("tool.describe", false, output, nil)

	if name, exists := compacted["name"]; exists {
		t.Fatalf("failed hydration result unexpectedly retained name %v", name)
	}
}
