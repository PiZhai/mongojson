package main

import (
	"os"
	"path/filepath"
	"strings"
)

const bundledUIDirectoryName = "ui"

func resolveStewardUIDir(explicit string, binaryPath string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(binaryPath), bundledUIDirectoryName)
	indexPath := filepath.Join(candidate, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil || info.IsDir() {
		return ""
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return ""
	}
	return absolute
}

func currentBinaryUIDir(explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return ""
	}
	return resolveStewardUIDir("", binaryPath)
}
