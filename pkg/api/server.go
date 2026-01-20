package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	proping "github.com/prometheus-community/pro-bing"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/log"
	"naiveswitcher/pkg/subscription"
	"naiveswitcher/util"
)

//go:embed static/*
var embeddedFiles embed.FS

// ServeWeb 启动 Web 管理界面
func ServeWeb(state *types.GlobalState, config *config.Config, doSwitch chan<- types.SwitchRequest, doCheckUpdate chan<- struct{}) {
	// API 端点
	http.HandleFunc("/api/switch", func(w http.ResponseWriter, r *http.Request) {
		handleSwitchAPI(state, w, r, doSwitch)
	})

	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		handleStatusAPI(state, config, w, r)
	})

	http.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		handleLogsAPI(w, r)
	})

	http.HandleFunc("/api/auto-switch", func(w http.ResponseWriter, r *http.Request) {
		handleAutoSwitchAPI(state, w, r)
	})

	http.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		handleUpdateAPI(doCheckUpdate, w, r)
	})

	// 保留原有的 /s 和 /p 端点
	http.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		handleSubscription(state, config, w, r)
	})

	http.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		handlePing(state, w, r)
	})

	// 静态文件服务 - 提供所有前端文件（HTML, CSS, JS）
	// 必须放在最后，这样 API 路由才能优先匹配
	// 创建子文件系统，移除 "static" 前缀
	webFS, err := fs.Sub(embeddedFiles, "static")
	if err != nil {
		panic("Failed to create sub filesystem: " + err.Error())
	}
	http.Handle("/", http.FileServer(http.FS(webFS)))

	http.ListenAndServe(config.WebPort, nil)
}

func handleSubscription(state *types.GlobalState, config *config.Config, w http.ResponseWriter, _ *http.Request) {
	newHostUrls, err := subscription.Subscription(config.SubscribeURL)
	if err != nil {
		w.Write([]byte(err.Error() + "\n"))
	} else {
		state.HostUrls = newHostUrls
	}
	w.Write([]byte(fmt.Sprintf("%d servers in pool\n", len(state.HostUrls))))
	hostIps := util.BatchLookupURLsIP(state.HostUrls)

	for host, ips := range hostIps {
		w.Write([]byte(fmt.Sprintf("%s: %+v\n", host, ips.IPs)))
	}

	w.Write([]byte("\n\n\n"))

	uniqueIPs := util.UniqueIPs(hostIps)
	for ip := range uniqueIPs {
		w.Write([]byte(fmt.Sprintf("%s\n", ip)))
	}
}

func handlePing(state *types.GlobalState, w http.ResponseWriter, _ *http.Request) {
	hostIps := util.BatchLookupURLsIP(state.HostUrls)
	uniqueIps := util.UniqueIPs(hostIps)
	uniqueHosts := make(map[string]struct{})
	for _, hosts := range uniqueIps {
		uniqueHosts[hosts[rand.Intn(len(hosts))]] = struct{}{}
	}
	sb := new(strings.Builder)
	wg := new(sync.WaitGroup)
	wg.Add(len(uniqueHosts))
	for host := range uniqueHosts {
		go func(host string) {
			defer wg.Done()
			p, pingErr := proping.NewPinger(host)
			p.Timeout = time.Second * 10
			if pingErr == nil {
				pingErr = p.Run()
			}
			sb.WriteString(fmt.Sprintf("%s, avg: %v, err: %v\n", host, p.Statistics().AvgRtt, pingErr))
		}(host)
	}
	wg.Wait()
	w.Write([]byte(sb.String()))
}

// API 处理函数

// handleSwitchAPI 处理服务器切换 API
func handleSwitchAPI(state *types.GlobalState, w http.ResponseWriter, r *http.Request, doSwitch chan<- types.SwitchRequest) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Type         string `json:"type"`          // "auto", "avoid", "select"
		TargetServer string `json:"target_server"` // for "select" type
		AvoidServer  string `json:"avoid_server"`  // for "avoid" type
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	switchReq := types.SwitchRequest{
		Type:         req.Type,
		TargetServer: req.TargetServer,
		AvoidServer:  req.AvoidServer,
	}

	doSwitch <- switchReq

	writeJSONSuccess(w, map[string]interface{}{
		"message": "Switch request sent",
		"type":    req.Type,
	})
}

// handleStatusAPI 返回当前状态的 JSON
func handleStatusAPI(state *types.GlobalState, config *config.Config, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state.AutoSwitchMutex.RLock()
	paused := state.AutoSwitchPaused
	state.AutoSwitchMutex.RUnlock()

	uptime := formatUptime(time.Since(time.Unix(state.StartTime, 0)))

	// 复制 ServerDownPriority map 需要加锁
	state.ServerDownPriorityMutex.RLock()
	downStatsCopy := make(map[string]int, len(state.ServerDownPriority))
	for k, v := range state.ServerDownPriority {
		downStatsCopy[k] = v
	}
	state.ServerDownPriorityMutex.RUnlock()

	// Get runtime metrics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	data := map[string]interface{}{
		"current_server":     state.FastestUrl,
		"error_count":        atomic.LoadInt32(&state.ErrorCount),
		"down_stats":         downStatsCopy,
		"naive_version":      common.Naive,
		"switcher_version":   config.Version,
		"auto_switch_paused": paused,
		"available_servers":  state.HostUrls,
		"uptime":             uptime,
		"start_time":         state.StartTime,
		"goroutine_count":    runtime.NumGoroutine(),
		"memory_usage_mb":    fmt.Sprintf("%.2f", float64(memStats.Sys)/1024/1024),
		"memory_alloc_mb":    fmt.Sprintf("%.2f", float64(memStats.Alloc)/1024/1024),
	}

	writeJSONSuccess(w, data)
}

// handleLogsAPI 返回日志
func handleLogsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	log.WriteLog(w)
}

// handleAutoSwitchAPI 处理自动切换的暂停/恢复
func handleAutoSwitchAPI(state *types.GlobalState, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action string `json:"action"` // "pause" or "resume"
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	state.AutoSwitchMutex.Lock()
	switch req.Action {
	case "pause":
		state.AutoSwitchPaused = true
		if state.FastestUrl != "" {
			state.LockedServer = state.FastestUrl
		}
	case "resume":
		state.AutoSwitchPaused = false
		state.LockedServer = ""
	default:
		state.AutoSwitchMutex.Unlock()
		writeJSONError(w, "Invalid action. Use 'pause' or 'resume'", http.StatusBadRequest)
		return
	}
	paused := state.AutoSwitchPaused
	ps := types.PersistedState{
		AutoSwitchPaused: state.AutoSwitchPaused,
		LockedServer:     state.LockedServer,
	}
	state.AutoSwitchMutex.Unlock()

	if err := types.SavePersistedState(common.BasePath, ps); err != nil {
		log.DebugF("Save persisted state error: %v\n", err)
	}

	writeJSONSuccess(w, map[string]interface{}{
		"message": "Auto switch " + req.Action + "d",
		"paused":  paused,
	})
}

// handleUpdateAPI 触发更新检查
func handleUpdateAPI(doCheckUpdate chan<- struct{}, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	doCheckUpdate <- struct{}{}

	writeJSONSuccess(w, map[string]interface{}{
		"message": "Update check triggered",
	})
}

// 辅助函数

// formatUptime 格式化运行时长
func formatUptime(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// writeJSONSuccess 写入成功的 JSON 响应
func writeJSONSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// writeJSONError 写入错误的 JSON 响应
func writeJSONError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}
