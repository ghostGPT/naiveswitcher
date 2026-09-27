package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/switcher"
)

func TestSwitchAPIRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown type", `{"type":"unknown"}`},
		{"missing target", `{"type":"select"}`},
		{"unknown target", `{"type":"select","target_server":"https://other.example"}`},
		{"missing avoided server", `{"type":"avoid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &types.GlobalState{HostUrls: []string{"https://known.example"}}
			requests := make(chan types.SwitchRequest, 1)
			r := httptest.NewRequest(http.MethodPost, "/api/switch", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handleSwitchAPI(state, w, r, requests)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
			}
			if len(requests) != 0 {
				t.Fatal("invalid request was queued")
			}
		})
	}
}

func TestStatusWhileServersChange(t *testing.T) {
	state := &types.GlobalState{StartTime: time.Now().Unix(), ServerDownPriority: make(map[string]int)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			state.SetCurrentServer("https://one.example")
			state.SetServers([]string{"https://one.example"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			w := httptest.NewRecorder()
			handleStatusAPI(state, &config.Config{}, w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
			if w.Code != http.StatusOK {
				t.Errorf("status = %d", w.Code)
				return
			}
		}
	}()
	wg.Wait()
}

func TestSwitchAPIReportsSwitcherFailure(t *testing.T) {
	state := &types.GlobalState{HostUrls: []string{"https://known.example"}}
	state.SetCurrentServer("https://known.example")
	requests := make(chan types.SwitchRequest)
	done := make(chan struct{})
	go func() {
		switcher.Switcher(state, &config.Config{}, requests)
		close(done)
	}()
	r := httptest.NewRequest(http.MethodPost, "/api/switch", strings.NewReader(`{"type":"select","target_server":"https://known.example"}`))
	w := httptest.NewRecorder()
	handleSwitchAPI(state, w, r, requests)
	close(requests)
	<-done
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

func TestSwitchAPIReturnsExecutionError(t *testing.T) {
	state := &types.GlobalState{HostUrls: []string{"https://known.example"}}
	requests := make(chan types.SwitchRequest)
	go func() {
		req := <-requests
		req.Result <- context.Canceled
	}()
	r := httptest.NewRequest(http.MethodPost, "/api/switch", strings.NewReader(`{"type":"select","target_server":"https://known.example"}`))
	w := httptest.NewRecorder()
	handleSwitchAPI(state, w, r, requests)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}
