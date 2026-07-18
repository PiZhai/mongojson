package steward

import (
	"strings"
	"testing"
)

func TestValidateModelSettingsRequiresSecretForRemoteEndpoint(t *testing.T) {
	values := modelSettingsValues{
		provider: "openai-compatible", baseURL: "https://models.example/v1", model: "example-model",
		maxDataLevel: DataD1, timeoutSeconds: 30,
	}
	if err := validateModelSettings(values); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected API key validation error, got %v", err)
	}
	values.allowNoAPIKey = true
	if err := validateModelSettings(values); err == nil || !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("expected loopback validation error, got %v", err)
	}
}

func TestModelSettingsKeyRecoveryRequiresExplicitMarker(t *testing.T) {
	t.Setenv("STEWARD_MODEL_SETTINGS_KEY_RECOVERY", "")
	if modelSettingsKeyRecoveryEnabled() {
		t.Fatal("key recovery unexpectedly enabled by default")
	}
	t.Setenv("STEWARD_MODEL_SETTINGS_KEY_RECOVERY", "true")
	if !modelSettingsKeyRecoveryEnabled() {
		t.Fatal("explicit key recovery marker was ignored")
	}
}

func TestModelSettingsAllowNoKeyOnlyOnLoopback(t *testing.T) {
	values := modelSettingsValues{
		provider: "openai-compatible", baseURL: "http://127.0.0.1:11434/v1", model: "local-model",
		allowNoAPIKey: true, maxDataLevel: DataD1, timeoutSeconds: 30,
	}
	if err := validateModelSettings(values); err != nil {
		t.Fatalf("expected loopback model settings to be accepted: %v", err)
	}
	advisor, planner := modelClientsFromSettings(values)
	if !advisor.Status().Enabled {
		t.Fatalf("expected advisor enabled: %+v", advisor.Status())
	}
	if !planner.Status().Enabled || !strings.Contains(planner.Status().Provider, "openai-compatible") {
		t.Fatalf("expected remote planner enabled: %+v", planner.Status())
	}
}

func TestPublicModelSettingsNeverReturnsAPIKey(t *testing.T) {
	service := NewService(nil)
	values := modelSettingsValues{
		provider: "openai-compatible", baseURL: "https://models.example/v1", model: "example-model",
		apiKey: "secret-value-1234", maxDataLevel: DataD1, timeoutSeconds: 30, source: modelSettingsSourceDB,
	}
	service.applyModelSettings(values)
	public := service.publicModelSettings(values)
	if !public.APIKeyConfigured || public.APIKeyMask != "••••••••1234" {
		t.Fatalf("unexpected secret metadata: %+v", public)
	}
	if strings.Contains(public.APIKeyMask, values.apiKey) {
		t.Fatal("public settings exposed the API key")
	}
}
