package switcher

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
)

func TestHandleSwitchKeepsServersWhenProbesFail(t *testing.T) {
	old := []string{"https://127.0.0.1:1"}
	state := &types.GlobalState{AppContext: context.Background(), ServerDownPriority: make(map[string]int)}
	cfg := &config.Config{SubscribeURL: "not a URL"}
	got, err := HandleSwitch(state, cfg, old, "")
	if err == nil {
		t.Fatal("expected probe failure")
	}
	if !reflect.DeepEqual(got, old) {
		t.Fatalf("servers = %v, want %v", got, old)
	}
}

func TestSwitcherRefreshesServersWhenCurrentRemainsBest(t *testing.T) {
	var target string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sub" {
			encodedHost := base64.StdEncoding.EncodeToString([]byte(strings.TrimPrefix(target, "https://")))
			w.Write([]byte(base64.StdEncoding.EncodeToString([]byte("http2://" + encodedHost))))
			return
		}
		w.Write(make([]byte, 1024))
	}))
	defer server.Close()
	target = server.URL
	previousClient := http.DefaultClient
	http.DefaultClient = server.Client()
	defer func() { http.DefaultClient = previousClient }()

	state := &types.GlobalState{AppContext: context.Background(), ServerDownPriority: make(map[string]int)}
	state.NaiveCmd = &exec.Cmd{} // The existing process is still alive.
	state.SetCurrentServer(target)
	state.SetServers([]string{"https://old.example"})
	requests := make(chan types.SwitchRequest, 1)
	requests <- types.SwitchRequest{Type: "auto"}
	close(requests)
	Switcher(state, &config.Config{SubscribeURL: server.URL + "/sub"}, requests)
	_, got := state.Servers()
	if !reflect.DeepEqual(got, []string{target}) {
		t.Fatalf("servers = %v, want [%s]", got, target)
	}
}

func TestRestartNaiveClearsCurrentWhenStartAndRollbackFail(t *testing.T) {
	previous := common.GetNaive()
	common.SetNaive("")
	t.Cleanup(func() { common.SetNaive(previous) })
	state := &types.GlobalState{AppContext: context.Background()}
	state.SetCurrentServer("https://old.example")
	if err := RestartNaive(state, "https://new.example"); err == nil {
		t.Fatal("expected restart failure")
	}
	if got := state.CurrentServer(); got != "" {
		t.Fatalf("current server = %q, want empty when no process is running", got)
	}
}

func TestRestartNaiveRejectsCanceledApplication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state := &types.GlobalState{AppContext: ctx}
	if err := RestartNaive(state, "https://example.com"); err == nil {
		t.Fatal("restart reported success after shutdown")
	}
}

func TestStaleAutomaticRequestDoesNotProbeOrSwitch(t *testing.T) {
	state := &types.GlobalState{AppContext: context.Background(), ServerDownPriority: make(map[string]int)}
	state.SetCurrentServer("https://new.example")
	state.AdvanceGeneration()
	req := types.SwitchRequest{Type: "avoid_auto", AvoidServer: "https://old.example", Coalesced: true, Generation: 0}
	if err := processRequest(state, &config.Config{SubscribeURL: "invalid"}, req); err != nil {
		t.Fatal(err)
	}
	if len(state.ServerDownPriority) != 0 || state.CurrentServer() != "https://new.example" {
		t.Fatal("stale event affected current node")
	}
}

func TestMonitorRetriesRecentFailuresWithoutNewTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &types.GlobalState{AppContext: ctx, NaiveCmd: &exec.Cmd{}}
	state.SetCurrentServer("https://failed.example")
	for i := 0; i < types.FailureThreshold; i++ {
		state.RecordFailure(state.Generation(), time.Now())
	}
	requests := make(chan types.SwitchRequest, 10)
	done := make(chan struct{})
	go func() { defer close(done); monitorProcess(state, requests, time.Millisecond) }()
	defer func() { cancel(); <-done }()
	select {
	case req := <-requests:
		if req.Type != "avoid_auto" || req.AvoidServer != "https://failed.example" {
			t.Fatalf("unexpected retry: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("recent failure retry required new traffic")
	}
}
