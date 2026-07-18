package servicecontrol

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// LoadPrivateEnvironmentFile loads the sensitive half of a service
// environment. Files are JSON objects because Windows SCM has no native
// EnvironmentFile equivalent. Non-sensitive keys are rejected so operators do
// not accidentally create a second, ambiguous configuration source.
func LoadPrivateEnvironmentFile(path string) error {
	values, err := ReadPrivateEnvironmentFile(path)
	if err != nil {
		return err
	}
	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set private service environment %s: %w", key, err)
		}
	}
	return nil
}

// ReadPrivateEnvironmentFile validates and returns a private environment
// without mutating the current process.
func ReadPrivateEnvironmentFile(path string) (map[string]string, error) {
	content, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read private service environment: %w", err)
	}
	values := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode private service environment: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("private service environment must contain exactly one JSON object")
	}
	for key, value := range values {
		if err := validateEnvKey(key); err != nil {
			return nil, err
		}
		if !isSensitiveEnvKey(key) {
			return nil, fmt.Errorf("private service environment contains non-sensitive key %q", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("private service environment %s contains NUL", key)
		}
	}
	return values, nil
}
