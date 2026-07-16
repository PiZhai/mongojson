package privilegebroker

import (
	"context"
	"os/exec"
)

type processTreeGuard interface {
	Terminate() error
	Close() error
}

func runBrokerCommand(ctx context.Context, command *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanup, err := configureBrokerProcess(command)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := command.Start(); err != nil {
		return err
	}
	guard, err := attachBrokerProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	select {
	case err := <-waited:
		_ = guard.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	case <-ctx.Done():
		_ = guard.Terminate()
		_ = command.Process.Kill()
		<-waited
		_ = guard.Close()
		return ctx.Err()
	}
}
