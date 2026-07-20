//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func showSystemNotification(ctx context.Context, notification systemNotification) (string, error) {
	helper := resolveWindowsNotifierHelper()
	return showSystemNotificationWithRunner(ctx, notification, helper, runWindowsNotificationCommand)
}

type windowsNotificationCommandRunner func(context.Context, string, []string, []string) ([]byte, error)

func resolveWindowsNotifierHelper() string {
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
	return helper
}

func showSystemNotificationWithRunner(ctx context.Context, notification systemNotification, helper string, runner windowsNotificationCommandRunner) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("Windows notification command runner is not configured")
	}
	helperDiagnostic := "not configured or not found"
	if helper != "" {
		actions, err := json.Marshal(notification.Actions)
		if err != nil {
			return "", fmt.Errorf("encode Windows notification actions: %w", err)
		}
		args := []string{
			"--id", notification.ID,
			"--title", notification.Title,
			"--body", notification.Body,
			"--priority", notification.Priority,
			"--actions-base64", base64.StdEncoding.EncodeToString(actions),
		}
		if output, runErr := runner(ctx, helper, args, nil); runErr == nil {
			return notification.ID, nil
		} else {
			helperDiagnostic = commandFailureDiagnostic(helper, runErr, output)
		}
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
	fallbackArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script}
	fallbackEnv := []string{"STEWARD_NOTIFICATION_TITLE=" + title, "STEWARD_NOTIFICATION_BODY=" + body}
	if output, fallbackErr := runner(ctx, "powershell.exe", fallbackArgs, fallbackEnv); fallbackErr != nil {
		return "", fmt.Errorf("Windows notification delivery failed; notifier helper path: %s; PowerShell/WinRT fallback path: %s",
			helperDiagnostic, commandFailureDiagnostic("powershell.exe", fallbackErr, output))
	}
	return notification.ID, nil
}

func runWindowsNotificationCommand(ctx context.Context, path string, args, extraEnv []string) ([]byte, error) {
	command := exec.CommandContext(ctx, path, args...)
	if len(extraEnv) > 0 {
		command.Env = append(os.Environ(), extraEnv...)
	}
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command.CombinedOutput()
}

func commandFailureDiagnostic(path string, err error, output []byte) string {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = "no process output"
	}
	return fmt.Sprintf("%s failed: %v; output: %s", path, err, detail)
}
