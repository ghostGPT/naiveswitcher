package subscription

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkFastestLargeResponse(b *testing.B) {
	response := make([]byte, 8<<20)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(response)
	}))
	defer server.Close()
	previousClient := http.DefaultClient
	http.DefaultClient = server.Client()
	defer func() { http.DefaultClient = previousClient }()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Fastest([]string{server.URL}, map[string]int{}, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSubscriptionLargeList(b *testing.B) {
	var decoded strings.Builder
	for i := 0; i < 5000; i++ {
		host := fmt.Sprintf("user:password@node-%d.example:443", i)
		decoded.WriteString("http2://")
		decoded.WriteString(base64.StdEncoding.EncodeToString([]byte(host)))
		decoded.WriteByte('\n')
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(decoded.String()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(encoded))
	}))
	defer server.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		urls, err := Subscription(server.URL)
		if err != nil || len(urls) != 5000 {
			b.Fatalf("servers = %d, error = %v", len(urls), err)
		}
	}
}
