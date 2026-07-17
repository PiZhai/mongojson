package steward

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestObservationModelOutputAcceptsStringOrArrayLists(t *testing.T) {
	var output ObservationModelOutput
	if err := json.Unmarshal([]byte(`{"summary":"今日归纳","insights":"单条洞察","suggested_actions":["提醒一次"," "]}`), &output); err != nil {
		t.Fatalf("unmarshal model output: %v", err)
	}
	if output.Summary != "今日归纳" {
		t.Fatalf("unexpected summary: %q", output.Summary)
	}
	if !reflect.DeepEqual(output.Insights, []string{"单条洞察"}) {
		t.Fatalf("unexpected insights: %#v", output.Insights)
	}
	if !reflect.DeepEqual(output.SuggestedActions, []string{"提醒一次"}) {
		t.Fatalf("unexpected suggested actions: %#v", output.SuggestedActions)
	}
}
