package types

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailuresExpireAndOldGenerationIsIgnored(t *testing.T) {
	s := &GlobalState{}
	now := time.Now()
	for i := 0; i < FailureThreshold-1; i++ {
		if s.RecordFailure(0, now) {
			t.Fatal("switched before threshold")
		}
	}
	if s.RecordFailure(0, now.Add(FailureWindow)) {
		t.Fatal("expired failures triggered switch")
	}
	if got := atomic.LoadInt32(&s.ErrorCount); got != 1 {
		t.Fatalf("count=%d", got)
	}
	for i := 0; i < FailureThreshold-1; i++ {
		s.RecordFailure(0, now.Add(FailureWindow))
	}
	if !s.RecordFailure(0, now.Add(FailureWindow)) {
		t.Fatal("recent failures did not trigger switch")
	}
	s.AdvanceGeneration()
	for i := 0; i < FailureThreshold*2; i++ {
		s.RecordFailure(0, now.Add(FailureWindow))
	}
	if atomic.LoadInt32(&s.ErrorCount) != 0 {
		t.Fatal("old process poisoned replacement")
	}
	atomic.StoreInt32(&s.Switching, 1)
	s.RecordFailure(s.Generation(), now)
	if atomic.LoadInt32(&s.ErrorCount) != 0 {
		t.Fatal("planned restart counted as failure")
	}
}

func TestAutoRequestsCoalesceUntilCompleted(t *testing.T) {
	s := &GlobalState{}
	ch := make(chan SwitchRequest, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.QueueAutoSwitch(ch, SwitchRequest{Type: "avoid_auto"}) }()
	}
	wg.Wait()
	if len(ch) != 1 {
		t.Fatalf("queued %d duplicate requests", len(ch))
	}
	req := <-ch
	if !req.Coalesced {
		t.Fatal("request not marked managed")
	}
	if s.QueueAutoSwitch(ch, SwitchRequest{Type: "auto"}) {
		t.Fatal("duplicate accepted while processing")
	}
	s.AdvanceGeneration()
	s.FinishAutoSwitch()
	if s.QueueAutoSwitch(ch, req) {
		t.Fatal("old generation queued")
	}
	if !s.QueueAutoSwitch(ch, SwitchRequest{Type: "recover", Generation: s.Generation()}) {
		t.Fatal("new recovery not accepted")
	}
}

func TestFullQueueDoesNotBlockFutureRecovery(t *testing.T) {
	s := &GlobalState{}
	ch := make(chan SwitchRequest, 1)
	ch <- SwitchRequest{Type: "select"}
	if s.QueueAutoSwitch(ch, SwitchRequest{Type: "auto"}) {
		t.Fatal("full queue accepted request")
	}
	<-ch
	if !s.QueueAutoSwitch(ch, SwitchRequest{Type: "recover"}) {
		t.Fatal("pending flag stuck")
	}
}

func TestCooldownAndIndependentRecovery(t *testing.T) {
	s := &GlobalState{}
	now := time.Now()
	if !s.BeginAutoSwitch(now) || s.BeginAutoSwitch(now.Add(time.Second)) {
		t.Fatal("auto cooldown not enforced")
	}
	if !s.BeginRecovery(now) {
		t.Fatal("node cooldown blocked process recovery")
	}
	if s.BeginRecovery(now.Add(time.Second)) {
		t.Fatal("crash loop not throttled")
	}
	if !s.BeginRecovery(now.Add(5*time.Second)) || !s.BeginAutoSwitch(now.Add(AutoSwitchCooldown)) {
		t.Fatal("retry did not resume")
	}
}

func TestStatusExpiresFailuresWithoutNewTraffic(t *testing.T) {
	s := &GlobalState{}
	now := time.Now()
	s.RecordFailure(0, now)
	if s.RecentFailureCount(now) != 1 {
		t.Fatal("recent failure missing")
	}
	if s.RecentFailureCount(now.Add(FailureWindow)) != 0 || atomic.LoadInt32(&s.ErrorCount) != 0 {
		t.Fatal("idle failure count did not expire")
	}
}

func TestFailureWindowRetainsNewestSamplesAtCapacity(t *testing.T) {
	state := &GlobalState{}
	now := time.Now()
	for i := 0; i < FailureThreshold; i++ {
		state.RecordFailure(0, now)
	}
	state.RecordFailure(0, now.Add(FailureWindow/2))
	// All initial samples expire; the newest one must survive compaction.
	if got := state.RecentFailureCount(now.Add(FailureWindow)); got != 1 {
		t.Fatalf("recent failures=%d, want 1", got)
	}
	// Warm the bounded buffer before checking steady-state allocation pressure.
	for i := 0; i < FailureThreshold; i++ {
		state.RecordFailure(0, now.Add(FailureWindow))
	}
	if allocs := testing.AllocsPerRun(1000, func() { state.RecordFailure(0, now.Add(FailureWindow)) }); allocs != 0 {
		t.Fatalf("steady-state failures allocate: %f", allocs)
	}
}
