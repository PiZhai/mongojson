//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openDefaultBrowser(target string) error {
	operation, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	url, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW").Call(
		0,
		uintptr(unsafe.Pointer(operation)),
		uintptr(unsafe.Pointer(url)),
		0,
		0,
		windows.SW_SHOWNORMAL,
	)
	if result <= 32 {
		return fmt.Errorf("open default browser failed with ShellExecute result %d: %w", result, callErr)
	}
	return nil
}
