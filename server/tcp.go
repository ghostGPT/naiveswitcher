package server

import (
	"io"
	"net"
	"sync"
	"time"

	"naiveswitcher/service"
	"naiveswitcher/internal/types"
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
		New: func() interface{} {
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
	defer conn.Close()

	if state.NaiveCmd == nil {
		service.DebugF("No naive running\n")
		doSwitch <- types.SwitchRequest{Type: "auto"}
		return
	}

	var serverDown bool = true
	var remoteOk bool

	naiveConn, err := net.DialTimeout("tcp", service.UpstreamListenPort, 3*time.Second)
	if err == nil {
		if tcpConn, ok := naiveConn.(*net.TCPConn); ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(2 * time.Minute)
		}
		go func() {
			defer naiveConn.Close()
			_, e := io.Copy(naiveConn, conn)
			remoteOk = e == nil
		}()
		buf := bufPool.Get()
		written, _ := io.CopyBuffer(util.NewDowngradeReaderWriter(conn), util.NewDowngradeReaderWriter(naiveConn), buf.([]byte))
		serverDown = isServerDown(int(written), buf.([]byte), remoteOk)
		bufPool.Put(buf)
	}

	if serverDown {
		state.ErrorCount++
	} else if state.ErrorCount > 0 {
		state.ErrorCount--
	}

	if state.ErrorCount > 10 {
		state.ErrorCount = 0
		service.DebugF("Too many errors, gonna switch\n")
		doSwitch <- types.SwitchRequest{
			Type:        "avoid",
			AvoidServer: state.FastestUrl,
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
