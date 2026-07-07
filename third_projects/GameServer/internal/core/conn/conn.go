package conn

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"project/internal/core/codec"
)

// 网络连接
type Connection interface {
	Send(data []byte)
	Close() error
	RemoteAddr() string
	Done() <-chan struct{}
	LastRecvUnixNano() int64
	TouchRecv()
	Recv() <-chan *codec.Packet
}

// 基于 net.Conn 的连接实现
type TCPConn struct {
	conn      net.Conn
	sendCh    chan []byte
	done      chan struct{}
	closeOnce sync.Once
	lastRecv  atomic.Int64
	recvCh    chan *codec.Packet
}

// NewTCPConn 创建 TCP 连接包装
func NewTCPConn(c net.Conn) *TCPConn {
	t := &TCPConn{
		conn:   c,
		sendCh: make(chan []byte, 256),
		done:   make(chan struct{}),
		recvCh: make(chan *codec.Packet, 64),
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
	buf := append([]byte(nil), data...)
	select {
	case <-c.done:
		return
	case c.sendCh <- buf:
	default:
		// 发送队列满时丢弃，避免阻塞主循环
	}
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

func (c *TCPConn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case data := <-c.sendCh:
			if data == nil {
				continue
			}
			if _, err := c.conn.Write(data); err != nil {
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
		frame := make([]byte, 4+bodyLen)
		copy(frame[:4], hdr)
		if bodyLen > 0 {
			if _, err := io.ReadFull(c.conn, frame[4:]); err != nil {
				_ = c.Close()
				return
			}
		}
		c.TouchRecv()
		pkt, err := codec.DecodePacket(frame)
		if err != nil {
			continue
		}
		select {
		case c.recvCh <- &pkt:
		case <-c.done:
			return
		}
	}
}

// Recv 返回接收到的 packet 通道
func (c *TCPConn) Recv() <-chan *codec.Packet { return c.recvCh }

var _ Connection = (*TCPConn)(nil)

// ErrClosed 表示连接已关闭
var ErrClosed = errors.New("connection closed")
