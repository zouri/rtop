package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"rtop/internal/app"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := app.NewCommand(version, commit, date, os.Stdout, os.Stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "rtop:", err)
		os.Exit(1)
	}
}
