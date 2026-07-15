package steward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

type presidioFinding struct {
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
	EntityType string  `json:"entity_type"`
}

func (s *Service) applyPresidioProtection(ctx context.Context, input CreateObservationInput) (CreateObservationInput, bool, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("STEWARD_PRESIDIO_URL")), "/")
	if endpoint == "" || dataLevelRank(input.DataLevel) < dataLevelRank(DataD3) {
		return input, false, nil
	}
	if err := validateLocalAdapterEndpoint(endpoint); err != nil {
		return input, false, fmt.Errorf("invalid Presidio endpoint: %w", err)
	}
	minimumScore := 0.65
	if value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("STEWARD_PRESIDIO_MIN_SCORE")), 64); err == nil && value > 0 && value <= 1 {
		minimumScore = value
	}
	summaryFindings, err := presidioAnalyze(ctx, endpoint, input.Summary, minimumScore)
	if err != nil {
		return input, false, err
	}
	contextFindings, err := presidioAnalyze(ctx, endpoint, input.ContextKey, minimumScore)
	if err != nil {
		return input, false, err
	}
	payloadText, _ := json.Marshal(input.Payload)
	payloadFindings, err := presidioAnalyze(ctx, endpoint, string(payloadText), minimumScore)
	if err != nil {
		return input, false, err
	}
	input.Summary = redactPresidioFindings(input.Summary, summaryFindings)
	input.ContextKey = redactPresidioFindings(input.ContextKey, contextFindings)
	if len(summaryFindings)+len(contextFindings)+len(payloadFindings) > 0 {
		if input.Metadata == nil {
			input.Metadata = map[string]any{}
		}
		input.Metadata["redacted"] = true
		return input, true, nil
	}
	return input, false, nil
}

func presidioAnalyze(ctx context.Context, endpoint, text string, minimumScore float64) ([]presidioFinding, error) {
	if strings.TrimSpace(text) == "" || text == "null" || text == "{}" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"text": text, "language": defaultString(strings.TrimSpace(os.Getenv("STEWARD_PRESIDIO_LANGUAGE")), "en"),
		"score_threshold": minimumScore,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := activityAdapterHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Presidio unavailable; D3/D4 observation rejected: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Presidio returned %s; D3/D4 observation rejected", response.Status)
	}
	findings := []presidioFinding{}
	if err := json.NewDecoder(response.Body).Decode(&findings); err != nil {
		return nil, fmt.Errorf("decode Presidio response: %w", err)
	}
	filtered := findings[:0]
	for _, finding := range findings {
		if finding.Score >= minimumScore && finding.Start >= 0 && finding.End > finding.Start {
			filtered = append(filtered, finding)
		}
	}
	return filtered, nil
}

func redactPresidioFindings(text string, findings []presidioFinding) string {
	runes := []rune(text)
	sort.Slice(findings, func(left, right int) bool { return findings[left].Start > findings[right].Start })
	lastStart := len(runes)
	for _, finding := range findings {
		if finding.Start < 0 || finding.End > len(runes) || finding.End > lastStart {
			continue
		}
		replacement := []rune("[REDACTED:" + strings.ToUpper(finding.EntityType) + "]")
		runes = append(append(append([]rune{}, runes[:finding.Start]...), replacement...), runes[finding.End:]...)
		lastStart = finding.Start
	}
	return string(runes)
}
