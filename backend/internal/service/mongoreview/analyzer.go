package mongoreview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type AnalyzerClient struct {
	baseURL string
	client  *http.Client
}

func NewAnalyzerClient(baseURL string) *AnalyzerClient {
	return &AnalyzerClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *AnalyzerClient) Parse(ctx context.Context, source string) (ParseResult, error) {
	var result ParseResult
	err := c.post(ctx, "/v1/parse", map[string]string{"source": source}, &result)
	return result, err
}

func (c *AnalyzerClient) SimulateUpdate(
	ctx context.Context,
	document json.RawMessage,
	update json.RawMessage,
	arrayFilters []json.RawMessage,
) (SimulationResult, error) {
	var result SimulationResult
	err := c.post(ctx, "/v1/simulate-update", map[string]any{
		"document":     document,
		"update":       update,
		"arrayFilters": arrayFilters,
	}, &result)
	return result, err
}

func (c *AnalyzerClient) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("analyzer health: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("analyzer health returned %s", response.Status)
	}
	return nil
}

func (c *AnalyzerClient) post(ctx context.Context, path string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("analyzer request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return fmt.Errorf("analyzer returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, MaxResultBytes+1)).Decode(output); err != nil {
		return fmt.Errorf("decode analyzer response: %w", err)
	}
	return nil
}
