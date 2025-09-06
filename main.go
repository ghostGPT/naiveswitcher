package main

import (
	"context"
	"embed"
	"flag"
	"html/template"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/internal/updater"
	"naiveswitcher/server"
	"naiveswitcher/service"
	"naiveswitcher/switcher"
)

const (
	dnsResolverProto     = "udp" // Protocol to use for the DNS resolver
	dnsResolverTimeoutMs = 5000  // Timeout (ms) for the DNS resolver (optional)
)

//go:embed templates/*.html
var templatesFS embed.FS

var (
	version   string
	templates *template.Template
	cfg       = config.NewConfig(version)
)

func init() {
	var err error
	templates, err = template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		panic("Failed to load embedded templates: " + err.Error())
	}

	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: time.Duration(dnsResolverTimeoutMs) * time.Millisecond,
				}
				return d.DialContext(ctx, dnsResolverProto, cfg.DNSResolverIP)
			},
		},
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	http.DefaultTransport.(*http.Transport).DialContext = dialContext
}

func main() {
	// 创建应用程序上下文
	ctxWithCancel, gracefulShutdown := context.WithCancel(context.Background())
	ctx, stop := signal.NotifyContext(ctxWithCancel, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	state := &types.GlobalState{
		ServerDownPriority: make(map[string]int),
		AppContext:         ctxWithCancel, // 设置应用程序上下文
	}

	// 解析命令行参数
	flag.BoolVar(&service.Debug, "d", false, "Debug mode")
	if cfg.ParseFlags() {
		return // 显示版本后退出
	}

	service.Init()

	// 验证配置
	if err := cfg.Validate(); err != nil {
		println(err.Error())
		return
	} // 初始化服务器列表
	var err error
	if len(state.HostUrls) == 0 {
		state.HostUrls = append(state.HostUrls, cfg.BootstrapNode)
	}
	state.HostUrls, err = switcher.HandleSwitch(state, cfg, state.HostUrls, "")
	if err != nil {
		println("Bootstrap error:", err.Error())
	}

	// 启动 TCP 监听
	l, err := net.Listen("tcp", cfg.ListenPort)
	if err != nil {
		panic(err)
	}

	service.DebugF("Running with naive: %s\n", service.Naive)

	doSwitch := make(chan types.SwitchRequest, 100)
	doCheckUpdate := make(chan struct{}, 10)

	go switcher.Switcher(state, cfg, doSwitch)

	go updater.Updater(state, cfg, gracefulShutdown, doCheckUpdate)

	doCheckUpdate <- struct{}{}

	go func() {
		ticker := time.NewTicker(time.Duration(cfg.AutoSwitchDuration) * time.Minute)
		for range ticker.C {
			state.AutoSwitchMutex.RLock()
			paused := state.AutoSwitchPaused
			state.AutoSwitchMutex.RUnlock()

			if !paused {
				doSwitch <- types.SwitchRequest{Type: "auto"}
				doCheckUpdate <- struct{}{}
			}
		}
	}()

	go server.ServeTCP(state, l, doSwitch)
	go server.ServeWeb(state, cfg, templates, doSwitch, doCheckUpdate)

	<-ctx.Done()
	println("Shutting down")

	// 安全地停止 naive 进程
	state.NaiveCmdLock.Lock()
	defer state.NaiveCmdLock.Unlock()

	if state.NaiveCmd != nil {
		if err := state.NaiveCmd.Cancel(); err != nil {
			println("Error cancel naive: ", err)
		}
		state.NaiveCmd.Wait()
		state.NaiveCmd = nil
	}

	println("Shutdown complete")
}
