//go:build windows

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWindowsNotificationFallsBackWhenHelperFails(t *testing.T) {
	notification := systemNotification{
		ID: "notification-1", Title: "提醒", Body: "起来活动一下", Priority: "normal",
		Actions: []systemNotificationAction{{ID: "later", Label: "15 分钟后", Kind: "snooze", Value: "900", CallbackToken: "signed-token"}},
	}
	paths := []string{}
	runner := func(_ context.Context, path string, args, extraEnv []string) ([]byte, error) {
		paths = append(paths, path)
		if path == `C:\test\steward-windows-notifier.exe` {
			return []byte("helper startup failed"), errors.New("exit status 1")
		}
		if path != "powershell.exe" {
			t.Fatalf("unexpected fallback executable %q", path)
		}
		if !containsWindowsNotificationEnvironment(extraEnv, "STEWARD_NOTIFICATION_TITLE=") ||
			!containsWindowsNotificationEnvironment(extraEnv, "STEWARD_NOTIFICATION_BODY=") {
			t.Fatalf("fallback environment = %v", extraEnv)
		}
		return nil, nil
	}

	providerID, err := showSystemNotificationWithRunner(context.Background(), notification, `C:\test\steward-windows-notifier.exe`, runner)
	if err != nil || providerID != notification.ID {
		t.Fatalf("provider id=%q err=%v", providerID, err)
	}
	if len(paths) != 2 || paths[0] != `C:\test\steward-windows-notifier.exe` || paths[1] != "powershell.exe" {
		t.Fatalf("delivery paths = %v", paths)
	}
}

func TestWindowsNotificationReportsHelperAndFallbackDiagnostics(t *testing.T) {
	runner := func(_ context.Context, path string, _ []string, _ []string) ([]byte, error) {
		if path == "powershell.exe" {
			return []byte("WinRT access denied"), errors.New("exit status 2")
		}
		return []byte("missing runtime"), errors.New("start failed")
	}
	_, err := showSystemNotificationWithRunner(context.Background(), systemNotification{
		ID: "notification-2", Title: "提醒", Body: "测试回退",
	}, `C:\test\steward-windows-notifier.exe`, runner)
	if err == nil {
		t.Fatal("both failed delivery paths must return an error")
	}
	message := err.Error()
	for _, want := range []string{
		"notifier helper path", `C:\test\steward-windows-notifier.exe`, "missing runtime",
		"PowerShell/WinRT fallback path", "powershell.exe", "WinRT access denied",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("combined diagnostic %q does not contain %q", message, want)
		}
	}
}

func TestWindowsNotificationDoesNotFallbackAfterHelperSuccess(t *testing.T) {
	calls := 0
	runner := func(_ context.Context, path string, _ []string, _ []string) ([]byte, error) {
		calls++
		if path != `C:\test\steward-windows-notifier.exe` {
			t.Fatalf("unexpected executable %q", path)
		}
		return []byte("notification-3"), nil
	}
	_, err := showSystemNotificationWithRunner(context.Background(), systemNotification{
		ID: "notification-3", Title: "提醒", Body: "helper success",
	}, `C:\test\steward-windows-notifier.exe`, runner)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func containsWindowsNotificationEnvironment(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
