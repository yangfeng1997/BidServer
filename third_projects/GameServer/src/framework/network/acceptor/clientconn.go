package acceptor

import (
	"io"
	"net"
	"project/src/framework/network/packet"
)

// ClientConn 客户端连接抽象，在 net.Conn 基础上增加完整 Packet 读取能力。
// 不同协议各自实现帧边界处理：TCP 用4字节头（type+length），WS 用原生帧。
// ReadPacket 返回完整的外层 Packet，调用方再按 packet.Type 处理。
type ClientConn interface {
	// ReadPacket 读取一个完整的外层 Packet
	ReadPacket() (*packet.Packet, error)
	net.Conn
}

// tcpClientConn 基于外层 Packet 协议的 TCP 连接实现
// 帧格式：| type(1) | length(3, big-endian) | body |
type tcpClientConn struct {
	net.Conn
}

func newTCPClientConn(conn net.Conn) ClientConn {
	return &tcpClientConn{Conn: conn}
}

func (c *tcpClientConn) ReadPacket() (*packet.Packet, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(c.Conn, header); err != nil {
		return nil, err
	}
	t, n, err := packet.DecodeHeader(header)
	if err != nil {
		return nil, err
	}
	body := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(c.Conn, body); err != nil {
			return nil, err
		}
	}
	return &packet.Packet{Type: t, Body: body}, nil
}
