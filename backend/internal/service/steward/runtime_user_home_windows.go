//go:build windows

package steward

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func runtimeUserHome() string {
	if configured := validRuntimeUserHome(os.Getenv("STEWARD_RUNTIME_USER_HOME")); configured != "" {
		return configured
	}
	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID != 0xffffffff {
		var token windows.Token
		if err := windows.WTSQueryUserToken(sessionID, &token); err == nil {
			defer token.Close()
			if profile, profileErr := token.GetUserProfileDirectory(); profileErr == nil {
				if resolved := validRuntimeUserHome(profile); resolved != "" {
					return resolved
				}
			}
		}
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
