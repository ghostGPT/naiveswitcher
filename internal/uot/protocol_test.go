package uot

import (
	"bytes"
	"io"
	"testing"
)

func TestV2WireFormat(t *testing.T) {
	var wire bytes.Buffer
	if err := WriteRequest(&wire, "1.2.3.4", 3478); err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 1, 1, 2, 3, 4, 13, 150}; !bytes.Equal(wire.Bytes(), want) {
		t.Fatalf("request: %x", wire.Bytes())
	}
	host, port, err := ReadRequest(&wire)
	if err != nil || host != "1.2.3.4" || port != 3478 {
		t.Fatalf("%s %d %v", host, port, err)
	}
	for _, host := range []string{"::1", "example.test"} {
		if err := WriteRequest(&wire, host, 443); err != nil {
			t.Fatal(err)
		}
		got, _, err := ReadRequest(&wire)
		if err != nil || got != host {
			t.Fatalf("host=%s err=%v", got, err)
		}
	}
	for _, payload := range [][]byte{nil, {1, 2, 3}, bytes.Repeat([]byte{7}, MaxPayload)} {
		if err := WritePacket(&wire, payload); err != nil {
			t.Fatal(err)
		}
		got, err := ReadPacket(&wire, make([]byte, MaxPayload))
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("packet length=%d err=%v", len(got), err)
		}
	}
}

func TestMalformedInput(t *testing.T) {
	for _, data := range [][]byte{{0}, {2}, {1, 3, 0}, {1, 1, 127}, {1, 1, 127, 0, 0, 1, 0, 0}} {
		if _, _, err := ReadRequest(bytes.NewReader(data)); err == nil {
			t.Fatalf("accepted %x", data)
		}
	}
	for _, data := range [][]byte{{1}, {0, 2, 1}, {255, 255}} {
		if _, err := ReadPacket(bytes.NewReader(data), make([]byte, MaxPayload)); err == nil {
			t.Fatalf("accepted %x", data)
		}
	}
	if err := WritePacket(io.Discard, make([]byte, MaxPayload+1)); err == nil {
		t.Fatal("accepted oversized datagram")
	}
}
