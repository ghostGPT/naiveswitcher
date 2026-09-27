package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
)

func BenchmarkStatusAPI(b *testing.B) {
	state := &types.GlobalState{
		StartTime:          time.Now().Unix(),
		ServerDownPriority: make(map[string]int, 100),
	}
	servers := make([]string, 100)
	for i := range servers {
		server := fmt.Sprintf("https://user:password@node-%d.example:443", i)
		servers[i] = server
		state.ServerDownPriority[fmt.Sprintf("node-%d.example", i)] = i
	}
	state.SetServers(servers)
	state.SetCurrentServer(servers[0])
	cfg := &config.Config{Version: "1.0.0"}
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handleStatusAPI(state, cfg, w, r)
		if w.Code != http.StatusOK {
			b.Fatalf("status = %d", w.Code)
		}
	}
}

func BenchmarkRuntimeReadMemStats(b *testing.B) {
	var stats runtime.MemStats
	for i := 0; i < b.N; i++ {
		runtime.ReadMemStats(&stats)
	}
}

func BenchmarkRuntimeMetricsRead(b *testing.B) {
	samples := []metrics.Sample{
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/objects:bytes"},
	}
	for i := 0; i < b.N; i++ {
		metrics.Read(samples)
	}
}
