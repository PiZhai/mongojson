package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStewardUIDirUsesExplicitValue(t *testing.T) {
	if got := resolveStewardUIDir("  custom-ui  ", filepath.Join(t.TempDir(), "steward.exe")); got != "custom-ui" {
		t.Fatalf("resolveStewardUIDir explicit = %q, want custom-ui", got)
	}
}

func TestResolveStewardUIDirFindsBundledWorkspace(t *testing.T) {
	root := t.TempDir()
	uiDir := filepath.Join(root, bundledUIDirectoryName)
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatalf("create ui dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<!doctype html>"), 0o600); err != nil {
		t.Fatalf("write ui index: %v", err)
	}
	got := resolveStewardUIDir("", filepath.Join(root, "steward.exe"))
	want, err := filepath.Abs(uiDir)
	if err != nil {
		t.Fatalf("resolve expected ui dir: %v", err)
	}
	if got != want {
		t.Fatalf("resolveStewardUIDir bundled = %q, want %q", got, want)
	}
}

func TestResolveStewardUIDirRequiresIndexFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, bundledUIDirectoryName), 0o755); err != nil {
		t.Fatalf("create ui dir: %v", err)
	}
	if got := resolveStewardUIDir("", filepath.Join(root, "steward.exe")); got != "" {
		t.Fatalf("resolveStewardUIDir without index = %q, want empty", got)
	}
}
