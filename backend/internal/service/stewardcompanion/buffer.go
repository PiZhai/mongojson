package stewardcompanion

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
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

var ErrBufferFull = errors.New("companion observation buffer reached its configured limit")

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
	Submitted int `json:"submitted"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

type bufferedObservation struct {
	ID         string
	Ciphertext []byte
	Nonce      []byte
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
			attempts integer not null default 0,
			last_error text not null default '',
			created_at text not null
		);`,
		`create index if not exists idx_pending_observations_created_at on pending_observations(created_at);`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize companion buffer: %w", err)
		}
	}
	_ = os.Chmod(path, 0o600)
	return buffer, nil
}

func (b *Buffer) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

func (b *Buffer) Enqueue(ctx context.Context, input steward.CreateObservationInput) (string, error) {
	level, category := steward.ClassifyObservationDataLevel(input)
	if !b.allowedDataLevels[level] {
		if level == steward.DataD5 {
			return "", fmt.Errorf("%w: companion D5 collection is not enabled (%s)", steward.ErrCredentialDataBlocked, category)
		}
		return "", fmt.Errorf("companion data level %q is not enabled", level)
	}
	input.DataLevel = level
	plain, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := b.aead.Seal(nil, nonce, plain, []byte(id))
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `select count(*) from pending_observations`).Scan(&count); err != nil {
		return "", err
	}
	if count >= b.maxPending {
		return "", ErrBufferFull
	}
	_, err = tx.ExecContext(ctx, `
		insert into pending_observations (id,ciphertext,nonce,created_at) values (?,?,?,?)
	`, id, ciphertext, nonce, time.Now().UTC().Format(time.RFC3339Nano))
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
		values = []string{steward.DataD0, steward.DataD1, steward.DataD2, steward.DataD3, steward.DataD4, steward.DataD6}
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
	err := b.db.QueryRowContext(ctx, `select count(*) from pending_observations`).Scan(&count)
	return count, err
}

func (b *Buffer) Flush(ctx context.Context, apiBase string, client *http.Client, limit int) (FlushResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	endpoint, err := observationEndpoint(apiBase)
	if err != nil {
		return FlushResult{}, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := b.db.QueryContext(ctx, `
		select id,ciphertext,nonce from pending_observations order by created_at limit ?
	`, limit)
	if err != nil {
		return FlushResult{}, err
	}
	items := []bufferedObservation{}
	for rows.Next() {
		var item bufferedObservation
		if err := rows.Scan(&item.ID, &item.Ciphertext, &item.Nonce); err != nil {
			rows.Close()
			return FlushResult{}, err
		}
		items = append(items, item)
	}
	rows.Close()
	result := FlushResult{}
	for _, item := range items {
		plain, err := b.aead.Open(nil, item.Nonce, item.Ciphertext, []byte(item.ID))
		if err != nil {
			result.Failed++
			_ = b.recordFailure(ctx, item.ID, "decrypt buffered observation failed")
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(plain))
		if err != nil {
			return result, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			result.Failed++
			_ = b.recordFailure(ctx, item.ID, err.Error())
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			result.Failed++
			_ = b.recordFailure(ctx, item.ID, fmt.Sprintf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body))))
			continue
		}
		if _, err := b.db.ExecContext(ctx, `delete from pending_observations where id=?`, item.ID); err != nil {
			return result, err
		}
		result.Submitted++
	}
	result.Pending, err = b.Pending(ctx)
	if result.Submitted > 0 {
		_, _ = b.db.ExecContext(ctx, `pragma wal_checkpoint(PASSIVE);`)
	}
	return result, err
}

func (b *Buffer) recordFailure(ctx context.Context, id, message string) error {
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := b.db.ExecContext(ctx, `
		update pending_observations set attempts=attempts+1,last_error=? where id=?
	`, message, id)
	return err
}

func observationEndpoint(apiBase string) (string, error) {
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
		return base + "/steward/activity/observations", nil
	}
	return base + "/api/steward/activity/observations", nil
}
