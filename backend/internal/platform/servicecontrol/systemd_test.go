package servicecontrol

import (
	"strings"
	"testing"
)

func TestSystemdEnvironmentFileRoundTrip(t *testing.T) {
	input := map[string]string{
		"HTTP_ADDR":           "127.0.0.1:18080",
		"STEWARD_SYNC_SECRET": "secret with spaces=and-equals",
	}
	rendered := renderSystemdEnvironmentFile(input)
	parsed, err := parseSystemdEnvironmentFile(rendered)
	if err != nil {
		t.Fatalf("parse rendered environment file: %v", err)
	}
	for key, want := range input {
		if got := parsed[key]; got != want {
			t.Fatalf("parsed %s = %q, want %q", key, got, want)
		}
	}
}

func TestReplaceSystemdEnvironmentDirectivesMigratesLegacyUnit(t *testing.T) {
	legacy := strings.Join([]string{
		"[Service]",
		`Environment="STEWARD_SYNC_SECRET=top-secret"`,
		`Environment="HTTP_ADDR=127.0.0.1:18080"`,
		`ExecStart="/opt/steward" run`,
	}, "\n")
	parsed, err := parseInlineSystemdEnvironment(legacy)
	if err != nil || parsed["STEWARD_SYNC_SECRET"] != "top-secret" {
		t.Fatalf("parse legacy unit: env=%#v err=%v", parsed, err)
	}
	migrated, err := replaceSystemdEnvironmentDirectives(legacy, "/etc/mongojson-steward/steward.env")
	if err != nil {
		t.Fatalf("migrate legacy unit: %v", err)
	}
	if strings.Contains(migrated, "top-secret") || strings.Contains(migrated, "Environment=\"") ||
		!strings.Contains(migrated, `EnvironmentFile="/etc/mongojson-steward/steward.env"`) {
		t.Fatalf("unexpected migrated unit:\n%s", migrated)
	}
}

func TestParseSystemdEnvironmentFileRejectsMalformedValue(t *testing.T) {
	if _, err := parseSystemdEnvironmentFile(`STEWARD_SYNC_SECRET="unterminated`); err == nil {
		t.Fatalf("expected malformed quoted value to fail")
	}
}
