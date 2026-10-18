package switcher

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/log"
	"naiveswitcher/pkg/subscription"
)

// Switcher serializes manual requests, recovery and automatic selection.
func Switcher(state *types.GlobalState, cfg *config.Config, doSwitch <-chan types.SwitchRequest) {
	ctx := state.AppContext
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-doSwitch:
			if !ok {
				return
			}
			err := processRequest(state, cfg, req)
			if req.Coalesced {
				state.FinishAutoSwitch()
			}
			if req.Result != nil {
				req.Result <- err
			}
			if err != nil {
				log.DebugF("Error switching: %v\n", err)
			}
		}
	}
}

func processRequest(state *types.GlobalState, cfg *config.Config, req types.SwitchRequest) error {
	if req.Coalesced && req.Generation != state.Generation() {
		return nil
	}
	state.AutoSwitchMutex.RLock()
	paused := state.AutoSwitchPaused
	locked := state.LockedServer
	state.AutoSwitchMutex.RUnlock()
	if paused && !isManualSwitchType(req.Type) && req.Type != "recover" {
		return errors.New("auto switch paused")
	}
	if req.Type == "avoid_auto" && req.AvoidServer != state.CurrentServer() {
		return nil
	}
	if !atomic.CompareAndSwapInt32(&state.Switching, 0, 1) {
		return errors.New("already switching")
	}
	defer atomic.StoreInt32(&state.Switching, 0)

	if req.Type == "recover" {
		if state.IsNaiveRunning() {
			return nil
		}
		if !state.BeginRecovery(time.Now()) {
			return nil
		}
		current := state.CurrentServer()
		if current == "" && paused {
			current = locked
		}
		if current != "" {
			return RestartNaive(state, current)
		}
		if paused {
			return errors.New("auto switch paused and no current server to recover")
		}
	} else if !isManualSwitchType(req.Type) && !state.BeginAutoSwitch(time.Now()) {
		return nil
	}

	_, servers := state.Servers()
	if len(servers) == 0 && cfg.BootstrapNode != "" {
		servers = []string{cfg.BootstrapNode}
		state.SetServers(servers)
	}
	var err error
	switch req.Type {
	case "select":
		return ProcessSelectRequest(state, req)
	case "avoid", "avoid_auto":
		servers, err = HandleSwitch(state, cfg, servers, req.AvoidServer)
	case "auto":
		servers, err = handleSwitch(state, cfg, servers, "", true)
	case "recover":
		servers, err = HandleSwitch(state, cfg, servers, "")
	default:
		return fmt.Errorf("unknown switch type: %s", req.Type)
	}
	if err != nil {
		return err
	}
	state.SetServers(servers)
	if req.Type == "avoid" {
		state.AutoSwitchMutex.Lock()
		state.LockedServer = state.CurrentServer()
		ps := types.PersistedState{AutoSwitchPaused: state.AutoSwitchPaused, LockedServer: state.LockedServer}
		state.AutoSwitchMutex.Unlock()
		if err := types.SavePersistedState(common.BasePath, ps); err != nil {
			log.DebugF("Save persisted state error: %v\n", err)
		}
	}
	return nil
}

// MonitorProcess also retries failed startup without requiring user traffic.
func MonitorProcess(state *types.GlobalState, doSwitch chan<- types.SwitchRequest) {
	monitorProcess(state, doSwitch, 5*time.Second)
}

func monitorProcess(state *types.GlobalState, doSwitch chan<- types.SwitchRequest, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-state.AppContext.Done():
			return
		case <-ticker.C:
			server, generation, running := state.ProxySnapshot()
			if !running {
				state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "recover", Generation: generation})
			} else if state.RecentFailureCount(time.Now()) >= types.FailureThreshold {
				state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "avoid_auto", AvoidServer: server, Generation: generation})
			}
		}
	}
}

func isManualSwitchType(t string) bool {
	return t == "select" || t == "avoid"
}

// HandleSwitch 处理服务器切换逻辑
func HandleSwitch(state *types.GlobalState, cfg *config.Config, oldHostUrls []string, deadServer string) ([]string, error) {
	return handleSwitch(state, cfg, oldHostUrls, deadServer, false)
}

func handleSwitch(state *types.GlobalState, cfg *config.Config, oldHostUrls []string, deadServer string, periodic bool) ([]string, error) {
	// 记录故障服务器
	if deadServer != "" {
		u, err := url.Parse(deadServer)
		if err != nil {
			log.DebugF("Error parsing dead server URL: %v\n", err)
		} else {
			state.ServerDownPriorityMutex.Lock()
			state.ServerDownPriority[u.Hostname()]++
			state.ServerDownPriorityMutex.Unlock()
		}
	}

	// 获取最新的服务器列表
	hostUrls, err := subscription.Subscription(cfg.SubscribeURL)
	if err != nil {
		log.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	// Fastest 会清理和调整优先级，必须获取写锁
	state.ServerDownPriorityMutex.Lock()
	var newFastestUrl string
	if periodic && state.IsNaiveRunning() {
		newFastestUrl, err = subscription.FastestWithCurrent(hostUrls, state.ServerDownPriority, state.CurrentServer())
	} else {
		newFastestUrl, err = subscription.Fastest(hostUrls, state.ServerDownPriority, deadServer)
	}
	state.ServerDownPriorityMutex.Unlock()
	if err != nil {
		log.DebugF("Error choosing fastest: %v\n", err)
		return oldHostUrls, err
	}

	if state.CurrentServer() == newFastestUrl && state.IsNaiveRunning() {
		return hostUrls, nil
	}

	log.DebugF("Fastest: %s\n", newFastestUrl)

	// 重启到新服务器
	if err := RestartNaive(state, newFastestUrl); err != nil {
		return oldHostUrls, err
	}

	return hostUrls, nil
}
