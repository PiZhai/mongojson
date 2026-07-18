package privilegebroker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// validateBrokerInputSchemaDefinition intentionally implements the small JSON
// Schema subset used by Steward tools. Keeping this validator in the Broker
// means a compromised or buggy main service cannot smuggle fields past the
// system-tool contract.
func validateBrokerInputSchemaDefinition(schema map[string]any) error {
	return validateBrokerSchemaNode(schema, "$", true)
}

func validateBrokerSchemaNode(schema map[string]any, path string, root bool) error {
	kind, _ := schema["type"].(string)
	if root && kind != "object" {
		return fmt.Errorf("root type must be object")
	}
	switch kind {
	case "object":
		if properties, ok := schema["properties"]; ok {
			items, ok := properties.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties must be an object", path)
			}
			for name, raw := range items {
				child, ok := raw.(map[string]any)
				if !ok {
					return fmt.Errorf("%s.properties.%s must be an object", path, name)
				}
				if err := validateBrokerSchemaNode(child, path+"."+name, false); err != nil {
					return err
				}
			}
		}
		if required, ok := schema["required"]; ok {
			if _, err := brokerSchemaStrings(required); err != nil {
				return fmt.Errorf("%s.required: %w", path, err)
			}
		}
	case "array":
		if raw, ok := schema["items"]; ok {
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.items must be an object", path)
			}
			if err := validateBrokerSchemaNode(child, path+"[]", false); err != nil {
				return err
			}
		}
	case "string", "integer", "number", "boolean", "null", "":
	default:
		return fmt.Errorf("%s has unsupported type %q", path, kind)
	}
	return nil
}

func validateBrokerInput(schema map[string]any, input map[string]any) error {
	if schema == nil {
		if len(input) != 0 {
			return fmt.Errorf("capability does not accept dynamic input")
		}
		return nil
	}
	return validateBrokerValue(schema, input, "$", true)
}

func validateBrokerValue(schema map[string]any, value any, path string, root bool) error {
	kind, _ := schema["type"].(string)
	switch kind {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := schema["properties"].(map[string]any)
		required, _ := brokerSchemaStrings(schema["required"])
		for _, name := range required {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		additional, hasAdditional := schema["additionalProperties"].(bool)
		for name, item := range object {
			raw, exists := properties[name]
			if !exists {
				if hasAdditional && !additional {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
				continue
			}
			child, _ := raw.(map[string]any)
			if err := validateBrokerValue(child, item, path+"."+name, false); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		if child, ok := schema["items"].(map[string]any); ok {
			for index, item := range items {
				if err := validateBrokerValue(child, item, fmt.Sprintf("%s[%d]", path, index), false); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("%s must be an integer", path)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
	case "":
		// An empty schema intentionally accepts any JSON value. Steward uses it
		// for registry values where the native type is selected separately.
	default:
		return fmt.Errorf("%s has unsupported schema type %q", path, kind)
	}
	_ = root
	return nil
}

func brokerSchemaStrings(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return append([]string(nil), typed...), nil
		}
		return nil, fmt.Errorf("must be an array of strings")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("must contain non-empty strings")
		}
		result = append(result, text)
	}
	return result, nil
}

func brokerInputDigest(input map[string]any) (string, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
