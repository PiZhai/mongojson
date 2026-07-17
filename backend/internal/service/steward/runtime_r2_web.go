package steward

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

type runtimeWebFetchTool struct{ service *Service }
type runtimeBrowserOpenTool struct{ service *Service }

func newRuntimeWebFetchTool(service *Service) RuntimeTool {
	return runtimeWebFetchTool{service: service}
}
func newRuntimeBrowserOpenTool(service *Service) RuntimeTool {
	return runtimeBrowserOpenTool{service: service}
}

func (runtimeWebFetchTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "web.fetch_text", Version: "2.0.0", Description: "Fetch one HTTP(S) text resource with redirect limits and private-network SSRF protection.",
		InputSchema:     map[string]any{"type": "object", "required": []string{"url"}, "properties": map[string]any{"url": map[string]any{"type": "string"}, "max_bytes": map[string]any{"type": "integer"}}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"url", "status_code", "content_type", "content", "sha256", "bytes"}},
		PermissionLevel: PermissionA2, RiskLevel: "low", SideEffect: RuntimeSideEffectNetwork,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyInherent,
		Deterministic: false, SupportsCancel: true, DefaultTimeoutSec: 30,
	}
}

func (t runtimeWebFetchTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "url", "max_bytes"); err != nil {
		return err
	}
	rawURL, err := runtimeRequiredString(input, "url")
	if err != nil {
		return err
	}
	if _, err := t.service.validateRuntimeURL(context.Background(), rawURL); err != nil {
		return err
	}
	maxBytes, err := runtimeInt(input, "max_bytes", 512<<10)
	if err != nil || maxBytes < 1 || maxBytes > runtimeMaxTextBytes {
		return fmt.Errorf("max_bytes must be between 1 and %d", runtimeMaxTextBytes)
	}
	return nil
}

func (t runtimeWebFetchTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	rawURL, _ := runtimeRequiredString(input, "url")
	maxBytes, _ := runtimeInt(input, "max_bytes", 512<<10)
	client := t.service.runtimeHTTPClient()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	request.Header.Set("Accept", "text/plain, text/html, application/json, application/xml;q=0.9, */*;q=0.1")
	request.Header.Set("User-Agent", "MongoJSON-Steward-Runtime/2")
	response, err := client.Do(request)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RuntimeToolResult{}, fmt.Errorf("web request returned %s", response.Status)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if contentType != "" && !strings.HasPrefix(contentType, "text/") && !strings.Contains(contentType, "json") && !strings.Contains(contentType, "xml") && !strings.Contains(contentType, "javascript") {
		return RuntimeToolResult{}, fmt.Errorf("response content type %q is not a supported text format", contentType)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
	if err != nil {
		return RuntimeToolResult{}, err
	}
	if len(data) > maxBytes {
		return RuntimeToolResult{}, fmt.Errorf("response exceeds max_bytes=%d", maxBytes)
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return RuntimeToolResult{}, fmt.Errorf("response appears to be binary")
	}
	digest := sha256.Sum256(data)
	output := map[string]any{
		"url": response.Request.URL.String(), "status_code": response.StatusCode, "content_type": response.Header.Get("Content-Type"),
		"content": string(data), "bytes": len(data), "sha256": hex.EncodeToString(digest[:]),
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "http_response", Summary: fmt.Sprintf("verified HTTP %d text response", response.StatusCode), Payload: map[string]any{
		"url": output["url"], "status_code": response.StatusCode, "content_type": output["content_type"], "bytes": len(data), "sha256": output["sha256"],
	}}}}, nil
}

func (runtimeWebFetchTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	content, ok := output["content"].(string)
	if !ok {
		return fmt.Errorf("web output is missing content")
	}
	digest := sha256.Sum256([]byte(content))
	if output["sha256"] != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("web output hash does not match content")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (runtimeBrowserOpenTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "browser.open_url", Version: "2.0.0", Description: "Ask the current user's default browser handler to open one validated HTTP(S) URL; success means launch dispatch was accepted, not that page load completed.",
		InputSchema:     map[string]any{"type": "object", "required": []string{"url"}, "properties": map[string]any{"url": map[string]any{"type": "string"}}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"url", "dispatch_status", "platform"}},
		PermissionLevel: PermissionA2, RiskLevel: "medium", SideEffect: RuntimeSideEffectLaunch,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyNonIdempotent,
		Deterministic: false, SupportsCancel: false, DefaultTimeoutSec: 15,
	}
}

func (t runtimeBrowserOpenTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "url"); err != nil {
		return err
	}
	if t.service == nil || !t.service.runtimeBrowserOpen {
		return fmt.Errorf("browser.open_url is disabled")
	}
	rawURL, err := runtimeRequiredString(input, "url")
	if err != nil {
		return err
	}
	_, err = t.service.validateRuntimeURL(context.Background(), rawURL)
	return err
}

func (t runtimeBrowserOpenTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	rawURL, _ := runtimeRequiredString(input, "url")
	parsed, _ := t.service.validateRuntimeURL(ctx, rawURL)
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", parsed.String())
	case "darwin":
		command = exec.CommandContext(ctx, "open", parsed.String())
	default:
		command = exec.CommandContext(ctx, "xdg-open", parsed.String())
	}
	command.Env = sanitizedRuntimeEnvironment()
	if err := command.Start(); err != nil {
		return RuntimeToolResult{}, err
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		return RuntimeToolResult{}, err
	}
	output := map[string]any{"url": parsed.String(), "dispatch_status": "accepted", "platform": runtime.GOOS, "launcher_pid": pid}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "browser_launch_dispatch", Summary: "default browser launch request accepted by the operating system", Payload: output}}}, nil
}

func (runtimeBrowserOpenTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if output["dispatch_status"] != "accepted" {
		return fmt.Errorf("browser launch dispatch was not accepted")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (s *Service) validateRuntimeURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, fmt.Errorf("url must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("url userinfo is not allowed")
	}
	if _, err := s.runtimeSafeIPs(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (s *Service) runtimeSafeIPs(ctx context.Context, hostname string) ([]net.IP, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	allowed := s != nil && s.runtimeWebAllowedHosts[hostname]
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("resolve URL host: %w", err)
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if !ownerModeEnabled() && !allowed && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
			return nil, fmt.Errorf("URL host resolves to a private or local address")
		}
		result = append(result, ip)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("URL host did not resolve to an address")
	}
	return result, nil
}

func (s *Service) runtimeHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := s.runtimeSafeIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
		},
	}
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("redirect limit exceeded")
		}
		_, err := s.validateRuntimeURL(request.Context(), request.URL.String())
		return err
	}
	return client
}
