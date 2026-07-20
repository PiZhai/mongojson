package steward

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunModelDispatchesSupersedesLegacyQueueInBatchMode(t *testing.T) {
	ctx, db := openAgentLoopCASTestDB(t)
	service := NewService(db)
	if err := service.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	var previousEnabled bool
	var previousMode string
	if err := db.Pool.QueryRow(ctx, `select enabled,mode from steward_intelligence_settings where id=$1`, defaultIntelligenceSettingsID).
		Scan(&previousEnabled, &previousMode); err != nil {
		t.Fatal(err)
	}
	dispatchID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `delete from steward_model_dispatches where id=$1`, dispatchID)
		_, _ = db.Pool.Exec(ctx, `update steward_intelligence_settings set enabled=$2,mode=$3,updated_at=now() where id=$1`,
			defaultIntelligenceSettingsID, previousEnabled, previousMode)
	})
	if _, err := db.Pool.Exec(ctx, `update steward_intelligence_settings set enabled=true,mode='batch',updated_at=now() where id=$1`, defaultIntelligenceSettingsID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := db.Pool.Exec(ctx, `insert into steward_model_dispatches(
		id,observation_id,observation_time,source,data_level,content_mode,status,created_at,updated_at)
		values($1,$2,$3,'test','D0','summary','pending',$3,$3)`, dispatchID, uuid.NewString(), now); err != nil {
		t.Fatal(err)
	}

	items, err := service.RunModelDispatches(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("legacy model dispatch ran in batch mode: %+v", items)
	}
	var status, reason, lastError string
	var attempts int
	var supersededAt, completedAt *time.Time
	if err := db.Pool.QueryRow(ctx, `select status,attempts,superseded_at,completed_at,superseded_reason,last_error
		from steward_model_dispatches where id=$1`, dispatchID).
		Scan(&status, &attempts, &supersededAt, &completedAt, &reason, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != modelDispatchBlocked || attempts != 0 || supersededAt == nil || completedAt == nil ||
		reason != "superseded by R5.3 activity batches" || lastError != reason {
		t.Fatalf("legacy dispatch was not service-level superseded: status=%s attempts=%d superseded=%v completed=%v reason=%q error=%q",
			status, attempts, supersededAt, completedAt, reason, lastError)
	}
}
