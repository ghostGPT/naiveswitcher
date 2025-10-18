//go:build windows

package naive

import (
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/log"
)

// KillProcessGroup 终止整个进程组
func KillProcessGroup(state *types.GlobalState, pid int) {
	// Windows 上直接使用 Kill 方法
	// CREATE_NEW_PROCESS_GROUP 标志会确保子进程也被终止
	if err := state.NaiveCmd.Process.Kill(); err != nil {
		log.DebugF("Error killing naive process (PID: %d): %v\n", pid, err)
	} else {
		log.DebugF("Sent kill signal to naive process (PID: %d)\n", pid)
	}

	// 等待进程退出
	done := make(chan error, 1)
	go func() {
		done <- state.NaiveCmd.Wait()
	}()

	// 等待最多2秒
	select {
	case err := <-done:
		if err != nil {
			log.DebugF("Naive process (PID: %d) exited with error: %v\n", pid, err)
		} else {
			log.DebugF("Naive process (PID: %d) exited gracefully\n", pid)
		}
	case <-time.After(2 * time.Second):
		log.DebugF("Naive process (PID: %d) did not exit after 2 seconds\n", pid)
		// Windows 上 Kill() 已经是强制终止，没有更强的方式
		<-done
	}
}
