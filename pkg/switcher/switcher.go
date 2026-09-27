package switcher

import (
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/log"
	"naiveswitcher/pkg/subscription"
)

// Switcher 处理切换请求
// 注意：此函数在单个 goroutine 中运行，从 doSwitch channel 顺序处理请求
// 使用原子标志避免并发切换，如果正在切换中则跳过新请求
func Switcher(state *types.GlobalState, cfg *config.Config, doSwitch <-chan types.SwitchRequest) {
	for switchReq := range doSwitch {
		state.AutoSwitchMutex.RLock()
		paused := state.AutoSwitchPaused
		state.AutoSwitchMutex.RUnlock()
		if paused && !isManualSwitchType(switchReq.Type) {
			log.DebugF("Auto switch paused, ignoring request type: %s\n", switchReq.Type)
			if switchReq.Result != nil {
				switchReq.Result <- errors.New("auto switch paused")
			}
			continue
		}

		// 检查是否正在切换，如果是则跳过
		if !atomic.CompareAndSwapInt32(&state.Switching, 0, 1) {
			log.DebugF("Already switching, skipping request\n")
			if switchReq.Result != nil {
				switchReq.Result <- errors.New("already switching")
			}
			continue
		}

		atomic.StoreInt32(&state.ErrorCount, 0)
		log.DebugF("Switch request: Type=%s, Target=%s, Avoid=%s\n",
			switchReq.Type, switchReq.TargetServer, switchReq.AvoidServer)

		// 确保有可用的服务器
		_, servers := state.Servers()
		if len(servers) == 0 && cfg.BootstrapNode != "" {
			servers = []string{cfg.BootstrapNode}
			state.SetServers(servers)
		}

		var err error
		switch switchReq.Type {
		case "select":
			err = ProcessSelectRequest(state, switchReq)
		case "avoid":
			servers, err = HandleSwitch(state, cfg, servers, switchReq.AvoidServer)
		case "avoid_auto":
			servers, err = HandleSwitch(state, cfg, servers, switchReq.AvoidServer)
		case "auto":
			servers, err = HandleSwitch(state, cfg, servers, "")
		default:
			err = fmt.Errorf("unknown switch type: %s", switchReq.Type)
		}
		if switchReq.Type != "select" && err == nil {
			state.SetServers(servers)
		}

		if err != nil {
			log.DebugF("Error switching: %v\n", err)
		} else if switchReq.Type == "avoid" {
			state.AutoSwitchMutex.Lock()
			state.LockedServer = state.CurrentServer()
			ps := types.PersistedState{
				AutoSwitchPaused: state.AutoSwitchPaused,
				LockedServer:     state.LockedServer,
			}
			state.AutoSwitchMutex.Unlock()
			if persistErr := types.SavePersistedState(common.BasePath, ps); persistErr != nil {
				log.DebugF("Save persisted state error: %v\n", persistErr)
			}
		}

		atomic.StoreInt32(&state.ErrorCount, 0)
		atomic.StoreInt32(&state.Switching, 0) // 重置切换标志
		if switchReq.Result != nil {
			switchReq.Result <- err
		}
		log.DebugF("Switching done\n")
	}
}

func isManualSwitchType(t string) bool {
	return t == "select" || t == "avoid"
}

// HandleSwitch 处理服务器切换逻辑
func HandleSwitch(state *types.GlobalState, cfg *config.Config, oldHostUrls []string, deadServer string) ([]string, error) {
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
	newFastestUrl, err := subscription.Fastest(hostUrls, state.ServerDownPriority, deadServer)
	state.ServerDownPriorityMutex.Unlock()
	if err != nil {
		log.DebugF("Error choosing fastest: %v\n", err)
		return oldHostUrls, err
	}

	if state.CurrentServer() == newFastestUrl {
		return hostUrls, nil
	}

	log.DebugF("Fastest: %s\n", newFastestUrl)

	// 重启到新服务器
	if err := RestartNaive(state, newFastestUrl); err != nil {
		return oldHostUrls, err
	}

	return hostUrls, nil
}
