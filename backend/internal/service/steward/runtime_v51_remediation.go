package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
)

const (
	runtimeChangePrepared        = "prepared"
	runtimeChangeRollbackPending = "rollback_pending"
	runtimeChangeRollingBack     = "rolling_back"
	runtimeChangeCommitted       = "committed"
	runtimeChangeRolledBack      = "rolled_back"
	runtimeChangeRollbackFailed  = "rollback_failed"
)

// RuntimeChangeSnapshot is captured before a mutating tool starts. Snapshot
// data must contain only the minimum state needed to restore the old value.
type RuntimeChangeSnapshot struct {
	Summary string         `json:"summary"`
	State   map[string]any `json:"state"`
}

// RuntimeTransactionalTool opts a mutating tool into the R5.1 transaction
// protocol. The runtime, not the model or tool script, owns the prepare,
// commit, rollback and crash-recovery state machine.
type RuntimeTransactionalTool interface {
	ChangeTransactionEnabled() bool
	SnapshotChange(context.Context, map[string]any) (RuntimeChangeSnapshot, error)
	VerifyChange(context.Context, map[string]any, RuntimeChangeSnapshot, RuntimeToolResult) error
	RollbackChange(context.Context, map[string]any, RuntimeChangeSnapshot, RuntimeToolResult, error) (RuntimeToolResult, error)
}

type runtimeFailureDiagnosis struct {
	Code             string   `json:"code"`
	Category         string   `json:"category"`
	Retryable        bool     `json:"retryable"`
	AlternativeHints []string `json:"alternative_hints,omitempty"`
	Summary          string   `json:"summary"`
}

type runtimeChangeCommitUncertainError struct{ cause error }

func (e runtimeChangeCommitUncertainError) Error() string {
	return "mutation journal commit outcome is unknown: " + e.cause.Error()
}
func (e runtimeChangeCommitUncertainError) Unwrap() error { return e.cause }

type runtimeInvocationContextKey struct{}

func withRuntimeInvocationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, runtimeInvocationContextKey{}, id)
}

func diagnoseRuntimeFailure(cause error) runtimeFailureDiagnosis {
	summary := sanitizeRuntimeError(cause)
	message := strings.ToLower(summary)
	diagnosis := runtimeFailureDiagnosis{Code: "unknown", Category: "execution", Summary: summary}
	var netErr net.Error
	switch {
	case errors.Is(cause, context.DeadlineExceeded) || strings.Contains(message, "timed out") || strings.Contains(message, "timeout"):
		diagnosis.Code, diagnosis.Category, diagnosis.Retryable = "timeout", "transient", true
		diagnosis.AlternativeHints = []string{"retry with a bounded longer timeout", "use a different local or remote device"}
	case errors.Is(cause, context.Canceled):
		diagnosis.Code, diagnosis.Category = "cancelled", "control"
	case errors.Is(cause, os.ErrPermission) || strings.Contains(message, "access is denied") || strings.Contains(message, "access denied") || strings.Contains(message, "permission denied") || strings.Contains(message, "unauthorizedaccess"):
		diagnosis.Code, diagnosis.Category = "access_denied", "permission"
		diagnosis.AlternativeHints = []string{"use an equivalent user-scoped operation", "use the session companion for the logged-in user", "select another writable target"}
	case errors.Is(cause, os.ErrNotExist) || strings.Contains(message, "not found") || strings.Contains(message, "cannot find") || strings.Contains(message, "does not exist"):
		diagnosis.Code, diagnosis.Category = "not_found", "availability"
		diagnosis.AlternativeHints = []string{"discover the actual path or installed capability", "install or create the missing dependency", "use an existing equivalent tool"}
	case errors.As(cause, &netErr) || strings.Contains(message, "connection refused") || strings.Contains(message, "temporarily unavailable"):
		diagnosis.Code, diagnosis.Category, diagnosis.Retryable = "unavailable", "transient", true
		diagnosis.AlternativeHints = []string{"retry after refreshing device and service state", "select another online device or endpoint"}
	case strings.Contains(message, "already exists") || strings.Contains(message, "conflict") || strings.Contains(message, "in use"):
		diagnosis.Code, diagnosis.Category = "conflict", "state"
		diagnosis.AlternativeHints = []string{"inspect the current state and reconcile instead of repeating", "choose a non-conflicting name or destination"}
	case strings.Contains(message, "postcondition failed") || strings.Contains(message, "output contract") || strings.Contains(message, "schema"):
		diagnosis.Code, diagnosis.Category = "verification_failed", "integrity"
		diagnosis.AlternativeHints = []string{"inspect the observed state instead of trusting process output", "repair the tool or use a separately verifiable alternative"}
	}
	return diagnosis
}

func attachRuntimeFailureDiagnostics(result RuntimeToolResult, diagnosis runtimeFailureDiagnosis, transactionID, rollbackStatus string) RuntimeToolResult {
	if result.Output == nil {
		result.Output = map[string]any{}
	}
	result.Output["_remediation"] = map[string]any{
		"failure": diagnosis, "transaction_id": transactionID,
		"rollback_status": rollbackStatus,
		"instruction":     "Inspect the verified state, then choose a materially different tool, scope, device, or implementation. Do not repeat an unchanged failing call.",
	}
	result.Evidence = append(result.Evidence, RuntimeEvidence{Kind: "failure_diagnosis", Summary: diagnosis.Code, Payload: map[string]any{
		"code": diagnosis.Code, "category": diagnosis.Category, "retryable": diagnosis.Retryable,
		"alternative_hints": diagnosis.AlternativeHints, "transaction_id": transactionID, "rollback_status": rollbackStatus,
	}})
	return result
}

func (s *Service) beginRuntimeChangeTransaction(ctx context.Context, step domain.StewardRunStep, invocation domain.StewardToolInvocation, tool RuntimeTool) (string, RuntimeChangeSnapshot, error) {
	transactional, ok := tool.(RuntimeTransactionalTool)
	if !ok || !transactional.ChangeTransactionEnabled() {
		return "", RuntimeChangeSnapshot{}, nil
	}
	snapshot, err := transactional.SnapshotChange(ctx, step.Arguments)
	if err != nil {
		return "", snapshot, fmt.Errorf("capture mutation pre-state: %w", err)
	}
	if snapshot.State == nil {
		snapshot.State = map[string]any{}
	}
	id := uuid.NewString()
	argumentsJSON, _ := json.Marshal(step.Arguments)
	snapshotJSON, _ := json.Marshal(snapshot)
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_system_change_transactions (
			id,invocation_id,run_id,step_id,tool_name,tool_version,status,arguments,snapshot,
			lease_owner,lease_expires_at,prepared_at,updated_at
		) values ($1,$2,$3,$4,$5,$6,'prepared',$7::jsonb,$8::jsonb,$9,now()+interval '90 seconds',now(),now())
	`, id, invocation.ID, step.RunID, step.ID, invocation.ToolName, invocation.ToolVersion, string(argumentsJSON), string(snapshotJSON), s.runtimeWorkerID)
	if err != nil {
		return "", snapshot, fmt.Errorf("persist mutation pre-state: %w", err)
	}
	return id, snapshot, nil
}

func (s *Service) commitRuntimeChangeTransaction(ctx context.Context, id string, result RuntimeToolResult) error {
	if id == "" {
		return nil
	}
	if result.Output == nil {
		result.Output = map[string]any{}
	}
	result.Output["_transaction"] = map[string]any{"id": id, "status": runtimeChangeCommitted, "verified": true}
	resultJSON, _ := json.Marshal(result.Output)
	command, err := s.db.Pool.Exec(ctx, `
		update steward_system_change_transactions set status='committed',result=$2::jsonb,committed_at=now(),updated_at=now(),
			lease_owner='',lease_expires_at=null where id=$1 and status='prepared'
	`, id, string(resultJSON))
	if err != nil {
		return runtimeChangeCommitUncertainError{cause: err}
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("commit mutation journal: transaction %s is no longer prepared", id)
	}
	return nil
}

func attachRuntimeCommitMetadata(result RuntimeToolResult, id string) RuntimeToolResult {
	if id == "" {
		return result
	}
	if result.Output == nil {
		result.Output = map[string]any{}
	}
	result.Output["_transaction"] = map[string]any{"id": id, "status": runtimeChangeCommitted, "verified": true}
	return result
}

func (s *Service) rollbackRuntimeChangeTransaction(ctx context.Context, id string, tool RuntimeTool, arguments map[string]any, snapshot RuntimeChangeSnapshot, result RuntimeToolResult, cause error) (RuntimeToolResult, string, error) {
	diagnosis := diagnoseRuntimeFailure(cause)
	if id == "" {
		return attachRuntimeFailureDiagnostics(result, diagnosis, "", "not_supported"), "not_supported", nil
	}
	transactional, ok := tool.(RuntimeTransactionalTool)
	if !ok || !transactional.ChangeTransactionEnabled() {
		return attachRuntimeFailureDiagnostics(result, diagnosis, id, "unavailable"), "unavailable", fmt.Errorf("transactional tool contract disappeared")
	}
	resultJSON, _ := json.Marshal(result.Output)
	_, _ = s.db.Pool.Exec(ctx, `update steward_system_change_transactions set status='rolling_back',result=$2::jsonb,
		failure_code=$3,failure_category=$4,failure_summary=$5,rollback_attempts=rollback_attempts+1,
		lease_owner=$6,lease_expires_at=now()+interval '90 seconds',updated_at=now() where id=$1`,
		id, string(resultJSON), diagnosis.Code, diagnosis.Category, diagnosis.Summary, s.runtimeWorkerID)
	rollbackResult, rollbackErr := transactional.RollbackChange(ctx, arguments, snapshot, result, cause)
	rollbackJSON, _ := json.Marshal(rollbackResult.Output)
	status := runtimeChangeRolledBack
	errorText := ""
	if rollbackErr != nil {
		status = runtimeChangeRollbackFailed
		errorText = sanitizeRuntimeError(rollbackErr)
	}
	_, persistErr := s.db.Pool.Exec(ctx, `update steward_system_change_transactions set status=$2,rollback_result=$3::jsonb,
		rollback_error=$4,rolled_back_at=case when $2='rolled_back' then now() else null end,
		next_rollback_attempt_at=case when $2='rollback_failed' then now()+interval '30 seconds' else null end,
		lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, id, status, string(rollbackJSON), errorText)
	result.Evidence = append(result.Evidence, rollbackResult.Evidence...)
	result = attachRuntimeFailureDiagnostics(result, diagnosis, id, status)
	return result, status, errors.Join(rollbackErr, persistErr)
}

// RecoverSystemChangeTransactions completes compensating actions that were
// interrupted by a backend crash. A transaction is claimed with SKIP LOCKED so
// multiple daemon processes cannot perform the same rollback.
func (s *Service) RecoverSystemChangeTransactions(ctx context.Context, limit int) (int, error) {
	if s == nil || !s.runtimeV2 || s.db == nil || s.db.Pool == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Pool.Query(ctx, `
		with candidates as (
			select id from steward_system_change_transactions
			where status in ('prepared','rollback_pending','rollback_failed')
			  and (next_rollback_attempt_at is null or next_rollback_attempt_at<=now())
			  and (lease_expires_at is null or lease_expires_at<now())
			order by updated_at for update skip locked limit $1
		)
		update steward_system_change_transactions transaction set status='rolling_back',lease_owner=$2,
			lease_expires_at=now()+interval '90 seconds',rollback_attempts=rollback_attempts+1,updated_at=now()
		from candidates where transaction.id=candidates.id
		returning transaction.id::text,transaction.tool_name,transaction.tool_version,
			transaction.arguments,transaction.snapshot,transaction.result,transaction.failure_summary
	`, limit, s.runtimeWorkerID+":remediation")
	if err != nil {
		return 0, err
	}
	type pending struct {
		id, toolName, toolVersion, failure string
		arguments, snapshot, result        []byte
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.toolName, &item.toolVersion, &item.arguments, &item.snapshot, &item.result, &item.failure); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	recovered := 0
	var recoveryErrors []error
	for _, item := range items {
		tool, ok := s.runtimeToolForRecovery(ctx, item.toolName, item.toolVersion)
		transactional, transactionalOK := tool.(RuntimeTransactionalTool)
		if !ok || !transactionalOK || !transactional.ChangeTransactionEnabled() {
			recoveryErrors = append(recoveryErrors, s.markSystemChangeRollbackFailed(ctx, item.id, "required immutable tool version is unavailable"))
			continue
		}
		arguments := map[string]any{}
		storedSnapshot := RuntimeChangeSnapshot{}
		storedResult := RuntimeToolResult{Output: map[string]any{}}
		_ = json.Unmarshal(item.arguments, &arguments)
		_ = json.Unmarshal(item.snapshot, &storedSnapshot)
		_ = json.Unmarshal(item.result, &storedResult.Output)
		rollbackResult, rollbackErr := transactional.RollbackChange(ctx, arguments, storedSnapshot, storedResult, errors.New(defaultString(item.failure, "interrupted mutation")))
		rollbackJSON, _ := json.Marshal(rollbackResult.Output)
		if rollbackErr != nil {
			recoveryErrors = append(recoveryErrors, s.markSystemChangeRollbackFailed(ctx, item.id, sanitizeRuntimeError(rollbackErr)))
			continue
		}
		if _, err := s.db.Pool.Exec(ctx, `update steward_system_change_transactions set status='rolled_back',rollback_result=$2::jsonb,
			rollback_error='',rolled_back_at=now(),next_rollback_attempt_at=null,lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, item.id, string(rollbackJSON)); err != nil {
			recoveryErrors = append(recoveryErrors, err)
			continue
		}
		recovered++
	}
	return recovered, errors.Join(recoveryErrors...)
}

func (s *Service) runtimeToolForRecovery(ctx context.Context, name, version string) (RuntimeTool, bool) {
	if tool, ok := s.runtimeTools.get(name); ok && tool.Spec().Version == version {
		return tool, true
	}
	var manifestJSON []byte
	if err := s.db.Pool.QueryRow(ctx, `select manifest from steward_tool_versions where tool_name=$1 and version=$2`, name, version).Scan(&manifestJSON); err != nil {
		return nil, false
	}
	var manifest ToolPackageManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, false
	}
	normalized, err := normalizeToolPackageManifest(manifest)
	if err != nil || normalized.Name != name || normalized.Version != version {
		return nil, false
	}
	return newPackageRuntimeTool(s, normalized), true
}

func (s *Service) markSystemChangeRollbackFailed(ctx context.Context, id, summary string) error {
	_, err := s.db.Pool.Exec(ctx, `update steward_system_change_transactions set status='rollback_failed',rollback_error=$2,
		next_rollback_attempt_at=now()+interval '30 seconds',lease_owner='',lease_expires_at=null,updated_at=now() where id=$1`, id, summary)
	return err
}

func (s *Service) ListSystemChangeTransactions(ctx context.Context, limit int) ([]domain.StewardSystemChangeTransaction, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Pool.Query(ctx, `select id::text,invocation_id::text,run_id::text,step_id::text,tool_name,tool_version,status,
		arguments,snapshot,result,failure_code,failure_category,failure_summary,rollback_result,rollback_error,rollback_attempts,
		next_rollback_attempt_at,prepared_at,committed_at,rolled_back_at,updated_at
		from steward_system_change_transactions order by prepared_at desc limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardSystemChangeTransaction{}
	for rows.Next() {
		var item domain.StewardSystemChangeTransaction
		var arguments, snapshot, result, rollback []byte
		if err := rows.Scan(&item.ID, &item.InvocationID, &item.RunID, &item.StepID, &item.ToolName, &item.ToolVersion, &item.Status,
			&arguments, &snapshot, &result, &item.FailureCode, &item.FailureCategory, &item.FailureSummary, &rollback,
			&item.RollbackError, &item.RollbackAttempts, &item.NextRollbackAttemptAt, &item.PreparedAt, &item.CommittedAt, &item.RolledBackAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(arguments, &item.Arguments)
		_ = json.Unmarshal(snapshot, &item.Snapshot)
		_ = json.Unmarshal(result, &item.Result)
		_ = json.Unmarshal(rollback, &item.RollbackResult)
		items = append(items, item)
	}
	return items, rows.Err()
}
