package message

import (
	"encoding/binary"

	"projectbid/server/util/compression"
)

// Encoder 消息编码器接口。
type Encoder interface {
	IsCompressionEnabled() bool
	Encode(message *Message) ([]byte, error)
}

// MessagesEncoder 实现 Encoder，支持 zlib 数据压缩和路由字典压缩。
type MessagesEncoder struct {
	DataCompression bool
}

// NewMessagesEncoder 创建消息编码器。
func NewMessagesEncoder(dataCompression bool) *MessagesEncoder {
	return &MessagesEncoder{DataCompression: dataCompression}
}

// IsCompressionEnabled 返回是否启用数据压缩。
func (me *MessagesEncoder) IsCompressionEnabled() bool {
	return me.DataCompression
}

// Encode 将消息编码为二进制格式。
//
// 协议格式（参见 Pitaya 通信协议文档）：
//
//	flag(1字节) [ID(变长)] [路由(变长)] [data]
//
// flag 位布局：
//
//	bit 0: 路由压缩标记
//	bit 1-3: 消息类型
//	bit 4: gzip/zlib 压缩标记
//	bit 5: 错误标记
func (me *MessagesEncoder) Encode(message *Message) ([]byte, error) {
	if invalidType(message.Type) {
		return nil, ErrWrongMessageType
	}

	buf := make([]byte, 0)
	flag := byte(message.Type) << 1

	routesCodesMutex.RLock()
	code, compressed := routes[message.Route]
	routesCodesMutex.RUnlock()
	if compressed {
		flag |= msgRouteCompressMask
	}

	if message.Err {
		flag |= errorMask
	}

	buf = append(buf, flag)

	// 变长编码消息 ID（仅 Request/Response）
	if message.Type == Request || message.Type == Response {
		n := message.ID
		for {
			b := byte(n % 128)
			n >>= 7
			if n != 0 {
				buf = append(buf, b+128)
			} else {
				buf = append(buf, b)
				break
			}
		}
	}

	// 路由
	if routable(message.Type) {
		if compressed {
			buf = append(buf, byte((code>>8)&0xFF))
			buf = append(buf, byte(code&0xFF))
		} else {
			buf = append(buf, byte(len(message.Route)))
			buf = append(buf, []byte(message.Route)...)
		}
	}

	// 数据压缩
	if me.DataCompression {
		d, err := compression.DeflateData(message.Data)
		if err != nil {
			return nil, err
		}
		if len(d) < len(message.Data) {
			message.Data = d
			buf[0] |= gzipMask
		}
	}

	buf = append(buf, message.Data...)
	return buf, nil
}

// Decode 将消息编码器解码能力委托给包级 Decode 函数。
func (me *MessagesEncoder) Decode(data []byte) (*Message, error) {
	return Decode(data)
}

// Decode 将二进制数据解码为 Message。
func Decode(data []byte) (*Message, error) {
	if len(data) < msgHeadLength {
		return nil, ErrInvalidMessage
	}

	m := New()
	flag := data[0]
	offset := 1
	m.Type = Type((flag >> 1) & msgTypeMask)

	if invalidType(m.Type) {
		return nil, ErrWrongMessageType
	}

	// 变长解码消息 ID
	if m.Type == Request || m.Type == Response {
		id := uint(0)
		for i := offset; i < len(data); i++ {
			b := data[i]
			id += uint(b&0x7F) << uint(7*(i-offset))
			if b < 128 {
				offset = i + 1
				break
			}
		}
		m.ID = id
	}

	m.Err = flag&errorMask == errorMask

	// 路由
	size := len(data)
	if routable(m.Type) {
		if flag&msgRouteCompressMask == 1 {
			if offset > size || (offset+2) > size {
				return nil, ErrInvalidMessage
			}
			m.compressed = true
			code := binary.BigEndian.Uint16(data[offset : offset+2])
			routesCodesMutex.RLock()
			route, ok := codes[code]
			routesCodesMutex.RUnlock()
			if !ok {
				return nil, ErrRouteInfoNotFound
			}
			m.Route = route
			offset += 2
		} else {
			m.compressed = false
			rl := data[offset]
			offset++
			if offset > size || (offset+int(rl)) > size {
				return nil, ErrInvalidMessage
			}
			m.Route = string(data[offset : offset+int(rl)])
			offset += int(rl)
		}
	}

	if offset > size {
		return nil, ErrInvalidMessage
	}

	m.Data = data[offset:]
	if flag&gzipMask == gzipMask {
		var err error
		m.Data, err = compression.InflateData(m.Data)
		if err != nil {
			return nil, err
		}
	}

	return m, nil
}
