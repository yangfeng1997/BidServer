package codec

import (
	"bytes"

	"projectbid/server/conn/packet"
)

// PomeloPacketDecoder 实现 Pomelo 协议的流式解码（处理 TCP 拆包粘包）。
type PomeloPacketDecoder struct{}

// NewPomeloPacketDecoder 创建新的解码器。
func NewPomeloPacketDecoder() *PomeloPacketDecoder {
	return &PomeloPacketDecoder{}
}

// Decode 从 TCP 流中解码一个或多个数据包。
// 数据不足时返回 (nil, nil) 而非错误，调用方需缓存数据继续读取。
func (c *PomeloPacketDecoder) Decode(data []byte) ([]*packet.Packet, error) {
	buf := bytes.NewBuffer(nil)
	buf.Write(data)

	if buf.Len() < HeadLength {
		return nil, nil
	}

	var packets []*packet.Packet

	size, typ, err := c.forward(buf)
	if err != nil {
		return nil, err
	}

	for size <= buf.Len() {
		p := &packet.Packet{Type: typ, Length: size, Data: buf.Next(size)}
		packets = append(packets, p)

		if buf.Len() < HeadLength {
			break
		}

		size, typ, err = c.forward(buf)
		if err != nil {
			return nil, err
		}
	}

	return packets, nil
}

func (c *PomeloPacketDecoder) forward(buf *bytes.Buffer) (int, packet.Type, error) {
	header := buf.Next(HeadLength)
	return ParseHeader(header)
}
