package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	proping "github.com/prometheus-community/pro-bing"

	"naiveswitcher/internal/config"
	"naiveswitcher/service"
	"naiveswitcher/internal/types"
	"naiveswitcher/util"
)

// ServeWeb 启动 Web 管理界面
func ServeWeb(state *types.GlobalState, config *config.Config, templates *template.Template, doSwitch chan<- types.SwitchRequest, doCheckUpdate chan<- struct{}) {
	// 主界面 - 移到根路径
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		handleStatusPage(state, config, templates, w, r, doSwitch, doCheckUpdate)
	})

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

	http.ListenAndServe(config.WebPort, nil)
}

func handleStatusPage(state *types.GlobalState, config *config.Config, templates *template.Template, w http.ResponseWriter, r *http.Request, doSwitch chan<- types.SwitchRequest, doCheckUpdate chan<- struct{}) {
	w.Header().Add("Content-Type", "text/html")

	// 重命名参数避免歧义：avoidServer 表示避免切换到此服务器
	avoidServer, _ := url.QueryUnescape(r.URL.Query().Get("avoidServer"))
	// 新增参数：selectServer 表示直接切换到选择的服务器
	selectServer, _ := url.QueryUnescape(r.URL.Query().Get("selectServer"))

	// Handle direct server selection
	if selectServer != "" {
		// 直接切换到选择的服务器
		doSwitch <- types.SwitchRequest{
			Type:         "select",
			TargetServer: selectServer,
		}
		err := templates.ExecuteTemplate(w, "switching.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Handle pause/resume auto switch
	if action := r.URL.Query().Get("autoSwitch"); action != "" {
		state.AutoSwitchMutex.Lock()
		switch action {
		case "pause":
			state.AutoSwitchPaused = true
		case "resume":
			state.AutoSwitchPaused = false
		}
		state.AutoSwitchMutex.Unlock()
		// Redirect back to status page to avoid refresh issues
		http.Redirect(w, r, "/v", http.StatusSeeOther)
		return
	}

	// 处理当前服务器出错，需要切换但避免切换到指定服务器
	if avoidServer == state.FastestUrl {
		doSwitch <- types.SwitchRequest{
			Type:        "avoid",
			AvoidServer: avoidServer,
		}
		err := templates.ExecuteTemplate(w, "switching.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	if r.URL.Query().Get("checkUpdate") == "true" {
		doCheckUpdate <- struct{}{}
		err := templates.ExecuteTemplate(w, "update_check.html", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	state.AutoSwitchMutex.RLock()
	paused := state.AutoSwitchPaused
	state.AutoSwitchMutex.RUnlock()

	// 计算运行时长
	uptime := formatUptime(time.Since(time.Unix(state.StartTime, 0)))

	data := types.StatusData{
		CurrentServer:    state.FastestUrl,
		ErrorCount:       state.ErrorCount,
		DownStats:        fmt.Sprintf("%+v", state.ServerDownPriority),
		AvoidServerURL:   url.QueryEscape(state.FastestUrl),
		NaiveVersion:     service.Naive,
		SwitcherVersion:  config.Version,
		AutoSwitchPaused: paused,
		AvailableServers: state.HostUrls,
		Uptime:           uptime,
		StartTime:        state.StartTime,
	}

	err := templates.ExecuteTemplate(w, "status.html", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleSubscription(state *types.GlobalState, config *config.Config, w http.ResponseWriter, _ *http.Request) {
	newHostUrls, err := service.Subscription(config.SubscribeURL)
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

	data := map[string]interface{}{
		"current_server":     state.FastestUrl,
		"error_count":        state.ErrorCount,
		"down_stats":         state.ServerDownPriority,
		"naive_version":      service.Naive,
		"switcher_version":   config.Version,
		"auto_switch_paused": paused,
		"available_servers":  state.HostUrls,
		"uptime":             uptime,
		"start_time":         state.StartTime,
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
	service.WriteLog(w)
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
	case "resume":
		state.AutoSwitchPaused = false
	default:
		state.AutoSwitchMutex.Unlock()
		writeJSONError(w, "Invalid action. Use 'pause' or 'resume'", http.StatusBadRequest)
		return
	}
	paused := state.AutoSwitchPaused
	state.AutoSwitchMutex.Unlock()

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
