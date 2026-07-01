package acceptor

import (
	"errors"
	"net"
	"sync"

	"project/internal/core/conn"
)

// 连接接入器
type Acceptor interface {
	Listen() error
	Accept() <-chan conn.Connection
	Close() error
}

// TCP 连接接入器
type TCPAcceptor struct {
	addr   string
	ln     net.Listener
	ch     chan conn.Connection
	done   chan struct{}
	once   sync.Once
	listen sync.Once
}

// 创建 TCP 接入器
func NewTCPAcceptor(addr string) *TCPAcceptor {
	return &TCPAcceptor{
		addr: addr,
		ch:   make(chan conn.Connection, 64),
		done: make(chan struct{}),
	}
}

// Listen 开始监听
func (a *TCPAcceptor) Listen() error {
	if a.addr == "" {
		return errors.New("acceptor: empty listen addr")
	}
	var err error
	a.listen.Do(func() {
		a.ln, err = net.Listen("tcp", a.addr)
		if err != nil {
			return
		}
		go a.loop()
	})
	return err
}

// Accept 返回新连接通道
func (a *TCPAcceptor) Accept() <-chan conn.Connection { return a.ch }

// Close 关闭监听器
func (a *TCPAcceptor) Close() error {
	var err error
	a.once.Do(func() {
		close(a.done)
		if a.ln != nil {
			err = a.ln.Close()
		}
	})
	return err
}

func (a *TCPAcceptor) loop() {
	for {
		raw, err := a.ln.Accept()
		if err != nil {
			select {
			case <-a.done:
				return
			default:
			}
			continue
		}
		wrapped := conn.NewTCPConn(raw)
		select {
		case a.ch <- wrapped:
		case <-a.done:
			_ = wrapped.Close()
			return
		}
	}
}

var _ Acceptor = (*TCPAcceptor)(nil)
