//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func showSystemNotification(ctx context.Context, notification systemNotification) (string, error) {
	helper := strings.TrimSpace(os.Getenv("STEWARD_WINDOWS_NOTIFIER_PATH"))
	if helper == "" {
		if executable, err := os.Executable(); err == nil {
			candidates := []string{
				filepath.Join(filepath.Dir(executable), "windows-notifier", "steward-windows-notifier.exe"),
				filepath.Join(filepath.Dir(executable), "steward-windows-notifier.exe"),
			}
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate); err == nil {
					helper = candidate
					break
				}
			}
		}
	}
	if helper != "" {
		command := exec.CommandContext(ctx, helper, "--id", notification.ID, "--title", notification.Title, "--body", notification.Body, "--priority", notification.Priority)
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := command.CombinedOutput(); err != nil {
			return "", fmt.Errorf("Windows notifier helper failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return notification.ID, nil
	}
	// The Companion runs in the interactive, non-elevated user session. This
	// WinRT fallback writes to Windows Notification Center even when the web UI
	// is closed. Deployments can set STEWARD_WINDOWS_NOTIFIER_PATH to the Windows
	// App SDK helper for richer activation buttons.
	title := base64.StdEncoding.EncodeToString([]byte(notification.Title))
	body := base64.StdEncoding.EncodeToString([]byte(notification.Body))
	script := `$ErrorActionPreference='Stop';` +
		`[Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime]|Out-Null;` +
		`[Windows.Data.Xml.Dom.XmlDocument,Windows.Data.Xml.Dom.XmlDocument,ContentType=WindowsRuntime]|Out-Null;` +
		`$t=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:STEWARD_NOTIFICATION_TITLE));` +
		`$b=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($env:STEWARD_NOTIFICATION_BODY));` +
		`$t=[Security.SecurityElement]::Escape($t);$b=[Security.SecurityElement]::Escape($b);` +
		`$x=[Windows.Data.Xml.Dom.XmlDocument]::new();$x.LoadXml('<toast><visual><binding template="ToastGeneric"><text>'+$t+'</text><text>'+$b+'</text></binding></visual></toast>');` +
		`$n=[Windows.UI.Notifications.ToastNotification]::new($x);` +
		`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Microsoft.Windows.PowerShell').Show($n);`
	command := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	command.Env = append(os.Environ(), "STEWARD_NOTIFICATION_TITLE="+title, "STEWARD_NOTIFICATION_BODY="+body)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("Windows Notification Center rejected notification: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return notification.ID, nil
}
