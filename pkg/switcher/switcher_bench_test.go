package switcher

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
)

// Includes the real queue consumer rejecting requests during cooldown.
func BenchmarkCooldownFailureStorm(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &types.GlobalState{AppContext: ctx, NaiveCmd: &exec.Cmd{}, ServerDownPriority: make(map[string]int)}
	state.SetCurrentServer("https://benchmark.invalid")
	state.BeginAutoSwitch(time.Now())
	requests := make(chan types.SwitchRequest, 100)
	done := make(chan struct{})
	go func() { defer close(done); Switcher(state, &config.Config{}, requests) }()
	defer func() { cancel(); <-done }()
	req := types.SwitchRequest{Type: "avoid_auto", AvoidServer: state.CurrentServer()}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if state.RecordFailure(0, time.Now()) {
				state.QueueAutoSwitch(requests, req)
			}
		}
	})
	b.StopTimer()
}
