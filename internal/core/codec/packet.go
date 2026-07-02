package codec

import (
	"encoding/binary"
	"fmt"
)

// 外层帧类型
type PacketType uint8

const (
	PacketHandshake    PacketType = 0x01
	PacketHandshakeAck PacketType = 0x02
	PacketHeartbeat    PacketType = 0x03
	PacketData         PacketType = 0x04
	PacketKick         PacketType = 0x05
)

// 外层帧
type Packet struct {
	Type PacketType
	Body []byte
}

// 编码为字节切片
// 格式为 type + length3 + body
func EncodePacket(p Packet) ([]byte, error) {
	if len(p.Body) > 0xFFFFFF {
		return nil, fmt.Errorf("packet too large: %d", len(p.Body))
	}
	out := make([]byte, 4+len(p.Body))
	out[0] = byte(p.Type)
	putUint24(out[1:4], uint32(len(p.Body)))
	copy(out[4:], p.Body)
	return out, nil
}

// 解码为 Packet
func DecodePacket(data []byte) (Packet, error) {
	if len(data) < 4 {
		return Packet{}, fmt.Errorf("packet too short: %d", len(data))
	}
	bodyLen := int(readUint24(data[1:4]))
	if len(data) != 4+bodyLen {
		return Packet{}, fmt.Errorf("packet length mismatch: want %d got %d", 4+bodyLen, len(data))
	}
	return Packet{Type: PacketType(data[0]), Body: append([]byte(nil), data[4:]...)}, nil
}

func putUint24(dst []byte, v uint32) {
	dst[0] = byte(v >> 16)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v)
}

func readUint24(src []byte) uint32 {
	return uint32(src[0])<<16 | uint32(src[1])<<8 | uint32(src[2])
}

// PutUint32 写入大端 uint32
func PutUint32(dst []byte, v uint32) { binary.BigEndian.PutUint32(dst, v) }

// Uint32 读取大端 uint32
func Uint32(src []byte) uint32 { return binary.BigEndian.Uint32(src) }
