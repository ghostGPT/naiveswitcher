package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/internal/uot"
)

func mixedListener(t *testing.T, upstream string, useSwitcher bool) (string, *types.GlobalState, context.CancelFunc, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	state := &types.GlobalState{NaiveCmd: &exec.Cmd{}, AppContext: ctx}
	done := make(chan error, 1)
	go func() {
		if !useSwitcher {
			done <- ServeSOCKS(ctx, l, upstream)
			return
		}
		pool := &sync.Pool{New: func() any { return make([]byte, forwardBufferSize) }}
		done <- serveSOCKS(ctx, l, upstream, func(ctx context.Context, c net.Conn) {
			handleConnectionContext(ctx, state, c, pool, make(chan types.SwitchRequest, 1), upstream)
		})
	}()
	return l.Addr().String(), state, cancel, done
}

func TestSharedSOCKSTCPAndUDP(t *testing.T) {
	for _, switcher := range []bool{false, true} {
		name := "standalone"
		if switcher {
			name = "switcher"
		}
		t.Run(name, func(t *testing.T) {
			upstream, _ := fakeNaive(t)
			address, _, cancel, done := mixedListener(t, upstream, switcher)
			control, udp, relay := associate(t, address)
			defer control.Close()
			// Keep UDP active while TCP connects on the exact same port.
			frame := []byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53, 42}
			udp.SetDeadline(time.Now().Add(3 * time.Second))
			udp.WriteToUDP(frame, relay)
			var b [100]byte
			if n, _, err := udp.ReadFromUDP(b[:]); err != nil || !bytes.Equal(b[:n], frame) {
				t.Fatalf("UDP response %x: %v", b[:n], err)
			}
			c, err := net.Dial("tcp", address)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(3 * time.Second))
			dest, _ := uot.Address("tcp.test", 443)
			// Pipeline greeting, CONNECT, and payload; response waits for EOF.
			request := append([]byte{5, 2, 2, 0, 5, 1, 0}, dest...)
			c.Write(append(request, []byte("half-close reply")...))
			c.(*net.TCPConn).CloseWrite()
			response, err := io.ReadAll(c)
			want := append([]byte{5, 0, 5, 0, 0, 1, 0, 0, 0, 0, 0, 0}, []byte("half-close reply")...)
			if err != nil || !bytes.Equal(response, want) {
				t.Fatalf("TCP response %x: %v", response, err)
			}
			udp.WriteToUDP(frame, relay)
			if n, _, err := udp.ReadFromUDP(b[:]); err != nil || !bytes.Equal(b[:n], frame) {
				t.Fatalf("UDP after TCP response %x: %v", b[:n], err)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("shared listener leaked connections")
			}
		})
	}
}

func TestSharedSOCKSFailureAccounting(t *testing.T) {
	upstream, _ := fakeNaive(t)
	address, state, cancel, done := mixedListener(t, upstream, true)
	c, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	dest, _ := uot.Address("failure.test", 443)
	c.Write(append([]byte{5, 1, 0, 5, 1, 0}, dest...))
	response, err := io.ReadAll(c)
	if err != nil || len(response) != 12 {
		t.Fatalf("failure response %x: %v", response, err)
	}
	cancel()
	<-done
	if atomic.LoadInt32(&state.ErrorCount) != 1 {
		t.Fatal("Naive failure no longer counted")
	}
}

func TestSharedSOCKSRejectsAndCancels(t *testing.T) {
	address, _, cancel, done := mixedListener(t, "127.0.0.1:1", false)
	for _, tc := range []struct{ request, response []byte }{
		{[]byte{5, 1, 2}, []byte{5, 255}},
		{[]byte{5, 1, 0, 5, 2, 0}, []byte{5, 0, 5, 7, 0, 1, 0, 0, 0, 0, 0, 0}},
	} {
		c, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write(tc.request)
		response, err := io.ReadAll(c)
		c.Close()
		if err != nil || !bytes.Equal(response, tc.response) {
			t.Fatalf("response %x: %v", response, err)
		}
	}
	c, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte{5})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("partial handshake blocked shutdown")
	}
}

func TestSOCKSTCPFragmentedGreeting(t *testing.T) {
	front, client := net.Pipe()
	defer front.Close()
	defer client.Close()
	c := &socksTCPConn{Conn: front}
	for _, p := range [][]byte{{5}, {0}} {
		if n, err := c.Write(p); n != 1 || err != nil {
			t.Fatalf("greeting %d %v", n, err)
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); c.Write([]byte("reply")) }()
	client.SetDeadline(time.Now().Add(time.Second))
	var b [5]byte
	if _, err := io.ReadFull(client, b[:]); err != nil || string(b[:]) != "reply" {
		t.Fatalf("response %q %v", b, err)
	}
	<-done
}

func TestSharedSOCKSListenerCloseCancelsUpstream(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	ready := make(chan struct{})
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		var b [3]byte
		io.ReadFull(c, b[:])
		close(ready)
		// Simulate an upstream which never answers the SOCKS handshake.
		io.Copy(io.Discard, c)
	}()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	done := make(chan error, 1)
	go func() { done <- ServeSOCKS(context.Background(), front, upstream.Addr().String()) }()
	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte{5, 1, 0, 5, 1, 0, 1, 1, 2, 3, 4, 0, 80})
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("upstream not connected")
	}
	front.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener close retained upstream TCP connection")
	}
}
