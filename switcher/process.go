package switcher

import (
	"errors"

	"naiveswitcher/internal/types"
	"naiveswitcher/service"
)

// StopNaive 安全地停止 naive 进程（需要外部已获取锁）
func stopNaiveUnsafe(state *types.GlobalState) {
	if state.NaiveCmd != nil {
		if err := state.NaiveCmd.Cancel(); err != nil {
			service.DebugF("Error cancel naive: %v\n", err)
		}
		state.NaiveCmd.Wait()
		state.NaiveCmd = nil
	}
}

// StartNaive 安全地启动 naive 进程（需要外部已获取锁）
func startNaiveUnsafe(state *types.GlobalState, targetServer string) error {
	// 检查应用程序上下文是否已经取消
	select {
	case <-state.AppContext.Done():
		service.DebugF("Application is shutting down, not starting naive process\n")
		return nil // 不启动新进程，但不返回错误
	default:
		// 继续启动进程
	}

	var err error
	state.NaiveCmd, err = service.NaiveCmd(state, targetServer)
	if err != nil {
		service.DebugF("Error creating naive command: %v\n", err)
		return err
	}
	if err := state.NaiveCmd.Start(); err != nil {
		service.DebugF("Error starting naive: %v\n", err)
		return err
	}
	service.DebugF("Successfully started naive process for server: %s\n", targetServer)
	return nil
}

// RestartNaive 重启 naive 进程到指定服务器
func RestartNaive(state *types.GlobalState, targetServer string) error {
	state.NaiveCmdLock.Lock()
	defer state.NaiveCmdLock.Unlock()

	// 停止当前进程
	stopNaiveUnsafe(state)

	// 启动新进程
	return startNaiveUnsafe(state, targetServer)
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
