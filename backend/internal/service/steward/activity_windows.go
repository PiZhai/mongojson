//go:build windows

package steward

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	activityUser32                 = windows.NewLazySystemDLL("user32.dll")
	activityGetForegroundWindow    = activityUser32.NewProc("GetForegroundWindow")
	activityGetWindowTextLengthW   = activityUser32.NewProc("GetWindowTextLengthW")
	activityGetWindowTextW         = activityUser32.NewProc("GetWindowTextW")
	activityGetWindowThreadProcess = activityUser32.NewProc("GetWindowThreadProcessId")
	activityKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	activityQueryFullProcessImageW = activityKernel32.NewProc("QueryFullProcessImageNameW")
)

func (s *Service) collectForegroundActivity(ctx context.Context, settings map[string]any) error {
	// Production runs the main service as LocalService in Session 0. Sampling
	// user32 there can never describe the signed-in user's desktop; the logged-in
	// Session Companion owns this collector. Keep this path only as an
	// interactive-development fallback.
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &sessionID); err == nil && sessionID == 0 {
		return nil
	}
	handle, _, _ := activityGetForegroundWindow.Call()
	if handle == 0 {
		return nil
	}
	length, _, _ := activityGetWindowTextLengthW.Call(handle)
	if length == 0 {
		return nil
	}
	buffer := make([]uint16, int(length)+1)
	written, _, _ := activityGetWindowTextW.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if written == 0 {
		return nil
	}
	title := strings.TrimSpace(syscall.UTF16ToString(buffer))
	if title == "" {
		return nil
	}
	var processID uint32
	activityGetWindowThreadProcess.Call(handle, uintptr(unsafe.Pointer(&processID)))
	application := foregroundProcessName(processID)
	if application == "" {
		application = "unknown"
	}
	now := time.Now().UTC()
	endedAt := now.Add(time.Duration(collectorInt(settings["sample_interval_seconds"], 15)) * time.Second)
	contextKey := application + "|" + truncateAdvisorText(title, 300)
	summary := application + " · " + truncateAdvisorText(title, 300)
	_, err := s.CreateObservation(ctx, CreateObservationInput{
		Source: "collector:windows-activity", Type: "foreground_window", Summary: summary,
		ContextKey: contextKey, DataLevel: DataD2, PermissionLevel: PermissionA1,
		Payload:     map[string]any{"application": application, "window_title": title},
		EntityHints: []ObservationEntityHint{{Type: "application", CanonicalKey: strings.ToLower(application), DisplayName: application}},
		OccurredAt:  &now, EndedAt: &endedAt,
		Metadata: map[string]any{"adapter": "windows-native", "duration_seconds": endedAt.Sub(now).Seconds(), "redacted": true},
	})
	if err != nil {
		return fmt.Errorf("collect foreground activity: %w", err)
	}
	return nil
}

func foregroundProcessName(processID uint32) string {
	if processID == 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	result, _, _ := activityQueryFullProcessImageW.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if result == 0 || size == 0 {
		return ""
	}
	path := syscall.UTF16ToString(buffer[:size])
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
