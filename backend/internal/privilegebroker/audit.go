package privilegebroker

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AuditRecord struct {
	Sequence     int64          `json:"sequence"`
	ID           string         `json:"id"`
	CreatedAt    time.Time      `json:"created_at"`
	Type         string         `json:"type"`
	Subject      string         `json:"subject,omitempty"`
	Capability   string         `json:"capability,omitempty"`
	Generation   int64          `json:"generation"`
	Outcome      string         `json:"outcome"`
	Details      map[string]any `json:"details,omitempty"`
	PreviousHash string         `json:"previous_hash,omitempty"`
	Hash         string         `json:"hash"`
	KeyID        string         `json:"key_id"`
	Signature    string         `json:"signature"`
}

type auditLog struct {
	mu                     sync.Mutex
	path                   string
	checkpointPath         string
	statePath              string
	privateKey             ed25519.PrivateKey
	publicKey              ed25519.PublicKey
	keyID                  string
	sequence               int64
	previousHash           string
	consumedApprovalProofs map[string]struct{}
	webAuthnSignCounts     map[string]uint32
}

func newAuditLog(path string, privateKey ed25519.PrivateKey) (*auditLog, error) {
	return openAuditLog(path, "", "", brokerState{}, privateKey, false)
}

func newProtectedAuditLog(path, checkpointPath, statePath string, state brokerState, privateKey ed25519.PrivateKey) (*auditLog, error) {
	return openAuditLog(path, checkpointPath, statePath, state, privateKey, true)
}

func openAuditLog(path, checkpointPath, statePath string, state brokerState, privateKey ed25519.PrivateKey, protected bool) (*auditLog, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("broker audit path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve broker audit path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create broker audit directory: %w", err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	if protected {
		checkpointPath, err = requiredAbsolutePath(checkpointPath, "broker checkpoint")
		if err != nil {
			return nil, err
		}
	}
	log := &auditLog{path: absolute, checkpointPath: checkpointPath, statePath: statePath, privateKey: privateKey, publicKey: publicKey, keyID: publicKeyID(publicKey), consumedApprovalProofs: map[string]struct{}{}, webAuthnSignCounts: map[string]uint32{}}
	if err := log.verifyExisting(); err != nil {
		return nil, err
	}
	if protected {
		if err := log.verifyCheckpoint(state); err != nil {
			return nil, err
		}
	}
	return log, nil
}

func (l *auditLog) verifyExisting() error {
	file, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open broker audit log: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	var previous string
	var sequence int64
	line := 0
	for scanner.Scan() {
		line++
		var record AuditRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("broker audit line %d is invalid JSON: %w", line, err)
		}
		if record.Sequence != sequence+1 || record.PreviousHash != previous || record.KeyID != l.keyID {
			return fmt.Errorf("broker audit chain is invalid at line %d", line)
		}
		expected, err := auditRecordHash(record)
		if err != nil || expected != record.Hash {
			return fmt.Errorf("broker audit hash is invalid at line %d", line)
		}
		signature, err := base64.RawURLEncoding.DecodeString(record.Signature)
		if err != nil || !ed25519.Verify(l.publicKey, []byte(record.Hash), signature) {
			return fmt.Errorf("broker audit signature is invalid at line %d", line)
		}
		sequence = record.Sequence
		previous = record.Hash
		l.recordConsumedApprovalProof(record)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read broker audit log: %w", err)
	}
	l.sequence = sequence
	l.previousHash = previous
	return nil
}

func (l *auditLog) Append(record AuditRecord) error {
	if l == nil {
		return fmt.Errorf("broker audit log is not configured")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if record.ID == "" {
		id, err := randomID()
		if err != nil {
			return err
		}
		record.ID = id
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.Sequence = l.sequence + 1
	record.PreviousHash = l.previousHash
	record.KeyID = l.keyID
	record.Hash = ""
	record.Signature = ""
	hash, err := auditRecordHash(record)
	if err != nil {
		return err
	}
	record.Hash = hash
	record.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(l.privateKey, []byte(hash)))
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open broker audit log for append: %w", err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("append broker audit record: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync broker audit record: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	l.sequence = record.Sequence
	l.previousHash = record.Hash
	l.recordConsumedApprovalProof(record)
	if l.checkpointPath != "" {
		state, _, err := loadBrokerState(l.statePath)
		if err != nil {
			return fmt.Errorf("reload broker state for checkpoint: %w", err)
		}
		if err := l.persistCheckpoint(state); err != nil {
			return err
		}
	}
	return nil
}

func requiredAbsolutePath(path, label string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	return absolute, nil
}

func (l *auditLog) recordConsumedApprovalProof(record AuditRecord) {
	if (record.Type != "grant.issued" && record.Type != "delegation.issued") || record.Details == nil {
		return
	}
	proofID, _ := record.Details["approval_proof_id"].(string)
	proofID = strings.TrimSpace(proofID)
	if proofID != "" {
		l.consumedApprovalProofs[proofID] = struct{}{}
	}
	keyID, _ := record.Details["approval_key_id"].(string)
	if raw, ok := record.Details["webauthn_sign_count"].(float64); ok && raw > 0 && raw <= float64(^uint32(0)) {
		count := uint32(raw)
		if count > l.webAuthnSignCounts[keyID] {
			l.webAuthnSignCounts[keyID] = count
		}
	}
}

func (l *auditLog) WebAuthnSignCounts() map[string]uint32 {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make(map[string]uint32, len(l.webAuthnSignCounts))
	for keyID, count := range l.webAuthnSignCounts {
		result[keyID] = count
	}
	return result
}

func (l *auditLog) ConsumedApprovalProofs() map[string]struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make(map[string]struct{}, len(l.consumedApprovalProofs))
	for proofID := range l.consumedApprovalProofs {
		result[proofID] = struct{}{}
	}
	return result
}

func auditRecordHash(record AuditRecord) (string, error) {
	record.Hash = ""
	record.Signature = ""
	payload, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
