package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	agentEpisodeOverviewTurnLimit = 6
	agentHistoryRecentTurnLimit   = 20
	agentWorkingAnchorLimit       = 256
	agentWorkingPendingLimit      = 128
	agentWorkingEvidenceLimit     = 512
	agentWorkingSummaryBudget     = 16000
	agentMemoryContentBudget      = 24000
	agentExecutionRefPageSize     = 128
)

// GetAgentEpisodeOverview returns bounded recent history for status polling.
// Full history is exposed separately through ListAgentEpisodeTurnsPage.
func (s *Service) GetAgentEpisodeOverview(ctx context.Context, id string, recentLimit int) (domain.StewardAgentEpisode, error) {
	if recentLimit <= 0 || recentLimit > 50 {
		recentLimit = agentEpisodeOverviewTurnLimit
	}
	item, err := s.getAgentEpisodeState(ctx, id)
	if err != nil {
		return item, err
	}
	page, err := s.ListAgentEpisodeTurnsPage(ctx, item.ID, 0, recentLimit)
	if err != nil {
		return item, err
	}
	item.Turns = page.Turns
	item.TurnCount = page.Total
	item.TurnsHasMore = page.HasMore
	return item, nil
}

// getAgentEpisodeState loads only the fixed-size episode row. It deliberately
// does not count or materialize turns, so completion, repair and control paths
// remain O(1) as an Episode grows to thousands of rounds.
func (s *Service) getAgentEpisodeState(ctx context.Context, id string) (domain.StewardAgentEpisode, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id::text,conversation_id::text,trigger_message_id::text,coalesce(progress_message_id::text,''),
		       coalesce(final_message_id::text,''),trigger_kind,goal,data_level,status,current_round,tool_call_count,
		       max_rounds,max_tool_calls,max_duration_seconds,no_progress_limit,no_progress_count,model_failure_count,target_device_id,
		       coalesce(active_execution_id::text,''),control_generation,failure_summary,last_result_summary,
		       hydrated_tool_names,catalog_generation,current_tool_versions,working_state,summary_through_round,
		       current_round,
		       created_at,updated_at,deadline_at,completed_at
		from steward_agent_episodes where id=$1
	`, id)
	item, err := scanAgentEpisodeOverview(row)
	return item, err
}

type agentEpisodeScanner interface {
	Scan(...any) error
}

func scanAgentEpisodeOverview(row agentEpisodeScanner) (domain.StewardAgentEpisode, error) {
	var item domain.StewardAgentEpisode
	var hydrated, versions, working []byte
	err := row.Scan(&item.ID, &item.ConversationID, &item.TriggerMessageID, &item.ProgressMessageID,
		&item.FinalMessageID, &item.TriggerKind, &item.Goal, &item.DataLevel, &item.Status, &item.CurrentRound,
		&item.ToolCallCount, &item.MaxRounds, &item.MaxToolCalls, &item.MaxDurationSeconds, &item.NoProgressLimit,
		&item.NoProgressCount, &item.ModelFailureCount, &item.TargetDeviceID, &item.ActiveExecutionID, &item.ControlGeneration,
		&item.FailureSummary, &item.LastResultSummary, &hydrated, &item.CatalogGeneration, &versions, &working,
		&item.SummaryThroughRound, &item.TurnCount, &item.CreatedAt, &item.UpdatedAt, &item.DeadlineAt, &item.CompletedAt)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal(hydrated, &item.HydratedToolNames)
	_ = json.Unmarshal(versions, &item.CurrentToolVersions)
	_ = json.Unmarshal(working, &item.WorkingState)
	return item, nil
}

// ListAgentEpisodeTurnsPage pages backwards using an exclusive round cursor.
// Rows inside a page are returned in chronological order so callers can prepend
// older pages without re-sorting the complete history.
func (s *Service) ListAgentEpisodeTurnsPage(ctx context.Context, episodeID string, beforeRound, limit int) (domain.StewardAgentTurnPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var total int
	// round_index is unique and contiguous for every persisted model turn, so
	// current_round is the O(1) authoritative total. Counting the child table
	// here would make every status poll grow with Episode age.
	if err := s.db.Pool.QueryRow(ctx, `select current_round from steward_agent_episodes where id=$1`, episodeID).Scan(&total); err != nil {
		return domain.StewardAgentTurnPage{}, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns
		where episode_id=$1 and ($2::integer <= 0 or round_index < $2)
		order by round_index desc
		limit $3
	`, episodeID, beforeRound, limit+1)
	if err != nil {
		return domain.StewardAgentTurnPage{}, err
	}
	defer rows.Close()
	items := make([]domain.StewardAgentTurn, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanAgentTurn(rows)
		if scanErr != nil {
			return domain.StewardAgentTurnPage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.StewardAgentTurnPage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RoundIndex < items[j].RoundIndex })
	nextBefore := 0
	if hasMore && len(items) > 0 {
		nextBefore = items[0].RoundIndex
	}
	return domain.StewardAgentTurnPage{Turns: items, NextBeforeRound: nextBefore, HasMore: hasMore, Total: total}, nil
}

func scanAgentTurn(scanner agentEpisodeScanner) (domain.StewardAgentTurn, error) {
	var item domain.StewardAgentTurn
	var calls, results []byte
	err := scanner.Scan(&item.ID, &item.EpisodeID, &item.RoundIndex, &item.Status, &item.AssistantContent,
		&item.ReasoningContent, &calls, &results, &item.Provider, &item.Model, &item.ProviderResponseID,
		&item.ExecutionID, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	if err != nil {
		return item, err
	}
	_ = json.Unmarshal(calls, &item.ToolCalls)
	_ = json.Unmarshal(results, &item.ToolResults)
	if item.ToolCalls == nil {
		item.ToolCalls = []domain.StewardAgentToolCall{}
	}
	if item.ToolResults == nil {
		item.ToolResults = []domain.StewardAgentToolResult{}
	}
	return item, nil
}

func (s *Service) getAgentTurn(ctx context.Context, episodeID, turnID string) (domain.StewardAgentTurn, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns where episode_id=$1 and id=$2
	`, episodeID, turnID)
	return scanAgentTurn(row)
}

func (s *Service) getLatestAgentTurn(ctx context.Context, episodeID string) (domain.StewardAgentTurn, error) {
	row := s.db.Pool.QueryRow(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns where episode_id=$1 order by round_index desc limit 1
	`, episodeID)
	return scanAgentTurn(row)
}

func (s *Service) listRecentAgentTurns(ctx context.Context, episodeID string, limit int) ([]domain.StewardAgentTurn, error) {
	if limit <= 0 || limit > 100 {
		limit = agentHistoryRecentTurnLimit
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns where episode_id=$1 order by round_index desc limit $2
	`, episodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.StewardAgentTurn, 0, limit)
	for rows.Next() {
		item, scanErr := scanAgentTurn(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].RoundIndex < items[j].RoundIndex })
	return items, nil
}

type agentExecutionReference struct {
	ExecutionID string
	RoundIndex  int
}

// forEachAgentEpisodeExecution pages through every historical child execution
// without retaining the complete result set. Memory can therefore reference
// all evidence even when its human-readable body uses bounded working state.
func (s *Service) forEachAgentEpisodeExecution(ctx context.Context, episodeID string, visit func(agentExecutionReference) error) error {
	cursor := 0
	for {
		rows, err := s.db.Pool.Query(ctx, `
			select execution_id::text,round_index
			from steward_agent_turns
			where episode_id=$1 and execution_id is not null and round_index>$2
			order by round_index
			limit $3
		`, episodeID, cursor, agentExecutionRefPageSize)
		if err != nil {
			return err
		}
		page := make([]agentExecutionReference, 0, agentExecutionRefPageSize)
		for rows.Next() {
			var item agentExecutionReference
			if err := rows.Scan(&item.ExecutionID, &item.RoundIndex); err != nil {
				rows.Close()
				return err
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, item := range page {
			if err := visit(item); err != nil {
				return err
			}
			cursor = item.RoundIndex
		}
		if len(page) < agentExecutionRefPageSize {
			return nil
		}
	}
}

func buildAgentEpisodeMemoryDetails(episode domain.StewardAgentEpisode) string {
	parts := make([]string, 0, 2+agentHistoryRecentTurnLimit)
	used := 0
	appendPart := func(value string) bool {
		value = strings.TrimSpace(value)
		if value == "" {
			return true
		}
		value = truncateAdvisorText(value, 2000)
		length := len([]rune(value))
		separator := 0
		if len(parts) > 0 {
			separator = 1
		}
		if used+separator+length > agentMemoryContentBudget {
			return false
		}
		parts = append(parts, value)
		used += separator + length
		return true
	}
	_ = appendPart(episode.Goal)
	_ = appendPart(episode.WorkingState.Summary)
	recent := episode.Turns
	if len(recent) > agentHistoryRecentTurnLimit {
		recent = recent[len(recent)-agentHistoryRecentTurnLimit:]
	}
	remainingBudget := agentMemoryContentBudget - used - len(recent)
	perTurnBudget := remainingBudget / max(1, len(recent))
	if perTurnBudget > 2000 {
		perTurnBudget = 2000
	}
	for _, turn := range recent {
		if strings.HasPrefix(turn.ID, "working-state:") {
			continue
		}
		turnDetails := []string{fmt.Sprintf("第 %d 轮：%s", turn.RoundIndex, turn.AssistantContent)}
		for index, call := range turn.ToolCalls {
			if index >= 16 {
				break
			}
			turnDetails = append(turnDetails, "工具="+call.ToolName)
		}
		for index, result := range turn.ToolResults {
			if index >= 16 {
				break
			}
			value := result.Error
			if value == "" {
				value = summarizeAgentToolSuccess(result.ToolName, result.Output)
			}
			turnDetails = append(turnDetails, fmt.Sprintf("结果=%s %s", result.ToolName, value))
		}
		if !appendPart(truncateAdvisorText(strings.Join(turnDetails, "；"), perTurnBudget)) {
			break
		}
	}
	return strings.Join(parts, "\n")
}

// RefreshAgentEpisodeWorkingState incrementally folds completed old turns into
// a bounded durable state. It never deletes or mutates the source turns.
func (s *Service) RefreshAgentEpisodeWorkingState(ctx context.Context, episodeID string, keepRecent int) (domain.StewardAgentWorkingState, int, error) {
	if keepRecent <= 0 {
		keepRecent = agentHistoryRecentTurnLimit
	}
	tx, err := s.db.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.StewardAgentWorkingState{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var raw []byte
	var through, currentRound int
	if err := tx.QueryRow(ctx, `select working_state,summary_through_round,current_round
		from steward_agent_episodes where id=$1 for update`, episodeID).Scan(&raw, &through, &currentRound); err != nil {
		return domain.StewardAgentWorkingState{}, 0, err
	}
	state := domain.StewardAgentWorkingState{}
	_ = json.Unmarshal(raw, &state)
	cutoff := currentRound - keepRecent
	if cutoff <= through {
		return state, through, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `
		select id::text,episode_id::text,round_index,status,assistant_content,reasoning_content,tool_calls,tool_results,
		       provider,model,provider_response_id,coalesce(execution_id::text,''),failure_summary,created_at,updated_at,completed_at
		from steward_agent_turns
		where episode_id=$1 and round_index>$2 and round_index<=$3
		order by round_index
	`, episodeID, through, cutoff)
	if err != nil {
		return domain.StewardAgentWorkingState{}, through, err
	}
	lastFolded := through
	expected := through + 1
	for rows.Next() {
		turn, scanErr := scanAgentTurn(rows)
		if scanErr != nil {
			rows.Close()
			return domain.StewardAgentWorkingState{}, through, scanErr
		}
		if turn.RoundIndex != expected || !agentTurnIsDurablyComplete(turn.Status) {
			break
		}
		foldAgentWorkingState(&state, turn)
		lastFolded = turn.RoundIndex
		expected++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.StewardAgentWorkingState{}, through, err
	}
	if lastFolded == through {
		return state, through, tx.Commit(ctx)
	}
	state.Summary = renderAgentWorkingSummary(state, lastFolded)
	encoded, err := json.Marshal(state)
	if err != nil {
		return domain.StewardAgentWorkingState{}, through, err
	}
	if _, err := tx.Exec(ctx, `update steward_agent_episodes set working_state=$2::jsonb,summary_through_round=$3,
		updated_at=now(),version=version+1 where id=$1`, episodeID, string(encoded), lastFolded); err != nil {
		return domain.StewardAgentWorkingState{}, through, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.StewardAgentWorkingState{}, through, err
	}
	return state, lastFolded, nil
}

func agentTurnIsDurablyComplete(status string) bool {
	switch status {
	case "tools_complete", "final", "silent", "failed":
		return true
	default:
		return false
	}
}

// GetAgentEpisodeForLoop is the bounded replacement for GetAgentEpisode on
// model-decision paths. The synthetic assistant-only first turn carries the
// persisted old-history summary and cannot create orphan tool messages.
func (s *Service) GetAgentEpisodeForLoop(ctx context.Context, id string) (domain.StewardAgentEpisode, error) {
	state, through, err := s.RefreshAgentEpisodeWorkingState(ctx, id, agentHistoryRecentTurnLimit)
	if err != nil {
		return domain.StewardAgentEpisode{}, err
	}
	item, err := s.getAgentEpisodeState(ctx, id)
	if err != nil {
		return item, err
	}
	item.Turns, err = s.listRecentAgentTurns(ctx, id, agentHistoryRecentTurnLimit)
	if err != nil {
		return item, err
	}
	item.TurnCount = item.CurrentRound
	item.TurnsHasMore = item.CurrentRound > len(item.Turns)
	item.WorkingState = state
	item.SummaryThroughRound = through
	if strings.TrimSpace(state.Summary) != "" {
		item.Turns = append([]domain.StewardAgentTurn{{
			ID:               fmt.Sprintf("working-state:%s:%d", id, through),
			EpisodeID:        id,
			RoundIndex:       through,
			Status:           "tools_complete",
			AssistantContent: state.Summary,
			ToolCalls:        []domain.StewardAgentToolCall{},
			ToolResults:      []domain.StewardAgentToolResult{},
		}}, item.Turns...)
	}
	return item, nil
}

func foldAgentWorkingState(state *domain.StewardAgentWorkingState, turn domain.StewardAgentTurn) {
	state.CompletedRounds++
	for _, call := range turn.ToolCalls {
		for _, anchor := range collectAgentHistoryAnchors(call.Arguments, "arg", 0) {
			state.Anchors = appendBoundedDurable(state.Anchors,
				fmt.Sprintf("第%d轮 %s %s", turn.RoundIndex, call.ToolName, anchor), agentWorkingAnchorLimit)
		}
	}
	for _, result := range turn.ToolResults {
		if strings.TrimSpace(result.Error) != "" {
			state.PendingItems = appendBoundedDurable(state.PendingItems,
				fmt.Sprintf("第%d轮 %s 失败: %s", turn.RoundIndex, result.ToolName, truncateAdvisorText(result.Error, 600)), agentWorkingPendingLimit)
		} else if summary := strings.TrimSpace(summarizeAgentToolSuccess(result.ToolName, result.Output)); summary != "" {
			state.Anchors = appendBoundedDurable(state.Anchors,
				fmt.Sprintf("第%d轮 %s 结果: %s", turn.RoundIndex, result.ToolName, truncateAdvisorText(summary, 800)), agentWorkingAnchorLimit)
		}
		for _, anchor := range collectAgentHistoryAnchors(result.Output, "result", 0) {
			state.Anchors = appendBoundedDurable(state.Anchors,
				fmt.Sprintf("第%d轮 %s %s", turn.RoundIndex, result.ToolName, anchor), agentWorkingAnchorLimit)
		}
		for _, evidence := range collectAgentHistoryAnchors(result.Evidence, "evidence", 0) {
			state.EvidenceReferences = appendBoundedDurable(state.EvidenceReferences,
				fmt.Sprintf("第%d轮 %s %s", turn.RoundIndex, result.ToolName, evidence), agentWorkingEvidenceLimit)
		}
	}
	if looksLikePendingAgentText(turn.AssistantContent) {
		state.PendingItems = appendBoundedDurable(state.PendingItems,
			fmt.Sprintf("第%d轮: %s", turn.RoundIndex, truncateAdvisorText(turn.AssistantContent, 800)), agentWorkingPendingLimit)
	}
}

func collectAgentHistoryAnchors(value map[string]any, prefix string, depth int) []string {
	if len(value) == 0 || depth > 4 {
		return nil
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := []string{}
	for _, key := range keys {
		item := value[key]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if agentHistoryKeyIsAnchor(key) {
			if text := compactAgentHistoryScalar(item); text != "" {
				items = append(items, path+"="+text)
			}
		}
		switch nested := item.(type) {
		case map[string]any:
			items = append(items, collectAgentHistoryAnchors(nested, path, depth+1)...)
		case []any:
			for index, child := range nested {
				if index >= 16 {
					break
				}
				if childMap, ok := child.(map[string]any); ok {
					items = append(items, collectAgentHistoryAnchors(childMap, path+"["+strconv.Itoa(index)+"]", depth+1)...)
				}
			}
		}
	}
	return items
}

func agentHistoryKeyIsAnchor(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "id" || key == "url" || key == "uri" || key == "path" || key == "name" || key == "sha256" || key == "hash" || key == "file" || key == "directory" {
		return true
	}
	return strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_path") || strings.Contains(key, "evidence") || strings.Contains(key, "destination") || strings.Contains(key, "source")
}

func compactAgentHistoryScalar(value any) string {
	switch typed := value.(type) {
	case string:
		return truncateAdvisorText(typed, 600)
	case float64, float32, int, int32, int64, bool, json.Number:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func looksLikePendingAgentText(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"待处理", "待完成", "下一步", "需要继续", "todo", "pending", "next step"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// appendBoundedDurable permanently retains the first half (early decisions and
// paths) and continuously refreshes the second half with the latest facts.
func appendBoundedDurable(items []string, value string, limit int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	if limit < 2 || len(items) < limit {
		return append(items, value)
	}
	stable := limit / 2
	copy(items[stable:limit-1], items[stable+1:limit])
	items[limit-1] = value
	return items
}

func renderAgentWorkingSummary(state domain.StewardAgentWorkingState, through int) string {
	sections := []string{fmt.Sprintf("持久化工作状态（已压缩并验证至第 %d 轮，共归纳 %d 个完成回合）：", through, state.CompletedRounds)}
	appendSection := func(title string, values []string, budget, maxItems int) {
		if len(values) == 0 {
			return
		}
		values = selectDurableSummaryValues(values, maxItems)
		part := []string{title}
		for _, value := range values {
			value = truncateAdvisorText(value, 420)
			candidate := strings.Join(append(part, "- "+value), "\n")
			if len([]rune(candidate)) > budget {
				part = append(part, "- 更多记录仍保存在可分页的原始回合与证据中。")
				break
			}
			part = append(part, "- "+value)
		}
		sections = append(sections, part...)
	}
	// Reserve independent budgets so a large success history can never crowd
	// out unresolved work or the evidence needed to verify it.
	appendSection("关键路径、标识与结果：", state.Anchors, 8500, 16)
	appendSection("尚待处理或失败项：", state.PendingItems, 3500, 8)
	appendSection("证据引用：", state.EvidenceReferences, 3000, 8)
	return truncateAdvisorText(strings.Join(sections, "\n"), agentWorkingSummaryBudget)
}

func selectDurableSummaryValues(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	stable := limit / 2
	selected := make([]string, 0, limit)
	selected = append(selected, values[:stable]...)
	selected = append(selected, values[len(values)-(limit-stable):]...)
	return selected
}
