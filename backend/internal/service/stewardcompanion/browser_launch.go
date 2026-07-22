package stewardcompanion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func RequestBrowserLaunchURL(ctx context.Context, apiBase string, client *http.Client) (string, error) {
	if client == nil {
		return "", fmt.Errorf("management HTTP client is required")
	}
	base, err := companionAPIBase(apiBase)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/auth/browser-tickets", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request browser ticket: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("request browser ticket: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var result struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode browser ticket: %w", err)
	}
	ticket := strings.TrimSpace(result.Ticket)
	if len(ticket) < 32 || strings.ContainsAny(ticket, "/\\?#\r\n") {
		return "", fmt.Errorf("management service returned an invalid browser ticket")
	}
	parsedBase, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return parsedBase.Scheme + "://" + parsedBase.Host + "/api/auth/browser-tickets/" + url.PathEscape(ticket), nil
}
