package subscription

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("response exceeded probe limit") }

func TestFastestOnlyReadsProbePrefix(t *testing.T) {
	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := io.MultiReader(bytes.NewReader(make([]byte, 1024)), failingReader{})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body), Header: make(http.Header), Request: r}, nil
	})}
	defer func() { http.DefaultClient = previousClient }()
	got, err := Fastest([]string{"https://127.0.0.1"}, map[string]int{}, "")
	if err != nil || got != "https://127.0.0.1" {
		t.Fatalf("Fastest = %q, %v", got, err)
	}
}

func TestSubscriptionReportsOversizedLine(t *testing.T) {
	line := "http2://" + strings.Repeat("A", 70<<10)
	body := base64.StdEncoding.EncodeToString([]byte(line))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(body))
	}))
	defer server.Close()
	if _, err := Subscription(server.URL); err == nil {
		t.Fatal("expected scanner error for oversized line")
	}
}
