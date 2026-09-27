package proxy

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/log"
	"naiveswitcher/util"
)

// DataServerDown 定义服务器下线检测的数据模式
var DataServerDown = map[[12]byte]struct{}{
	{
		5, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0,
	}: {},
}

const forwardBufferSize = 64 * 1024

// ServeTCP 启动 TCP 代理服务器
func ServeTCP(state *types.GlobalState, l net.Listener, doSwitch chan<- types.SwitchRequest) {
	bufPool := &sync.Pool{
		New: func() any {
			return make([]byte, forwardBufferSize)
		},
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			log.DebugF("Error accepting connection: %v\n", err)
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go HandleConnection(state, conn, bufPool, doSwitch)
	}
}

// HandleConnection 处理单个连接
func HandleConnection(state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest) {
	handleConnection(state, conn, bufPool, doSwitch, common.UpstreamListenPort)
}

func handleConnection(state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest, upstreamAddress string) {
	defer func() {
		conn.SetDeadline(time.Now())
		conn.Close()
	}()

	if !state.IsNaiveRunning() {
		log.DebugF("No naive running\n")
		queueSwitch(doSwitch, types.SwitchRequest{Type: "auto"})
		return
	}

	var serverDown bool = true

	naiveConn, err := net.DialTimeout("tcp", upstreamAddress, 3*time.Second)
	if err == nil {
		defer naiveConn.Close()
		uploadDone := make(chan struct{}, 1)
		go func() {
			io.Copy(naiveConn, conn)
			closeWrite(naiveConn)
			uploadDone <- struct{}{}
		}()
		buf := bufPool.Get()
		written, _ := io.CopyBuffer(util.NewDowngradeReaderWriter(conn), util.NewDowngradeReaderWriter(naiveConn), buf.([]byte))
		closeWrite(conn)
		closeRead(conn)
		<-uploadDone
		serverDown = isServerDown(int(written), buf.([]byte))
		bufPool.Put(buf)
	}

	// 更新错误计数
	if serverDown {
		newCount := atomic.AddInt32(&state.ErrorCount, 1)
		// 错误过多时触发切换
		if newCount > 10 {
			atomic.StoreInt32(&state.ErrorCount, 0)
			log.DebugF("Too many errors (%d), switching server\n", newCount)
			queueSwitch(doSwitch, types.SwitchRequest{
				Type:        "avoid_auto",
				AvoidServer: state.CurrentServer(),
			})
		}
	} else {
		// 成功时减少错误计数（但不低于0）
		decrementErrorCount(&state.ErrorCount)
	}
}

func queueSwitch(ch chan<- types.SwitchRequest, req types.SwitchRequest) {
	select {
	case ch <- req:
	default:
		log.DebugF("Switch queue full, skipping duplicate request: %s\n", req.Type)
	}
}

func closeWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		c.CloseWrite()
	} else {
		conn.Close()
	}
}

func closeRead(conn net.Conn) {
	if c, ok := conn.(interface{ CloseRead() error }); ok {
		c.CloseRead()
	} else {
		conn.SetReadDeadline(time.Now())
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

func isServerDown(written int, data []byte) bool {
	if written != 12 {
		return false
	}
	var key [12]byte
	copy(key[:], data[:12])
	_, isDown := DataServerDown[key]
	return isDown
}
