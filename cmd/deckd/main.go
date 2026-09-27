package main

import (
	"context"
	"os"
	"os/signal"
	"patchbay/internal/cli"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.RunContext(ctx, "deckd", os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
