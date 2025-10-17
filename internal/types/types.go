package types

import (
	"context"
	"os/exec"
	"sync"
)

// SwitchRequest 定义切换请求的类型
type SwitchRequest struct {
	Type         string // "auto", "avoid", "select"
	TargetServer string // 目标服务器（用于select类型）
	AvoidServer  string // 避免的服务器（用于avoid类型）
}

// GlobalState 包含全局状态
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
	AppContext              context.Context // 应用程序上下文，用于控制进程启动
	StartTime               int64           // 启动时间戳
	Switching               int32           // 切换中标志，使用 atomic 操作
	Checking                int32           // 更新检查中标志，使用 atomic 操作
}
