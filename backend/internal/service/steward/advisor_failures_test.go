package steward

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDescribeAdvisorFailureClassifiesRequestedUnloadedTool(t *testing.T) {
	tests := []struct {
		name  string
		cause error
	}{
		{name: "agent", cause: errors.New(`agent model requested unknown tool "screen__capture"`)},
		{name: "conversation wrapped", cause: fmt.Errorf(`conversation turn failed: %w`, errors.New(`conversation model requested unknown tool "screen__capture"`))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			detail := describeAdvisorFailure(test.cause)
			if detail.Code != "MODEL_TOOL_NOT_LOADED" {
				t.Fatalf("failure code = %q, want MODEL_TOOL_NOT_LOADED: %#v", detail.Code, detail)
			}
			if detail.Retryable {
				t.Fatalf("unregistered tool call must not be blindly retryable: %#v", detail)
			}
			if !strings.Contains(detail.Message, "已拒绝执行") || len(detail.Suggestions) < 2 {
				t.Fatalf("failure details do not explain the safe recovery path: %#v", detail)
			}

			reply := conversationAdvisorFailureReply(test.cause)
			for _, expected := range []string{"模型请求的工具尚未加载", "MODEL_TOOL_NOT_LOADED", "没有执行任何工具"} {
				if !strings.Contains(reply, expected) {
					t.Fatalf("reply missing %q: %s", expected, reply)
				}
			}
		})
	}
}

func TestDescribeAdvisorFailureDoesNotClassifyArbitraryUnknownToolAsUnloaded(t *testing.T) {
	tests := []error{
		errors.New(`unknown tool "screen.capture"`),
		errors.New(`tool screen.capture failed: unknown tool error`),
		errors.New(`model requested unknown tool "screen.capture"`),
	}

	for _, cause := range tests {
		if detail := describeAdvisorFailure(cause); detail.Code == "MODEL_TOOL_NOT_LOADED" {
			t.Errorf("arbitrary error %q was classified as an internally rejected tool call: %#v", cause, detail)
		}
	}
}
