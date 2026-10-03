package proxy

import (
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestProfileUDPFrontendMemory(t *testing.T) {
	if os.Getenv("UOT_PROFILE_MEMORY") != "1" {
		t.Skip("manual memory profile")
	}
	upstream, targets := fakeNaive(t)
	address, cancel, done := udpListener(t, upstream)
	var before, active, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	beforeG := runtime.NumGoroutine()
	var controls []net.Conn
	var sockets []*net.UDPConn
	for i := 0; i < 64; i++ {
		control, udp, relay := associate(t, address)
		controls = append(controls, control)
		sockets = append(sockets, udp)
		udp.SetDeadline(time.Now().Add(3 * time.Second))
		frame := append([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53}, make([]byte, 320)...)
		udp.WriteToUDP(frame, relay)
		if _, _, err := udp.ReadFromUDP(make([]byte, 2048)); err != nil {
			t.Fatal(err)
		}
		<-targets
	}
	runtime.GC()
	runtime.ReadMemStats(&active)
	activeG := runtime.NumGoroutine()
	for _, c := range controls {
		c.Close()
	}
	for _, u := range sockets {
		u.Close()
	}
	controls = nil
	sockets = nil
	cancel()
	<-done
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > beforeG+2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("64 UDP associations + fake upstream heap before=%d active=%d after=%d; goroutines before=%d active=%d after=%d", before.HeapAlloc, active.HeapAlloc, after.HeapAlloc, beforeG, activeG, runtime.NumGoroutine())
	t.Logf("stack bytes before=%d active=%d after=%d", before.StackInuse, active.StackInuse, after.StackInuse)
	if int64(after.HeapAlloc)-int64(before.HeapAlloc) > 2<<20 {
		t.Fatal("excess retained heap after association close")
	}
	if runtime.NumGoroutine() > beforeG+2 {
		t.Fatal("goroutines survived association close")
	}
}
