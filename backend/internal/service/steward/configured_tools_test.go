package steward

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeToolDefinitionSupportsFullPermissionRange(t *testing.T) {
	for rank := 0; rank <= 9; rank++ {
		level := "A" + string(rune('0'+rank))
		item, err := normalizeToolDefinition(UpsertToolDefinitionInput{
			Action: "tool:test-" + strings.ToLower(level), Name: "test", Executable: `C:\Windows\System32\whoami.exe`,
			PermissionLevel: level, RiskLevel: "critical", TimeoutSeconds: 30,
		})
		if err != nil || item.PermissionLevel != level {
			t.Fatalf("normalize %s = %#v, %v", level, item, err)
		}
		if item.Arguments == nil || item.RollbackArguments == nil {
			t.Fatalf("normalize %s returned nil argument arrays", level)
		}
	}
	if _, err := normalizeToolDefinition(UpsertToolDefinitionInput{
		Action: "shell command", Name: "bad", Executable: "relative.exe", PermissionLevel: PermissionA9,
	}); err == nil {
		t.Fatal("invalid tool definition was accepted")
	}
}

func TestConfiguredToolEnvironmentExcludesServiceSecrets(t *testing.T) {
	t.Setenv("STEWARD_LLM_API_KEY", "must-not-be-inherited")
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", "must-not-be-inherited")
	t.Setenv("PATH", os.Getenv("PATH"))
	environment := strings.Join(configuredToolEnvironment(), "\n")
	if strings.Contains(environment, "STEWARD_LLM_API_KEY") || strings.Contains(environment, "STEWARD_LOCAL_ENCRYPTION_KEY") {
		t.Fatalf("configured tool environment leaked service secrets: %s", environment)
	}
	if !strings.Contains(strings.ToUpper(environment), "PATH=") {
		t.Fatal("configured tool environment omitted PATH")
	}
}

func TestCappedToolOutputBoundsStoredBytes(t *testing.T) {
	var output cappedToolOutput
	payload := []byte(strings.Repeat("x", 64*1024))
	written, err := output.Write(payload)
	if err != nil || written != len(payload) || output.total != int64(len(payload)) {
		t.Fatalf("write = %d total=%d err=%v", written, output.total, err)
	}
	if output.buffer.Len() != 32*1024 || len(output.digest()) != 64 {
		t.Fatalf("buffer=%d digest=%q", output.buffer.Len(), output.digest())
	}
}
