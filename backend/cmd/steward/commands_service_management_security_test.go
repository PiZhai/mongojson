package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceInstallRejectsInvalidManagementAuthEnvironment(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "definitely-not-a-boolean")
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "")
	t.Setenv("STEWARD_MANAGEMENT_ALLOWED_ORIGINS", "")

	err := serviceInstall([]string{"--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "STEWARD_MANAGEMENT_AUTH_REQUIRED must be true or false") {
		t.Fatalf("invalid management auth environment error = %v", err)
	}
}

func TestServiceVerificationManagementTokenPrefersExplicitFile(t *testing.T) {
	t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", strings.Repeat("e", 32))
	fileToken := strings.Repeat("f", 32)
	path := filepath.Join(t.TempDir(), "management-token.txt")
	if err := os.WriteFile(path, []byte(fileToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := serviceVerificationManagementToken(path, map[string]string{
		"STEWARD_MANAGEMENT_AUTH_TOKEN": strings.Repeat("s", 32),
	})
	if err != nil {
		t.Fatalf("read explicit management token file: %v", err)
	}
	if token != fileToken {
		t.Fatalf("management token source priority was not explicit file")
	}
}

func TestValidateServiceEnvManagementTargetRequiresVerificationCredentialBeforeMutation(t *testing.T) {
	targetToken := strings.Repeat("t", 32)
	target := map[string]string{
		"STEWARD_MANAGEMENT_AUTH_REQUIRED":   "true",
		"STEWARD_MANAGEMENT_AUTH_TOKEN":      targetToken,
		"STEWARD_MANAGEMENT_ALLOWED_ORIGINS": "https://console.example.test",
	}
	if err := validateServiceEnvManagementTarget(target, "", true); err == nil || !strings.Contains(err.Error(), "management-token-file") {
		t.Fatalf("missing verification credential error = %v", err)
	}
	if err := validateServiceEnvManagementTarget(target, strings.Repeat("x", 32), true); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched verification credential error = %v", err)
	}
	if err := validateServiceEnvManagementTarget(target, targetToken, true); err != nil {
		t.Fatalf("matching verification credential rejected: %v", err)
	}
}

func TestValidateServiceEnvManagementTargetRejectsRuntimeInvalidValues(t *testing.T) {
	for _, tt := range []struct {
		name    string
		target  map[string]string
		wantErr string
	}{
		{
			name:    "invalid auth boolean",
			target:  map[string]string{"STEWARD_MANAGEMENT_AUTH_REQUIRED": "truthy"},
			wantErr: "must be true or false",
		},
		{
			name: "short configured token",
			target: map[string]string{
				"STEWARD_MANAGEMENT_AUTH_REQUIRED": "false",
				"STEWARD_MANAGEMENT_AUTH_TOKEN":    "short-token",
			},
			wantErr: "at least 32 characters",
		},
		{
			name: "invalid configured origin",
			target: map[string]string{
				"STEWARD_MANAGEMENT_ALLOWED_ORIGINS": "https://console.example.test/path",
			},
			wantErr: "invalid origin",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServiceEnvManagementTarget(tt.target, "", false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("target validation error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestServiceInstallRejectsRuntimeInvalidManagementSecurity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "non-empty short token",
			args:    []string{"--dry-run", "--management-auth-token", "short-token"},
			wantErr: "must contain at least 32 characters",
		},
		{
			name:    "required without token",
			args:    []string{"--dry-run", "--management-auth-required"},
			wantErr: "is required when management authentication is enabled",
		},
		{
			name:    "wildcard origin",
			args:    []string{"--dry-run", "--management-allowed-origins", "*"},
			wantErr: "must not contain wildcard origins",
		},
		{
			name:    "origin with path",
			args:    []string{"--dry-run", "--management-allowed-origins", "https://console.example.test/path"},
			wantErr: "contains invalid origin",
		},
		{
			name:    "unsupported origin scheme",
			args:    []string{"--dry-run", "--management-allowed-origins", "file://console.example.test"},
			wantErr: "must use http or https",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("STEWARD_MANAGEMENT_AUTH_REQUIRED", "false")
			t.Setenv("STEWARD_MANAGEMENT_AUTH_TOKEN", "")
			t.Setenv("STEWARD_MANAGEMENT_ALLOWED_ORIGINS", "")
			err := serviceInstall(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("service install error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
