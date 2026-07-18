//go:build linux

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func showSystemNotification(ctx context.Context, notification systemNotification) (string, error) {
	urgency := "normal"
	if notification.Priority == "high" || notification.Priority == "urgent" {
		urgency = "critical"
	} else if notification.Priority == "low" {
		urgency = "low"
	}
	output, err := exec.CommandContext(ctx, "notify-send", "--app-name=Steward", "--urgency="+urgency, notification.Title, notification.Body).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Linux desktop notification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return notification.ID, nil
}
