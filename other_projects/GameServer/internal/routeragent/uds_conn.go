package routeragent

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
)

// UDSConn 表示一条 UDS 连接
type UDSConn struct {
	conn       net.Conn
	remoteAddr string
	sendCh     chan Frame
	recvCh     chan Frame
	done       chan struct{}
	closeOnce  sync.Once
}

// 包装 net.Conn
func NewUDSConn(c net.Conn) *UDSConn {
	u := &UDSConn{
		conn:       c,
		remoteAddr: c.RemoteAddr().String(),
		sendCh:     make(chan Frame, 64),
		recvCh:     make(chan Frame, 64),
		done:       make(chan struct{}),
	}
	go u.readLoop()
	go u.writeLoop()
	return u
}

// 返回远端地址
func (u *UDSConn) RemoteAddr() string { return u.remoteAddr }

// Recv 返回接收通道
func (u *UDSConn) Recv() <-chan Frame { return u.recvCh }

// Send 投递一条帧
func (u *UDSConn) Send(frame Frame) error {
	if u == nil || u.sendCh == nil {
		return io.ErrClosedPipe
	}
	select {
	case <-u.done:
		return io.EOF
	case u.sendCh <- frame:
		return nil
	}
}

// Close 关闭连接
func (u *UDSConn) Close() error {
	if u == nil {
		return nil
	}
	var err error
	u.closeOnce.Do(func() {
		close(u.done)
		err = u.conn.Close()
	})
	return err
}

func (u *UDSConn) readLoop() {
	defer close(u.recvCh)
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(u.conn, hdr); err != nil {
			_ = u.Close()
			return
		}
		length := int(binary.BigEndian.Uint32(hdr))
		if length < 3 {
			_ = u.Close()
			return
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(u.conn, buf); err != nil {
			_ = u.Close()
			return
		}
		data := make([]byte, 4+length)
		copy(data[:4], hdr)
		copy(data[4:], buf)
		frame, err := DecodeFrame(data)
		if err != nil {
			continue
		}
		select {
		case u.recvCh <- frame:
		case <-u.done:
			return
		}
	}
}

func (u *UDSConn) writeLoop() {
	for {
		select {
		case <-u.done:
			return
		case frame := <-u.sendCh:
			data, err := EncodeFrame(frame)
			if err != nil {
				continue
			}
			if _, err := u.conn.Write(data); err != nil {
				_ = u.Close()
				return
			}
		}
	}
}

// NewTestUDSConn 创建用于集成测试的 UDS 连接（公开 sendCh/recvCh）
func NewTestUDSConn(remoteAddr string) *UDSConn {
	ch := make(chan Frame, 64)
	return &UDSConn{
		remoteAddr: remoteAddr,
		sendCh:     ch,
		recvCh:     ch,
		done:       make(chan struct{}),
	}
}
