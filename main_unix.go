//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func setupSignalHandler(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
}
