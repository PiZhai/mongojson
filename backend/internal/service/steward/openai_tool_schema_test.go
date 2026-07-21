package steward

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestNormalizeOpenAIToolParametersOmitsNilRequired(t *testing.T) {
	parameters, err := normalizeOpenAIToolParameters(map[string]any{
		"type": "object", "properties": map[string]any{}, "required": []string(nil), "additionalProperties": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := parameters["required"]; exists {
		t.Fatalf("nil required must be omitted, got %#v", parameters["required"])
	}
}

func TestNormalizeOpenAIToolParametersRejectsMissingRequiredProperty(t *testing.T) {
	_, err := normalizeOpenAIToolParameters(map[string]any{
		"type": "object", "properties": map[string]any{}, "required": []string{"path"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing property") {
		t.Fatalf("expected missing required property error, got %v", err)
	}
}

func TestOpenAICompatibleAgentTurnSendsProviderCompatibleOptionalTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Tools []struct {
				Function struct {
					Name       string         `json:"name"`
					Parameters map[string]any `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Tools) < 1 || payload.Tools[0].Function.Name != "fs__get_known_folders" {
			t.Fatalf("unexpected tools: %#v", payload.Tools)
		}
		if _, exists := payload.Tools[0].Function.Parameters["required"]; exists {
			t.Fatalf("provider request still contains required: %#v", payload.Tools[0].Function.Parameters)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "协议可用"}}},
		})
	}))
	defer server.Close()

	advisor := openAICompatibleAutonomyAdvisor{client: server.Client(), baseURL: server.URL, model: "test-model"}
	decision, err := advisor.NextTurn(context.Background(), AgentTurnInput{
		Message: "probe", DataLevel: DataD0,
		Tools: []domain.StewardToolSpec{{
			Name: "fs.get_known_folders", Description: "known folders",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "required": []string(nil), "additionalProperties": false},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Content != "协议可用" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestDescribeAdvisorFailureProvidesActionableToolSchemaGuidance(t *testing.T) {
	cause := newAdvisorHTTPError("advisor request", "400 Bad Request", http.StatusBadRequest, []byte(`{"error":{"message":"Invalid schema for function 'fs__get_known_folders': null is not of type array","type":"invalid_request_error"}}`))
	detail := describeAdvisorFailure(cause)
	if detail.Code != "MODEL_TOOL_SCHEMA_INVALID" || detail.Retryable || len(detail.Suggestions) < 2 {
		t.Fatalf("unexpected failure details: %#v", detail)
	}
	reply := conversationAdvisorFailureReply(cause)
	for _, expected := range []string{"工具调用协议配置错误", "处理建议", "MODEL_TOOL_SCHEMA_INVALID", "没有执行任何工具"} {
		if !strings.Contains(reply, expected) {
			t.Fatalf("reply missing %q: %s", expected, reply)
		}
	}
}

func TestDescribeAdvisorFailureClassifiesCommonProviderErrors(t *testing.T) {
	tests := []struct {
		status int
		body   string
		code   string
	}{
		{http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`, "MODEL_AUTHENTICATION_FAILED"},
		{http.StatusNotFound, `{"error":{"message":"model not found"}}`, "MODEL_NOT_FOUND"},
		{http.StatusTooManyRequests, `{"error":{"message":"rate limit exceeded"}}`, "MODEL_RATE_LIMITED"},
		{http.StatusServiceUnavailable, `{"error":{"message":"temporarily unavailable"}}`, "MODEL_PROVIDER_UNAVAILABLE"},
	}
	for _, test := range tests {
		cause := newAdvisorHTTPError("advisor request", http.StatusText(test.status), test.status, []byte(test.body))
		if detail := describeAdvisorFailure(cause); detail.Code != test.code {
			t.Errorf("status %d classified as %s, want %s", test.status, detail.Code, test.code)
		}
	}
}
