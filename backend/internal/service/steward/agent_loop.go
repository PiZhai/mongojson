package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"mongojson/backend/internal/domain"
)

const (
	agentEpisodeThinking      = "thinking"
	agentEpisodeExecuting     = "executing"
	agentEpisodeAwaitingInput = "awaiting_input"
	agentEpisodePaused        = "paused"
	agentEpisodeCompleted     = "completed"
	agentEpisodeFailed        = "failed"
	agentEpisodeCancelled     = "cancelled"
	agentEpisodeBlocked       = "blocked"

	agentAskUserTool    = "steward.ask_user"
	agentStaySilentTool = "steward.stay_silent"

	agentModelFailureLimit         = 5
	agentTechnicalRetryDelay       = 5 * time.Second
	agentTranscriptRecentTurnLimit = 20
	agentTranscriptSummaryBudget   = 16000
	agentTranscriptRecentBudget    = 96000
)

var errAgentEpisodeClaimLost = errors.New("agent episode claim lost")

// agentModelTurnError marks failures from the actual model-decision stage.
// Database, catalog, dispatch and tool-runtime failures must not consume the
// model failure budget.
type agentModelTurnError struct {
	cause error
}

func (e *agentModelTurnError) Error() string {
	if e == nil || e.cause == nil {
		return "agent model turn failed"
	}
	return e.cause.Error()
}

func (e *agentModelTurnError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type DecideAgentEpisodeInput struct {
	Decision       string `json:"decision"`
	TargetDeviceID string `json:"target_device_id"`
}

type agentLoopLimits struct {
	MaxRounds, MaxToolCalls, MaxDurationSeconds, NoProgressLimit int
}

func encodeAgentJSON(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(raw)
}

func normalizeAgentLoopLimits(values modelSettingsValues) agentLoopLimits {
	limits := agentLoopLimits{values.agentMaxRounds, values.agentMaxToolCalls, values.agentMaxDurationSeconds, values.agentNoProgressLimit}
	if limits.NoProgressLimit <= 0 {
		limits.NoProgressLimit = 3
	}
	return limits
}

func nextValidAgentTurn(ctx context.Context, advisor AgentTurnAdvisor, input AgentTurnInput) (AgentTurnDecision, error) {
	for attempt := 0; attempt < 2; attempt++ {
		decision, err := advisor.NextTurn(ctx, input)
		if err != nil {
			// A second identical request can hide the provider's original error and,
			// after the breaker opens, replace it with a less useful circuit-open
			// message. Only an otherwise successful but empty response is retried.
			return AgentTurnDecision{}, err
		}
		if strings.TrimSpace(decision.Content) != "" || len(decision.ToolCalls) > 0 {
			return decision, nil
		}
	}
	return AgentTurnDecision{}, fmt.Errorf("agent model returned neither text nor tool calls")
}

func (s *Service) startAgentEpisode(ctx context.Context, conversation domain.StewardConversation, trigger domain.StewardConversationMessage, goal, level, triggerKind string, decision AgentTurnDecision) (domain.StewardConversationMessage, domain.StewardAgentEpisode, error) {
	values, err := s.loadModelSettings(ctx)
	if err != nil {
		return domain.StewardConversationMessage{}, domain.StewardAgentEpisode{}, err
	}
	limits := normalizeAgentLoopLimits(values)
	now := time.Now().UTC()
	episode := domain.StewardAgentEpisode{
		ID: uuid.NewString(), ConversationID: conversation.ID, TriggerMessageID: trigger.ID,
		TriggerKind: defaultString(triggerKind, "conversation"), Goal: goal, DataLevel: level,
		Status: agentEpisodeThinking, CurrentRound: 1, ToolCallCount: countExternalAgentCalls(decision.ToolCalls),
		MaxRounds: limits.MaxRounds, MaxToolCalls: limits.MaxToolCalls, MaxDurationSeconds: limits.MaxDurationSeconds,
		NoProgressLimit: limits.NoProgressLimit, CreatedAt: now, UpdatedAt: now,
		HydratedToolNames: []string{}, CurrentToolVersions: map[string]string{}, CatalogGeneration: s.runtimeTools.generationValue(),
	}
	if limits.MaxToolCalls > 0 && episode.ToolCallCount > limits.MaxToolCalls {
		return domain.StewardConversationMessage{}, episode, fmt.Errorf("model requested %d tools, exceeding the configured limit %d", episode.ToolCallCount, limits.MaxToolCalls)
	}
	if limits.MaxDurationSeconds > 0 {
		deadline := now.Add(time.Duration(limits.MaxDurationSeconds) * time.Second)
		episode.DeadlineAt = &deadline
	}
	if err := s.insertAgentEpisode(ctx, episode); err != nil {
		return domain.StewardConversationMessage{}, episode, err
	}
	turn := domain.StewardAgentTurn{
		ID: uuid.NewString(), EpisodeID: episode.ID, RoundIndex: 1, Status: "model_complete",
		AssistantContent: decision.Content, ReasoningContent: decision.ReasoningContent, ToolCalls: decision.ToolCalls,
		Provider: s.autonomyAdvisor().Status().Provider, Model: s.autonomyAdvisor().Status().Model,
		ProviderResponseID: decision.ProviderResponseID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertAgentTurn(ctx, turn); err != nil {
		_ = s.failAgentEpisode(ctx, episode.ID, err)
		return domain.StewardConversationMessage{}, episode, err
	}
	message, err := s.applyAgentTurnDecision(ctx, conversation, trigger, episode, turn)
	if err != nil {
		// The model decision is already durable. Convert a local preflight or
		// dispatch rejection into a tool result so the next model turn can choose
		// another route, just as an interactive tool agent would. If persistence
		// itself is unavailable this recovery also fails and remains terminal.
		message, recoveryErr := s.recordUndispatchedAgentTurnError(ctx, episode, turn, err)
		if recoveryErr != nil {
			_ = s.failAgentEpisode(ctx, episode.ID, errors.Join(err, recoveryErr))
			return message, episode, errors.Join(err, recoveryErr)
		}
		err = nil
	}
	episode, _ = s.GetAgentEpisodeOverview(ctx, episode.ID, agentEpisodeOverviewTurnLimit)
	if message.ID != "" {
		message.Episodes = []domain.StewardAgentEpisode{episode}
	}
	return message, episode, nil
}

func countExternalAgentCalls(calls []domain.StewardAgentToolCall) int {
	count := 0
	for _, call := range calls {
		if call.ToolName != agentAskUserTool && call.ToolName != agentStaySilentTool {
			count++
		}
	}
	return count
}

func (s *Service) insertAgentEpisode(ctx context.Context, item domain.StewardAgentEpisode) error {
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_agent_episodes (
			id,conversation_id,trigger_message_id,trigger_kind,goal,data_level,status,current_round,tool_call_count,
			max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,no_progress_count,target_device_id,
			control_generation,created_at,updated_at,deadline_at,hydrated_tool_names,catalog_generation,current_tool_versions
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb,$21,$22::jsonb)
	`, item.ID, item.ConversationID, item.TriggerMessageID, item.TriggerKind, item.Goal, item.DataLevel, item.Status,
		item.CurrentRound, item.ToolCallCount, item.MaxRounds, item.MaxToolCalls, item.MaxDurationSeconds,
		item.NoProgressLimit, item.NoProgressCount, item.TargetDeviceID, item.ControlGeneration,
		item.CreatedAt, item.UpdatedAt, item.DeadlineAt, encodeAgentJSON(item.HydratedToolNames, "[]"), item.CatalogGeneration, encodeAgentJSON(item.CurrentToolVersions, "{}"))
	return err
}

func (s *Service) insertAgentTurn(ctx context.Context, item domain.StewardAgentTurn) error {
	return insertAgentTurnWithExecer(ctx, s.db.Pool, item)
}

type agentTurnExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertAgentTurnWithExecer(ctx context.Context, execer agentTurnExecer, item domain.StewardAgentTurn) error {
	if item.ToolCalls == nil {
		item.ToolCalls = []domain.StewardAgentToolCall{}
	}
	if item.ToolResults == nil {
		item.ToolResults = []domain.StewardAgentToolResult{}
	}
	calls, _ := json.Marshal(item.ToolCalls)
	results, _ := json.Marshal(item.ToolResults)
	_, err := execer.Exec(ctx, `
		insert into steward_agent_turns (
			id,episode_id,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
			provider,model,provider_response_id,execution_id,failure_summary,created_at,updated_at,completed_at
		) values ($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11,nullif($12,'')::uuid,$13,$14,$15,$16)
	`, item.ID, item.EpisodeID, item.RoundIndex, item.Status, item.AssistantContent, item.ReasoningContent,
		string(calls), string(results), item.Provider, item.Model, item.ProviderResponseID, item.ExecutionID,
		item.FailureSummary, item.CreatedAt, item.UpdatedAt, item.CompletedAt)
	return err
}

func (s *Service) GetAgentEpisode(ctx context.Context, id string) (domain.StewardAgentEpisode, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id::text,conversation_id::text,trigger_message_id::text,coalesce(progress_message_id::text,''),
		       coalesce(final_message_id::text,''),trigger_kind,goal,data_level,status,current_round,tool_call_count,
		       max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,no_progress_count,model_failure_count,target_device_id,
		       coalesce(active_execution_id::text,''),control_generation,failure_summary,last_result_summary,
		       hydrated_tool_names,catalog_generation,current_tool_versions,created_at,updated_at,deadline_at,completed_at
		from steward_agent_episodes where id=$1
	`, id)
	var item domain.StewardAgentEpisode
	var hydrated, versions []byte
	if err := row.Scan(&item.ID, &item.ConversationID, &item.TriggerMessageID, &item.ProgressMessageID,
		&item.FinalMessageID, &item.TriggerKind, &item.Goal, &item.DataLevel, &item.Status, &item.CurrentRound,
		&item.ToolCallCount, &item.MaxRounds, &item.MaxToolCalls, &item.MaxDurationSeconds, &item.NoProgressLimit,
		&item.NoProgressCount, &item.ModelFailureCount, &item.TargetDeviceID, &item.ActiveExecutionID, &item.ControlGeneration,
		&item.FailureSummary, &item.LastResultSummary, &hydrated, &item.CatalogGeneration, &versions, &item.CreatedAt, &item.UpdatedAt, &item.DeadlineAt,
		&item.CompletedAt); err != nil {
		return item, err
	}
	_ = json.Unmarshal(hydrated, &item.HydratedToolNames)
	_ = json.Unmarshal(versions, &item.CurrentToolVersions)
	turns, err := s.listAgentTurns(ctx, item.ID)
	if err != nil {
		return item, err
	}
	item.Turns = turns
	return item, nil
}

func (s *Service) listAgentTurns(ctx context.Context, episodeID string) ([]domain.StewardAgentTurn, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns where episode_id=$1 order by round_index
	`, episodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardAgentTurn{}
	for rows.Next() {
		var item domain.StewardAgentTurn
		var calls, results []byte
		if err := rows.Scan(&item.ID, &item.EpisodeID, &item.RoundIndex, &item.Status, &item.AssistantContent,
			&item.ReasoningContent, &calls, &results, &item.Provider, &item.Model, &item.ProviderResponseID,
			&item.ExecutionID, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(calls, &item.ToolCalls)
		_ = json.Unmarshal(results, &item.ToolResults)
		if item.ToolCalls == nil {
			item.ToolCalls = []domain.StewardAgentToolCall{}
		}
		if item.ToolResults == nil {
			item.ToolResults = []domain.StewardAgentToolResult{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) listAgentEpisodesForMessage(ctx context.Context, messageID string) ([]domain.StewardAgentEpisode, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text from steward_agent_episodes
		where progress_message_id=$1 or final_message_id=$1
		order by created_at
	`, messageID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	items := make([]domain.StewardAgentEpisode, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetAgentEpisodeOverview(ctx, id, agentEpisodeOverviewTurnLimit)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) applyAgentTurnDecision(ctx context.Context, conversation domain.StewardConversation, trigger domain.StewardConversationMessage, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn) (domain.StewardConversationMessage, error) {
	if len(turn.ToolCalls) == 0 {
		return s.finishAgentEpisode(ctx, episode, turn, defaultString(strings.TrimSpace(turn.AssistantContent), "任务已经结束。"), false)
	}
	controlCalls := 0
	for _, call := range turn.ToolCalls {
		if call.ToolName == agentAskUserTool || call.ToolName == agentStaySilentTool {
			controlCalls++
		}
	}
	if controlCalls > 0 && len(turn.ToolCalls) != 1 {
		return s.rejectAgentControlBatch(ctx, episode, turn)
	}
	if len(turn.ToolCalls) == 1 && turn.ToolCalls[0].ToolName == agentAskUserTool {
		question, _ := turn.ToolCalls[0].Arguments["question"].(string)
		if strings.TrimSpace(question) == "" {
			return domain.StewardConversationMessage{}, fmt.Errorf("steward.ask_user requires question")
		}
		message, err := s.insertConversationMessage(ctx, episode.ConversationID, conversationRoleAssistant, question, episode.DataLevel, turn.Model, "agent-awaiting-input:"+episode.ID)
		if err == nil {
			now := time.Now().UTC()
			_, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='waiting_input',completed_at=$2,updated_at=$2 where id=$1`, turn.ID, now)
			if err == nil {
				_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='awaiting_input',progress_message_id=$2,updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, episode.ID, message.ID, now)
			}
		}
		return message, err
	}
	if len(turn.ToolCalls) == 1 && turn.ToolCalls[0].ToolName == agentStaySilentTool {
		if episode.TriggerKind == "conversation" {
			return domain.StewardConversationMessage{}, fmt.Errorf("steward.stay_silent is only valid for proactive episodes")
		}
		return s.finishAgentEpisode(ctx, episode, turn, "", true)
	}
	return s.dispatchAgentTurn(ctx, conversation, trigger, episode, turn)
}

func (s *Service) rejectAgentControlBatch(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn) (domain.StewardConversationMessage, error) {
	const protocolError = "协议错误：steward.ask_user 和 steward.stay_silent 必须单独调用；本批工具未执行，请重新决策"
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for _, call := range turn.ToolCalls {
		results = append(results, domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Error: protocolError})
	}
	encoded, _ := json.Marshal(results)
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,failure_summary=$3,completed_at=$4,updated_at=$4 where id=$1`, turn.ID, string(encoded), protocolError, now); err != nil {
		return domain.StewardConversationMessage{}, err
	}
	message, err := s.insertConversationMessage(ctx, episode.ConversationID, conversationRoleAssistant, "模型正在修正无效的控制工具组合。", episode.DataLevel, turn.Model, "agent-protocol-retry:"+episode.ID)
	if err != nil {
		return message, err
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',progress_message_id=$2,last_result_summary=$3,
		tool_call_count=greatest(0,tool_call_count-$5),updated_at=$4,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`,
		episode.ID, message.ID, protocolError, now, countExternalAgentCalls(turn.ToolCalls))
	return message, err
}

func (s *Service) dispatchAgentTurn(ctx context.Context, conversation domain.StewardConversation, trigger domain.StewardConversationMessage, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn) (domain.StewardConversationMessage, error) {
	if message, found, err := s.resumeExistingAgentTurnExecution(ctx, episode, turn); err != nil || found {
		return message, err
	}
	steps := make([]CreateAgentRunStepInput, 0, len(turn.ToolCalls))
	stepTargets := map[string]string{}
	uniqueTargets := map[string]conversationExecutionTarget{}
	for index, call := range turn.ToolCalls {
		key := fmt.Sprintf("tool_%d", index+1)
		steps = append(steps, CreateAgentRunStepInput{Key: key, Title: call.ToolName, ToolName: call.ToolName, Arguments: call.Arguments})
		requested := defaultString(call.TargetDeviceID, episode.TargetDeviceID)
		target, _, err := s.selectConversationExecutionTarget(ctx, episode.Goal, requested)
		if err != nil {
			return domain.StewardConversationMessage{}, err
		}
		stepTargets[key] = target.ID
		uniqueTargets[target.ID] = target
	}
	target := conversationExecutionTarget{ID: s.agentIDValue(), Name: "本机"}
	for _, value := range uniqueTargets {
		target = value
		break
	}
	if len(uniqueTargets) > 1 {
		target = conversationExecutionTarget{ID: "multiple", Name: "多设备", Remote: true}
	}
	plan := RuntimePlanDraft{Summary: episode.Goal, Steps: steps, Planner: "agent-loop", PlannerVersion: "4.9.0", ReasoningContent: turn.ReasoningContent}
	idempotencyKey := fmt.Sprintf("agent:%s:%d:batch", episode.ID, turn.RoundIndex)
	if len(turn.ToolCalls) == 1 {
		idempotencyKey = fmt.Sprintf("agent:%s:%d:%s", episode.ID, turn.RoundIndex, turn.ToolCalls[0].ID)
	}
	message, execution, found, err := s.ensureAgentTurnExecution(ctx, episode, turn, func() (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
		return s.createConversationExecutionFromPlanLinked(ctx, conversation, trigger, episode.Goal, episode.DataLevel, target, plan, agentExecutionLink{
			EpisodeID: episode.ID, TurnID: turn.ID, RoundIndex: turn.RoundIndex, IdempotencyKey: idempotencyKey, StepTargets: stepTargets,
		})
	})
	if err != nil {
		return message, err
	}
	if !found {
		return message, fmt.Errorf("agent turn %s dispatch did not create a linked execution", turn.ID)
	}
	if strings.TrimSpace(turn.AssistantContent) != "" {
		_, _ = s.db.Pool.Exec(ctx, `update steward_conversation_messages set content=$2 where id=$1`, message.ID, truncateAdvisorText(turn.AssistantContent, 8000))
		message.Content = truncateAdvisorText(turn.AssistantContent, 8000)
	}
	return s.reconcileLinkedAgentTurnExecution(ctx, episode, message, execution)
}

func (s *Service) resumeExistingAgentTurnExecution(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn) (domain.StewardConversationMessage, bool, error) {
	message, execution, found, err := s.ensureAgentTurnExecution(ctx, episode, turn, nil)
	if err != nil || !found {
		return message, found, err
	}
	message, err = s.reconcileLinkedAgentTurnExecution(ctx, episode, message, execution)
	return message, true, err
}

type agentTurnExecutionFactory func() (domain.StewardConversationMessage, domain.StewardConversationExecution, error)

// ensureAgentTurnExecution serializes dispatch and recovery for one durable
// model-complete turn. The shared row locks keep pause/cancel updates ordered
// around child creation while remaining compatible with the foreign-key locks
// taken when the linked conversation execution is inserted.
func (s *Service) ensureAgentTurnExecution(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn, factory agentTurnExecutionFactory) (domain.StewardConversationMessage, domain.StewardConversationExecution, bool, error) {
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardConversationMessage{}, domain.StewardConversationExecution{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, "steward-agent-turn-dispatch:"+turn.ID); err != nil {
		return domain.StewardConversationMessage{}, domain.StewardConversationExecution{}, false, err
	}
	var episodeStatus, activeExecutionID string
	var controlGeneration int64
	if err = tx.QueryRow(ctx, `select status,control_generation,coalesce(active_execution_id::text,'')
		from steward_agent_episodes where id=$1 for share`, episode.ID).Scan(&episodeStatus, &controlGeneration, &activeExecutionID); err != nil {
		return domain.StewardConversationMessage{}, domain.StewardConversationExecution{}, false, err
	}
	var turnStatus, turnExecutionID string
	if err = tx.QueryRow(ctx, `select status,coalesce(execution_id::text,'') from steward_agent_turns
		where id=$1 and episode_id=$2 for share`, turn.ID, episode.ID).Scan(&turnStatus, &turnExecutionID); err != nil {
		return domain.StewardConversationMessage{}, domain.StewardConversationExecution{}, false, err
	}

	executionID, messageID, findErr := findAgentTurnExecution(ctx, tx, turn.ID, turnExecutionID, activeExecutionID)
	if findErr != nil && !errors.Is(findErr, pgx.ErrNoRows) {
		return domain.StewardConversationMessage{}, domain.StewardConversationExecution{}, false, findErr
	}
	var message domain.StewardConversationMessage
	var createErr error
	if executionID == "" {
		if factory == nil {
			return message, domain.StewardConversationExecution{}, false, nil
		}
		if episodeStatus != agentEpisodeThinking || controlGeneration != episode.ControlGeneration || activeExecutionID != "" || turnStatus != "model_complete" || turnExecutionID != "" {
			return message, domain.StewardConversationExecution{}, false, errAgentEpisodeClaimLost
		}
		var created domain.StewardConversationExecution
		message, created, createErr = factory()
		executionID, messageID, findErr = findAgentTurnExecution(ctx, tx, turn.ID, created.ID, activeExecutionID)
		if findErr != nil {
			if createErr != nil {
				return message, created, false, createErr
			}
			if errors.Is(findErr, pgx.ErrNoRows) {
				return message, created, false, fmt.Errorf("agent turn %s dispatch created no linked execution", turn.ID)
			}
			return message, created, false, findErr
		}
	}
	if message.ID == "" {
		message = domain.StewardConversationMessage{ID: messageID, ConversationID: episode.ConversationID}
	}

	linkableTurn := turnStatus == "model_complete" || turnStatus == "tools_running"
	linkableEpisode := controlGeneration == episode.ControlGeneration &&
		(episodeStatus == agentEpisodeThinking || episodeStatus == agentEpisodeExecuting) &&
		(activeExecutionID == "" || activeExecutionID == executionID) &&
		(turnExecutionID == "" || turnExecutionID == executionID)
	if linkableTurn && linkableEpisode {
		now := time.Now().UTC()
		tag, updateErr := tx.Exec(ctx, `update steward_agent_turns set status='tools_running',execution_id=$2,updated_at=$3
			where id=$1 and episode_id=$4 and status in ('model_complete','tools_running')
			and (execution_id is null or execution_id=$2)`, turn.ID, executionID, now, episode.ID)
		if updateErr != nil {
			return message, domain.StewardConversationExecution{}, true, updateErr
		}
		if tag.RowsAffected() != 1 {
			return message, domain.StewardConversationExecution{}, true, errAgentEpisodeClaimLost
		}
		tag, updateErr = tx.Exec(ctx, `update steward_agent_episodes set status='executing',active_execution_id=$2,progress_message_id=$3,
			updated_at=$4,lease_owner='',lease_expires_at=null,version=version+1
			where id=$1 and control_generation=$5 and status in ('thinking','executing')
			and (active_execution_id is null or active_execution_id=$2)`, episode.ID, executionID, messageID, now, controlGeneration)
		if updateErr != nil {
			return message, domain.StewardConversationExecution{}, true, updateErr
		}
		if tag.RowsAffected() != 1 {
			return message, domain.StewardConversationExecution{}, true, errAgentEpisodeClaimLost
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return message, domain.StewardConversationExecution{}, true, err
	}
	execution, err := s.getConversationExecution(ctx, executionID)
	if err != nil {
		return message, domain.StewardConversationExecution{}, true, err
	}
	return message, execution, true, createErr
}

type agentTurnExecutionQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func findAgentTurnExecution(ctx context.Context, queryer agentTurnExecutionQueryer, turnID, preferredExecutionID, activeExecutionID string) (string, string, error) {
	var executionID, messageID string
	err := queryer.QueryRow(ctx, `select id::text,message_id::text from steward_conversation_executions
		where turn_id=$1 order by
			case when id=nullif($2,'')::uuid then 0 when id=nullif($3,'')::uuid then 1 else 2 end,
			created_at desc,id desc limit 1`, turnID, preferredExecutionID, activeExecutionID).Scan(&executionID, &messageID)
	return executionID, messageID, err
}

func (s *Service) reconcileLinkedAgentTurnExecution(ctx context.Context, episode domain.StewardAgentEpisode, message domain.StewardConversationMessage, execution domain.StewardConversationExecution) (domain.StewardConversationMessage, error) {
	// If the child execution reached a terminal state before the episode link
	// was restored, refresh may have had no state transition to trigger the
	// normal completion callback. Reconcile it explicitly.
	if runtimeRunTerminal(execution.Status) {
		if refreshed, getErr := s.getAgentEpisodeState(ctx, episode.ID); getErr == nil && refreshed.Status == agentEpisodeExecuting && refreshed.ActiveExecutionID == execution.ID {
			results, resultErr := s.agentConversationExecutionResults(ctx, execution)
			if resultErr != nil {
				return message, resultErr
			}
			if completeErr := s.completeAgentEpisodeExecution(ctx, execution, results); completeErr != nil {
				return message, completeErr
			}
		}
	}
	message.Executions = []domain.StewardConversationExecution{execution}
	return message, nil
}

func (s *Service) agentConversationExecutionResults(ctx context.Context, execution domain.StewardConversationExecution) ([]ConversationToolResult, error) {
	switch {
	case execution.Kind == conversationExecutionRun && execution.RunID != "":
		run, err := s.GetAgentRun(ctx, execution.RunID)
		if err != nil {
			return nil, err
		}
		return s.conversationRunToolResults(ctx, run), nil
	case execution.Kind == conversationExecutionOrchestration && execution.OrchestrationID != "":
		orchestration, err := s.GetOrchestration(ctx, execution.OrchestrationID)
		if err != nil {
			return nil, err
		}
		return s.conversationOrchestrationToolResults(ctx, orchestration), nil
	default:
		return nil, fmt.Errorf("agent execution %s has no runtime subject", execution.ID)
	}
}

func pendingModelCompleteAgentTurn(episode domain.StewardAgentEpisode) (domain.StewardAgentTurn, bool) {
	for index := len(episode.Turns) - 1; index >= 0; index-- {
		turn := episode.Turns[index]
		if turn.RoundIndex >= episode.CurrentRound && turn.Status == "model_complete" {
			return turn, true
		}
	}
	return domain.StewardAgentTurn{}, false
}

func (s *Service) resumePersistedAgentTurn(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn) error {
	if turn.RoundIndex > episode.CurrentRound {
		externalCalls := countExternalAgentCalls(turn.ToolCalls)
		if _, err := s.db.Pool.Exec(ctx, `update steward_agent_episodes set current_round=$2,
			tool_call_count=tool_call_count+$3,model_failure_count=0,updated_at=$4
			where id=$1 and current_round<$2`, episode.ID, turn.RoundIndex, externalCalls, time.Now().UTC()); err != nil {
			return err
		}
		episode.CurrentRound = turn.RoundIndex
		episode.ToolCallCount += externalCalls
	}
	conversation, err := s.getConversation(ctx, episode.ConversationID)
	if err != nil {
		return err
	}
	trigger, err := s.agentEpisodeTriggerMessage(ctx, episode.TriggerMessageID)
	if err != nil {
		return err
	}
	_, err = s.applyAgentTurnDecision(ctx, conversation, trigger, episode, turn)
	if err == nil {
		return nil
	}
	_, recoveryErr := s.recordUndispatchedAgentTurnError(ctx, episode, turn, err)
	if recoveryErr != nil {
		return errors.Join(err, recoveryErr)
	}
	return nil
}

func (s *Service) agentEpisodeTriggerMessage(ctx context.Context, id string) (domain.StewardConversationMessage, error) {
	var trigger domain.StewardConversationMessage
	err := s.db.Pool.QueryRow(ctx, `select id::text,conversation_id::text,role,content,data_level,model,context_summary,false,created_at
		from steward_conversation_messages where id=$1`, id).Scan(
		&trigger.ID, &trigger.ConversationID, &trigger.Role, &trigger.Content, &trigger.DataLevel, &trigger.Model,
		&trigger.ContextSummary, &trigger.PayloadEncrypted, &trigger.CreatedAt)
	return trigger, err
}

func (s *Service) recordUndispatchedAgentTurnError(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn, cause error) (domain.StewardConversationMessage, error) {
	// A partially created child execution takes precedence over synthesizing a
	// failure result. Re-linking by turn_id prevents duplicate side effects.
	if message, found, err := s.resumeExistingAgentTurnExecution(ctx, episode, turn); err != nil || found {
		return message, err
	}
	if errors.Is(cause, errAgentEpisodeClaimLost) {
		return domain.StewardConversationMessage{}, nil
	}
	var runnable bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_agent_episodes
		where id=$1 and status='thinking' and control_generation=$2)`, episode.ID, episode.ControlGeneration).Scan(&runnable); err != nil || !runnable {
		return domain.StewardConversationMessage{}, err
	}
	messageText := "工具未能执行，模型会根据真实错误尝试其他方法。"
	var message domain.StewardConversationMessage
	var err error
	if episode.TriggerKind == "conversation" {
		message, err = s.insertConversationMessage(ctx, episode.ConversationID, conversationRoleAssistant, messageText,
			episode.DataLevel, turn.Model, "agent-tool-dispatch-error:"+turn.ID)
		if err != nil {
			return message, err
		}
	}
	failure := sanitizeRuntimeError(cause)
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for _, call := range turn.ToolCalls {
		results = append(results, domain.StewardAgentToolResult{
			ToolCallID: call.ID,
			ToolName:   call.ToolName,
			Error:      "工具未执行：" + failure,
		})
	}
	encoded, _ := json.Marshal(results)
	now := time.Now().UTC()
	if _, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,
		failure_summary=$3,completed_at=$4,updated_at=$4 where id=$1 and status='model_complete'`,
		turn.ID, string(encoded), failure, now); err != nil {
		return message, err
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',active_execution_id=null,
		progress_message_id=coalesce(nullif($2,'')::uuid,progress_message_id),last_result_summary=$3,failure_summary=$3,
		updated_at=$4,lease_owner='',lease_expires_at=$5,version=version+1
		where id=$1 and status='thinking' and control_generation=$6`,
		episode.ID, message.ID, failure, now, now.Add(agentTechnicalRetryDelay), episode.ControlGeneration)
	return message, err
}

func (s *Service) completeAgentEpisodeExecution(ctx context.Context, execution domain.StewardConversationExecution, raw []ConversationToolResult) error {
	episode, err := s.getAgentEpisodeState(ctx, execution.EpisodeID)
	if err != nil || episode.Status != agentEpisodeExecuting {
		return err
	}
	if episode.ActiveExecutionID != execution.ID {
		return nil
	}
	turn, err := s.getAgentTurn(ctx, episode.ID, execution.TurnID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("agent turn %s not found", execution.TurnID)
		}
		return err
	}
	if turn.ExecutionID != "" && turn.ExecutionID != execution.ID {
		return fmt.Errorf("agent turn %s belongs to execution %s, not %s", turn.ID, turn.ExecutionID, execution.ID)
	}
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for index, call := range turn.ToolCalls {
		result := domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Evidence: execution.Evidence}
		if index < len(raw) {
			if raw[index].ToolName != "" && raw[index].ToolName != call.ToolName {
				result.Error = fmt.Sprintf("tool result mapping mismatch: expected %s, received %s", call.ToolName, raw[index].ToolName)
			} else {
				result.Error = raw[index].Error
				result.Output = compactAgentToolOutput(call.ToolName, result.Error == "", raw[index].Output, execution.Evidence)
			}
		}
		if result.Error == "" && execution.Status != RuntimeRunSucceeded && index >= len(raw) {
			result.Error = defaultString(execution.FailureSummary, "tool execution failed")
		}
		results = append(results, result)
	}
	resultsJSON, _ := json.Marshal(results)
	toolsmithFailure := episode.TriggerKind == "proactive_toolsmith" && allAgentToolResultsFailed(results)
	callFingerprint := agentToolCallProgressFingerprint(turn.ToolCalls, toolsmithFailure)
	resultFingerprint := agentToolResultProgressFingerprint(results)
	noProgress := 1
	if toolsmithFailure {
		var previousMatches int
		_ = s.db.Pool.QueryRow(ctx, `
			select count(*) from (
				select call_fingerprint,result_fingerprint from steward_agent_turns
				where episode_id=$1 and round_index<$2 and status='tools_complete'
				order by round_index desc limit 8
			) recent where call_fingerprint=$3 and result_fingerprint=$4
		`, episode.ID, turn.RoundIndex, callFingerprint, resultFingerprint).Scan(&previousMatches)
		noProgress = previousMatches + 1
	} else {
		_ = s.db.Pool.QueryRow(ctx, `
			select case when call_fingerprint=$3 and result_fingerprint=$4 then $5+1 else 1 end
			from steward_agent_turns where episode_id=$1 and round_index<$2 and status='tools_complete'
			order by round_index desc limit 1
		`, episode.ID, turn.RoundIndex, callFingerprint, resultFingerprint, episode.NoProgressCount).Scan(&noProgress)
	}
	now := time.Now().UTC()
	summary := summarizeAgentResults(results)
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeStatus, activeExecutionID string
	err = tx.QueryRow(ctx, `select status,coalesce(active_execution_id::text,'') from steward_agent_episodes where id=$1 for update`, episode.ID).Scan(&activeStatus, &activeExecutionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if activeStatus != agentEpisodeExecuting || activeExecutionID != execution.ID {
		return nil
	}
	tag, err := tx.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,call_fingerprint=$3,
		       result_fingerprint=$4,completed_at=$5,updated_at=$5 where id=$1 and episode_id=$6 and execution_id=$7`,
		turn.ID, string(resultsJSON), callFingerprint, resultFingerprint, now, episode.ID, execution.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return nil
	}
	tag, err = tx.Exec(ctx, `update steward_agent_episodes set status='thinking',active_execution_id=null,no_progress_count=$2,
		       last_call_fingerprint=$3,last_result_fingerprint=$4,last_result_summary=$5,
		       updated_at=$6,lease_owner='',lease_expires_at=null,version=version+1
		where id=$1 and status='executing' and active_execution_id=$7`, episode.ID, noProgress, callFingerprint, resultFingerprint, summary, now, execution.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return nil
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if !supportsNativeAgentTurns(s.autonomyAdvisor()) {
		updated, getErr := s.getAgentEpisodeState(ctx, episode.ID)
		if getErr != nil {
			return getErr
		}
		turn.ToolResults = results
		_, finishErr := s.finishAgentEpisode(ctx, updated, turn, "已完成："+summary, false)
		return finishErr
	}
	return nil
}

func compactAgentToolOutput(toolName string, succeeded bool, output, evidence map[string]any) map[string]any {
	if output == nil {
		return map[string]any{}
	}
	encoded, _ := json.Marshal(output)
	if len(encoded) <= 32768 {
		return output
	}
	compacted := map[string]any{
		"truncated": true, "size_bytes": len(encoded), "sha256": agentFingerprint(encoded),
		"summary": truncateAdvisorText(string(encoded), 6000), "evidence": evidence,
	}
	if succeeded && (toolName == "tool.describe" || toolName == "tool.create" || toolName == "tool.update") {
		if name, ok := output["name"].(string); ok && strings.TrimSpace(name) != "" {
			compacted["name"] = name
		}
	}
	return compacted
}

// buildAgentTranscript keeps recent native tool-call/tool-result pairs intact
// while collapsing arbitrarily old history into one assistant-only summary.
// This makes very long Episodes bounded without ever emitting orphan tool
// messages, which OpenAI-compatible providers reject.
func buildAgentTranscript(turns []domain.StewardAgentTurn) []AgentTurnTranscript {
	if len(turns) == 0 {
		return []AgentTurnTranscript{}
	}
	recentStart := len(turns) - agentTranscriptRecentTurnLimit
	if recentStart < 0 {
		recentStart = 0
	}
	transcript := make([]AgentTurnTranscript, 0, len(turns)-recentStart+1)
	if recentStart > 0 {
		transcript = append(transcript, AgentTurnTranscript{AssistantContent: summarizeCompletedAgentTurns(turns[:recentStart], agentTranscriptSummaryBudget)})
	}
	recent := turns[recentStart:]
	perTurnBudget := agentTranscriptRecentBudget
	if len(recent) > 0 {
		perTurnBudget /= len(recent)
	}
	if perTurnBudget < 2000 {
		perTurnBudget = 2000
	}
	for _, turn := range recent {
		transcript = append(transcript, compactAgentTurnForTranscript(turn, perTurnBudget))
	}
	return transcript
}

func compactAgentTurnForTranscript(turn domain.StewardAgentTurn, budget int) AgentTurnTranscript {
	itemCount := 2 + len(turn.ToolCalls)*2
	perItem := budget / agentMaxInt(itemCount, 1)
	if perItem < 256 {
		perItem = 256
	}
	if perItem > 4000 {
		perItem = 4000
	}
	resultByCall := make(map[string]domain.StewardAgentToolResult, len(turn.ToolResults))
	for _, result := range turn.ToolResults {
		resultByCall[result.ToolCallID] = result
	}
	calls := make([]domain.StewardAgentToolCall, 0, len(turn.ToolCalls))
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for _, call := range turn.ToolCalls {
		compactedCall := call
		compactedCall.Arguments = compactAgentTranscriptMap(call.Arguments, perItem)
		calls = append(calls, compactedCall)
		result, found := resultByCall[call.ID]
		if !found {
			result = domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Error: "历史工具结果未完整落库"}
		}
		result.ToolCallID = call.ID
		result.ToolName = defaultString(result.ToolName, call.ToolName)
		result.Error = truncateAdvisorText(result.Error, perItem)
		result.Output = compactAgentTranscriptMap(result.Output, perItem)
		result.Evidence = compactAgentTranscriptMap(result.Evidence, agentMinInt(perItem, 1000))
		results = append(results, result)
	}
	return AgentTurnTranscript{
		AssistantContent: truncateAdvisorText(turn.AssistantContent, perItem),
		ReasoningContent: truncateAdvisorText(turn.ReasoningContent, perItem),
		ToolCalls:        calls,
		ToolResults:      results,
	}
}

func compactAgentTranscriptMap(value map[string]any, budget int) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	encoded, _ := json.Marshal(value)
	if len([]rune(string(encoded))) <= budget {
		return value
	}
	summaryBudget := budget - 120
	if summaryBudget < 80 {
		summaryBudget = 80
	}
	return map[string]any{
		"compacted_summary": truncateAdvisorText(string(encoded), summaryBudget),
		"sha256":            agentFingerprint(encoded),
	}
}

func summarizeCompletedAgentTurns(turns []domain.StewardAgentTurn, budget int) string {
	lines := []string{fmt.Sprintf("较早的 %d 个已持久化回合已压缩；以下是可验证摘要：", len(turns))}
	for _, turn := range turns {
		toolNames := make([]string, 0, len(turn.ToolCalls))
		for _, call := range turn.ToolCalls {
			toolNames = append(toolNames, call.ToolName)
		}
		succeeded, failed := 0, 0
		evidence := []string{}
		resultFacts := []string{}
		for _, result := range turn.ToolResults {
			if strings.TrimSpace(result.Error) == "" {
				succeeded++
				if len(removeAgentGovernance(result.Output)) > 0 {
					// Prefer the semantic formatter over a raw JSON prefix. Large
					// directory listings and search results commonly sort bulky arrays
					// before the path/identifier fields in JSON, causing the durable
					// working fact to be truncated out of long-history summaries.
					resultFacts = append(resultFacts, result.ToolName+"="+truncateAdvisorText(summarizeAgentToolSuccess(result.ToolName, result.Output), 360))
				}
			} else {
				failed++
				resultFacts = append(resultFacts, result.ToolName+" error="+truncateAdvisorText(result.Error, 240))
			}
			if len(result.Evidence) > 0 {
				encoded, _ := json.Marshal(result.Evidence)
				evidence = append(evidence, truncateAdvisorText(string(encoded), 240))
			}
		}
		line := fmt.Sprintf("- 第%d轮 status=%s tools=[%s] success=%d failed=%d",
			turn.RoundIndex, turn.Status, strings.Join(toolNames, ","), succeeded, failed)
		if len(evidence) > 0 {
			line += " evidence=" + strings.Join(evidence, ",")
		}
		if len(resultFacts) > 0 {
			line += " results=" + strings.Join(resultFacts, ";")
		}
		if strings.TrimSpace(turn.AssistantContent) != "" {
			line += " reply=" + truncateAdvisorText(turn.AssistantContent, 300)
		}
		candidate := strings.Join(append(lines, line), "\n")
		if len([]rune(candidate)) > budget {
			lines = append(lines, "- 其余更早回合省略；原始回合和证据仍保存在 Episode 中。")
			break
		}
		lines = append(lines, line)
	}
	return truncateAdvisorText(strings.Join(lines, "\n"), budget)
}

func agentMinInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func agentMaxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func agentFingerprint(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func agentToolCallProgressFingerprint(calls []domain.StewardAgentToolCall, failureClassOnly bool) string {
	canonical := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		item := map[string]any{"tool_name": call.ToolName}
		if !failureClassOnly {
			item["arguments"] = call.Arguments
			item["target_device_id"] = call.TargetDeviceID
		}
		canonical = append(canonical, item)
	}
	encoded, _ := json.Marshal(canonical)
	return agentFingerprint(encoded)
}

func agentToolResultProgressFingerprint(results []domain.StewardAgentToolResult) string {
	canonical := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := map[string]any{"tool_name": result.ToolName}
		if result.Error != "" {
			item["error"] = normalizeAgentProgressError(result.Error)
		} else {
			item["output"] = result.Output
		}
		canonical = append(canonical, item)
	}
	encoded, _ := json.Marshal(canonical)
	return agentFingerprint(encoded)
}

func allAgentToolResultsFailed(results []domain.StewardAgentToolResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if strings.TrimSpace(result.Error) == "" {
			return false
		}
	}
	return true
}

func normalizeAgentProgressError(value string) string {
	value = strings.TrimSpace(value)
	for _, marker := range []string{
		"invalid steward-tool/1 response:",
		"invalid steward-tool/1 response JSON:",
	} {
		if index := strings.Index(value, marker); index >= 0 {
			return value[index:]
		}
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "is immutable and already exists"):
		return "tool version is immutable and already exists"
	case strings.Contains(lower, "generated tools require at least one executable test"):
		return "generated tool has no executable test"
	case strings.Contains(lower, "tool title and description are required"):
		return "generated tool title or description is missing"
	case strings.Contains(lower, "input must contain tool arguments directly"), strings.Contains(lower, "input does not match input_schema"):
		return "generated tool test input does not match input_schema"
	default:
		return truncateAdvisorText(value, 1000)
	}
}

func summarizeAgentResults(results []domain.StewardAgentToolResult) string {
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if result.Error != "" {
			parts = append(parts, result.ToolName+" 失败："+truncateAdvisorText(result.Error, 300))
		} else {
			parts = append(parts, result.ToolName+"："+summarizeAgentToolSuccess(result.ToolName, result.Output))
		}
	}
	return truncateAdvisorText(strings.Join(parts, "；"), 2000)
}

func removeAgentGovernance(output map[string]any) map[string]any {
	clean := cloneStringAnyMap(output)
	delete(clean, "_governance")
	return clean
}

func summarizeAgentToolSuccess(toolName string, output map[string]any) string {
	clean := removeAgentGovernance(output)
	stringValue := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := clean[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	numberValue := func(key string) string {
		if value, ok := clean[key]; ok && value != nil {
			return fmt.Sprint(value)
		}
		return ""
	}
	switch toolName {
	case "screen.capture":
		path := stringValue("path", "saved_path", "output_path")
		dimensions := ""
		if width, height := numberValue("width"), numberValue("height"); width != "" && height != "" {
			dimensions = width + "×" + height
		}
		return defaultString(strings.TrimSpace(strings.Join([]string{path, dimensions}, " ")), "截图已保存")
	case "fs.get_known_folders":
		return "已获取当前登录用户的桌面、文档、下载等已知目录"
	case "fs.list":
		path := stringValue("path", "directory")
		count := numberValue("count")
		if count == "" {
			if entries, ok := clean["entries"].([]any); ok {
				count = fmt.Sprint(len(entries))
			}
		}
		if count != "" {
			return strings.TrimSpace(path + " 共 " + count + " 项")
		}
		return defaultString(path, "目录已列出")
	case "fs.move", "fs.copy":
		target := stringValue("destination", "target", "to", "path")
		return defaultString(target, "文件操作已完成")
	}
	encoded, _ := json.Marshal(clean)
	if len(clean) == 0 {
		return "已完成"
	}
	return truncateAdvisorText(string(encoded), 320)
}

func (s *Service) RunAgentEpisodeCycle(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err == nil && (control.Stopped || control.Paused) {
		return 0, nil
	}
	if err := s.repairExecutingAgentEpisodes(ctx, limit); err != nil {
		return 0, err
	}
	// Discover candidates without mutating their claims. The durable claim is
	// written only after this worker owns the PostgreSQL session advisory lock.
	// If a slow model call outlives its lease, another service therefore cannot
	// overwrite lease_owner before noticing that the first worker is still live.
	worker := s.agentIDValue() + ":agent-loop:" + uuid.NewString()
	rows, err := s.db.Pool.Query(ctx, `
		select id::text from steward_agent_episodes
		where status='thinking' and (lease_expires_at is null or lease_expires_at<now())
		order by updated_at limit $1
	`, limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	processed := 0
	for _, id := range ids {
		claimed := false
		locked, advanceErr := s.withAgentEpisodeAdvisoryLock(ctx, id, func() error {
			tag, claimErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes
				set lease_owner=$2,lease_expires_at=now()+interval '90 seconds',updated_at=now(),version=version+1
				where id=$1 and status='thinking' and (lease_expires_at is null or lease_expires_at<now())`, id, worker)
			if claimErr != nil {
				return claimErr
			}
			if tag.RowsAffected() != 1 {
				return nil
			}
			claimed = true
			return s.advanceAgentEpisode(ctx, id, worker)
		})
		if !locked {
			continue
		}
		if !claimed && advanceErr == nil {
			continue
		}
		if advanceErr != nil {
			if claimed {
				_ = s.handleAgentEpisodeAdvanceError(ctx, id, advanceErr)
			}
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) withAgentEpisodeAdvisoryLock(ctx context.Context, id string, fn func() error) (bool, error) {
	conn, err := s.db.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	lockKey := "steward-agent-episode:" + id
	var locked bool
	if err := conn.QueryRow(ctx, `select pg_try_advisory_lock(hashtextextended($1,0))`, lockKey).Scan(&locked); err != nil || !locked {
		return locked, err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ignored bool
		_ = conn.QueryRow(unlockCtx, `select pg_advisory_unlock(hashtextextended($1,0))`, lockKey).Scan(&ignored)
	}()
	return true, fn()
}

func (s *Service) repairExecutingAgentEpisodes(ctx context.Context, limit int) error {
	rows, err := s.db.Pool.Query(ctx, `select id::text,active_execution_id::text from steward_agent_episodes
		where status='executing' and active_execution_id is not null order by updated_at limit $1`, limit)
	if err != nil {
		return err
	}
	type candidate struct{ episodeID, executionID string }
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.episodeID, &item.executionID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range candidates {
		execution, err := s.getConversationExecution(ctx, item.executionID)
		if err != nil {
			return err
		}
		if !runtimeRunTerminal(execution.Status) {
			continue
		}
		results, err := s.agentConversationExecutionResults(ctx, execution)
		if err != nil {
			return err
		}
		if err := s.completeAgentEpisodeExecution(ctx, execution, results); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleAgentEpisodeAdvanceError(ctx context.Context, id string, cause error) error {
	if errors.Is(cause, errAgentEpisodeClaimLost) {
		return nil
	}
	now := time.Now().UTC()
	var modelErr *agentModelTurnError
	if !errors.As(cause, &modelErr) {
		// A persisted model_complete turn can fail while being dispatched (for
		// example, a temporarily unavailable session path). Keep it runnable and
		// do not mislabel that infrastructure failure as a provider failure.
		_, err := s.db.Pool.Exec(ctx, `update steward_agent_episodes set failure_summary=$2,
			lease_owner='',lease_expires_at=$3,updated_at=$4
			where id=$1 and status='thinking'`, id, sanitizeRuntimeError(cause), now.Add(agentTechnicalRetryDelay), now)
		return err
	}

	if retryAt, open := advisorCircuitRetryAt(cause); open {
		if !retryAt.After(now) {
			retryAt = now.Add(time.Second)
		}
		// The breaker rejected this turn before a provider request was made. Do
		// not increment model_failure_count and do not overwrite the original
		// provider error already stored in failure_summary.
		_, err := s.db.Pool.Exec(ctx, `update steward_agent_episodes set
			lease_owner='',lease_expires_at=$2,updated_at=$3
			where id=$1 and status='thinking'`, id, retryAt, now)
		return err
	}

	var failures int
	err := s.db.Pool.QueryRow(ctx, `update steward_agent_episodes set failure_summary=$2,
		model_failure_count=model_failure_count+1,lease_owner='',lease_expires_at=$3,updated_at=$4
		where id=$1 and status='thinking' returning model_failure_count`,
		id, sanitizeRuntimeError(cause), now.Add(agentTechnicalRetryDelay), now).Scan(&failures)
	if err != nil || failures < agentModelFailureLimit {
		return err
	}
	episode, getErr := s.getAgentEpisodeState(ctx, id)
	if getErr != nil {
		return getErr
	}
	return s.finishAgentEpisodeWithText(ctx, episode,
		"模型连续请求失败，任务已暂停。修复模型连接后可点击继续："+sanitizeRuntimeError(cause), agentEpisodeBlocked)
}

func (s *Service) advanceAgentEpisode(ctx context.Context, id, claimID string) error {
	episode, err := s.GetAgentEpisodeForLoop(ctx, id)
	if err != nil || episode.Status != agentEpisodeThinking {
		return err
	}
	if err := s.verifyAgentEpisodeClaim(ctx, episode.ID, claimID, episode.ControlGeneration); err != nil {
		return err
	}
	if pending, ok := pendingModelCompleteAgentTurn(episode); ok {
		return s.resumePersistedAgentTurn(ctx, episode, pending)
	}
	if episode.DeadlineAt != nil && time.Now().After(*episode.DeadlineAt) {
		return s.finishAgentEpisodeWithText(ctx, episode, "任务已达到最长运行时间，已停止继续调用工具。", agentEpisodeBlocked)
	}
	if episode.MaxRounds > 0 && episode.CurrentRound >= episode.MaxRounds {
		return s.finishAgentEpisodeWithText(ctx, episode, "任务已达到最大模型轮次，已停止继续调用工具。", agentEpisodeBlocked)
	}
	if episode.MaxToolCalls > 0 && episode.ToolCallCount >= episode.MaxToolCalls {
		return s.finishAgentEpisodeWithText(ctx, episode, "任务已达到最大工具调用次数，已停止继续调用工具。", agentEpisodeBlocked)
	}
	if episode.NoProgressCount > episode.NoProgressLimit {
		return s.finishAgentEpisodeWithText(ctx, episode, "连续多轮没有取得新进展，已停止重复调用。", agentEpisodeBlocked)
	}
	advisor, ok := s.autonomyAdvisor().(AgentTurnAdvisor)
	if !ok || !s.autonomyAdvisor().Status().Enabled {
		return fmt.Errorf("configured model does not support agent turns")
	}
	history, err := s.conversationAdvisorHistory(ctx, episode.ConversationID, 20)
	if err != nil {
		return err
	}
	localContext, err := s.conversationContext(ctx, episode.Goal, episode.DataLevel, 10)
	if err != nil {
		return err
	}
	transcript := buildAgentTranscript(episode.Turns)
	notice := ""
	if episode.NoProgressCount >= episode.NoProgressLimit {
		notice = "最近多轮调用产生了相同结果。请反思目标，改用其他工具或给出最终结论，不要重复相同调用。"
	}
	modelTools, toolCatalog, catalogErr := s.agentToolContext(ctx, &episode)
	if catalogErr != nil {
		return catalogErr
	}
	decision, err := nextAgentTurnWithContextRecovery(ctx, advisor, AgentTurnInput{
		Message: episode.Goal, DataLevel: episode.DataLevel, TriggerKind: episode.TriggerKind,
		History: history, Transcript: transcript, Context: localContext, Tools: modelTools, ToolCatalog: toolCatalog,
		Devices: s.conversationAdvisorDevices(ctx), KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(),
		Round: episode.CurrentRound + 1, ToolCallCount: episode.ToolCallCount, Deadline: episode.DeadlineAt, NoProgressNotice: notice,
	})
	if err != nil {
		return &agentModelTurnError{cause: err}
	}
	externalCalls := countExternalAgentCalls(decision.ToolCalls)
	if episode.MaxToolCalls > 0 && episode.ToolCallCount+externalCalls > episode.MaxToolCalls {
		return s.finishAgentEpisodeWithText(ctx, episode, "模型请求的下一批工具会超过调用上限，任务已停止。", agentEpisodeBlocked)
	}
	now := time.Now().UTC()
	turn := domain.StewardAgentTurn{
		ID: uuid.NewString(), EpisodeID: episode.ID, RoundIndex: episode.CurrentRound + 1, Status: "model_complete",
		AssistantContent: decision.Content, ReasoningContent: decision.ReasoningContent, ToolCalls: decision.ToolCalls,
		Provider: s.autonomyAdvisor().Status().Provider, Model: s.autonomyAdvisor().Status().Model,
		ProviderResponseID: decision.ProviderResponseID, CreatedAt: now, UpdatedAt: now,
	}
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAgentTurnWithExecer(ctx, tx, turn); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `update steward_agent_episodes set current_round=$2,tool_call_count=tool_call_count+$3,
		model_failure_count=0,failure_summary='',updated_at=$4 where id=$1 and status='thinking'
		and lease_owner=$5 and control_generation=$6`, episode.ID, turn.RoundIndex, externalCalls, now, claimID, episode.ControlGeneration)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAgentEpisodeClaimLost
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	episode.CurrentRound = turn.RoundIndex
	episode.ToolCallCount += externalCalls
	return s.resumePersistedAgentTurn(ctx, episode, turn)
}

func (s *Service) verifyAgentEpisodeClaim(ctx context.Context, id, claimID string, generation int64) error {
	var valid bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_agent_episodes
		where id=$1 and status='thinking' and lease_owner=$2 and control_generation=$3)`,
		id, claimID, generation).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errAgentEpisodeClaimLost
	}
	return nil
}

func (s *Service) finishAgentEpisode(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn, text string, silent bool) (domain.StewardConversationMessage, error) {
	now := time.Now().UTC()
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardConversationMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus, activeExecutionID, turnStatus string
	var controlGeneration int64
	if err = tx.QueryRow(ctx, `select status,control_generation,coalesce(active_execution_id::text,'')
		from steward_agent_episodes where id=$1 for update`, episode.ID).Scan(&currentStatus, &controlGeneration, &activeExecutionID); err != nil {
		return domain.StewardConversationMessage{}, err
	}
	if currentStatus != agentEpisodeThinking || controlGeneration != episode.ControlGeneration || activeExecutionID != "" {
		return domain.StewardConversationMessage{}, nil
	}
	if err = tx.QueryRow(ctx, `select status from steward_agent_turns where id=$1 and episode_id=$2 for update`, turn.ID, episode.ID).Scan(&turnStatus); err != nil {
		return domain.StewardConversationMessage{}, err
	}
	if turnStatus != "model_complete" && turnStatus != "tools_complete" {
		return domain.StewardConversationMessage{}, nil
	}
	var message domain.StewardConversationMessage
	if !silent {
		message, err = s.insertConversationMessage(ctx, episode.ConversationID, conversationRoleAssistant, text, episode.DataLevel, turn.Model, "agent-final:"+episode.ID)
		if err != nil {
			return message, err
		}
	}
	status := "final"
	if silent {
		status = "silent"
	}
	tag, err := tx.Exec(ctx, `update steward_agent_turns set status=$2,completed_at=$3,updated_at=$3
		where id=$1 and episode_id=$4 and status in ('model_complete','tools_complete')`, turn.ID, status, now, episode.ID)
	if err != nil {
		return message, err
	}
	if tag.RowsAffected() != 1 {
		return domain.StewardConversationMessage{}, nil
	}
	tag, err = tx.Exec(ctx, `update steward_agent_episodes set status='completed',final_message_id=nullif($2,'')::uuid,
		progress_message_id=coalesce(nullif($2,'')::uuid,progress_message_id),active_execution_id=null,
		completed_at=$3,updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1
		where id=$1 and status='thinking' and control_generation=$4 and active_execution_id is null`,
		episode.ID, message.ID, now, episode.ControlGeneration)
	if err != nil {
		return message, err
	}
	if tag.RowsAffected() != 1 {
		return domain.StewardConversationMessage{}, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return message, err
	}
	if memoryErr := s.recordAgentEpisodeMemory(ctx, episode.ID); memoryErr != nil {
		_, _ = s.db.Pool.Exec(ctx, `update steward_agent_episodes set failure_summary=$2 where id=$1`, episode.ID, "long-term memory: "+sanitizeRuntimeError(memoryErr))
		return message, memoryErr
	}
	return message, nil
}

func (s *Service) finishAgentEpisodeWithText(ctx context.Context, episode domain.StewardAgentEpisode, text, status string) error {
	message, err := s.insertConversationMessage(ctx, episode.ConversationID, conversationRoleAssistant, text, episode.DataLevel, s.autonomyAdvisor().Status().Model, "agent-terminal:"+episode.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status=$2,final_message_id=$3,progress_message_id=$3,
		failure_summary=$4,completed_at=$5,updated_at=$5,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, episode.ID, status, message.ID, text, now)
	return err
}

func (s *Service) failAgentEpisode(ctx context.Context, id string, cause error) error {
	episode, err := s.getAgentEpisodeState(ctx, id)
	if err != nil {
		return err
	}
	failure := "Agent 运行失败：" + sanitizeRuntimeError(cause)
	if episode.TriggerKind == "proactive_toolsmith" {
		// Tool catalog maintenance is background infrastructure. Keep its full
		// failure in the Episode and daemon status without injecting repetitive
		// terminal messages into the user's normal conversation.
		now := time.Now().UTC()
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status=$2,failure_summary=$3,
			completed_at=$4,updated_at=$4,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`,
			episode.ID, agentEpisodeFailed, failure, now)
		return err
	}
	return s.finishAgentEpisodeWithText(ctx, episode, failure, agentEpisodeFailed)
}

func (s *Service) recordAgentEpisodeMemory(ctx context.Context, id string) error {
	episode, err := s.GetAgentEpisodeForLoop(ctx, id)
	if err != nil {
		return err
	}
	confirmed := true
	details := buildAgentEpisodeMemoryDetails(episode)
	memory, err := s.CreateMemory(ctx, CreateMemoryInput{
		Type: "execution_episode", Title: "已完成：" + truncateAdvisorText(episode.Goal, 200),
		Summary: fmt.Sprintf("循环 Agent 用 %d 轮、%d 次工具调用完成任务：%s", episode.CurrentRound, episode.ToolCallCount, truncateAdvisorText(strings.ReplaceAll(details, "\n", "；"), 1000)),
		Content: details, Scope: "conversation:" + episode.ConversationID, Source: "agent_episode",
		DataLevel: episode.DataLevel, PermissionLevel: PermissionA9, Confidence: 1, UserConfirmed: &confirmed,
	})
	if err != nil {
		return err
	}
	return s.forEachAgentEpisodeExecution(ctx, episode.ID, func(item agentExecutionReference) error {
		_, refErr := s.CreateSourceRef(ctx, CreateSourceRefInput{
			TargetType: "memory", TargetID: memory.ID, SourceType: "conversation_execution", SourceID: item.ExecutionID,
			Summary: fmt.Sprintf("Agent 第 %d 轮", item.RoundIndex), Confidence: 1,
		})
		return refErr
	})
}

func (s *Service) DecideAgentEpisode(ctx context.Context, id string, input DecideAgentEpisodeInput) (domain.StewardAgentEpisode, error) {
	var result domain.StewardAgentEpisode
	locked, err := s.withAgentEpisodeAdvisoryLock(ctx, id, func() error {
		var decisionErr error
		result, decisionErr = s.decideAgentEpisodeLocked(ctx, id, input)
		return decisionErr
	})
	if err != nil {
		return result, err
	}
	if !locked {
		return result, fmt.Errorf("agent episode is busy; retry the control action")
	}
	return result, nil
}

func (s *Service) decideAgentEpisodeLocked(ctx context.Context, id string, input DecideAgentEpisodeInput) (domain.StewardAgentEpisode, error) {
	episode, err := s.getAgentEpisodeState(ctx, id)
	if err != nil {
		return episode, err
	}
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	now := time.Now().UTC()
	switch decision {
	case "pause":
		tag, updateErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='paused',control_generation=control_generation+1,
			updated_at=$2,lease_owner='',lease_expires_at=null,version=version+1
			where id=$1 and control_generation=$3 and status in ('thinking','executing')`, id, now, episode.ControlGeneration)
		if updateErr != nil {
			return episode, updateErr
		}
		if tag.RowsAffected() != 1 {
			return episode, fmt.Errorf("agent episode cannot pause from %s", episode.Status)
		}
		episode.ControlGeneration++
		episode.Status = agentEpisodePaused
		if episode.ActiveExecutionID != "" {
			if err = s.controlAndReconcileAgentExecution(ctx, episode, "pause"); err != nil {
				return episode, err
			}
		}
	case "resume":
		if episode.Status != agentEpisodePaused && episode.Status != agentEpisodeBlocked && episode.Status != agentEpisodeFailed {
			return episode, fmt.Errorf("agent episode cannot resume from %s", episode.Status)
		}
		reconciledSummary := episode.LastResultSummary
		if episode.ActiveExecutionID != "" {
			outcome, reconcileErr := s.reconcileControlledAgentExecution(ctx, episode, "resume")
			if reconcileErr != nil {
				return episode, reconcileErr
			}
			reconciledSummary = outcome.Summary
			if !outcome.Resolved {
				tag, updateErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='blocked',failure_summary=$2,
					control_generation=control_generation+1,updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1
					where id=$1 and control_generation=$4 and status in ('paused','blocked','failed')`, id, outcome.Summary, now, episode.ControlGeneration)
				if updateErr != nil {
					return episode, updateErr
				}
				if tag.RowsAffected() != 1 {
					return episode, errAgentEpisodeClaimLost
				}
				return s.GetAgentEpisodeOverview(ctx, id, agentEpisodeOverviewTurnLimit)
			}
		}
		settings, settingsErr := s.loadModelSettings(ctx)
		if settingsErr != nil {
			return episode, settingsErr
		}
		limits := normalizeAgentLoopLimits(settings)
		maxRounds := episode.MaxRounds
		if maxRounds > 0 && episode.CurrentRound >= maxRounds {
			if limits.MaxRounds == 0 {
				maxRounds = 0
			} else {
				maxRounds = episode.CurrentRound + limits.MaxRounds
			}
		}
		maxToolCalls := episode.MaxToolCalls
		if maxToolCalls > 0 && episode.ToolCallCount >= maxToolCalls {
			if limits.MaxToolCalls == 0 {
				maxToolCalls = 0
			} else {
				maxToolCalls = episode.ToolCallCount + limits.MaxToolCalls
			}
		}
		var deadline any
		if episode.DeadlineAt != nil && !episode.DeadlineAt.After(now) {
			if limits.MaxDurationSeconds > 0 {
				deadline = now.Add(time.Duration(limits.MaxDurationSeconds) * time.Second)
			}
		} else {
			deadline = episode.DeadlineAt
		}
		tag, updateErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',active_execution_id=null,
			failure_summary='',model_failure_count=0,no_progress_count=0,max_rounds=$2,max_tool_calls=$3,deadline_at=$4,
			completed_at=null,final_message_id=null,last_result_summary=$5,control_generation=control_generation+1,
			updated_at=$6,lease_owner='',lease_expires_at=null,version=version+1
			where id=$1 and control_generation=$7 and status in ('paused','blocked','failed')`,
			id, maxRounds, maxToolCalls, deadline, reconciledSummary, now, episode.ControlGeneration)
		if updateErr != nil {
			return episode, updateErr
		}
		if tag.RowsAffected() != 1 {
			return episode, errAgentEpisodeClaimLost
		}
	case "cancel":
		tag, updateErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='cancelled',control_generation=control_generation+1,
			failure_summary='用户取消了当前任务。',completed_at=$2,updated_at=$2,lease_owner='',lease_expires_at=null,version=version+1
			where id=$1 and control_generation=$3 and status not in ('completed','cancelled')`, id, now, episode.ControlGeneration)
		if updateErr != nil {
			return episode, updateErr
		}
		if tag.RowsAffected() != 1 {
			return episode, fmt.Errorf("agent episode cannot cancel from %s", episode.Status)
		}
		episode.ControlGeneration++
		episode.Status = agentEpisodeCancelled
		if episode.ActiveExecutionID != "" {
			if err = s.controlAndReconcileAgentExecution(ctx, episode, "cancel"); err != nil {
				return episode, err
			}
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set active_execution_id=null,updated_at=$2
			where id=$1 and status='cancelled' and control_generation=$3`, id, now, episode.ControlGeneration)
	case "switch_device":
		target, _, targetErr := s.selectConversationExecutionTarget(ctx, episode.Goal, input.TargetDeviceID)
		if targetErr != nil {
			return episode, targetErr
		}
		tag, updateErr := s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='paused',target_device_id=$2,
			control_generation=control_generation+1,updated_at=$3,lease_owner='',lease_expires_at=null,
			version=version+1 where id=$1 and control_generation=$4 and status in ('thinking','executing','paused','blocked','failed')`, id, target.ID, now, episode.ControlGeneration)
		if updateErr != nil {
			return episode, updateErr
		}
		if tag.RowsAffected() != 1 {
			return episode, fmt.Errorf("agent episode cannot switch device from %s", episode.Status)
		}
		episode.ControlGeneration++
		episode.Status = agentEpisodePaused
		episode.TargetDeviceID = target.ID
		resolved := true
		resultSummary := episode.LastResultSummary
		if episode.ActiveExecutionID != "" {
			outcome, controlErr := s.cancelAndReconcileAgentExecution(ctx, episode, false, "switch_device")
			if controlErr != nil {
				return episode, controlErr
			}
			resolved = outcome.Resolved
			resultSummary = outcome.Summary
		}
		nextStatus := agentEpisodeThinking
		failure := ""
		if !resolved {
			nextStatus = agentEpisodeBlocked
			failure = "当前设备上的工具已经开始，但真实终态尚未确认。为避免在新设备重复副作用，已阻止自动重做；请先核验原设备状态，再继续或取消。"
		}
		tag, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status=$2,active_execution_id=case when $2='thinking' then null else active_execution_id end,
			failure_summary=$3,last_result_summary=$4,updated_at=$5,version=version+1 where id=$1 and status='paused' and control_generation=$6`,
			id, nextStatus, failure, resultSummary, now, episode.ControlGeneration)
		if err == nil && tag.RowsAffected() != 1 {
			return episode, errAgentEpisodeClaimLost
		}
	default:
		return episode, fmt.Errorf("decision must be pause, resume, cancel, or switch_device")
	}
	if err != nil {
		return episode, err
	}
	return s.GetAgentEpisodeOverview(ctx, id, agentEpisodeOverviewTurnLimit)
}

func (s *Service) resumeAwaitingAgentEpisode(ctx context.Context, conversationID, userContent string) (domain.StewardAgentEpisode, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `select id::text from steward_agent_episodes where conversation_id=$1 and status='awaiting_input' order by updated_at desc limit 1`, conversationID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardAgentEpisode{}, false, nil
	}
	if err != nil {
		return domain.StewardAgentEpisode{}, false, err
	}
	now := time.Now().UTC()
	episode, getErr := s.getAgentEpisodeState(ctx, id)
	if getErr != nil {
		return domain.StewardAgentEpisode{}, false, getErr
	}
	turn, turnErr := s.getLatestAgentTurn(ctx, id)
	if turnErr == nil {
		if turn.Status == "waiting_input" && len(turn.ToolCalls) == 1 {
			results, _ := json.Marshal([]domain.StewardAgentToolResult{{
				ToolCallID: turn.ToolCalls[0].ID, ToolName: agentAskUserTool, Output: map[string]any{"user_response": userContent},
			}})
			_, _ = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,updated_at=$3,completed_at=$3 where id=$1`, turn.ID, string(results), now)
		}
	} else if !errors.Is(turnErr, pgx.ErrNoRows) {
		return domain.StewardAgentEpisode{}, false, turnErr
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set goal=goal||E'\n\n用户补充：'||$2,status='thinking',updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, id, userContent, now)
	if err != nil {
		return domain.StewardAgentEpisode{}, false, err
	}
	episode, err = s.GetAgentEpisodeOverview(ctx, id, agentEpisodeOverviewTurnLimit)
	return episode, true, err
}

type agentControlCallState string

const (
	agentControlCallKnown       agentControlCallState = "known"
	agentControlCallNotExecuted agentControlCallState = "not_executed"
	agentControlCallUnknown     agentControlCallState = "unknown"
)

type agentControlCallObservation struct {
	State       agentControlCallState
	Idempotency string
}

type agentControlReconciliation struct {
	Resolved bool
	Summary  string
	Results  []domain.StewardAgentToolResult
}

func (s *Service) controlAndReconcileAgentExecution(ctx context.Context, episode domain.StewardAgentEpisode, action string) error {
	pause := action == "pause"
	outcome, err := s.cancelAndReconcileAgentExecution(ctx, episode, pause, action)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	status := episode.Status
	failure := ""
	clearActive := outcome.Resolved
	if !outcome.Resolved {
		failure = outcome.Summary
	}
	if action == "cancel" {
		status = agentEpisodeCancelled
		clearActive = true
		failure = "用户取消了当前任务。"
		if !outcome.Resolved && strings.TrimSpace(outcome.Summary) != "" {
			failure += " " + outcome.Summary
		}
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status=$2,
		active_execution_id=case when $3 then null else active_execution_id end,
		failure_summary=$4,last_result_summary=$5,updated_at=$6,version=version+1
		where id=$1 and control_generation=$7 and status=$2`,
		episode.ID, status, clearActive, failure, outcome.Summary, now, episode.ControlGeneration)
	return err
}

func (s *Service) cancelAndReconcileAgentExecution(ctx context.Context, episode domain.StewardAgentEpisode, pause bool, action string) (agentControlReconciliation, error) {
	execution, err := s.getConversationExecution(ctx, episode.ActiveExecutionID)
	if err != nil {
		return agentControlReconciliation{}, err
	}
	_, cancelErr := s.cancelConversationExecution(ctx, execution, pause)
	outcome, err := s.reconcileControlledAgentExecution(ctx, episode, action)
	if err != nil {
		return outcome, err
	}
	if cancelErr != nil {
		outcome.Resolved = false
		outcome.Summary = "无法确认子执行已停止：" + sanitizeRuntimeError(cancelErr) + "。为避免重复副作用，已阻止自动重做。"
		if err := s.persistAgentControlReconciliation(ctx, episode, execution, outcome); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

func (s *Service) reconcileControlledAgentExecution(ctx context.Context, episode domain.StewardAgentEpisode, action string) (agentControlReconciliation, error) {
	execution, err := s.getConversationExecution(ctx, episode.ActiveExecutionID)
	if err != nil {
		return agentControlReconciliation{}, err
	}
	if execution.TurnID == "" {
		return agentControlReconciliation{}, fmt.Errorf("agent execution %s has no linked turn", execution.ID)
	}
	turn, err := s.getAgentTurn(ctx, episode.ID, execution.TurnID)
	if err != nil {
		return agentControlReconciliation{}, fmt.Errorf("agent turn for active execution %s: %w", execution.ID, err)
	}
	raw, observations, err := s.inspectAgentControlExecution(ctx, execution)
	if err != nil {
		return agentControlReconciliation{}, err
	}
	results := agentControlToolResults(turn, execution, raw, observations, action)
	resolved := true
	unknownNames := make([]string, 0)
	nonIdempotentUnknown := make([]string, 0)
	for index, observation := range observations {
		if observation.State != agentControlCallUnknown {
			continue
		}
		resolved = false
		name := "unknown"
		if index < len(turn.ToolCalls) {
			name = turn.ToolCalls[index].ToolName
		}
		unknownNames = append(unknownNames, name)
		if observation.Idempotency == RuntimeIdempotencyNonIdempotent {
			nonIdempotentUnknown = append(nonIdempotentUnknown, name)
		}
	}
	summary := summarizeAgentResults(results)
	if !resolved {
		if len(nonIdempotentUnknown) > 0 {
			summary = fmt.Sprintf("工具 %s 已经开始执行，但回执在控制操作期间中断，真实结果未知。它是非幂等调用；为避免重复副作用，自动重试已被阻止。请先核验外部状态，然后取消该任务或在确认后重新发起明确操作。", strings.Join(nonIdempotentUnknown, "、"))
		} else {
			summary = fmt.Sprintf("工具 %s 已经开始执行，但真实终态尚未落库。为避免重复执行，自动重试已被阻止；请等待子执行结束后再次继续。", strings.Join(unknownNames, "、"))
		}
	}
	outcome := agentControlReconciliation{Resolved: resolved, Summary: summary, Results: results}
	if err := s.persistAgentControlReconciliation(ctx, episode, execution, outcome); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func agentTurnForExecution(episode domain.StewardAgentEpisode, executionID string) (domain.StewardAgentTurn, bool) {
	for index := len(episode.Turns) - 1; index >= 0; index-- {
		if episode.Turns[index].ExecutionID == executionID {
			return episode.Turns[index], true
		}
	}
	return domain.StewardAgentTurn{}, false
}

func (s *Service) inspectAgentControlExecution(ctx context.Context, execution domain.StewardConversationExecution) ([]ConversationToolResult, []agentControlCallObservation, error) {
	switch {
	case execution.Kind == conversationExecutionRun && execution.RunID != "":
		run, err := s.GetAgentRun(ctx, execution.RunID)
		if err != nil {
			return nil, nil, err
		}
		return s.conversationRunToolResults(ctx, run), agentControlRunObservations(run), nil
	case execution.Kind == conversationExecutionOrchestration && execution.OrchestrationID != "":
		orchestration, err := s.GetOrchestration(ctx, execution.OrchestrationID)
		if err != nil {
			return nil, nil, err
		}
		observations := make([]agentControlCallObservation, 0, len(orchestration.Nodes))
		for _, node := range orchestration.Nodes {
			idempotency := RuntimeIdempotencyNonIdempotent
			if len(node.Steps) > 0 {
				if tool, ok := s.runtimeTools.get(node.Steps[0].ToolName); ok {
					idempotency = normalizeRuntimeToolSpec(tool.Spec()).IdempotencyMode
				}
			}
			switch {
			case node.RuntimeRunID != "":
				run, runErr := s.GetAgentRun(ctx, node.RuntimeRunID)
				if runErr != nil {
					return nil, nil, runErr
				}
				states := agentControlRunObservations(run)
				if len(states) > 0 {
					observations = append(observations, states[0])
				} else {
					observations = append(observations, agentControlCallObservation{State: agentControlCallNotExecuted, Idempotency: idempotency})
				}
			case node.RemoteDispatch != nil:
				state := agentControlCallUnknown
				if node.RemoteDispatch.Status == RuntimeRunSucceeded {
					state = agentControlCallKnown
				} else if node.RemoteDispatch.RemoteRunID == "" && node.RemoteDispatch.HeartbeatAt == nil && node.RemoteDispatch.Status == "pending" {
					state = agentControlCallNotExecuted
				}
				observations = append(observations, agentControlCallObservation{State: state, Idempotency: idempotency})
			default:
				observations = append(observations, agentControlCallObservation{State: agentControlCallNotExecuted, Idempotency: idempotency})
			}
		}
		return s.conversationOrchestrationToolResults(ctx, orchestration), observations, nil
	default:
		return nil, nil, fmt.Errorf("agent execution %s has no runtime subject", execution.ID)
	}
}

func agentControlRunObservations(run domain.StewardAgentRun) []agentControlCallObservation {
	observations := make([]agentControlCallObservation, 0, len(run.Steps))
	terminal := runtimeRunTerminal(run.Status)
	for _, step := range run.Steps {
		idempotency := defaultString(step.ToolIdempotency, RuntimeIdempotencyNonIdempotent)
		if len(step.Invocations) == 0 {
			state := agentControlCallNotExecuted
			if !terminal && step.Status != RuntimeStepPending && step.Status != RuntimeStepCancelled {
				state = agentControlCallUnknown
			}
			observations = append(observations, agentControlCallObservation{State: state, Idempotency: idempotency})
			continue
		}
		invocation := step.Invocations[len(step.Invocations)-1]
		state := agentControlCallKnown
		explicitlyNotExecuted := strings.Contains(strings.ToLower(invocation.ErrorSummary), "before execution") ||
			strings.Contains(strings.ToLower(invocation.ErrorSummary), "before tool execution")
		if explicitlyNotExecuted {
			state = agentControlCallNotExecuted
		} else if invocation.Status == RuntimeStepSucceeded && invocation.FinishedAt != nil {
			// The successful invocation receipt is the authoritative exactly-once
			// boundary even if the parent run has not yet persisted its own final
			// status.
			state = agentControlCallKnown
		} else if !terminal || invocation.Status == RuntimeStepRunning || invocation.FinishedAt == nil {
			state = agentControlCallUnknown
		} else if idempotency == RuntimeIdempotencyNonIdempotent && invocation.Status != RuntimeStepSucceeded {
			// A failed/cancelled non-idempotent invocation can have changed the
			// outside world before its receipt was persisted. Never infer safety
			// merely from the terminal error status.
			state = agentControlCallUnknown
		}
		observations = append(observations, agentControlCallObservation{State: state, Idempotency: idempotency})
	}
	return observations
}

func agentControlToolResults(turn domain.StewardAgentTurn, execution domain.StewardConversationExecution, raw []ConversationToolResult, observations []agentControlCallObservation, action string) []domain.StewardAgentToolResult {
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for index, call := range turn.ToolCalls {
		result := domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Evidence: execution.Evidence}
		observation := agentControlCallObservation{State: agentControlCallUnknown, Idempotency: RuntimeIdempotencyNonIdempotent}
		if index < len(observations) {
			observation = observations[index]
		}
		switch observation.State {
		case agentControlCallNotExecuted:
			result.Error = fmt.Sprintf("工具已确认未执行（控制操作：%s）；模型可以基于这一真实结果重新规划。", action)
		case agentControlCallUnknown:
			result.Error = fmt.Sprintf("工具已经开始执行，但回执未能确认；自动重复调用已阻止（幂等语义：%s）。", observation.Idempotency)
		default:
			if index < len(raw) {
				if raw[index].ToolName != "" && raw[index].ToolName != call.ToolName {
					result.Error = fmt.Sprintf("tool result mapping mismatch: expected %s, received %s", call.ToolName, raw[index].ToolName)
				} else {
					result.Error = raw[index].Error
					result.Output = compactAgentToolOutput(call.ToolName, result.Error == "", raw[index].Output, execution.Evidence)
				}
			} else {
				result.Error = defaultString(execution.FailureSummary, "tool result is unavailable")
			}
		}
		results = append(results, result)
	}
	return results
}

func (s *Service) persistAgentControlReconciliation(ctx context.Context, episode domain.StewardAgentEpisode, execution domain.StewardConversationExecution, outcome agentControlReconciliation) error {
	if execution.TurnID == "" {
		return fmt.Errorf("agent execution %s has no linked turn", execution.ID)
	}
	turn, err := s.getAgentTurn(ctx, episode.ID, execution.TurnID)
	if err != nil {
		return fmt.Errorf("agent turn for active execution %s: %w", execution.ID, err)
	}
	encoded, _ := json.Marshal(outcome.Results)
	now := time.Now().UTC()
	failure := ""
	if !outcome.Resolved {
		failure = outcome.Summary
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,
		failure_summary=$3,call_fingerprint=$4,result_fingerprint=$5,completed_at=$6,updated_at=$6
		where id=$1 and execution_id=$7`, turn.ID, string(encoded), failure,
		agentToolCallProgressFingerprint(turn.ToolCalls, false), agentToolResultProgressFingerprint(outcome.Results), now, execution.ID)
	return err
}
