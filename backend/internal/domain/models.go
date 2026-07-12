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
	ContentJSON   json.RawMessage    `json:"content_json"`
	ContentHTML   string             `json:"content_html"`
	ContentText   string             `json:"content_text"`
	FloatingCards []MemoFloatingCard `json:"floating_cards"`
	SchemaVersion int                `json:"schema_version"`
	Revision      int64              `json:"revision"`
	EditorType    string             `json:"editor_type"`
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

type MemoSideNoteRecord struct {
	ID            string          `json:"id"`
	DocumentID    string          `json:"document_id"`
	AnchorBlockID *string         `json:"anchor_block_id,omitempty"`
	BodyJSON      json.RawMessage `json:"body_json"`
	Color         string          `json:"color"`
	SortOrder     int             `json:"sort_order"`
	Collapsed     bool            `json:"collapsed"`
	Status        string          `json:"status"`
	Revision      int64           `json:"revision"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type MusicTrackRecord struct {
	ID               string          `json:"id"`
	FileID           string          `json:"-"`
	LyricFileID      *string         `json:"-"`
	Title            string          `json:"title"`
	Artist           string          `json:"artist,omitempty"`
	Note             string          `json:"note,omitempty"`
	OriginalName     string          `json:"original_name"`
	MIMEType         string          `json:"mime_type"`
	SizeBytes        int64           `json:"size_bytes"`
	Duration         *float64        `json:"duration,omitempty"`
	AudioQuality     json.RawMessage `json:"audio_quality,omitempty"`
	ContentSHA256    string          `json:"-"`
	StoragePath      string          `json:"-"`
	LyricFileName    string          `json:"lyric_file_name,omitempty"`
	LyricMIMEType    string          `json:"lyric_mime_type,omitempty"`
	LyricStoragePath string          `json:"-"`
	FileAvailable    bool            `json:"file_available"`
	RecordIssue      string          `json:"record_issue,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

type CanvasBoardRecord struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Scene     json.RawMessage `json:"scene,omitempty"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type CanvasAssetRecord struct {
	ID           string    `json:"id"`
	BoardID      string    `json:"board_id"`
	FileID       string    `json:"-"`
	CanvasFileID string    `json:"canvas_file_id"`
	OriginalName string    `json:"original_name"`
	MIMEType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	StoragePath  string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}
