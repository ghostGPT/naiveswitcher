// Package uot implements the connected mode of the documented UoT v2 wire
// format. This is an independent implementation, without third-party code.
package uot

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
)

// Port 443 allows an ordinary Naive SOCKS CONNECT to carry the UoT stream.
// This authority is intercepted by the authenticated forwardproxy handler;
// it must never be resolved in DNS or dialed as a real TCP destination.
const Authority = "sp.v2.udp-over-tcp.arpa:443"
const MaxPayload = 65507

func ReadAddress(r io.Reader) (string, uint16, error) {
	var kind [1]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch kind[0] {
	case 1, 4:
		n := 4
		if kind[0] == 4 {
			n = 16
		}
		ip := make(net.IP, n)
		if _, err := io.ReadFull(r, ip); err != nil {
			return "", 0, err
		}
		host = ip.String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(r, size[:]); err != nil {
			return "", 0, err
		}
		if size[0] == 0 {
			return "", 0, errors.New("empty domain")
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(r, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	default:
		return "", 0, errors.New("unsupported SOCKS address type")
	}
	var port [2]byte
	_, err := io.ReadFull(r, port[:])
	return host, binary.BigEndian.Uint16(port[:]), err
}

func Address(host string, port uint16) ([]byte, error) {
	var out []byte
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			out = append([]byte{1}, ip4...)
		} else {
			out = append([]byte{4}, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("invalid domain length")
		}
		out = append([]byte{3, byte(len(host))}, host...)
	}
	return binary.BigEndian.AppendUint16(out, port), nil
}

func ReadRequest(r io.Reader) (string, uint16, error) {
	var mode [1]byte
	if _, err := io.ReadFull(r, mode[:]); err != nil {
		return "", 0, err
	}
	if mode[0] != 1 {
		return "", 0, errors.New("only connected UoT v2 is supported")
	}
	host, port, err := ReadAddress(r)
	if err == nil && port == 0 {
		err = errors.New("zero destination port")
	}
	return host, port, err
}

func WriteRequest(w io.Writer, host string, port uint16) error {
	if port == 0 {
		return errors.New("zero destination port")
	}
	address, err := Address(host, port)
	if err != nil {
		return err
	}
	return write(w, append([]byte{1}, address...))
}

func ReadPacket(r io.Reader, buf []byte) ([]byte, error) {
	var reader PacketReader
	return reader.ReadPacket(r, buf)
}

// PacketReader reuses its framing header across datagrams. One reader per flow.
type PacketReader struct{ header [2]byte }

func (p *PacketReader) ReadPacket(r io.Reader, buf []byte) ([]byte, error) {
	if _, err := io.ReadFull(r, p.header[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(p.header[:]))
	if n > MaxPayload || n > len(buf) {
		return nil, errors.New("oversized UoT datagram")
	}
	_, err := io.ReadFull(r, buf[:n])
	return buf[:n], err
}

func WritePacket(w io.Writer, payload []byte) error {
	var writer PacketWriter
	return writer.WritePacket(w, payload)
}

// PacketWriter retains only the largest frame seen on this flow, bounded by
// MaxPayload. A complete frame is sent in one write without per-packet allocation.
type PacketWriter struct{ frame []byte }

func (p *PacketWriter) WritePacket(w io.Writer, payload []byte) error {
	if len(payload) > MaxPayload {
		return errors.New("oversized UoT datagram")
	}
	if cap(p.frame) < 2+len(payload) {
		p.frame = make([]byte, 2+len(payload))
	}
	frame := p.frame[:2+len(payload)]
	binary.BigEndian.PutUint16(frame, uint16(len(payload)))
	copy(frame[2:], payload)
	return write(w, frame)
}

func HostPort(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func write(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
