package steward

import (
	"errors"
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestBuildProfileSnapshotsUsesExplicitStableRecentPrecedence(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	facts := []domain.StewardProfileFact{
		profileFactForTest("focus", domain.StewardProfileHorizonRecent, 1, "recent", now.AddDate(0, 0, -1)),
		profileFactForTest("focus", domain.StewardProfileHorizonStable, 1, "stable", now.AddDate(0, 0, -10)),
		profileFactForTest("focus", domain.StewardProfileHorizonExplicit, 1, "explicit", now.Add(-time.Hour)),
		profileFactForTest("editor", domain.StewardProfileHorizonStable, 1, "old", now.AddDate(0, 0, -20)),
		profileFactForTest("editor", domain.StewardProfileHorizonStable, 2, "new", now.AddDate(0, 0, -2)),
	}
	projections := BuildProfileSnapshots(facts, now, 14)
	merged := projections[domain.StewardProfileHorizonMerged]
	if got := merged.Profile["focus"].(map[string]any)["value"]; got != "explicit" {
		t.Fatalf("merged focus = %v, want explicit", got)
	}
	if got := merged.Profile["editor"].(map[string]any)["value"]; got != "new" {
		t.Fatalf("merged editor = %v, want latest stable version", got)
	}
	if len(projections[domain.StewardProfileHorizonRecent].Facts) != 1 {
		t.Fatalf("recent facts = %+v", projections[domain.StewardProfileHorizonRecent].Facts)
	}
}

func TestBuildProfileSnapshotsExpiresOnlyRecentFacts(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	facts := []domain.StewardProfileFact{
		profileFactForTest("recent_old", domain.StewardProfileHorizonRecent, 1, "old", now.AddDate(0, 0, -15)),
		profileFactForTest("stable_old", domain.StewardProfileHorizonStable, 1, "kept", now.AddDate(0, 0, -100)),
	}
	projections := BuildProfileSnapshots(facts, now, 14)
	if _, found := projections[domain.StewardProfileHorizonRecent].Profile["recent_old"]; found {
		t.Fatal("expired recent fact remained in projection")
	}
	if _, found := projections[domain.StewardProfileHorizonStable].Profile["stable_old"]; !found {
		t.Fatal("stable fact was incorrectly expired by recent window")
	}
}

func TestBuildProfileSnapshotsChoosesConflictingCandidateByEffectiveConfidence(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	higherConfidence := profileFactForTest("focus_window", domain.StewardProfileHorizonRecent, 1, "morning", now.Add(-24*time.Hour))
	higherConfidence.Confidence = 1
	newerButWeaker := profileFactForTest("focus_window", domain.StewardProfileHorizonRecent, 2, "evening", now)
	newerButWeaker.Confidence = 0.4

	projection := BuildProfileSnapshots([]domain.StewardProfileFact{higherConfidence, newerButWeaker}, now, 14)[domain.StewardProfileHorizonRecent]
	if got := projection.Profile["focus_window"].(map[string]any)["value"]; got != "morning" {
		t.Fatalf("conflicting winner = %v, want higher effective-confidence evidence branch", got)
	}
	if len(projection.Facts) != 1 || projection.Facts[0].EffectiveConfidence <= newerButWeaker.Confidence*profileConflictConfidenceMultiplier {
		t.Fatalf("selected fact effective confidence = %+v", projection.Facts)
	}
	if projection.Facts[0].EffectiveConfidence >= projection.Facts[0].Confidence {
		t.Fatalf("conflicting inference confidence was not reduced: %+v", projection.Facts[0])
	}
}

func TestBuildProfileSnapshotsContinuouslyDecaysOnlyRecentConfidence(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	recent := profileFactForTest("recent", domain.StewardProfileHorizonRecent, 1, "value", now.Add(-7*24*time.Hour))
	stable := profileFactForTest("stable", domain.StewardProfileHorizonStable, 1, "value", now.Add(-100*24*time.Hour))
	explicit := profileFactForTest("explicit", domain.StewardProfileHorizonExplicit, 1, "value", now.Add(-100*24*time.Hour))
	for _, fact := range []*domain.StewardProfileFact{&recent, &stable, &explicit} {
		fact.Confidence = 0.8
	}
	explicit.CreatedBy = "user"
	explicit.UserConfirmed = true

	projections := BuildProfileSnapshots([]domain.StewardProfileFact{recent, stable, explicit}, now, 14)
	if got := projections[domain.StewardProfileHorizonRecent].Facts[0].EffectiveConfidence; got != 0.4 {
		t.Fatalf("half-window recent confidence = %v, want 0.4", got)
	}
	if got := projections[domain.StewardProfileHorizonStable].Facts[0].EffectiveConfidence; got != 0.8 {
		t.Fatalf("stable confidence decayed to %v", got)
	}
	if got := projections[domain.StewardProfileHorizonExplicit].Facts[0].EffectiveConfidence; got != 0.8 {
		t.Fatalf("explicit confidence decayed to %v", got)
	}
}

func TestBuildProfileSnapshotsKeepsExplicitUserCorrectionHighestWithinLayer(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	modelInference := profileFactForTest("editor", domain.StewardProfileHorizonExplicit, 2, "model", now)
	modelInference.Confidence = 1
	userCorrection := profileFactForTest("editor", domain.StewardProfileHorizonExplicit, 1, "user", now.Add(-time.Hour))
	userCorrection.Confidence = 0.2
	userCorrection.CreatedBy = "user"
	userCorrection.UserConfirmed = true

	projection := BuildProfileSnapshots([]domain.StewardProfileFact{modelInference, userCorrection}, now, 14)[domain.StewardProfileHorizonExplicit]
	if got := projection.Profile["editor"].(map[string]any)["value"]; got != "user" {
		t.Fatalf("explicit winner = %v, want user correction", got)
	}
	if projection.Facts[0].EffectiveConfidence != userCorrection.Confidence {
		t.Fatalf("user correction received conflict penalty: %+v", projection.Facts[0])
	}
}

func TestStableProfileToolRequiresThreeDistinctEvidenceDays(t *testing.T) {
	tool := runtimeIntelligenceTool{action: "steward.profile.upsert_fact"}
	input := map[string]any{
		"key": "work_style", "value": map[string]any{"value": "deep-work"}, "horizon": "stable",
		"evidence": []any{
			map[string]any{"source_type": "session", "source_id": "one", "evidence_day": "2026-07-17"},
			map[string]any{"source_type": "session", "source_id": "two", "evidence_day": "2026-07-18"},
		},
	}
	if err := tool.Validate(input); !errors.Is(err, ErrProfileEvidenceInsufficient) {
		t.Fatalf("Validate error = %v, want insufficient evidence", err)
	}
	input["evidence"] = append(input["evidence"].([]any), map[string]any{"source_type": "session", "source_id": "three", "evidence_day": "2026-07-19"})
	if err := tool.Validate(input); err != nil {
		t.Fatalf("Validate with three evidence days: %v", err)
	}
}

func TestExplicitProfileToolRequiresConfirmation(t *testing.T) {
	tool := runtimeIntelligenceTool{action: "steward.profile.upsert_fact"}
	input := map[string]any{"key": "name", "value": map[string]any{"value": "Alice"}, "horizon": "explicit"}
	if err := tool.Validate(input); err == nil {
		t.Fatal("explicit fact without user confirmation was accepted")
	}
	input["user_confirmed"] = true
	if err := tool.Validate(input); !errors.Is(err, ErrProfileEvidenceRequired) {
		t.Fatalf("model explicit fact without persisted evidence error = %v, want evidence required", err)
	}
	input["evidence"] = []any{map[string]any{"source_type": "user_message", "source_id": "message-id", "evidence_day": "2026-07-19"}}
	if err := tool.Validate(input); err != nil {
		t.Fatalf("confirmed explicit fact with evidence rejected syntactically: %v", err)
	}
}

func TestProfileGetToolRequiresDeclaredProjection(t *testing.T) {
	tool := runtimeIntelligenceTool{action: "steward.profile.get"}
	for name, input := range map[string]map[string]any{
		"missing": {},
		"invalid": {"view": "all"},
		"unknown": {"view": "merged", "include_history": true},
	} {
		if err := tool.Validate(input); err == nil {
			t.Fatalf("%s profile projection input was accepted: %#v", name, input)
		}
	}
	for _, projection := range []string{"recent", "stable", "explicit", "merged"} {
		if err := tool.Validate(map[string]any{"view": projection}); err != nil {
			t.Fatalf("valid projection %q rejected: %v", projection, err)
		}
	}

	merged := &domain.StewardProfileSnapshot{Horizon: domain.StewardProfileHorizonMerged}
	selected, ok := ProfileSnapshotForView(domain.StewardProfileView{Merged: merged}, "merged")
	if !ok || selected != merged {
		t.Fatalf("merged projection selected=%+v ok=%v", selected, ok)
	}
	if _, ok := ProfileSnapshotForView(domain.StewardProfileView{}, "all"); ok {
		t.Fatal("unsupported projection was selected")
	}
}

func TestReportToolAllowsSilentCompleteReport(t *testing.T) {
	tool := runtimeIntelligenceTool{action: "steward.report.write"}
	input := map[string]any{
		"cadence": "daily", "period_start": "2026-07-19T00:00:00+08:00", "period_end": "2026-07-20T00:00:00+08:00",
		"title": "日报", "body": "今天完成了开发。", "silent": true,
	}
	if err := tool.Validate(input); err != nil {
		t.Fatalf("silent report should still be writable: %v", err)
	}
}

func TestDueIntelligencePeriodsSchedulesDailyWeeklyMonthly(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 2, 1, 22, 30, 0, 0, location) // Sunday and first day of month.
	periods := dueIntelligencePeriods(now)
	if len(periods) != 3 {
		t.Fatalf("periods = %+v, want daily, weekly and monthly", periods)
	}
	if periods[0].Kind != intelligenceJobReportDaily || periods[1].Kind != intelligenceJobReportWeekly || periods[2].Kind != intelligenceJobReportMonthly {
		t.Fatalf("unexpected period order: %+v", periods)
	}
}

func TestConfiguredMonthlyReportCoversPreviousCalendarMonthOnFirstDay(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	settings := IntelligenceSettings{
		DailyReportFallbackLocal: "21:30", WeeklyReportDay: int(time.Sunday), WeeklyReportLocal: "21:45",
		MonthlyReportLocal: "22:00", ProfileBootstrapDays: 2, ReportCatchupDays: 7,
	}
	periods, err := dueIntelligencePeriodsForSettings(time.Date(2026, 7, 1, 22, 30, 0, 0, location), settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range periods {
		if period.Kind != intelligenceJobReportMonthly {
			continue
		}
		if period.Key != "2026-06" || period.Start.Day() != 1 || period.Start.Month() != time.June ||
			period.End.Day() != 1 || period.End.Month() != time.July || period.DueAt.Hour() != 22 {
			t.Fatalf("monthly period=%+v", period)
		}
		return
	}
	t.Fatal("monthly report was not scheduled on the first day")
}

func TestConfiguredIntelligenceScheduleBackfillsProfilesAndReports(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 7, 19, 22, 30, 0, 0, location)
	settings := IntelligenceSettings{
		DailyReportFallbackLocal: "21:30", WeeklyReportDay: int(time.Sunday), WeeklyReportLocal: "21:45",
		MonthlyReportLocal: "22:00", ProfileBootstrapDays: 30, ReportCatchupDays: 7,
	}
	periods, err := dueIntelligencePeriodsForSettings(now, settings)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	counts := map[string]int{}
	for _, period := range periods {
		counts[period.Kind]++
		if period.DueAt.After(now) {
			t.Fatalf("future period scheduled: %+v", period)
		}
	}
	if counts[intelligenceJobProfileConsolidation] != 30 || counts[intelligenceJobReportDaily] != 7 || counts[intelligenceJobReportWeekly] != 1 {
		t.Fatalf("configured catch-up counts = %+v", counts)
	}
}

func TestConfiguredIntelligenceScheduleHasNoDailyCoverageGap(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	settings := IntelligenceSettings{
		DailyReportFallbackLocal: "21:30", WeeklyReportDay: int(time.Sunday), WeeklyReportLocal: "21:45",
		MonthlyReportLocal: "22:00", ProfileBootstrapDays: 2, ReportCatchupDays: 2,
	}
	periods, err := dueIntelligencePeriodsForSettings(time.Date(2026, 7, 20, 22, 0, 0, 0, location), settings)
	if err != nil {
		t.Fatal(err)
	}
	daily := []dueIntelligencePeriod{}
	for _, period := range periods {
		if period.Kind == intelligenceJobReportDaily {
			daily = append(daily, period)
		}
	}
	if len(daily) != 2 {
		t.Fatalf("daily periods = %+v", daily)
	}
	if !daily[0].End.Equal(daily[1].Start) {
		t.Fatalf("daily coverage has a gap: first end=%s second start=%s", daily[0].End, daily[1].Start)
	}
	if got := daily[0].End.Sub(daily[0].Start); got != 24*time.Hour {
		t.Fatalf("daily coverage duration = %s", got)
	}
}

func TestConfiguredIntelligenceScheduleUsesSystemLocalTimezoneWhenUnset(t *testing.T) {
	location := time.FixedZone("system-local", 8*60*60)
	resolved, err := intelligenceScheduleLocation("", location)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != location {
		t.Fatalf("resolved location = %v, want system local %v", resolved, location)
	}
	settings := IntelligenceSettings{
		DailyReportFallbackLocal: "21:30", WeeklyReportDay: int(time.Sunday), WeeklyReportLocal: "21:45",
		MonthlyReportLocal: "22:00", ProfileBootstrapDays: 1, ReportCatchupDays: 1,
	}
	previousLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = previousLocal })
	periods, err := dueIntelligencePeriodsForSettings(time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC), settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range periods {
		if period.DueAt.Location() != location {
			t.Fatalf("period %s used %v, want system-local location", period.Kind, period.DueAt.Location())
		}
	}
}

func TestConfiguredIntelligenceSchedulePrefersConfiguredTimezone(t *testing.T) {
	location, err := intelligenceScheduleLocation("Asia/Shanghai", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got := location.String(); got != "Asia/Shanghai" {
		t.Fatalf("resolved timezone = %q", got)
	}
}

func TestIntelligenceWriteToolsDoNotRequireBusinessApproval(t *testing.T) {
	for _, action := range []string{"steward.profile.upsert_fact", "steward.report.write"} {
		spec := (runtimeIntelligenceTool{action: action}).Spec()
		if spec.PermissionLevel != PermissionA0 || spec.ApprovalMode != RuntimeApprovalNever || spec.SideEffect != RuntimeSideEffectNone {
			t.Fatalf("%s policy = permission %s approval %s side effect %s", action, spec.PermissionLevel, spec.ApprovalMode, spec.SideEffect)
		}
		if err := validateRuntimeToolSpec(spec); err != nil {
			t.Fatalf("%s spec validation: %v", action, err)
		}
	}
}

func TestActivityQueryWindowRejectsInvalidRange(t *testing.T) {
	_, _, err := parseActivityQueryWindow("2026-07-19T10:00:00+08:00", "2026-07-19T09:00:00+08:00")
	if err == nil {
		t.Fatal("invalid activity query range was accepted")
	}
}

func TestIntelligenceRetryDelayIsBoundedExponential(t *testing.T) {
	if got := intelligenceRetryDelay(1); got != 30*time.Second {
		t.Fatalf("attempt 1 delay = %s", got)
	}
	if got := intelligenceRetryDelay(4); got != 4*time.Minute {
		t.Fatalf("attempt 4 delay = %s", got)
	}
	if got := intelligenceRetryDelay(99); got != time.Hour {
		t.Fatalf("large attempt delay = %s", got)
	}
}

func TestNormalizeProfileEvidenceDeduplicatesSourceAndPreservesCalendarDay(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	items := normalizeProfileEvidence([]ProfileEvidenceInput{
		{SourceType: " session ", SourceID: "same", EvidenceDay: time.Date(2026, 7, 19, 1, 0, 0, 0, location)},
		{SourceType: "session", SourceID: "same", EvidenceDay: time.Date(2026, 7, 20, 1, 0, 0, 0, location)},
	}, time.Now())
	if len(items) != 1 {
		t.Fatalf("normalized evidence count = %d", len(items))
	}
	if got := items[0].EvidenceDay.Format("2006-01-02"); got != "2026-07-19" {
		t.Fatalf("evidence day = %s, want source-local calendar day", got)
	}
}

func profileFactForTest(key, horizon string, version int, value string, validFrom time.Time) domain.StewardProfileFact {
	return domain.StewardProfileFact{Key: key, Horizon: horizon, Status: domain.StewardProfileFactActive,
		Version: version, Value: map[string]any{"value": value}, ValidFrom: validFrom, UpdatedAt: validFrom}
}
