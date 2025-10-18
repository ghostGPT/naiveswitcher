package server

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/service"
	"naiveswitcher/util"
)

// DataServerDown 定义服务器下线检测的数据模式
var DataServerDown = map[[12]byte]struct{}{
	{
		5, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0,
	}: {},
}

// ServeTCP 启动 TCP 代理服务器
func ServeTCP(state *types.GlobalState, l net.Listener, doSwitch chan<- types.SwitchRequest) {
	bufPool := &sync.Pool{
		New: func() any {
			return make([]byte, 32*1024)
		},
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			service.DebugF("Error accepting connection: %v\n", err)
			continue
		}
		go HandleConnection(state, conn, bufPool, doSwitch)
	}
}

// HandleConnection 处理单个连接
func HandleConnection(state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest) {
	defer func() {
		conn.SetDeadline(time.Now())
		conn.Close()
	}()

	if state.NaiveCmd == nil {
		service.DebugF("No naive running\n")
		doSwitch <- types.SwitchRequest{Type: "auto"}
		return
	}

	var serverDown bool = true
	var remoteOk bool

	naiveConn, err := net.DialTimeout("tcp", service.UpstreamListenPort, 3*time.Second)
	if err == nil {
		go func() {
			defer func() {
				naiveConn.SetDeadline(time.Now())
				naiveConn.Close()
			}()
			_, e := io.Copy(naiveConn, conn)
			remoteOk = e == nil
		}()
		buf := bufPool.Get()
		written, _ := io.CopyBuffer(util.NewDowngradeReaderWriter(conn), util.NewDowngradeReaderWriter(naiveConn), buf.([]byte))
		serverDown = isServerDown(int(written), buf.([]byte), remoteOk)
		bufPool.Put(buf)
	}

	// 更新错误计数
	if serverDown {
		newCount := atomic.AddInt32(&state.ErrorCount, 1)
		// 错误过多时触发切换
		if newCount > 10 {
			atomic.StoreInt32(&state.ErrorCount, 0)
			service.DebugF("Too many errors (%d), switching server\n", newCount)
			doSwitch <- types.SwitchRequest{
				Type:        "avoid",
				AvoidServer: state.FastestUrl,
			}
		}
	} else {
		// 成功时减少错误计数（但不低于0）
		decrementErrorCount(&state.ErrorCount)
	}
}

// decrementErrorCount 原子地减少错误计数，但不会低于0
func decrementErrorCount(count *int32) {
	for {
		old := atomic.LoadInt32(count)
		if old <= 0 {
			return
		}
		if atomic.CompareAndSwapInt32(count, old, old-1) {
			return
		}
	}
}

func isServerDown(written int, data []byte, remoteOk bool) bool {
	if remoteOk {
		return false
	}
	if written != 12 {
		return false
	}
	var key [12]byte
	copy(key[:], data[:12])
	_, isDown := DataServerDown[key]
	return isDown
}
