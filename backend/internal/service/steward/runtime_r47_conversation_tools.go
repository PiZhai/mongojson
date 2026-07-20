package steward

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

type runtimeCreateTaskTool struct{ service *Service }

func newRuntimeCreateTaskTool(service *Service) RuntimeTool {
	return runtimeCreateTaskTool{service: service}
}

func (runtimeCreateTaskTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "steward.create_task", Version: "4.7.0",
		Description: "Create a durable local task, one-time reminder, or recurring reminder. due_at must be RFC3339 when supplied; recurrence is a human-readable schedule retained with the task.",
		InputSchema: map[string]any{
			"type": "object", "required": []string{"title"}, "additionalProperties": false,
			"properties": map[string]any{
				"title": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"},
				"due_at": map[string]any{"type": "string"}, "recurrence": map[string]any{"type": "string"},
			},
		},
		OutputSchema: map[string]any{
			"type": "object", "required": []string{"id", "title", "status"},
			"properties": map[string]any{"id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "due_at": map[string]any{"type": "string"}},
		},
		PermissionLevel: PermissionA3, RiskLevel: "low", SideEffect: RuntimeSideEffectWrite,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyKeyed,
		Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 15,
	}
}

func (runtimeCreateTaskTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "title", "description", "due_at", "recurrence"); err != nil {
		return err
	}
	if _, err := runtimeRequiredString(input, "title"); err != nil {
		return err
	}
	if dueAt, _ := input["due_at"].(string); strings.TrimSpace(dueAt) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(dueAt)); err != nil {
			return fmt.Errorf("due_at must be RFC3339: %w", err)
		}
	}
	return nil
}

func (t runtimeCreateTaskTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	title, _ := runtimeRequiredString(input, "title")
	description, _ := input["description"].(string)
	dueValue, _ := input["due_at"].(string)
	recurrence, _ := input["recurrence"].(string)
	var dueAt *time.Time
	if strings.TrimSpace(dueValue) != "" {
		parsed, _ := time.Parse(time.RFC3339, strings.TrimSpace(dueValue))
		dueAt = &parsed
	}
	taskType := "conversation_reminder"
	if strings.TrimSpace(recurrence) != "" {
		taskType = "conversation_recurring"
		description = strings.TrimSpace(description + "\n周期：" + strings.TrimSpace(recurrence))
	}
	authorization, _ := runtimeExecutionAuthorizationFromContext(ctx)
	recordID, err := activityBatchSideEffectRecordID(ctx, t.service)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	confirmed := true
	task, err := t.service.CreateTask(ctx, CreateTaskInput{
		ID: recordID, Type: taskType, Title: title, Description: description, Priority: "normal", DueAt: dueAt, Source: "conversation_tool",
		DataLevel: defaultString(authorization.DataLevel, DataD0), PermissionLevel: PermissionA3, RiskLevel: "low", UserConfirmed: &confirmed,
	})
	if err != nil {
		return RuntimeToolResult{}, err
	}
	output := map[string]any{"id": task.ID, "title": task.Title, "status": task.Status}
	if task.DueAt != nil {
		output["due_at"] = task.DueAt.Format(time.RFC3339)
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "task_created", Summary: "durable steward task created", Payload: output}}}, nil
}

func (runtimeCreateTaskTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if strings.TrimSpace(fmt.Sprint(output["id"])) == "" || strings.TrimSpace(fmt.Sprint(output["status"])) == "" {
		return fmt.Errorf("created task output is incomplete")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

type runtimeSaveMemoryTool struct{ service *Service }

func newRuntimeSaveMemoryTool(service *Service) RuntimeTool {
	return runtimeSaveMemoryTool{service: service}
}

func (runtimeSaveMemoryTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "steward.save_memory", Version: "4.7.0",
		Description: "Persist an explicit user fact, preference, correction, or decision in long-term memory. Use only when the user asks to remember it or clearly states a durable preference or fact.",
		InputSchema: map[string]any{
			"type": "object", "required": []string{"title", "content"}, "additionalProperties": false,
			"properties": map[string]any{
				"title": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string", "enum": []string{"global", "conversation"}},
			},
		},
		OutputSchema: map[string]any{
			"type": "object", "required": []string{"id", "title", "status"},
			"properties": map[string]any{"id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}},
		},
		PermissionLevel: PermissionA3, RiskLevel: "low", SideEffect: RuntimeSideEffectWrite,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyKeyed,
		Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 15,
	}
}

func (runtimeSaveMemoryTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "title", "summary", "content", "scope"); err != nil {
		return err
	}
	if _, err := runtimeRequiredString(input, "title"); err != nil {
		return err
	}
	if _, err := runtimeRequiredString(input, "content"); err != nil {
		return err
	}
	if scope, _ := input["scope"].(string); scope != "" && scope != "global" && scope != "conversation" {
		return fmt.Errorf("scope must be global or conversation")
	}
	return nil
}

func (t runtimeSaveMemoryTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	title, _ := runtimeRequiredString(input, "title")
	content, _ := runtimeRequiredString(input, "content")
	summary, _ := input["summary"].(string)
	scope, _ := input["scope"].(string)
	authorization, _ := runtimeExecutionAuthorizationFromContext(ctx)
	if scope == "conversation" {
		conversationID := strings.TrimPrefix(authorization.RequestedBy, "conversation:")
		if conversationID != authorization.RequestedBy && conversationID != "" {
			scope = "conversation:" + conversationID
		}
	}
	confirmed := true
	recordID, err := activityBatchSideEffectRecordID(ctx, t.service)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	memory, err := t.service.CreateMemory(ctx, CreateMemoryInput{
		ID: recordID, Type: "explicit_conversation_memory", Title: title, Summary: defaultString(strings.TrimSpace(summary), content), Content: content,
		Scope: defaultString(scope, "global"), Source: "conversation_tool", DataLevel: defaultString(authorization.DataLevel, DataD0),
		PermissionLevel: PermissionA3, Confidence: 1, UserConfirmed: &confirmed,
	})
	if err != nil {
		return RuntimeToolResult{}, err
	}
	output := map[string]any{"id": memory.ID, "title": memory.Title, "status": memory.Status}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "memory_saved", Summary: "explicit long-term memory saved", Payload: output}}}, nil
}

func activityBatchSideEffectRecordID(ctx context.Context, service *Service) (string, error) {
	key, err := service.activityBatchToolIdempotencyKey(ctx)
	if err != nil || key == "" {
		return "", err
	}
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String(), nil
}

func (runtimeSaveMemoryTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if strings.TrimSpace(fmt.Sprint(output["id"])) == "" || strings.TrimSpace(fmt.Sprint(output["status"])) == "" {
		return fmt.Errorf("saved memory output is incomplete")
	}
	return runtimeOutputMatchesExpected(output, expected)
}
