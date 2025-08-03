package server

import (
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
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		service.WriteLog(w)
	})

	http.HandleFunc("/v", func(w http.ResponseWriter, r *http.Request) {
		handleStatusPage(state, config, templates, w, r, doSwitch, doCheckUpdate)
	})

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

	data := types.StatusData{
		CurrentServer:    state.FastestUrl,
		ErrorCount:       state.ErrorCount,
		DownStats:        fmt.Sprintf("%+v", state.ServerDownPriority),
		AvoidServerURL:   url.QueryEscape(state.FastestUrl),
		NaiveVersion:     service.Naive,
		SwitcherVersion:  config.Version,
		AutoSwitchPaused: paused,
		AvailableServers: state.HostUrls,
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
