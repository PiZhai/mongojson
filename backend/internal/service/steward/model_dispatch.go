package steward

import (
	"context"
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
	modelDispatchPending   = "pending"
	modelDispatchRunning   = "processing"
	modelDispatchCompleted = "completed"
	modelDispatchBlocked   = "blocked"
	modelDispatchFailed    = "failed"
)

type storedObservationForModel struct {
	ID               string
	Source           string
	Type             string
	Summary          string
	DataLevel        string
	ContextKey       string
	Fingerprint      string
	Payload          map[string]any
	PayloadEncrypted bool
	OccurredAt       time.Time
}

func (s *Service) enqueueModelDispatch(ctx context.Context, observationID string, observationTime time.Time, source, level, contentMode string) error {
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_model_dispatches (
			id,observation_id,observation_time,source,data_level,content_mode,status,created_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		on conflict (observation_id,observation_time) do nothing
	`, uuid.NewString(), observationID, observationTime, source, level, contentMode, modelDispatchPending, now)
	return err
}

func (s *Service) ListModelDispatches(ctx context.Context, limit int) ([]domain.StewardModelDispatch, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, `
		select id,observation_id,observation_time,source,data_level,content_mode,status,
		       attempts,request_summary,response_summary,last_error,next_attempt_at,
		       provider,model,audit_id,created_at,updated_at,completed_at
		from steward_model_dispatches order by created_at desc limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list model dispatches: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardModelDispatch{}
	for rows.Next() {
		item, err := scanModelDispatch(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) RunModelDispatches(ctx context.Context, limit int) ([]domain.StewardModelDispatch, error) {
	batchMode, err := s.intelligenceBatchEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("read intelligence dispatch mode: %w", err)
	}
	if batchMode {
		if _, err := s.supersedeLegacyModelDispatches(ctx, time.Now().UTC()); err != nil {
			return nil, err
		}
		return []domain.StewardModelDispatch{}, nil
	}
	limit = normalizeLimit(limit, 20, 100)
	rows, err := s.db.Pool.Query(ctx, `
		select id from steward_model_dispatches
		where status in ('pending','failed') and (next_attempt_at is null or next_attempt_at <= now())
		order by created_at limit $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("select model dispatches: %w", err)
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
	results := []domain.StewardModelDispatch{}
	for _, id := range ids {
		item, claimed, err := s.claimModelDispatch(ctx, id)
		if err != nil {
			return results, err
		}
		if !claimed {
			continue
		}
		item = s.processModelDispatch(ctx, item)
		results = append(results, item)
	}
	return results, nil
}

func (s *Service) claimModelDispatch(ctx context.Context, id string) (domain.StewardModelDispatch, bool, error) {
	row := s.db.Pool.QueryRow(ctx, `
		update steward_model_dispatches set status=$1,attempts=attempts+1,updated_at=now()
		where id=$2 and status in ('pending','failed') and (next_attempt_at is null or next_attempt_at <= now())
		returning id,observation_id,observation_time,source,data_level,content_mode,status,
		          attempts,request_summary,response_summary,last_error,next_attempt_at,
		          provider,model,audit_id,created_at,updated_at,completed_at
	`, modelDispatchRunning, id)
	item, err := scanModelDispatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.StewardModelDispatch{}, false, nil
		}
		return domain.StewardModelDispatch{}, false, err
	}
	return item, true, nil
}

func (s *Service) processModelDispatch(ctx context.Context, item domain.StewardModelDispatch) domain.StewardModelDispatch {
	policy, err := s.ResolveDataPolicy(ctx, item.DataLevel, item.Source)
	if err != nil || policy.ModelMode != PolicyModeAuto {
		reason := "automatic model disclosure is no longer allowed"
		if err != nil {
			reason = err.Error()
		}
		return s.finishModelDispatch(ctx, item, modelDispatchBlocked, "", reason, nil)
	}
	permission, err := s.ResolvePermissionPolicy(ctx, PermissionA6, "model:observation")
	if err != nil || permission.ExecutionMode != PolicyModeAuto {
		reason := "A6 model disclosure policy is not automatic"
		if err != nil {
			reason = err.Error()
		}
		return s.finishModelDispatch(ctx, item, modelDispatchBlocked, "", reason, nil)
	}
	advisor, ok := s.autonomyAdvisor().(ObservationModelAdvisor)
	if !ok || !s.autonomyAdvisor().Status().Enabled {
		return s.retryModelDispatch(ctx, item, "configured advisor does not support observation analysis")
	}
	observation, err := s.loadObservationForModel(ctx, item.ObservationID, item.ObservationTime)
	if err != nil {
		return s.retryModelDispatch(ctx, item, err.Error())
	}
	content, err := modelDispatchContent(observation, policy.ModelContentMode)
	if err != nil {
		return s.retryModelDispatch(ctx, item, err.Error())
	}
	status := s.autonomyAdvisor().Status()
	output, err := advisor.AnalyzeObservation(ctx, ObservationModelInput{
		Source: observation.Source, Type: observation.Type, DataLevel: observation.DataLevel,
		ContentMode: policy.ModelContentMode, Content: content,
	})
	if err != nil {
		if errors.Is(err, ErrAdvisorDataLevelDenied) {
			return s.finishModelDispatch(ctx, item, modelDispatchBlocked, "", err.Error(), nil)
		}
		return s.retryModelDispatch(ctx, item, err.Error())
	}
	item.Provider = status.Provider
	item.Model = status.Model
	item.RequestSummary = fmt.Sprintf("%s %s payload, %d characters", item.DataLevel, policy.ModelContentMode, len([]rune(content)))
	item.ResponseSummary = output.Summary
	if err := s.storeModelDispatchInsight(ctx, item, output); err != nil {
		return s.retryModelDispatch(ctx, item, err.Error())
	}
	return s.finishModelDispatch(ctx, item, modelDispatchCompleted, output.Summary, "", &status)
}

func (s *Service) loadObservationForModel(ctx context.Context, id string, occurredAt time.Time) (storedObservationForModel, error) {
	var item storedObservationForModel
	err := s.db.Pool.QueryRow(ctx, `
		select id,source,type,summary,data_level,context_key,fingerprint,payload,payload_encrypted,occurred_at
		from steward_observations where id=$1 and occurred_at=$2
	`, id, occurredAt).Scan(&item.ID, &item.Source, &item.Type, &item.Summary, &item.DataLevel,
		&item.ContextKey, &item.Fingerprint, &item.Payload, &item.PayloadEncrypted, &item.OccurredAt)
	if err != nil {
		return storedObservationForModel{}, fmt.Errorf("load observation for model: %w", err)
	}
	if item.PayloadEncrypted {
		keyring, err := localPayloadKeyringFromEnv()
		if err != nil {
			return storedObservationForModel{}, err
		}
		item.Payload, err = decryptPayloadEnvelope(keyring, observationEncryptionAAD(item.Source, item.Fingerprint, item.OccurredAt), item.Payload, "model observation payload")
		if err != nil {
			return storedObservationForModel{}, err
		}
	}
	if value, ok := item.Payload["_steward_original_summary"].(string); ok && value != "" {
		item.Summary = value
		delete(item.Payload, "_steward_original_summary")
	}
	if value, ok := item.Payload["_steward_original_context_key"].(string); ok && value != "" {
		item.ContextKey = value
		delete(item.Payload, "_steward_original_context_key")
	}
	return item, nil
}

func modelDispatchContent(item storedObservationForModel, mode string) (string, error) {
	metadata := map[string]any{
		"source": item.Source, "type": item.Type, "data_level": item.DataLevel,
		"occurred_at": item.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	switch mode {
	case ModelContentMetadata:
		encoded, _ := json.Marshal(metadata)
		return string(encoded), nil
	case ModelContentSummary:
		metadata["summary"] = item.Summary
		encoded, _ := json.Marshal(metadata)
		return string(encoded), nil
	case ModelContentRedacted, ModelContentRaw:
		metadata["summary"] = item.Summary
		metadata["context_key"] = item.ContextKey
		metadata["payload"] = item.Payload
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return "", err
		}
		content := string(encoded)
		if mode == ModelContentRedacted {
			content = redactCredentialText(content)
		}
		return content, nil
	default:
		return "", fmt.Errorf("unsupported model content mode %q", mode)
	}
}

func redactCredentialText(value string) string {
	result := value
	for _, candidate := range credentialPatterns {
		result = candidate.pattern.ReplaceAllString(result, "[REDACTED:"+strings.ToUpper(candidate.name)+"]")
	}
	return result
}

func (s *Service) storeModelDispatchInsight(ctx context.Context, dispatch domain.StewardModelDispatch, output ObservationModelOutput) error {
	insights := make([]string, 0, len(output.Insights)+1)
	insights = append(insights, redactCredentialText(output.Summary))
	for _, insight := range output.Insights {
		insights = append(insights, redactCredentialText(insight))
	}
	suggestedActions := make([]string, 0, len(output.SuggestedActions))
	for _, action := range output.SuggestedActions {
		suggestedActions = append(suggestedActions, redactCredentialText(action))
	}
	suggested := strings.Join(suggestedActions, "；")
	if dispatch.DataLevel == DataD5 {
		insights = []string{"凭据类数据的模型分析已完成；返回内容未写入明文洞察。"}
		suggested = ""
	}
	now := time.Now().UTC()
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_insights (
			id,type,title,summary,suggested_action,status,data_level,confidence,evidence_count,
			value_score,user_confirmed,retention_locked,created_at,updated_at
		) values ($1,'model_analysis',$2,$3,$4,'candidate',$5,0.7,1,0.6,false,false,$6,$6)
	`, uuid.NewString(), "模型分析："+dispatch.Source, strings.Join(insights, "\n"), suggested, dispatch.DataLevel, now)
	return err
}

func (s *Service) retryModelDispatch(ctx context.Context, item domain.StewardModelDispatch, message string) domain.StewardModelDispatch {
	if item.Attempts >= 5 {
		return s.finishModelDispatch(ctx, item, modelDispatchBlocked, "", message, nil)
	}
	delay := time.Duration(1<<min(item.Attempts, 8)) * time.Minute
	next := time.Now().UTC().Add(delay)
	item.Status = modelDispatchFailed
	item.LastError = sanitizeAdvisorStatusError(errors.New(message))
	item.NextAttemptAt = &next
	item.UpdatedAt = time.Now().UTC()
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_model_dispatches set status=$1,last_error=$2,next_attempt_at=$3,updated_at=$4 where id=$5
	`, item.Status, item.LastError, next, item.UpdatedAt, item.ID)
	return item
}

func (s *Service) finishModelDispatch(ctx context.Context, item domain.StewardModelDispatch, status, response, message string, advisorStatus *domain.StewardAutonomyAdvisorStatus) domain.StewardModelDispatch {
	now := time.Now().UTC()
	item.Status = status
	item.ResponseSummary = response
	item.LastError = sanitizeAdvisorStatusError(errors.New(message))
	item.NextAttemptAt = nil
	item.UpdatedAt = now
	if status == modelDispatchCompleted {
		item.CompletedAt = &now
	}
	if advisorStatus != nil {
		item.Provider = advisorStatus.Provider
		item.Model = advisorStatus.Model
	}
	resultStatus := ResultOK
	if status == modelDispatchBlocked {
		resultStatus = ResultBlocked
	} else if status == modelDispatchFailed {
		resultStatus = ResultFailed
	}
	confirmed, syncable := false, false
	errorSummary := (*string)(nil)
	if item.LastError != "" {
		errorSummary = &item.LastError
	}
	auditID, _ := s.recordAudit(ctx, AuditInput{
		Actor: "model-dispatch", Action: "model.dispatch." + status, TargetType: "observation", TargetID: &item.ObservationID,
		Source: "security:model-disclosure", PermissionLevel: PermissionA6, DataLevel: item.DataLevel,
		InputSummary:  defaultString(item.RequestSummary, item.ContentMode+" observation payload"),
		OutputSummary: defaultString(item.ResponseSummary, status), Reason: message,
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: resultStatus, ErrorSummary: errorSummary,
	})
	if auditID != "" {
		item.AuditID = &auditID
	}
	_, _ = s.db.Pool.Exec(ctx, `
		update steward_model_dispatches set status=$1,request_summary=$2,response_summary=$3,last_error=$4,
		       next_attempt_at=null,provider=$5,model=$6,audit_id=$7,updated_at=$8,completed_at=$9
		where id=$10
	`, status, item.RequestSummary, item.ResponseSummary, item.LastError, item.Provider, item.Model,
		item.AuditID, now, item.CompletedAt, item.ID)
	return item
}

func scanModelDispatch(row rowScanner) (domain.StewardModelDispatch, error) {
	var item domain.StewardModelDispatch
	err := row.Scan(&item.ID, &item.ObservationID, &item.ObservationTime, &item.Source,
		&item.DataLevel, &item.ContentMode, &item.Status, &item.Attempts, &item.RequestSummary,
		&item.ResponseSummary, &item.LastError, &item.NextAttemptAt, &item.Provider, &item.Model,
		&item.AuditID, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt)
	return item, err
}
