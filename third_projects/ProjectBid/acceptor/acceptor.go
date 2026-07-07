// Package acceptor 提供网络监听器抽象，支持 TCP、WebSocket、KCP 协议。
package acceptor

import (
	"net"
	"strings"
	"time"

	"projectbid/server/conn/codec"
	"projectbid/server/conn/message"
	"projectbid/server/serialize"
)

// Transport 传输协议类型。
type Transport string

const (
	// TCPTransport 表示 TCP 传输。
	TCPTransport Transport = "tcp"
	// WSTransport 表示 WebSocket 传输。
	WSTransport Transport = "ws"
	// KCPTransport 表示 KCP 传输（基于 UDP 的可靠协议，适合弱网场景）。
	KCPTransport Transport = "kcp"
)

// PlayerConn 封装底层网络连接，提供 Pomelo 协议的消息读写能力。
type PlayerConn interface {
	net.Conn
	// GetNextMessage 从连接读取下一条完整消息（已解码的 Pomelo body）。
	GetNextMessage() ([]byte, error)
}

// Acceptor 表示网络监听器。
type Acceptor interface {
	// ListenAndServe 开始监听并处理新连接。
	ListenAndServe() error
	// Stop 优雅关闭监听器。
	Stop() error
	// GetConnChan 返回新连接通道，上层（Agent）从此通道接收连接。
	GetConnChan() <-chan PlayerConn
	// GetAddr 返回监听地址。
	GetAddr() string
}

// Options 创建 Acceptor 时的配置参数。
type Options struct {
	Addr               string
	PacketDecoder      codec.PacketDecoder
	PacketEncoder      codec.PacketEncoder
	MessageEncoder     message.Encoder
	Serializer         serialize.Serializer
	HeartbeatTimeout   time.Duration
	WriteTimeout       time.Duration
	MessagesBufferSize int
	Transport          Transport
	// KCPOptions KCP 协议专用配置，仅 Transport 为 KCP 时生效。
	KCPOptions interface{}
}

// NewAcceptor 根据 Options 创建对应传输协议的 Acceptor。
func NewAcceptor(opts Options) Acceptor {
	switch opts.Transport {
	case WSTransport:
		return NewWSAcceptor(opts)
	case KCPTransport:
		return NewKCPAcceptor(opts)
	default:
		return NewTCPAcceptor(opts)
	}
}

// trimScheme 移除地址中的协议前缀（如 "ws://"）。
func trimScheme(addr string) string {
	for _, scheme := range []string{"ws://", "wss://", "tcp://", "kcp://"} {
		addr = strings.TrimPrefix(addr, scheme)
	}
	return addr
}
