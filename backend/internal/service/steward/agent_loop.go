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
)

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
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		decision, err := advisor.NextTurn(ctx, input)
		if err == nil && (strings.TrimSpace(decision.Content) != "" || len(decision.ToolCalls) > 0) {
			return decision, nil
		}
		if err == nil {
			err = fmt.Errorf("agent model returned neither text nor tool calls")
		}
		lastErr = err
	}
	return AgentTurnDecision{}, lastErr
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
		_ = s.failAgentEpisode(ctx, episode.ID, err)
		return message, episode, err
	}
	episode, _ = s.GetAgentEpisode(ctx, episode.ID)
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
	if item.ToolCalls == nil {
		item.ToolCalls = []domain.StewardAgentToolCall{}
	}
	if item.ToolResults == nil {
		item.ToolResults = []domain.StewardAgentToolResult{}
	}
	calls, _ := json.Marshal(item.ToolCalls)
	results, _ := json.Marshal(item.ToolResults)
	_, err := s.db.Pool.Exec(ctx, `
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
		item, err := s.GetAgentEpisode(ctx, id)
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
	message, execution, err := s.createConversationExecutionFromPlanLinked(ctx, conversation, trigger, episode.Goal, episode.DataLevel, target, plan, agentExecutionLink{
		EpisodeID: episode.ID, TurnID: turn.ID, RoundIndex: turn.RoundIndex, IdempotencyKey: idempotencyKey, StepTargets: stepTargets,
	})
	if err != nil {
		return message, err
	}
	now := time.Now().UTC()
	if strings.TrimSpace(turn.AssistantContent) != "" {
		_, _ = s.db.Pool.Exec(ctx, `update steward_conversation_messages set content=$2 where id=$1`, message.ID, truncateAdvisorText(turn.AssistantContent, 8000))
		message.Content = truncateAdvisorText(turn.AssistantContent, 8000)
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_running',execution_id=$2,updated_at=$3 where id=$1`, turn.ID, execution.ID, now)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='executing',active_execution_id=$2,progress_message_id=$3,
		       updated_at=$4,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, episode.ID, execution.ID, message.ID, now)
	}
	message.Executions = []domain.StewardConversationExecution{execution}
	return message, err
}

func (s *Service) completeAgentEpisodeExecution(ctx context.Context, execution domain.StewardConversationExecution, raw []ConversationToolResult) error {
	episode, err := s.GetAgentEpisode(ctx, execution.EpisodeID)
	if err != nil || episode.Status != agentEpisodeExecuting {
		return err
	}
	var turn *domain.StewardAgentTurn
	for index := range episode.Turns {
		if episode.Turns[index].ID == execution.TurnID {
			turn = &episode.Turns[index]
			break
		}
	}
	if turn == nil {
		return fmt.Errorf("agent turn %s not found", execution.TurnID)
	}
	results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
	for index, call := range turn.ToolCalls {
		result := domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Evidence: execution.Evidence}
		if index < len(raw) {
			result.Output = compactAgentToolOutput(raw[index].Output, execution.Evidence)
			result.Error = raw[index].Error
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
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,call_fingerprint=$3,
		       result_fingerprint=$4,completed_at=$5,updated_at=$5 where id=$1`, turn.ID, string(resultsJSON), callFingerprint, resultFingerprint, now)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',active_execution_id=null,no_progress_count=$2,
		       last_call_fingerprint=$3,last_result_fingerprint=$4,last_result_summary=$5,
		       updated_at=$6,lease_owner='',lease_expires_at=null,version=version+1
		where id=$1 and status='executing'`, episode.ID, noProgress, callFingerprint, resultFingerprint, summary, now)
	}
	if err != nil {
		return err
	}
	if !supportsNativeAgentTurns(s.autonomyAdvisor()) {
		updated, getErr := s.GetAgentEpisode(ctx, episode.ID)
		if getErr != nil {
			return getErr
		}
		turn.ToolResults = results
		_, finishErr := s.finishAgentEpisode(ctx, updated, *turn, "已完成："+summary, false)
		return finishErr
	}
	return nil
}

func compactAgentToolOutput(output, evidence map[string]any) map[string]any {
	if output == nil {
		return map[string]any{}
	}
	encoded, _ := json.Marshal(output)
	if len(encoded) <= 32768 {
		return output
	}
	return map[string]any{
		"truncated": true, "size_bytes": len(encoded), "sha256": agentFingerprint(encoded),
		"summary": truncateAdvisorText(string(encoded), 6000), "evidence": evidence,
	}
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
			encoded, _ := json.Marshal(result.Output)
			parts = append(parts, result.ToolName+"："+truncateAdvisorText(string(encoded), 500))
		}
	}
	return truncateAdvisorText(strings.Join(parts, "；"), 2000)
}

func (s *Service) RunAgentEpisodeCycle(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err == nil && (control.Stopped || control.Paused) {
		return 0, nil
	}
	worker := s.agentIDValue() + ":agent-loop"
	rows, err := s.db.Pool.Query(ctx, `
		with candidates as (
			select id from steward_agent_episodes
			where status='thinking' and (lease_expires_at is null or lease_expires_at<now())
			order by updated_at for update skip locked limit $1
		)
		update steward_agent_episodes episode set lease_owner=$2,lease_expires_at=now()+interval '90 seconds',
		       updated_at=now(),version=version+1
		from candidates where episode.id=candidates.id returning episode.id::text
	`, limit, worker)
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
		if err := s.advanceAgentEpisode(ctx, id); err != nil {
			var failures int
			updateErr := s.db.Pool.QueryRow(ctx, `update steward_agent_episodes set failure_summary=$2,model_failure_count=model_failure_count+1,
				lease_owner='',lease_expires_at=null,updated_at=now() where id=$1 and status='thinking' returning model_failure_count`, id, sanitizeRuntimeError(err)).Scan(&failures)
			if updateErr == nil && failures >= 5 {
				if episode, getErr := s.GetAgentEpisode(ctx, id); getErr == nil {
					_ = s.finishAgentEpisodeWithText(ctx, episode, "模型连续失败，循环 Agent 已停止："+sanitizeRuntimeError(err), agentEpisodeFailed)
				}
			}
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) advanceAgentEpisode(ctx context.Context, id string) error {
	episode, err := s.GetAgentEpisode(ctx, id)
	if err != nil || episode.Status != agentEpisodeThinking {
		return err
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
	transcript := make([]AgentTurnTranscript, 0, len(episode.Turns))
	for index, turn := range episode.Turns {
		results := turn.ToolResults
		reasoning := turn.ReasoningContent
		if index < len(episode.Turns)-4 && len(results) > 0 {
			compacted := make([]domain.StewardAgentToolResult, 0, len(results))
			for _, result := range results {
				encoded, _ := json.Marshal(result.Output)
				compacted = append(compacted, domain.StewardAgentToolResult{
					ToolCallID: result.ToolCallID, ToolName: result.ToolName, Error: truncateAdvisorText(result.Error, 1000), Evidence: result.Evidence,
					Output: map[string]any{"compacted_summary": truncateAdvisorText(string(encoded), 2000), "sha256": agentFingerprint(encoded)},
				})
			}
			results = compacted
			reasoning = ""
		}
		transcript = append(transcript, AgentTurnTranscript{AssistantContent: truncateAdvisorText(turn.AssistantContent, 4000), ReasoningContent: reasoning, ToolCalls: turn.ToolCalls, ToolResults: results})
	}
	notice := ""
	if episode.NoProgressCount >= episode.NoProgressLimit {
		notice = "最近多轮调用产生了相同结果。请反思目标，改用其他工具或给出最终结论，不要重复相同调用。"
	}
	modelTools, toolCatalog, catalogErr := s.agentToolContext(ctx, &episode)
	if catalogErr != nil {
		return catalogErr
	}
	decision, err := nextValidAgentTurn(ctx, advisor, AgentTurnInput{
		Message: episode.Goal, DataLevel: episode.DataLevel, TriggerKind: episode.TriggerKind,
		History: history, Transcript: transcript, Context: localContext, Tools: modelTools, ToolCatalog: toolCatalog,
		Devices: s.conversationAdvisorDevices(ctx), KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(),
		Round: episode.CurrentRound + 1, ToolCallCount: episode.ToolCallCount, Deadline: episode.DeadlineAt, NoProgressNotice: notice,
	})
	if err != nil {
		return err
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
	if err := s.insertAgentTurn(ctx, turn); err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set current_round=$2,tool_call_count=tool_call_count+$3,model_failure_count=0,failure_summary='',updated_at=$4 where id=$1`, episode.ID, turn.RoundIndex, externalCalls, now)
	if err != nil {
		return err
	}
	episode.CurrentRound = turn.RoundIndex
	episode.ToolCallCount += externalCalls
	conversation, err := s.getConversation(ctx, episode.ConversationID)
	if err != nil {
		return err
	}
	var trigger domain.StewardConversationMessage
	if err := s.db.Pool.QueryRow(ctx, `select id::text,conversation_id::text,role,content,data_level,model,context_summary,false,created_at from steward_conversation_messages where id=$1`, episode.TriggerMessageID).Scan(
		&trigger.ID, &trigger.ConversationID, &trigger.Role, &trigger.Content, &trigger.DataLevel, &trigger.Model, &trigger.ContextSummary, &trigger.PayloadEncrypted, &trigger.CreatedAt); err != nil {
		return err
	}
	_, err = s.applyAgentTurnDecision(ctx, conversation, trigger, episode, turn)
	return err
}

func (s *Service) finishAgentEpisode(ctx context.Context, episode domain.StewardAgentEpisode, turn domain.StewardAgentTurn, text string, silent bool) (domain.StewardConversationMessage, error) {
	now := time.Now().UTC()
	var message domain.StewardConversationMessage
	var err error
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
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_turns set status=$2,completed_at=$3,updated_at=$3 where id=$1`, turn.ID, status, now)
	if err == nil {
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='completed',final_message_id=nullif($2,'')::uuid,
		       progress_message_id=coalesce(nullif($2,'')::uuid,progress_message_id),active_execution_id=null,
		       completed_at=$3,updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, episode.ID, message.ID, now)
	}
	if err != nil {
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
	episode, err := s.GetAgentEpisode(ctx, id)
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
	episode, err := s.GetAgentEpisode(ctx, id)
	if err != nil {
		return err
	}
	confirmed := true
	details := []string{episode.Goal}
	for _, turn := range episode.Turns {
		if strings.TrimSpace(turn.AssistantContent) != "" {
			details = append(details, turn.AssistantContent)
		}
		for _, call := range turn.ToolCalls {
			details = append(details, call.ToolName)
		}
	}
	memory, err := s.CreateMemory(ctx, CreateMemoryInput{
		Type: "execution_episode", Title: "已完成：" + truncateAdvisorText(episode.Goal, 200),
		Summary: fmt.Sprintf("循环 Agent 用 %d 轮、%d 次工具调用完成任务：%s", episode.CurrentRound, episode.ToolCallCount, truncateAdvisorText(strings.Join(details, "；"), 1000)),
		Content: strings.Join(details, "\n"), Scope: "conversation:" + episode.ConversationID, Source: "agent_episode",
		DataLevel: episode.DataLevel, PermissionLevel: PermissionA9, Confidence: 1, UserConfirmed: &confirmed,
	})
	if err != nil {
		return err
	}
	for _, turn := range episode.Turns {
		if turn.ExecutionID != "" {
			_, _ = s.CreateSourceRef(ctx, CreateSourceRefInput{TargetType: "memory", TargetID: memory.ID, SourceType: "conversation_execution", SourceID: turn.ExecutionID, Summary: fmt.Sprintf("Agent 第 %d 轮", turn.RoundIndex), Confidence: 1})
		}
	}
	return nil
}

func (s *Service) DecideAgentEpisode(ctx context.Context, id string, input DecideAgentEpisodeInput) (domain.StewardAgentEpisode, error) {
	episode, err := s.GetAgentEpisode(ctx, id)
	if err != nil {
		return episode, err
	}
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	now := time.Now().UTC()
	switch decision {
	case "pause":
		if episode.ActiveExecutionID != "" {
			if execution, getErr := s.getConversationExecution(ctx, episode.ActiveExecutionID); getErr == nil {
				_, _ = s.cancelConversationExecution(ctx, execution, true)
			}
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='paused',updated_at=$2,lease_owner='',lease_expires_at=null,version=version+1 where id=$1 and status in ('thinking','executing')`, id, now)
	case "resume":
		if episode.Status != agentEpisodePaused && episode.Status != agentEpisodeBlocked {
			return episode, fmt.Errorf("agent episode cannot resume from %s", episode.Status)
		}
		if episode.ActiveExecutionID != "" {
			_ = s.abandonActiveAgentTurn(ctx, episode, "execution paused; replan required")
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',active_execution_id=null,failure_summary='',updated_at=$2,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, id, now)
	case "cancel":
		if episode.ActiveExecutionID != "" {
			if execution, getErr := s.getConversationExecution(ctx, episode.ActiveExecutionID); getErr == nil {
				_, _ = s.cancelConversationExecution(ctx, execution, false)
			}
			_ = s.abandonActiveAgentTurn(ctx, episode, "execution cancelled because the target device changed")
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='cancelled',active_execution_id=null,completed_at=$2,updated_at=$2,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, id, now)
	case "switch_device":
		target, _, targetErr := s.selectConversationExecutionTarget(ctx, episode.Goal, input.TargetDeviceID)
		if targetErr != nil {
			return episode, targetErr
		}
		if episode.ActiveExecutionID != "" {
			if execution, getErr := s.getConversationExecution(ctx, episode.ActiveExecutionID); getErr == nil {
				_, _ = s.cancelConversationExecution(ctx, execution, false)
			}
		}
		_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set status='thinking',target_device_id=$2,active_execution_id=null,updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, id, target.ID, now)
	default:
		return episode, fmt.Errorf("decision must be pause, resume, cancel, or switch_device")
	}
	if err != nil {
		return episode, err
	}
	return s.GetAgentEpisode(ctx, id)
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
	episode, getErr := s.GetAgentEpisode(ctx, id)
	if getErr != nil {
		return domain.StewardAgentEpisode{}, false, getErr
	}
	if len(episode.Turns) > 0 {
		turn := episode.Turns[len(episode.Turns)-1]
		if turn.Status == "waiting_input" && len(turn.ToolCalls) == 1 {
			results, _ := json.Marshal([]domain.StewardAgentToolResult{{
				ToolCallID: turn.ToolCalls[0].ID, ToolName: agentAskUserTool, Output: map[string]any{"user_response": userContent},
			}})
			_, _ = s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,updated_at=$3,completed_at=$3 where id=$1`, turn.ID, string(results), now)
		}
	}
	_, err = s.db.Pool.Exec(ctx, `update steward_agent_episodes set goal=goal||E'\n\n用户补充：'||$2,status='thinking',updated_at=$3,lease_owner='',lease_expires_at=null,version=version+1 where id=$1`, id, userContent, now)
	if err != nil {
		return domain.StewardAgentEpisode{}, false, err
	}
	episode, err = s.GetAgentEpisode(ctx, id)
	return episode, true, err
}

func (s *Service) abandonActiveAgentTurn(ctx context.Context, episode domain.StewardAgentEpisode, reason string) error {
	for index := len(episode.Turns) - 1; index >= 0; index-- {
		turn := episode.Turns[index]
		if turn.ExecutionID != episode.ActiveExecutionID || turn.Status != "tools_running" {
			continue
		}
		results := make([]domain.StewardAgentToolResult, 0, len(turn.ToolCalls))
		for _, call := range turn.ToolCalls {
			results = append(results, domain.StewardAgentToolResult{ToolCallID: call.ID, ToolName: call.ToolName, Error: reason})
		}
		encoded, _ := json.Marshal(results)
		_, err := s.db.Pool.Exec(ctx, `update steward_agent_turns set status='tools_complete',tool_results=$2::jsonb,completed_at=$3,updated_at=$3 where id=$1`, turn.ID, string(encoded), time.Now().UTC())
		return err
	}
	return nil
}
