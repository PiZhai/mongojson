package storage

import (
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
	defer file.Close()

	storedName = fmt.Sprintf("%s-%s", uuid.NewString(), filepath.Base(originalName))
	path = filepath.Join(s.root, category, storedName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", 0, err
	}

	dst, err := os.Create(path)
	if err != nil {
		return "", "", 0, err
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		return "", "", 0, err
	}
	return storedName, path, written, nil
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
