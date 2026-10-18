package proxy

import (
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestProfileTCPFrontendMemory(t *testing.T) {
	if os.Getenv("NAIVE_PROFILE_RESOURCES") != "1" {
		t.Skip("set NAIVE_PROFILE_RESOURCES=1 for TCP resource profiling")
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	beforeG := runtime.NumGoroutine()
	for wave := 1; wave <= 4; wave++ {
		address, cleanup := benchmarkProxy(t, forwardBufferSize)
		conns := make([]net.Conn, 0, 64)
		for i := 0; i < 64; i++ {
			c, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
			c.SetDeadline(time.Now().Add(10 * time.Second))
			if _, err = c.Write([]byte("ping")); err != nil {
				t.Fatal(err)
			}
			var response [4]byte
			if _, err = io.ReadFull(c, response[:]); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		var active runtime.MemStats
		runtime.ReadMemStats(&active)
		activeG := runtime.NumGoroutine()
		for _, c := range conns {
			c.Close()
		}
		cleanup()
		runtime.GC()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		t.Logf("wave=%d activeHeap=%d activeG=%d afterHeap=%d retained=%d goroutines=%d baselineG=%d", wave, active.HeapAlloc, activeG, after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc), runtime.NumGoroutine(), beforeG)
		if runtime.NumGoroutine() > beforeG+2 {
			t.Fatal("TCP goroutines survived cleanup")
		}
		if int64(after.HeapAlloc)-int64(before.HeapAlloc) > 2<<20 {
			t.Fatal("excess retained TCP heap")
		}
	}
}
