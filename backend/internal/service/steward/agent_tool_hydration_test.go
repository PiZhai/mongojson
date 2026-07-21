package steward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestNextTurnHydratesCatalogToolBeforeExecution(t *testing.T) {
	t.Setenv("STEWARD_OWNER_MODE", "false")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"id":"response_next_turn",
			"choices":[{"message":{"content":null,"tool_calls":[{
				"id":"call_capture","type":"function","function":{"name":"screen.capture","arguments":"{}"}
			}]}}]
		}`))
	}))
	defer server.Close()

	advisor := openAICompatibleAutonomyAdvisor{
		client: server.Client(), baseURL: server.URL, model: "test-model",
	}
	decision, err := advisor.NextTurn(context.Background(), AgentTurnInput{
		Message: "截取屏幕", DataLevel: DataD0,
		Tools: []domain.StewardToolSpec{{
			Name: "tool.describe", Description: "Describe a catalog tool.",
			InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}},
				"required": []string{"name"}, "additionalProperties": false,
			},
		}},
		ToolCatalog: []AgentToolCatalogEntry{{Name: "screen.capture"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.ToolCalls) != 1 || decision.ToolCalls[0].ToolName != "tool.describe" || decision.ToolCalls[0].Arguments["name"] != "screen.capture" {
		t.Fatalf("NextTurn did not auto-hydrate the catalog tool: %+v", decision)
	}
}

func TestParseOpenAIAgentTurnHydratesCatalogToolBeforeExecution(t *testing.T) {
	decision, err := parseOpenAIAgentTurnWithCatalog([]byte(`{
		"id":"response_1",
		"choices":[{"message":{"content":"截图已完成","reasoning_content":"capture the screen","tool_calls":[{
			"id":"call_capture","type":"function","function":{"name":"screen.capture","arguments":"{\"monitor\":1}"}
		}]}}]
	}`), map[string]domain.StewardToolSpec{
		"tool__describe": {Name: "tool.describe"},
	}, []AgentToolCatalogEntry{{Name: "screen.capture"}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.ProviderResponseID != "response_1" || decision.ReasoningContent != "capture the screen" {
		t.Fatalf("provider response metadata was not retained: %+v", decision)
	}
	if decision.Content != agentToolHydrationProgress {
		t.Fatalf("synthetic hydration retained unsafe model content: %q", decision.Content)
	}
	if len(decision.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want one hydration call", decision.ToolCalls)
	}
	call := decision.ToolCalls[0]
	if call.ID != "call_capture" || call.ToolName != "tool.describe" || call.Arguments["name"] != "screen.capture" {
		t.Fatalf("unexpected hydration call: %+v", call)
	}
}

func TestParseOpenAIAgentTurnHydratesUniqueCatalogAlias(t *testing.T) {
	decision, err := parseOpenAIAgentTurnWithCatalog([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[{
			"id":"call_capture","type":"function","function":{"name":"screen__capture","arguments":"{}"}
		}]}}]
	}`), map[string]domain.StewardToolSpec{
		"tool__describe": {Name: "tool.describe"},
	}, []AgentToolCatalogEntry{{Name: "screen.capture"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.ToolCalls) != 1 || decision.ToolCalls[0].ToolName != "tool.describe" || decision.ToolCalls[0].Arguments["name"] != "screen.capture" {
		t.Fatalf("unique catalog alias was not hydrated to its canonical name: %+v", decision)
	}
}

func TestParseOpenAIAgentTurnRejectsAmbiguousCatalogAlias(t *testing.T) {
	truncatedAlias := strings.Repeat("a", 56)
	tests := []struct {
		name        string
		alias       string
		toolCatalog []AgentToolCatalogEntry
	}{
		{
			name: "punctuation collision", alias: "screen__capture",
			toolCatalog: []AgentToolCatalogEntry{{Name: "screen.capture"}, {Name: "screen/capture"}},
		},
		{
			name: "truncation collision", alias: truncatedAlias,
			toolCatalog: []AgentToolCatalogEntry{{Name: truncatedAlias + "_first"}, {Name: truncatedAlias + "_second"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_ambiguous","type":"function","function":{"name":"` + test.alias + `","arguments":"{}"}}]}}]}`)
			decision, err := parseOpenAIAgentTurnWithCatalog(payload, map[string]domain.StewardToolSpec{
				"tool__describe": {Name: "tool.describe"},
			}, test.toolCatalog)
			if err == nil || !strings.Contains(err.Error(), "agent model requested unknown tool") {
				t.Fatalf("ambiguous catalog alias error = %v", err)
			}
			if len(decision.ToolCalls) != 0 {
				t.Fatalf("ambiguous catalog alias produced calls: %+v", decision.ToolCalls)
			}
		})
	}
}

func TestParseOpenAIAgentTurnRequiresExposedToolDescribeForHydration(t *testing.T) {
	decision, err := parseOpenAIAgentTurnWithCatalog([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[{
			"id":"call_capture","type":"function","function":{"name":"screen.capture","arguments":"{}"}
		}]}}]
	}`), map[string]domain.StewardToolSpec{
		"runtime__echo": {Name: "runtime.echo"},
	}, []AgentToolCatalogEntry{{Name: "screen.capture"}})
	if err == nil || !strings.Contains(err.Error(), "tool.describe is not available in this turn") {
		t.Fatalf("missing tool.describe error = %v", err)
	}
	if len(decision.ToolCalls) != 0 {
		t.Fatalf("missing tool.describe produced calls: %+v", decision.ToolCalls)
	}
}

func TestParseOpenAIAgentTurnAcceptsCanonicalNameAfterHydration(t *testing.T) {
	decision, err := parseOpenAIAgentTurn([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[{
			"id":"call_capture","type":"function","function":{"name":"screen.capture","arguments":"{\"monitor\":1,\"_target_device_id\":\"device-1\"}"}
		}]}}]
	}`), map[string]domain.StewardToolSpec{
		"screen__capture": {Name: "screen.capture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want hydrated tool call", decision.ToolCalls)
	}
	call := decision.ToolCalls[0]
	if call.ToolName != "screen.capture" || call.TargetDeviceID != "device-1" || call.Arguments["monitor"] != float64(1) {
		t.Fatalf("canonical hydrated tool call was not resolved: %+v", call)
	}
	if _, leaked := call.Arguments["_target_device_id"]; leaked {
		t.Fatalf("target device leaked into tool arguments: %+v", call.Arguments)
	}
}

func TestParseOpenAIAgentTurnRejectsToolOutsideCatalog(t *testing.T) {
	_, err := parseOpenAIAgentTurnWithCatalog([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[{
			"id":"call_unknown","type":"function","function":{"name":"screen.delete_everything","arguments":"{}"}
		}]}}]
	}`), nil, []AgentToolCatalogEntry{{Name: "screen.capture"}})
	if err == nil || !strings.Contains(err.Error(), `agent model requested unknown tool "screen.delete_everything"`) {
		t.Fatalf("unknown tool error = %v", err)
	}
}

func TestParseOpenAIAgentTurnHydrationDefersLoadedCallsAtomically(t *testing.T) {
	decision, err := parseOpenAIAgentTurnWithCatalog([]byte(`{
		"choices":[{"message":{"content":null,"tool_calls":[
			{"id":"call_write","type":"function","function":{"name":"fs__write_text","arguments":"{\"path\":\"desktop/result.txt\",\"content\":\"written too early\"}"}},
			{"id":"call_capture","type":"function","function":{"name":"screen.capture","arguments":"{}"}}
		]}}]
	}`), map[string]domain.StewardToolSpec{
		"fs__write_text": {Name: "fs.write_text"},
		"tool__describe": {Name: "tool.describe"},
	}, []AgentToolCatalogEntry{{Name: "fs.write_text"}, {Name: "screen.capture"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.ToolCalls) != 1 {
		t.Fatalf("mixed turn returned executable calls: %+v", decision.ToolCalls)
	}
	call := decision.ToolCalls[0]
	if call.ID != "call_capture" || call.ToolName != "tool.describe" || call.Arguments["name"] != "screen.capture" {
		t.Fatalf("mixed turn was not reduced to hydration only: %+v", call)
	}
}
