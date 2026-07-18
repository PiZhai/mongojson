package steward

import (
	"strings"
	"testing"
)

func validGeneratedToolManifestForTest() ToolPackageManifest {
	return ToolPackageManifest{
		Name: "example.lookup", Version: "1.0.0", Title: "Example lookup", Description: "Look up one example.",
		Runtime: toolRuntimePowerShell, ExecutionTarget: toolTargetSystem, Entrypoint: "tool.ps1",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"scope": map[string]any{"type": "string"}},
		},
		OutputSchema: map[string]any{"type": "object"},
		Files:        []ToolPackageFile{{Path: "tool.ps1", Content: "placeholder"}},
		Tests:        []ToolPackageTest{{Name: "empty input", Input: map[string]any{}}},
		SideEffect:   RuntimeSideEffectNone,
	}
}

func TestNormalizeToolPackageManifestNormalizesNullTestInput(t *testing.T) {
	manifest := validGeneratedToolManifestForTest()
	manifest.Tests[0].Input = nil
	normalized, err := normalizeToolPackageManifest(manifest)
	if err != nil {
		t.Fatalf("normalize manifest: %v", err)
	}
	if normalized.Tests[0].Input == nil || len(normalized.Tests[0].Input) != 0 {
		t.Fatalf("expected null test input to become an empty object, got %#v", normalized.Tests[0].Input)
	}
}

func TestNormalizeToolPackageManifestRejectsNestedArgumentsTestInput(t *testing.T) {
	manifest := validGeneratedToolManifestForTest()
	manifest.Tests[0].Input = map[string]any{"arguments": map[string]any{"scope": "User"}}
	_, err := normalizeToolPackageManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), `not a nested "arguments" object`) {
		t.Fatalf("expected nested arguments guidance, got %v", err)
	}
}

func TestNormalizeToolPackageManifestValidatesEveryTestBeforePublish(t *testing.T) {
	manifest := validGeneratedToolManifestForTest()
	manifest.Tests = append(manifest.Tests, ToolPackageTest{Name: "bad input", Input: map[string]any{"unknown": true}})
	_, err := normalizeToolPackageManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), `test "bad input" input does not match input_schema: unknown argument unknown`) {
		t.Fatalf("expected input schema guidance, got %v", err)
	}
}
