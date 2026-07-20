package stewardcompanion

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"mongojson/backend/internal/service/steward"
)

const DefaultMaxPending = 100_000

const (
	EnvelopeObservation          = "observation"
	EnvelopeNotificationFeedback = "notification_feedback"
	captureControlStateKey       = "authenticated-capture-control-v1"
)

var ErrBufferFull = errors.New("companion observation buffer reached its configured limit")
var ErrNoCachedCaptureControl = errors.New("no authenticated capture control is cached")

type Buffer struct {
	db                *sql.DB
	aead              cipher.AEAD
	maxPending        int
	allowedDataLevels map[string]bool
}

type Options struct {
	Path              string
	Key               []byte
	MaxPending        int
	AllowedDataLevels []string
}

type FlushResult struct {
	Submitted int    `json:"submitted"`
	Failed    int    `json:"failed"`
	Pending   int    `json:"pending"`
	LastError string `json:"last_error,omitempty"`
}

type bufferedObservation struct {
	ID          string
	Kind        string
	Ciphertext  []byte
	Nonce       []byte
	EventKey    string
	Revision    int64
	PayloadHash string
}

type NotificationFeedbackEnvelope struct {
	CallbackToken string         `json:"callback_token"`
	Action        string         `json:"action"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func (e NotificationFeedbackEnvelope) EventKey() string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(e.CallbackToken) + "\x00" + strings.ToLower(strings.TrimSpace(e.Action))))
	return "notification-feedback:" + hex.EncodeToString(hash[:])
}

func Open(ctx context.Context, options Options) (*Buffer, error) {
	if len(options.Key) != 32 {
		return nil, fmt.Errorf("companion encryption key must be 32 bytes")
	}
	path := filepath.Clean(strings.TrimSpace(options.Path))
	if path == "." || path == "" {
		return nil, fmt.Errorf("companion SQLite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create companion data directory: %w", err)
	}
	block, err := aes.NewCipher(options.Key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	allowedDataLevels, err := normalizeAllowedDataLevels(options.AllowedDataLevels)
	if err != nil {
		db.Close()
		return nil, err
	}
	buffer := &Buffer{db: db, aead: aead, maxPending: options.MaxPending, allowedDataLevels: allowedDataLevels}
	if buffer.maxPending <= 0 {
		buffer.maxPending = DefaultMaxPending
	}
	statements := []string{
		`pragma journal_mode=WAL;`,
		`pragma synchronous=NORMAL;`,
		`pragma secure_delete=ON;`,
		`pragma busy_timeout=5000;`,
		`create table if not exists pending_observations (
			id text primary key,
			ciphertext blob not null,
			nonce blob not null,
			event_key text not null default '',
			revision integer not null default 1,
			payload_hash text not null default '',
			attempts integer not null default 0,
			last_error text not null default '',
			created_at text not null,
			updated_at text not null default ''
		);`,
		`alter table pending_observations add column event_key text not null default '';`,
		`alter table pending_observations add column revision integer not null default 1;`,
		`alter table pending_observations add column payload_hash text not null default '';`,
		`alter table pending_observations add column updated_at text not null default '';`,
		`update pending_observations set event_key=id where event_key='';`,
		`update pending_observations set updated_at=created_at where updated_at='';`,
		`create index if not exists idx_pending_observations_created_at on pending_observations(created_at);`,
		`create unique index if not exists idx_pending_observations_event_key on pending_observations(event_key);`,
		`create table if not exists pending_envelopes (
			id text primary key,
			kind text not null,
			event_key text not null,
			revision integer not null default 1,
			payload_hash text not null,
			ciphertext blob not null,
			nonce blob not null,
			attempts integer not null default 0,
			last_error text not null default '',
			created_at text not null,
			updated_at text not null
		);`,
		`create unique index if not exists idx_pending_envelopes_event_key on pending_envelopes(kind,event_key);`,
		`create index if not exists idx_pending_envelopes_created_at on pending_envelopes(created_at);`,
		`create table if not exists companion_state (
			state_key text primary key,
			ciphertext blob not null,
			nonce blob not null,
			updated_at text not null
		);`,
		`insert into pending_envelopes (
			id,kind,event_key,revision,payload_hash,ciphertext,nonce,attempts,last_error,created_at,updated_at
		) select id,'observation',event_key,revision,payload_hash,ciphertext,nonce,attempts,last_error,created_at,updated_at
		  from pending_observations where true on conflict(id) do nothing;`,
		`delete from pending_observations;`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			// SQLite has no portable ADD COLUMN IF NOT EXISTS. Existing companion
			// databases therefore legitimately report duplicate-column errors.
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(statement)), "alter table") &&
				strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				continue
			}
			db.Close()
			return nil, fmt.Errorf("initialize companion buffer: %w", err)
		}
	}
	_ = os.Chmod(path, 0o600)
	return buffer, nil
}

type cachedCaptureControlEnvelope struct {
	Version       int    `json:"version"`
	Binding       string `json:"binding"`
	IntervalNanos int64  `json:"interval_nanos"`
	CachedCaptureControl
}

// SaveAuthenticatedCaptureControl atomically replaces the encrypted control
// snapshot. Callers must invoke it only after every control endpoint has
// returned successfully through an authenticated management client.
func (b *Buffer) SaveAuthenticatedCaptureControl(ctx context.Context, binding string, control CaptureControl, authenticatedAt time.Time) error {
	if b == nil || b.db == nil || b.aead == nil {
		return fmt.Errorf("companion buffer is not open")
	}
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return fmt.Errorf("authenticated capture-control cache binding is required")
	}
	if authenticatedAt.IsZero() {
		return fmt.Errorf("capture-control authentication time is required")
	}
	if control.Interval <= 0 {
		control.Interval = DefaultCaptureInterval
	}
	control.Timezone = strings.TrimSpace(control.Timezone)
	payload, err := json.Marshal(cachedCaptureControlEnvelope{
		Version: 1, Binding: binding, IntervalNanos: int64(control.Interval),
		CachedCaptureControl: CachedCaptureControl{Control: control, AuthenticatedAt: authenticatedAt.UTC()},
	})
	if err != nil {
		return fmt.Errorf("encode authenticated capture control: %w", err)
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("create capture-control nonce: %w", err)
	}
	ciphertext := b.aead.Seal(nil, nonce, payload, []byte(captureControlStateKey))
	_, err = b.db.ExecContext(ctx, `
		insert into companion_state (state_key,ciphertext,nonce,updated_at) values (?,?,?,?)
		on conflict(state_key) do update set
			ciphertext=excluded.ciphertext,
			nonce=excluded.nonce,
			updated_at=excluded.updated_at
	`, captureControlStateKey, ciphertext, nonce, authenticatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("persist authenticated capture control: %w", err)
	}
	return nil
}

// LoadAuthenticatedCaptureControl returns a cache only when it was written for
// the current authenticated endpoint and credential identity.
func (b *Buffer) LoadAuthenticatedCaptureControl(ctx context.Context, binding string) (CachedCaptureControl, error) {
	if b == nil || b.db == nil || b.aead == nil {
		return CachedCaptureControl{}, fmt.Errorf("companion buffer is not open")
	}
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return CachedCaptureControl{}, ErrNoCachedCaptureControl
	}
	var ciphertext, nonce []byte
	err := b.db.QueryRowContext(ctx, `select ciphertext,nonce from companion_state where state_key=?`, captureControlStateKey).
		Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return CachedCaptureControl{}, ErrNoCachedCaptureControl
	}
	if err != nil {
		return CachedCaptureControl{}, fmt.Errorf("load authenticated capture control: %w", err)
	}
	if len(nonce) != b.aead.NonceSize() {
		return CachedCaptureControl{}, fmt.Errorf("decrypt authenticated capture control: invalid nonce size")
	}
	payload, err := b.aead.Open(nil, nonce, ciphertext, []byte(captureControlStateKey))
	if err != nil {
		return CachedCaptureControl{}, fmt.Errorf("decrypt authenticated capture control: %w", err)
	}
	var cached cachedCaptureControlEnvelope
	if err := json.Unmarshal(payload, &cached); err != nil {
		return CachedCaptureControl{}, fmt.Errorf("decode authenticated capture control: %w", err)
	}
	if cached.Version != 1 || cached.Binding != binding || cached.AuthenticatedAt.IsZero() {
		return CachedCaptureControl{}, ErrNoCachedCaptureControl
	}
	cached.Control.Interval = time.Duration(cached.IntervalNanos)
	if cached.Control.Interval <= 0 {
		cached.Control.Interval = DefaultCaptureInterval
	}
	cached.Control.Timezone = strings.TrimSpace(cached.Control.Timezone)
	return cached.CachedCaptureControl, nil
}

func (b *Buffer) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

func (b *Buffer) Enqueue(ctx context.Context, input steward.CreateObservationInput) (string, error) {
	// D0-D6 are retained only for older clients and stored records. They must
	// not decide whether genuine desktop activity is collected. Explicit
	// credential plaintext is instead removed field by field before the
	// encrypted outbox sees the envelope.
	input, _ = steward.SanitizeObservationSecrets(input)
	if err := steward.ValidateObservationBeforePersistence(input); err != nil {
		return "", err
	}
	input.DataLevel = normalizeObservationDataLevel(input.DataLevel)
	eventKey := strings.TrimSpace(input.SourceEventKey)
	if eventKey == "" {
		eventKey = uuid.NewString()
		input.SourceEventKey = eventKey
	}
	revision := input.SourceRevision
	if revision <= 0 {
		revision = 1
		input.SourceRevision = revision
	}
	plain, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	return b.enqueueEnvelope(ctx, EnvelopeObservation, eventKey, revision, plain)
}

// EnqueueEnvelope stores a generic authenticated Companion callback payload.
// Notification activation can therefore survive a main-service restart using
// the same encrypted, revision-aware outbox as activity capture.
func (b *Buffer) EnqueueEnvelope(ctx context.Context, kind, eventKey string, revision int64, payload any) (string, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return b.enqueueEnvelope(ctx, kind, eventKey, revision, plain)
}

func (b *Buffer) enqueueEnvelope(ctx context.Context, kind, eventKey string, revision int64, plain []byte) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case EnvelopeObservation, EnvelopeNotificationFeedback:
	default:
		return "", fmt.Errorf("unsupported companion envelope kind %q", kind)
	}
	eventKey = strings.TrimSpace(eventKey)
	if eventKey == "" {
		return "", fmt.Errorf("companion envelope event key is required")
	}
	if revision <= 0 {
		revision = 1
	}
	hash := sha256.Sum256(plain)
	payloadHash := hex.EncodeToString(hash[:])
	id := uuid.NewString()
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var existingID string
	var existingRevision int64
	var existingHash string
	err = tx.QueryRowContext(ctx, `select id,revision,payload_hash from pending_envelopes where kind=? and event_key=?`, kind, eventKey).
		Scan(&existingID, &existingRevision, &existingHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil && (existingRevision > revision || (existingRevision == revision && existingHash == payloadHash)) {
		return existingID, tx.Commit()
	}
	if err == nil && existingRevision == revision && existingHash != payloadHash {
		return "", fmt.Errorf("%s event %q revision %d conflicts with a different payload", kind, eventKey, revision)
	}
	if existingID != "" {
		id = existingID
	}
	ciphertext := b.aead.Seal(nil, nonce, plain, []byte(id))
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from pending_envelopes`).Scan(&count); err != nil {
		return "", err
	}
	if existingID == "" && count >= b.maxPending {
		return "", ErrBufferFull
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
		insert into pending_envelopes (id,kind,ciphertext,nonce,event_key,revision,payload_hash,created_at,updated_at)
		values (?,?,?,?,?,?,?,?,?)
		on conflict(kind,event_key) do update set
			ciphertext=excluded.ciphertext,
			nonce=excluded.nonce,
			revision=excluded.revision,
			payload_hash=excluded.payload_hash,
			attempts=0,
			last_error='',
			updated_at=excluded.updated_at
		where excluded.revision > pending_envelopes.revision
	`, id, kind, ciphertext, nonce, eventKey, revision, payloadHash, now, now)
	if err != nil {
		return "", fmt.Errorf("enqueue companion observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit companion observation: %w", err)
	}
	return id, nil
}

func normalizeAllowedDataLevels(values []string) (map[string]bool, error) {
	if values == nil {
		values = []string{steward.DataD0, steward.DataD1, steward.DataD2, steward.DataD3, steward.DataD4, steward.DataD5, steward.DataD6}
	}
	valid := map[string]bool{
		steward.DataD0: true, steward.DataD1: true, steward.DataD2: true, steward.DataD3: true,
		steward.DataD4: true, steward.DataD5: true, steward.DataD6: true,
	}
	result := map[string]bool{}
	for _, value := range values {
		level := strings.ToUpper(strings.TrimSpace(value))
		if level == "" {
			continue
		}
		if level == "ALL" || level == "*" {
			for candidate := range valid {
				result[candidate] = true
			}
			continue
		}
		if !valid[level] {
			return nil, fmt.Errorf("unsupported companion data level %q", value)
		}
		result[level] = true
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one companion data level must be enabled")
	}
	return result, nil
}

func normalizeObservationDataLevel(value string) string {
	level := strings.ToUpper(strings.TrimSpace(value))
	switch level {
	case steward.DataD0, steward.DataD1, steward.DataD2, steward.DataD3, steward.DataD4, steward.DataD5, steward.DataD6:
		return level
	default:
		return steward.DataD2
	}
}

func (b *Buffer) AllowedDataLevels() []string {
	result := make([]string, 0, len(b.allowedDataLevels))
	for rank := 0; rank <= 6; rank++ {
		level := fmt.Sprintf("D%d", rank)
		if b.allowedDataLevels[level] {
			result = append(result, level)
		}
	}
	return result
}

func (b *Buffer) Pending(ctx context.Context) (int, error) {
	var count int
	err := b.db.QueryRowContext(ctx, `select count(*) from pending_envelopes`).Scan(&count)
	return count, err
}

func (b *Buffer) Flush(ctx context.Context, apiBase string, client *http.Client, limit int) (FlushResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if _, err := companionAPIBase(apiBase); err != nil {
		return FlushResult{}, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := b.db.QueryContext(ctx, `
		select id,kind,ciphertext,nonce,event_key,revision,payload_hash from pending_envelopes order by created_at limit ?
	`, limit)
	if err != nil {
		return FlushResult{}, err
	}
	items := []bufferedObservation{}
	for rows.Next() {
		var item bufferedObservation
		if err := rows.Scan(&item.ID, &item.Kind, &item.Ciphertext, &item.Nonce, &item.EventKey, &item.Revision, &item.PayloadHash); err != nil {
			rows.Close()
			return FlushResult{}, err
		}
		items = append(items, item)
	}
	rows.Close()
	result := FlushResult{}
	observationPending := 0
	if err := b.db.QueryRowContext(ctx, `select count(*) from pending_envelopes where kind=?`, EnvelopeObservation).Scan(&observationPending); err != nil {
		return result, err
	}
	for _, item := range items {
		endpoint, endpointErr := envelopeEndpoint(apiBase, item.Kind)
		if endpointErr != nil {
			result.Failed++
			result.LastError = endpointErr.Error()
			_ = b.recordFailure(ctx, item.ID, endpointErr.Error())
			continue
		}
		plain, err := b.aead.Open(nil, item.Nonce, item.Ciphertext, []byte(item.ID))
		if err != nil {
			result.Failed++
			result.LastError = "decrypt buffered observation failed"
			_ = b.recordFailure(ctx, item.ID, result.LastError)
			continue
		}
		if item.Kind == EnvelopeObservation {
			// The main service cannot inspect the Companion's private SQLite
			// outbox. Attach the queue depth that will remain after this exact
			// revision is accepted so collection health can distinguish a fresh,
			// caught-up session from one that is accumulating an offline backlog.
			plain, err = decorateObservationForDelivery(plain, maxCompanionInt(0, observationPending-1))
			if err != nil {
				result.Failed++
				result.LastError = err.Error()
				_ = b.recordFailure(ctx, item.ID, err.Error())
				continue
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(plain))
		if err != nil {
			return result, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			result.Failed++
			result.LastError = err.Error()
			_ = b.recordFailure(ctx, item.ID, err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			result.Failed++
			result.LastError = fmt.Sprintf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
			_ = b.recordFailure(ctx, item.ID, result.LastError)
			continue
		}
		// A capture heartbeat may have advanced while this HTTP request was in
		// flight. Delete only the exact encrypted revision we submitted.
		if _, err := b.db.ExecContext(ctx, `delete from pending_envelopes where id=? and revision=? and payload_hash=?`, item.ID, item.Revision, item.PayloadHash); err != nil {
			return result, err
		}
		if item.Kind == EnvelopeObservation && observationPending > 0 {
			observationPending--
		}
		result.Submitted++
	}
	result.Pending, err = b.Pending(ctx)
	if result.Submitted > 0 {
		_, _ = b.db.ExecContext(ctx, `pragma wal_checkpoint(PASSIVE);`)
	}
	return result, err
}

func decorateObservationForDelivery(plain []byte, backlog int) ([]byte, error) {
	var input steward.CreateObservationInput
	if err := json.Unmarshal(plain, &input); err != nil {
		return nil, fmt.Errorf("decode buffered observation before delivery: %w", err)
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	input.Metadata["companion_outbox_backlog"] = maxCompanionInt(0, backlog)
	return json.Marshal(input)
}

func maxCompanionInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (b *Buffer) recordFailure(ctx context.Context, id, message string) error {
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := b.db.ExecContext(ctx, `
		update pending_envelopes set attempts=attempts+1,last_error=? where id=?
	`, message, id)
	return err
}

func companionAPIBase(apiBase string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(apiBase), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("companion API base must be an HTTP URL")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("companion only submits to a loopback API")
		}
	}
	base := strings.TrimRight(parsed.String(), "/")
	if strings.HasSuffix(base, "/api") {
		return base, nil
	}
	return base + "/api", nil
}

func envelopeEndpoint(apiBase, kind string) (string, error) {
	base, err := companionAPIBase(apiBase)
	if err != nil {
		return "", err
	}
	switch kind {
	case EnvelopeObservation:
		return base + "/steward/activity/observations", nil
	case EnvelopeNotificationFeedback:
		return base + "/steward/notifications/feedback/callback", nil
	default:
		return "", fmt.Errorf("unsupported companion envelope kind %q", kind)
	}
}

func observationEndpoint(apiBase string) (string, error) {
	return envelopeEndpoint(apiBase, EnvelopeObservation)
}
