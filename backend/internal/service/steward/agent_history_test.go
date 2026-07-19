package steward

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"testing"

	"mongojson/backend/internal/domain"
)

func TestAgentWorkingStatePreservesEarlyFactsAcrossHundredsOfRounds(t *testing.T) {
	state := domain.StewardAgentWorkingState{}
	for round := 1; round <= 240; round++ {
		path := fmt.Sprintf(`C:\workspace\round-%03d\result.txt`, round)
		if round == 1 {
			path = `C:\Users\zhj13\Desktop\工作区\first-critical-result.txt`
		}
		foldAgentWorkingState(&state, domain.StewardAgentTurn{
			RoundIndex: round,
			Status:     "tools_complete",
			ToolCalls: []domain.StewardAgentToolCall{{
				ID: "call", ToolName: "fs.read_text", Arguments: map[string]any{"path": path},
			}},
			ToolResults: []domain.StewardAgentToolResult{{
				ToolCallID: "call", ToolName: "fs.read_text",
				Output:   map[string]any{"path": path, "sha256": fmt.Sprintf("hash-%03d", round)},
				Evidence: map[string]any{"evidence_id": fmt.Sprintf("evidence-%03d", round)},
			}},
		})
	}
	state.Summary = renderAgentWorkingSummary(state, 240)

	joinedAnchors := strings.Join(state.Anchors, "\n")
	if !strings.Contains(joinedAnchors, `C:\Users\zhj13\Desktop\工作区\first-critical-result.txt`) {
		t.Fatalf("early critical path was evicted from bounded working state: %s", joinedAnchors)
	}
	if !strings.Contains(strings.Join(state.EvidenceReferences, "\n"), "evidence-001") {
		t.Fatal("early evidence reference was evicted")
	}
	if !strings.Contains(state.Summary, "first-critical-result.txt") {
		t.Fatal("rendered working summary omitted the early critical path")
	}
	if !strings.Contains(state.Summary, "round-240") {
		t.Fatal("rendered working summary omitted the newest compacted path")
	}
	if got := len([]rune(state.Summary)); got > agentWorkingSummaryBudget {
		t.Fatalf("working summary length = %d, want <= %d", got, agentWorkingSummaryBudget)
	}
	if state.CompletedRounds != 240 {
		t.Fatalf("completed rounds = %d, want 240", state.CompletedRounds)
	}
}

func TestAppendBoundedDurableKeepsFirstHalfAndNewestHalf(t *testing.T) {
	items := []string{}
	for index := 0; index < 20; index++ {
		items = appendBoundedDurable(items, fmt.Sprintf("item-%02d", index), 10)
	}
	if len(items) != 10 {
		t.Fatalf("len = %d, want 10", len(items))
	}
	for index := 0; index < 5; index++ {
		if items[index] != fmt.Sprintf("item-%02d", index) {
			t.Fatalf("stable item %d = %q", index, items[index])
		}
	}
	if items[len(items)-1] != "item-19" {
		t.Fatalf("newest item = %q, want item-19", items[len(items)-1])
	}
}

func TestAgentEpisodeMemoryDetailsRemainBoundedAndRetainDurableAndRecentFacts(t *testing.T) {
	episode := domain.StewardAgentEpisode{
		Goal: "完成一个超长任务",
		WorkingState: domain.StewardAgentWorkingState{
			Summary: `早期关键结果：C:\Users\Alice\Desktop\Workspace\first-result.txt`,
		},
	}
	for round := 1; round <= 240; round++ {
		episode.Turns = append(episode.Turns, domain.StewardAgentTurn{
			ID: "turn-" + fmt.Sprint(round), RoundIndex: round,
			AssistantContent: strings.Repeat(fmt.Sprintf("第%d轮说明 ", round), 200),
			ToolCalls:        []domain.StewardAgentToolCall{{ID: "call", ToolName: "fs.read_text"}},
			ToolResults: []domain.StewardAgentToolResult{{
				ToolCallID: "call", ToolName: "fs.read_text",
				Output: map[string]any{"path": fmt.Sprintf(`C:\workspace\round-%03d\result.txt`, round)},
			}},
		})
	}
	details := buildAgentEpisodeMemoryDetails(episode)
	if got := len([]rune(details)); got > agentMemoryContentBudget {
		t.Fatalf("memory content length=%d, want <= %d", got, agentMemoryContentBudget)
	}
	if !strings.Contains(details, "first-result.txt") {
		t.Fatal("durable working-state fact disappeared from final memory")
	}
	if !strings.Contains(details, "第240轮") {
		t.Fatal("most recent round disappeared from final memory")
	}
	if strings.Contains(details, "第1轮说明") {
		t.Fatal("raw old turns were materialized instead of the durable summary")
	}
}

func TestAgentEpisodeHotPathsDoNotCallFullHistoryLoader(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	loopFile := strings.TrimSuffix(currentFile, "agent_history_test.go") + "agent_loop.go"
	parsed, err := parser.ParseFile(token.NewFileSet(), loopFile, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	hotPaths := map[string]bool{
		"completeAgentEpisodeExecution":     false,
		"repairExecutingAgentEpisodes":      false,
		"handleAgentEpisodeAdvanceError":    false,
		"failAgentEpisode":                  false,
		"recordAgentEpisodeMemory":          false,
		"decideAgentEpisodeLocked":          false,
		"resumeAwaitingAgentEpisode":        false,
		"reconcileControlledAgentExecution": false,
		"persistAgentControlReconciliation": false,
	}
	for _, declaration := range parsed.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := hotPaths[fn.Name.Name]; !tracked {
			continue
		}
		hotPaths[fn.Name.Name] = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "GetAgentEpisode" {
				t.Errorf("hot path %s calls GetAgentEpisode and materializes unbounded turns", fn.Name.Name)
			}
			return true
		})
	}
	for name, found := range hotPaths {
		if !found {
			t.Errorf("hot path %s was not found", name)
		}
	}
}
