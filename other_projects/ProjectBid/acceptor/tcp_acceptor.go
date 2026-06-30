package acceptor

import (
	"io"
	"net"

	"projectbid/server/conn/codec"
	"projectbid/server/logger"
)

// TCPAcceptor 实现 TCP 网络监听。
type TCPAcceptor struct {
	opts     Options
	listener net.Listener
	connChan chan PlayerConn
	running  bool
}

// NewTCPAcceptor 创建 TCP 监听器。
func NewTCPAcceptor(opts Options) *TCPAcceptor {
	return &TCPAcceptor{
		opts:     opts,
		connChan: make(chan PlayerConn, opts.MessagesBufferSize),
	}
}

// GetAddr 返回监听地址。
func (a *TCPAcceptor) GetAddr() string {
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return ""
}

// GetConnChan 返回新连接通道。
func (a *TCPAcceptor) GetConnChan() <-chan PlayerConn {
	return a.connChan
}

// ListenAndServe 开始监听。
func (a *TCPAcceptor) ListenAndServe() error {
	var err error
	a.listener, err = net.Listen("tcp", a.opts.Addr)
	if err != nil {
		return err
	}

	a.running = true
	logger.Infow("TCP 监听器已启动", "地址", a.opts.Addr)

	go a.serve()
	return nil
}

// Stop 停止监听。
func (a *TCPAcceptor) Stop() error {
	a.running = false
	if a.listener != nil {
		return a.listener.Close()
	}
	return nil
}

func (a *TCPAcceptor) serve() {
	defer func() {
		if a.listener != nil {
			a.listener.Close()
		}
	}()

	for a.running {
		conn, err := a.listener.Accept()
		if err != nil {
			if a.running {
				logger.Errorw("接受 TCP 连接失败", "错误", err)
			}
			continue
		}
		a.connChan <- newTCPConn(conn, a.opts.PacketDecoder)
	}
}

// ——— tcpConn ———

type tcpConn struct {
	net.Conn
	decoder codec.PacketDecoder
}

func newTCPConn(conn net.Conn, decoder codec.PacketDecoder) *tcpConn {
	return &tcpConn{
		Conn:    conn,
		decoder: decoder,
	}
}

// GetNextMessage 从 TCP 流中读取经过 Pomelo 编码的完整消息。
func (c *tcpConn) GetNextMessage() ([]byte, error) {
	// 先读 4 字节头
	header, err := io.ReadAll(io.LimitReader(c.Conn, codec.HeadLength))
	if err != nil {
		return nil, err
	}
	if len(header) == 0 {
		return nil, io.EOF
	}
	if len(header) < codec.HeadLength {
		return nil, io.ErrUnexpectedEOF
	}

	msgSize, _, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	msgData, err := io.ReadAll(io.LimitReader(c.Conn, int64(msgSize)))
	if err != nil {
		return nil, err
	}
	if len(msgData) < msgSize {
		return nil, io.ErrUnexpectedEOF
	}

	return append(header, msgData...), nil
}
