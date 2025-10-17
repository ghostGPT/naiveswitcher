package switcher

import (
	"errors"
	"fmt"
	"net/url"
	"sync/atomic"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/service"
)

// Switcher 处理切换请求
// 注意：此函数在单个 goroutine 中运行，从 doSwitch channel 顺序处理请求
// 使用原子标志避免并发切换，如果正在切换中则跳过新请求
func Switcher(state *types.GlobalState, cfg *config.Config, doSwitch <-chan types.SwitchRequest) {
	for switchReq := range doSwitch {
		// 检查是否正在切换，如果是则跳过
		if !atomic.CompareAndSwapInt32(&state.Switching, 0, 1) {
			service.DebugF("Already switching, skipping request\n")
			continue
		}

		atomic.StoreInt32(&state.ErrorCount, 0)
		service.DebugF("Switch request: Type=%s, Target=%s, Avoid=%s\n",
			switchReq.Type, switchReq.TargetServer, switchReq.AvoidServer)

		// 确保有可用的服务器
		if len(state.HostUrls) == 0 {
			state.HostUrls = append(state.HostUrls, cfg.BootstrapNode)
		}

		var err error
		switch switchReq.Type {
		case "select":
			err = ProcessSelectRequest(state, switchReq)
		case "avoid":
			state.HostUrls, err = HandleSwitch(state, cfg, state.HostUrls, switchReq.AvoidServer)
		case "auto":
			state.HostUrls, err = HandleSwitch(state, cfg, state.HostUrls, "")
		default:
			err = fmt.Errorf("unknown switch type: %s", switchReq.Type)
		}

		if err != nil {
			service.DebugF("Error switching: %v\n", err)
		}

		atomic.StoreInt32(&state.ErrorCount, 0)
		atomic.StoreInt32(&state.Switching, 0) // 重置切换标志
		service.DebugF("Switching done\n")
	}
}

// HandleSwitch 处理服务器切换逻辑
func HandleSwitch(state *types.GlobalState, cfg *config.Config, oldHostUrls []string, deadServer string) ([]string, error) {
	// 记录故障服务器
	if deadServer != "" {
		u, err := url.Parse(deadServer)
		if err != nil {
			service.DebugF("Error parsing dead server URL: %v\n", err)
		} else {
			state.ServerDownPriorityMutex.Lock()
			state.ServerDownPriority[u.Hostname()]++
			state.ServerDownPriorityMutex.Unlock()
		}
	}

	// 获取最新的服务器列表
	hostUrls, err := service.Subscription(cfg.SubscribeURL)
	if err != nil {
		service.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	// 选择最佳服务器（需要读锁保护）
	state.ServerDownPriorityMutex.RLock()
	newFastestUrl, err := service.Fastest(hostUrls, state.ServerDownPriority, deadServer)
	state.ServerDownPriorityMutex.RUnlock()
	if err != nil {
		service.DebugF("Error choosing fastest: %v\n", err)
		return nil, err
	}

	if state.FastestUrl == newFastestUrl {
		return hostUrls, errors.New("no change")
	}

	service.DebugF("Fastest: %s\n", newFastestUrl)

	// 重启到新服务器
	if err := RestartNaive(state, newFastestUrl); err != nil {
		return nil, err
	}

	state.FastestUrl = newFastestUrl
	return hostUrls, nil
}
