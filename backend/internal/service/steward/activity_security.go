package steward

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode"
)

var ErrCredentialDataBlocked = errors.New("credential-like D5 data is blocked before persistence")

func ValidateObservationBeforePersistence(input CreateObservationInput) error {
	level, _ := ClassifyObservationDataLevel(input)
	if level == DataD5 {
		return ErrCredentialDataBlocked
	}
	return nil
}

// ClassifyObservationDataLevel promotes credential-like content to D5 before
// any persistence or disclosure policy is evaluated.
func ClassifyObservationDataLevel(input CreateObservationInput) (string, string) {
	level := strings.ToUpper(strings.TrimSpace(input.DataLevel))
	if level == DataD5 {
		return DataD5, "declared_d5"
	}
	if category, found := detectCredentialData(input.Summary, input.ContextKey); found {
		return DataD5, category
	}
	if category, found := detectCredentialPayload(input.Payload); found {
		return DataD5, category
	}
	if category, found := detectCredentialPayload(input.Metadata); found {
		return DataD5, category
	}
	return level, ""
}

var credentialPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{name: "private_key", pattern: regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{name: "aws_access_key", pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{name: "github_token", pattern: regexp.MustCompile(`\bgh[opusr]_[A-Za-z0-9]{30,}\b`)},
	{name: "openai_style_key", pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{name: "jwt", pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)},
	{name: "cookie_header", pattern: regexp.MustCompile(`(?i)(?:^|[\r\n])\s*(?:set-cookie|cookie)\s*:`)},
	{name: "credential_assignment", pattern: regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret)\b\s*["']?\s*[:=]\s*["']?[^\s"']{8,}`)},
}

func detectCredentialData(values ...string) (string, bool) {
	for _, value := range values {
		for _, candidate := range credentialPatterns {
			if candidate.pattern.MatchString(value) {
				return candidate.name, true
			}
		}
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return unicode.IsSpace(r) || strings.ContainsRune(`"'=:,;()[]{}<>`, r)
		}) {
			if len(token) >= 32 && len(token) <= 512 && tokenEntropy(token) >= 4.35 && hasMixedTokenAlphabet(token) {
				return "high_entropy_secret", true
			}
		}
	}
	return "", false
}

func detectCredentialPayload(payload map[string]any) (string, bool) {
	if len(payload) == 0 {
		return "", false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "invalid_payload", true
	}
	return detectCredentialData(string(encoded))
}

func tokenEntropy(value string) float64 {
	if value == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, r := range value {
		counts[r]++
	}
	length := float64(len([]rune(value)))
	result := 0.0
	for _, count := range counts {
		probability := float64(count) / length
		result -= probability * math.Log2(probability)
	}
	return result
}

func hasMixedTokenAlphabet(value string) bool {
	var lower, upper, digit bool
	for _, r := range value {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return digit && (lower || upper) && (lower != upper || strings.ContainsAny(value, "_-+/="))
}

type StewardValueSignals struct {
	UserUse         float64
	Actionability   float64
	Recurrence      float64
	Uniqueness      float64
	Confidence      float64
	CrossSource     float64
	Recency         float64
	Redundancy      float64
	SensitivityCost float64
}

func CalculateStewardValue(signals StewardValueSignals) float64 {
	value := 0.20*clamp01(signals.UserUse) +
		0.20*clamp01(signals.Actionability) +
		0.15*clamp01(signals.Recurrence) +
		0.15*clamp01(signals.Uniqueness) +
		0.15*clamp01(signals.Confidence) +
		0.10*clamp01(signals.CrossSource) +
		0.05*clamp01(signals.Recency) -
		0.20*clamp01(signals.Redundancy) -
		0.15*clamp01(signals.SensitivityCost)
	return math.Round(clamp01(value)*10000) / 10000
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
