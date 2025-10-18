//go:build unix

package service

import (
	"syscall"
	"time"

	"naiveswitcher/internal/types"
)

// KillProcessGroup 终止整个进程组
func KillProcessGroup(state *types.GlobalState, pid int) {
	pgid := pid // 进程组ID默认等于进程ID（因为我们设置了Setpgid）

	// 先尝试发送 SIGTERM 到整个进程组，给进程优雅退出的机会
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		DebugF("Error sending SIGTERM to process group (PGID: %d): %v, trying single process\n", pgid, err)
		// 如果进程组信号失败，尝试只发送给主进程
		if err := state.NaiveCmd.Process.Signal(syscall.SIGTERM); err != nil {
			DebugF("Error sending SIGTERM to naive process (PID: %d): %v\n", pid, err)
		}
	} else {
		DebugF("Sent SIGTERM to process group (PGID: %d)\n", pgid)
	}

	// 等待进程退出，带超时机制
	done := make(chan error, 1)
	go func() {
		done <- state.NaiveCmd.Wait()
	}()

	// 等待最多2秒
	select {
	case err := <-done:
		if err != nil {
			DebugF("Naive process (PID: %d) exited with error: %v\n", pid, err)
		} else {
			DebugF("Naive process (PID: %d) exited gracefully\n", pid)
		}
	case <-time.After(2 * time.Second):
		// 超时后强制杀死整个进程组
		DebugF("Naive process (PID: %d) did not exit after SIGTERM, sending SIGKILL to process group\n", pid)
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			DebugF("Error sending SIGKILL to process group (PGID: %d): %v, trying single process\n", pgid, err)
			// 如果进程组信号失败，尝试只杀死主进程
			if err := state.NaiveCmd.Process.Kill(); err != nil {
				DebugF("Error killing naive process (PID: %d): %v\n", pid, err)
			}
		}
		// 再等待一下，确保进程被清理
		<-done
	}
}
