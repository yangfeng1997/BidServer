package routeragent

import (
	"errors"
	"net"
	"sync"
)

// PeerHandler 处理远端 RA 连接
type PeerHandler func(conn net.Conn, listenAddr string)

// TCPServer 监听远端 RA 的 TCP 连接
type TCPServer struct {
	addr       string
	listenAddr string
	handler    PeerHandler
	ln         net.Listener
	once       sync.Once
}

// NewTCPServer 创建 TCP 服务
func NewTCPServer(addr, listenAddr string, handler PeerHandler) *TCPServer {
	return &TCPServer{addr: addr, listenAddr: listenAddr, handler: handler}
}

// Listen 开始监听
func (s *TCPServer) Listen() error {
	if s.addr == "" {
		return errors.New("tcp addr is empty")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Serve 接受连接
func (s *TCPServer) Serve(stopCh <-chan struct{}) {
	if s.ln == nil {
		return
	}
	for {
		raw, err := s.ln.Accept()
		if err != nil {
			select {
			case <-stopCh:
				return
			default:
			}
			continue
		}
		if s.handler != nil {
			go s.handler(raw, s.listenAddr)
		} else {
			raw.Close()
		}
	}
}

// Close 关闭监听器
func (s *TCPServer) Close() error {
	var err error
	s.once.Do(func() {
		if s.ln != nil {
			err = s.ln.Close()
		}
	})
	return err
}
