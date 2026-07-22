//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openDefaultBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "linux":
		command = exec.Command("xdg-open", target)
	default:
		return fmt.Errorf("opening the default browser is unsupported on %s", runtime.GOOS)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open default browser: %w", err)
	}
	return nil
}
