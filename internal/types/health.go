package types

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	FailureWindow      = 30 * time.Second
	FailureThreshold   = 11
	AutoSwitchCooldown = 30 * time.Second
)

type healthState struct {
	mu           sync.Mutex
	generation   uint64
	failures     []time.Time
	pending      bool
	lastAttempt  time.Time
	lastRecovery time.Time
}

func (s *GlobalState) Generation() uint64 {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	return s.health.generation
}

// AdvanceGeneration invalidates observations from every previous process, even
// when the replacement uses the same server.
func (s *GlobalState) AdvanceGeneration() {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	s.health.generation++
	s.health.lastAttempt = time.Now()
	s.health.failures = s.health.failures[:0]
	atomic.StoreInt32(&s.ErrorCount, 0)
}

// RecordFailure counts failures in a bounded sliding window. Completed healthy
// long-lived connections must not cancel recent failures from new connections.
func (s *GlobalState) RecordFailure(generation uint64, now time.Time) bool {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if generation != s.health.generation || atomic.LoadInt32(&s.Switching) != 0 {
		return false
	}
	s.pruneFailures(now)
	failures := s.health.failures
	if len(failures) == FailureThreshold {
		// Keep the backing array: slicing off its head would allocate again
		// every few failures during an outage.
		copy(failures, failures[1:])
		failures = failures[:FailureThreshold-1]
	}
	s.health.failures = append(failures, now)
	atomic.StoreInt32(&s.ErrorCount, int32(len(s.health.failures)))
	return len(s.health.failures) >= FailureThreshold
}

// QueueAutoSwitch coalesces requests while queued AND while being processed.
// A full queue does not leave the pending flag stuck.
func (s *GlobalState) QueueAutoSwitch(ch chan<- SwitchRequest, req SwitchRequest) bool {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if s.health.pending || req.Generation != s.health.generation {
		return false
	}
	req.Coalesced = true
	select {
	case ch <- req:
		s.health.pending = true
		return true
	default:
		return false
	}
}

func (s *GlobalState) FinishAutoSwitch() {
	s.health.mu.Lock()
	s.health.pending = false
	s.health.mu.Unlock()
}

func (s *GlobalState) BeginAutoSwitch(now time.Time) bool {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if !s.health.lastAttempt.IsZero() && now.Sub(s.health.lastAttempt) < AutoSwitchCooldown {
		return false
	}
	s.health.lastAttempt = now
	s.health.failures = s.health.failures[:0]
	atomic.StoreInt32(&s.ErrorCount, 0)
	return true
}

// Recovery is independently throttled: a dead local process bypasses node cooldown.
func (s *GlobalState) BeginRecovery(now time.Time) bool {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	if !s.health.lastRecovery.IsZero() && now.Sub(s.health.lastRecovery) < 5*time.Second {
		return false
	}
	s.health.lastRecovery = now
	return true
}

// RecentFailureCount also expires idle counters used by status and retry checks.
func (s *GlobalState) RecentFailureCount(now time.Time) int {
	s.health.mu.Lock()
	defer s.health.mu.Unlock()
	s.pruneFailures(now)
	return len(s.health.failures)
}

func (s *GlobalState) pruneFailures(now time.Time) {
	failures := s.health.failures[:0]
	for _, at := range s.health.failures {
		if now.Sub(at) < FailureWindow {
			failures = append(failures, at)
		}
	}
	s.health.failures = failures
	atomic.StoreInt32(&s.ErrorCount, int32(len(failures)))
}
