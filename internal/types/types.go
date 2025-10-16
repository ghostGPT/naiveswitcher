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

// StatusData represents the data passed to status template
type StatusData struct {
	CurrentServer    string
	ErrorCount       int
	DownStats        string
	AvoidServerURL   string // 重命名避免歧义：避免切换到此服务器
	NaiveVersion     string
	SwitcherVersion  string
	AutoSwitchPaused bool
	AvailableServers []string // 新增：可用服务器列表
	Uptime           string   // 运行时长
	StartTime        int64    // 启动时间戳（用于前端计算）
}

// GlobalState 包含全局状态
type GlobalState struct {
	ErrorCount         int
	NaiveCmd           *exec.Cmd
	NaiveCmdCancel     context.CancelFunc // naive进程的取消函数
	NaiveCmdLock       sync.Mutex
	FastestUrl         string
	HostUrls           []string
	ServerDownPriority map[string]int
	AutoSwitchPaused   bool
	AutoSwitchMutex    sync.RWMutex
	AppContext         context.Context // 应用程序上下文，用于控制进程启动
	StartTime          int64           // 启动时间戳
}
