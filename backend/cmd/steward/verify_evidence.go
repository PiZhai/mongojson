package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type verificationEvidenceEnvelope struct {
	Kind      string    `json:"kind"`
	OK        bool      `json:"ok"`
	Command   []string  `json:"command,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Payload   any       `json:"payload"`
}

func printVerificationResult(kind string, evidenceDir string, result any, ok bool) error {
	payload := map[string]any{"verification": result}
	if strings.TrimSpace(evidenceDir) != "" {
		path, err := writeVerificationEvidence(kind, evidenceDir, payload, ok)
		if err != nil {
			return err
		}
		payload["evidence_path"] = path
	}
	return printJSON(payload)
}

func writeVerificationEvidence(kind string, evidenceDir string, payload any, ok bool) (string, error) {
	evidenceDir = strings.TrimSpace(evidenceDir)
	if evidenceDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return "", fmt.Errorf("create evidence dir: %w", err)
	}
	absDir, err := filepath.Abs(evidenceDir)
	if err != nil {
		return "", fmt.Errorf("resolve evidence dir: %w", err)
	}
	now := time.Now().UTC()
	status := "fail"
	if ok {
		status = "pass"
	}
	baseName := fmt.Sprintf("steward-verify-%s-%s", sanitizeEvidenceName(kind), now.Format("20060102T150405.000000000Z"))
	path, err := uniqueEvidencePath(absDir, baseName, status)
	if err != nil {
		return "", err
	}
	envelope := verificationEvidenceEnvelope{
		Kind:      sanitizeEvidenceName(kind),
		OK:        ok,
		Command:   redactedCommandArgs(os.Args),
		CreatedAt: now,
		Payload:   payload,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode evidence: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write evidence: %w", err)
	}
	return path, nil
}

func uniqueEvidencePath(dir string, baseName string, status string) (string, error) {
	path := filepath.Join(dir, baseName+"-"+status+".json")
	for attempt := 2; ; attempt++ {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("check evidence path: %w", err)
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%02d-%s.json", baseName, attempt, status))
	}
}

func sanitizeEvidenceName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "verification"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "._-")
	if name == "" {
		return "verification"
	}
	return name
}

func redactedCommandArgs(args []string) []string {
	redacted := append([]string(nil), args...)
	redactNext := false
	for index, arg := range redacted {
		if redactNext {
			redacted[index] = "<redacted>"
			redactNext = false
			continue
		}
		key, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if isSensitiveCommandKey(key) {
			if hasValue {
				prefix, _, _ := strings.Cut(arg, "=")
				redacted[index] = prefix + "=<redacted>"
			} else {
				redactNext = strings.HasPrefix(arg, "-")
			}
		}
	}
	return redacted
}

func isSensitiveCommandKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	key = strings.NewReplacer("-", "_", ".", "_").Replace(key)
	return strings.Contains(key, "SECRET") ||
		strings.Contains(key, "TOKEN") ||
		strings.Contains(key, "PASSWORD") ||
		strings.Contains(key, "API_KEY") ||
		strings.Contains(key, "PRIVATE_KEY") ||
		(strings.Contains(key, "ENCRYPTION_KEY") && !strings.Contains(key, "ENCRYPTION_KEY_ID"))
}
