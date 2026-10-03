package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/net/proxy"
	"naiveswitcher/internal/uot"
)

const udpIdleTimeout = 2 * time.Minute

// ServeUDP serves a separate, opt-in SOCKS5 UDP ASSOCIATE endpoint. TCP users
// keep the existing transparent listener. Each UDP destination gets its own
// ordinary CONNECT stream through the unmodified local Naive client.
func ServeUDP(ctx context.Context, listener net.Listener, upstream string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	slots := make(chan struct{}, 128)
	flowSlots := make(chan struct{}, 256)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		select {
		case slots <- struct{}{}:
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				serveUDPAssociation(ctx, conn, upstream, flowSlots)
			}()
		default:
			conn.Close()
		}
	}
}

func serveUDPAssociation(parent context.Context, control net.Conn, upstream string, flowSlots chan struct{}) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer control.Close()
	stop := context.AfterFunc(ctx, func() { control.Close() })
	defer stop()
	_ = control.SetDeadline(time.Now().Add(10 * time.Second))
	var header [3]byte
	if _, err := io.ReadFull(control, header[:2]); err != nil || header[0] != 5 || header[1] == 0 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(control, methods); err != nil {
		return
	}
	if !bytes.Contains(methods, []byte{0}) {
		_, _ = control.Write([]byte{5, 255})
		return
	}
	if _, err := control.Write([]byte{5, 0}); err != nil {
		return
	}
	if _, err := io.ReadFull(control, header[:]); err != nil || header[0] != 5 || header[2] != 0 {
		return
	}
	if header[1] != 3 {
		socksReply(control, 7, nil)
		return
	}
	host, port, err := uot.ReadAddress(control)
	if err != nil {
		socksReply(control, 8, nil)
		return
	}
	peer, ok := control.RemoteAddr().(*net.TCPAddr)
	local, localOK := control.LocalAddr().(*net.TCPAddr)
	if !ok || !localOK {
		return
	}
	requestedIP := net.ParseIP(host)
	if requestedIP == nil || (!requestedIP.IsUnspecified() && !requestedIP.Equal(peer.IP)) {
		socksReply(control, 2, nil)
		return
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: local.IP, Zone: local.Zone})
	if err != nil {
		socksReply(control, 1, nil)
		return
	}
	defer udp.Close()
	stopUDP := context.AfterFunc(ctx, func() { udp.Close() })
	defer stopUDP()
	if err := socksReply(control, 0, udp.LocalAddr().(*net.UDPAddr)); err != nil {
		return
	}
	_ = control.SetDeadline(time.Time{})
	controlDone := make(chan struct{})
	go func() { defer close(controlDone); _, _ = io.Copy(io.Discard, control); cancel() }()
	defer func() { control.Close(); <-controlDone }()
	var mu sync.Mutex
	flows := make(map[string]chan []byte)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	var endpoint *net.UDPAddr
	buf := make([]byte, 65535)
	for {
		_ = udp.SetReadDeadline(time.Now().Add(udpIdleTimeout))
		n, source, err := udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Pin both the control peer IP and UDP source port. No third-party relay.
		if !source.IP.Equal(peer.IP) || (port != 0 && source.Port != int(port)) {
			continue
		}
		if endpoint != nil && source.String() != endpoint.String() {
			continue
		}
		if n < 4 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 {
			continue
		} // FRAG unsupported
		reader := bytes.NewReader(buf[3:n])
		dest, destPort, err := uot.ReadAddress(reader)
		if err != nil || destPort == 0 || reader.Len() > uot.MaxPayload {
			continue
		}
		if endpoint == nil {
			endpoint = source
		}
		key := uot.HostPort(dest, destPort)
		mu.Lock()
		queue := flows[key]
		if queue == nil && len(flows) < 64 {
			select {
			case flowSlots <- struct{}{}:
			default:
				mu.Unlock()
				continue
			}
			queue = make(chan []byte, 8)
			flows[key] = queue
			wg.Add(1)
			go func(queue chan []byte, target string, targetPort uint16, client *net.UDPAddr) {
				defer wg.Done()
				defer func() { <-flowSlots }()
				defer func() { mu.Lock(); delete(flows, key); mu.Unlock() }()
				_ = relayUDPFlow(ctx, upstream, target, targetPort, queue, udp, client)
				// Avoid reconnect storms if an old server rejects the UoT endpoint.
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}(queue, dest, destPort, endpoint)
		}
		if queue != nil {
			packet := append([]byte{}, buf[n-reader.Len():n]...)
			select {
			case queue <- packet:
			default:
			} // bounded, lossy UDP queue
		}
		mu.Unlock()
	}
}

func socksReply(w io.Writer, code byte, addr *net.UDPAddr) error {
	if addr == nil {
		addr = &net.UDPAddr{IP: net.IPv4zero}
	}
	address, err := uot.Address(addr.IP.String(), uint16(addr.Port))
	if err != nil {
		return err
	}
	_, err = w.Write(append([]byte{5, code, 0}, address...))
	return err
}

func relayUDPFlow(ctx context.Context, upstream, host string, port uint16, queue <-chan []byte, udp *net.UDPConn, client *net.UDPAddr) error {
	dialer, err := proxy.SOCKS5("tcp", upstream, nil, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := dialer.(proxy.ContextDialer).DialContext(dialCtx, "tcp", uot.Authority)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := uot.WriteRequest(conn, host, port); err != nil {
		return err
	}
	address, _ := uot.Address(host, port)
	prefix := append([]byte{0, 0, 0}, address...)
	done := make(chan struct{})
	var readErr error
	go func() {
		defer close(done)
		buf := make([]byte, len(prefix)+uot.MaxPayload)
		copy(buf, prefix)
		var reader uot.PacketReader
		for {
			_ = conn.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			payload, err := reader.ReadPacket(conn, buf[len(prefix):])
			if err != nil {
				readErr = err
				return
			}
			frame := buf[:len(prefix)+len(payload)]
			_ = udp.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			_ = udp.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := udp.WriteToUDP(frame, client); err != nil {
				readErr = err
				return
			}
		}
	}()
	defer func() { conn.Close(); <-done }()
	var writer uot.PacketWriter
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return readErr
		case payload := <-queue:
			_ = conn.SetReadDeadline(time.Now().Add(udpIdleTimeout))
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := writer.WritePacket(conn, payload); err != nil {
				return err
			}
		}
	}
}
