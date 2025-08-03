package switcher

import (
	"errors"
	"naiveswitcher/service"
	"naiveswitcher/internal/types"
)

// RestartNaive 重启 naive 进程到指定服务器
func RestartNaive(state *types.GlobalState, targetServer string) error {
	state.NaiveCmdLock.Lock()
	defer state.NaiveCmdLock.Unlock()

	// 停止当前进程
	if state.NaiveCmd != nil {
		if err := state.NaiveCmd.Process.Kill(); err != nil {
			service.DebugF("Error killing naive: %v\n", err)
		}
		state.NaiveCmd.Wait()
		state.NaiveCmd = nil
	}

	// 启动新进程
	var err error
	state.NaiveCmd, err = service.NaiveCmd(targetServer)
	if err != nil {
		service.DebugF("Error creating naive command: %v\n", err)
		return err
	}
	if err := state.NaiveCmd.Start(); err != nil {
		service.DebugF("Error starting naive: %v\n", err)
		return err
	}

	return nil
}

// ProcessSelectRequest 处理直接选择服务器的请求
func ProcessSelectRequest(state *types.GlobalState, req types.SwitchRequest) error {
	if req.TargetServer == "" {
		return errors.New("target server cannot be empty")
	}

	// 验证目标服务器是否在可用列表中
	var found bool
	for _, server := range state.HostUrls {
		if server == req.TargetServer {
			found = true
			break
		}
	}

	if !found {
		return errors.New("target server not found in available servers")
	}

	if state.FastestUrl == req.TargetServer {
		return errors.New("already connected to target server")
	}

	service.DebugF("Switching to selected server: %s\n", req.TargetServer)

	if err := RestartNaive(state, req.TargetServer); err != nil {
		return err
	}

	state.FastestUrl = req.TargetServer
	return nil
}
