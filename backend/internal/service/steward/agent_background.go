package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

// backgroundAgentTrigger identifies internal Episodes whose durable output is
// projected into batches, profile snapshots, reports or tool-catalog state.
// They still use the same Agent loop and evidence pipeline as conversation
// work, but transport progress must not flood the user's chat.
func backgroundAgentTrigger(triggerKind string) bool {
	triggerKind = strings.ToLower(strings.TrimSpace(triggerKind))
	return triggerKind == "activity_batch" ||
		triggerKind == "profile_consolidation" ||
		triggerKind == "reminder_policy_learning" ||
		strings.HasPrefix(triggerKind, "report_") ||
		triggerKind == "proactive_toolsmith"
}

func (s *Service) hideBackgroundConversationMessage(ctx context.Context, triggerKind, messageID string) {
	if s == nil || !backgroundAgentTrigger(triggerKind) || strings.TrimSpace(messageID) == "" {
		return
	}
	// System messages remain durable execution anchors and foreign-key targets,
	// while ListConversationMessages intentionally excludes them.
	_, _ = s.db.Pool.Exec(ctx, `update steward_conversation_messages set role='system' where id=$1`, messageID)
}

// configureBackgroundEpisode switches a freshly persisted Episode to the
// longer R5.3 background budget and records the durable result sink. A zero
// budget remains unlimited, matching the Agent-loop configuration contract.
func (s *Service) configureBackgroundEpisode(ctx context.Context, episodeID, contextType, contextID, idempotencyKey string) error {
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return err
	}
	noProgress := settings.BackgroundNoProgressLimit
	if noProgress <= 0 {
		noProgress = 3
	}
	var deadline any
	if settings.BackgroundMaxDurationSeconds > 0 {
		deadline = time.Now().UTC().Add(time.Duration(settings.BackgroundMaxDurationSeconds) * time.Second)
	}
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_agent_episodes set visibility='background',context_ref_type=$2,context_ref_id=$3,
		result_sink='database',idempotency_key=$4,max_rounds=$5,max_tool_calls=$6,
		max_duration_seconds=$7,no_progress_limit=$8,deadline_at=$9,updated_at=now()
		where id=$1
	`, episodeID, strings.TrimSpace(contextType), strings.TrimSpace(contextID), strings.TrimSpace(idempotencyKey),
		settings.BackgroundMaxRounds, settings.BackgroundMaxToolCalls, settings.BackgroundMaxDurationSeconds,
		noProgress, deadline)
	if err != nil {
		return fmt.Errorf("configure background Agent Episode: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("configure background Agent Episode: episode not found")
	}
	return nil
}

func (s *Service) backgroundEpisodeByKey(ctx context.Context, idempotencyKey string) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `select id::text from steward_agent_episodes where idempotency_key=$1`, strings.TrimSpace(idempotencyKey)).Scan(&id)
	return id, err
}

// enqueueBackgroundAgentEpisode persists the durable recovery anchor before
// the first provider request. The ordinary Agent-loop lease then performs
// round one. A process crash can therefore only delay the model request; it
// cannot lose the job or cause an untracked duplicate request on restart.
func (s *Service) enqueueBackgroundAgentEpisode(
	ctx context.Context,
	conversation domain.StewardConversation,
	trigger domain.StewardConversationMessage,
	goal, level, triggerKind, contextRefType, contextRefID, idempotencyKey string,
) (domain.StewardAgentEpisode, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return domain.StewardAgentEpisode{}, fmt.Errorf("background Agent Episode idempotency key is required")
	}
	if existingID, err := s.backgroundEpisodeByKey(ctx, idempotencyKey); err == nil {
		return s.GetAgentEpisodeOverview(ctx, existingID, agentEpisodeOverviewTurnLimit)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentEpisode{}, err
	}
	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	noProgressLimit := settings.BackgroundNoProgressLimit
	if noProgressLimit <= 0 {
		noProgressLimit = 3
	}
	now := time.Now().UTC()
	episode := domain.StewardAgentEpisode{
		ID: uuid.NewString(), ConversationID: conversation.ID, TriggerMessageID: trigger.ID,
		TriggerKind: defaultString(strings.TrimSpace(triggerKind), "background"), Goal: goal, DataLevel: level,
		Visibility: "background", ContextRefType: strings.TrimSpace(contextRefType), ContextRefID: strings.TrimSpace(contextRefID),
		ResultSink: "database", IdempotencyKey: idempotencyKey, Status: agentEpisodeThinking,
		CurrentRound: 0, ToolCallCount: 0, MaxRounds: settings.BackgroundMaxRounds,
		MaxToolCalls: settings.BackgroundMaxToolCalls, MaxDurationSeconds: settings.BackgroundMaxDurationSeconds,
		NoProgressLimit: noProgressLimit, HydratedToolNames: []string{}, CurrentToolVersions: map[string]string{},
		CatalogGeneration: s.runtimeTools.generationValue(), CreatedAt: now, UpdatedAt: now,
	}
	if episode.MaxDurationSeconds > 0 {
		deadline := now.Add(time.Duration(episode.MaxDurationSeconds) * time.Second)
		episode.DeadlineAt = &deadline
	}
	if err := s.insertAgentEpisode(ctx, episode); err != nil {
		if isAgentEpisodeIdempotencyConflict(err) {
			if existingID, lookupErr := s.backgroundEpisodeByKey(ctx, idempotencyKey); lookupErr == nil {
				return s.GetAgentEpisodeOverview(ctx, existingID, agentEpisodeOverviewTurnLimit)
			}
		}
		return episode, err
	}
	return episode, nil
}
