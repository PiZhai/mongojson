package steward

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
)

const (
	maxObservationMediaBytes = 32 << 20
	heartbeatMergeWindow     = 5 * time.Minute
)

type ObservationEntityHint struct {
	Type               string `json:"type"`
	CanonicalKey       string `json:"canonical_key"`
	DisplayName        string `json:"display_name"`
	Summary            string `json:"summary"`
	RelationType       string `json:"relation_type,omitempty"`
	TargetType         string `json:"target_type,omitempty"`
	TargetCanonicalKey string `json:"target_canonical_key,omitempty"`
	TargetDisplayName  string `json:"target_display_name,omitempty"`
	Inferred           bool   `json:"inferred,omitempty"`
}

type ObservationBlobInput struct {
	MIMEType   string     `json:"mime_type"`
	DataBase64 string     `json:"data_base64"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type CreateObservationInput struct {
	Source          string                  `json:"source"`
	Type            string                  `json:"type"`
	Summary         string                  `json:"summary"`
	DataLevel       string                  `json:"data_level"`
	PermissionLevel string                  `json:"permission_level"`
	ContextKey      string                  `json:"context_key"`
	Fingerprint     string                  `json:"fingerprint"`
	Payload         map[string]any          `json:"payload"`
	Metadata        map[string]any          `json:"metadata"`
	EntityHints     []ObservationEntityHint `json:"entity_hints"`
	Blob            *ObservationBlobInput   `json:"blob,omitempty"`
	OccurredAt      *time.Time              `json:"occurred_at,omitempty"`
	EndedAt         *time.Time              `json:"ended_at,omitempty"`
	ExpiresAt       *time.Time              `json:"expires_at,omitempty"`
	SystemGenerated *bool                   `json:"system_generated,omitempty"`
	RetentionLocked *bool                   `json:"retention_locked,omitempty"`
}

type UpdateRetentionPolicyInput struct {
	TTLDays               *float64 `json:"ttl_days,omitempty"`
	QuarantineDays        *int     `json:"quarantine_days,omitempty"`
	AutoPurge             *bool    `json:"auto_purge,omitempty"`
	RequirePreview        *bool    `json:"require_preview,omitempty"`
	ProtectUserConfirmed  *bool    `json:"protect_user_confirmed,omitempty"`
	ProtectReferenced     *bool    `json:"protect_referenced,omitempty"`
	DeletionTombstoneDays *int     `json:"deletion_tombstone_days,omitempty"`
	Description           *string  `json:"description,omitempty"`
}

type UpdateInferenceInput struct {
	Status        *string `json:"status,omitempty"`
	Title         *string `json:"title,omitempty"`
	Summary       *string `json:"summary,omitempty"`
	UserConfirmed *bool   `json:"user_confirmed,omitempty"`
}

func (s *Service) ensureActivityDefaults(ctx context.Context, now time.Time) error {
	if err := s.ensureObservationPartitions(ctx, now); err != nil {
		return err
	}
	defaults := []domain.StewardRetentionPolicy{
		{SourcePattern: "*", DataKind: "clipboard", DataLevel: "*", TTLDays: 1.0 / 24.0, QuarantineDays: 0, AutoPurge: true, RequirePreview: false, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "剪贴板原始内容保留 1 小时"},
		{SourcePattern: "*", DataKind: "screenshot", DataLevel: "*", TTLDays: 1, QuarantineDays: 0, AutoPurge: true, RequirePreview: false, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "截图原始证据保留 24 小时"},
		{SourcePattern: "*", DataKind: "audio", DataLevel: "*", TTLDays: 1, QuarantineDays: 0, AutoPurge: true, RequirePreview: false, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "音频原始证据保留 24 小时"},
		{SourcePattern: "*", DataKind: "observation", DataLevel: "*", TTLDays: 30, QuarantineDays: 0, AutoPurge: true, RequirePreview: false, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "一般原始观察保留 30 天"},
		{SourcePattern: "*", DataKind: "inference", DataLevel: "*", TTLDays: 180, QuarantineDays: 30, AutoPurge: true, RequirePreview: false, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "低价值系统推断先隔离 30 天"},
		{SourcePattern: "*", DataKind: "timeline", DataLevel: "*", TTLDays: 365, QuarantineDays: 30, AutoPurge: false, RequirePreview: true, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "时间线默认保留 1 年，不自动删除"},
		{SourcePattern: "*", DataKind: "audit", DataLevel: "*", TTLDays: 365, QuarantineDays: 0, AutoPurge: false, RequirePreview: true, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "普通采集审计保留至少 1 年"},
		{SourcePattern: "security:*", DataKind: "audit", DataLevel: "*", TTLDays: 1095, QuarantineDays: 0, AutoPurge: false, RequirePreview: true, ProtectUserConfirmed: true, ProtectReferenced: true, DeletionTombstoneDays: 90, Description: "权限、外发、阻断和删除审计保留至少 3 年"},
	}
	for _, item := range defaults {
		_, err := s.db.Pool.Exec(ctx, `
			insert into steward_retention_policies (
				id, source_pattern, data_kind, data_level, ttl_days, quarantine_days, auto_purge,
				require_preview, protect_user_confirmed, protect_referenced, deletion_tombstone_days,
				description, created_at, updated_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
			on conflict (source_pattern, data_kind, data_level) do nothing
		`, uuid.NewString(), item.SourcePattern, item.DataKind, item.DataLevel, item.TTLDays,
			item.QuarantineDays, item.AutoPurge, item.RequirePreview, item.ProtectUserConfirmed,
			item.ProtectReferenced, item.DeletionTombstoneDays, item.Description, now)
		if err != nil {
			return fmt.Errorf("ensure retention policy %s: %w", item.DataKind, err)
		}
	}
	return nil
}

func (s *Service) ensureObservationPartitions(ctx context.Context, now time.Time) error {
	month := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for offset := -1; offset <= 2; offset++ {
		from := month.AddDate(0, offset, 0)
		to := from.AddDate(0, 1, 0)
		name := fmt.Sprintf("steward_observations_%04d_%02d", from.Year(), int(from.Month()))
		query := fmt.Sprintf(`create table if not exists %s partition of steward_observations for values from ('%s') to ('%s')`,
			name, from.Format(time.RFC3339), to.Format(time.RFC3339))
		if _, err := s.db.Pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("ensure observation partition %s: %w", name, err)
		}
	}
	return nil
}

func (s *Service) CreateObservation(ctx context.Context, input CreateObservationInput) (domain.StewardObservation, error) {
	input.Source = strings.TrimSpace(input.Source)
	input.Type = strings.TrimSpace(input.Type)
	input.Summary = strings.TrimSpace(input.Summary)
	input.ContextKey = strings.TrimSpace(input.ContextKey)
	input.DataLevel = strings.ToUpper(defaultString(strings.TrimSpace(input.DataLevel), DataD2))
	input.PermissionLevel = defaultString(strings.TrimSpace(input.PermissionLevel), PermissionA1)
	if input.Source == "" || input.Type == "" {
		return domain.StewardObservation{}, fmt.Errorf("source and type are required")
	}
	if !validDataLevel(input.DataLevel) {
		return domain.StewardObservation{}, fmt.Errorf("invalid data level %q", input.DataLevel)
	}
	classifiedLevel, credentialCategory := ClassifyObservationDataLevel(input)
	if classifiedLevel == DataD5 {
		input.DataLevel = DataD5
		if input.Metadata == nil {
			input.Metadata = map[string]any{}
		}
		input.Metadata["classification"] = credentialCategory
	}
	systemGenerated := defaultBool(input.SystemGenerated, true)
	dataPolicy, err := s.ResolveDataPolicy(ctx, input.DataLevel, input.Source)
	if err != nil || !dataPolicyAllowsCollection(dataPolicy, systemGenerated) {
		reason := "collection policy is not enabled"
		if err != nil {
			reason = err.Error()
		}
		s.recordDataPolicyBlock(ctx, input, reason)
		if input.DataLevel == DataD5 {
			return domain.StewardObservation{}, fmt.Errorf("%w: %s", ErrCredentialDataBlocked, reason)
		}
		return domain.StewardObservation{}, fmt.Errorf("%w: %s", ErrDataPolicyDenied, reason)
	}
	input, piiDetected, err := s.applyPresidioProtection(ctx, input)
	if err != nil {
		return domain.StewardObservation{}, err
	}

	now := time.Now().UTC()
	occurredAt := now
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}
	if occurredAt.After(now.Add(5 * time.Minute)) {
		return domain.StewardObservation{}, fmt.Errorf("occurred_at cannot be more than 5 minutes in the future")
	}
	endedAt := input.EndedAt
	if endedAt != nil {
		value := endedAt.UTC()
		if value.Before(occurredAt) {
			return domain.StewardObservation{}, fmt.Errorf("ended_at cannot be before occurred_at")
		}
		endedAt = &value
	}
	expiresAt, err := s.resolveObservationExpiry(ctx, input.Source, input.Type, occurredAt, input.ExpiresAt)
	if err != nil {
		return domain.StewardObservation{}, err
	}
	fingerprint := strings.TrimSpace(input.Fingerprint)
	if fingerprint == "" {
		hash := sha256.Sum256([]byte(input.Source + "\x00" + input.Type + "\x00" + input.ContextKey + "\x00" + input.Summary))
		fingerprint = hex.EncodeToString(hash[:])
	}
	if input.Blob == nil {
		if existing, found, err := s.mergeObservationHeartbeat(ctx, input, fingerprint, occurredAt, endedAt, expiresAt); err != nil {
			return domain.StewardObservation{}, err
		} else if found {
			return existing, nil
		}
	}

	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	} else {
		cloned := make(map[string]any, len(payload)+2)
		for key, value := range payload {
			cloned[key] = value
		}
		payload = cloned
	}
	if dataLevelRank(input.DataLevel) >= dataLevelRank(DataD4) {
		if input.Summary != "" {
			payload["_steward_original_summary"] = input.Summary
		}
		if input.ContextKey != "" {
			payload["_steward_original_context_key"] = input.ContextKey
		}
		input.Summary = input.DataLevel + " encrypted observation"
		input.ContextKey = "sensitive:" + fingerprint[:16]
	}
	payloadEncrypted := false
	if (dataPolicy.RequireEncryption || dataLevelRank(input.DataLevel) >= dataLevelRank(DataD4) || piiDetected) && len(payload) > 0 {
		cipherConfig, enabled, err := localPayloadCipherFromEnv()
		if err != nil {
			return domain.StewardObservation{}, err
		}
		if !enabled {
			return domain.StewardObservation{}, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY is required for D4-D6 observations")
		}
		payload, err = encryptPayloadEnvelope(cipherConfig, observationEncryptionAAD(input.Source, fingerprint, occurredAt), payload, SyncEncryptionScopeLocalAtRest)
		if err != nil {
			return domain.StewardObservation{}, err
		}
		payloadEncrypted = true
	}
	metadata := sanitizeObservationMetadata(input.Metadata)
	observationID := uuid.NewString()
	var media encryptedBlobResult
	var mediaMIME string
	if input.Blob != nil {
		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(input.Blob.DataBase64))
		if err != nil {
			return domain.StewardObservation{}, fmt.Errorf("invalid media base64: %w", err)
		}
		if len(content) == 0 || len(content) > maxObservationMediaBytes {
			return domain.StewardObservation{}, fmt.Errorf("media must be between 1 byte and 32 MiB")
		}
		mediaMIME = defaultString(strings.TrimSpace(input.Blob.MIMEType), "application/octet-stream")
		media, err = s.writeEncryptedBlob(observationID, content)
		if err != nil {
			return domain.StewardObservation{}, err
		}
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		if media.Path != "" {
			_ = os.Remove(media.Path)
		}
		return domain.StewardObservation{}, err
	}
	defer tx.Rollback(ctx)
	retentionLocked := defaultBool(input.RetentionLocked, false)
	_, err = tx.Exec(ctx, `
		insert into steward_observations (
			id, source, type, summary, data_level, permission_level, device_id, context_key,
			fingerprint, payload, payload_encrypted, metadata, status, system_generated,
			retention_locked, duplicate_count, occurred_at, ended_at, expires_at, created_at
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'active',$13,$14,1,$15,$16,$17,$18)
	`, observationID, input.Source, input.Type, input.Summary, input.DataLevel, input.PermissionLevel,
		s.agentIDValue(), input.ContextKey, fingerprint, payload, payloadEncrypted, metadata,
		systemGenerated, retentionLocked, occurredAt, endedAt, expiresAt, now)
	if err != nil {
		if media.Path != "" {
			_ = os.Remove(media.Path)
		}
		return domain.StewardObservation{}, fmt.Errorf("create observation: %w", err)
	}
	if media.Path != "" {
		blobExpiry := expiresAt
		if input.Blob.ExpiresAt != nil && (blobExpiry == nil || input.Blob.ExpiresAt.Before(*blobExpiry)) {
			value := input.Blob.ExpiresAt.UTC()
			blobExpiry = &value
		}
		_, err = tx.Exec(ctx, `
			insert into steward_encrypted_blobs (
				id, observation_id, observation_time, storage_path, mime_type, size_bytes,
				key_id, ciphertext_hash, expires_at, created_at
			) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		`, uuid.NewString(), observationID, occurredAt, media.Path, mediaMIME, media.PlaintextSize,
			media.KeyID, media.CiphertextHash, blobExpiry, now)
		if err != nil {
			_ = os.Remove(media.Path)
			return domain.StewardObservation{}, fmt.Errorf("record encrypted media: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if media.Path != "" {
			_ = os.Remove(media.Path)
		}
		return domain.StewardObservation{}, err
	}
	s.storeObservationEmbedding(ctx, observationID, occurredAt, input.Summary)

	if err := s.linkObservationEntities(ctx, observationID, occurredAt, input.DataLevel, input.EntityHints); err != nil {
		return domain.StewardObservation{}, fmt.Errorf("link observation entities: %w", err)
	}
	confirmed, syncable := false, false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "collector", Action: "activity.observation.create", TargetType: "observation", TargetID: &observationID,
		Source: input.Source, PermissionLevel: input.PermissionLevel, DataLevel: input.DataLevel,
		InputSummary: "redacted observation envelope", OutputSummary: input.Type + " observation stored",
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK,
	})
	if dataPolicy.ModelMode == PolicyModeAuto {
		if err := s.enqueueModelDispatch(ctx, observationID, occurredAt, input.Source, input.DataLevel, dataPolicy.ModelContentMode); err != nil {
			return domain.StewardObservation{}, fmt.Errorf("enqueue observation model dispatch: %w", err)
		}
	}
	return s.getObservation(ctx, observationID, occurredAt)
}

func observationEncryptionAAD(source, fingerprint string, occurredAt time.Time) string {
	return "observation|" + source + "|" + fingerprint + "|" + occurredAt.UTC().Format(time.RFC3339Nano)
}

func sanitizeObservationMetadata(input map[string]any) map[string]any {
	result := map[string]any{}
	allowed := map[string]bool{
		"adapter": true, "source_version": true, "schema_version": true, "bucket_id": true,
		"duration_seconds": true, "redacted": true, "capture_profile": true, "classification": true,
	}
	for key, value := range input {
		if allowed[key] {
			result[key] = value
		}
	}
	return result
}

func (s *Service) recordDataPolicyBlock(ctx context.Context, input CreateObservationInput, reason string) {
	confirmed, syncable := false, false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "policy", Action: "policy.data.collection.block", TargetType: "observation", Source: "security:data-policy",
		PermissionLevel: input.PermissionLevel, DataLevel: input.DataLevel,
		InputSummary: input.Source + ":" + input.Type, OutputSummary: "observation rejected before persistence", Reason: reason,
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultBlocked,
	})
}

func (s *Service) recordCredentialBlock(ctx context.Context, category, source, observationType string) {
	confirmed, syncable := false, false
	_, _ = s.recordAudit(ctx, AuditInput{
		Actor: "policy", Action: "security.d5.block", TargetType: "observation", Source: "security:d5-gate",
		PermissionLevel: PermissionA0, DataLevel: DataD0, InputSummary: "credential category: " + category,
		OutputSummary: "blocked before buffer and storage", Reason: source + ":" + observationType,
		UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultBlocked,
	})
}

func (s *Service) mergeObservationHeartbeat(ctx context.Context, input CreateObservationInput, fingerprint string, occurredAt time.Time, endedAt, expiresAt *time.Time) (domain.StewardObservation, bool, error) {
	var id string
	var originalTime time.Time
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, occurred_at
		from steward_observations
		where source = $1 and type = $2 and context_key = $3 and fingerprint = $4
		  and status = 'active' and occurred_at >= $5
		order by occurred_at desc limit 1
	`, input.Source, input.Type, input.ContextKey, fingerprint, occurredAt.Add(-heartbeatMergeWindow)).Scan(&id, &originalTime)
	if err == pgx.ErrNoRows {
		return domain.StewardObservation{}, false, nil
	}
	if err != nil {
		return domain.StewardObservation{}, false, err
	}
	end := occurredAt
	if endedAt != nil && endedAt.After(end) {
		end = endedAt.UTC()
	}
	_, err = s.db.Pool.Exec(ctx, `
		update steward_observations
		set ended_at = greatest(coalesce(ended_at, occurred_at), $1), duplicate_count = duplicate_count + 1,
		    expires_at = case when expires_at is null then $2 when $2::timestamptz is null then expires_at else greatest(expires_at, $2) end
		where id = $3 and occurred_at = $4
	`, end, expiresAt, id, originalTime)
	if err != nil {
		return domain.StewardObservation{}, false, err
	}
	record, err := s.getObservation(ctx, id, originalTime)
	return record, true, err
}

func (s *Service) resolveObservationExpiry(ctx context.Context, source, kind string, occurredAt time.Time, requested *time.Time) (*time.Time, error) {
	var ttl float64
	err := s.db.Pool.QueryRow(ctx, `
		select ttl_days
		from steward_retention_policies
		where data_kind in ($1, 'observation')
		  and ($2 like replace(source_pattern, '*', '%'))
		order by case when data_kind = $1 then 0 else 1 end,
		         case when source_pattern = '*' then 1 else 0 end
		limit 1
	`, kind, source).Scan(&ttl)
	if err == pgx.ErrNoRows {
		ttl = 30
	} else if err != nil {
		return nil, fmt.Errorf("resolve observation retention: %w", err)
	}
	value := occurredAt.Add(time.Duration(ttl * float64(24*time.Hour)))
	if requested != nil && requested.Before(value) {
		value = requested.UTC()
	}
	return &value, nil
}

func (s *Service) ListObservations(ctx context.Context, limit int) ([]domain.StewardObservation, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, observationSelect+` order by o.occurred_at desc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list observations: %w", err)
	}
	defer rows.Close()
	items := []domain.StewardObservation{}
	for rows.Next() {
		item, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const observationSelect = `
	select o.id::text, o.source, o.type, o.summary, o.data_level, o.permission_level,
	       o.device_id, o.context_key, o.fingerprint, o.payload_encrypted,
	       (b.id is not null), coalesce(b.mime_type, ''), coalesce(b.size_bytes, 0),
	       o.status, o.system_generated, o.retention_locked, o.duplicate_count,
	       o.session_id::text, o.occurred_at, o.ended_at, o.expires_at, o.created_at, o.metadata
	from steward_observations o
	left join lateral (
		select id, mime_type, size_bytes from steward_encrypted_blobs
		where observation_id = o.id and observation_time = o.occurred_at limit 1
	) b on true
`

func (s *Service) getObservation(ctx context.Context, id string, occurredAt time.Time) (domain.StewardObservation, error) {
	item, err := scanObservation(s.db.Pool.QueryRow(ctx, observationSelect+` where o.id = $1 and o.occurred_at = $2`, id, occurredAt))
	if err != nil {
		return domain.StewardObservation{}, fmt.Errorf("get observation: %w", err)
	}
	return item, nil
}

func scanObservation(row scanner) (domain.StewardObservation, error) {
	var item domain.StewardObservation
	if err := row.Scan(
		&item.ID, &item.Source, &item.Type, &item.Summary, &item.DataLevel, &item.PermissionLevel,
		&item.DeviceID, &item.ContextKey, &item.Fingerprint, &item.PayloadEncrypted,
		&item.HasMedia, &item.MediaType, &item.MediaSizeBytes, &item.Status, &item.SystemGenerated,
		&item.RetentionLocked, &item.DuplicateCount, &item.SessionID, &item.OccurredAt, &item.EndedAt,
		&item.ExpiresAt, &item.CreatedAt, &item.Metadata,
	); err != nil {
		return domain.StewardObservation{}, err
	}
	return item, nil
}

func (s *Service) ListActivitySessions(ctx context.Context, limit int) ([]domain.StewardActivitySession, error) {
	limit = normalizeLimit(limit, 100, 500)
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, type, title, summary, source, context_key, device_id, data_level,
		       status, observation_count, confidence, value_score, started_at, ended_at,
		       timeline_id::text, created_at, updated_at
		from steward_activity_sessions order by started_at desc limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardActivitySession{}
	for rows.Next() {
		var item domain.StewardActivitySession
		if err := rows.Scan(&item.ID, &item.Type, &item.Title, &item.Summary, &item.Source,
			&item.ContextKey, &item.DeviceID, &item.DataLevel, &item.Status, &item.ObservationCount,
			&item.Confidence, &item.ValueScore, &item.StartedAt, &item.EndedAt, &item.TimelineID,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListRetentionPolicies(ctx context.Context) ([]domain.StewardRetentionPolicy, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, source_pattern, data_kind, data_level, ttl_days, quarantine_days,
		       auto_purge, require_preview, protect_user_confirmed, protect_referenced,
		       deletion_tombstone_days, description, created_at, updated_at
		from steward_retention_policies order by data_kind, source_pattern, data_level
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.StewardRetentionPolicy{}
	for rows.Next() {
		var item domain.StewardRetentionPolicy
		if err := rows.Scan(&item.ID, &item.SourcePattern, &item.DataKind, &item.DataLevel,
			&item.TTLDays, &item.QuarantineDays, &item.AutoPurge, &item.RequirePreview,
			&item.ProtectUserConfirmed, &item.ProtectReferenced, &item.DeletionTombstoneDays,
			&item.Description, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) UpdateRetentionPolicy(ctx context.Context, id string, input UpdateRetentionPolicyInput) (domain.StewardRetentionPolicy, error) {
	if input.TTLDays != nil && (*input.TTLDays <= 0 || *input.TTLDays > 3650) {
		return domain.StewardRetentionPolicy{}, fmt.Errorf("ttl_days must be between 0 and 3650")
	}
	if input.QuarantineDays != nil && (*input.QuarantineDays < 0 || *input.QuarantineDays > 365) {
		return domain.StewardRetentionPolicy{}, fmt.Errorf("quarantine_days must be between 0 and 365")
	}
	if input.DeletionTombstoneDays != nil && (*input.DeletionTombstoneDays < 90 || *input.DeletionTombstoneDays > 1095) {
		return domain.StewardRetentionPolicy{}, fmt.Errorf("deletion_tombstone_days must be between 90 and 1095")
	}
	var item domain.StewardRetentionPolicy
	err := s.db.Pool.QueryRow(ctx, `
		update steward_retention_policies set
			ttl_days = coalesce($2, ttl_days), quarantine_days = coalesce($3, quarantine_days),
			auto_purge = coalesce($4, auto_purge), require_preview = coalesce($5, require_preview),
			protect_user_confirmed = coalesce($6, protect_user_confirmed),
			protect_referenced = coalesce($7, protect_referenced),
			deletion_tombstone_days = coalesce($8, deletion_tombstone_days),
			description = coalesce($9, description), updated_at = now()
		where id = $1
		returning id::text, source_pattern, data_kind, data_level, ttl_days, quarantine_days,
		          auto_purge, require_preview, protect_user_confirmed, protect_referenced,
		          deletion_tombstone_days, description, created_at, updated_at
	`, id, input.TTLDays, input.QuarantineDays, input.AutoPurge, input.RequirePreview,
		input.ProtectUserConfirmed, input.ProtectReferenced, input.DeletionTombstoneDays,
		input.Description).Scan(&item.ID, &item.SourcePattern, &item.DataKind, &item.DataLevel,
		&item.TTLDays, &item.QuarantineDays, &item.AutoPurge, &item.RequirePreview,
		&item.ProtectUserConfirmed, &item.ProtectReferenced, &item.DeletionTombstoneDays,
		&item.Description, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return domain.StewardRetentionPolicy{}, fmt.Errorf("update retention policy: %w", err)
	}
	confirmed, syncable := true, false
	_, _ = s.recordAudit(ctx, AuditInput{Actor: "user", Action: "lifecycle.retention.update", TargetType: "retention_policy", TargetID: &id,
		Source: "lifecycle", PermissionLevel: PermissionA3, DataLevel: DataD0, InputSummary: "retention policy changed",
		OutputSummary: item.DataKind + " retention updated", UserConfirmed: &confirmed, Syncable: &syncable, ResultStatus: ResultOK})
	return item, nil
}

func normalizeLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}

func marshalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
