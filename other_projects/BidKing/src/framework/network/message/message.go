package message

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Type 内层 Message 类型
type Type byte

const (
	Request  Type = 0x01 // 客→服，有 MID，期待回包
	Response Type = 0x02 // 服→客，有 MID，对应 Request 的回包
	OneWay   Type = 0x03 // 双向，无 MID，不需要回包
)

var ErrInvalidMessage = errors.New("invalid message")

// Message 内层帧
// MsgID 是 proto option 定义的数字消息 ID，替代字符串 route 在网络上传输
type Message struct {
	Type  Type
	MID   uint16 // 仅 Request/Response 有效，客户端生成用于回包匹配
	MsgID uint32 // 消息类型 ID，对应 proto option msg_id
	Code  int32  // 仅 Response 有效，框架错误码（0=成功，负数=框架错误）
	Data  []byte
}

// 帧格式：
// Request:  | type(1) | MID(2) | MsgID(4) | data |
// Response: | type(1) | MID(2) | MsgID(4) | code(4) | data |
// OneWay:   | type(1) | MsgID(4) | data |

// Encode 编码 Message
func Encode(m *Message) ([]byte, error) {
	switch m.Type {
	case Request:
		buf := make([]byte, 1+2+4+len(m.Data))
		buf[0] = byte(m.Type)
		binary.BigEndian.PutUint16(buf[1:3], m.MID)
		binary.BigEndian.PutUint32(buf[3:7], m.MsgID)
		copy(buf[7:], m.Data)
		return buf, nil
	case Response:
		buf := make([]byte, 1+2+4+4+len(m.Data))
		buf[0] = byte(m.Type)
		binary.BigEndian.PutUint16(buf[1:3], m.MID)
		binary.BigEndian.PutUint32(buf[3:7], m.MsgID)
		binary.BigEndian.PutUint32(buf[7:11], uint32(m.Code))
		copy(buf[11:], m.Data)
		return buf, nil
	case OneWay:
		buf := make([]byte, 1+4+len(m.Data))
		buf[0] = byte(m.Type)
		binary.BigEndian.PutUint32(buf[1:5], m.MsgID)
		copy(buf[5:], m.Data)
		return buf, nil
	default:
		return nil, fmt.Errorf("%w: unknown type 0x%02x", ErrInvalidMessage, m.Type)
	}
}

// Decode 从字节解码 Message
func Decode(data []byte) (*Message, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("%w: empty", ErrInvalidMessage)
	}
	t := Type(data[0])
	m := &Message{Type: t}

	switch t {
	case Request:
		// | type(1) | MID(2) | MsgID(4) | data |
		if len(data) < 7 {
			return nil, fmt.Errorf("%w: truncated Request", ErrInvalidMessage)
		}
		m.MID = binary.BigEndian.Uint16(data[1:3])
		m.MsgID = binary.BigEndian.Uint32(data[3:7])
		m.Data = data[7:]
	case Response:
		// | type(1) | MID(2) | MsgID(4) | code(4) | data |
		if len(data) < 11 {
			return nil, fmt.Errorf("%w: truncated Response", ErrInvalidMessage)
		}
		m.MID = binary.BigEndian.Uint16(data[1:3])
		m.MsgID = binary.BigEndian.Uint32(data[3:7])
		m.Code = int32(binary.BigEndian.Uint32(data[7:11]))
		m.Data = data[11:]
	case OneWay:
		// | type(1) | MsgID(4) | data |
		if len(data) < 5 {
			return nil, fmt.Errorf("%w: truncated OneWay", ErrInvalidMessage)
		}
		m.MsgID = binary.BigEndian.Uint32(data[1:5])
		m.Data = data[5:]
	default:
		return nil, fmt.Errorf("%w: unknown type 0x%02x", ErrInvalidMessage, t)
	}
	return m, nil
}

// NewRequest 构造 Request
func NewRequest(mid uint16, msgID uint32, data []byte) *Message {
	return &Message{Type: Request, MID: mid, MsgID: msgID, Data: data}
}

// NewResponse 构造成功 Response（Code=0）
func NewResponse(mid uint16, msgID uint32, data []byte) *Message {
	return &Message{Type: Response, MID: mid, MsgID: msgID, Data: data}
}

// NewErrorResponse 构造错误 Response，携带框架错误码（负数），无 data
func NewErrorResponse(mid uint16, msgID uint32, code int32) *Message {
	return &Message{Type: Response, MID: mid, MsgID: msgID, Code: code}
}

// NewOneWay 构造 OneWay
func NewOneWay(msgID uint32, data []byte) *Message {
	return &Message{Type: OneWay, MsgID: msgID, Data: data}
}
