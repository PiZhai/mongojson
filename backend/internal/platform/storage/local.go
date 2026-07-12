package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type LocalStore struct {
	root string
}

func NewLocalStore(root string) *LocalStore {
	return &LocalStore{root: root}
}

func (s *LocalStore) SaveUploadedFile(file multipart.File, originalName string, category string) (storedName string, path string, size int64, err error) {
	storedName, path, size, _, err = s.saveUploadedFile(file, originalName, category, false)
	return
}

func (s *LocalStore) SaveUploadedFileWithSHA256(file multipart.File, originalName string, category string) (storedName string, path string, size int64, digest string, err error) {
	return s.saveUploadedFile(file, originalName, category, true)
}

func (s *LocalStore) saveUploadedFile(file multipart.File, originalName string, category string, hashContent bool) (storedName string, path string, size int64, digest string, err error) {
	defer file.Close()

	storedName = fmt.Sprintf("%s-%s", uuid.NewString(), filepath.Base(originalName))
	path = filepath.Join(s.root, category, storedName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", 0, "", err
	}

	dst, err := os.Create(path)
	if err != nil {
		return "", "", 0, "", err
	}
	defer dst.Close()

	var writer io.Writer = dst
	var hasher = sha256.New()
	if hashContent {
		writer = io.MultiWriter(dst, hasher)
	}
	written, err := io.Copy(writer, file)
	if err != nil {
		return "", "", 0, "", err
	}
	if hashContent {
		digest = hex.EncodeToString(hasher.Sum(nil))
	}
	return storedName, path, written, digest, nil
}

func (s *LocalStore) SaveBytes(content []byte, outputName string, category string) (storedName string, path string, size int64, err error) {
	storedName = fmt.Sprintf("%s-%s", uuid.NewString(), filepath.Base(outputName))
	path = filepath.Join(s.root, category, storedName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", 0, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", "", 0, err
	}
	return storedName, path, int64(len(content)), nil
}

func (s *LocalStore) Delete(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
