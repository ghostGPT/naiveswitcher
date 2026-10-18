package types

import (
	"os/exec"
	"testing"
	"time"
)

func BenchmarkProxySnapshotParallel(b *testing.B) {
	state := &GlobalState{NaiveCmd: &exec.Cmd{}}
	state.SetCurrentServer("https://benchmark.invalid")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			state.ProxySnapshot()
		}
	})
}

func BenchmarkFailureStorm(b *testing.B) {
	for _, parallel := range []bool{false, true} {
		name := "serial"
		if parallel {
			name = "parallel"
		}
		b.Run(name, func(b *testing.B) {
			state := &GlobalState{}
			requests := make(chan SwitchRequest, 100)
			req := SwitchRequest{Type: "avoid_auto", AvoidServer: "https://benchmark.invalid"}
			fail := func() {
				if state.RecordFailure(0, time.Now()) {
					state.QueueAutoSwitch(requests, req)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			if parallel {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						fail()
					}
				})
			} else {
				for i := 0; i < b.N; i++ {
					fail()
				}
			}
			b.StopTimer()
			if b.N >= FailureThreshold && len(requests) != 1 {
				b.Fatalf("expected one queued request, got %d", len(requests))
			}
		})
	}
}
