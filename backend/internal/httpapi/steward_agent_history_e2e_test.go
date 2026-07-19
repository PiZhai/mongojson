package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func TestStewardAgentHistoryOverviewAndCursorPagination(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres-backed Agent history pagination test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	node := newStewardHTTPNode(t, ctx, temporaryPostgresDatabaseConfig(t, ctx, baseDSN, "agent_history"), "agent-history")

	conversationID := uuid.NewString()
	messageID := uuid.NewString()
	episodeID := uuid.NewString()
	if _, err := node.pool.Exec(ctx, `insert into steward_conversations(id,title,status,data_level) values($1,'history','active','D0')`, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := node.pool.Exec(ctx, `insert into steward_conversation_messages(id,conversation_id,role,content,data_level) values($1,$2,'user','long task','D0')`, messageID, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := node.pool.Exec(ctx, `insert into steward_agent_episodes(
		id,conversation_id,trigger_message_id,goal,status,current_round,max_rounds,max_tool_calls,max_duration_seconds
	) values($1,$2,$3,'long task','thinking',205,0,0,0)`, episodeID, conversationID, messageID); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 205; round++ {
		path := fmt.Sprintf(`C:\workspace\round-%03d.txt`, round)
		if round == 1 {
			path = `C:\workspace\first-critical-path.txt`
		}
		calls, _ := json.Marshal([]domain.StewardAgentToolCall{{ID: fmt.Sprintf("call-%03d", round), ToolName: "fs.read_text", Arguments: map[string]any{"path": path}}})
		results, _ := json.Marshal([]domain.StewardAgentToolResult{{ToolCallID: fmt.Sprintf("call-%03d", round), ToolName: "fs.read_text", Output: map[string]any{"path": path}, Evidence: map[string]any{"evidence_id": fmt.Sprintf("evidence-%03d", round)}}})
		if _, err := node.pool.Exec(ctx, `insert into steward_agent_turns(
			id,episode_id,round_index,status,assistant_content,tool_calls,tool_results
		) values($1,$2,$3,'tools_complete',$4,$5::jsonb,$6::jsonb)`, uuid.NewString(), episodeID, round, fmt.Sprintf("round %d", round), string(calls), string(results)); err != nil {
			t.Fatal(err)
		}
	}

	state, through, err := node.service.RefreshAgentEpisodeWorkingState(ctx, episodeID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if through != 185 {
		t.Fatalf("summary through = %d, want 185", through)
	}
	if !strings.Contains(state.Summary, "first-critical-path.txt") {
		t.Fatalf("working state lost early path: %s", state.Summary)
	}
	loopEpisode, err := node.service.GetAgentEpisodeForLoop(ctx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loopEpisode.Turns) != 21 || loopEpisode.Turns[0].ID != "working-state:"+episodeID+":185" || loopEpisode.Turns[1].RoundIndex != 186 || loopEpisode.Turns[20].RoundIndex != 205 {
		t.Fatalf("bounded loop history = len %d first %q rounds %d..%d", len(loopEpisode.Turns), loopEpisode.Turns[0].ID, loopEpisode.Turns[1].RoundIndex, loopEpisode.Turns[len(loopEpisode.Turns)-1].RoundIndex)
	}

	response, err := http.Get(node.apiBase + "/steward/agent-episodes/" + episodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("overview status = %s", response.Status)
	}
	var overview struct {
		Episode domain.StewardAgentEpisode `json:"episode"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	if overview.Episode.TurnCount != 205 || len(overview.Episode.Turns) != 6 || !overview.Episode.TurnsHasMore {
		t.Fatalf("overview = count %d, turns %d, has_more %v", overview.Episode.TurnCount, len(overview.Episode.Turns), overview.Episode.TurnsHasMore)
	}
	if overview.Episode.Turns[0].RoundIndex != 200 || overview.Episode.Turns[5].RoundIndex != 205 {
		t.Fatalf("overview rounds = %d..%d, want 200..205", overview.Episode.Turns[0].RoundIndex, overview.Episode.Turns[5].RoundIndex)
	}

	first := getAgentHistoryPage(t, node.apiBase+"/steward/agent-episodes/"+episodeID+"/turns?limit=50")
	if first.Total != 205 || len(first.Turns) != 50 || first.Turns[0].RoundIndex != 156 || first.Turns[49].RoundIndex != 205 || first.NextBeforeRound != 156 || !first.HasMore {
		t.Fatalf("first page = total %d len %d rounds %d..%d cursor %d more %v", first.Total, len(first.Turns), first.Turns[0].RoundIndex, first.Turns[len(first.Turns)-1].RoundIndex, first.NextBeforeRound, first.HasMore)
	}
	second := getAgentHistoryPage(t, node.apiBase+"/steward/agent-episodes/"+episodeID+"/turns?limit=50&before_round=156")
	if len(second.Turns) != 50 || second.Turns[0].RoundIndex != 106 || second.Turns[49].RoundIndex != 155 || second.NextBeforeRound != 106 || !second.HasMore {
		t.Fatalf("second page = len %d rounds %d..%d cursor %d more %v", len(second.Turns), second.Turns[0].RoundIndex, second.Turns[len(second.Turns)-1].RoundIndex, second.NextBeforeRound, second.HasMore)
	}
}

func getAgentHistoryPage(t *testing.T, url string) domain.StewardAgentTurnPage {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history page status = %s", response.Status)
	}
	var page domain.StewardAgentTurnPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}
