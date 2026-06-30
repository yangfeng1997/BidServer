// Package codec 定义数据包编解码器接口与 Pomelo 协议实现。
package codec

import (
	"errors"

	"projectbid/server/conn/packet"
)

const (
	// HeadLength 是 Pomelo 协议头长度（1字节类型 + 3字节长度）。
	HeadLength = 4
	// MaxPacketSize 是最大数据包大小（16MB）。
	MaxPacketSize = 1 << 24
)

var (
	ErrPacketSizeExcced = errors.New("数据包大小超出限制")
)

// PacketEncoder 将 raw bytes 编码为 Pomelo 协议二进制帧。
type PacketEncoder interface {
	Encode(typ packet.Type, data []byte) ([]byte, error)
}

// PacketDecoder 将二进制帧解码为 packet.Packet 切片。
type PacketDecoder interface {
	Decode(data []byte) ([]*packet.Packet, error)
}
