package steward

import (
	"fmt"
	"strings"
)

// normalizeOpenAIToolParameters converts the tool catalog's JSON Schema into
// the stricter subset accepted by OpenAI-compatible function calling APIs.
// In particular, Go nil slices must not leak out as JSON null schema keywords.
func normalizeOpenAIToolParameters(schema map[string]any) (map[string]any, error) {
	cloned := cloneStringAnyMap(schema)
	normalized, err := normalizeOpenAIJSONSchemaNode(cloned, "parameters")
	if err != nil {
		return nil, err
	}
	result, ok := normalized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("parameters must be a JSON object")
	}
	if schemaType, _ := result["type"].(string); schemaType != "object" {
		return nil, fmt.Errorf("parameters.type must be object")
	}
	if _, ok := result["properties"]; !ok {
		result["properties"] = map[string]any{}
	}
	return result, nil
}

func normalizeOpenAIJSONSchemaNode(value any, path string) (any, error) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if child == nil {
				delete(node, key)
				continue
			}
			normalized, err := normalizeOpenAIJSONSchemaNode(child, path+"."+key)
			if err != nil {
				return nil, err
			}
			node[key] = normalized
		}

		if raw, exists := node["required"]; exists {
			required, err := openAIRequiredNames(raw, path+".required")
			if err != nil {
				return nil, err
			}
			if len(required) == 0 {
				delete(node, "required")
			} else {
				properties, ok := node["properties"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("%s.properties must be an object when required is present", path)
				}
				for _, name := range required {
					if _, ok := properties[name]; !ok {
						return nil, fmt.Errorf("%s.required references missing property %q", path, name)
					}
				}
				node["required"] = required
			}
		}

		if schemaType, _ := node["type"].(string); schemaType == "object" {
			if _, exists := node["properties"]; !exists {
				node["properties"] = map[string]any{}
			} else if _, ok := node["properties"].(map[string]any); !ok {
				return nil, fmt.Errorf("%s.properties must be an object", path)
			}
		}
		if schemaType, _ := node["type"].(string); schemaType == "array" {
			if _, exists := node["items"]; !exists {
				// An empty schema accepts any item and is valid with providers that
				// require the items keyword to be present for array parameters.
				node["items"] = map[string]any{}
			}
		}
		return node, nil
	case []any:
		for index, child := range node {
			if child == nil {
				return nil, fmt.Errorf("%s[%d] must not be null", path, index)
			}
			normalized, err := normalizeOpenAIJSONSchemaNode(child, fmt.Sprintf("%s[%d]", path, index))
			if err != nil {
				return nil, err
			}
			node[index] = normalized
		}
		return node, nil
	default:
		return value, nil
	}
}

func openAIRequiredNames(value any, path string) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for index, item := range items {
		name, ok := item.(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("%s[%d] must be a non-empty string", path, index)
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}
