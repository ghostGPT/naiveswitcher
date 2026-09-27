package switcher

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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
