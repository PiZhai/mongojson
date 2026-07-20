package steward

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
)

var ErrCredentialDataBlocked = errors.New("credential plaintext is blocked before persistence")

const redactedCredentialValue = "[REDACTED:CREDENTIAL]"

// ObservationSecretRedaction describes deterministic, local redaction applied
// before an activity envelope is queued or persisted. Data-level labels are
// intentionally absent: D0-D6 remain readable compatibility metadata, not a
// collection decision.
type ObservationSecretRedaction struct {
	Redacted   bool     `json:"redacted"`
	Count      int      `json:"count"`
	Categories []string `json:"categories,omitempty"`
}

func ValidateObservationBeforePersistence(input CreateObservationInput) error {
	if _, found := observationCredentialCategory(input); found {
		return ErrCredentialDataBlocked
	}
	return nil
}

// SanitizeObservationSecrets removes credential plaintext at field level while
// preserving the activity record and all unrelated fields. It deliberately
// does not treat arbitrary high-entropy identifiers as credentials: window
// titles commonly contain hashes, UUIDs and build IDs that are useful activity
// evidence and must not cause the whole observation to disappear.
func SanitizeObservationSecrets(input CreateObservationInput) (CreateObservationInput, ObservationSecretRedaction) {
	tracker := newSecretRedactionTracker()
	input.Source = redactObservationCredentialText(input.Source, tracker)
	input.Type = redactObservationCredentialText(input.Type, tracker)
	input.Summary = redactObservationCredentialText(input.Summary, tracker)
	input.SourceEventKey = redactObservationCredentialText(input.SourceEventKey, tracker)
	input.InteractiveSessionID = redactObservationCredentialText(input.InteractiveSessionID, tracker)
	input.SourceTimezone = redactObservationCredentialText(input.SourceTimezone, tracker)
	input.ContextKey = redactObservationCredentialText(input.ContextKey, tracker)
	input.Fingerprint = redactObservationCredentialText(input.Fingerprint, tracker)
	input.Payload = redactCredentialMap(input.Payload, tracker)
	input.Metadata = redactCredentialMap(input.Metadata, tracker)
	for index := range input.EntityHints {
		hint := &input.EntityHints[index]
		hint.Type = redactObservationCredentialText(hint.Type, tracker)
		hint.CanonicalKey = redactObservationCredentialText(hint.CanonicalKey, tracker)
		hint.DisplayName = redactObservationCredentialText(hint.DisplayName, tracker)
		hint.Summary = redactObservationCredentialText(hint.Summary, tracker)
		hint.RelationType = redactObservationCredentialText(hint.RelationType, tracker)
		hint.TargetType = redactObservationCredentialText(hint.TargetType, tracker)
		hint.TargetCanonicalKey = redactObservationCredentialText(hint.TargetCanonicalKey, tracker)
		hint.TargetDisplayName = redactObservationCredentialText(hint.TargetDisplayName, tracker)
	}
	if input.Blob != nil && isTextObservationBlob(input.Blob.MIMEType) {
		encoded := strings.TrimSpace(input.Blob.DataBase64)
		if encoded != "" {
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				input.Blob.DataBase64 = ""
				tracker.add("invalid_text_blob")
			} else {
				redacted := redactObservationCredentialText(string(decoded), tracker)
				input.Blob.DataBase64 = base64.StdEncoding.EncodeToString([]byte(redacted))
			}
		}
	}
	result := tracker.result()
	if result.Redacted {
		if input.Metadata == nil {
			input.Metadata = map[string]any{}
		}
		input.Metadata["secret_redacted"] = true
		input.Metadata["secret_redaction_count"] = result.Count
		input.Metadata["secret_redaction_categories"] = append([]string(nil), result.Categories...)
	}
	return input, result
}

// ClassifyObservationDataLevel promotes credential-like content to D5 before
// any disclosure policy is evaluated. Activity ingestion sanitizes first and
// never uses the resulting legacy label as a collection gate.
func ClassifyObservationDataLevel(input CreateObservationInput) (string, string) {
	level := strings.ToUpper(strings.TrimSpace(input.DataLevel))
	if level == DataD5 {
		return DataD5, "declared_d5"
	}
	if category, found := observationCredentialCategory(input); found {
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
	{name: "authorization_header", pattern: regexp.MustCompile(`(?im)\b(?:proxy-)?authorization\s*:[^\r\n]*`)},
	{name: "cookie_header", pattern: regexp.MustCompile(`(?im)\b(?:set-cookie|cookie)\s*:[^\r\n]*`)},
	{name: "bearer_token", pattern: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}={0,2}`)},
	{name: "credential_assignment", pattern: regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|api[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret)\b\s*["']?\s*[:=]\s*["']?[^\s"']{8,}`)},
}

var sensitiveCredentialFieldNames = map[string]bool{
	"password": true, "passwd": true, "pwd": true,
	"apikey": true, "accesskey": true, "secretkey": true,
	"accesstoken": true, "refreshtoken": true, "idtoken": true,
	"clientsecret": true, "appsecret": true, "privatekey": true,
	"authorization": true, "proxyauthorization": true,
	"cookie": true, "setcookie": true, "credential": true, "credentials": true,
}

var nonCredentialFieldNameCharacter = regexp.MustCompile(`[^a-z0-9]+`)

func detectCredentialData(values ...string) (string, bool) {
	for _, value := range values {
		for _, candidate := range credentialPatterns {
			if candidate.pattern.MatchString(value) {
				return candidate.name, true
			}
		}
	}
	return "", false
}

func detectCredentialPayload(payload map[string]any) (string, bool) {
	return detectCredentialValue(payload)
}

func observationCredentialCategory(input CreateObservationInput) (string, bool) {
	if category, found := detectCredentialData(
		input.Source, input.Type, input.Summary, input.SourceEventKey,
		input.InteractiveSessionID, input.SourceTimezone, input.ContextKey, input.Fingerprint,
	); found {
		return category, true
	}
	if category, found := detectCredentialPayload(input.Payload); found {
		return category, true
	}
	if category, found := detectCredentialPayload(input.Metadata); found {
		return category, true
	}
	for _, hint := range input.EntityHints {
		if category, found := detectCredentialData(
			hint.Type, hint.CanonicalKey, hint.DisplayName, hint.Summary,
			hint.RelationType, hint.TargetType, hint.TargetCanonicalKey, hint.TargetDisplayName,
		); found {
			return category, true
		}
	}
	if input.Blob != nil && isTextObservationBlob(input.Blob.MIMEType) && strings.TrimSpace(input.Blob.DataBase64) != "" {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input.Blob.DataBase64))
		if err != nil {
			return "invalid_text_blob", true
		}
		if category, found := detectCredentialData(string(decoded)); found {
			return category, true
		}
	}
	return "", false
}

func detectCredentialValue(value any) (string, bool) {
	return detectCredentialValueDepth(value, 0)
}

func detectCredentialValueDepth(value any, depth int) (string, bool) {
	if depth > 32 {
		return "invalid_payload", true
	}
	switch item := value.(type) {
	case nil:
		return "", false
	case string:
		return detectCredentialData(item)
	case bool, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return "", false
	case map[string]any:
		for key, nested := range item {
			if isSensitiveCredentialField(key) && !isRedactedCredentialValue(nested) {
				return "sensitive_field", true
			}
			if category, found := detectCredentialValueDepth(nested, depth+1); found {
				return category, true
			}
		}
		return "", false
	case []any:
		for _, nested := range item {
			if category, found := detectCredentialValueDepth(nested, depth+1); found {
				return category, true
			}
		}
		return "", false
	default:
		encoded, err := json.Marshal(item)
		if err != nil {
			return "invalid_payload", true
		}
		var generic any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			return "invalid_payload", true
		}
		return detectCredentialValueDepth(generic, depth+1)
	}
}

type secretRedactionTracker struct {
	count      int
	categories map[string]bool
}

func newSecretRedactionTracker() *secretRedactionTracker {
	return &secretRedactionTracker{categories: map[string]bool{}}
}

func (t *secretRedactionTracker) add(category string) {
	t.count++
	t.categories[category] = true
}

func (t *secretRedactionTracker) result() ObservationSecretRedaction {
	categories := make([]string, 0, len(t.categories))
	for category := range t.categories {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return ObservationSecretRedaction{Redacted: t.count > 0, Count: t.count, Categories: categories}
}

func redactObservationCredentialText(value string, tracker *secretRedactionTracker) string {
	result := value
	for _, candidate := range credentialPatterns {
		result = candidate.pattern.ReplaceAllStringFunc(result, func(_ string) string {
			tracker.add(candidate.name)
			return "[REDACTED:" + strings.ToUpper(candidate.name) + "]"
		})
	}
	return result
}

func redactCredentialMap(input map[string]any, tracker *secretRedactionTracker) map[string]any {
	return redactCredentialMapDepth(input, tracker, 0)
}

func redactCredentialMapDepth(input map[string]any, tracker *secretRedactionTracker, depth int) map[string]any {
	if input == nil {
		return nil
	}
	if depth > 32 {
		tracker.add("invalid_payload")
		return map[string]any{"_redacted": "[REDACTED:INVALID_PAYLOAD]"}
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		if isSensitiveCredentialField(key) && !isRedactedCredentialValue(value) {
			result[key] = redactedCredentialValue
			tracker.add("sensitive_field")
			continue
		}
		result[key] = redactCredentialValueDepth(value, tracker, depth+1)
	}
	return result
}

func redactCredentialValue(value any, tracker *secretRedactionTracker) any {
	return redactCredentialValueDepth(value, tracker, 0)
}

func redactCredentialValueDepth(value any, tracker *secretRedactionTracker, depth int) any {
	if depth > 32 {
		tracker.add("invalid_payload")
		return "[REDACTED:INVALID_PAYLOAD]"
	}
	switch item := value.(type) {
	case nil:
		return nil
	case string:
		return redactObservationCredentialText(item, tracker)
	case bool, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return item
	case map[string]any:
		return redactCredentialMapDepth(item, tracker, depth+1)
	case []any:
		result := make([]any, len(item))
		for index := range item {
			result[index] = redactCredentialValueDepth(item[index], tracker, depth+1)
		}
		return result
	case []string:
		result := make([]string, len(item))
		for index := range item {
			result[index] = redactObservationCredentialText(item[index], tracker)
		}
		return result
	default:
		encoded, err := json.Marshal(item)
		if err != nil {
			tracker.add("invalid_payload")
			return "[REDACTED:INVALID_PAYLOAD]"
		}
		var generic any
		if err := json.Unmarshal(encoded, &generic); err != nil {
			tracker.add("invalid_payload")
			return "[REDACTED:INVALID_PAYLOAD]"
		}
		return redactCredentialValueDepth(generic, tracker, depth+1)
	}
}

func isSensitiveCredentialField(value string) bool {
	normalized := strings.ToLower(nonCredentialFieldNameCharacter.ReplaceAllString(value, ""))
	return sensitiveCredentialFieldNames[normalized]
}

func isRedactedCredentialValue(value any) bool {
	text, ok := value.(string)
	return ok && strings.HasPrefix(strings.TrimSpace(text), "[REDACTED:")
}

func isTextObservationBlob(mimeType string) bool {
	value := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	return strings.HasPrefix(value, "text/") || value == "application/json" || value == "application/xml" || strings.HasSuffix(value, "+json") || strings.HasSuffix(value, "+xml")
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
