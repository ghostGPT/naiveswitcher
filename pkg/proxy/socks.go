package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// ServeSOCKS serves TCP CONNECT and UDP ASSOCIATE on one SOCKS5 port through
// an existing official Naive client. ServeTCP also retains switcher callbacks.
func ServeSOCKS(ctx context.Context, listener net.Listener, upstream string) error {
	return serveSOCKS(ctx, listener, upstream, func(ctx context.Context, conn net.Conn) {
		defer conn.Close()
		remote, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", upstream)
		if err != nil {
			return
		}
		defer remote.Close()
		stop := context.AfterFunc(ctx, func() { remote.Close() })
		defer stop()
		done := make(chan struct{})
		go func() {
			defer close(done)
			io.Copy(remote, conn)
			closeWrite(remote)
		}()
		io.Copy(conn, remote)
		closeWrite(conn)
		closeRead(conn)
		<-done
	})
}

func serveSOCKS(ctx context.Context, listener net.Listener, upstream string, tcp func(context.Context, net.Conn)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	handshakes := make(chan struct{}, 128)
	associations := make(chan struct{}, 128)
	flows := make(chan struct{}, 256)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		select {
		case handshakes <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			conn.SetDeadline(time.Now().Add(10 * time.Second))
			command, err := readSOCKSCommand(conn)
			<-handshakes
			if err != nil {
				return
			}
			switch command {
			case 1:
				conn.SetDeadline(time.Time{})
				// Replay only the consumed handshake. Destination and any pipelined
				// payload remain unread, and the existing TCP forwarding path keeps
				// its half-close and Naive failure detection behavior.
				tcp(ctx, &socksTCPConn{Conn: conn, prefix: []byte{5, 1, 0, 5, 1, 0}})
			case 3:
				select {
				case associations <- struct{}{}:
					defer func() { <-associations }()
					serveUDPRequest(ctx, conn, upstream, flows)
				default:
					socksReply(conn, 1, nil)
				}
			default:
				socksReply(conn, 7, nil)
			}
		}()
	}
}

func readSOCKSCommand(conn net.Conn) (byte, error) {
	var header [3]byte
	if _, err := io.ReadFull(conn, header[:2]); err != nil {
		return 0, err
	}
	if header[0] != 5 || header[1] == 0 {
		return 0, errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return 0, err
	}
	if !bytes.Contains(methods, []byte{0}) {
		conn.Write([]byte{5, 255})
		return 0, errors.New("no supported SOCKS authentication method")
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return 0, err
	}
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, err
	}
	if header[0] != 5 || header[2] != 0 {
		return 0, errors.New("invalid SOCKS request")
	}
	return header[1], nil
}

// The client has already received a greeting response. Consume the upstream's
// response without forwarding it twice; preserve byte counts for error tracking.
type socksTCPConn struct {
	net.Conn
	prefix        []byte
	greetingBytes int
	response      [12]byte
	responseN     int
}

func (c *socksTCPConn) Read(p []byte) (int, error) {
	if len(c.prefix) != 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *socksTCPConn) Write(p []byte) (int, error) {
	skipped := 0
	for c.greetingBytes < 2 && skipped < len(p) {
		if p[skipped] != []byte{5, 0}[c.greetingBytes] {
			return skipped, errors.New("Naive rejected SOCKS greeting")
		}
		c.greetingBytes++
		skipped++
	}
	if skipped == len(p) {
		return skipped, nil
	}
	// Keep the CONNECT reply independent of TCP read boundaries for the
	// switcher's existing empty-response failure detection.
	c.responseN += copy(c.response[c.responseN:], p[skipped:])
	n, err := c.Conn.Write(p[skipped:])
	return skipped + n, err
}

func (c *socksTCPConn) CloseRead() error  { closeRead(c.Conn); return nil }
func (c *socksTCPConn) CloseWrite() error { closeWrite(c.Conn); return nil }
