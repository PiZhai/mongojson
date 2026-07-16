package privilegebroker

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type signedCheckpoint struct {
	Version        int       `json:"version"`
	AuditSequence  int64     `json:"audit_sequence"`
	AuditHash      string    `json:"audit_hash"`
	Stopped        bool      `json:"stopped"`
	Generation     int64     `json:"generation"`
	StateChangedAt time.Time `json:"state_changed_at"`
	IssuedAt       time.Time `json:"issued_at"`
	KeyID          string    `json:"key_id"`
	Signature      string    `json:"signature"`
}

func (l *auditLog) verifyCheckpoint(state brokerState) error {
	payload, err := os.ReadFile(l.checkpointPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("broker checkpoint is missing; initialize it explicitly before startup")
	}
	if err != nil {
		return fmt.Errorf("read broker checkpoint: %w", err)
	}
	var checkpoint signedCheckpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return fmt.Errorf("decode broker checkpoint: %w", err)
	}
	unsigned := checkpoint
	unsigned.Signature = ""
	if checkpoint.Version != 1 || checkpoint.KeyID != l.keyID || verifyValue(l.publicKey, unsigned, checkpoint.Signature) != nil {
		return fmt.Errorf("broker checkpoint signature is invalid")
	}
	if checkpoint.AuditSequence != l.sequence || checkpoint.AuditHash != l.previousHash ||
		checkpoint.Generation != state.Generation || checkpoint.Stopped != state.Stopped ||
		!checkpoint.StateChangedAt.Equal(state.ChangedAt) {
		return fmt.Errorf("broker audit/state is behind or inconsistent with the signed checkpoint")
	}
	return nil
}

func (l *auditLog) persistCheckpoint(state brokerState) error {
	checkpoint := signedCheckpoint{Version: 1, AuditSequence: l.sequence, AuditHash: l.previousHash,
		Stopped: state.Stopped, Generation: state.Generation, StateChangedAt: state.ChangedAt,
		IssuedAt: time.Now().UTC(), KeyID: l.keyID}
	unsigned := checkpoint
	unsigned.Signature = ""
	signature, err := signValue(l.privateKey, unsigned)
	if err != nil {
		return err
	}
	checkpoint.Signature = signature
	payload, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	return persistAtomicFile(l.checkpointPath, payload, "broker-checkpoint")
}

func persistAtomicFile(path string, payload []byte, prefix string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+prefix+"-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
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
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace broker checkpoint: %w", err)
	}
	return syncParentDirectory(path)
}

// InitializeCheckpoint is the only supported migration from pre-checkpoint
// deployments. It validates the complete signed audit chain and current state,
// refuses to overwrite an anchor, and writes a signed anchor without starting
// the broker.
func InitializeCheckpoint(config ServerConfig) error {
	if len(config.SigningKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("broker signing key is invalid")
	}
	state, statePath, err := loadBrokerState(config.StatePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		// Materialize the initial state so the signed checkpoint and the later
		// server load bind the exact same timestamp and bytes.
		if err := persistBrokerState(statePath, state); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	checkpointPath, err := requiredAbsolutePath(config.CheckpointPath, "broker checkpoint")
	if err != nil {
		return err
	}
	if _, err := os.Stat(checkpointPath); err == nil {
		return fmt.Errorf("broker checkpoint already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	// Use a temporary impossible checkpoint path so construction cannot silently
	// initialize it; verify the audit chain directly, then create the real anchor.
	publicKey := config.SigningKey.Public().(ed25519.PublicKey)
	auditPath, err := requiredAbsolutePath(config.AuditPath, "broker audit")
	if err != nil {
		return err
	}
	log := &auditLog{path: auditPath, checkpointPath: checkpointPath, statePath: statePath,
		privateKey: config.SigningKey, publicKey: publicKey, keyID: publicKeyID(publicKey),
		consumedApprovalProofs: map[string]struct{}{}, webAuthnSignCounts: map[string]uint32{}}
	if err := log.verifyExisting(); err != nil {
		return err
	}
	return log.persistCheckpoint(state)
}
