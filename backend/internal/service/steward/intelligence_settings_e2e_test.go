package steward

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

func TestIntelligenceSettingsSynchronizeReminderAndRetentionBehavior(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("set TEST_DATABASE_URL to run the intelligence settings integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db := newProfileReportTestDatabase(t, ctx, baseDSN)
	service := NewService(db, WithAgentID("intelligence-settings-e2e"))

	initialSettings, err := service.GetIntelligenceSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initialPolicy, err := service.GetReminderPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initialPolicy.Policy["preferred_windows"] = []any{"09:30", "15:00"}
	initialPolicy.Policy["model_only_rule"] = map[string]any{"activity": "focused", "decision": "defer"}
	initialPolicy.Policy["quiet_hours"] = map[string]any{
		"start": "21:00", "end": "07:00", "mode": "adaptive", "weekends": "later",
	}
	modelPolicy, err := service.UpdateReminderPolicy(ctx, UpdateReminderPolicyInput{
		ProfileScope: "default",
		Category:     "*",
		Policy:       initialPolicy.Policy,
		Rationale:    "model-learned baseline",
	})
	if err != nil {
		t.Fatal(err)
	}

	quietStart, quietEnd := "22:45", "08:15"
	dailyBudget, categoryBudget, cooldown := 5, 2, 2700
	rawDays, mediaDays := 7, 30
	updated, err := service.UpdateIntelligenceSettings(ctx, UpdateIntelligenceSettingsInput{
		QuietStartLocal:                &quietStart,
		QuietEndLocal:                  &quietEnd,
		ReminderDailySoftBudget:        &dailyBudget,
		ReminderCategorySoftBudget:     &categoryBudget,
		ReminderCooldownSeconds:        &cooldown,
		RawMetadataRetentionDays:       &rawDays,
		UnreferencedMediaRetentionDays: &mediaDays,
		ExpectedRevision:               &initialSettings.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != initialSettings.Revision+1 {
		t.Fatalf("settings revision=%d, want %d", updated.Revision, initialSettings.Revision+1)
	}
	activePolicy, err := service.GetReminderPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activePolicy.Version != modelPolicy.Version+1 || activePolicy.SupersedesID == nil || *activePolicy.SupersedesID != modelPolicy.ID {
		t.Fatalf("settings did not create a successor policy: %+v", activePolicy)
	}
	quiet, ok := activePolicy.Policy["quiet_hours"].(map[string]any)
	if !ok || quiet["start"] != quietStart || quiet["end"] != quietEnd || quiet["mode"] != "adaptive" || quiet["weekends"] != "later" {
		t.Fatalf("quiet-hours policy merge=%#v", activePolicy.Policy["quiet_hours"])
	}
	if _, ok := activePolicy.Policy["model_only_rule"]; !ok {
		t.Fatalf("model policy fields were discarded: %#v", activePolicy.Policy)
	}
	if !strings.Contains(activePolicy.Rationale, "revision") || !strings.Contains(activePolicy.Rationale, "继承") {
		t.Fatalf("settings policy rationale does not explain the merge: %q", activePolicy.Rationale)
	}

	for _, want := range []struct {
		kind string
		ttl  float64
	}{
		{kind: "observation", ttl: 7},
		{kind: "unreferenced_media", ttl: 30},
	} {
		var ttl float64
		var autoPurge bool
		if err := db.Pool.QueryRow(ctx, `
			select ttl_days,auto_purge from steward_retention_policies
			where source_pattern='*' and data_kind=$1 and data_level='*'
		`, want.kind).Scan(&ttl, &autoPurge); err != nil {
			t.Fatal(err)
		}
		if ttl != want.ttl || !autoPurge {
			t.Fatalf("retention policy %s ttl=%v auto=%v", want.kind, ttl, autoPurge)
		}
	}

	occurredAt := time.Now().UTC().Add(-time.Hour)
	observation, err := service.CreateObservation(ctx, CreateObservationInput{
		Source: "test:continuous-intelligence", Type: "r53_metadata_probe", Summary: "metadata retention probe",
		DataLevel: DataD2, PermissionLevel: PermissionA1, ContextKey: "settings-e2e", OccurredAt: &occurredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.ExpiresAt == nil || observation.ExpiresAt.Sub(occurredAt.AddDate(0, 0, rawDays)) > time.Second || occurredAt.AddDate(0, 0, rawDays).Sub(*observation.ExpiresAt) > time.Second {
		t.Fatalf("generic observation expiry=%v, want occurred_at+%d days", observation.ExpiresAt, rawDays)
	}

	orphanID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		insert into steward_encrypted_blobs(
			id,observation_id,observation_time,storage_path,mime_type,size_bytes,
			key_id,ciphertext_hash,expires_at,created_at
		) values($1,$2,$3,$4,'image/png',1,'test-key','test-hash',null,now()-interval '31 days')
	`, orphanID, uuid.NewString(), time.Now().UTC().AddDate(0, 0, -31), "settings-e2e-"+orphanID); err != nil {
		t.Fatal(err)
	}
	evaluation, err := service.EvaluateLifecycle(ctx, EvaluateLifecycleInput{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if !hasLifecycleAction(evaluation.Actions, "delete_orphan_blob", orphanID) {
		t.Fatalf("expired unreferenced media was not selected: %+v", evaluation.Actions)
	}

	zero := 0
	retentionRevision := updated.Revision
	if _, err := service.UpdateIntelligenceSettings(ctx, UpdateIntelligenceSettingsInput{
		UnreferencedMediaRetentionDays: &zero,
		ExpectedRevision:               &retentionRevision,
	}); err != nil {
		t.Fatal(err)
	}
	var autoPurge bool
	if err := db.Pool.QueryRow(ctx, `
		select auto_purge from steward_retention_policies
		where source_pattern='*' and data_kind='unreferenced_media' and data_level='*'
	`).Scan(&autoPurge); err != nil {
		t.Fatal(err)
	}
	if autoPurge {
		t.Fatal("zero-day unreferenced media retention must disable automatic cleanup")
	}
	evaluation, err = service.EvaluateLifecycle(ctx, EvaluateLifecycleInput{Limit: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if hasLifecycleAction(evaluation.Actions, "delete_orphan_blob", orphanID) {
		t.Fatal("disabled unreferenced-media cleanup still selected the orphan")
	}
	unchangedPolicy, err := service.GetReminderPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchangedPolicy.ID != activePolicy.ID {
		t.Fatal("retention-only settings update published an unrelated reminder policy version")
	}
}

func hasLifecycleAction(actions []domain.StewardLifecycleAction, action, targetID string) bool {
	for _, item := range actions {
		if item.Action == action && item.TargetID == targetID {
			return true
		}
	}
	return false
}
