package switcher

import (
	"errors"
	"syscall"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/service"
)

// StopNaive 安全地停止 naive 进程（需要外部已获取锁）
func stopNaiveUnsafe(state *types.GlobalState) {
	if state.NaiveCmd == nil {
		return
	}

	// 1. 先取消 context，触发进程优雅退出
	if state.NaiveCmdCancel != nil {
		state.NaiveCmdCancel()
		state.NaiveCmdCancel = nil
	}

	// 2. 如果进程还在运行，尝试终止
	if state.NaiveCmd.Process != nil {
		pid := state.NaiveCmd.Process.Pid

		// 2.1 先尝试发送 SIGTERM 到整个进程组，给进程优雅退出的机会
		// 使用负PID来向整个进程组发送信号
		pgid := pid // 进程组ID默认等于进程ID（因为我们设置了Setpgid）
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			service.DebugF("Error sending SIGTERM to process group (PGID: %d): %v, trying single process\n", pgid, err)
			// 如果进程组信号失败，尝试只发送给主进程
			if err := state.NaiveCmd.Process.Signal(syscall.SIGTERM); err != nil {
				service.DebugF("Error sending SIGTERM to naive process (PID: %d): %v\n", pid, err)
			}
		} else {
			service.DebugF("Sent SIGTERM to process group (PGID: %d)\n", pgid)
		}

		// 2.2 等待进程退出，带超时机制
		done := make(chan error, 1)
		go func() {
			done <- state.NaiveCmd.Wait()
		}()

		// 等待最多2秒
		select {
		case err := <-done:
			if err != nil {
				service.DebugF("Naive process (PID: %d) exited with error: %v\n", pid, err)
			} else {
				service.DebugF("Naive process (PID: %d) exited gracefully\n", pid)
			}
		case <-time.After(2 * time.Second):
			// 超时后强制杀死整个进程组
			service.DebugF("Naive process (PID: %d) did not exit after SIGTERM, sending SIGKILL to process group\n", pid)
			if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
				service.DebugF("Error sending SIGKILL to process group (PGID: %d): %v, trying single process\n", pgid, err)
				// 如果进程组信号失败，尝试只杀死主进程
				if err := state.NaiveCmd.Process.Kill(); err != nil {
					service.DebugF("Error killing naive process (PID: %d): %v\n", pid, err)
				}
			}
			// 再等待一下，确保进程被清理
			<-done
		}
	}

	state.NaiveCmd = nil
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
	state.NaiveCmd, state.NaiveCmdCancel, err = service.NaiveCmd(state, targetServer)
	if err != nil {
		service.DebugF("Error creating naive command: %v\n", err)
		return err
	}
	if err := state.NaiveCmd.Start(); err != nil {
		service.DebugF("Error starting naive: %v\n", err)
		// 如果启动失败，取消 context 释放资源
		if state.NaiveCmdCancel != nil {
			state.NaiveCmdCancel()
			state.NaiveCmdCancel = nil
		}
		state.NaiveCmd = nil
		return err
	}
	service.DebugF("Successfully started naive process (PID: %d) for server: %s\n", state.NaiveCmd.Process.Pid, targetServer)
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
