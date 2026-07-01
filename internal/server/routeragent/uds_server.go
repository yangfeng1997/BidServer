package routeragent

import (
	"errors"
	"net"
	"os"
	"sync"
)

// 处理新连接
type ConnHandler func(*UDSConn)

// UDSServer 监听本机业务进程连接
type UDSServer struct {
	path    string
	handler ConnHandler
	ln      net.Listener
	once    sync.Once
}

// 创建 UDS 服务
func NewUDSServer(path string, handler ConnHandler) *UDSServer {
	return &UDSServer{path: path, handler: handler}
}

// Listen 开始监听
func (s *UDSServer) Listen() error {
	if s.path == "" {
		return errors.New("uds path is empty")
	}
	_ = os.Remove(s.path)
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

// Serve 接受连接
func (s *UDSServer) Serve(stopCh <-chan struct{}) {
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
			go s.handler(NewUDSConn(raw))
			continue
		}
		_ = raw.Close()
	}
}

// Close 关闭监听器
func (s *UDSServer) Close() error {
	var err error
	s.once.Do(func() {
		if s.ln != nil {
			err = s.ln.Close()
			_ = os.Remove(s.path)
		}
	})
	return err
}
