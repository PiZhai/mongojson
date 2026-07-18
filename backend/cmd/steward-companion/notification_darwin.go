//go:build darwin

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func showSystemNotification(ctx context.Context, notification systemNotification) (string, error) {
	script := `display notification ` + appleScriptString(notification.Body) + ` with title ` + appleScriptString(notification.Title)
	if output, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput(); err != nil {
		return "", fmt.Errorf("macOS notification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return notification.ID, nil
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
