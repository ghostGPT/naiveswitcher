package proxy

import (
	"bytes"
	"testing"
	"time"
)

func BenchmarkUDPOverNaive(b *testing.B) {
	for _, size := range []struct {
		name string
		n    int
	}{{"voice320", 320}, {"mtu1200", 1200}} {
		b.Run(size.name, func(b *testing.B) {
			upstream, _ := fakeNaive(b)
			address, cancel, done := udpListener(b, upstream)
			control, udp, relay := associate(b, address)
			defer func() { control.Close(); cancel(); <-done }()
			frame := append([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53}, bytes.Repeat([]byte{1}, size.n)...)
			buf := make([]byte, len(frame))
			udp.SetDeadline(time.Now().Add(60 * time.Second))
			udp.WriteToUDP(frame, relay)
			udp.ReadFromUDP(buf)
			b.SetBytes(int64(size.n))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := udp.WriteToUDP(frame, relay); err != nil {
					b.Fatal(err)
				}
				if n, _, err := udp.ReadFromUDP(buf); err != nil || n != len(frame) {
					b.Fatalf("received %d err %v", n, err)
				}
			}
			b.StopTimer()
		})
	}
}
