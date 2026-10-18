//go:build unix

package switcher

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
)

// Opt-in resource soak: real OS processes, without a remote server or subscription.
func TestProfileProcessLifecycleResources(t *testing.T) {
	if os.Getenv("NAIVE_PROFILE_RESOURCES") != "1" {
		t.Skip("set NAIVE_PROFILE_RESOURCES=1 for process resource profiling")
	}
	for _, crash := range []bool{false, true} {
		name, script := "restart", "#!/bin/sh\nexec sleep 30\n"
		if crash {
			name, script = "crash", "#!/bin/sh\nexit 1\n"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			if err := os.WriteFile(filepath.Join(base, "fake-naive"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			oldBase, oldNaive := common.BasePath, common.GetNaive()
			common.BasePath = base
			common.SetNaive("fake-naive")
			defer func() { common.BasePath = oldBase; common.SetNaive(oldNaive) }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			state := &types.GlobalState{AppContext: ctx}
			stop := func() { state.NaiveCmdLock.Lock(); defer state.NaiveCmdLock.Unlock(); stopNaiveUnsafe(state) }
			defer stop()
			cycle := func() {
				if err := RestartNaive(state, "https://fixture.invalid"); err != nil {
					t.Fatal(err)
				}
				if crash {
					select {
					case <-state.NaiveDone:
					case <-time.After(3 * time.Second):
						t.Fatal("crashed process not reaped")
					}
				}
				stop()
			}
			for i := 0; i < 10; i++ {
				cycle()
			}
			sample := func() (uint64, int, int) {
				runtime.GC()
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				fdPath := "/dev/fd"
				if runtime.GOOS == "linux" {
					fdPath = "/proc/self/fd"
				}
				dir, err := os.Open(fdPath)
				if err != nil {
					t.Fatal(err)
				}
				fds, err := dir.Readdirnames(-1)
				dir.Close()
				if err != nil {
					t.Fatal(err)
				}
				return m.HeapAlloc, runtime.NumGoroutine(), len(fds)
			}
			heap0, g0, fd0 := sample()
			t.Logf("warm heap=%d goroutines=%d fds=%d", heap0, g0, fd0)
			for batch := 1; batch <= 4; batch++ {
				for i := 0; i < 50; i++ {
					cycle()
				}
				heap, g, fd := sample()
				t.Logf("cycles=%d heap=%d delta=%d goroutines=%d fds=%d", batch*50, heap, int64(heap)-int64(heap0), g, fd)
				if g > g0+2 || fd > fd0+2 {
					t.Fatal("process lifecycle retained goroutines or descriptors")
				}
				if int64(heap)-int64(heap0) > 2<<20 {
					t.Fatal("process lifecycle retained excessive heap")
				}
			}
		})
	}
}
