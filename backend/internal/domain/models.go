package domain

import (
	"encoding/json"
	"time"
)

type FileRecord struct {
	ID           string     `json:"id"`
	OriginalName string     `json:"original_name"`
	StoredName   string     `json:"stored_name"`
	StoragePath  string     `json:"-"`
	MIMEType     string     `json:"mime_type"`
	SizeBytes    int64      `json:"size_bytes"`
	Category     string     `json:"category"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type JobRecord struct {
	ID           string         `json:"id"`
	ToolType     string         `json:"tool_type"`
	Status       string         `json:"status"`
	InputFileID  *string        `json:"input_file_id,omitempty"`
	OutputFileID *string        `json:"output_file_id,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	ErrorMessage *string        `json:"error_message,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
	ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
}

type PresetRecord struct {
	ID        string         `json:"id"`
	ToolType  string         `json:"tool_type"`
	Name      string         `json:"name"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type MemoRecord struct {
	ID            string             `json:"id"`
	Slug          string             `json:"slug"`
	Title         string             `json:"title"`
	ContentHTML   string             `json:"content_html"`
	ContentText   string             `json:"content_text"`
	FloatingCards []MemoFloatingCard `json:"floating_cards"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type MemoFloatingCard struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type MusicTrackRecord struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Artist       string          `json:"artist,omitempty"`
	Note         string          `json:"note,omitempty"`
	OriginalName string          `json:"original_name"`
	MIMEType     string          `json:"mime_type"`
	SizeBytes    int64           `json:"size_bytes"`
	Duration     *float64        `json:"duration,omitempty"`
	AudioQuality json.RawMessage `json:"audio_quality,omitempty"`
	StoragePath  string          `json:"-"`
	CreatedAt    time.Time       `json:"created_at"`
}
