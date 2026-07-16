package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	runtimeEvidenceStateInline      = "inline"
	runtimeEvidenceStateEncrypted   = "encrypted"
	runtimeEvidenceStateSummaryOnly = "summary_only"
	runtimeEvidenceScope            = "runtime-evidence"
)

type governedRuntimePayload struct {
	Stored      map[string]any
	Preview     map[string]any
	State       string
	Available   bool
	SizeBytes   int64
	SHA256      string
	Redacted    bool
	DataLevel   string
	ContentType string
}

func (s *Service) storeRuntimeEvidence(ctx context.Context, tx pgx.Tx, evidenceID, runID, stepID, kind, summary, dataLevel string, payload map[string]any) (governedRuntimePayload, error) {
	governed, err := s.governRuntimePayload(evidenceID, runID, stepID, dataLevel, payload)
	if err != nil {
		return governedRuntimePayload{}, err
	}
	payloadJSON, err := json.Marshal(governed.Stored)
	if err != nil {
		return governedRuntimePayload{}, err
	}
	if _, err := tx.Exec(ctx, `
		insert into steward_evidence_artifacts (
			id, run_id, step_id, kind, summary, payload, data_level, content_type,
			payload_state, payload_available, size_bytes, sha256, redacted, created_at
		) values ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12,$13,now())
	`, evidenceID, runID, stepID, defaultString(strings.TrimSpace(kind), "tool_output"),
		strings.TrimSpace(summary), string(payloadJSON), governed.DataLevel, governed.ContentType,
		governed.State, governed.Available, governed.SizeBytes, governed.SHA256, governed.Redacted); err != nil {
		return governedRuntimePayload{}, fmt.Errorf("store governed execution evidence: %w", err)
	}
	return governed, nil
}

func (s *Service) governRuntimePayload(evidenceID, runID, stepID, dataLevel string, payload map[string]any) (governedRuntimePayload, error) {
	redactedPayload, redacted := redactRuntimePayloadMap(payload)
	plain, err := json.Marshal(redactedPayload)
	if err != nil {
		return governedRuntimePayload{}, fmt.Errorf("marshal governed runtime payload: %w", err)
	}
	digest := sha256.Sum256(plain)
	result := governedRuntimePayload{
		Stored: redactedPayload,
		State:  runtimeEvidenceStateInline, Available: true,
		SizeBytes: int64(len(plain)), SHA256: hex.EncodeToString(digest[:]), Redacted: redacted,
		DataLevel:   defaultString(strings.ToUpper(strings.TrimSpace(dataLevel)), DataD0),
		ContentType: "application/json",
	}
	result.Preview = runtimePayloadPreview(redactedPayload, result.DataLevel)
	limit := s.runtimeEvidenceMaxBytes
	if limit <= 0 {
		limit = 64 << 10
	}
	if len(plain) > limit {
		result.Stored = map[string]any{}
		result.State = runtimeEvidenceStateSummaryOnly
		result.Available = false
		return result, nil
	}
	if dataLevelRank(result.DataLevel) >= dataLevelRank(DataD4) {
		cipher, enabled, err := localPayloadCipherFromEnv()
		if err != nil {
			return governedRuntimePayload{}, err
		}
		if !enabled {
			result.Stored = map[string]any{}
			result.State = runtimeEvidenceStateSummaryOnly
			result.Available = false
			return result, nil
		}
		encrypted, err := encryptPayloadEnvelope(cipher, runtimeEvidenceAAD(evidenceID, runID, stepID), redactedPayload, runtimeEvidenceScope)
		if err != nil {
			return governedRuntimePayload{}, err
		}
		result.Stored = encrypted
		result.State = runtimeEvidenceStateEncrypted
	}
	return result, nil
}

func (s *Service) GetEvidenceArtifact(ctx context.Context, runID, evidenceID string) (domain.StewardEvidenceArtifact, error) {
	if err := s.runtimeEnabled(); err != nil {
		return domain.StewardEvidenceArtifact{}, err
	}
	if _, err := uuid.Parse(strings.TrimSpace(runID)); err != nil {
		return domain.StewardEvidenceArtifact{}, ErrAgentRunNotFound
	}
	if _, err := uuid.Parse(strings.TrimSpace(evidenceID)); err != nil {
		return domain.StewardEvidenceArtifact{}, ErrAgentRunNotFound
	}
	var item domain.StewardEvidenceArtifact
	var payloadJSON []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, run_id::text, step_id::text, kind, summary, data_level,
		       content_type, payload_state, payload_available, size_bytes, sha256,
		       redacted, payload, created_at
		from steward_evidence_artifacts where id = $1 and run_id = $2
	`, evidenceID, runID).Scan(&item.ID, &item.RunID, &item.StepID, &item.Kind, &item.Summary,
		&item.DataLevel, &item.ContentType, &item.PayloadState, &item.PayloadAvailable,
		&item.SizeBytes, &item.SHA256, &item.Redacted, &payloadJSON, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StewardEvidenceArtifact{}, ErrAgentRunNotFound
	}
	if err != nil {
		return domain.StewardEvidenceArtifact{}, fmt.Errorf("get execution evidence: %w", err)
	}
	if !item.PayloadAvailable || item.PayloadState == runtimeEvidenceStateSummaryOnly {
		return item, nil
	}
	payload := decodeRuntimeMap(payloadJSON)
	if item.PayloadState == runtimeEvidenceStateEncrypted {
		keyring, err := localPayloadKeyringFromEnv()
		if err != nil {
			return domain.StewardEvidenceArtifact{}, err
		}
		payload, err = decryptPayloadEnvelope(keyring, runtimeEvidenceAAD(item.ID, item.RunID, item.StepID), payload, "runtime execution evidence")
		if err != nil {
			return domain.StewardEvidenceArtifact{}, err
		}
	}
	item.Payload = payload
	return item, nil
}

func runtimeEvidenceAAD(evidenceID, runID, stepID string) string {
	return strings.Join([]string{runtimeEvidenceScope, evidenceID, runID, stepID}, ":")
}

func redactRuntimePayloadMap(payload map[string]any) (map[string]any, bool) {
	if payload == nil {
		return map[string]any{}, false
	}
	redacted := false
	value, _ := redactRuntimePayloadValue(payload, &redacted).(map[string]any)
	return value, redacted
}

func redactRuntimePayloadValue(value any, redacted *bool) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if runtimeSecretField(key) {
				out[key] = "[REDACTED]"
				*redacted = true
				continue
			}
			out[key] = redactRuntimePayloadValue(item, redacted)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index := range typed {
			out[index] = redactRuntimePayloadValue(typed[index], redacted)
		}
		return out
	case string:
		clean := redactCredentialText(typed)
		if clean != typed {
			*redacted = true
		}
		return clean
	default:
		return typed
	}
}

func runtimeSecretField(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(key)))
	for _, marker := range []string{"password", "passwd", "pwd", "secret", "api_key", "access_token", "refresh_token", "authorization", "cookie", "private_key"} {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}

func runtimePayloadPreview(payload map[string]any, dataLevel string) map[string]any {
	if dataLevelRank(dataLevel) >= dataLevelRank(DataD4) {
		return map[string]any{}
	}
	preview := make(map[string]any, len(payload))
	for key, value := range payload {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if runtimeSecretField(key) || normalized == "content" || normalized == "stdout" || normalized == "stderr" || normalized == "body" || normalized == "raw" || normalized == "html" {
			preview[key] = "[OMITTED: load governed evidence to inspect]"
			continue
		}
		switch typed := value.(type) {
		case string:
			if len([]rune(typed)) > 500 {
				preview[key] = string([]rune(typed)[:500]) + "…"
			} else {
				preview[key] = typed
			}
		case []any:
			if len(typed) > 20 {
				preview[key] = append(append([]any{}, typed[:20]...), fmt.Sprintf("[%d more items omitted]", len(typed)-20))
			} else {
				preview[key] = typed
			}
		default:
			preview[key] = typed
		}
	}
	return preview
}
