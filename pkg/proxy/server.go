package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
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

// ServeTCP serves the shared SOCKS TCP/UDP entry point.
func ServeTCP(state *types.GlobalState, l net.Listener, doSwitch chan<- types.SwitchRequest) {
	bufPool := &sync.Pool{New: func() any { return make([]byte, forwardBufferSize) }}
	ctx := state.AppContext
	if ctx == nil {
		ctx = context.Background()
	}
	if err := serveSOCKS(ctx, l, common.UpstreamListenPort, func(ctx context.Context, conn net.Conn) {
		handleConnectionContext(ctx, state, conn, bufPool, doSwitch, common.UpstreamListenPort)
	}, udpFailureObserver(state, doSwitch)); err != nil && ctx.Err() == nil {
		log.DebugF("SOCKS listener stopped: %v\n", err)
	}
}

// HandleConnection 处理单个连接
func HandleConnection(state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest) {
	handleConnection(state, conn, bufPool, doSwitch, common.UpstreamListenPort)
}

func handleConnection(state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest, upstreamAddress string) {
	ctx := state.AppContext
	if ctx == nil {
		ctx = context.Background()
	}
	handleConnectionContext(ctx, state, conn, bufPool, doSwitch, upstreamAddress)
}

func handleConnectionContext(ctx context.Context, state *types.GlobalState, conn net.Conn, bufPool *sync.Pool, doSwitch chan<- types.SwitchRequest, upstreamAddress string) {
	defer func() {
		conn.SetDeadline(time.Now())
		conn.Close()
	}()

	server, generation, running := state.ProxySnapshot()
	if !running {
		log.DebugF("No naive running\n")
		state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "recover", Generation: generation})
		return
	}

	var serverDown bool = true

	naiveConn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", upstreamAddress)
	if err != nil && ctx.Err() != nil {
		return
	}
	if err == nil {
		defer naiveConn.Close()
		stop := context.AfterFunc(ctx, func() { naiveConn.Close() })
		defer stop()
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
		if socks, ok := conn.(*socksTCPConn); ok {
			serverDown = isServerDown(int(written), socks.response[:])
		}
		bufPool.Put(buf)
	}

	if serverDown && state.RecordFailure(generation, time.Now()) {
		state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "avoid_auto", AvoidServer: server, Generation: generation})
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

func isServerDown(written int, data []byte) bool {
	if written != 12 {
		return false
	}
	var key [12]byte
	copy(key[:], data[:12])
	_, isDown := DataServerDown[key]
	return isDown
}

func udpFailureObserver(state *types.GlobalState, doSwitch chan<- types.SwitchRequest) func() func(error) {
	return func() func(error) {
		server, generation, running := state.ProxySnapshot()
		if !running {
			state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "recover", Generation: generation})
			return nil
		}
		return func(err error) {
			var upstreamErr upstreamConnectError
			if errors.As(err, &upstreamErr) && state.RecordFailure(generation, time.Now()) {
				state.QueueAutoSwitch(doSwitch, types.SwitchRequest{Type: "avoid_auto", AvoidServer: server, Generation: generation})
			}
		}
	}
}
