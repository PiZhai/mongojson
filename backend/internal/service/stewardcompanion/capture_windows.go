//go:build windows

package stewardcompanion

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
	captureUser32                 = windows.NewLazySystemDLL("user32.dll")
	captureGetForegroundWindow    = captureUser32.NewProc("GetForegroundWindow")
	captureGetWindowTextLengthW   = captureUser32.NewProc("GetWindowTextLengthW")
	captureGetWindowTextW         = captureUser32.NewProc("GetWindowTextW")
	captureGetWindowThreadProcess = captureUser32.NewProc("GetWindowThreadProcessId")
	captureGetLastInputInfo       = captureUser32.NewProc("GetLastInputInfo")
	captureKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	captureQueryFullProcessImageW = captureKernel32.NewProc("QueryFullProcessImageNameW")
	captureGetTickCount64         = captureKernel32.NewProc("GetTickCount64")
)

type nativeActivitySampler struct{}

type lastInputInfo struct {
	Size uint32
	Time uint32
}

func NewNativeActivitySampler() ActivitySampler { return nativeActivitySampler{} }

func (nativeActivitySampler) Sample(context.Context) (ActivitySnapshot, error) {
	now := time.Now().UTC()
	sessionID := uint32(0)
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &sessionID); err != nil {
		return ActivitySnapshot{}, fmt.Errorf("resolve companion session: %w", err)
	}
	handle, _, _ := captureGetForegroundWindow.Call()
	snapshot := ActivitySnapshot{CapturedAt: now, SessionID: fmt.Sprintf("windows-%d", sessionID), IdleFor: nativeIdleDuration()}
	if handle == 0 {
		return snapshot, nil
	}
	length, _, _ := captureGetWindowTextLengthW.Call(handle)
	if length > 0 {
		buffer := make([]uint16, int(length)+1)
		written, _, _ := captureGetWindowTextW.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if written > 0 {
			snapshot.WindowTitle = strings.TrimSpace(syscall.UTF16ToString(buffer))
		}
	}
	captureGetWindowThreadProcess.Call(handle, uintptr(unsafe.Pointer(&snapshot.ProcessID)))
	snapshot.Application = nativeProcessName(snapshot.ProcessID)
	return snapshot, nil
}

func nativeIdleDuration() time.Duration {
	info := lastInputInfo{Size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ok, _, _ := captureGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0
	}
	ticks, _, _ := captureGetTickCount64.Call()
	// LASTINPUTINFO is a DWORD tick count and wraps roughly every 49 days.
	current := uint32(ticks)
	return time.Duration(current-info.Time) * time.Millisecond
}

func nativeProcessName(processID uint32) string {
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
	result, _, _ := captureQueryFullProcessImageW.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)))
	if result == 0 || size == 0 {
		return ""
	}
	path := syscall.UTF16ToString(buffer[:size])
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
