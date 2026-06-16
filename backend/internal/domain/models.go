package domain

import "time"

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
