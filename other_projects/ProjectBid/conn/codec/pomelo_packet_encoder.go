package codec

import "projectbid/server/conn/packet"

// PomeloPacketEncoder 实现 Pomelo 协议编码。
type PomeloPacketEncoder struct{}

// NewPomeloPacketEncoder 创建新的编码器。
func NewPomeloPacketEncoder() *PomeloPacketEncoder {
	return &PomeloPacketEncoder{}
}

// Encode 将类型和数据编码为 Pomelo 二进制帧。
//
// 协议格式：
//
//	-------|----------|--------
//	1字节type | 3字节长度（大端）| data
func (e *PomeloPacketEncoder) Encode(typ packet.Type, data []byte) ([]byte, error) {
	if typ < packet.Handshake || typ > packet.Kick {
		return nil, packet.ErrWrongPomeloPacketType
	}

	if len(data) > MaxPacketSize {
		return nil, ErrPacketSizeExcced
	}

	buf := make([]byte, len(data)+HeadLength)
	buf[0] = byte(typ)
	copy(buf[1:HeadLength], IntToBytes(len(data)))
	copy(buf[HeadLength:], data)

	return buf, nil
}
