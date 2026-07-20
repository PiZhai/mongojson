package steward

import (
	"math"
	"testing"
)

func TestNormalizeReminderFeedbackActionKeepsLegacyCompatibility(t *testing.T) {
	tests := map[string]string{
		"acknowledge": ReminderFeedbackAcknowledged,
		"snooze":      ReminderFeedbackSnoozed,
		"cancel":      ReminderFeedbackCancelled,
		"complete":    ReminderFeedbackActed,
		"dismiss":     ReminderFeedbackDismissed,
		"opened":      ReminderFeedbackOpened,
	}
	for input, want := range tests {
		got, err := normalizeReminderFeedbackAction(input)
		if err != nil || got != want {
			t.Fatalf("normalize %q = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeReminderFeedbackAction("unknown"); err == nil {
		t.Fatal("unknown reminder feedback must be rejected")
	}
}

func TestReminderFeedbackPolarityAndScore(t *testing.T) {
	positive, snoozed, negative := feedbackPolarity(ReminderFeedbackActed)
	if positive != 1 || snoozed != 0 || negative != 0 {
		t.Fatalf("acted polarity = %d/%d/%d", positive, snoozed, negative)
	}
	if score := receptivityScore(3, 1, 0); math.Abs(score-0.6875) > 0.0001 {
		t.Fatalf("positive score = %f", score)
	}
	if score := receptivityScore(0, 1, 3); score >= 0 {
		t.Fatalf("negative score = %f", score)
	}
}

func TestDefaultReminderPolicyIsSoftAndValid(t *testing.T) {
	policy := defaultReminderPolicy()
	if err := validateReminderPolicy(policy); err != nil {
		t.Fatalf("default policy invalid: %v", err)
	}
	quiet, ok := policy["quiet_hours"].(map[string]any)
	if !ok || quiet["mode"] != "soft" || quiet["start"] != "23:00" || quiet["end"] != "08:00" {
		t.Fatalf("unexpected quiet policy: %#v", policy["quiet_hours"])
	}
	if policy["daily_soft_budget"] != 8 || policy["category_soft_budget"] != 3 {
		t.Fatalf("unexpected reminder budgets: %#v", policy)
	}
}

func TestMergeIntelligenceReminderPolicyPreservesModelLearnedFields(t *testing.T) {
	base := map[string]any{
		"quiet_hours":       map[string]any{"start": "21:00", "end": "07:00", "mode": "adaptive", "weekends": "later"},
		"preferred_windows": []any{"09:30", "15:00"},
		"custom_model_rule": map[string]any{"when": "focused", "action": "defer"},
	}
	settings := validTestIntelligenceSettings()
	settings.QuietStartLocal = "22:45"
	settings.QuietEndLocal = "08:15"
	settings.ReminderDailySoftBudget = 5
	settings.ReminderCategorySoftBudget = 2
	settings.ReminderCooldownSeconds = 2700

	merged := mergeIntelligenceReminderPolicy(base, settings)
	quiet, ok := merged["quiet_hours"].(map[string]any)
	if !ok || quiet["start"] != "22:45" || quiet["end"] != "08:15" || quiet["mode"] != "adaptive" || quiet["weekends"] != "later" {
		t.Fatalf("quiet-hours merge lost learned fields: %#v", merged["quiet_hours"])
	}
	if merged["daily_soft_budget"] != 5 || merged["category_soft_budget"] != 2 || merged["cooldown_seconds"] != 2700 {
		t.Fatalf("settings values were not applied: %#v", merged)
	}
	if _, ok := merged["custom_model_rule"]; !ok {
		t.Fatalf("model-authored policy field was discarded: %#v", merged)
	}
	if base["quiet_hours"].(map[string]any)["start"] != "21:00" {
		t.Fatal("merge mutated the active policy map")
	}
}

func TestValidateReminderPolicyRejectsMalformedValues(t *testing.T) {
	if err := validateReminderPolicy(map[string]any{"cooldown_seconds": -1}); err == nil {
		t.Fatal("negative cooldown must be rejected")
	}
	if err := validateReminderPolicy(map[string]any{"quiet_hours": map[string]any{"start": "late", "end": "08:00"}}); err == nil {
		t.Fatal("invalid quiet time must be rejected")
	}
	if err := validateReminderPolicy(map[string]any{"preferred_windows": []any{"whenever"}}); err == nil {
		t.Fatal("invalid preferred reminder window must be rejected")
	}
	if err := validateReminderPolicy(map[string]any{"preferred_channels": []any{"carrier-pigeon"}}); err == nil {
		t.Fatal("unsupported preferred channel must be rejected")
	}
}

func TestReminderPolicyRuntimeToolsExposeModelLearningLoop(t *testing.T) {
	service := &Service{runtimeTools: newRuntimeToolRegistry()}
	service.registerIntelligenceTools()
	for _, name := range []string{
		"steward.reminder.context",
		"steward.reminder_policy.get",
		"steward.reminder_policy.update",
		"steward.reminder_feedback.query",
	} {
		tool, ok := service.runtimeTools.get(name)
		if !ok {
			t.Fatalf("runtime tool %s was not registered", name)
		}
		if tool.Spec().ApprovalMode != RuntimeApprovalNever || tool.Spec().PermissionLevel != PermissionA0 {
			t.Fatalf("runtime tool %s unexpectedly requires business approval: %+v", name, tool.Spec())
		}
	}
	update, _ := service.runtimeTools.get("steward.reminder_policy.update")
	validator, ok := update.(runtimeIntelligenceTool)
	if !ok {
		t.Fatalf("unexpected reminder policy tool type %T", update)
	}
	if err := validator.Validate(map[string]any{
		"category":          "focus",
		"policy":            map[string]any{"daily_soft_budget": float64(4), "quiet_hours": map[string]any{"start": "22:30", "end": "08:30"}},
		"rationale":         "Recent snooze feedback moved the useful window later.",
		"evidence_manifest": []any{"feedback-1", "window-2"},
	}); err != nil {
		t.Fatalf("valid model reminder policy update rejected: %v", err)
	}
	if err := validator.Validate(map[string]any{"policy": map[string]any{}, "rationale": "missing"}); err == nil {
		t.Fatal("empty reminder policy must be rejected")
	}
}
