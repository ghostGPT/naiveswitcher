//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"
)

func setupSignalHandler(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt)
}
