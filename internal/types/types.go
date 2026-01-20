package types

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type PersistedState struct {
	AutoSwitchPaused bool   `json:"auto_switch_paused"`
	LockedServer     string `json:"locked_server"`
}

const persistedStateFile = "switcher_state.json"

func persistedStatePath(basePath string) string {
	return filepath.Join(basePath, persistedStateFile)
}

func LoadPersistedState(basePath string) (PersistedState, error) {
	path := persistedStatePath(basePath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PersistedState{}, nil
		}
		return PersistedState{}, err
	}
	var ps PersistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		return PersistedState{}, err
	}
	return ps, nil
}

func SavePersistedState(basePath string, ps PersistedState) error {
	path := persistedStatePath(basePath)
	tmp := path + ".tmp"
	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SwitchRequest 定义切换请求的类型
// Type: "auto", "avoid", "avoid_auto", "select"
type SwitchRequest struct {
	Type         string // "auto", "avoid", "select", "avoid_auto"
	TargetServer string // 目标服务器（用于select类型）
	AvoidServer  string // 避免的服务器（用于avoid类型）
}

// GlobalState 包含全局状态
// LockedServer: 锁定模式下持久化的节点
type GlobalState struct {
	ErrorCount              int32 // 使用 int32 以便使用 atomic 操作
	NaiveCmd                *exec.Cmd
	NaiveCmdCancel          context.CancelFunc // naive进程的取消函数
	NaiveCmdLock            sync.Mutex
	FastestUrl              string
	HostUrls                []string
	ServerDownPriority      map[string]int
	ServerDownPriorityMutex sync.RWMutex // 保护ServerDownPriority的并发访问
	AutoSwitchPaused        bool
	AutoSwitchMutex         sync.RWMutex
	LockedServer            string
	AppContext              context.Context // 应用程序上下文，用于控制进程启动
	StartTime               int64           // 启动时间戳
	Switching               int32           // 切换中标志，使用 atomic 操作
	Checking                int32           // 更新检查中标志，使用 atomic 操作
}
