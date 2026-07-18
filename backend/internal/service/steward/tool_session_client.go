package steward

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

type sessionToolRequest struct {
	Manifest   ToolPackageManifest `json:"manifest"`
	PackageDir string              `json:"package_dir"`
	Input      map[string]any      `json:"input"`
}

type sessionToolResponse struct {
	OK       bool               `json:"ok"`
	Output   map[string]any     `json:"output"`
	Evidence []toolHostEvidence `json:"evidence"`
	Error    string             `json:"error"`
}

func (s *Service) executeSessionTool(ctx context.Context, manifest ToolPackageManifest, input map[string]any) (RuntimeToolResult, error) {
	request := sessionToolRequest{Manifest: manifest, PackageDir: s.toolPackageDir(manifest.Name, manifest.Version), Input: input}
	payload, _ := json.Marshal(request)
	key, err := sessionToolKey()
	if err != nil {
		return RuntimeToolResult{}, err
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("STEWARD_COMPANION_URL")), "/")
	client := &http.Client{Timeout: time.Duration(manifest.DefaultTimeoutSec+10) * time.Second}
	if base == "" {
		base = "http://steward-companion"
		client = sessionCompanionHTTPClient(time.Duration(manifest.DefaultTimeoutSec+10) * time.Second)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/tools/execute", bytes.NewReader(payload))
	if err != nil {
		return RuntimeToolResult{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	httpRequest.Header.Set("X-Steward-Tool-Timestamp", timestamp)
	httpRequest.Header.Set("X-Steward-Tool-Signature", signSessionToolPayload(key, timestamp, payload))
	response, err := client.Do(httpRequest)
	if err != nil {
		return RuntimeToolResult{}, fmt.Errorf("interactive session companion unavailable: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if response.StatusCode != http.StatusOK {
		return RuntimeToolResult{}, fmt.Errorf("interactive session companion returned %s: %s", response.Status, truncateAdvisorText(string(body), 2000))
	}
	var decoded sessionToolResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return RuntimeToolResult{}, err
	}
	if !decoded.OK {
		return RuntimeToolResult{}, fmt.Errorf("interactive tool failed: %s", decoded.Error)
	}
	evidence := make([]RuntimeEvidence, 0, len(decoded.Evidence))
	for _, item := range decoded.Evidence {
		evidence = append(evidence, RuntimeEvidence{Kind: item.Kind, Summary: item.Summary, Payload: item.Payload})
	}
	return RuntimeToolResult{Output: decoded.Output, Evidence: evidence}, nil
}

func sessionToolKey() ([]byte, error) {
	value := strings.TrimSpace(os.Getenv("STEWARD_LOCAL_ENCRYPTION_KEY"))
	key, err := base64.StdEncoding.DecodeString(value)
	if value == "" || err != nil || len(key) != 32 {
		return nil, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY must be configured for session tools")
	}
	return key, nil
}

func signSessionToolPayload(key []byte, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) GetToolHostStatuses(ctx context.Context) []domain.StewardToolHostStatus {
	now := time.Now().UTC()
	systemOnline := s != nil && s.runtimeTools != nil
	systemSummary := "local tool runtime"
	systemTransport := "in-process + Job Object"
	if restrictedWindowsMainService() {
		systemOnline = s != nil && s.runtimeR3 && s.privilegeBroker != nil && s.privilegeBrokerError == nil
		systemSummary = "restricted main service; privileged system mutations require a fixed Broker capability"
		systemTransport = "Privilege Broker only"
	}
	hosts := []domain.StewardToolHostStatus{{
		Name: "System Host", Target: "system", Transport: systemTransport,
		Online: systemOnline, Summary: systemSummary, CheckedAt: now,
	}}
	status := domain.StewardToolHostStatus{
		Name: "Session Companion", Target: "session", Transport: "authenticated named pipe",
		CheckedAt: now,
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("STEWARD_COMPANION_URL")), "/")
	client := &http.Client{Timeout: 800 * time.Millisecond}
	if base == "" {
		base = "http://steward-companion"
		client = sessionCompanionHTTPClient(800 * time.Millisecond)
	} else {
		status.Transport = "explicit loopback HTTP"
	}
	requestCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base+"/status", nil)
	if err == nil {
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			defer response.Body.Close()
			status.Online = response.StatusCode == http.StatusOK
			if !status.Online {
				status.Summary = "status endpoint returned " + response.Status
			}
		}
	}
	if err != nil {
		status.Summary = "companion unavailable: " + truncateAdvisorText(err.Error(), 240)
	} else if status.Online {
		status.Summary = "interactive desktop session is available"
	}
	return append(hosts, status)
}
