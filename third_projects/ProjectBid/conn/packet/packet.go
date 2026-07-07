// Package packet 定义 Pomelo 协议的网络数据包类型与结构。
package packet

import (
	"errors"
	"fmt"
)

// Type 表示网络数据包类型（握手、心跳、数据、踢下线等）。
type Type byte

const (
	_         Type = iota
	Handshake Type = 0x01 // 握手：client ↔ server
	HandshakeAck  = 0x02 // 握手确认：client → server
	Heartbeat     = 0x03 // 心跳
	Data          = 0x04 // 普通数据包
	Kick          = 0x05 // 踢下线通知
)

var (
	ErrWrongPomeloPacketType = errors.New("数据包类型错误")
	ErrInvalidPomeloHeader   = errors.New("数据包头无效")
)

// Packet 表示一个网络数据包。
type Packet struct {
	Type   Type
	Length int
	Data   []byte
}

// New 创建一个空 Packet。
func New() *Packet {
	return &Packet{}
}

func (p *Packet) String() string {
	return fmt.Sprintf("类型: %d, 长度: %d, 数据: %s", p.Type, p.Length, string(p.Data))
}
