//go:build !windows

package privilegebroker

import (
	"os/exec"
	"syscall"
)

type unixProcessTree struct{ pid int }

func configureBrokerProcess(command *exec.Cmd) (func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return func() {}, nil
}

// Broker capabilities are absolute executables and receive no ambient service
// environment. This avoids turning PATH, HOME, or temporary-directory values
// controlled by the service launcher into an elevated input channel.
func brokerEnvironment() []string { return []string{"LANG=C.UTF-8"} }

func attachBrokerProcessTree(command *exec.Cmd) (processTreeGuard, error) {
	return &unixProcessTree{pid: command.Process.Pid}, nil
}

func (g *unixProcessTree) Terminate() error {
	if g == nil || g.pid <= 0 {
		return nil
	}
	return syscall.Kill(-g.pid, syscall.SIGKILL)
}

func (g *unixProcessTree) Close() error {
	g.pid = 0
	return nil
}
