package steward

import (
	"strings"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestDueProactivePeriodsUseDailyAndWeeklyWindows(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	now := time.Date(2026, 7, 19, 21, 0, 0, 0, location) // Sunday
	periods := dueProactivePeriods(now, RunProactiveInput{})
	if len(periods) != 2 {
		t.Fatalf("period count = %d, want daily and weekly", len(periods))
	}
	if periods[0].Cadence != proactiveCadenceDaily || periods[0].Key != "2026-07-19" {
		t.Fatalf("unexpected daily period: %+v", periods[0])
	}
	if periods[1].Cadence != proactiveCadenceWeekly || periods[1].Key != "2026-W29" {
		t.Fatalf("unexpected weekly period: %+v", periods[1])
	}
}

func TestDueProactivePeriodsForceOneCadence(t *testing.T) {
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.Local)
	periods := dueProactivePeriods(now, RunProactiveInput{Force: true, Cadence: "daily"})
	if len(periods) != 1 || periods[0].Cadence != proactiveCadenceDaily || !strings.Contains(periods[0].Key, "-manual-") {
		t.Fatalf("forced periods = %+v", periods)
	}
}

func TestProactiveDecisionPromptMakesSilenceAndToolsModelChoices(t *testing.T) {
	run := domainProactiveRunForTest()
	prompt := proactiveDecisionPrompt(run, ObservationModelOutput{Summary: "今天主要在开发", Insights: []string{"重复调试"}, SuggestedActions: []string{"明天复盘"}})
	for _, expected := range []string{proactiveSilentToken, "直接使用提供的 tools", "不要重复现有任务", "高风险动作只能进入确认"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestProactiveExecutionTextHidesInternalDecisionPrompt(t *testing.T) {
	run := domain.StewardProactiveRun{Cadence: proactiveCadenceDaily}
	plan := RuntimePlanDraft{Steps: []CreateAgentRunStepInput{{Title: "检查桌面目录", ToolName: "fs.list"}}}
	instruction, summary := proactiveExecutionText(run, ObservationModelOutput{Summary: "桌面状态需要确认"}, plan)
	if summary != "每日主动帮助：检查桌面目录" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if !strings.Contains(instruction, "桌面状态需要确认") || strings.Contains(instruction, proactiveSilentToken) {
		t.Fatalf("unexpected instruction: %q", instruction)
	}
}

func domainProactiveRunForTest() domain.StewardProactiveRun {
	return domain.StewardProactiveRun{Cadence: proactiveCadenceDaily, PeriodStart: time.Unix(0, 0), PeriodEnd: time.Unix(3600, 0)}
}
