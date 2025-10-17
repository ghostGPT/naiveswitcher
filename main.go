package main

import (
	"context"
	"embed"
	"flag"
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

//go:embed web/*
var staticFS embed.FS

var (
	version string = "888.888.888"
	cfg     = config.NewConfig(version)
)

func init() {
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
		StartTime:          time.Now().Unix(),
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
	}

	// 初始化服务器列表
	var err error
	if len(state.HostUrls) == 0 {
		state.HostUrls = append(state.HostUrls, cfg.BootstrapNode)
	}
	state.HostUrls, err = switcher.HandleSwitch(state, cfg, state.HostUrls, "")
	if err != nil {
		service.DebugF("Bootstrap error: %v (will auto retry)\n", err)
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

	// Create sub-filesystem for the web directory
	webSubFS, err := staticFS.ReadDir("web")
	if err != nil {
		panic("Failed to access web directory: " + err.Error())
	}
	_ = webSubFS // Just check it exists

	go server.ServeWeb(state, cfg, http.FS(staticFS), doSwitch, doCheckUpdate)

	<-ctx.Done()
	println("Shutting down")

	// 1. 先触发 gracefulShutdown 取消所有子 context
	gracefulShutdown()

	// 2. 关闭通道，通知所有 goroutine 停止接收新请求
	close(doSwitch)
	close(doCheckUpdate)

	// 3. 给 goroutines 一些时间完成当前操作
	time.Sleep(500 * time.Millisecond)

	// 4. 最后安全地停止 naive 进程
	state.NaiveCmdLock.Lock()
	defer state.NaiveCmdLock.Unlock()

	if state.NaiveCmd != nil {
		// 先取消 context
		if state.NaiveCmdCancel != nil {
			state.NaiveCmdCancel()
			state.NaiveCmdCancel = nil
		}

		// 尝试优雅终止进程
		if state.NaiveCmd.Process != nil {
			pid := state.NaiveCmd.Process.Pid

			// 先尝试发送 SIGTERM 到整个进程组
			pgid := pid // 进程组ID默认等于进程ID
			if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
				service.DebugF("Error sending SIGTERM to process group (PGID: %d): %v, trying single process\n", pgid, err)
				if err := state.NaiveCmd.Process.Signal(syscall.SIGTERM); err != nil {
					service.DebugF("Error sending SIGTERM to naive process (PID: %d): %v\n", pid, err)
				} else {
					println("Sent SIGTERM to naive process (PID:", pid, ")")
				}
			} else {
				println("Sent SIGTERM to process group (PGID:", pgid, ")")
			}

			// 等待进程退出，带超时机制
			done := make(chan error, 1)
			go func() {
				done <- state.NaiveCmd.Wait()
			}()

			// 等待最多3秒（shutdown时可以多给一点时间）
			select {
			case err := <-done:
				if err != nil {
					println("Naive process exited with error:", err)
				} else {
					println("Naive process exited gracefully")
				}
			case <-time.After(3 * time.Second):
				println("Naive process did not exit after SIGTERM, sending SIGKILL to process group")
				if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
					service.DebugF("Error sending SIGKILL to process group (PGID: %d): %v, trying single process\n", pgid, err)
					if err := state.NaiveCmd.Process.Kill(); err != nil {
						println("Error killing naive process:", err)
					}
				}
				<-done
			}
		}

		state.NaiveCmd = nil
	}

	println("Shutdown complete")
}
