//go:build unix

package switcher

import (
	"context"
	"naiveswitcher/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
)

func TestRestartNaiveRestoresPreviousServer(t *testing.T) {
	base := t.TempDir()
	name := "fake-naive"
	if err := os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	previousBase, previousNaive := common.BasePath, common.GetNaive()
	common.BasePath = base
	common.SetNaive(name)
	t.Cleanup(func() {
		common.BasePath = previousBase
		common.SetNaive(previousNaive)
	})

	state := &types.GlobalState{AppContext: context.Background()}
	state.SetCurrentServer("https://old.example")
	t.Cleanup(func() {
		state.NaiveCmdLock.Lock()
		defer state.NaiveCmdLock.Unlock()
		stopNaiveUnsafe(state)
	})
	if err := RestartNaive(state, ""); err == nil {
		t.Fatal("expected invalid target to fail")
	}
	if !state.IsNaiveRunning() || state.CurrentServer() != "https://old.example" {
		t.Fatalf("previous server was not restored: running=%v current=%q", state.IsNaiveRunning(), state.CurrentServer())
	}
}

func TestMonitorRecoversCrashedProcessWhilePaused(t *testing.T) {
	base := t.TempDir()
	name := "fake-naive"
	// First invocation exits unexpectedly; the replacement remains running.
	script := "#!/bin/sh\nif [ ! -f \"$0.started\" ]; then touch \"$0.started\"; exit 1; fi\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(base, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	oldBase, oldNaive := common.BasePath, common.GetNaive()
	common.BasePath = base
	common.SetNaive(name)
	defer func() { common.BasePath = oldBase; common.SetNaive(oldNaive) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state := &types.GlobalState{AppContext: ctx, AutoSwitchPaused: true, ServerDownPriority: make(map[string]int)}
	if err := RestartNaive(state, "https://original.example"); err != nil {
		t.Fatal(err)
	}
	initialGeneration := state.Generation()
	select {
	case <-state.NaiveDone:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not exit")
	}
	if state.IsNaiveRunning() {
		t.Fatal("exited process reported alive")
	}
	requests := make(chan types.SwitchRequest, 100)
	switchDone, monitorDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(switchDone); Switcher(state, &config.Config{}, requests) }()
	go func() { defer close(monitorDone); monitorProcess(state, requests, 10*time.Millisecond) }()
	defer func() {
		cancel()
		<-switchDone
		<-monitorDone
		state.NaiveCmdLock.Lock()
		defer state.NaiveCmdLock.Unlock()
		stopNaiveUnsafe(state)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if state.IsNaiveRunning() && state.Generation() > initialGeneration {
			if state.CurrentServer() != "https://original.example" {
				t.Fatal("recovery changed locked server")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("monitor did not recover crashed process")
}

func TestRecoveryRetriesLockedNodeWhenCurrentIsEmpty(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "fake-naive"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldBase, oldNaive := common.BasePath, common.GetNaive()
	common.BasePath = base
	common.SetNaive("fake-naive")
	defer func() { common.BasePath = oldBase; common.SetNaive(oldNaive) }()
	state := &types.GlobalState{AppContext: context.Background(), AutoSwitchPaused: true, LockedServer: "https://locked.example"}
	defer func() { state.NaiveCmdLock.Lock(); defer state.NaiveCmdLock.Unlock(); stopNaiveUnsafe(state) }()
	if err := processRequest(state, &config.Config{}, types.SwitchRequest{Type: "recover"}); err != nil {
		t.Fatal(err)
	}
	if !state.IsNaiveRunning() || state.CurrentServer() != state.LockedServer {
		t.Fatal("failed startup did not retry locked node")
	}
}
