package steward

import (
	"context"
	"encoding/json"
	"time"

	"mongojson/backend/internal/domain"
)

// liveAgentContext injects current operational facts into every model round.
// The dedicated tools remain authoritative and can expand these snapshots;
// this compact projection prevents the model from answering questions about
// background work from an old memory or from the mere existence of a tool.
func (s *Service) liveAgentContext(ctx context.Context) []domain.StewardSearchResult {
	now := time.Now().UTC()
	items := []domain.StewardSearchResult{}
	if status, err := s.GetBackgroundStatus(ctx); err == nil {
		items = append(items, domain.StewardSearchResult{
			EntityType: "runtime_state", ID: "continuous-intelligence", Type: "live_status",
			Title: "持续采集与后台任务实时状态", Summary: compactAgentContextJSON(status, 10000),
			Status: status.State, Source: "live_database", UpdatedAt: status.CheckedAt,
		})
	}
	if profile, err := s.GetProfileView(ctx); err == nil {
		items = append(items, domain.StewardSearchResult{
			EntityType: "user_profile", ID: "merged", Type: "live_profile",
			Title: "当前近期、稳定与显式用户画像", Summary: compactAgentContextJSON(profile, 12000),
			Status: "current", Source: "profile_projection", UpdatedAt: now,
		})
	}
	if reports, err := s.ListReports(ctx, "", 3, false); err == nil && len(reports) > 0 {
		items = append(items, domain.StewardSearchResult{
			EntityType: "activity_reports", ID: "latest", Type: "live_reports",
			Title: "最近持久化报告", Summary: compactAgentContextJSON(reports, 8000),
			Status: "current", Source: "report_store", UpdatedAt: now,
		})
	}
	if reminderContext, err := s.reminderContextSnapshot(ctx); err == nil {
		items = append(items, domain.StewardSearchResult{
			EntityType: "reminder_intelligence", ID: "current", Type: "live_reminder_context",
			Title: "当前提醒策略、近期反馈与高接收时段", Summary: compactAgentContextJSON(reminderContext, 10000),
			Status: "current", Source: "reminder_learning", UpdatedAt: now,
		})
	}
	return items
}

func compactAgentContextJSON(value any, limit int) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return truncateAdvisorText(string(raw), limit)
}
