package conn

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"project/internal/core/codec"
	"project/pkg/logger"
)

// 网络连接
type Connection interface {
	Send(data []byte)
	Close() error
	RemoteAddr() string
	Done() <-chan struct{}
	LastRecvUnixNano() int64
	TouchRecv()
	Recv() <-chan codec.Packet
}

const (
	tcpConnSendQueueSize = 256
	tcpConnRecvQueueSize = 256
)

// 基于 net.Conn 的连接实现
type TCPConn struct {
	conn      net.Conn
	sendCh    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	lastRecv  atomic.Int64
	recvCh    chan codec.Packet
}

// NewTCPConn 创建 TCP 连接包装
func NewTCPConn(c net.Conn) *TCPConn {
	t := &TCPConn{
		conn:   c,
		sendCh: make(chan []byte, tcpConnSendQueueSize),
		done:   make(chan struct{}),
		recvCh: make(chan codec.Packet, tcpConnRecvQueueSize),
	}
	t.TouchRecv()
	go t.writeLoop()
	go t.readLoop()
	return t
}

// 异步发送数据
func (c *TCPConn) Send(data []byte) {
	if len(data) == 0 {
		return
	}
	c.send(append([]byte(nil), data...))
}

// SendOwned 异步发送调用方移交所有权的数据，入队后调用方不得再修改 data。
func (c *TCPConn) SendOwned(data []byte) {
	if len(data) == 0 {
		return
	}
	c.send(data)
}

func (c *TCPConn) send(data []byte) {
	select {
	case <-c.done:
		return
	case c.sendCh <- data:
	default:
		logger.Warn("tcp conn send queue full, drop packet", logger.String("remote", c.RemoteAddr()), logger.Int("queue_len", len(c.sendCh)), logger.Int("queue_cap", cap(c.sendCh)))
	}
}

// SendOwned 发送调用方移交所有权的数据；不支持快速路径的连接会退化为安全复制。
func SendOwned(c Connection, data []byte) {
	if owned, ok := c.(interface{ SendOwned([]byte) }); ok {
		owned.SendOwned(data)
		return
	}
	c.Send(data)
}

// Close 关闭连接
func (c *TCPConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.conn.Close()
	})
	return err
}

// 返回远端地址
func (c *TCPConn) RemoteAddr() string {
	if c.conn == nil || c.conn.RemoteAddr() == nil {
		return ""
	}
	return c.conn.RemoteAddr().String()
}

// Done 返回关闭通知通道
func (c *TCPConn) Done() <-chan struct{} { return c.done }

// LastRecvUnixNano 返回最后一次收到数据的时间戳
func (c *TCPConn) LastRecvUnixNano() int64 { return c.lastRecv.Load() }

// TouchRecv 刷新最近收包时间
func (c *TCPConn) TouchRecv() { c.lastRecv.Store(time.Now().UnixNano()) }

const tcpWriteBatchMaxFrames = 16

func (c *TCPConn) writeLoop() {
	buf := make([]byte, 0, 65536)
	for {
		select {
		case <-c.done:
			return
		case data := <-c.sendCh:
			if data == nil {
				continue
			}
			buf = append(buf[:0], data...)
			for drained := 1; drained < tcpWriteBatchMaxFrames; drained++ {
				select {
				case d2 := <-c.sendCh:
					if d2 == nil {
						continue
					}
					buf = append(buf, d2...)
				default:
					goto flushNow
				}
			}
		flushNow:
			if _, err := c.conn.Write(buf); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *TCPConn) readLoop() {
	defer close(c.recvCh)
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(c.conn, hdr); err != nil {
			_ = c.Close()
			return
		}
		bodyLen := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		body := make([]byte, bodyLen)
		if bodyLen > 0 {
			if _, err := io.ReadFull(c.conn, body); err != nil {
				_ = c.Close()
				return
			}
		}
		c.TouchRecv()
		pkt := codec.Packet{Type: codec.PacketType(hdr[0]), Body: body}
		select {
		case c.recvCh <- pkt:
		case <-c.done:
			return
		}
	}
}

// Recv 返回接收到的 packet 通道
func (c *TCPConn) Recv() <-chan codec.Packet { return c.recvCh }

var _ Connection = (*TCPConn)(nil)

// ErrClosed 表示连接已关闭
var ErrClosed = errors.New("connection closed")
