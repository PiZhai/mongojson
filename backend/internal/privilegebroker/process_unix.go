//go:build !windows

package privilegebroker

import (
	"context"
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

func capabilityRuntimeLaunchSelfTest() error { return nil }

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

func runBrokerCommand(ctx context.Context, command *exec.Cmd) (brokerCommandResult, error) {
	return runBrokerCommandWithProfile(ctx, command, capabilityTokenProfileProduction)
}

func runBrokerCommandWithProfile(ctx context.Context, command *exec.Cmd, _ capabilityTokenProfile) (brokerCommandResult, error) {
	if err := ctx.Err(); err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	cleanup, err := configureBrokerProcess(command)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	defer cleanup()
	if err := command.Start(); err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	guard, err := attachBrokerProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return brokerCommandResult{exitCode: -1}, err
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case err := <-waited:
		_ = guard.Close()
		result := brokerCommandResult{exitCode: -1}
		if command.ProcessState != nil {
			result.exitCode = command.ProcessState.ExitCode()
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	case <-ctx.Done():
		_ = guard.Terminate()
		_ = command.Process.Kill()
		<-waited
		_ = guard.Close()
		return brokerCommandResult{exitCode: -1}, ctx.Err()
	}
}
