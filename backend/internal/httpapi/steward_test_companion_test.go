package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mongojson/backend/internal/service/steward"
)

func startStewardTestCompanion(t *testing.T, keyID string) {
	t.Helper()
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("STEWARD_LOCAL_ENCRYPTION_KEY_ID", keyID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		if r.URL.Path != "/tools/execute" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Manifest   steward.ToolPackageManifest `json:"manifest"`
			PackageDir string                      `json:"package_dir"`
			Input      map[string]any              `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := steward.ExecuteCompanionToolPackage(r.Context(), request.Manifest, request.PackageDir, request.Input)
		response := map[string]any{"ok": err == nil, "output": result.Output, "evidence": result.Evidence}
		if err != nil {
			response["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	t.Setenv("STEWARD_COMPANION_URL", server.URL)
}
