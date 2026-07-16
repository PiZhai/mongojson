//go:build !windows

package steward

import (
	"os/exec"
	"syscall"
)

type unixRuntimeProcessTree struct{ pid int }

func configureRuntimeProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachRuntimeProcessTree(command *exec.Cmd) (runtimeProcessTreeGuard, error) {
	return &unixRuntimeProcessTree{pid: command.Process.Pid}, nil
}

func (g *unixRuntimeProcessTree) Terminate() error {
	if g == nil || g.pid <= 0 {
		return nil
	}
	return syscall.Kill(-g.pid, syscall.SIGKILL)
}

func (g *unixRuntimeProcessTree) Close() error {
	_ = g.Terminate()
	g.pid = 0
	return nil
}
