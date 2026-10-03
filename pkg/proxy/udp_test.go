package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"naiveswitcher/internal/uot"
)

func fakeNaive(t testing.TB) (string, <-chan string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	targets := make(chan string, 16)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(30 * time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(c, greeting[:]); err != nil {
					return
				}
				if greeting != [3]byte{5, 1, 0} {
					return
				}
				c.Write([]byte{5, 0})
				if _, err := io.ReadFull(c, greeting[:]); err != nil || greeting != [3]byte{5, 1, 0} {
					return
				}
				host, port, err := uot.ReadAddress(c)
				if err != nil {
					return
				}
				c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				if host == "failure.test" {
					return
				}
				if host == "tcp.test" {
					payload, _ := io.ReadAll(c)
					c.Write(payload)
					return
				}
				if uot.HostPort(host, port) != uot.Authority {
					return
				}
				host, port, err = uot.ReadRequest(c)
				if err != nil {
					return
				}
				targets <- uot.HostPort(host, port)
				buf := make([]byte, uot.MaxPayload)
				for {
					p, err := uot.ReadPacket(c, buf)
					if err != nil {
						return
					}
					if uot.WritePacket(c, p) != nil {
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String(), targets
}

func udpListener(t testing.TB, upstream string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeSOCKS(ctx, l, upstream) }()
	t.Cleanup(cancel)
	return l.Addr().String(), cancel, done
}

func associate(t testing.TB, address string) (net.Conn, *net.UDPConn, *net.UDPAddr) {
	t.Helper()
	c, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(3 * time.Second))
	// Pipelined greeting and request also exercise buffered data handling.
	c.Write([]byte{5, 1, 0, 5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	var response [5]byte
	if _, err := io.ReadFull(c, response[:]); err != nil || response != [5]byte{5, 0, 5, 0, 0} {
		t.Fatalf("handshake %x %v", response, err)
	}
	host, port, err := uot.ReadAddress(c)
	if err != nil {
		t.Fatal(err)
	}
	u, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { u.Close() })
	return c, u, &net.UDPAddr{IP: net.ParseIP(host), Port: int(port)}
}

func TestUDPAssociationMultipleTargetsAndPacketBoundaries(t *testing.T) {
	upstream, targets := fakeNaive(t)
	address, cancel, done := udpListener(t, upstream)
	control, udp, relay := associate(t, address)
	for _, host := range []string{"1.2.3.4", "::1", "example.test"} {
		addr, _ := uot.Address(host, 3478)
		for _, p := range [][]byte{nil, []byte("voice"), bytes.Repeat([]byte{3}, 1200), bytes.Repeat([]byte{4}, 8000)} {
			frame := append(append([]byte{0, 0, 0}, addr...), p...)
			udp.SetDeadline(time.Now().Add(3 * time.Second))
			if _, err := udp.WriteToUDP(frame, relay); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 65535)
			n, _, err := udp.ReadFromUDP(buf)
			if err != nil || !bytes.Equal(buf[:n], frame) {
				t.Fatalf("%s payload %d received %d err %v", host, len(p), n, err)
			}
		}
	}
	for i := 0; i < 3; i++ {
		select {
		case <-targets:
		case <-time.After(time.Second):
			t.Fatal("missing distinct flow")
		}
	}
	control.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("association did not stop")
	}
}

func TestUDPRejectsFragmentsAndOtherSourcePorts(t *testing.T) {
	upstream, _ := fakeNaive(t)
	address, cancel, done := udpListener(t, upstream)
	_, udp, relay := associate(t, address)
	addr, _ := uot.Address("1.2.3.4", 53)
	frame := append(append([]byte{0, 0, 0}, addr...), byte(7))
	udp.SetDeadline(time.Now().Add(time.Second))
	udp.WriteToUDP(frame, relay)
	buf := make([]byte, 100)
	if _, _, err := udp.ReadFromUDP(buf); err != nil {
		t.Fatal(err)
	}
	other, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetDeadline(time.Now().Add(100 * time.Millisecond))
	other.WriteToUDP(frame, relay)
	if _, _, err := other.ReadFromUDP(buf); err == nil {
		t.Fatal("relayed an unassociated source")
	}
	frame[2] = 1
	udp.SetDeadline(time.Now().Add(100 * time.Millisecond))
	udp.WriteToUDP(frame, relay)
	if _, _, err := udp.ReadFromUDP(buf); err == nil {
		t.Fatal("accepted fragmented SOCKS datagram")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("leaked association")
	}
}

func TestUDPShutdownDuringHandshake(t *testing.T) {
	address, cancel, done := udpListener(t, "127.0.0.1:1")
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
		t.Fatal("handshake blocked shutdown")
	}
}

func TestUDPConcurrentAssociations(t *testing.T) {
	upstream, _ := fakeNaive(t)
	address, cancel, done := udpListener(t, upstream)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		control, udp, relay := associate(t, address)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer control.Close()
			frame := []byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53, 42}
			udp.SetDeadline(time.Now().Add(time.Second))
			udp.WriteToUDP(frame, relay)
			var b [100]byte
			n, _, err := udp.ReadFromUDP(b[:])
			if err != nil || !bytes.Equal(b[:n], frame) {
				t.Errorf("echo=%x err=%v", b[:n], err)
			}
		}()
	}
	wg.Wait()
	cancel()
	<-done
}
