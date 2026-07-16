package privilegebroker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type brokerState struct {
	Stopped    bool      `json:"stopped"`
	Generation int64     `json:"generation"`
	Reason     string    `json:"reason,omitempty"`
	ChangedBy  string    `json:"changed_by,omitempty"`
	ChangedAt  time.Time `json:"changed_at"`
}

func loadBrokerState(path string) (brokerState, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return brokerState{}, "", fmt.Errorf("broker state path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return brokerState{}, "", fmt.Errorf("resolve broker state path: %w", err)
	}
	payload, err := os.ReadFile(absolute)
	if os.IsNotExist(err) {
		return brokerState{ChangedAt: time.Now().UTC()}, absolute, nil
	}
	if err != nil {
		return brokerState{}, "", fmt.Errorf("read broker state: %w", err)
	}
	var state brokerState
	if err := json.Unmarshal(payload, &state); err != nil {
		return brokerState{}, "", fmt.Errorf("decode broker state: %w", err)
	}
	if state.Generation < 0 {
		return brokerState{}, "", fmt.Errorf("broker state generation must not be negative")
	}
	return state, absolute, nil
}

func persistBrokerState(path string, state brokerState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create broker state directory: %w", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".broker-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create broker state temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace broker state: %w", err)
	}
	return syncParentDirectory(path)
}
