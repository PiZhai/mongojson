package steward

import (
	"testing"
)

func TestNormalizeToolDefinitionRestrictsR30BrokerPermissionRange(t *testing.T) {
	for rank := 4; rank <= 7; rank++ {
		level := "A" + string(rune('0'+rank))
		item, err := normalizeToolDefinition(UpsertToolDefinitionInput{
			Action: "tool:test-a" + string(rune('0'+rank)), Name: "test", Executable: `C:\Windows\System32\whoami.exe`,
			PermissionLevel: level, RiskLevel: "critical", TimeoutSeconds: 30,
		})
		if err != nil || item.PermissionLevel != level {
			t.Fatalf("normalize %s = %#v, %v", level, item, err)
		}
		if item.Arguments == nil || item.RollbackArguments == nil {
			t.Fatalf("normalize %s returned nil argument arrays", level)
		}
	}
	for _, level := range []string{PermissionA3, PermissionA8, PermissionA9} {
		if _, err := normalizeToolDefinition(UpsertToolDefinitionInput{
			Action: "tool:denied", Name: "denied", Executable: `C:\Windows\System32\whoami.exe`, PermissionLevel: level, RiskLevel: "critical",
		}); err == nil {
			t.Fatalf("R3.0 accepted unsupported broker permission %s", level)
		}
	}
}
