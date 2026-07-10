package steward

import (
	"context"

	"mongojson/backend/internal/domain"
)

func (s *Service) GetAutonomyOverview(ctx context.Context) (domain.StewardAutonomyOverview, error) {
	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	rules, err := s.ListAutonomyRules(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	proposals, err := s.ListAutonomyProposals(ctx, "", 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	approvals, err := s.ListApprovalRequests(ctx, ApprovalPending, 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	runs, err := s.ListAutonomousRuns(ctx, 20)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	return domain.StewardAutonomyOverview{
		Settings:  settings,
		Advisor:   s.autonomyAdvisor().Status(),
		Actions:   s.autonomyActionCapabilities(),
		Rules:     rules,
		Proposals: proposals,
		Approvals: approvals,
		Runs:      runs,
	}, nil
}

func (s *Service) RunAutonomyCycle(ctx context.Context, limit int) (domain.StewardAutonomyOverview, error) {
	settings, err := s.GetAutonomySettings(ctx)
	if err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if settings.Paused {
		_, _ = s.recordAutonomousRun(ctx, nil, nil, RunModeSimulate, RunBlocked, "autonomy paused", "no proposals created", "resume autonomy to scan")
		return s.GetAutonomyOverview(ctx)
	}
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	if err := s.proposalSources.discover(ctx, limit); err != nil {
		return domain.StewardAutonomyOverview{}, err
	}
	if settings.Mode == AutonomyModeControlled {
		if err := s.executeControlledAutoProposals(ctx, limit); err != nil {
			return domain.StewardAutonomyOverview{}, err
		}
	}
	return s.GetAutonomyOverview(ctx)
}
