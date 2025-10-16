package switcher

import (
	"errors"
	"fmt"
	"sync"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/service"
	"net/url"
)

// Switcher 处理切换请求
func Switcher(state *types.GlobalState, cfg *config.Config, doSwitch <-chan types.SwitchRequest) {
	var switching bool
	var switchingLock sync.Mutex
	for switchReq := range doSwitch {
		switchingLock.Lock()
		if switching {
			switchingLock.Unlock()
			continue
		}
		switching = true
		switchingLock.Unlock()

		state.ErrorCount = 0
		service.DebugF("Switch request: Type=%s, Target=%s, Avoid=%s\n",
			switchReq.Type, switchReq.TargetServer, switchReq.AvoidServer)

		go func(req types.SwitchRequest) {
			defer func() {
				switchingLock.Lock()
				switching = false
				switchingLock.Unlock()
				state.ErrorCount = 0
				service.DebugF("Switching done\n")
			}()

			// 确保有可用的服务器
			if len(state.HostUrls) == 0 {
				state.HostUrls = append(state.HostUrls, cfg.BootstrapNode)
			}

			var err error
			switch req.Type {
			case "select":
				err = ProcessSelectRequest(state, req)
			case "avoid":
				state.HostUrls, err = HandleSwitch(state, cfg, state.HostUrls, req.AvoidServer)
			case "auto":
				state.HostUrls, err = HandleSwitch(state, cfg, state.HostUrls, "")
			default:
				err = fmt.Errorf("unknown switch type: %s", req.Type)
			}

			if err != nil {
				service.DebugF("Error switching: %v\n", err)
			}
		}(switchReq)
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
			state.ServerDownPriority[u.Hostname()]++
		}
	}

	// 获取最新的服务器列表
	hostUrls, err := service.Subscription(cfg.SubscribeURL)
	if err != nil {
		service.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	// 选择最佳服务器
	newFastestUrl, err := service.Fastest(hostUrls, state.ServerDownPriority, deadServer)
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
