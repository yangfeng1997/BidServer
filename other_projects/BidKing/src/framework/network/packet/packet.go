package packet

import (
	"errors"
	"fmt"
)

// Type 外层 Packet 类型
type Type byte

const (
	Handshake    Type = 0x01 // 客→服，握手请求
	HandshakeAck Type = 0x02 // 服→客，握手确认
	Heartbeat    Type = 0x03 // 双向，心跳
	Data         Type = 0x04 // 业务数据，body 为内层 Message
	Kick         Type = 0x05 // 服→客，踢人
)

// headerSize 外层 Packet 头：1字节type + 3字节length
const headerSize = 4

// MaxBodySize 单个 Packet body 最大字节数（4MB），防止恶意客户端用超大 length
// 字段触发巨量内存分配。与 WS Acceptor 的 SetReadLimit 默认值保持一致。
// 3字节 length 理论上限为 16MB，此处收紧到 4MB。
const MaxBodySize = 4 * 1024 * 1024

var (
	ErrInvalidPacket  = errors.New("invalid packet")
	ErrPacketTooLarge = errors.New("packet body exceeds max size")
)

// Packet 外层帧
type Packet struct {
	Type Type
	Body []byte
}

// Encode 编码为字节：| type(1) | length(3, big-endian) | body |
func Encode(t Type, body []byte) []byte {
	n := len(body)
	buf := make([]byte, headerSize+n)
	buf[0] = byte(t)
	buf[1] = byte(n >> 16)
	buf[2] = byte(n >> 8)
	buf[3] = byte(n)
	copy(buf[headerSize:], body)
	return buf
}

// DecodeHeader 从4字节头解析出 type 和 body length
func DecodeHeader(header []byte) (Type, int, error) {
	if len(header) < headerSize {
		return 0, 0, ErrInvalidPacket
	}
	t := Type(header[0])
	n := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
	if n > MaxBodySize {
		return t, n, ErrPacketTooLarge
	}
	return t, n, nil
}

// Decode 从完整字节解码，支持批量（一次 TCP 读可能含多个 Packet）
func Decode(data []byte) ([]*Packet, error) {
	var packets []*Packet
	for len(data) > 0 {
		if len(data) < headerSize {
			return nil, fmt.Errorf("%w: truncated header", ErrInvalidPacket)
		}
		t, n, err := DecodeHeader(data)
		if err != nil {
			return nil, err
		}
		if len(data) < headerSize+n {
			return nil, fmt.Errorf("%w: truncated body", ErrInvalidPacket)
		}
		body := make([]byte, n)
		copy(body, data[headerSize:headerSize+n])
		packets = append(packets, &Packet{Type: t, Body: body})
		data = data[headerSize+n:]
	}
	return packets, nil
}

// EncodeHeartbeat 编码心跳包（无 body）
func EncodeHeartbeat() []byte { return Encode(Heartbeat, nil) }

// EncodeKick 编码踢人包，reason 为可选原因
func EncodeKick(reason []byte) []byte { return Encode(Kick, reason) }
