//go:build unix

package switcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
