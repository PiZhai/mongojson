//go:build windows

package steward

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRuntimeProcessTreeHelper(t *testing.T) {
	mode := ""
	pidFile := ""
	for index, argument := range os.Args {
		if argument == "helper-parent" || argument == "helper-child" {
			mode = argument
			if index+1 < len(os.Args) {
				pidFile = os.Args[index+1]
			}
			break
		}
	}
	if mode == "" {
		return
	}
	if mode == "helper-parent" {
		time.Sleep(300 * time.Millisecond)
		child := exec.Command(os.Args[0], "-test.run=TestRuntimeProcessTreeHelper", "--", "helper-child")
		if err := child.Start(); err != nil {
			_ = os.WriteFile(pidFile, []byte("ERROR: "+err.Error()), 0o600)
			os.Exit(2)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(3)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestRuntimeProcessTreeCancellationKillsDescendantOnWindows(t *testing.T) {
	pidFile := t.TempDir() + `\child.pid`
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.Command(os.Args[0], "-test.run=TestRuntimeProcessTreeHelper", "--", "helper-parent", pidFile)
	done := make(chan error, 1)
	go func() { done <- runRuntimeCommand(ctx, command) }()
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(pidFile)
		if err == nil {
			if strings.HasPrefix(string(payload), "ERROR:") {
				cancel()
				t.Fatal(string(payload))
			}
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(payload)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		cancel()
		t.Fatal("runtime process helper did not create a descendant")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("runtime command cancellation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime process tree did not terminate")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !windowsProcessActive(uint32(childPID)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived Job Object termination", childPID)
}

func windowsProcessActive(pid uint32) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == 259
}
