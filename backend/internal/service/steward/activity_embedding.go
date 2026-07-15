package steward

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"time"
)

const stewardEmbeddingDimensions = 768

func localSummaryEmbedding(text string) string {
	runes := []rune(strings.ToLower(strings.TrimSpace(text)))
	vector := make([]float64, stewardEmbeddingDimensions)
	for index := range runes {
		end := index + 3
		if end > len(runes) {
			end = len(runes)
		}
		token := string(runes[index:end])
		hash := sha256.Sum256([]byte(token))
		position := int(binary.BigEndian.Uint32(hash[:4]) % stewardEmbeddingDimensions)
		sign := 1.0
		if hash[4]&1 == 1 {
			sign = -1
		}
		vector[position] += sign
	}
	norm := 0.0
	for _, value := range vector {
		norm += value * value
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
	}
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range vector {
		if index > 0 {
			builder.WriteByte(',')
		}
		if norm > 0 {
			value /= norm
		}
		builder.WriteString(strconv.FormatFloat(value, 'f', 6, 64))
	}
	builder.WriteByte(']')
	return builder.String()
}

func (s *Service) storeObservationEmbedding(ctx context.Context, id string, occurredAt time.Time, summary string) {
	if strings.TrimSpace(summary) == "" || !s.vectorExtensionAvailable(ctx) {
		return
	}
	_, _ = s.db.Pool.Exec(ctx, `update steward_observations set embedding=$3::vector where id=$1 and occurred_at=$2`,
		id, occurredAt, localSummaryEmbedding(summary))
}

func (s *Service) storeActivitySessionEmbedding(ctx context.Context, id, summary string) {
	if strings.TrimSpace(summary) == "" || !s.vectorExtensionAvailable(ctx) {
		return
	}
	_, _ = s.db.Pool.Exec(ctx, `update steward_activity_sessions set embedding=$2::vector where id=$1`, id, localSummaryEmbedding(summary))
}

func (s *Service) vectorExtensionAvailable(ctx context.Context) bool {
	var enabled bool
	if err := s.db.Pool.QueryRow(ctx, `select exists(select 1 from pg_extension where extname='vector')`).Scan(&enabled); err != nil {
		return false
	}
	return enabled
}
