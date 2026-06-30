package acceptor

import (
	"net"
	"sync/atomic"
	"time"

	"project/src/common/logger"
)

const defaultConnChanSize = 64

// TCPAcceptor 基于 TCP 协议的监听器实现，帧格式使用 LTV（4字节大端长度前缀）
type TCPAcceptor struct {
	addr     string
	connChan chan ClientConn
	listener atomic.Pointer[net.Listener]
	running  atomic.Bool
}

// NewTCPAcceptor 创建 TCPAcceptor，可通过 TCPOption 覆盖默认配置
func NewTCPAcceptor(addr string, opts ...TCPOption) *TCPAcceptor {
	a := &TCPAcceptor{
		addr:     addr,
		connChan: make(chan ClientConn, defaultConnChanSize),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *TCPAcceptor) ConnChan() chan ClientConn {
	return a.connChan
}

func (a *TCPAcceptor) IsRunning() bool {
	return a.running.Load()
}

// ListenAndServe 开始监听，阻塞直到 Stop 被调用。应在独立 goroutine 中运行。
func (a *TCPAcceptor) ListenAndServe() {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		// 与 WSAcceptor 一致：listen 失败不 panic（panic 发生在 application.Start() 启动的
		// 独立 goroutine 中无法 recover，会崩整进程），改为记录错误 + 关闭 connChan
		// （通知消费方退出）后返回。失败时 running 尚未置 true，无需复位。
		logger.Error("tcp listen failed", logger.String("addr", a.addr), logger.Err(err))
		close(a.connChan)
		return
	}
	a.listener.Store(&ln)
	a.running.Store(true)
	defer func() {
		a.running.Store(false)
		close(a.connChan)
	}()

	var tempDelay time.Duration
	for {
		conn, err := (*a.listener.Load()).Accept()
		if err != nil {
			if !a.running.Load() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > 1*time.Second {
					tempDelay = 1 * time.Second
				}
				time.Sleep(tempDelay)
				continue
			}
			return
		}
		tempDelay = 0
		a.connChan <- newTCPClientConn(conn)
	}
}

// Stop 停止监听，关闭底层 listener，触发 ListenAndServe 退出
func (a *TCPAcceptor) Stop() {
	if a.running.CompareAndSwap(true, false) {
		if ln := a.listener.Load(); ln != nil {
			(*ln).Close()
		}
	}
}
