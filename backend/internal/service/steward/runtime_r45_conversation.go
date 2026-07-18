package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
)

const (
	conversationExecutionRun           = "run"
	conversationExecutionOrchestration = "orchestration"
	conversationExecutionQuestion      = "question"

	conversationExecutionNeedsInput   = "needs_input"
	conversationExecutionConfirmation = "awaiting_confirmation"
	conversationExecutionQueued       = "queued"
	conversationExecutionRunning      = "running"
	conversationExecutionPaused       = "paused"
)

type DecideConversationExecutionInput struct {
	Decision      string                              `json:"decision"`
	Reason        string                              `json:"reason"`
	ApprovalProof privilegebroker.SignedApprovalProof `json:"approval_proof"`
}

type conversationExecutionTarget struct {
	ID, Name string
	Remote   bool
}

type agentExecutionLink struct {
	EpisodeID      string
	TurnID         string
	RoundIndex     int
	IdempotencyKey string
	StepTargets    map[string]string
}

func conversationControlCommand(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Trim(value, "。.!！?？")))
	switch value {
	case "继续", "继续执行", "确认", "确认执行", "continue", "resume", "proceed":
		return "continue"
	case "暂停", "暂停执行", "先停一下", "pause":
		return "pause"
	case "取消", "取消执行", "停止这个任务", "cancel":
		return "cancel"
	case "换到另一台电脑", "换一台电脑", "在另一台电脑执行", "switch device", "use another device":
		return "switch_device"
	default:
		return ""
	}
}

func (s *Service) createConversationExecution(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, instruction, level, targetOverride string) (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
	target, plannerInstruction, err := s.selectConversationExecutionTarget(ctx, instruction, targetOverride)
	if err != nil {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, err.Error())
	}
	planner := s.runtimePlannerValue()
	plan, err := planner.Plan(ctx, RuntimePlannerInput{Instruction: plannerInstruction, DataLevel: level, Tools: s.runtimeTools.specs()})
	if err != nil {
		question := "我还不能把这句话安全地编译为可执行计划。请明确目标路径、URL、白名单程序或 tool:<name> 能力。"
		if !errors.Is(err, ErrRuntimePlannerUnsupported) {
			question = "计划生成失败：" + sanitizeRuntimeError(err)
		}
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, question)
	}
	return s.createConversationExecutionFromPlan(ctx, conversation, userMessage, instruction, level, target, plan)
}

func (s *Service) createConversationExecutionFromModel(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, instruction, level, targetOverride string, plan RuntimePlanDraft) (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
	target, _, err := s.selectConversationExecutionTarget(ctx, instruction, targetOverride)
	if err != nil {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, err.Error())
	}
	plan.Planner = defaultString(plan.Planner, "conversation-model")
	plan.PlannerVersion = defaultString(plan.PlannerVersion, "4.6.0")
	return s.createConversationExecutionFromPlan(ctx, conversation, userMessage, instruction, level, target, plan)
}

func (s *Service) createConversationExecutionFromPlan(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, instruction, level string, target conversationExecutionTarget, plan RuntimePlanDraft) (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
	return s.createConversationExecutionFromPlanLinked(ctx, conversation, userMessage, instruction, level, target, plan, agentExecutionLink{})
}

func (s *Service) createConversationExecutionFromPlanLinked(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, instruction, level string, target conversationExecutionTarget, plan RuntimePlanDraft, link agentExecutionLink) (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
	plannerStatus := s.runtimePlannerValue().Status()
	if len(plan.Steps) == 0 || len(plan.Steps) > 20 {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, "我理解这是一个执行请求，但模型没有生成完整的可执行步骤。请补充期望结果。")
	}
	if err := s.validatePlannedPrivilegeCapabilities(ctx, plan.Steps); err != nil {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, sanitizeRuntimeError(err))
	}
	permission, risk, capability, err := s.conversationPlanRisk(plan.Steps)
	if err != nil {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level, sanitizeRuntimeError(err))
	}
	if !ownerModeEnabled() && target.Remote && permissionRank(permission) > permissionRank(PermissionA2) && capability == "" {
		return s.createConversationExecutionQuestion(ctx, conversation, userMessage, instruction, level,
			"当前跨设备普通执行最高为 A2；A4–A7 必须使用目标 Broker 已登记的 tool:<name> 能力。")
	}
	requiresConfirmation := capability != "" || (!ownerModeEnabled() && (risk != "low" || permissionRank(permission) > permissionRank(PermissionA3)))
	summary := truncateAdvisorText(defaultString(strings.TrimSpace(plan.Summary), instruction), 1000)
	responseText := "我已生成执行计划。"
	if requiresConfirmation {
		responseText = "计划已就绪，需要你确认后执行。"
	} else {
		responseText = "开始执行，我会在这里更新状态和结果。"
	}
	assistant, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, responseText, level, "execution-router-r4.5", summary)
	if err != nil {
		return assistant, domain.StewardConversationExecution{}, err
	}
	execution := domain.StewardConversationExecution{
		ID: uuid.NewString(), ConversationID: conversation.ID, MessageID: assistant.ID, RequestMessageID: userMessage.ID,
		Instruction: instruction, Summary: summary, Status: conversationExecutionConfirmation,
		TargetDeviceID: target.ID, TargetDeviceName: target.Name, PermissionLevel: permission, RiskLevel: risk,
		RequiresConfirmation: requiresConfirmation, Capability: capability, Evidence: map[string]any{},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		EpisodeID: link.EpisodeID, TurnID: link.TurnID, RoundIndex: link.RoundIndex,
	}
	if plan.ReasoningContent != "" {
		execution.ModelState = map[string]any{"reasoning_content": plan.ReasoningContent}
	}
	if requiresConfirmation {
		execution.ConfirmationReason = conversationConfirmationReason(permission, risk, target.Remote, capability)
	}
	if !target.Remote && len(plan.Steps) == 1 {
		idempotencyKey := defaultString(link.IdempotencyKey, "conversation:"+userMessage.ID)
		run, createErr := s.CreateAgentRun(ctx, CreateAgentRunInput{
			Goal: summary, Mode: "planned", IdempotencyKey: idempotencyKey,
			RequestedBy: "conversation:" + conversation.ID, TargetDevice: target.ID, DataLevel: level,
			PermissionCeiling: permission, AutoStart: false, Steps: plan.Steps,
			Planner:           defaultString(plan.Planner, plannerStatus.Provider),
			PlannerVersion:    defaultString(plan.PlannerVersion, plannerStatus.Version),
			SourceInstruction: instruction, PlanSummary: summary,
		})
		if createErr != nil {
			s.discardUnlinkedConversationMessage(ctx, assistant.ID)
			assistant = domain.StewardConversationMessage{}
			return assistant, execution, createErr
		}
		execution.Kind, execution.RunID, execution.PlanHash = conversationExecutionRun, run.ID, run.PlanHash
		execution.ApprovalSubject = "runtime:" + run.ID
		if capability != "" {
			control, controlErr := s.GetRuntimeExecutionControl(ctx)
			if controlErr != nil {
				return assistant, execution, controlErr
			}
			execution.ControlGeneration = control.Generation
		}
	} else {
		idempotencyKey := defaultString(link.IdempotencyKey, "conversation:"+userMessage.ID)
		orchestration, createErr := s.createConversationOrchestration(ctx, idempotencyKey, summary, level, permission, target, plan.Steps, link.StepTargets)
		if createErr != nil {
			s.discardUnlinkedConversationMessage(ctx, assistant.ID)
			assistant = domain.StewardConversationMessage{}
			return assistant, execution, createErr
		}
		execution.Kind, execution.OrchestrationID, execution.PlanHash = conversationExecutionOrchestration, orchestration.ID, orchestration.PlanHash
		if target.Remote && capability != "" {
			if len(orchestration.Nodes) != 1 {
				return assistant, execution, fmt.Errorf("remote privileged conversation plan must contain exactly one node")
			}
			preview, previewErr := s.PreviewRemotePrivilegeNode(ctx, orchestration.ID, orchestration.Nodes[0].ID)
			if previewErr != nil {
				return assistant, execution, previewErr
			}
			execution.PlanHash = preview.PlanHash
			execution.ApprovalSubject = preview.Subject
			execution.ControlGeneration = preview.ControlGeneration
		}
	}
	if !requiresConfirmation {
		now := time.Now().UTC()
		execution.ConfirmedAt = &now
		execution.Status = conversationExecutionQueued
	}
	if err := s.insertConversationExecution(ctx, execution); err != nil {
		return assistant, execution, err
	}
	if !requiresConfirmation {
		execution, err = s.startConversationExecution(ctx, execution, DecideConversationExecutionInput{Decision: "confirm", Reason: "explicit low-risk conversation instruction"})
		if err != nil {
			_ = s.failConversationExecution(ctx, execution.ID, err)
			return assistant, execution, err
		}
	}
	assistant.Executions = []domain.StewardConversationExecution{execution}
	return assistant, execution, nil
}

func (s *Service) discardUnlinkedConversationMessage(ctx context.Context, messageID string) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return
	}
	_, _ = s.db.Pool.Exec(ctx, `
		delete from steward_conversation_messages message
		where message.id = $1
		  and not exists (
			select 1 from steward_conversation_executions execution
			where execution.message_id = message.id
		  )
	`, messageID)
}

func (s *Service) createConversationExecutionQuestion(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, instruction, level, question string) (domain.StewardConversationMessage, domain.StewardConversationExecution, error) {
	assistant, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, question, level, "execution-router-r4.5", "需要补充执行信息")
	if err != nil {
		return assistant, domain.StewardConversationExecution{}, err
	}
	now := time.Now().UTC()
	execution := domain.StewardConversationExecution{
		ID: uuid.NewString(), ConversationID: conversation.ID, MessageID: assistant.ID, RequestMessageID: userMessage.ID,
		Instruction: instruction, Summary: "需要补充执行信息", Kind: conversationExecutionQuestion,
		Status: conversationExecutionNeedsInput, TargetDeviceID: s.agentIDValue(), TargetDeviceName: "本机",
		PermissionLevel: PermissionA0, RiskLevel: "low", Question: question, Evidence: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.insertConversationExecution(ctx, execution); err != nil {
		return assistant, execution, err
	}
	assistant.Executions = []domain.StewardConversationExecution{execution}
	return assistant, execution, nil
}

func (s *Service) selectConversationExecutionTarget(ctx context.Context, instruction, override string) (conversationExecutionTarget, string, error) {
	devices, err := s.ListDevices(ctx)
	if err != nil {
		return conversationExecutionTarget{}, instruction, err
	}
	local := conversationExecutionTarget{ID: s.agentIDValue(), Name: "本机"}
	peers := make([]domain.StewardDevice, 0)
	for _, device := range devices {
		if device.ID == s.agentIDValue() || device.Role == DeviceRoleLocal {
			local.ID = device.ID
			local.Name = defaultString(device.DeviceName, "本机")
			continue
		}
		if device.TrustStatus == DeviceTrusted && device.SyncEnabled && device.RevokedAt == nil && device.APIBaseURL != "" && device.PublicKey != "" {
			peers = append(peers, device)
		}
	}
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].LastSeenAt == nil {
			return false
		}
		if peers[j].LastSeenAt == nil {
			return true
		}
		return peers[i].LastSeenAt.After(*peers[j].LastSeenAt)
	})
	requested := strings.TrimSpace(override)
	if requested != "" {
		if requested == local.ID || strings.EqualFold(requested, local.Name) || requested == "local" {
			return local, instruction, nil
		}
		for _, device := range peers {
			if requested == device.ID || strings.EqualFold(requested, device.DeviceName) {
				return conversationExecutionTarget{ID: device.ID, Name: device.DeviceName, Remote: true}, stripConversationDevicePhrase(instruction, device), nil
			}
		}
		return conversationExecutionTarget{}, instruction, fmt.Errorf("目标设备 %q 当前不可用或不受信任", requested)
	}
	lower := strings.ToLower(instruction)
	for _, device := range peers {
		if strings.Contains(lower, strings.ToLower(device.ID)) || (device.DeviceName != "" && strings.Contains(lower, strings.ToLower(device.DeviceName))) {
			return conversationExecutionTarget{ID: device.ID, Name: device.DeviceName, Remote: true}, stripConversationDevicePhrase(instruction, device), nil
		}
	}
	if strings.Contains(lower, "另一台电脑") || strings.Contains(lower, "远程设备") || strings.Contains(lower, "remote device") {
		if len(peers) == 0 {
			return conversationExecutionTarget{}, instruction, fmt.Errorf("没有在线且受信任的远程设备")
		}
		return conversationExecutionTarget{ID: peers[0].ID, Name: peers[0].DeviceName, Remote: true}, stripGenericRemotePhrase(instruction), nil
	}
	return local, instruction, nil
}

func stripConversationDevicePhrase(instruction string, device domain.StewardDevice) string {
	result := instruction
	for _, name := range []string{device.DeviceName, device.ID} {
		if name == "" {
			continue
		}
		for _, pattern := range []string{"在" + name + "上", "请在" + name + "上", "在 " + name + " 上", "请在 " + name + " 上", "on " + name} {
			result = strings.ReplaceAll(result, pattern, "")
		}
	}
	return strings.TrimSpace(result)
}

func stripGenericRemotePhrase(instruction string) string {
	result := instruction
	for _, phrase := range []string{"在另一台电脑上", "请在另一台电脑上", "在远程设备上", "请在远程设备上", "on another computer", "on remote device"} {
		result = strings.ReplaceAll(result, phrase, "")
	}
	return strings.TrimSpace(result)
}

func (s *Service) conversationPlanRisk(steps []CreateAgentRunStepInput) (string, string, string, error) {
	permission, risk, capability := PermissionA0, "low", ""
	for _, step := range steps {
		tool, ok := s.runtimeTools.get(step.ToolName)
		if !ok {
			return "", "", "", fmt.Errorf("unknown runtime tool %q", step.ToolName)
		}
		spec := normalizeRuntimeToolSpec(tool.Spec())
		if permissionRank(spec.PermissionLevel) > permissionRank(permission) {
			permission = spec.PermissionLevel
		}
		if conversationRiskRank(spec.RiskLevel) > conversationRiskRank(risk) {
			risk = spec.RiskLevel
		}
		if step.ToolName == "privilege.execute" {
			capability, _ = step.Arguments["capability"].(string)
			capability = strings.ToLower(strings.TrimSpace(capability))
		}
	}
	return permission, risk, capability, nil
}

func conversationRiskRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}

func conversationConfirmationReason(permission, risk string, remote bool, capability string) string {
	parts := []string{permission + " · " + strings.ToLower(risk) + " risk"}
	if remote {
		parts = append(parts, "将在远程设备执行")
	}
	if capability != "" {
		parts = append(parts, "需要独立 Broker 审批 "+capability)
	}
	return strings.Join(parts, "；")
}

type conversationAgentProfile struct {
	ID, Name, Role, Permission string
	Tools                      []string
	MaxRuntimeSeconds          int
	MaxAttempts                int
}

func conversationAgentGroup(tool string) (string, string, string) {
	switch {
	case strings.HasPrefix(tool, "fs."):
		return "r45-file", "文件 Agent", "file"
	case strings.HasPrefix(tool, "web.") || strings.HasPrefix(tool, "browser."):
		return "r45-network", "网络 Agent", "network"
	case strings.HasPrefix(tool, "shell."):
		return "r45-process", "进程 Agent", "process"
	case tool == "privilege.execute":
		return "r45-privilege", "权限 Agent", "privilege"
	default:
		return "r45-general", "通用 Agent", "general"
	}
}

func (s *Service) createConversationOrchestration(ctx context.Context, idempotencyKey, summary, level, permission string, target conversationExecutionTarget, steps []CreateAgentRunStepInput, stepTargets map[string]string) (domain.StewardOrchestration, error) {
	profiles := map[string]*conversationAgentProfile{}
	stepAgents := map[string]string{}
	for _, step := range steps {
		id, name, role := conversationAgentGroup(step.ToolName)
		profile := profiles[id]
		if profile == nil {
			profile = &conversationAgentProfile{ID: id, Name: name, Role: role, Permission: PermissionA0}
			profiles[id] = profile
		}
		if !containsString(profile.Tools, step.ToolName) {
			profile.Tools = append(profile.Tools, step.ToolName)
		}
		tool, _ := s.runtimeTools.get(step.ToolName)
		if tool != nil {
			spec := normalizeRuntimeToolSpec(tool.Spec())
			if permissionRank(spec.PermissionLevel) > permissionRank(profile.Permission) {
				profile.Permission = spec.PermissionLevel
			}
			attempts := step.MaxAttempts
			if attempts <= 0 {
				attempts = 1
			}
			profile.MaxRuntimeSeconds = max(profile.MaxRuntimeSeconds, spec.DefaultTimeoutSec*attempts)
			profile.MaxAttempts = max(profile.MaxAttempts, attempts)
		}
		stepAgents[step.Key] = id
	}
	for _, profile := range profiles {
		sort.Strings(profile.Tools)
		enabled := true
		maxRuntimeSeconds := max(900, profile.MaxRuntimeSeconds)
		maxAttempts := max(20, profile.MaxAttempts)
		if _, err := s.UpsertOrchestrationAgent(ctx, UpsertOrchestrationAgentInput{
			ID: profile.ID, Name: profile.Name, Role: profile.Role,
			Description: "R4.5 对话路由生成的最小权限执行角色", PermissionCeiling: profile.Permission,
			DataLevelCeiling: level, ToolAllowlist: profile.Tools, MaxConcurrency: 2,
			MaxRuntimeSeconds: maxRuntimeSeconds, MaxAttempts: maxAttempts, MaxEvidenceBytes: 262144, Enabled: &enabled,
		}); err != nil {
			return domain.StewardOrchestration{}, err
		}
	}
	nodes := make([]CreateOrchestrationNodeInput, 0, len(steps))
	targetDevice := target.ID
	if !target.Remote {
		targetDevice = "local"
	}
	for _, step := range steps {
		child := step
		child.DependsOn = nil
		stepTarget := targetDevice
		if selected := strings.TrimSpace(stepTargets[step.Key]); selected != "" {
			stepTarget = selected
			if selected == s.agentIDValue() {
				stepTarget = "local"
			}
		}
		nodes = append(nodes, CreateOrchestrationNodeInput{
			Key: step.Key, AgentID: stepAgents[step.Key], Goal: defaultString(step.Title, step.Key),
			TargetDevice: stepTarget, DependsOn: append([]string(nil), step.DependsOn...),
			PermissionCeiling: permission, DataLevel: level, Steps: []CreateAgentRunStepInput{child},
		})
	}
	return s.CreateOrchestration(ctx, CreateOrchestrationInput{
		Goal: summary, IdempotencyKey: idempotencyKey, RequestedBy: "conversation",
		PermissionCeiling: permission, DataLevel: level, FailurePolicy: "fail_fast",
		MaxParallel: 2, MaxChildren: len(nodes), AutoStart: false, Nodes: nodes,
	})
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) insertConversationExecution(ctx context.Context, item domain.StewardConversationExecution) error {
	evidence, _ := json.Marshal(item.Evidence)
	storedModelState := map[string]any{}
	if len(item.ModelState) > 0 {
		keyring, err := localPayloadKeyringFromEnv()
		if err != nil {
			return fmt.Errorf("encrypt conversation model state: %w", err)
		}
		storedModelState, err = encryptPayloadEnvelope(keyring, conversationModelStateAAD(item.ID), item.ModelState, SyncEncryptionScopeLocalAtRest)
		if err != nil {
			return fmt.Errorf("encrypt conversation model state: %w", err)
		}
	}
	modelState, _ := json.Marshal(storedModelState)
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_conversation_executions (
			id, conversation_id, message_id, request_message_id, instruction, summary, kind, status,
			run_id, orchestration_id, target_device_id, target_device_name, permission_level, risk_level,
			plan_hash, requires_confirmation, confirmation_reason, question, capability, approval_subject,
			control_generation, evidence, model_state, failure_summary, created_at, updated_at, confirmed_at, completed_at,
			episode_id, turn_id, round_index
		) values ($1,$2,$3,$4,$5,$6,$7,$8,nullif($9,'')::uuid,nullif($10,'')::uuid,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22::jsonb,$23::jsonb,$24,$25,$26,$27,$28,nullif($29,'')::uuid,nullif($30,'')::uuid,$31)
	`, item.ID, item.ConversationID, item.MessageID, item.RequestMessageID, item.Instruction, item.Summary,
		item.Kind, item.Status, item.RunID, item.OrchestrationID, item.TargetDeviceID, item.TargetDeviceName,
		item.PermissionLevel, item.RiskLevel, item.PlanHash, item.RequiresConfirmation, item.ConfirmationReason,
		item.Question, item.Capability, item.ApprovalSubject, item.ControlGeneration, string(evidence), string(modelState), item.FailureSummary,
		item.CreatedAt, item.UpdatedAt, item.ConfirmedAt, item.CompletedAt, item.EpisodeID, item.TurnID, item.RoundIndex)
	return err
}

func conversationModelStateAAD(executionID string) string {
	return "conversation-execution-model-state:" + executionID
}

func (s *Service) listConversationExecutions(ctx context.Context, messageID string) ([]domain.StewardConversationExecution, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, conversation_id::text, message_id::text, request_message_id::text,
		       instruction, summary, kind, status, coalesce(run_id::text,''), coalesce(orchestration_id::text,''),
		       target_device_id, target_device_name, permission_level, risk_level, plan_hash,
		       requires_confirmation, confirmation_reason, question, capability, approval_subject,
		       control_generation, evidence, model_state, failure_summary, created_at, updated_at, confirmed_at, completed_at,
		       coalesce(episode_id::text,''), coalesce(turn_id::text,''), round_index
		from steward_conversation_executions where message_id=$1 order by created_at
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardConversationExecution{}
	for rows.Next() {
		var item domain.StewardConversationExecution
		var evidence, modelState []byte
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.MessageID, &item.RequestMessageID,
			&item.Instruction, &item.Summary, &item.Kind, &item.Status, &item.RunID, &item.OrchestrationID,
			&item.TargetDeviceID, &item.TargetDeviceName, &item.PermissionLevel, &item.RiskLevel, &item.PlanHash,
			&item.RequiresConfirmation, &item.ConfirmationReason, &item.Question, &item.Capability, &item.ApprovalSubject,
			&item.ControlGeneration, &evidence, &modelState, &item.FailureSummary, &item.CreatedAt, &item.UpdatedAt,
			&item.ConfirmedAt, &item.CompletedAt, &item.EpisodeID, &item.TurnID, &item.RoundIndex); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evidence, &item.Evidence)
		var storedState map[string]any
		_ = json.Unmarshal(modelState, &storedState)
		if len(storedState) > 0 {
			keyring, keyErr := localPayloadKeyringFromEnv()
			if keyErr != nil {
				return nil, fmt.Errorf("decrypt conversation model state: %w", keyErr)
			}
			item.ModelState, keyErr = decryptPayloadEnvelope(keyring, conversationModelStateAAD(item.ID), storedState, "conversation model state")
			if keyErr != nil {
				return nil, keyErr
			}
		}
		item, _ = s.refreshConversationExecution(ctx, item)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) getConversationExecution(ctx context.Context, id string) (domain.StewardConversationExecution, error) {
	var messageID string
	if err := s.db.Pool.QueryRow(ctx, `select message_id::text from steward_conversation_executions where id=$1`, id).Scan(&messageID); err != nil {
		return domain.StewardConversationExecution{}, err
	}
	items, err := s.listConversationExecutions(ctx, messageID)
	if err != nil {
		return domain.StewardConversationExecution{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.StewardConversationExecution{}, pgx.ErrNoRows
}

func (s *Service) latestConversationExecution(ctx context.Context, conversationID string) (domain.StewardConversationExecution, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		select id::text from steward_conversation_executions
		where conversation_id=$1 and status in ('needs_input','awaiting_confirmation','queued','running','paused','blocked')
		order by updated_at desc, created_at desc limit 1
	`, conversationID).Scan(&id)
	if err != nil {
		return domain.StewardConversationExecution{}, err
	}
	return s.getConversationExecution(ctx, id)
}

func (s *Service) refreshConversationExecution(ctx context.Context, item domain.StewardConversationExecution) (domain.StewardConversationExecution, error) {
	if item.Status == conversationExecutionNeedsInput || item.Status == conversationExecutionConfirmation || item.Status == conversationExecutionPaused {
		return item, nil
	}
	previousStatus := item.Status
	status, failure := item.Status, item.FailureSummary
	evidence := map[string]any{}
	toolResults := []ConversationToolResult{}
	var completed *time.Time
	if item.Kind == conversationExecutionRun && item.RunID != "" {
		run, err := s.GetAgentRun(ctx, item.RunID)
		if err != nil {
			return item, err
		}
		status = conversationExecutionStatus(run.Status)
		failure, completed = run.FailureSummary, run.CompletedAt
		evidence = s.conversationRunEvidence(ctx, run.ID)
		toolResults = s.conversationRunToolResults(ctx, run)
	} else if item.Kind == conversationExecutionOrchestration && item.OrchestrationID != "" {
		orchestration, err := s.GetOrchestration(ctx, item.OrchestrationID)
		if err != nil {
			return item, err
		}
		status = conversationExecutionStatus(orchestration.Status)
		failure, completed = orchestration.FailureSummary, orchestration.CompletedAt
		evidence = map[string]any{
			"child_run_count": orchestration.Evidence.ChildRunCount, "artifact_count": orchestration.Evidence.ArtifactCount,
			"redacted_count": orchestration.Evidence.RedactedCount, "data_levels": orchestration.Evidence.DataLevels,
			"manifest_sha256": orchestration.Evidence.ManifestSHA256,
		}
		toolResults = s.conversationOrchestrationToolResults(ctx, orchestration)
	}
	encoded, _ := json.Marshal(evidence)
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		update steward_conversation_executions set status=$2, failure_summary=$3, evidence=$4::jsonb,
		       completed_at=$5, updated_at=$6 where id=$1
	`, item.ID, status, failure, string(encoded), completed, now)
	if err != nil {
		return item, err
	}
	item.Status, item.FailureSummary, item.Evidence, item.CompletedAt, item.UpdatedAt = status, failure, evidence, completed, now
	if status == RuntimeRunSucceeded && previousStatus != RuntimeRunSucceeded && item.EpisodeID == "" {
		_ = s.recordConversationExecutionMemory(ctx, item)
	}
	if runtimeRunTerminal(status) && status != previousStatus {
		if item.EpisodeID != "" {
			_ = s.completeAgentEpisodeExecution(ctx, item, toolResults)
		} else {
			_ = s.recordConversationExecutionResultMessage(ctx, item, toolResults)
		}
	}
	return item, nil
}

func (s *Service) conversationOrchestrationToolResults(ctx context.Context, orchestration domain.StewardOrchestration) []ConversationToolResult {
	results := []ConversationToolResult{}
	for _, node := range orchestration.Nodes {
		if node.RuntimeRunID != "" {
			if run, err := s.GetAgentRun(ctx, node.RuntimeRunID); err == nil {
				results = append(results, s.conversationRunToolResults(ctx, run)...)
			}
			continue
		}
		if node.RemoteDispatch != nil {
			for index, step := range node.Steps {
				result := ConversationToolResult{
					ID: fmt.Sprintf("remote_%s_%d", node.ID, index+1), ToolName: step.ToolName, Arguments: step.Arguments,
					Output: node.RemoteDispatch.ResultPayload, Error: node.RemoteDispatch.LastError,
				}
				results = append(results, result)
			}
		}
	}
	return results
}

func (s *Service) conversationRunToolResults(ctx context.Context, run domain.StewardAgentRun) []ConversationToolResult {
	results := make([]ConversationToolResult, 0, len(run.Steps))
	for index, step := range run.Steps {
		result := ConversationToolResult{
			ID: fmt.Sprintf("call_%d", index+1), ToolName: step.ToolName, Arguments: step.Arguments,
		}
		if count := len(step.Invocations); count > 0 {
			invocation := step.Invocations[count-1]
			result.ID = defaultString(invocation.ID, result.ID)
			result.Output = invocation.Output
			if governance, ok := invocation.Output["_governance"].(map[string]any); ok {
				if evidenceID, _ := governance["evidence_id"].(string); evidenceID != "" {
					if artifact, evidenceErr := s.GetEvidenceArtifact(ctx, run.ID, evidenceID); evidenceErr == nil && artifact.PayloadAvailable && len(artifact.Payload) > 0 {
						result.Output = cloneStringAnyMap(artifact.Payload)
						result.Output["_governance"] = governance
					}
				}
			}
			result.Error = invocation.ErrorSummary
		} else if step.LastError != "" {
			result.Error = step.LastError
		}
		results = append(results, result)
	}
	return results
}

func (s *Service) recordConversationExecutionResultMessage(ctx context.Context, item domain.StewardConversationExecution, results []ConversationToolResult) error {
	marker := "execution-result:" + item.ID
	var exists bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_conversation_messages where conversation_id=$1 and context_summary=$2)`, item.ConversationID, marker).Scan(&exists); err != nil || exists {
		return err
	}
	level := DataD0
	_ = s.db.Pool.QueryRow(ctx, `select data_level from steward_conversation_messages where id=$1`, item.RequestMessageID).Scan(&level)
	text := "执行失败：" + defaultString(item.FailureSummary, "工具未能完成请求。")
	if item.Status == RuntimeRunSucceeded {
		text = "已完成：" + item.Summary
	}
	model := "execution-result-r4.7"
	if advisor, ok := s.autonomyAdvisor().(ConversationToolResultAdvisor); ok && s.autonomyAdvisor().Status().Enabled && len(results) > 0 {
		dataPolicy, policyErr := s.ResolveDataPolicy(ctx, level, "conversation")
		permissionPolicy, permissionErr := s.ResolvePermissionPolicy(ctx, PermissionA3, "model:conversation")
		if policyErr == nil && permissionErr == nil && dataPolicyAllowsManualModel(dataPolicy) && permissionPolicy.ExecutionMode != PolicyModeDeny {
			modelResults := make([]ConversationToolResult, 0, len(results))
			for _, result := range results {
				encoded, _ := json.Marshal(result.Output)
				if dataPolicy.ModelContentMode != ModelContentRaw {
					result.Output = map[string]any{"governed_summary": conversationModelText(string(encoded), level, dataPolicy.ModelContentMode)}
				}
				result.Error = truncateAdvisorText(result.Error, 1000)
				modelResults = append(modelResults, result)
			}
			reasoningContent, _ := item.ModelState["reasoning_content"].(string)
			if conclusion, concludeErr := advisor.ConcludeToolCalls(ctx, ConversationToolResultInput{
				Message: conversationModelText(item.Instruction, level, dataPolicy.ModelContentMode), DataLevel: level,
				ReasoningContent: reasoningContent, Results: modelResults,
			}); concludeErr == nil {
				text = conclusion
				model = defaultString(s.autonomyAdvisor().Status().Model, s.autonomyAdvisor().Status().Provider)
			} else {
				s.recordConversationAdvisorFailure(ctx, item.RequestMessageID, level, fmt.Errorf("tool result conclusion: %w", concludeErr))
			}
		}
	}
	_, err := s.insertConversationMessage(ctx, item.ConversationID, conversationRoleAssistant, text, level, model, marker)
	return err
}

func (s *Service) recordConversationExecutionMemory(ctx context.Context, item domain.StewardConversationExecution) error {
	s.conversationMemoryMu.Lock()
	defer s.conversationMemoryMu.Unlock()
	var exists bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from steward_source_refs where source_type='conversation_execution' and source_id=$1 and target_type='memory')`, item.ID).Scan(&exists); err != nil || exists {
		return err
	}
	level := DataD0
	_ = s.db.Pool.QueryRow(ctx, `select data_level from steward_conversation_messages where id=$1`, item.RequestMessageID).Scan(&level)
	evidence, _ := json.Marshal(item.Evidence)
	title, content := "已完成："+item.Summary, item.Instruction
	if dataLevelRank(level) >= dataLevelRank(DataD4) {
		title = "已完成受保护的对话任务"
		content = "敏感执行原文保留在加密对话中；长期记忆只保存治理后的执行结果。"
	}
	confirmed := true
	memory, err := s.CreateMemory(ctx, CreateMemoryInput{
		Type: "execution_episode", Title: title,
		Summary: fmt.Sprintf("在%s完成对话任务；执行证据摘要：%s", defaultString(item.TargetDeviceName, item.TargetDeviceID), truncateAdvisorText(string(evidence), 1000)),
		Content: content, Scope: "conversation:" + item.ConversationID, Source: "conversation_execution",
		DataLevel: level, PermissionLevel: PermissionA3, Confidence: 1, UserConfirmed: &confirmed,
	})
	if err != nil {
		return err
	}
	_, err = s.CreateSourceRef(ctx, CreateSourceRefInput{TargetType: "memory", TargetID: memory.ID, SourceType: "conversation_execution", SourceID: item.ID, Summary: item.Summary, Confidence: 1})
	return err
}

func conversationExecutionStatus(status string) string {
	switch status {
	case "draft", "planning", "awaiting_approval":
		return conversationExecutionConfirmation
	case "queued":
		return conversationExecutionQueued
	case "running", "verifying", "compensating":
		return conversationExecutionRunning
	case "succeeded", "compensated":
		return RuntimeRunSucceeded
	case "failed", "compensation_failed":
		return RuntimeRunFailed
	case "cancelled":
		return RuntimeRunCancelled
	default:
		return RuntimeRunBlocked
	}
}

func (s *Service) conversationRunEvidence(ctx context.Context, runID string) map[string]any {
	var artifacts, redacted int
	var levels []string
	_ = s.db.Pool.QueryRow(ctx, `
		select count(*)::int, count(*) filter (where redacted)::int,
		       coalesce(array_agg(distinct data_level) filter (where data_level<>''), array[]::text[])
		from steward_evidence_artifacts where run_id=$1
	`, runID).Scan(&artifacts, &redacted, &levels)
	return map[string]any{"artifact_count": artifacts, "redacted_count": redacted, "data_levels": levels}
}

func (s *Service) DecideConversationExecution(ctx context.Context, id string, input DecideConversationExecutionInput) (domain.StewardConversationExecution, error) {
	item, err := s.getConversationExecution(ctx, id)
	if err != nil {
		return item, err
	}
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	if item.EpisodeID != "" && (decision == "pause" || decision == "cancel") {
		_, episodeErr := s.DecideAgentEpisode(ctx, item.EpisodeID, DecideAgentEpisodeInput{Decision: decision})
		if episodeErr != nil {
			return item, episodeErr
		}
		return s.getConversationExecution(ctx, id)
	}
	switch decision {
	case "confirm", "continue":
		return s.startConversationExecution(ctx, item, input)
	case "cancel":
		return s.cancelConversationExecution(ctx, item, false)
	case "pause":
		return s.cancelConversationExecution(ctx, item, true)
	default:
		return item, fmt.Errorf("decision must be confirm, pause, or cancel")
	}
}

func (s *Service) startConversationExecution(ctx context.Context, item domain.StewardConversationExecution, input DecideConversationExecutionInput) (domain.StewardConversationExecution, error) {
	if item.Status != conversationExecutionConfirmation && item.Status != conversationExecutionQueued && item.Status != conversationExecutionPaused && item.Status != RuntimeRunBlocked {
		return item, fmt.Errorf("conversation execution cannot continue from %s", item.Status)
	}
	now := time.Now().UTC()
	if item.Kind == conversationExecutionRun {
		run, err := s.GetAgentRun(ctx, item.RunID)
		if err != nil {
			return item, err
		}
		if item.Status == conversationExecutionPaused && (run.Status == RuntimeRunCancelled || run.Status == RuntimeRunFailed || run.Status == RuntimeRunBlocked) {
			run, err = s.ResumeAgentRun(ctx, run.ID)
			if err != nil {
				return item, err
			}
		}
		if item.Status == conversationExecutionPaused && (run.Status == RuntimeRunRunning || run.Status == RuntimeRunVerifying || run.Status == RuntimeRunCompensating) {
			return item, fmt.Errorf("任务仍在完成协作暂停，请稍后再说“继续”")
		}
		if run.Status == RuntimeRunAwaitingApproval || (run.Status == RuntimeRunDraft && runtimeRunRequiresApproval(run)) {
			reason := defaultString(strings.TrimSpace(input.Reason), "confirmed in conversation")
			run, err = s.ApproveAgentRun(ctx, run.ID, ApproveAgentRunInput{
				PlanHash: run.PlanHash, GrantedBy: "conversation-user", Scope: "run", Reason: reason, ApprovalProof: conversationApprovalProofPointer(input.ApprovalProof),
			})
			if err != nil {
				return item, err
			}
		}
		if run.Status == RuntimeRunDraft || run.Status == RuntimeRunPlanning {
			if _, err := s.StartAgentRun(ctx, run.ID); err != nil {
				return item, err
			}
		}
	} else if item.Kind == conversationExecutionOrchestration {
		orchestration, err := s.GetOrchestration(ctx, item.OrchestrationID)
		if err != nil {
			return item, err
		}
		if item.Capability != "" {
			if len(orchestration.Nodes) != 1 {
				return item, fmt.Errorf("remote privilege execution has invalid node count")
			}
			if _, err := s.ApproveRemotePrivilegeNode(ctx, orchestration.ID, orchestration.Nodes[0].ID, ApproveRemotePrivilegeInput{
				PlanHash: item.PlanHash, ApprovalProof: input.ApprovalProof,
			}); err != nil {
				return item, err
			}
		}
		if orchestration.Status == OrchestrationDraft {
			if _, err := s.StartOrchestration(ctx, orchestration.ID); err != nil {
				return item, err
			}
		} else if item.Status == conversationExecutionPaused {
			return item, fmt.Errorf("paused multi-Agent execution must be replanned before continuing")
		}
	}
	_, err := s.db.Pool.Exec(ctx, `
		update steward_conversation_executions set status='queued', confirmed_at=coalesce(confirmed_at,$2),
		       requires_confirmation=false, updated_at=$2 where id=$1
	`, item.ID, now)
	if err != nil {
		return item, err
	}
	item.Status, item.RequiresConfirmation, item.ConfirmedAt, item.UpdatedAt = conversationExecutionQueued, false, &now, now
	return item, nil
}

func conversationApprovalProofPointer(proof privilegebroker.SignedApprovalProof) *privilegebroker.SignedApprovalProof {
	if strings.TrimSpace(proof.Claims.ProofID) == "" && strings.TrimSpace(proof.Signature) == "" {
		return nil
	}
	return &proof
}

func runtimeRunRequiresApproval(run domain.StewardAgentRun) bool {
	for _, step := range run.Steps {
		if step.RequiresApproval {
			return true
		}
	}
	return false
}

func (s *Service) cancelConversationExecution(ctx context.Context, item domain.StewardConversationExecution, pause bool) (domain.StewardConversationExecution, error) {
	if item.Kind == conversationExecutionRun && item.RunID != "" {
		run, err := s.GetAgentRun(ctx, item.RunID)
		if err == nil && !runtimeRunTerminal(run.Status) {
			if _, err := s.CancelAgentRun(ctx, item.RunID); err != nil {
				return item, err
			}
		}
	} else if item.Kind == conversationExecutionOrchestration && item.OrchestrationID != "" {
		orchestration, err := s.GetOrchestration(ctx, item.OrchestrationID)
		if err == nil && !orchestrationTerminal(orchestration.Status) {
			if _, err := s.CancelOrchestration(ctx, item.OrchestrationID); err != nil {
				return item, err
			}
		}
	}
	status := RuntimeRunCancelled
	if pause {
		status = conversationExecutionPaused
	}
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `update steward_conversation_executions set status=$2::text, updated_at=$3::timestamptz, completed_at=case when $2::text='cancelled' then $3::timestamptz else null::timestamptz end where id=$1`, item.ID, status, now)
	item.Status, item.UpdatedAt = status, now
	if !pause {
		item.CompletedAt = &now
	}
	return item, err
}

func (s *Service) failConversationExecution(ctx context.Context, id string, cause error) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `update steward_conversation_executions set status='failed', failure_summary=$2, updated_at=$3, completed_at=$3 where id=$1`, id, sanitizeRuntimeError(cause), now)
	return err
}

func (s *Service) applyConversationExecutionCommand(ctx context.Context, conversation domain.StewardConversation, userMessage domain.StewardConversationMessage, command, level string) (domain.StewardConversationMessage, error) {
	var episodeID string
	episodeErr := s.db.Pool.QueryRow(ctx, `select id::text from steward_agent_episodes where conversation_id=$1 and status in ('thinking','executing','awaiting_input','paused','blocked') order by updated_at desc limit 1`, conversation.ID).Scan(&episodeID)
	if episodeErr == nil {
		decision := command
		if decision == "continue" {
			decision = "resume"
		}
		targetID := ""
		if decision == "switch_device" {
			episode, getErr := s.GetAgentEpisode(ctx, episodeID)
			if getErr != nil {
				return domain.StewardConversationMessage{}, getErr
			}
			devices := s.conversationAdvisorDevices(ctx)
			for _, device := range devices {
				if device.Online && device.ID != episode.TargetDeviceID && device.ID != s.agentIDValue() {
					targetID = device.ID
					break
				}
			}
			if targetID == "" {
				return s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, "没有另一台在线设备可用。", level, "agent-loop-r4.9", "")
			}
		}
		episode, err := s.DecideAgentEpisode(ctx, episodeID, DecideAgentEpisodeInput{Decision: decision, TargetDeviceID: targetID})
		if err != nil {
			return domain.StewardConversationMessage{}, err
		}
		texts := map[string]string{"resume": "继续处理。", "pause": "已暂停当前任务。", "cancel": "已取消当前任务。", "switch_device": "已切换设备，将从最近完整结果继续。"}
		assistant, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, texts[decision], level, "agent-loop-r4.9", "agent-control:"+episode.ID)
		if err == nil {
			assistant.Episodes = []domain.StewardAgentEpisode{episode}
		}
		return assistant, err
	}
	if episodeErr != nil && !errors.Is(episodeErr, pgx.ErrNoRows) {
		return domain.StewardConversationMessage{}, episodeErr
	}
	item, err := s.latestConversationExecution(ctx, conversation.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, "当前没有可控制的执行任务。", level, "execution-router-r4.5", "")
	}
	if err != nil {
		return domain.StewardConversationMessage{}, err
	}
	text := ""
	switch command {
	case "continue":
		if item.Status == conversationExecutionConfirmation && item.Capability != "" {
			assistant, insertErr := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, "这是高权限任务，请点击确认卡并完成系统身份验证。", level, "execution-router-r4.5", item.Summary)
			if insertErr != nil {
				return assistant, insertErr
			}
			assistant.Executions = []domain.StewardConversationExecution{item}
			return assistant, nil
		}
		if item.Status == conversationExecutionPaused && item.Kind == conversationExecutionOrchestration {
			target := item.TargetDeviceID
			assistant, _, createErr := s.createConversationExecution(ctx, conversation, userMessage, item.Instruction, level, target)
			return assistant, createErr
		}
		item, err = s.startConversationExecution(ctx, item, DecideConversationExecutionInput{Decision: "continue", Reason: "continued in conversation"})
		text = "继续执行。"
	case "pause":
		item, err = s.cancelConversationExecution(ctx, item, true)
		text = "已暂停当前任务。你可以说“继续”或“取消”。"
	case "cancel":
		item, err = s.cancelConversationExecution(ctx, item, false)
		text = "已取消当前任务。"
	case "switch_device":
		if item.Status == conversationExecutionRunning || item.Status == conversationExecutionQueued {
			return s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, "任务已经开始；请先说“暂停”，再切换设备。", level, "execution-router-r4.5", "")
		}
		{
			devices, listErr := s.ListDevices(ctx)
			if listErr != nil {
				return domain.StewardConversationMessage{}, listErr
			}
			other := ""
			for _, device := range devices {
				if device.ID != s.agentIDValue() && device.ID != item.TargetDeviceID && device.TrustStatus == DeviceTrusted && device.SyncEnabled && device.RevokedAt == nil && device.APIBaseURL != "" && device.PublicKey != "" {
					other = device.ID
					break
				}
			}
			if other == "" {
				return s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, "没有另一台在线且受信任的设备可用。", level, "execution-router-r4.5", "")
			}
			_, _ = s.cancelConversationExecution(ctx, item, false)
			assistant, _, createErr := s.createConversationExecution(ctx, conversation, userMessage, item.Instruction, level, other)
			return assistant, createErr
		}
	}
	if err != nil {
		return domain.StewardConversationMessage{}, err
	}
	assistant, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleAssistant, text, level, "execution-router-r4.5", item.Summary)
	if err != nil {
		return assistant, err
	}
	assistant.Executions = []domain.StewardConversationExecution{item}
	return assistant, nil
}

// RunConversationExecutionCycle pre-authorizes child Runtime runs created by
// a confirmed R4.5 orchestration. Owner mode ignores legacy A-level ceilings;
// privilege.execute remains bound to its independent Broker proof path.
func (s *Service) RunConversationExecutionCycle(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.orchestrationR4 {
		return 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Pool.Query(ctx, `
		select distinct run.id::text, run.plan_hash
		from steward_conversation_executions execution
		join steward_orchestration_nodes node on node.orchestration_id=execution.orchestration_id
		join steward_agent_runs run on run.id=node.runtime_run_id
		where execution.status in ('queued','running') and execution.confirmed_at is not null
		  and ($2::boolean or execution.permission_level in ('A0','A1','A2','A3')) and run.status='awaiting_approval'
		limit $1
	`, limit, ownerModeEnabled())
	if err != nil {
		return 0, err
	}
	type candidate struct{ id, hash string }
	items := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.hash); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if _, err := s.ApproveAgentRun(ctx, item.id, ApproveAgentRunInput{
			PlanHash: item.hash, GrantedBy: "conversation-user", Scope: "run", Reason: "confirmed conversation orchestration",
		}); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// RunConversationExecutionRefreshCycle projects terminal Runtime/R4 state back
// into the originating conversation even when no browser is polling messages.
// The first successful transition also persists one evidence-linked episodic
// memory, so long-term continuity does not depend on the UI being open.
func (s *Service) RunConversationExecutionRefreshCycle(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.runtimeR2 {
		return 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Pool.Query(ctx, `
		select id::text from steward_conversation_executions
		where status in ('queued','running')
		order by updated_at, created_at limit $1
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
	if err := rows.Err(); err != nil {
		return 0, err
	}
	refreshed := 0
	for _, id := range ids {
		item, err := s.getConversationExecution(ctx, id)
		if err != nil {
			return refreshed, err
		}
		if _, err := s.refreshConversationExecution(ctx, item); err != nil {
			return refreshed, err
		}
		refreshed++
	}
	return refreshed, nil
}
