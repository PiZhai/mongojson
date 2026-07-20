package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

type ContinuousIntelligenceCycleResult struct {
	SessionsAggregated       int                           `json:"sessions_aggregated"`
	BatchesCreated           int                           `json:"batches_created"`
	BatchesStarted           int                           `json:"batches_started"`
	BatchesDeferred          int                           `json:"batches_deferred"`
	BatchesReconciled        ActivityBatchReconcileResult  `json:"batches_reconciled"`
	IntelligenceJobs         ProfileReportControllerResult `json:"intelligence_jobs"`
	IgnoredReminderFeedback  int                           `json:"ignored_reminder_feedback"`
	ReminderLearningStarted  int                           `json:"reminder_learning_started"`
	LegacyDispatchSuperseded int64                         `json:"legacy_dispatch_superseded"`
}

// RunContinuousIntelligenceCycle is the single R5.3 deterministic controller.
// It only decides what is due, owns leases and projects terminal state; the
// model remains responsible for interpreting activity and choosing business
// actions through the ordinary multi-round Agent loop.
func (s *Service) RunContinuousIntelligenceCycle(ctx context.Context, now time.Time, workerID string, limit int) (ContinuousIntelligenceCycleResult, error) {
	result := ContinuousIntelligenceCycleResult{}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	workerID = defaultString(strings.TrimSpace(workerID), defaultString(strings.TrimSpace(s.runtimeWorkerID), s.agentIDValue())+":continuous-intelligence")
	limit = normalizeLimit(limit, 4, 32)

	settings, err := s.GetIntelligenceSettings(ctx)
	if err != nil {
		return result, err
	}
	if !settings.Enabled {
		return result, nil
	}
	paused, err := s.runtimeExecutionPaused(ctx)
	if err != nil {
		return result, fmt.Errorf("read global execution control: %w", err)
	}
	if paused {
		return result, nil
	}

	var cycleErrors []error
	if settings.Mode == "batch" {
		if superseded, supersedeErr := s.supersedeLegacyModelDispatches(ctx, now); supersedeErr != nil {
			cycleErrors = append(cycleErrors, supersedeErr)
		} else {
			result.LegacyDispatchSuperseded = superseded
		}
		if count, aggregateErr := s.AggregateActivitySessions(ctx, 5000); aggregateErr != nil {
			cycleErrors = append(cycleErrors, aggregateErr)
		} else {
			result.SessionsAggregated = count
			if batches, buildErr := s.BuildDueActivityBatches(ctx, now); buildErr != nil {
				cycleErrors = append(cycleErrors, buildErr)
			} else {
				result.BatchesCreated = len(batches)
			}
		}
		if reconciled, reconcileErr := s.ReconcileActivityBatchEpisodes(ctx, now, limit*4); reconcileErr != nil {
			cycleErrors = append(cycleErrors, reconcileErr)
		} else {
			result.BatchesReconciled = reconciled
		}
		for index := 0; index < limit; index++ {
			batch, claimErr := s.ClaimActivityBatch(ctx, workerID, 2*time.Minute)
			if claimErr != nil {
				cycleErrors = append(cycleErrors, claimErr)
				break
			}
			if batch == nil {
				break
			}
			episode, startErr := s.startActivityBatchEpisode(ctx, *batch)
			if startErr != nil {
				detail := describeAdvisorFailure(startErr)
				next := now.Add(activityBatchRetryDelay(batch.AttemptCount))
				if retryErr := s.RetryActivityBatch(ctx, batch.ID, workerID, batch.ControlGeneration, detail.Code, startErr, next); retryErr != nil {
					cycleErrors = append(cycleErrors, errors.Join(startErr, retryErr))
				} else {
					result.BatchesDeferred++
				}
				continue
			}
			if attachErr := s.AttachActivityBatchEpisode(ctx, batch.ID, workerID, episode.ID, batch.ControlGeneration); attachErr != nil {
				cycleErrors = append(cycleErrors, attachErr)
				continue
			}
			result.BatchesStarted++
		}
	}

	jobs, jobErr := s.RunProfileReportController(ctx, now, workerID+":profile-report", limit)
	if jobErr != nil {
		cycleErrors = append(cycleErrors, jobErr)
	} else {
		result.IntelligenceJobs = jobs
	}
	if inferred, feedbackErr := s.InferIgnoredReminderFeedback(ctx, now, 100); feedbackErr != nil {
		cycleErrors = append(cycleErrors, feedbackErr)
	} else {
		result.IgnoredReminderFeedback = inferred
	}
	if started, learningErr := s.ReconcileReminderFeedbackEpisodes(ctx, limit); learningErr != nil {
		cycleErrors = append(cycleErrors, learningErr)
	} else {
		result.ReminderLearningStarted = started
	}
	return result, errors.Join(cycleErrors...)
}

func (s *Service) supersedeLegacyModelDispatches(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.db.Pool.Exec(ctx, `
		update steward_model_dispatches set status='blocked',last_error=$2,completed_at=$1,
			superseded_at=$1,superseded_reason=$2,updated_at=$1
		where status in ('pending','failed') and superseded_at is null
	`, now, "superseded by R5.3 activity batches")
	if err != nil {
		return 0, fmt.Errorf("supersede legacy observation dispatches: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Service) startActivityBatchEpisode(ctx context.Context, batch ActivityBatch) (domain.StewardAgentEpisode, error) {
	if batch.EpisodeID != nil && strings.TrimSpace(*batch.EpisodeID) != "" {
		return s.resumeActivityBatchEpisode(ctx, strings.TrimSpace(*batch.EpisodeID))
	}
	episodeKey := fmt.Sprintf("activity-batch:%s:revision:%d:attempt:%d", batch.ID, batch.Revision, batch.AttemptCount)
	if existingID, err := s.backgroundEpisodeByKey(ctx, episodeKey); err == nil {
		return s.resumeActivityBatchEpisode(ctx, existingID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentEpisode{}, err
	}
	batchContext, err := s.GetActivityBatchContext(ctx, batch.ID)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	prompt := activityBatchPrompt(batchContext)
	if _, ok := s.autonomyAdvisor().(AgentTurnAdvisor); !ok || !s.autonomyAdvisor().Status().Enabled {
		return domain.StewardAgentEpisode{}, fmt.Errorf("configured model does not support background Agent turns")
	}
	conversation, err := s.ensureProactiveConversation(ctx)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	trigger, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleSystem, prompt, DataD2,
		s.autonomyAdvisor().Status().Model, episodeKey)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	episode, err := s.enqueueBackgroundAgentEpisode(ctx, conversation, trigger,
		fmt.Sprintf("归纳活动批次 %s（%s 至 %s）", batch.ID, batch.WindowStart.Format(time.RFC3339), batch.WindowEnd.Format(time.RFC3339)),
		DataD2, "activity_batch", "activity_batch", batch.ID, episodeKey)
	if err != nil {
		return episode, err
	}
	return episode, nil
}

// resumeActivityBatchEpisode keeps the original transcript and completed tool
// turns when a model failure, process crash or receipt gap makes a batch due
// again. Reusing the Episode is what makes per-tool side effects replay-safe.
func (s *Service) resumeActivityBatchEpisode(ctx context.Context, episodeID string) (domain.StewardAgentEpisode, error) {
	episode, err := s.GetAgentEpisodeOverview(ctx, episodeID, agentEpisodeOverviewTurnLimit)
	if err != nil {
		return episode, err
	}
	switch episode.Status {
	case agentEpisodePaused, agentEpisodeBlocked, agentEpisodeFailed:
		return s.DecideAgentEpisode(ctx, episode.ID, DecideAgentEpisodeInput{Decision: "resume"})
	case agentEpisodeCancelled:
		return episode, fmt.Errorf("activity batch Episode %s was cancelled", episode.ID)
	default:
		return episode, nil
	}
}

func activityBatchPrompt(value ActivityBatchContext) string {
	raw, _ := json.Marshal(value)
	return strings.Join([]string{
		"这是一个后台活动归纳批次。以下内容来自本机持久化事实，不是用户即时对话。",
		"先理解时间块和上下文；需要细节时调用 steward.activity.query 或其他取证工具。",
		"结合当前画像、任务、日程、提醒反馈和完整工具清单，自主决定是否更新近期画像、稳定画像候选、记忆、提醒、任务或执行低风险帮助。",
		"稳定画像必须有多个日期的独立证据；不要把一次窗口标题变成人格结论。不要伪造完成情况。",
		"若当前批次不值得打扰用户，可以调用 steward.stay_silent；沉默不等于丢弃本批次，批次本身仍会持久完成。",
		"活动批次：" + truncateAdvisorText(string(raw), 24000),
	}, "\n")
}
