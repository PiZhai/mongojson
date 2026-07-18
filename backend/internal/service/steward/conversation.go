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

const (
	conversationRoleUser      = "user"
	conversationRoleAssistant = "assistant"
	conversationRoleSystem    = "system"
)

type CreateConversationInput struct {
	Title     string `json:"title"`
	DataLevel string `json:"data_level"`
}

type SendConversationMessageInput struct {
	Content      string `json:"content"`
	DataLevel    string `json:"data_level"`
	ContextLimit int    `json:"context_limit"`
}

type UpdateConversationInput struct {
	Archived bool `json:"archived"`
}

type DecideConversationSuggestionInput struct {
	Decision string `json:"decision"`
}

type SendConversationMessageResult struct {
	Conversation domain.StewardConversation        `json:"conversation"`
	Message      domain.StewardConversationMessage `json:"message"`
}

func (s *Service) CreateConversation(ctx context.Context, input CreateConversationInput) (domain.StewardConversation, error) {
	level, err := conversationDataLevel(input.DataLevel)
	if err != nil {
		return domain.StewardConversation{}, err
	}
	now := time.Now().UTC()
	record := domain.StewardConversation{
		ID:        uuid.NewString(),
		Title:     defaultString(truncateAdvisorText(input.Title, 120), "新对话"),
		Status:    StatusActive,
		DataLevel: level,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if !ownerModeEnabled() && dataLevelRank(level) >= dataLevelRank(DataD4) {
		record.Title = level + " 加密对话"
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_conversations (id, title, status, data_level, created_at, updated_at)
		values ($1,$2,$3,$4,$5,$5)
	`, record.ID, record.Title, record.Status, record.DataLevel, now); err != nil {
		return domain.StewardConversation{}, fmt.Errorf("create steward conversation: %w", err)
	}
	return record, nil
}

func (s *Service) ListConversations(ctx context.Context, limit int) ([]domain.StewardConversation, error) {
	return s.listConversations(ctx, limit, false)
}

func (s *Service) ListArchivedConversations(ctx context.Context, limit int) ([]domain.StewardConversation, error) {
	return s.listConversations(ctx, limit, true)
}

func (s *Service) listConversations(ctx context.Context, limit int, archived bool) ([]domain.StewardConversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.Pool.Query(ctx, `
		select c.id, c.title, c.status, c.data_level, count(m.id), max(m.created_at), c.archived_at, c.created_at, c.updated_at
		from steward_conversations c
		left join steward_conversation_messages m on m.conversation_id = c.id and m.role <> 'system'
		where c.status <> 'deleted'
		  and (($2 and c.archived_at is not null) or (not $2 and c.archived_at is null))
		group by c.id
		order by case when $2 then c.archived_at else coalesce(max(m.created_at), c.updated_at) end desc
		limit $1
	`, limit, archived)
	if err != nil {
		return nil, fmt.Errorf("list steward conversations: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardConversation{}
	for rows.Next() {
		var item domain.StewardConversation
		if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.DataLevel, &item.MessageCount, &item.LastMessageAt, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpdateConversation(ctx context.Context, id string, input UpdateConversationInput) (domain.StewardConversation, error) {
	now := time.Now().UTC()
	command, err := s.db.Pool.Exec(ctx, `
		update steward_conversations
		set archived_at = case when $2 then coalesce(archived_at, $3) else null end,
		    updated_at = $3
		where id = $1 and status <> 'deleted'
	`, id, input.Archived, now)
	if err != nil {
		return domain.StewardConversation{}, fmt.Errorf("update steward conversation: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.StewardConversation{}, fmt.Errorf("update steward conversation: %w", pgx.ErrNoRows)
	}
	return s.getConversation(ctx, id)
}

func (s *Service) ListConversationMessages(ctx context.Context, conversationID string, limit int) ([]domain.StewardConversationMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id, conversation_id, role, content, data_level, model, context_summary, payload_encrypted, encrypted_payload, created_at
		from (
			select id, conversation_id, role, content, data_level, model, context_summary, payload_encrypted, encrypted_payload, created_at
			from steward_conversation_messages
			where conversation_id = $1 and role <> 'system'
			order by created_at desc
			limit $2
		) recent
		order by created_at
	`, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list steward conversation messages: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardConversationMessage{}
	for rows.Next() {
		var item domain.StewardConversationMessage
		var encryptedPayload map[string]any
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.Role, &item.Content, &item.DataLevel, &item.Model, &item.ContextSummary, &item.PayloadEncrypted, &encryptedPayload, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := decryptConversationMessage(&item, encryptedPayload); err != nil {
			return nil, err
		}
		item.Suggestions, err = s.listConversationSuggestions(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Executions, err = s.listConversationExecutions(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		item.Episodes, err = s.listAgentEpisodesForMessage(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) SendConversationMessage(ctx context.Context, conversationID string, input SendConversationMessageInput) (SendConversationMessageResult, error) {
	content := truncateAdvisorText(input.Content, 8000)
	if content == "" {
		return SendConversationMessageResult{}, fmt.Errorf("conversation message content is required")
	}
	conversation, err := s.getConversation(ctx, conversationID)
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	if conversation.ArchivedAt != nil {
		return SendConversationMessageResult{}, fmt.Errorf("conversation is archived; restore it before sending messages")
	}
	// The conversation level describes data already stored in the thread; it is
	// not the provenance of a new message typed by the user. Inheriting it here
	// made ordinary questions in the proactive D2 thread fail policy checks
	// before the configured model was called. A manually submitted message is
	// D0 unless the caller explicitly labels it, and the content classifier below
	// can still promote credentials or other sensitive input to a stricter level.
	level, err := conversationDataLevel(defaultString(input.DataLevel, DataD0))
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	level, _ = ClassifyObservationDataLevel(CreateObservationInput{DataLevel: level, Summary: content})
	history, err := s.conversationAdvisorHistory(ctx, conversationID, 12)
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	userMessage, err := s.insertConversationMessage(ctx, conversationID, conversationRoleUser, content, level, "", "")
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	if command := conversationControlCommand(content); command != "" {
		executionMessage, executionErr := s.applyConversationExecutionCommand(ctx, conversation, userMessage, command, level)
		if executionErr != nil {
			return SendConversationMessageResult{}, executionErr
		}
		if (conversation.MessageCount == 0 || conversation.Title == "新对话") && dataLevelRank(level) < dataLevelRank(DataD4) {
			_, _ = s.db.Pool.Exec(ctx, `update steward_conversations set title = $1, updated_at = $2 where id = $3`, truncateAdvisorText(content, 48), time.Now().UTC(), conversationID)
		}
		conversation, err = s.getConversation(ctx, conversationID)
		if err != nil {
			return SendConversationMessageResult{}, err
		}
		return SendConversationMessageResult{Conversation: conversation, Message: executionMessage}, nil
	}
	if episode, resumed, resumeErr := s.resumeAwaitingAgentEpisode(ctx, conversationID, content); resumeErr != nil {
		return SendConversationMessageResult{}, resumeErr
	} else if resumed {
		assistant, insertErr := s.insertConversationMessage(ctx, conversationID, conversationRoleAssistant, "收到补充，我会从刚才的位置继续。", level, "agent-loop-r4.9", "agent-resumed:"+episode.ID)
		if insertErr != nil {
			return SendConversationMessageResult{}, insertErr
		}
		_, _ = s.db.Pool.Exec(ctx, `update steward_agent_episodes set progress_message_id=$2,updated_at=$3 where id=$1`, episode.ID, assistant.ID, time.Now().UTC())
		conversation, err = s.finishConversationTurn(ctx, conversation, content, level)
		return SendConversationMessageResult{Conversation: conversation, Message: assistant}, err
	}
	contextLimit := input.ContextLimit
	if contextLimit <= 0 || contextLimit > 20 {
		contextLimit = 10
	}
	localContext, err := s.conversationContext(ctx, content, level, contextLimit)
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	response := localConversationFallback(content)
	model := "local-fallback"
	dataPolicy, policyErr := s.ResolveDataPolicy(ctx, level, "conversation")
	// Model inference itself is an A3 governed disclosure. Any tool call the
	// model requests is evaluated separately against the tool's real A-level.
	permissionPolicy, permissionErr := s.ResolvePermissionPolicy(ctx, PermissionA3, "model:conversation")
	modelAllowed := policyErr == nil && permissionErr == nil && dataPolicyAllowsManualModel(dataPolicy) && permissionPolicy.ExecutionMode != PolicyModeDeny
	if s.autonomyAdvisor().Status().Enabled && modelAllowed {
		modelContent := conversationModelText(content, level, dataPolicy.ModelContentMode)
		modelHistory := conversationModelHistory(history, dataPolicy.ModelContentMode)
		devices := s.conversationAdvisorDevices(ctx)
		modelTools, toolCatalog, catalogErr := s.agentToolContext(ctx, nil)
		if catalogErr != nil {
			return SendConversationMessageResult{}, catalogErr
		}
		var advisorErr error
		if advisor, ok := s.autonomyAdvisor().(AgentTurnAdvisor); ok {
			decision, turnErr := nextValidAgentTurn(ctx, advisor, AgentTurnInput{
				Message: modelContent, DataLevel: level, TriggerKind: "conversation", History: modelHistory, Context: localContext,
				Tools: modelTools, ToolCatalog: toolCatalog, Devices: devices, KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(), Round: 1,
			})
			advisorErr = turnErr
			if turnErr == nil {
				model = defaultString(s.autonomyAdvisor().Status().Model, s.autonomyAdvisor().Status().Provider)
				if len(decision.ToolCalls) > 0 {
					executionMessage, _, executionErr := s.startAgentEpisode(ctx, conversation, userMessage, content, level, "conversation", decision)
					if executionErr != nil {
						return SendConversationMessageResult{}, executionErr
					}
					conversation, err = s.finishConversationTurn(ctx, conversation, content, level)
					if err != nil {
						return SendConversationMessageResult{}, err
					}
					return SendConversationMessageResult{Conversation: conversation, Message: executionMessage}, nil
				}
				response = ConversationAdvisorResponse{Intent: "answer", Reply: decision.Content, Confidence: 1}
			}
		} else if advisor, ok := s.autonomyAdvisor().(ConversationAdvisor); ok {
			response, advisorErr = advisor.Converse(ctx, ConversationAdvisorInput{
				Message: modelContent, DataLevel: level, History: modelHistory, Context: localContext,
				Tools: modelTools, Devices: devices, KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(),
			})
		} else {
			advisorErr = fmt.Errorf("configured model does not support conversation")
		}
		if advisorErr == nil {
			model = defaultString(s.autonomyAdvisor().Status().Model, s.autonomyAdvisor().Status().Provider)
			s.recordConversationAdvisorDisclosure(ctx, userMessage.ID, level, dataPolicy.ModelContentMode, modelContent, model)
			if response.Intent == "execution" {
				if response.ExecutionPlan == nil {
					response.Intent = "clarify"
					response.Clarification = defaultString(response.Clarification, "我已经理解这是一个执行请求，但还缺少可验证的执行步骤。请补充你期望的最终结果。")
					response.Reply = response.Clarification
				} else {
					executionMessage, _, executionErr := s.createConversationExecutionFromModel(ctx, conversation, userMessage, content, level, response.TargetDevice, *response.ExecutionPlan)
					if executionErr != nil {
						return SendConversationMessageResult{}, executionErr
					}
					conversation, err = s.finishConversationTurn(ctx, conversation, content, level)
					if err != nil {
						return SendConversationMessageResult{}, err
					}
					return SendConversationMessageResult{Conversation: conversation, Message: executionMessage}, nil
				}
			}
		} else {
			s.recordConversationAdvisorFailure(ctx, userMessage.ID, level, advisorErr)
			response.Reply = conversationAdvisorFailureReply(advisorErr)
		}
	} else if !modelAllowed {
		cause := ErrDataPolicyDenied
		if policyErr != nil {
			cause = policyErr
		} else if permissionErr != nil {
			cause = permissionErr
		}
		s.recordConversationAdvisorFailure(ctx, userMessage.ID, level, cause)
		response.Reply = conversationAdvisorFailureReply(cause)
	}
	if response.Intent == "clarify" {
		response.Reply = defaultString(response.Clarification, response.Reply)
	}
	contextSummary := conversationContextSummary(localContext)
	assistantMessage, err := s.insertConversationMessage(ctx, conversationID, conversationRoleAssistant, response.Reply, level, model, contextSummary)
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	conversation, err = s.finishConversationTurn(ctx, conversation, content, level)
	if err != nil {
		return SendConversationMessageResult{}, err
	}
	return SendConversationMessageResult{Conversation: conversation, Message: assistantMessage}, nil
}

func (s *Service) conversationAdvisorDevices(ctx context.Context) []ConversationAdvisorDevice {
	items := []ConversationAdvisorDevice{{ID: s.agentIDValue(), Name: "本机", Platform: "local", TrustStatus: "trusted", PermissionLevel: PermissionA9, Online: true}}
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return items
	}
	seen := map[string]bool{s.agentIDValue(): true}
	for _, device := range devices {
		if seen[device.ID] || device.RevokedAt != nil {
			continue
		}
		seen[device.ID] = true
		online := device.LastSeenAt != nil && time.Since(*device.LastSeenAt) < 2*time.Minute
		items = append(items, ConversationAdvisorDevice{ID: device.ID, Name: device.DeviceName, Platform: device.Platform, TrustStatus: device.TrustStatus, PermissionLevel: device.PermissionLevel, Online: online})
	}
	return items
}

func (s *Service) finishConversationTurn(ctx context.Context, conversation domain.StewardConversation, content, level string) (domain.StewardConversation, error) {
	if (conversation.MessageCount == 0 || conversation.Title == "新对话") && dataLevelRank(level) < dataLevelRank(DataD4) {
		_, _ = s.db.Pool.Exec(ctx, `update steward_conversations set title = $1, updated_at = $2 where id = $3`, truncateAdvisorText(content, 48), time.Now().UTC(), conversation.ID)
	}
	return s.getConversation(ctx, conversation.ID)
}

func (s *Service) DecideConversationSuggestion(ctx context.Context, id string, input DecideConversationSuggestionInput) (domain.StewardConversationSuggestion, error) {
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	if decision != StatusAccepted && decision != StatusDismissed {
		return domain.StewardConversationSuggestion{}, fmt.Errorf("decision must be accepted or dismissed")
	}
	item, err := s.getConversationSuggestion(ctx, id)
	if err != nil {
		return domain.StewardConversationSuggestion{}, err
	}
	if item.Status != StatusCandidate {
		return item, nil
	}
	var targetID *string
	if decision == StatusAccepted {
		switch item.Kind {
		case "memory":
			target, createErr := s.CreateMemory(ctx, CreateMemoryInput{Type: "conversation_fact", Title: item.Title, Summary: item.Summary, Content: defaultString(item.Content, item.Summary), Scope: "global", Source: "conversation", DataLevel: item.DataLevel, PermissionLevel: PermissionA3, Confidence: 0.8, UserConfirmed: boolPointer(true)})
			if createErr != nil {
				return domain.StewardConversationSuggestion{}, createErr
			}
			targetID = &target.ID
		case "task":
			target, createErr := s.CreateTask(ctx, CreateTaskInput{Type: "conversation", Title: item.Title, Description: defaultString(item.Content, item.Summary), Priority: "normal", Source: "conversation", DataLevel: item.DataLevel, PermissionLevel: PermissionA3, RiskLevel: "low", UserConfirmed: boolPointer(true)})
			if createErr != nil {
				return domain.StewardConversationSuggestion{}, createErr
			}
			targetID = &target.ID
		case "intent":
			target, createErr := s.CreateIntent(ctx, CreateIntentInput{Type: "conversation", Title: item.Title, Summary: item.Summary, Reason: "captured from conversation", SuggestedAction: item.SuggestedAction, RiskLevel: "low", Source: "conversation", DataLevel: item.DataLevel, PermissionLevel: PermissionA3, Confidence: 0.8, UserConfirmed: boolPointer(true)})
			if createErr != nil {
				return domain.StewardConversationSuggestion{}, createErr
			}
			targetID = &target.ID
		default:
			return domain.StewardConversationSuggestion{}, fmt.Errorf("unsupported conversation suggestion kind %q", item.Kind)
		}
	}
	now := time.Now().UTC()
	if _, err := s.db.Pool.Exec(ctx, `update steward_conversation_suggestions set status = $1, target_id = $2, updated_at = $3 where id = $4`, decision, targetID, now, id); err != nil {
		return domain.StewardConversationSuggestion{}, fmt.Errorf("decide steward conversation suggestion: %w", err)
	}
	userConfirmed := true
	syncable := false
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "user", Action: "conversation.suggestion." + decision, TargetType: "conversation_suggestion", TargetID: &id, Source: "conversation", PermissionLevel: PermissionA3, DataLevel: item.DataLevel, InputSummary: item.Kind + ":" + item.Title, OutputSummary: decision, UserConfirmed: &userConfirmed, Syncable: &syncable, ResultStatus: ResultOK})
	return s.getConversationSuggestion(ctx, id)
}

func (s *Service) getConversation(ctx context.Context, id string) (domain.StewardConversation, error) {
	var item domain.StewardConversation
	err := s.db.Pool.QueryRow(ctx, `
		select c.id, c.title, c.status, c.data_level, count(m.id), max(m.created_at), c.archived_at, c.created_at, c.updated_at
		from steward_conversations c
		left join steward_conversation_messages m on m.conversation_id = c.id and m.role <> 'system'
		where c.id = $1 and c.status <> 'deleted'
		group by c.id
	`, id).Scan(&item.ID, &item.Title, &item.Status, &item.DataLevel, &item.MessageCount, &item.LastMessageAt, &item.ArchivedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.StewardConversation{}, fmt.Errorf("get steward conversation: %w", err)
	}
	return item, nil
}

func (s *Service) insertConversationMessage(ctx context.Context, conversationID, role, content, level, model, contextSummary string) (domain.StewardConversationMessage, error) {
	now := time.Now().UTC()
	item := domain.StewardConversationMessage{ID: uuid.NewString(), ConversationID: conversationID, Role: role, Content: content, DataLevel: level, Model: model, ContextSummary: contextSummary, Suggestions: []domain.StewardConversationSuggestion{}, Executions: []domain.StewardConversationExecution{}, CreatedAt: now}
	userConfirmed := role == conversationRoleUser
	syncable := false
	auditID, err := s.recordAudit(ctx, AuditInput{Actor: role, Action: "conversation.message." + role, TargetType: "conversation", TargetID: &conversationID, Source: "conversation", PermissionLevel: PermissionA3, DataLevel: level, InputSummary: fmt.Sprintf("%s message, %d characters", role, len([]rune(content))), OutputSummary: "conversation message stored locally", UserConfirmed: &userConfirmed, Syncable: &syncable, ResultStatus: ResultOK})
	if err != nil {
		return domain.StewardConversationMessage{}, err
	}
	storedContent, storedContext := item.Content, item.ContextSummary
	encryptedPayload := map[string]any{}
	if !ownerModeEnabled() && dataLevelRank(level) >= dataLevelRank(DataD4) {
		keyring, encryptionErr := localPayloadKeyringFromEnv()
		if encryptionErr != nil {
			return domain.StewardConversationMessage{}, encryptionErr
		}
		encryptedPayload, encryptionErr = encryptPayloadEnvelope(keyring,
			conversationMessageEncryptionAAD(item.ConversationID, item.ID, item.Role),
			map[string]any{"content": item.Content, "context_summary": item.ContextSummary}, SyncEncryptionScopeLocalAtRest)
		if encryptionErr != nil {
			return domain.StewardConversationMessage{}, encryptionErr
		}
		storedContent, storedContext = "[encrypted "+level+" message]", ""
		item.PayloadEncrypted = true
	}
	if _, err := s.db.Pool.Exec(ctx, `
		insert into steward_conversation_messages (
			id,conversation_id,role,content,data_level,model,context_summary,audit_id,
			payload_encrypted,encrypted_payload,created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, item.ID, item.ConversationID, item.Role, storedContent, item.DataLevel, item.Model, storedContext, auditID, item.PayloadEncrypted, encryptedPayload, now); err != nil {
		return domain.StewardConversationMessage{}, fmt.Errorf("insert steward conversation message: %w", err)
	}
	_, _ = s.db.Pool.Exec(ctx, `update steward_conversations set updated_at = $1 where id = $2`, now, conversationID)
	return item, nil
}

func (s *Service) conversationAdvisorHistory(ctx context.Context, conversationID string, limit int) ([]ConversationAdvisorMessage, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, role, content, data_level, payload_encrypted, encrypted_payload from (
			select id, role, content, data_level, payload_encrypted, encrypted_payload, created_at from steward_conversation_messages where conversation_id = $1 order by created_at desc limit $2
		) recent order by created_at
	`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ConversationAdvisorMessage{}
	for rows.Next() {
		var item ConversationAdvisorMessage
		var id, level string
		var encrypted bool
		var encryptedPayload map[string]any
		if err := rows.Scan(&id, &item.Role, &item.Content, &level, &encrypted, &encryptedPayload); err != nil {
			return nil, err
		}
		message := domain.StewardConversationMessage{ID: id, ConversationID: conversationID, Role: item.Role, Content: item.Content, DataLevel: level, PayloadEncrypted: encrypted}
		if err := decryptConversationMessage(&message, encryptedPayload); err != nil {
			return nil, err
		}
		item.Content = message.Content
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) conversationContext(ctx context.Context, query, maxLevel string, limit int) ([]domain.StewardSearchResult, error) {
	results, err := s.Search(ctx, SearchInput{Query: query, Limit: limit * 2})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		results, err = s.Search(ctx, SearchInput{Limit: limit * 2})
		if err != nil {
			return nil, err
		}
	}
	filtered := make([]domain.StewardSearchResult, 0, limit)
	for _, item := range results {
		policy, policyErr := s.ResolveDataPolicy(ctx, item.DataLevel, item.Source)
		levelAllowed := ownerModeEnabled() || dataLevelRank(item.DataLevel) <= dataLevelRank(maxLevel)
		if levelAllowed && policyErr == nil && dataPolicyAllowsManualModel(policy) {
			filtered = append(filtered, conversationSearchResultForModel(item, policy.ModelContentMode))
			if len(filtered) >= limit {
				break
			}
		}
	}
	return filtered, nil
}

func conversationModelText(content, level, mode string) string {
	switch mode {
	case ModelContentMetadata:
		return fmt.Sprintf("用户提交了一条 %s 对话消息，长度 %d 字符。", level, len([]rune(content)))
	case ModelContentSummary:
		return truncateAdvisorText(redactCredentialText(content), 600)
	case ModelContentRedacted:
		return redactCredentialText(content)
	default:
		return content
	}
}

func conversationModelHistory(history []ConversationAdvisorMessage, mode string) []ConversationAdvisorMessage {
	if mode == ModelContentMetadata {
		return []ConversationAdvisorMessage{}
	}
	result := make([]ConversationAdvisorMessage, 0, len(history))
	for _, item := range history {
		if mode == ModelContentSummary {
			item.Content = truncateAdvisorText(redactCredentialText(item.Content), 300)
		} else if mode == ModelContentRedacted {
			item.Content = redactCredentialText(item.Content)
		}
		result = append(result, item)
	}
	return result
}

func conversationSearchResultForModel(item domain.StewardSearchResult, mode string) domain.StewardSearchResult {
	switch mode {
	case ModelContentMetadata:
		item.Title = item.EntityType
		item.Summary = ""
	case ModelContentSummary:
		item.Title = truncateAdvisorText(redactCredentialText(item.Title), 80)
		item.Summary = truncateAdvisorText(redactCredentialText(item.Summary), 300)
	case ModelContentRedacted:
		item.Title = redactCredentialText(item.Title)
		item.Summary = redactCredentialText(item.Summary)
	}
	return item
}

func conversationMessageEncryptionAAD(conversationID, messageID, role string) string {
	return strings.Join([]string{"steward-conversation-message", conversationID, messageID, role}, ":")
}

func conversationSuggestionEncryptionAAD(messageID, suggestionID, kind string) string {
	return strings.Join([]string{"steward-conversation-suggestion", messageID, suggestionID, kind}, ":")
}

func decryptConversationMessage(item *domain.StewardConversationMessage, encryptedPayload map[string]any) error {
	if item == nil || !item.PayloadEncrypted {
		return nil
	}
	keyring, err := localPayloadKeyringFromEnv()
	if err != nil {
		return err
	}
	payload, err := decryptPayloadEnvelope(keyring,
		conversationMessageEncryptionAAD(item.ConversationID, item.ID, item.Role), encryptedPayload, "conversation message")
	if err != nil {
		return err
	}
	item.Content = stringPayload(payload, "content", "")
	item.ContextSummary = stringPayload(payload, "context_summary", "")
	return nil
}

func decryptConversationSuggestion(item *domain.StewardConversationSuggestion, encryptedPayload map[string]any) error {
	if item == nil || !item.PayloadEncrypted {
		return nil
	}
	keyring, err := localPayloadKeyringFromEnv()
	if err != nil {
		return err
	}
	payload, err := decryptPayloadEnvelope(keyring,
		conversationSuggestionEncryptionAAD(item.MessageID, item.ID, item.Kind), encryptedPayload, "conversation suggestion")
	if err != nil {
		return err
	}
	item.Title = stringPayload(payload, "title", "")
	item.Summary = stringPayload(payload, "summary", "")
	item.Content = stringPayload(payload, "content", "")
	item.SuggestedAction = stringPayload(payload, "suggested_action", "")
	return nil
}

func conversationContextSummary(items []domain.StewardSearchResult) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.EntityType+":"+truncateAdvisorText(item.Title, 40))
	}
	return strings.Join(parts, ", ")
}

func (s *Service) listConversationSuggestions(ctx context.Context, messageID string) ([]domain.StewardConversationSuggestion, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id, message_id, kind, title, summary, content, suggested_action, data_level, permission_level, risk_level, status, target_id, payload_encrypted, encrypted_payload, created_at, updated_at
		from steward_conversation_suggestions where message_id = $1 order by created_at
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardConversationSuggestion{}
	for rows.Next() {
		var item domain.StewardConversationSuggestion
		var encryptedPayload map[string]any
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Kind, &item.Title, &item.Summary, &item.Content, &item.SuggestedAction, &item.DataLevel, &item.PermissionLevel, &item.RiskLevel, &item.Status, &item.TargetID, &item.PayloadEncrypted, &encryptedPayload, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := decryptConversationSuggestion(&item, encryptedPayload); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getConversationSuggestion(ctx context.Context, id string) (domain.StewardConversationSuggestion, error) {
	var item domain.StewardConversationSuggestion
	row := s.db.Pool.QueryRow(ctx, `
		select id, message_id, kind, title, summary, content, suggested_action, data_level, permission_level, risk_level, status, target_id, payload_encrypted, encrypted_payload, created_at, updated_at
		from steward_conversation_suggestions where id = $1
	`, id)
	var encryptedPayload map[string]any
	err := row.Scan(&item.ID, &item.MessageID, &item.Kind, &item.Title, &item.Summary, &item.Content, &item.SuggestedAction, &item.DataLevel, &item.PermissionLevel, &item.RiskLevel, &item.Status, &item.TargetID, &item.PayloadEncrypted, &encryptedPayload, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardConversationSuggestion{}, fmt.Errorf("conversation suggestion not found")
	}
	if err != nil {
		return domain.StewardConversationSuggestion{}, err
	}
	if err := decryptConversationSuggestion(&item, encryptedPayload); err != nil {
		return domain.StewardConversationSuggestion{}, err
	}
	return item, nil
}

func localConversationFallback(content string) ConversationAdvisorResponse {
	return ConversationAdvisorResponse{Reply: "模型请求未完成：模型未启用\n原因：当前没有可用的模型配置。\n处理建议：\n- 打开“模型”配置\n- 填写接口地址、模型名称和 API Key\n- 保存后运行完整协议检查\n本次消息已保存在本地，且没有执行任何工具。\n错误代码：MODEL_DISABLED"}
}

func conversationDataLevel(value string) (string, error) {
	level := strings.ToUpper(strings.TrimSpace(defaultString(value, DataD0)))
	if !validDataLevel(level) {
		return "", fmt.Errorf("conversation data level must be D0-D6")
	}
	return level, nil
}

func boolPointer(value bool) *bool {
	return &value
}

func (s *Service) recordConversationAdvisorFailure(ctx context.Context, messageID, level string, cause error) {
	if cause == nil {
		return
	}
	userConfirmed := false
	syncable := false
	errorSummary := sanitizeAdvisorStatusError(cause)
	result := ResultFailed
	if errors.Is(cause, ErrAdvisorDataLevelDenied) {
		result = ResultBlocked
	}
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "system", Action: "conversation.advisor.fallback", TargetType: "conversation_message", TargetID: &messageID, Source: "conversation", PermissionLevel: PermissionA3, DataLevel: level, InputSummary: "conversation advisor request", OutputSummary: "local fallback response used", UserConfirmed: &userConfirmed, Syncable: &syncable, ResultStatus: result, ErrorSummary: &errorSummary})
}

func (s *Service) recordConversationAdvisorDisclosure(ctx context.Context, messageID, level, contentMode, content, model string) {
	userConfirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "user", Action: "model.dispatch.conversation", TargetType: "conversation_message", TargetID: &messageID,
		Source: "conversation", PermissionLevel: PermissionA6, DataLevel: level,
		InputSummary:  fmt.Sprintf("content_mode=%s characters=%d model=%s", contentMode, len([]rune(content)), model),
		OutputSummary: "conversation request sent to configured model",
		UserConfirmed: &userConfirmed, Syncable: &syncable, ResultStatus: ResultOK,
	})
}
