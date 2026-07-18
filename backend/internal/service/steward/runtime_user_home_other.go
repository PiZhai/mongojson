//go:build !windows

package steward

import (
	"os"
	"path/filepath"
	"strings"
)

func runtimeUserHome() string {
	if configured := validRuntimeUserHome(os.Getenv("STEWARD_RUNTIME_USER_HOME")); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return validRuntimeUserHome(home)
}

func validRuntimeUserHome(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	value = filepath.Clean(value)
	if info, err := os.Stat(value); err != nil || !info.IsDir() {
		return ""
	}
	return value
}
