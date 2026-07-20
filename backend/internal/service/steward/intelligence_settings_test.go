package steward

import "testing"

func validTestIntelligenceSettings() IntelligenceSettings {
	return IntelligenceSettings{
		Enabled: true, Mode: "batch", CaptureProfile: "deep", ActivitySampleSeconds: 10,
		SessionizeIntervalSeconds: 60, BatchIntervalSeconds: 1800, BoundaryGraceSeconds: 300,
		DailyReportFallbackLocal: "21:30", WeeklyReportDay: 0, WeeklyReportLocal: "21:45",
		MonthlyReportLocal: "22:00", RecentProfileDays: 14, StableMinEvidenceDays: 3,
		ProfileBootstrapDays: 30, ReportCatchupDays: 7, BackgroundMaxRounds: 64,
		BackgroundMaxToolCalls: 256, BackgroundMaxDurationSeconds: 7200,
		BackgroundNoProgressLimit: 3, QuietStartLocal: "23:00", QuietEndLocal: "08:00",
		ReminderDailySoftBudget: 8, ReminderCategorySoftBudget: 3, ReminderCooldownSeconds: 1200,
		RawMetadataRetentionDays: 0, UnreferencedMediaRetentionDays: 30,
	}
}

func TestValidateIntelligenceSettingsAcceptsDefaultBatchConfiguration(t *testing.T) {
	if err := validateIntelligenceSettings(validTestIntelligenceSettings()); err != nil {
		t.Fatalf("validate defaults: %v", err)
	}
}

func TestApplyIntelligenceSettingsUpdatePreservesUnspecifiedValues(t *testing.T) {
	settings := validTestIntelligenceSettings()
	mode := " legacy "
	batchSeconds := 900
	maxRounds := 0
	applyIntelligenceSettingsUpdate(&settings, UpdateIntelligenceSettingsInput{
		Mode: &mode, BatchIntervalSeconds: &batchSeconds, BackgroundMaxRounds: &maxRounds,
	})
	if settings.Mode != "legacy" || settings.BatchIntervalSeconds != 900 || settings.BackgroundMaxRounds != 0 {
		t.Fatalf("unexpected updated settings: %#v", settings)
	}
	if settings.ActivitySampleSeconds != 10 || settings.RecentProfileDays != 14 {
		t.Fatalf("unspecified settings changed: %#v", settings)
	}
}

func TestValidateIntelligenceSettingsRejectsInvalidScheduleAndRequiredIntervals(t *testing.T) {
	settings := validTestIntelligenceSettings()
	settings.DailyReportFallbackLocal = "25:70"
	if err := validateIntelligenceSettings(settings); err == nil {
		t.Fatal("expected invalid report time to fail")
	}
	settings = validTestIntelligenceSettings()
	settings.BatchIntervalSeconds = 0
	if err := validateIntelligenceSettings(settings); err == nil {
		t.Fatal("expected zero batch interval to fail")
	}
}

func TestIntelligenceSettingsSideEffectChangeDetection(t *testing.T) {
	before := validTestIntelligenceSettings()
	after := before
	after.BatchIntervalSeconds = 600
	if intelligenceReminderSettingsChanged(before, after) || intelligenceRetentionSettingsChanged(before, after) {
		t.Fatal("unrelated scheduling setting must not publish reminder or retention policy versions")
	}
	after.QuietStartLocal = "22:30"
	if !intelligenceReminderSettingsChanged(before, after) {
		t.Fatal("quiet-hours change must publish a reminder policy version")
	}
	after = before
	after.RawMetadataRetentionDays = 14
	if !intelligenceRetentionSettingsChanged(before, after) {
		t.Fatal("raw metadata retention change must synchronize lifecycle policies")
	}
}
