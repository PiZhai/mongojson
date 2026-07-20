package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const defaultIntelligenceSettingsID = "default"

// IntelligenceSettings controls the durable continuous-intelligence pipeline.
// It is intentionally independent from model provider settings so collection
// keeps running while a provider is unavailable or being reconfigured.
type IntelligenceSettings struct {
	Enabled                        bool      `json:"enabled"`
	Mode                           string    `json:"mode"`
	CaptureProfile                 string    `json:"capture_profile"`
	Timezone                       string    `json:"timezone"`
	ActivitySampleSeconds          int       `json:"activity_sample_seconds"`
	SessionizeIntervalSeconds      int       `json:"sessionize_interval_seconds"`
	BatchIntervalSeconds           int       `json:"batch_interval_seconds"`
	BoundaryGraceSeconds           int       `json:"boundary_grace_seconds"`
	DailyReportFallbackLocal       string    `json:"daily_report_fallback_local"`
	WeeklyReportDay                int       `json:"weekly_report_day"`
	WeeklyReportLocal              string    `json:"weekly_report_local"`
	MonthlyReportLocal             string    `json:"monthly_report_local"`
	RecentProfileDays              int       `json:"recent_profile_days"`
	StableMinEvidenceDays          int       `json:"stable_min_evidence_days"`
	ProfileBootstrapDays           int       `json:"profile_bootstrap_days"`
	ReportCatchupDays              int       `json:"report_catchup_days"`
	BackgroundMaxRounds            int       `json:"background_max_rounds"`
	BackgroundMaxToolCalls         int       `json:"background_max_tool_calls"`
	BackgroundMaxDurationSeconds   int       `json:"background_max_duration_seconds"`
	BackgroundNoProgressLimit      int       `json:"background_no_progress_limit"`
	QuietStartLocal                string    `json:"quiet_start_local"`
	QuietEndLocal                  string    `json:"quiet_end_local"`
	ReminderDailySoftBudget        int       `json:"reminder_daily_soft_budget"`
	ReminderCategorySoftBudget     int       `json:"reminder_category_soft_budget"`
	ReminderCooldownSeconds        int       `json:"reminder_cooldown_seconds"`
	RawMetadataRetentionDays       int       `json:"raw_metadata_retention_days"`
	UnreferencedMediaRetentionDays int       `json:"unreferenced_media_retention_days"`
	Revision                       int64     `json:"revision"`
	CreatedAt                      time.Time `json:"created_at"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

type UpdateIntelligenceSettingsInput struct {
	Enabled                        *bool   `json:"enabled"`
	Mode                           *string `json:"mode"`
	CaptureProfile                 *string `json:"capture_profile"`
	Timezone                       *string `json:"timezone"`
	ActivitySampleSeconds          *int    `json:"activity_sample_seconds"`
	SessionizeIntervalSeconds      *int    `json:"sessionize_interval_seconds"`
	BatchIntervalSeconds           *int    `json:"batch_interval_seconds"`
	BoundaryGraceSeconds           *int    `json:"boundary_grace_seconds"`
	DailyReportFallbackLocal       *string `json:"daily_report_fallback_local"`
	WeeklyReportDay                *int    `json:"weekly_report_day"`
	WeeklyReportLocal              *string `json:"weekly_report_local"`
	MonthlyReportLocal             *string `json:"monthly_report_local"`
	RecentProfileDays              *int    `json:"recent_profile_days"`
	StableMinEvidenceDays          *int    `json:"stable_min_evidence_days"`
	ProfileBootstrapDays           *int    `json:"profile_bootstrap_days"`
	ReportCatchupDays              *int    `json:"report_catchup_days"`
	BackgroundMaxRounds            *int    `json:"background_max_rounds"`
	BackgroundMaxToolCalls         *int    `json:"background_max_tool_calls"`
	BackgroundMaxDurationSeconds   *int    `json:"background_max_duration_seconds"`
	BackgroundNoProgressLimit      *int    `json:"background_no_progress_limit"`
	QuietStartLocal                *string `json:"quiet_start_local"`
	QuietEndLocal                  *string `json:"quiet_end_local"`
	ReminderDailySoftBudget        *int    `json:"reminder_daily_soft_budget"`
	ReminderCategorySoftBudget     *int    `json:"reminder_category_soft_budget"`
	ReminderCooldownSeconds        *int    `json:"reminder_cooldown_seconds"`
	RawMetadataRetentionDays       *int    `json:"raw_metadata_retention_days"`
	UnreferencedMediaRetentionDays *int    `json:"unreferenced_media_retention_days"`
	ExpectedRevision               *int64  `json:"expected_revision,omitempty"`
}

func (s *Service) GetIntelligenceSettings(ctx context.Context) (IntelligenceSettings, error) {
	var out IntelligenceSettings
	err := s.db.Pool.QueryRow(ctx, `
		select enabled,mode,capture_profile,timezone,activity_sample_seconds,
			sessionize_interval_seconds,batch_interval_seconds,boundary_grace_seconds,
			to_char(daily_report_fallback_local,'HH24:MI'),weekly_report_day,
			to_char(weekly_report_local,'HH24:MI'),to_char(monthly_report_local,'HH24:MI'),
			recent_profile_days,stable_min_evidence_days,profile_bootstrap_days,report_catchup_days,
			background_max_rounds,background_max_tool_calls,background_max_duration_seconds,
			background_no_progress_limit,to_char(quiet_start_local,'HH24:MI'),
			to_char(quiet_end_local,'HH24:MI'),reminder_daily_soft_budget,
			reminder_category_soft_budget,reminder_cooldown_seconds,raw_metadata_retention_days,
			unreferenced_media_retention_days,settings_revision,created_at,updated_at
		from steward_intelligence_settings where id=$1`, defaultIntelligenceSettingsID).Scan(
		&out.Enabled, &out.Mode, &out.CaptureProfile, &out.Timezone, &out.ActivitySampleSeconds,
		&out.SessionizeIntervalSeconds, &out.BatchIntervalSeconds, &out.BoundaryGraceSeconds,
		&out.DailyReportFallbackLocal, &out.WeeklyReportDay, &out.WeeklyReportLocal,
		&out.MonthlyReportLocal, &out.RecentProfileDays, &out.StableMinEvidenceDays,
		&out.ProfileBootstrapDays, &out.ReportCatchupDays, &out.BackgroundMaxRounds,
		&out.BackgroundMaxToolCalls, &out.BackgroundMaxDurationSeconds,
		&out.BackgroundNoProgressLimit, &out.QuietStartLocal, &out.QuietEndLocal,
		&out.ReminderDailySoftBudget, &out.ReminderCategorySoftBudget,
		&out.ReminderCooldownSeconds, &out.RawMetadataRetentionDays,
		&out.UnreferencedMediaRetentionDays, &out.Revision, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

func (s *Service) UpdateIntelligenceSettings(ctx context.Context, input UpdateIntelligenceSettingsInput) (IntelligenceSettings, error) {
	current, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return IntelligenceSettings{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return IntelligenceSettings{}, fmt.Errorf("intelligence settings changed concurrently: expected revision %d, current %d", *input.ExpectedRevision, current.Revision)
	}
	baseline := current
	applyIntelligenceSettingsUpdate(&current, input)
	if err := validateIntelligenceSettings(current); err != nil {
		return IntelligenceSettings{}, err
	}
	now := time.Now().UTC()
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return IntelligenceSettings{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		update steward_intelligence_settings set
			enabled=$2,mode=$3,capture_profile=$4,timezone=$5,activity_sample_seconds=$6,
			sessionize_interval_seconds=$7,batch_interval_seconds=$8,boundary_grace_seconds=$9,
			daily_report_fallback_local=$10::time,weekly_report_day=$11,
			weekly_report_local=$12::time,monthly_report_local=$13::time,recent_profile_days=$14,
			stable_min_evidence_days=$15,profile_bootstrap_days=$16,report_catchup_days=$17,
			background_max_rounds=$18,background_max_tool_calls=$19,
			background_max_duration_seconds=$20,background_no_progress_limit=$21,
			quiet_start_local=$22::time,quiet_end_local=$23::time,
			reminder_daily_soft_budget=$24,reminder_category_soft_budget=$25,
			reminder_cooldown_seconds=$26,raw_metadata_retention_days=$27,
			unreferenced_media_retention_days=$28,settings_revision=settings_revision+1,updated_at=$29
		where id=$1 and settings_revision=$30`, defaultIntelligenceSettingsID,
		current.Enabled, current.Mode, current.CaptureProfile, current.Timezone,
		current.ActivitySampleSeconds, current.SessionizeIntervalSeconds,
		current.BatchIntervalSeconds, current.BoundaryGraceSeconds,
		current.DailyReportFallbackLocal, current.WeeklyReportDay, current.WeeklyReportLocal,
		current.MonthlyReportLocal, current.RecentProfileDays, current.StableMinEvidenceDays,
		current.ProfileBootstrapDays, current.ReportCatchupDays, current.BackgroundMaxRounds,
		current.BackgroundMaxToolCalls, current.BackgroundMaxDurationSeconds,
		current.BackgroundNoProgressLimit, current.QuietStartLocal, current.QuietEndLocal,
		current.ReminderDailySoftBudget, current.ReminderCategorySoftBudget,
		current.ReminderCooldownSeconds, current.RawMetadataRetentionDays,
		current.UnreferencedMediaRetentionDays, now, current.Revision)
	if err != nil {
		return IntelligenceSettings{}, err
	}
	if tag.RowsAffected() != 1 {
		return IntelligenceSettings{}, errors.New("intelligence settings changed concurrently")
	}
	if intelligenceReminderSettingsChanged(baseline, current) {
		if err := s.syncReminderPolicyFromIntelligenceSettingsTx(ctx, tx, current, baseline.Revision+1); err != nil {
			return IntelligenceSettings{}, fmt.Errorf("synchronize reminder policy: %w", err)
		}
	}
	if intelligenceRetentionSettingsChanged(baseline, current) {
		if err := syncIntelligenceRetentionPoliciesTx(ctx, tx, current); err != nil {
			return IntelligenceSettings{}, fmt.Errorf("synchronize retention policies: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return IntelligenceSettings{}, err
	}
	return s.GetIntelligenceSettings(ctx)
}

func intelligenceReminderSettingsChanged(before, after IntelligenceSettings) bool {
	return before.QuietStartLocal != after.QuietStartLocal ||
		before.QuietEndLocal != after.QuietEndLocal ||
		before.ReminderDailySoftBudget != after.ReminderDailySoftBudget ||
		before.ReminderCategorySoftBudget != after.ReminderCategorySoftBudget ||
		before.ReminderCooldownSeconds != after.ReminderCooldownSeconds
}

func intelligenceRetentionSettingsChanged(before, after IntelligenceSettings) bool {
	return before.RawMetadataRetentionDays != after.RawMetadataRetentionDays ||
		before.UnreferencedMediaRetentionDays != after.UnreferencedMediaRetentionDays
}

// syncIntelligenceRetentionPoliciesTx maps the two continuous-intelligence
// retention controls onto the existing lifecycle vocabulary. Raw metadata uses
// the generic observation fallback; source/type-specific policies keep taking
// precedence. Orphan media has no observation kind, so it gets a dedicated
// lifecycle-only policy consumed by EvaluateLifecycle.
//
// A value of zero disables automatic purging. The last positive TTL is kept in
// the policy so re-enabling cleanup is reversible and does not manufacture a
// fake zero-day expiry.
func syncIntelligenceRetentionPoliciesTx(ctx context.Context, tx pgx.Tx, settings IntelligenceSettings) error {
	type retentionBinding struct {
		kind        string
		days        int
		fallbackTTL float64
		description string
	}
	bindings := []retentionBinding{
		{
			kind: "observation", days: settings.RawMetadataRetentionDays, fallbackTTL: 30,
			description: "持续智能原始元数据保留；0 天表示关闭自动清理，具体媒体策略优先",
		},
		{
			kind: "unreferenced_media", days: settings.UnreferencedMediaRetentionDays, fallbackTTL: 30,
			description: "持续智能未引用媒体保留；0 天表示关闭自动清理",
		},
	}
	for _, binding := range bindings {
		ttl := binding.fallbackTTL
		if binding.days > 0 {
			ttl = float64(binding.days)
		}
		if _, err := tx.Exec(ctx, `
			insert into steward_retention_policies(
				id,source_pattern,data_kind,data_level,ttl_days,quarantine_days,auto_purge,
				require_preview,protect_user_confirmed,protect_referenced,
				deletion_tombstone_days,description,created_at,updated_at
			) values(gen_random_uuid(),'*',$1,'*',$2,0,$3,false,true,true,90,$4,now(),now())
			on conflict(source_pattern,data_kind,data_level) do update set
				ttl_days=case when excluded.auto_purge then excluded.ttl_days else steward_retention_policies.ttl_days end,
				auto_purge=excluded.auto_purge,
				require_preview=false,
				protect_user_confirmed=true,
				protect_referenced=true,
				description=excluded.description,
				updated_at=now()
		`, binding.kind, ttl, binding.days > 0, binding.description); err != nil {
			return err
		}
	}
	if settings.RawMetadataRetentionDays > 0 {
		// Only shorten generic metadata retention. An explicitly requested
		// earlier expiry remains authoritative, and type-specific policies are
		// intentionally outside this setting's scope.
		if _, err := tx.Exec(ctx, `
			update steward_observations o
			set expires_at=least(
				coalesce(o.expires_at,o.occurred_at+make_interval(days=>$1)),
				o.occurred_at+make_interval(days=>$1)
			)
			where o.retention_locked=false
			  and not exists(
				select 1 from steward_retention_policies p
				where p.data_kind=o.type
				  and o.source like replace(p.source_pattern,'*','%')
			  )
		`, settings.RawMetadataRetentionDays); err != nil {
			return err
		}
	}
	if settings.UnreferencedMediaRetentionDays > 0 {
		if _, err := tx.Exec(ctx, `
			update steward_encrypted_blobs b
			set expires_at=least(
				coalesce(b.expires_at,b.created_at+make_interval(days=>$1)),
				b.created_at+make_interval(days=>$1)
			)
			where not exists(
				select 1 from steward_observations o
				where o.id=b.observation_id and o.occurred_at=b.observation_time
			)
		`, settings.UnreferencedMediaRetentionDays); err != nil {
			return err
		}
	}
	return nil
}

func applyIntelligenceSettingsUpdate(current *IntelligenceSettings, input UpdateIntelligenceSettingsInput) {
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.Mode != nil {
		current.Mode = strings.ToLower(strings.TrimSpace(*input.Mode))
	}
	if input.CaptureProfile != nil {
		current.CaptureProfile = strings.ToLower(strings.TrimSpace(*input.CaptureProfile))
	}
	if input.Timezone != nil {
		current.Timezone = strings.TrimSpace(*input.Timezone)
	}
	if input.ActivitySampleSeconds != nil {
		current.ActivitySampleSeconds = *input.ActivitySampleSeconds
	}
	if input.SessionizeIntervalSeconds != nil {
		current.SessionizeIntervalSeconds = *input.SessionizeIntervalSeconds
	}
	if input.BatchIntervalSeconds != nil {
		current.BatchIntervalSeconds = *input.BatchIntervalSeconds
	}
	if input.BoundaryGraceSeconds != nil {
		current.BoundaryGraceSeconds = *input.BoundaryGraceSeconds
	}
	if input.DailyReportFallbackLocal != nil {
		current.DailyReportFallbackLocal = strings.TrimSpace(*input.DailyReportFallbackLocal)
	}
	if input.WeeklyReportDay != nil {
		current.WeeklyReportDay = *input.WeeklyReportDay
	}
	if input.WeeklyReportLocal != nil {
		current.WeeklyReportLocal = strings.TrimSpace(*input.WeeklyReportLocal)
	}
	if input.MonthlyReportLocal != nil {
		current.MonthlyReportLocal = strings.TrimSpace(*input.MonthlyReportLocal)
	}
	if input.RecentProfileDays != nil {
		current.RecentProfileDays = *input.RecentProfileDays
	}
	if input.StableMinEvidenceDays != nil {
		current.StableMinEvidenceDays = *input.StableMinEvidenceDays
	}
	if input.ProfileBootstrapDays != nil {
		current.ProfileBootstrapDays = *input.ProfileBootstrapDays
	}
	if input.ReportCatchupDays != nil {
		current.ReportCatchupDays = *input.ReportCatchupDays
	}
	if input.BackgroundMaxRounds != nil {
		current.BackgroundMaxRounds = *input.BackgroundMaxRounds
	}
	if input.BackgroundMaxToolCalls != nil {
		current.BackgroundMaxToolCalls = *input.BackgroundMaxToolCalls
	}
	if input.BackgroundMaxDurationSeconds != nil {
		current.BackgroundMaxDurationSeconds = *input.BackgroundMaxDurationSeconds
	}
	if input.BackgroundNoProgressLimit != nil {
		current.BackgroundNoProgressLimit = *input.BackgroundNoProgressLimit
	}
	if input.QuietStartLocal != nil {
		current.QuietStartLocal = strings.TrimSpace(*input.QuietStartLocal)
	}
	if input.QuietEndLocal != nil {
		current.QuietEndLocal = strings.TrimSpace(*input.QuietEndLocal)
	}
	if input.ReminderDailySoftBudget != nil {
		current.ReminderDailySoftBudget = *input.ReminderDailySoftBudget
	}
	if input.ReminderCategorySoftBudget != nil {
		current.ReminderCategorySoftBudget = *input.ReminderCategorySoftBudget
	}
	if input.ReminderCooldownSeconds != nil {
		current.ReminderCooldownSeconds = *input.ReminderCooldownSeconds
	}
	if input.RawMetadataRetentionDays != nil {
		current.RawMetadataRetentionDays = *input.RawMetadataRetentionDays
	}
	if input.UnreferencedMediaRetentionDays != nil {
		current.UnreferencedMediaRetentionDays = *input.UnreferencedMediaRetentionDays
	}
}

func validateIntelligenceSettings(settings IntelligenceSettings) error {
	if settings.Mode != "batch" && settings.Mode != "legacy" {
		return errors.New("mode must be batch or legacy")
	}
	if settings.CaptureProfile != "metadata" && settings.CaptureProfile != "hybrid" && settings.CaptureProfile != "deep" {
		return errors.New("capture_profile must be metadata, hybrid, or deep")
	}
	if settings.Timezone != "" {
		if _, err := time.LoadLocation(settings.Timezone); err != nil {
			return fmt.Errorf("invalid IANA timezone %q", settings.Timezone)
		}
	}
	for name, value := range map[string]int{
		"activity_sample_seconds":      settings.ActivitySampleSeconds,
		"sessionize_interval_seconds":  settings.SessionizeIntervalSeconds,
		"batch_interval_seconds":       settings.BatchIntervalSeconds,
		"boundary_grace_seconds":       settings.BoundaryGraceSeconds,
		"recent_profile_days":          settings.RecentProfileDays,
		"stable_min_evidence_days":     settings.StableMinEvidenceDays,
		"profile_bootstrap_days":       settings.ProfileBootstrapDays,
		"report_catchup_days":          settings.ReportCatchupDays,
		"background_no_progress_limit": settings.BackgroundNoProgressLimit,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be greater than zero", name)
		}
	}
	for name, value := range map[string]int{
		"background_max_rounds":             settings.BackgroundMaxRounds,
		"background_max_tool_calls":         settings.BackgroundMaxToolCalls,
		"background_max_duration_seconds":   settings.BackgroundMaxDurationSeconds,
		"reminder_daily_soft_budget":        settings.ReminderDailySoftBudget,
		"reminder_category_soft_budget":     settings.ReminderCategorySoftBudget,
		"reminder_cooldown_seconds":         settings.ReminderCooldownSeconds,
		"raw_metadata_retention_days":       settings.RawMetadataRetentionDays,
		"unreferenced_media_retention_days": settings.UnreferencedMediaRetentionDays,
	} {
		if value < 0 {
			return fmt.Errorf("%s cannot be negative", name)
		}
	}
	if settings.WeeklyReportDay < 0 || settings.WeeklyReportDay > 6 {
		return errors.New("weekly_report_day must be between 0 and 6")
	}
	for name, value := range map[string]string{
		"daily_report_fallback_local": settings.DailyReportFallbackLocal,
		"weekly_report_local":         settings.WeeklyReportLocal,
		"monthly_report_local":        settings.MonthlyReportLocal,
		"quiet_start_local":           settings.QuietStartLocal,
		"quiet_end_local":             settings.QuietEndLocal,
	} {
		if _, err := time.Parse("15:04", value); err != nil {
			return fmt.Errorf("%s must use HH:MM", name)
		}
	}
	return nil
}

func (s *Service) intelligenceBatchEnabled(ctx context.Context) (bool, error) {
	settings, err := s.GetIntelligenceSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return settings.Enabled && settings.Mode == "batch", nil
}
