package proxy

import (
	"bytes"
	"io"
	"net"
	"os/exec"
	"sync"
	"testing"

	"naiveswitcher/internal/types"
)

func BenchmarkTCPForward1MiB(b *testing.B) {
	front, cleanup := benchmarkProxy(b, forwardBufferSize)
	defer cleanup()
	payload := bytes.Repeat([]byte("x"), 1<<20)
	response := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := net.Dial("tcp", front)
		if err != nil {
			b.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { _, err := io.Copy(conn, bytes.NewReader(payload)); written <- err }()
		if _, err := io.ReadFull(conn, response); err != nil {
			b.Fatal(err)
		}
		if err := <-written; err != nil {
			b.Fatal(err)
		}
		conn.Close()
	}
}

func BenchmarkTCPForwardStream1MiB(b *testing.B) {
	benchmarkTCPStream(b, forwardBufferSize)
}

func BenchmarkTCPForwardBufferSizes(b *testing.B) {
	for _, size := range []struct {
		name  string
		bytes int
	}{
		{"8KiB", 8 * 1024},
		{"16KiB", 16 * 1024},
		{"32KiB", 32 * 1024},
		{"64KiB", 64 * 1024},
		{"128KiB", 128 * 1024},
		{"256KiB", 256 * 1024},
	} {
		b.Run(size.name, func(b *testing.B) { benchmarkTCPStream(b, size.bytes) })
	}
}

func benchmarkTCPStream(b *testing.B, bufferSize int) {
	front, cleanup := benchmarkProxy(b, bufferSize)
	defer cleanup()
	conn, err := net.Dial("tcp", front)
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	payload := bytes.Repeat([]byte("x"), 1<<20)
	response := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		written := make(chan error, 1)
		go func() { _, err := io.Copy(conn, bytes.NewReader(payload)); written <- err }()
		if _, err := io.ReadFull(conn, response); err != nil {
			b.Fatal(err)
		}
		if err := <-written; err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkProxy(b *testing.B, bufferSize int) (string, func()) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	front, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		upstream.Close()
		b.Fatal(err)
	}
	state := &types.GlobalState{NaiveCmd: &exec.Cmd{}}
	pool := &sync.Pool{New: func() any { return make([]byte, bufferSize) }}
	switches := make(chan types.SwitchRequest, 1)
	go func() {
		for {
			conn, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	go func() {
		for {
			conn, err := front.Accept()
			if err != nil {
				return
			}
			go handleConnection(state, conn, pool, switches, upstream.Addr().String())
		}
	}()
	return front.Addr().String(), func() { front.Close(); upstream.Close() }
}
