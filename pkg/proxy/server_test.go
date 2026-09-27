package proxy

import (
	"io"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"naiveswitcher/internal/types"
)

func TestServeTCPReturnsWhenListenerCloses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		ServeTCP(&types.GlobalState{}, listener, make(chan types.SwitchRequest, 1))
		close(done)
	}()
	listener.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeTCP did not return after listener closed")
	}
}

func TestHandleConnectionReturnsWhenSwitchQueueIsFull(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	requests := make(chan types.SwitchRequest, 1)
	requests <- types.SwitchRequest{Type: "auto"}
	done := make(chan struct{})
	go func() {
		HandleConnection(&types.GlobalState{}, server, nil, requests)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection blocked on a full switch queue")
	}
}

func TestTCPForwardCountsNaiveFailurePacket(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request [1]byte
		io.ReadFull(conn, request[:])
		conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0})
	}()
	state := &types.GlobalState{NaiveCmd: &exec.Cmd{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := front.Accept()
		if err != nil {
			return
		}
		pool := &sync.Pool{New: func() any { return make([]byte, 32*1024) }}
		handleConnection(state, conn, pool, make(chan types.SwitchRequest, 1), upstream.Addr().String())
	}()
	client, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if len(response) != 12 || atomic.LoadInt32(&state.ErrorCount) != 1 {
		t.Fatalf("response length = %d, error count = %d", len(response), atomic.LoadInt32(&state.ErrorCount))
	}
}

func TestTCPForwardPreservesHalfClose(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer front.Close()
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
		conn.Write([]byte("reply"))
	}()
	state := &types.GlobalState{NaiveCmd: &exec.Cmd{}}
	proxyDone := make(chan struct{})
	go func() {
		defer close(proxyDone)
		conn, err := front.Accept()
		if err != nil {
			return
		}
		pool := &sync.Pool{New: func() any { return make([]byte, 32*1024) }}
		handleConnection(state, conn, pool, make(chan types.SwitchRequest, 1), upstream.Addr().String())
	}()
	client, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "reply" {
		t.Fatalf("response = %q, want reply", response)
	}
	<-proxyDone
	<-upstreamDone
}
