package types

import (
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
}

// GlobalState 包含全局状态
type GlobalState struct {
	ErrorCount         int
	NaiveCmd           *exec.Cmd
	NaiveCmdLock       sync.Mutex
	FastestUrl         string
	HostUrls           []string
	ServerDownPriority map[string]int
	AutoSwitchPaused   bool
	AutoSwitchMutex    sync.RWMutex
}
