//go:build windows

package naive

import (
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/log"
)

// KillProcessGroup 终止整个进程组
func KillProcessGroup(state *types.GlobalState, pid int) {
	select {
	case <-state.NaiveDone:
		return // Already reaped by StartProcess.
	default:
	}

	// Windows 上直接使用 Kill 方法
	// CREATE_NEW_PROCESS_GROUP 标志会确保子进程也被终止
	if err := state.NaiveCmd.Process.Kill(); err != nil {
		log.DebugF("Error killing naive process (PID: %d): %v\n", pid, err)
	} else {
		log.DebugF("Sent kill signal to naive process (PID: %d)\n", pid)
	}

	// 等待进程退出
	done := state.NaiveDone
	if done == nil {
		// Compatibility for commands started outside StartProcess.
		waited := make(chan struct{})
		cmd := state.NaiveCmd
		go func() { cmd.Wait(); close(waited) }()
		done = waited
	}

	// 等待最多2秒
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		log.DebugF("Naive process (PID: %d) did not exit after 2 seconds\n", pid)
		// Windows 上 Kill() 已经是强制终止，没有更强的方式
		<-done
	}
}
