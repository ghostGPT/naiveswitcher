package main

import (
	"context"
	"testing"
	"time"

	"naiveswitcher/internal/types"
)

func TestAutoSwitchLoopStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	switches := make(chan types.SwitchRequest, 1)
	updates := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		autoSwitchLoop(ctx, &types.GlobalState{}, time.Millisecond, switches, updates)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auto switch loop did not stop")
	}
}
