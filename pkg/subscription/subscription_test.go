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
	"time"
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

func TestPeriodicSelectionKeepsHealthyCurrentNode(t *testing.T) {
	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Different ports on one IP: current must still be measured separately.
		if r.URL.Port() == "2" {
			time.Sleep(10 * time.Millisecond)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(make([]byte, 1024))), Header: make(http.Header), Request: r}, nil
	})}
	defer func() { http.DefaultClient = previousClient }()
	current := "https://127.0.0.1:2"
	got, err := FastestWithCurrent([]string{"https://127.0.0.1:1", current}, map[string]int{}, current)
	if err != nil || got != current {
		t.Fatalf("got %s, %v; expected healthy current node", got, err)
	}
}

func TestPeriodicSelectionRequiresMeaningfulImprovement(t *testing.T) {
	for _, tc := range []struct {
		current, candidate time.Duration
		want               bool
	}{
		{100 * time.Millisecond, 60 * time.Millisecond, false},
		{time.Second, 900 * time.Millisecond, false},
		{time.Second, 700 * time.Millisecond, true},
		{time.Second, 2 * time.Second, false},
	} {
		if got := worthSwitching(tc.current, tc.candidate); got != tc.want {
			t.Fatalf("current=%s candidate=%s got=%v", tc.current, tc.candidate, got)
		}
	}
}

func TestPeriodicSelectionReplacesUnreachableCurrentNode(t *testing.T) {
	previousClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Port() == "2" {
			return nil, errors.New("offline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(make([]byte, 1024))), Header: make(http.Header), Request: r}, nil
	})}
	defer func() { http.DefaultClient = previousClient }()
	candidate, current := "https://127.0.0.1:1", "https://127.0.0.2:2"
	got, err := FastestWithCurrent([]string{candidate, current}, map[string]int{}, current)
	if err != nil || got != candidate {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
