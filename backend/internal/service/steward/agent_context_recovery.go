package steward

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mongojson/backend/internal/domain"
)

const (
	agentContextRetryHistoryLimit     = 4
	agentContextRetryContextLimit     = 4
	agentContextRetryTranscriptLimit  = 6
	agentContextRetrySummaryBudget    = 4000
	agentContextRetryRecentBudget     = 24000
	agentContextRetryCatalogDescLimit = 160
)

// nextAgentTurnWithContextRecovery makes one deliberately smaller request
// when an OpenAI-compatible provider says that the original request exceeded
// its context or HTTP body limit. The durable Episode, Turns and evidence are
// not changed; only this retry's model view is compacted.
func nextAgentTurnWithContextRecovery(ctx context.Context, advisor AgentTurnAdvisor, input AgentTurnInput) (AgentTurnDecision, error) {
	decision, err := nextValidAgentTurn(ctx, advisor, input)
	if err == nil || !advisorContextSizeExceeded(err) {
		return decision, err
	}

	retryInput := compactAgentTurnInputForContextRetry(input)
	decision, retryErr := nextValidAgentTurn(ctx, advisor, retryInput)
	if retryErr == nil {
		return decision, nil
	}
	if advisorContextSizeExceeded(retryErr) {
		return AgentTurnDecision{}, fmt.Errorf("model context remained too large after automatic compaction: %w", retryErr)
	}
	return AgentTurnDecision{}, fmt.Errorf("model retry after automatic context compaction failed: %w", retryErr)
}

// advisorContextSizeExceeded is intentionally narrow: a generic 400 must not
// be retried because authentication, schema and protocol failures need their
// original diagnosis. A 413 always denotes an oversized HTTP request; a 400
// must also carry a known context/token-size marker.
func advisorContextSizeExceeded(err error) bool {
	var httpErr *advisorHTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return false
	}
	if httpErr.StatusCode == 413 {
		return true
	}
	if httpErr.StatusCode != 400 {
		return false
	}

	message := strings.ToLower(strings.Join([]string{httpErr.ProviderMsg, httpErr.Status}, " "))
	markers := []string{
		"context_length_exceeded",
		"context length",
		"context window",
		"maximum context",
		"max context",
		"too many tokens",
		"token limit",
		"maximum number of tokens",
		"request too large",
		"request entity too large",
		"payload too large",
		"input is too long",
		"input too long",
		"prompt is too long",
		"prompt too long",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func compactAgentTurnInputForContextRetry(input AgentTurnInput) AgentTurnInput {
	compacted := input
	compacted.History = compactAgentHistoryForContextRetry(input.History)
	compacted.Context = compactAgentLocalContextForRetry(input.Context)
	compacted.Transcript = compactAgentTranscriptForContextRetry(input.Transcript)
	compacted.ToolCatalog = compactAgentToolCatalogForContextRetry(input.ToolCatalog)
	notice := "模型服务拒绝了上一请求的上下文大小；当前已使用持久化摘要和最近完整工具回合继续。不要重复已经完成的工具调用。"
	if strings.TrimSpace(compacted.NoProgressNotice) == "" {
		compacted.NoProgressNotice = notice
	} else {
		compacted.NoProgressNotice = compacted.NoProgressNotice + " " + notice
	}
	return compacted
}

func compactAgentHistoryForContextRetry(history []ConversationAdvisorMessage) []ConversationAdvisorMessage {
	start := len(history) - agentContextRetryHistoryLimit
	if start < 0 {
		start = 0
	}
	items := make([]ConversationAdvisorMessage, 0, len(history)-start)
	for _, item := range history[start:] {
		item.Content = truncateAdvisorText(item.Content, 1000)
		items = append(items, item)
	}
	return items
}

func compactAgentLocalContextForRetry(items []domain.StewardSearchResult) []domain.StewardSearchResult {
	if len(items) > agentContextRetryContextLimit {
		items = items[:agentContextRetryContextLimit]
	}
	compacted := make([]domain.StewardSearchResult, 0, len(items))
	for _, item := range items {
		item.Title = truncateAdvisorText(item.Title, 160)
		item.Summary = truncateAdvisorText(item.Summary, 600)
		compacted = append(compacted, item)
	}
	return compacted
}

func compactAgentToolCatalogForContextRetry(items []AgentToolCatalogEntry) []AgentToolCatalogEntry {
	compacted := make([]AgentToolCatalogEntry, 0, len(items))
	for _, item := range items {
		item.Description = truncateAdvisorText(item.Description, agentContextRetryCatalogDescLimit)
		compacted = append(compacted, item)
	}
	return compacted
}

// compactAgentTranscriptForContextRetry preserves the native protocol invariant
// that every retained assistant tool_call is followed by exactly one result
// carrying the same tool_call_id. Older turns become assistant-only summary
// text, so a provider never sees an orphan tool message.
func compactAgentTranscriptForContextRetry(transcript []AgentTurnTranscript) []AgentTurnTranscript {
	if len(transcript) == 0 {
		return []AgentTurnTranscript{}
	}
	recentStart := len(transcript) - agentContextRetryTranscriptLimit
	if recentStart < 0 {
		recentStart = 0
	}
	result := make([]AgentTurnTranscript, 0, len(transcript)-recentStart+1)
	if recentStart > 0 {
		result = append(result, AgentTurnTranscript{
			AssistantContent: summarizeAgentTranscriptPrefix(transcript[:recentStart], agentContextRetrySummaryBudget),
		})
	}

	recent := transcript[recentStart:]
	perTurnBudget := agentContextRetryRecentBudget / agentMaxInt(len(recent), 1)
	if perTurnBudget < 1200 {
		perTurnBudget = 1200
	}
	for _, turn := range recent {
		result = append(result, compactAgentTranscriptTurnForRetry(turn, perTurnBudget))
	}
	return result
}

func summarizeAgentTranscriptPrefix(turns []AgentTurnTranscript, budget int) string {
	lines := []string{fmt.Sprintf("较早的 %d 个模型回合已从本次请求压缩；原始回合和证据仍已持久化：", len(turns))}
	for index, turn := range turns {
		parts := []string{fmt.Sprintf("回合 %d", index+1)}
		contentBudget := 240
		// buildAgentTranscript emits the durable old-turn working summary as an
		// assistant-only first item. Give that item more room than ordinary
		// progress prose so established paths/identifiers survive the emergency
		// compaction whenever possible.
		if index == 0 && len(turn.ToolCalls) == 0 {
			contentBudget = agentMinInt(1800, budget/2)
		}
		if content := strings.TrimSpace(truncateAdvisorText(turn.AssistantContent, contentBudget)); content != "" {
			parts = append(parts, content)
		}
		if len(turn.ToolCalls) > 0 {
			names := make([]string, 0, len(turn.ToolCalls))
			for _, call := range turn.ToolCalls {
				names = append(names, call.ToolName)
			}
			parts = append(parts, "工具="+strings.Join(names, ","))
		}
		failed := 0
		for _, toolResult := range turn.ToolResults {
			if strings.TrimSpace(toolResult.Error) != "" {
				failed++
			}
		}
		if len(turn.ToolResults) > 0 {
			parts = append(parts, fmt.Sprintf("结果=%d成功/%d失败", len(turn.ToolResults)-failed, failed))
		}
		lines = append(lines, strings.Join(parts, "；"))
		if len([]rune(strings.Join(lines, "\n"))) >= budget {
			break
		}
	}
	return truncateAdvisorText(strings.Join(lines, "\n"), budget)
}

func compactAgentTranscriptTurnForRetry(turn AgentTurnTranscript, budget int) AgentTurnTranscript {
	itemCount := 2 + len(turn.ToolCalls)*2
	perItem := budget / agentMaxInt(itemCount, 1)
	if perItem < 192 {
		perItem = 192
	}
	if perItem > 1200 {
		perItem = 1200
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
			result = domain.StewardAgentToolResult{Error: "历史工具结果未完整落库"}
		}
		result.ToolCallID = call.ID
		result.ToolName = defaultString(result.ToolName, call.ToolName)
		result.Error = truncateAdvisorText(result.Error, perItem)
		result.Output = compactAgentTranscriptMap(result.Output, perItem)
		result.Evidence = compactAgentTranscriptMap(result.Evidence, agentMinInt(perItem, 400))
		results = append(results, result)
	}

	return AgentTurnTranscript{
		AssistantContent: truncateAdvisorText(turn.AssistantContent, perItem),
		ReasoningContent: truncateAdvisorText(turn.ReasoningContent, agentMinInt(perItem, 400)),
		ToolCalls:        calls,
		ToolResults:      results,
	}
}
