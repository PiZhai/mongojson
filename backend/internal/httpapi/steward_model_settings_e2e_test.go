package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/service/steward"
)

func TestStewardModelSettingsHTTPPersistsEncryptedSecretWithoutReturningIt(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the model settings HTTP acceptance test")
	}
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", "model-settings-test")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "model_settings"), "model-settings",
		steward.WithRuntimeR2Enabled(true))

	const secret = "model-secret-must-never-be-returned-1234"
	payload := map[string]any{
		"provider": "openai-compatible", "base_url": "http://127.0.0.1:11434/v1", "model": "local-test-model",
		"api_key": secret, "allow_no_api_key": false, "max_data_level": "D1", "timeout_seconds": 20,
	}
	body, _ := json.Marshal(payload)
	blockedRequest, _ := http.NewRequestWithContext(ctx, http.MethodPatch, node.apiBase+"/steward/model-settings", bytes.NewReader(body))
	blockedRequest.Header.Set("Content-Type", "application/json")
	blockedRequest.Header.Set("Origin", "https://attacker.example")
	blockedResponse, err := http.DefaultClient.Do(blockedRequest)
	if err != nil {
		t.Fatal(err)
	}
	blockedResponse.Body.Close()
	if blockedResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin model settings update returned %d, want 403", blockedResponse.StatusCode)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, node.apiBase+"/steward/model-settings", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update model settings returned %d: %s", response.StatusCode, data)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("update response exposed the model API key")
	}

	response, err = http.Get(node.apiBase + "/steward/model-settings")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || bytes.Contains(data, []byte(secret)) {
		t.Fatalf("model settings GET was unsafe or failed: status=%d body=%s", response.StatusCode, data)
	}
	var result struct {
		Settings steward.StewardModelSettings `json:"settings"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Settings.Source != "database" || !result.Settings.APIKeyConfigured || result.Settings.APIKeyMask != "••••••••1234" {
		t.Fatalf("unexpected public model settings: %+v", result.Settings)
	}
	var storedJSON string
	if err := node.pool.QueryRow(ctx, `select api_key_encrypted::text from steward_model_settings where id='primary'`).Scan(&storedJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedJSON, secret) || !strings.Contains(storedJSON, "ciphertext") {
		t.Fatal("database model secret is not stored as an encrypted envelope")
	}
}
