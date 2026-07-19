package steward

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestBuildAgentTranscriptRetainsFirstRoundAnchorAfterTwoHundredFortyTurns(t *testing.T) {
	const anchor = `C:\Users\Alice\Desktop\Workspace\anchor.txt`
	turns := make([]domain.StewardAgentTurn, 0, 240)
	for round := 1; round <= 240; round++ {
		callID := fmt.Sprintf("call_%03d", round)
		path := fmt.Sprintf(`C:\scratch\round-%03d`, round)
		toolName := "fs.list"
		output := map[string]any{"path": path, "count": round}
		if round == 1 {
			toolName = "screen.capture"
			output = map[string]any{"path": anchor, "width": 2560, "height": 1440}
		}
		completed := time.Unix(int64(round), 0).UTC()
		turns = append(turns, domain.StewardAgentTurn{
			ID: fmt.Sprintf("turn_%03d", round), EpisodeID: "episode-long", RoundIndex: round,
			Status: "tools_complete", AssistantContent: fmt.Sprintf("round %d", round),
			ToolCalls:   []domain.StewardAgentToolCall{{ID: callID, ToolName: toolName, Arguments: map[string]any{"path": path}}},
			ToolResults: []domain.StewardAgentToolResult{{ToolCallID: callID, ToolName: toolName, Output: output}},
			CreatedAt:   completed, UpdatedAt: completed, CompletedAt: &completed,
		})
	}

	transcript := buildAgentTranscript(turns)
	if len(transcript) != agentTranscriptRecentTurnLimit+1 {
		t.Fatalf("long transcript is not bounded to summary + recent turns: got=%d", len(transcript))
	}
	if !strings.Contains(transcript[0].AssistantContent, anchor) {
		t.Fatalf("first-round durable path disappeared after 240 turns: %s", transcript[0].AssistantContent)
	}
	if len(transcript[0].ToolCalls) != 0 || len(transcript[0].ToolResults) != 0 {
		t.Fatalf("old summary emitted orphan tool protocol messages: %+v", transcript[0])
	}
	last := transcript[len(transcript)-1]
	if len(last.ToolCalls) != 1 || last.ToolCalls[0].ID != "call_240" || len(last.ToolResults) != 1 || last.ToolResults[0].ToolCallID != "call_240" {
		t.Fatalf("most recent native tool call/result pair was not retained: %+v", last)
	}
	encoded, err := json.Marshal(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > agentTranscriptSummaryBudget+agentTranscriptRecentBudget+16_000 {
		t.Fatalf("240-turn transcript exceeded its bounded context budget: bytes=%d", len(encoded))
	}
}
