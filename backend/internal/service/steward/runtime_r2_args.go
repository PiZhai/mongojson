package steward

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func runtimeRejectUnknownFields(input map[string]any, allowed ...string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = true
	}
	for key := range input {
		if !allowedSet[key] {
			return fmt.Errorf("unsupported input field %s", key)
		}
	}
	return nil
}

func runtimeRequiredString(input map[string]any, key string) (string, error) {
	value, ok := input[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return strings.TrimSpace(text), nil
}

func runtimeOptionalString(input map[string]any, key string) (string, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(text), nil
}

func runtimeBool(input map[string]any, key string, fallback bool) (bool, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err == nil {
			return parsed, nil
		}
	}
	return false, fmt.Errorf("%s must be a boolean", key)
}

func runtimeInt(input map[string]any, key string, fallback int) (int, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, nil
	case float64:
		if typed == float64(int(typed)) {
			return int(typed), nil
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed), nil
		}
	}
	return 0, fmt.Errorf("%s must be an integer", key)
}

func runtimeStringSlice(input map[string]any, key string) ([]string, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return []string{}, nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must contain only strings", key)
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func runtimeOutputMatchesExpected(output map[string]any, expected map[string]any) error {
	for key, expectedValue := range expected {
		actual, exists := output[key]
		if !exists {
			return fmt.Errorf("expected output field %s is missing", key)
		}
		actualJSON, _ := json.Marshal(actual)
		expectedJSON, _ := json.Marshal(expectedValue)
		if string(actualJSON) != string(expectedJSON) {
			return fmt.Errorf("output field %s did not match expected value", key)
		}
	}
	return nil
}
