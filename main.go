package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hewitt-iso/hewitt/cmd"
)

// version is set with -ldflags at build time.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd.NewRootCommand(version).ExecuteContext(ctx); err != nil {
		cmd.PrintError(os.Stderr, err)
		os.Exit(1)
	}
}
