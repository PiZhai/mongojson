package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

const (
	defaultAutonomyRetryMaxAttempts = 3
	defaultAutonomyRetryBackoff     = 5 * time.Minute
	defaultAutonomyRetryMaxBackoff  = time.Hour
)

type autonomyRetryPolicy struct {
	maxAttempts int
	backoff     time.Duration
	maxBackoff  time.Duration
	now         func() time.Time
}

type autonomyRetryRecord struct {
	failedAttempts int
	lastFailedAt   *time.Time
}

func autonomyRetryPolicyFromEnv() autonomyRetryPolicy {
	maxAttempts := intEnv("STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS", defaultAutonomyRetryMaxAttempts)
	if maxAttempts <= 0 || maxAttempts > 20 {
		maxAttempts = defaultAutonomyRetryMaxAttempts
	}
	backoff := durationEnv("STEWARD_AUTONOMY_RETRY_BACKOFF", defaultAutonomyRetryBackoff)
	if backoff <= 0 || backoff > 24*time.Hour {
		backoff = defaultAutonomyRetryBackoff
	}
	maxBackoff := durationEnv("STEWARD_AUTONOMY_RETRY_MAX_BACKOFF", defaultAutonomyRetryMaxBackoff)
	if maxBackoff < backoff || maxBackoff > 24*time.Hour {
		maxBackoff = maxDuration(backoff, defaultAutonomyRetryMaxBackoff)
	}
	return autonomyRetryPolicy{
		maxAttempts: maxAttempts,
		backoff:     backoff,
		maxBackoff:  maxBackoff,
		now:         time.Now,
	}
}

func (p autonomyRetryPolicy) delay(failedAttempts int) time.Duration {
	if failedAttempts <= 0 {
		return 0
	}
	delay := p.backoff
	for attempt := 1; attempt < failedAttempts; attempt++ {
		if delay >= p.maxBackoff/2 {
			return p.maxBackoff
		}
		delay *= 2
	}
	if delay > p.maxBackoff {
		return p.maxBackoff
	}
	return delay
}

func (p autonomyRetryPolicy) status() domain.StewardAutonomyRetryPolicy {
	return domain.StewardAutonomyRetryPolicy{
		MaxAttempts: p.maxAttempts,
		Backoff:     p.backoff.String(),
		MaxBackoff:  p.maxBackoff.String(),
	}
}

func (p autonomyRetryPolicy) apply(proposal *domain.StewardAutonomyProposal, record autonomyRetryRecord) {
	if proposal == nil {
		return
	}
	proposal.FailedAttempts = record.failedAttempts
	proposal.RetryExhausted = record.failedAttempts >= p.maxAttempts
	proposal.RetryEligible = record.failedAttempts > 0 && proposal.Status != ProposalExecuted && proposal.Status != ProposalDismissed
	proposal.AutoRetryAt = nil
	if proposal.Policy != AutonomyPolicyAuto || record.lastFailedAt == nil || proposal.RetryExhausted {
		return
	}
	retryAt := record.lastFailedAt.UTC().Add(p.delay(record.failedAttempts))
	proposal.AutoRetryAt = &retryAt
}

func (p autonomyRetryPolicy) automaticRetryReady(proposal domain.StewardAutonomyProposal) bool {
	if proposal.Policy != AutonomyPolicyAuto || proposal.RetryExhausted {
		return false
	}
	if proposal.AutoRetryAt == nil {
		return true
	}
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	return !now.UTC().Before(proposal.AutoRetryAt.UTC())
}

func (s *Service) populateAutonomyRetryState(ctx context.Context, proposal *domain.StewardAutonomyProposal) error {
	if proposal == nil || strings.TrimSpace(proposal.ID) == "" {
		return nil
	}
	proposals := []domain.StewardAutonomyProposal{*proposal}
	if err := s.populateAutonomyRetryStates(ctx, proposals); err != nil {
		return err
	}
	*proposal = proposals[0]
	return nil
}

func (s *Service) populateAutonomyRetryStates(ctx context.Context, proposals []domain.StewardAutonomyProposal) error {
	if len(proposals) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(proposals))
	for index := range proposals {
		id, err := uuid.Parse(strings.TrimSpace(proposals[index].ID))
		if err != nil {
			return fmt.Errorf("parse autonomy proposal id for retry state: %w", err)
		}
		ids = append(ids, id)
	}
	rows, err := s.db.Pool.Query(ctx, `
		select proposal_id::text, count(*)::int, max(created_at)
		from steward_autonomous_runs
		where proposal_id = any($1) and mode = $2 and status = $3
		group by proposal_id
	`, ids, RunModeExecute, RunFailed)
	if err != nil {
		return fmt.Errorf("read autonomy retry state: %w", err)
	}
	defer rows.Close()
	records := make(map[string]autonomyRetryRecord, len(proposals))
	for rows.Next() {
		var proposalID string
		var record autonomyRetryRecord
		if err := rows.Scan(&proposalID, &record.failedAttempts, &record.lastFailedAt); err != nil {
			return fmt.Errorf("scan autonomy retry state: %w", err)
		}
		records[proposalID] = record
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read autonomy retry states: %w", err)
	}
	for index := range proposals {
		s.retryPolicy.apply(&proposals[index], records[proposals[index].ID])
	}
	return nil
}

func (s *Service) latestAutonomyExecutionFailed(ctx context.Context, proposalID string) (bool, error) {
	var status string
	err := s.db.Pool.QueryRow(ctx, `
		select status
		from steward_autonomous_runs
		where proposal_id = $1 and mode = $2
		order by created_at desc
		limit 1
	`, proposalID, RunModeExecute).Scan(&status)
	if err != nil {
		return false, fmt.Errorf("read latest autonomy execution: %w", err)
	}
	return status == RunFailed, nil
}

func maxDuration(a time.Duration, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
