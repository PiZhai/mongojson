package steward

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const stewardVaultMagic = "STEWV1"

type encryptedBlobResult struct {
	Path           string
	KeyID          string
	CiphertextHash string
	PlaintextSize  int64
}

func (s *Service) writeEncryptedBlob(observationID string, content []byte) (encryptedBlobResult, error) {
	if len(content) == 0 {
		return encryptedBlobResult{}, fmt.Errorf("media content is empty")
	}
	cipherConfig, enabled, err := localPayloadCipherFromEnv()
	if err != nil {
		return encryptedBlobResult{}, err
	}
	if !enabled || cipherConfig.current == nil {
		return encryptedBlobResult{}, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY is required for media storage")
	}
	block, err := aes.NewCipher(cipherConfig.current.key)
	if err != nil {
		return encryptedBlobResult{}, fmt.Errorf("create media cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return encryptedBlobResult{}, fmt.Errorf("create media gcm: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return encryptedBlobResult{}, fmt.Errorf("generate media nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, content, []byte(observationID))
	root := filepath.Join(s.storageDir, "steward-vault")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return encryptedBlobResult{}, fmt.Errorf("create media vault: %w", err)
	}
	randomName := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, randomName); err != nil {
		return encryptedBlobResult{}, fmt.Errorf("generate media path: %w", err)
	}
	path := filepath.Join(root, hex.EncodeToString(randomName)+".bin")
	temp, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return encryptedBlobResult{}, fmt.Errorf("create encrypted media: %w", err)
	}
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(path + ".tmp")
	}
	if _, err := temp.Write(append(append([]byte(stewardVaultMagic), nonce...), ciphertext...)); err != nil {
		cleanup()
		return encryptedBlobResult{}, fmt.Errorf("write encrypted media: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return encryptedBlobResult{}, fmt.Errorf("sync encrypted media: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(path + ".tmp")
		return encryptedBlobResult{}, fmt.Errorf("close encrypted media: %w", err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return encryptedBlobResult{}, fmt.Errorf("publish encrypted media: %w", err)
	}
	hash := sha256.Sum256(ciphertext)
	return encryptedBlobResult{
		Path:           path,
		KeyID:          cipherConfig.current.keyID,
		CiphertextHash: hex.EncodeToString(hash[:]),
		PlaintextSize:  int64(len(content)),
	}, nil
}
