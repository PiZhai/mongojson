package servicecontrol

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func Run(name string, run func(context.Context) error) error {
	return runPlatform(defaultString(name, DefaultName()), run)
}

func runWithSignals(run func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx)
}
