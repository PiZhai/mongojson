package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

type runtimeIntelligenceTool struct {
	service *Service
	action  string
}

func newRuntimeIntelligenceTool(service *Service, action string) RuntimeTool {
	return runtimeIntelligenceTool{service: service, action: action}
}

func (t runtimeIntelligenceTool) Spec() domain.StewardToolSpec {
	spec := domain.StewardToolSpec{
		Name: t.action, Version: "5.3.0", RiskLevel: "low", Deterministic: true, SupportsCancel: true,
		PermissionLevel: PermissionA0, SideEffect: RuntimeSideEffectNone, ApprovalMode: RuntimeApprovalNever,
		IdempotencyMode: RuntimeIdempotencyInherent, DefaultTimeoutSec: 20,
	}
	switch t.action {
	case "steward.runtime_status":
		spec.Description = "Inspect the real Agent, model, planner, execution-control and tool-host runtime state."
		spec.InputSchema = objectSchema(map[string]any{}, nil)
		spec.OutputSchema = objectOutputSchema("agent", "planner", "model", "execution_control", "tool_hosts", "checked_at")
	case "steward.collection_status":
		spec.Description = "Inspect whether continuous activity collection is enabled, source freshness, backlog and batch state."
		spec.InputSchema = objectSchema(map[string]any{}, nil)
		spec.OutputSchema = objectOutputSchema("enabled", "mode", "sources", "pending_batches", "processing_batches", "waiting_model", "failed_batches", "updated_at")
	case "steward.activity.query":
		spec.Description = "Query real persisted activity sessions and observations for a time window. Use the returned evidence_refs verbatim when writing reports or profile facts; their source IDs and calendar days are server-derived."
		spec.InputSchema = objectSchema(map[string]any{
			"from": stringSchema(), "to": stringSchema(), "source": stringSchema(), "device_id": stringSchema(),
			"kind":  map[string]any{"type": "string", "enum": []string{"all", "sessions", "observations"}},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
		}, nil)
		spec.OutputSchema = objectOutputSchema("sessions", "observations", "evidence_refs", "window", "counts")
	case "steward.background_jobs.list":
		spec.Description = "List real durable profile/report jobs, activity batches, background Agent episodes and daemon loops."
		spec.InputSchema = objectSchema(map[string]any{
			"status": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		}, nil)
		spec.OutputSchema = objectOutputSchema("intelligence_jobs", "activity_batches", "episodes", "loops", "counts", "checked_at")
	case "steward.reminder.context":
		spec.Description = "Read the current versioned reminder policy, recent user feedback, learned receptivity windows and recent reminder volume before deciding whether and when to notify."
		spec.InputSchema = objectSchema(map[string]any{}, nil)
		spec.OutputSchema = objectOutputSchema("policy", "active_policies", "receptivity_windows", "recent_feedback", "notification_counts", "checked_at")
	case "steward.reminder_policy.get":
		spec.Description = "Resolve the active reminder policy for a category, falling back to the global policy."
		spec.InputSchema = objectSchema(map[string]any{"profile_scope": stringSchema(), "category": stringSchema()}, nil)
		spec.OutputSchema = objectOutputSchema("policy")
	case "steward.reminder_policy.update":
		spec.Description = "Persist a new immutable reminder policy version chosen from current activity, profile and feedback evidence. Policies are soft model guidance, not a fixed business-rule gate."
		spec.InputSchema = objectSchema(map[string]any{
			"profile_scope": stringSchema(), "category": stringSchema(), "policy": map[string]any{"type": "object"},
			"rationale": stringSchema(), "evidence_manifest": map[string]any{"type": "array", "items": stringSchema()},
			"source_episode_id": stringSchema(),
		}, []string{"policy", "rationale"})
		spec.OutputSchema = objectOutputSchema("id", "profile_scope", "category", "version", "policy")
		spec.PermissionLevel, spec.SideEffect, spec.ApprovalMode, spec.IdempotencyMode = PermissionA0, RuntimeSideEffectWrite, RuntimeApprovalNever, RuntimeIdempotencyKeyed
	case "steward.reminder_feedback.query":
		spec.Description = "Query normalized reminder outcomes and learned receptivity windows so the model can adapt timing, frequency, content and channels."
		spec.InputSchema = objectSchema(map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}}, nil)
		spec.OutputSchema = objectOutputSchema("feedback", "receptivity_windows")
	case "steward.profile.get":
		spec.Description = "Read exactly one declared user profile projection: recent, stable, explicit, or merged."
		spec.InputSchema = objectSchema(map[string]any{
			"view": map[string]any{"type": "string", "enum": []string{"recent", "stable", "explicit", "merged"}},
		}, []string{"view"})
		spec.OutputSchema = objectOutputSchema("view", "snapshot")
	case "steward.profile.explain":
		spec.Description = "Read every immutable version and evidence reference for one profile fact key."
		spec.InputSchema = objectSchema(map[string]any{"key": stringSchema()}, []string{"key"})
		spec.OutputSchema = objectOutputSchema("key", "facts")
	case "steward.profile.upsert_fact":
		spec.Description = "Create an immutable profile fact from persisted evidence IDs. Evidence sources and calendar days are verified against the database; stable facts require three distinct verified days unless a persisted user-authored source confirms them. Explicit facts require a persisted user-authored source (the direct user-correction API is the only evidence-free exception). Echo the background job_id when one is provided in the task."
		spec.InputSchema = objectSchema(map[string]any{
			"key": stringSchema(), "value": map[string]any{"type": "object"}, "summary": stringSchema(),
			"horizon":    map[string]any{"type": "string", "enum": []string{"recent", "stable", "explicit"}},
			"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "user_confirmed": map[string]any{"type": "boolean"},
			"job_id": stringSchema(), "evidence": evidenceArraySchema(),
		}, []string{"key", "value", "horizon"})
		spec.OutputSchema = objectOutputSchema("id", "key", "horizon", "version", "evidence_days", "status")
		spec.PermissionLevel, spec.SideEffect, spec.ApprovalMode, spec.IdempotencyMode = PermissionA0, RuntimeSideEffectNone, RuntimeApprovalNever, RuntimeIdempotencyKeyed
	case "steward.report.get":
		spec.Description = "Read a report by id, or list current daily, weekly or monthly reports."
		spec.InputSchema = objectSchema(map[string]any{"id": stringSchema(), "cadence": map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}}, "limit": map[string]any{"type": "integer"}, "include_history": map[string]any{"type": "boolean"}}, nil)
		spec.OutputSchema = objectOutputSchema("reports")
	case "steward.report.write":
		spec.Description = "Persist a versioned daily, weekly, or monthly report from verified persisted evidence. silent controls notification only and never skips report persistence. For a background intelligence task, job_id must exactly echo the job_id in the task; the runtime also recovers it from the current Episode when omitted."
		spec.InputSchema = objectSchema(map[string]any{
			"cadence": map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}}, "period_key": stringSchema(),
			"period_start": stringSchema(), "period_end": stringSchema(), "status": map[string]any{"type": "string", "enum": []string{"complete", "partial"}},
			"title": stringSchema(), "summary": stringSchema(), "body": stringSchema(), "metrics": map[string]any{"type": "object"},
			"silent": map[string]any{"type": "boolean"}, "evidence": evidenceArraySchema(), "episode_id": stringSchema(), "job_id": stringSchema(),
		}, []string{"cadence", "period_start", "period_end", "title", "body"})
		spec.OutputSchema = objectOutputSchema("id", "cadence", "period_key", "revision", "status", "silent", "evidence_count")
		spec.PermissionLevel, spec.SideEffect, spec.ApprovalMode, spec.IdempotencyMode = PermissionA0, RuntimeSideEffectNone, RuntimeApprovalNever, RuntimeIdempotencyKeyed
	}
	return spec
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func objectSchema(properties map[string]any, required []string) map[string]any {
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func objectOutputSchema(required ...string) map[string]any {
	properties := map[string]any{}
	for _, key := range required {
		properties[key] = map[string]any{}
	}
	return map[string]any{"type": "object", "required": required, "properties": properties}
}

func evidenceArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": objectSchema(map[string]any{
		"source_type": map[string]any{"type": "string", "enum": []string{"observation", "activity_session", "activity_batch", "profile_fact", "report", "conversation_message", "memory", "agent_episode", "intelligence_job"}},
		"source_id":   stringSchema(), "summary": stringSchema(),
		"evidence_day": stringSchema(), "content_hash": stringSchema(),
	}, []string{"source_type", "source_id", "evidence_day"})}
}

func (t runtimeIntelligenceTool) Validate(input map[string]any) error {
	switch t.action {
	case "steward.runtime_status", "steward.collection_status", "steward.reminder.context":
		return runtimeRejectUnknownFields(input)
	case "steward.reminder_policy.get":
		return runtimeRejectUnknownFields(input, "profile_scope", "category")
	case "steward.reminder_policy.update":
		if err := runtimeRejectUnknownFields(input, "profile_scope", "category", "policy", "rationale", "evidence_manifest", "source_episode_id"); err != nil {
			return err
		}
		policy, ok := input["policy"].(map[string]any)
		if !ok || len(policy) == 0 {
			return fmt.Errorf("policy must be a non-empty object")
		}
		if err := validateReminderPolicy(policy); err != nil {
			return err
		}
		if _, err := runtimeRequiredString(input, "rationale"); err != nil {
			return err
		}
		_, err := runtimeStringSlice(input, "evidence_manifest")
		return err
	case "steward.reminder_feedback.query":
		if err := runtimeRejectUnknownFields(input, "limit"); err != nil {
			return err
		}
		limit, err := runtimeInt(input, "limit", 100)
		if err != nil || limit < 1 || limit > 500 {
			return fmt.Errorf("limit must be an integer between 1 and 500")
		}
		return nil
	case "steward.activity.query":
		if err := runtimeRejectUnknownFields(input, "from", "to", "source", "device_id", "kind", "limit"); err != nil {
			return err
		}
		from, _ := runtimeOptionalString(input, "from")
		to, _ := runtimeOptionalString(input, "to")
		if _, _, err := parseActivityQueryWindow(from, to); err != nil {
			return err
		}
		kind, _ := runtimeOptionalString(input, "kind")
		if kind != "" && kind != "all" && kind != "sessions" && kind != "observations" {
			return fmt.Errorf("kind must be all, sessions, or observations")
		}
		limit, err := runtimeInt(input, "limit", 100)
		if err != nil || limit < 1 || limit > 500 {
			return fmt.Errorf("limit must be an integer between 1 and 500")
		}
		return nil
	case "steward.background_jobs.list":
		if err := runtimeRejectUnknownFields(input, "status", "limit"); err != nil {
			return err
		}
		limit, err := runtimeInt(input, "limit", 50)
		if err != nil || limit < 1 || limit > 200 {
			return fmt.Errorf("limit must be an integer between 1 and 200")
		}
		return nil
	case "steward.profile.get":
		if err := runtimeRejectUnknownFields(input, "view"); err != nil {
			return err
		}
		projection, err := runtimeRequiredString(input, "view")
		if err != nil {
			return err
		}
		if _, ok := ProfileSnapshotForView(domain.StewardProfileView{}, projection); !ok {
			return fmt.Errorf("view must be recent, stable, explicit, or merged")
		}
		return nil
	case "steward.profile.explain":
		if err := runtimeRejectUnknownFields(input, "key"); err != nil {
			return err
		}
		_, err := runtimeRequiredString(input, "key")
		return err
	case "steward.profile.upsert_fact":
		if err := runtimeRejectUnknownFields(input, "key", "value", "summary", "horizon", "confidence", "user_confirmed", "job_id", "evidence"); err != nil {
			return err
		}
		if _, err := runtimeRequiredString(input, "key"); err != nil {
			return err
		}
		if _, ok := input["value"].(map[string]any); !ok {
			return fmt.Errorf("value must be an object")
		}
		horizon, err := runtimeRequiredString(input, "horizon")
		if err != nil {
			return err
		}
		if !validProfileHorizon(strings.ToLower(horizon)) {
			return fmt.Errorf("horizon must be recent, stable, or explicit")
		}
		evidence, err := parseRuntimeEvidence(input["evidence"])
		if err != nil {
			return err
		}
		if len(evidence) == 0 {
			return ErrProfileEvidenceRequired
		}
		confirmed, err := runtimeBool(input, "user_confirmed", false)
		if err != nil {
			return err
		}
		evidence = normalizeProfileEvidence(evidence, time.Now())
		if strings.EqualFold(horizon, domain.StewardProfileHorizonStable) && !confirmed && distinctEvidenceDays(evidence) < profileStableEvidenceDays {
			return ErrProfileEvidenceInsufficient
		}
		if strings.EqualFold(horizon, domain.StewardProfileHorizonExplicit) && !confirmed {
			return fmt.Errorf("explicit profile facts must be user confirmed")
		}
		return nil
	case "steward.report.get":
		return runtimeRejectUnknownFields(input, "id", "cadence", "limit", "include_history")
	case "steward.report.write":
		if err := runtimeRejectUnknownFields(input, "cadence", "period_key", "period_start", "period_end", "status", "title", "summary", "body", "metrics", "silent", "evidence", "episode_id", "job_id"); err != nil {
			return err
		}
		for _, key := range []string{"cadence", "period_start", "period_end", "title", "body"} {
			if _, err := runtimeRequiredString(input, key); err != nil {
				return err
			}
		}
		for _, key := range []string{"period_start", "period_end"} {
			value, _ := runtimeRequiredString(input, key)
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return fmt.Errorf("%s must be RFC3339: %w", key, err)
			}
		}
		_, err := parseRuntimeEvidence(input["evidence"])
		return err
	default:
		return fmt.Errorf("unsupported intelligence tool %s", t.action)
	}
}

func (t runtimeIntelligenceTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if t.service == nil {
		return RuntimeToolResult{}, fmt.Errorf("steward service is unavailable")
	}
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	switch t.action {
	case "steward.runtime_status":
		output, err := t.service.runtimeIntelligenceStatus(ctx)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("runtime_status", "current Steward runtime state loaded", output), nil
	case "steward.collection_status":
		status, err := t.service.ActivityPipelineStatus(ctx, time.Now())
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("collection_status", "current collection source and batch state loaded", intelligenceStructMap(status)), nil
	case "steward.activity.query":
		output, err := t.service.queryRuntimeActivity(ctx, input)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("activity_query", "persisted activities loaded from the requested window", output), nil
	case "steward.background_jobs.list":
		status, _ := runtimeOptionalString(input, "status")
		limit, _ := runtimeInt(input, "limit", 50)
		output, err := t.service.backgroundJobsSnapshot(ctx, status, limit)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("background_jobs", "durable background jobs and loops loaded", output), nil
	case "steward.reminder.context":
		output, err := t.service.reminderContextSnapshot(ctx)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("reminder_context", "current reminder policy and learned receptivity loaded", output), nil
	case "steward.reminder_policy.get":
		profileScope, _ := runtimeOptionalString(input, "profile_scope")
		category, _ := runtimeOptionalString(input, "category")
		policy, err := t.service.GetReminderPolicyFor(ctx, profileScope, category)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("reminder_policy", "active reminder policy resolved", map[string]any{"policy": policy}), nil
	case "steward.reminder_policy.update":
		profileScope, _ := runtimeOptionalString(input, "profile_scope")
		category, _ := runtimeOptionalString(input, "category")
		rationale, _ := runtimeRequiredString(input, "rationale")
		evidence, _ := runtimeStringSlice(input, "evidence_manifest")
		episodeID, _ := runtimeOptionalString(input, "source_episode_id")
		binding, contextErr := t.service.intelligenceInvocationBinding(ctx)
		if contextErr != nil {
			return RuntimeToolResult{}, contextErr
		}
		if episodeID == "" {
			episodeID = binding.EpisodeID
		}
		idempotencyKey, err := activityBatchToolIdempotencyKey(binding)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		var sourceEpisodeID *string
		if episodeID != "" {
			sourceEpisodeID = &episodeID
		}
		policy, err := t.service.UpdateReminderPolicy(ctx, UpdateReminderPolicyInput{
			ProfileScope: profileScope, Category: category, Policy: input["policy"].(map[string]any),
			Rationale: rationale, EvidenceManifest: evidence, SourceEpisodeID: sourceEpisodeID,
			IdempotencyKey: idempotencyKey,
		})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("reminder_policy", "new reminder policy version persisted", intelligenceStructMap(policy)), nil
	case "steward.reminder_feedback.query":
		limit, _ := runtimeInt(input, "limit", 100)
		feedback, err := t.service.ListReminderFeedback(ctx, limit)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		windows, err := t.service.ListReceptivityWindows(ctx, limit)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		return intelligenceToolResult("reminder_feedback", "reminder outcomes and receptivity windows loaded", map[string]any{"feedback": feedback, "receptivity_windows": windows}), nil
	case "steward.profile.get":
		projection, _ := runtimeRequiredString(input, "view")
		view, err := t.service.GetProfileView(ctx)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		snapshot, ok := ProfileSnapshotForView(view, projection)
		if !ok {
			return RuntimeToolResult{}, fmt.Errorf("view must be recent, stable, explicit, or merged")
		}
		output := map[string]any{"view": projection, "snapshot": snapshot}
		return intelligenceToolResult("profile_snapshot", "declared profile projection loaded", output), nil
	case "steward.profile.explain":
		key, _ := runtimeRequiredString(input, "key")
		facts, err := t.service.ListProfileFacts(ctx, ListProfileFactsInput{Key: key, Limit: 100})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output := map[string]any{"key": normalizeProfileKey(key), "facts": facts}
		return intelligenceToolResult("profile_evidence", "profile fact versions and evidence loaded", output), nil
	case "steward.profile.upsert_fact":
		key, _ := runtimeRequiredString(input, "key")
		horizon, _ := runtimeRequiredString(input, "horizon")
		summary, _ := runtimeOptionalString(input, "summary")
		jobID, _ := runtimeOptionalString(input, "job_id")
		_, invocationJobID, contextErr := t.service.intelligenceInvocationContext(ctx)
		if contextErr != nil {
			return RuntimeToolResult{}, contextErr
		}
		if jobID == "" {
			jobID = invocationJobID
		}
		confirmed, _ := runtimeBool(input, "user_confirmed", false)
		confidence, err := runtimeFloat(input, "confidence", 0.7)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		evidence, _ := parseRuntimeEvidence(input["evidence"])
		status := t.service.autonomyAdvisor().Status()
		fact, err := t.service.UpsertProfileFact(ctx, UpsertProfileFactInput{Key: key, Value: input["value"].(map[string]any), Summary: summary,
			Horizon: horizon, Confidence: confidence, UserConfirmed: confirmed, Evidence: evidence, CreatedBy: "model", JobID: jobID,
			Provider: status.Provider, Model: status.Model})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output := map[string]any{"id": fact.ID, "key": fact.Key, "horizon": fact.Horizon, "version": fact.Version,
			"evidence_days": fact.EvidenceDays, "status": fact.Status}
		return intelligenceToolResult("profile_fact", "profile fact version persisted", output), nil
	case "steward.report.get":
		id, _ := runtimeOptionalString(input, "id")
		if id != "" {
			report, err := t.service.GetReport(ctx, id)
			if err != nil {
				return RuntimeToolResult{}, err
			}
			output := map[string]any{"reports": []domain.StewardReport{report}}
			return intelligenceToolResult("report", "report loaded", output), nil
		}
		cadence, _ := runtimeOptionalString(input, "cadence")
		limit, _ := runtimeInt(input, "limit", 20)
		history, _ := runtimeBool(input, "include_history", false)
		reports, err := t.service.ListReports(ctx, cadence, limit, history)
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output := map[string]any{"reports": reports}
		return intelligenceToolResult("report", "reports loaded", output), nil
	case "steward.report.write":
		cadence, _ := runtimeRequiredString(input, "cadence")
		periodStart, _ := runtimeRequiredString(input, "period_start")
		periodEnd, _ := runtimeRequiredString(input, "period_end")
		title, _ := runtimeRequiredString(input, "title")
		body, _ := runtimeRequiredString(input, "body")
		periodKey, _ := runtimeOptionalString(input, "period_key")
		reportStatus, _ := runtimeOptionalString(input, "status")
		summary, _ := runtimeOptionalString(input, "summary")
		episodeID, _ := runtimeOptionalString(input, "episode_id")
		jobID, _ := runtimeOptionalString(input, "job_id")
		invocationEpisodeID, invocationJobID, contextErr := t.service.intelligenceInvocationContext(ctx)
		if contextErr != nil {
			return RuntimeToolResult{}, contextErr
		}
		if episodeID == "" {
			episodeID = invocationEpisodeID
		}
		if jobID == "" {
			jobID = invocationJobID
		}
		silent, _ := runtimeBool(input, "silent", false)
		evidence, _ := parseRuntimeEvidence(input["evidence"])
		metrics, _ := input["metrics"].(map[string]any)
		start, _ := time.Parse(time.RFC3339, periodStart)
		end, _ := time.Parse(time.RFC3339, periodEnd)
		modelStatus := t.service.autonomyAdvisor().Status()
		report, err := t.service.WriteReport(ctx, WriteReportInput{Cadence: cadence, PeriodKey: periodKey, PeriodStart: start, PeriodEnd: end,
			Status: reportStatus, Title: title, Summary: summary, Body: body, Metrics: metrics, Silent: silent, Evidence: evidence,
			EpisodeID: episodeID, JobID: jobID, Provider: modelStatus.Provider, Model: modelStatus.Model})
		if err != nil {
			return RuntimeToolResult{}, err
		}
		output := map[string]any{"id": report.ID, "cadence": report.Cadence, "period_key": report.PeriodKey, "revision": report.Revision,
			"status": report.Status, "silent": report.Silent, "evidence_count": report.EvidenceCount}
		return intelligenceToolResult("report", "versioned report persisted", output), nil
	default:
		return RuntimeToolResult{}, fmt.Errorf("unsupported intelligence tool %s", t.action)
	}
}

type intelligenceInvocationBinding struct {
	EpisodeID      string
	JobID          string
	ContextRefType string
	ContextRefID   string
	ToolCallID     string
}

// intelligenceInvocationBinding follows the durable invocation back to the
// exact provider tool call. Runtime retries create new invocation rows, while
// this binding remains stable because the Agent turn and tool-call ID do not.
func (s *Service) intelligenceInvocationBinding(ctx context.Context) (intelligenceInvocationBinding, error) {
	invocationID, _ := ctx.Value(runtimeInvocationContextKey{}).(string)
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return intelligenceInvocationBinding{}, nil
	}
	var binding intelligenceInvocationBinding
	var stepKey, invocationToolName string
	var toolCallsJSON []byte
	err := s.db.Pool.QueryRow(ctx, `select coalesce(execution.episode_id::text,''),coalesce(job.id::text,''),
		       coalesce(episode.context_ref_type,''),coalesce(episode.context_ref_id,''),
		       coalesce(step.step_key,''),invocation.tool_name,coalesce(turn.tool_calls,'[]'::jsonb)
		from steward_tool_invocations invocation
		join steward_agent_runs run on run.id=invocation.run_id
		join steward_run_steps step on step.id=invocation.step_id
		join steward_conversation_executions execution on execution.run_id=run.id
			or (run.orchestration_id is not null and execution.orchestration_id=run.orchestration_id)
		left join steward_agent_episodes episode on episode.id=execution.episode_id
		left join steward_agent_turns turn on turn.id=execution.turn_id
		left join steward_memory_consolidation_runs job on job.episode_id=execution.episode_id
		where invocation.id::text=$1
		order by case when job.status='executing' then 0 else 1 end,job.updated_at desc nulls last,
		         execution.updated_at desc limit 1`, invocationID).
		Scan(&binding.EpisodeID, &binding.JobID, &binding.ContextRefType, &binding.ContextRefID,
			&stepKey, &invocationToolName, &toolCallsJSON)
	if err == pgx.ErrNoRows {
		return intelligenceInvocationBinding{}, nil
	}
	if err != nil {
		return intelligenceInvocationBinding{}, err
	}
	var calls []domain.StewardAgentToolCall
	if err := json.Unmarshal(toolCallsJSON, &calls); err != nil {
		return intelligenceInvocationBinding{}, fmt.Errorf("decode Agent tool calls for invocation %s: %w", invocationID, err)
	}
	if index, parseErr := strconv.Atoi(strings.TrimPrefix(stepKey, "tool_")); parseErr == nil && strings.HasPrefix(stepKey, "tool_") && index > 0 && index <= len(calls) {
		call := calls[index-1]
		if call.ToolName == invocationToolName {
			binding.ToolCallID = strings.TrimSpace(call.ID)
		}
	}
	if binding.ToolCallID == "" {
		for _, call := range calls {
			if call.ToolName != invocationToolName || strings.TrimSpace(call.ID) == "" {
				continue
			}
			if binding.ToolCallID != "" {
				binding.ToolCallID = ""
				break
			}
			binding.ToolCallID = strings.TrimSpace(call.ID)
		}
	}
	return binding, nil
}

func activityBatchToolIdempotencyKey(binding intelligenceInvocationBinding) (string, error) {
	if binding.ContextRefType != "activity_batch" {
		return "", nil
	}
	batchID := strings.TrimSpace(binding.ContextRefID)
	toolCallID := strings.TrimSpace(binding.ToolCallID)
	if batchID == "" || toolCallID == "" {
		return "", fmt.Errorf("activity batch invocation is missing its stable batch or tool-call identity")
	}
	return fmt.Sprintf("batch:%s:tool:%s", batchID, toolCallID), nil
}

func (s *Service) activityBatchToolIdempotencyKey(ctx context.Context) (string, error) {
	binding, err := s.intelligenceInvocationBinding(ctx)
	if err != nil {
		return "", err
	}
	return activityBatchToolIdempotencyKey(binding)
}

// intelligenceInvocationContext preserves the existing Episode/job lookup for
// intelligence tools that do not need the provider tool-call identity.
func (s *Service) intelligenceInvocationContext(ctx context.Context) (string, string, error) {
	binding, err := s.intelligenceInvocationBinding(ctx)
	if err != nil {
		return "", "", err
	}
	return binding.EpisodeID, binding.JobID, nil
}

func (t runtimeIntelligenceTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if len(output) == 0 {
		return fmt.Errorf("%s returned no output", t.action)
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func intelligenceToolResult(kind, summary string, output map[string]any) RuntimeToolResult {
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: kind, Summary: summary, Payload: output}}}
}

func intelligenceStructMap(value any) map[string]any {
	raw, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}

func runtimeFloat(input map[string]any, key string, fallback float64) (float64, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback, nil
	}
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case json.Number:
		return typed.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(typed), 64)
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func parseRuntimeEvidence(value any) ([]ProfileEvidenceInput, error) {
	if value == nil {
		return []ProfileEvidenceInput{}, nil
	}
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]map[string]any); typedOK {
			items = make([]any, len(typed))
			for i := range typed {
				items[i] = typed[i]
			}
		} else {
			return nil, fmt.Errorf("evidence must be an array")
		}
	}
	result := make([]ProfileEvidenceInput, 0, len(items))
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("evidence[%d] must be an object", index)
		}
		if err := runtimeRejectUnknownFields(item, "source_type", "source_id", "summary", "evidence_day", "content_hash"); err != nil {
			return nil, fmt.Errorf("evidence[%d]: %w", index, err)
		}
		sourceType, okType := item["source_type"].(string)
		sourceID, okID := item["source_id"].(string)
		dayText, okDay := item["evidence_day"].(string)
		if !okType || strings.TrimSpace(sourceType) == "" || !okID || strings.TrimSpace(sourceID) == "" || !okDay || strings.TrimSpace(dayText) == "" {
			return nil, fmt.Errorf("evidence[%d] requires source_type, source_id and evidence_day", index)
		}
		day, err := time.Parse("2006-01-02", strings.TrimSpace(dayText))
		if err != nil {
			day, err = time.Parse(time.RFC3339, strings.TrimSpace(dayText))
		}
		if err != nil {
			return nil, fmt.Errorf("evidence[%d].evidence_day must be YYYY-MM-DD or RFC3339", index)
		}
		summary, _ := item["summary"].(string)
		hash, _ := item["content_hash"].(string)
		if strings.TrimSpace(hash) == "" {
			hash = evidenceHash(sourceType, sourceID, summary)
		}
		result = append(result, ProfileEvidenceInput{SourceType: sourceType, SourceID: sourceID, Summary: summary, EvidenceDay: day, ContentHash: hash})
	}
	return result, nil
}

func parseActivityQueryWindow(fromText, toText string) (*time.Time, *time.Time, error) {
	var from, to *time.Time
	if strings.TrimSpace(fromText) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(fromText))
		if err != nil {
			return nil, nil, fmt.Errorf("from must be RFC3339: %w", err)
		}
		parsed = parsed.UTC()
		from = &parsed
	}
	if strings.TrimSpace(toText) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(toText))
		if err != nil {
			return nil, nil, fmt.Errorf("to must be RFC3339: %w", err)
		}
		parsed = parsed.UTC()
		to = &parsed
	}
	if from != nil && to != nil && !to.After(*from) {
		return nil, nil, fmt.Errorf("to must be after from")
	}
	return from, to, nil
}

func (s *Service) runtimeIntelligenceStatus(ctx context.Context) (map[string]any, error) {
	agent, err := s.GetAgentStatus(ctx)
	if err != nil {
		return nil, err
	}
	control, err := s.GetRuntimeExecutionControl(ctx)
	if err != nil {
		return nil, err
	}
	model := s.autonomyAdvisor().Status()
	return map[string]any{
		"agent": agent, "planner": s.GetRuntimePlannerStatus(), "model": model,
		"execution_control": control, "tool_hosts": s.GetToolHostStatuses(ctx), "checked_at": time.Now().UTC(),
	}, nil
}

func (s *Service) queryRuntimeActivity(ctx context.Context, input map[string]any) (map[string]any, error) {
	fromText, _ := runtimeOptionalString(input, "from")
	toText, _ := runtimeOptionalString(input, "to")
	from, to, err := parseActivityQueryWindow(fromText, toText)
	if err != nil {
		return nil, err
	}
	source, _ := runtimeOptionalString(input, "source")
	deviceID, _ := runtimeOptionalString(input, "device_id")
	kind, _ := runtimeOptionalString(input, "kind")
	kind = strings.ToLower(defaultString(kind, "all"))
	limit, _ := runtimeInt(input, "limit", 100)
	limit = normalizeLimit(limit, 100, 500)

	sessions := []domain.StewardActivitySession{}
	if kind == "all" || kind == "sessions" {
		rows, queryErr := s.db.Pool.Query(ctx, `select id::text,type,title,summary,source,context_key,device_id,data_level,
			status,observation_count,confidence,value_score,started_at,ended_at,timeline_id::text,created_at,updated_at
			from steward_activity_sessions where ($1::timestamptz is null or ended_at >= $1)
			and ($2::timestamptz is null or started_at <= $2) and ($3='' or source=$3) and ($4='' or device_id=$4)
			order by started_at desc limit $5`, from, to, source, deviceID, limit)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var item domain.StewardActivitySession
			if scanErr := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Summary, &item.Source, &item.ContextKey,
				&item.DeviceID, &item.DataLevel, &item.Status, &item.ObservationCount, &item.Confidence, &item.ValueScore,
				&item.StartedAt, &item.EndedAt, &item.TimelineID, &item.CreatedAt, &item.UpdatedAt); scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			sessions = append(sessions, item)
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return nil, queryErr
		}
		rows.Close()
	}

	observations := []domain.StewardObservation{}
	if kind == "all" || kind == "observations" {
		rows, queryErr := s.db.Pool.Query(ctx, observationSelect+` where ($1::timestamptz is null or o.occurred_at >= $1)
			and ($2::timestamptz is null or o.occurred_at <= $2) and ($3='' or o.source=$3) and ($4='' or o.device_id=$4)
			order by o.occurred_at desc limit $5`, from, to, source, deviceID, limit)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			item, scanErr := scanObservation(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			observations = append(observations, item)
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return nil, queryErr
		}
		rows.Close()
	}
	location := s.evidenceTimezone(ctx)
	evidenceRefs := make([]map[string]any, 0, len(sessions)+len(observations))
	for _, item := range sessions {
		evidenceRefs = append(evidenceRefs, map[string]any{
			"source_type": "activity_session", "source_id": item.ID, "summary": item.Summary,
			"evidence_day": item.StartedAt.In(location).Format("2006-01-02"),
			"content_hash": evidenceHash("activity_session", item.ID, item.Summary),
		})
	}
	for _, item := range observations {
		evidenceRefs = append(evidenceRefs, map[string]any{
			"source_type": "observation", "source_id": item.ID, "summary": item.Summary,
			"evidence_day": item.OccurredAt.In(location).Format("2006-01-02"),
			"content_hash": defaultString(strings.TrimSpace(item.Fingerprint), evidenceHash("observation", item.ID, item.Summary)),
		})
	}
	return map[string]any{
		"sessions": sessions, "observations": observations, "evidence_refs": evidenceRefs,
		"window": map[string]any{"from": from, "to": to, "source": source, "device_id": deviceID, "kind": kind},
		"counts": map[string]any{"sessions": len(sessions), "observations": len(observations)},
	}, nil
}

func (s *Service) backgroundJobsSnapshot(ctx context.Context, status string, limit int) (map[string]any, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	limit = normalizeLimit(limit, 50, 200)
	jobs, err := s.ListIntelligenceJobs(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	type batchSummary struct {
		ID            string    `json:"id"`
		DeviceID      string    `json:"device_id"`
		Status        string    `json:"status"`
		TriggerKind   string    `json:"trigger_kind"`
		ErrorCode     string    `json:"error_code,omitempty"`
		ErrorSummary  string    `json:"error_summary,omitempty"`
		WindowStart   time.Time `json:"window_start"`
		WindowEnd     time.Time `json:"window_end"`
		NextAttemptAt time.Time `json:"next_attempt_at"`
		UpdatedAt     time.Time `json:"updated_at"`
		AttemptCount  int       `json:"attempt_count"`
		EpisodeID     *string   `json:"episode_id,omitempty"`
	}
	batches := []batchSummary{}
	rows, err := s.db.Pool.Query(ctx, `select id::text,device_id,status,trigger_kind,error_code,error_summary,
		window_start,window_end,next_attempt_at,updated_at,attempt_count,episode_id::text from steward_activity_batches
		where ($1='' or status=$1) order by due_at desc,created_at desc limit $2`, status, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item batchSummary
		if err := rows.Scan(&item.ID, &item.DeviceID, &item.Status, &item.TriggerKind, &item.ErrorCode, &item.ErrorSummary,
			&item.WindowStart, &item.WindowEnd, &item.NextAttemptAt, &item.UpdatedAt, &item.AttemptCount, &item.EpisodeID); err != nil {
			rows.Close()
			return nil, err
		}
		batches = append(batches, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	type episodeSummary struct {
		ID                string     `json:"id"`
		TriggerKind       string     `json:"trigger_kind"`
		Goal              string     `json:"goal"`
		Status            string     `json:"status"`
		LastResultSummary string     `json:"last_result_summary,omitempty"`
		FailureSummary    string     `json:"failure_summary,omitempty"`
		CurrentRound      int        `json:"current_round"`
		ToolCallCount     int        `json:"tool_call_count"`
		CreatedAt         time.Time  `json:"created_at"`
		UpdatedAt         time.Time  `json:"updated_at"`
		DeadlineAt        *time.Time `json:"deadline_at,omitempty"`
	}
	episodes := []episodeSummary{}
	rows, err = s.db.Pool.Query(ctx, `select id::text,trigger_kind,goal,status,last_result_summary,failure_summary,
		current_round,tool_call_count,created_at,updated_at,deadline_at from steward_agent_episodes
		where trigger_kind<>'conversation' and ($1='' or status=$1) order by updated_at desc limit $2`, status, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item episodeSummary
		if err := rows.Scan(&item.ID, &item.TriggerKind, &item.Goal, &item.Status, &item.LastResultSummary,
			&item.FailureSummary, &item.CurrentRound, &item.ToolCallCount, &item.CreatedAt, &item.UpdatedAt, &item.DeadlineAt); err != nil {
			rows.Close()
			return nil, err
		}
		episodes = append(episodes, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	agent, err := s.GetAgentStatus(ctx)
	if err != nil {
		return nil, err
	}
	pipeline, err := s.ActivityPipelineStatus(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"intelligence_jobs": jobs, "activity_batches": batches, "episodes": episodes, "loops": agent.BackgroundLoops,
		"counts": map[string]any{
			"intelligence_jobs_returned": len(jobs), "activity_batches_returned": len(batches), "episodes_returned": len(episodes),
			"pending_activity_batches": pipeline.PendingBatches, "processing_activity_batches": pipeline.ProcessingBatches,
			"waiting_model_activity_batches": pipeline.WaitingModel, "failed_activity_batches": pipeline.FailedBatches,
		},
		"checked_at": time.Now().UTC(),
	}, nil
}

func (s *Service) registerIntelligenceTools() {
	if s == nil || s.runtimeTools == nil {
		return
	}
	for _, action := range []string{
		"steward.runtime_status", "steward.collection_status", "steward.activity.query", "steward.background_jobs.list",
		"steward.reminder.context", "steward.reminder_policy.get", "steward.reminder_policy.update", "steward.reminder_feedback.query",
		"steward.profile.get", "steward.profile.explain", "steward.profile.upsert_fact", "steward.report.get", "steward.report.write",
	} {
		s.runtimeTools.registerIfAbsent(newRuntimeIntelligenceTool(s, action))
	}
}
