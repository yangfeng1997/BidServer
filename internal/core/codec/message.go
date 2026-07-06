package codec

import (
	"encoding/binary"
	"fmt"

	"project/internal/core/errcode"
)

// 内层消息类型
type MessageType uint8

const (
	MessageRequest  MessageType = 0x01
	MessageResponse MessageType = 0x02
	MessageNotify   MessageType = 0x03
)

// 内层消息
type Message struct {
	Type    MessageType
	SeqID   uint16
	CmdID   uint32
	ErrCode errcode.ErrCode
	Body    []byte
}

// 编码为字节切片
func EncodeMessage(m Message) ([]byte, error) {
	return AppendMessage(nil, m)
}

// AppendMessage 将消息编码追加到 dst。
func AppendMessage(dst []byte, m Message) ([]byte, error) {
	switch m.Type {
	case MessageRequest:
		pos := len(dst)
		dst = grow(dst, 1+2+4+len(m.Body))
		dst[pos] = byte(m.Type)
		binary.BigEndian.PutUint16(dst[pos+1:pos+3], m.SeqID)
		binary.BigEndian.PutUint32(dst[pos+3:pos+7], m.CmdID)
		copy(dst[pos+7:], m.Body)
		return dst, nil
	case MessageResponse:
		pos := len(dst)
		dst = grow(dst, 1+2+4+4+len(m.Body))
		dst[pos] = byte(m.Type)
		binary.BigEndian.PutUint16(dst[pos+1:pos+3], m.SeqID)
		binary.BigEndian.PutUint32(dst[pos+3:pos+7], m.CmdID)
		binary.BigEndian.PutUint32(dst[pos+7:pos+11], uint32(m.ErrCode))
		copy(dst[pos+11:], m.Body)
		return dst, nil
	case MessageNotify:
		pos := len(dst)
		dst = grow(dst, 1+4+len(m.Body))
		dst[pos] = byte(m.Type)
		binary.BigEndian.PutUint32(dst[pos+1:pos+5], m.CmdID)
		copy(dst[pos+5:], m.Body)
		return dst, nil
	default:
		return nil, fmt.Errorf("message: unknown type %d", m.Type)
	}
}

func EncodeDataMessagePacket(m Message) ([]byte, error) {
	msgLen, err := encodedMessageLen(m)
	if err != nil {
		return nil, err
	}
	if msgLen > 0xFFFFFF {
		return nil, fmt.Errorf("packet too large: %d", msgLen)
	}
	out := make([]byte, 4, 4+msgLen)
	out[0] = byte(PacketData)
	putUint24(out[1:4], uint32(msgLen))
	return AppendMessage(out, m)
}

func encodedMessageLen(m Message) (int, error) {
	switch m.Type {
	case MessageRequest:
		return 1 + 2 + 4 + len(m.Body), nil
	case MessageResponse:
		return 1 + 2 + 4 + 4 + len(m.Body), nil
	case MessageNotify:
		return 1 + 4 + len(m.Body), nil
	default:
		return 0, fmt.Errorf("message: unknown type %d", m.Type)
	}
}

func grow(dst []byte, n int) []byte {
	pos := len(dst)
	need := pos + n
	if cap(dst) < need {
		newCap := cap(dst) * 2
		if newCap < need {
			newCap = need
		}
		grown := make([]byte, need, newCap)
		copy(grown, dst)
		return grown
	}
	return dst[:need]
}

// 解码为消息
func DecodeMessage(data []byte) (Message, error) {
	if len(data) < 1 {
		return Message{}, fmt.Errorf("message too short")
	}
	msgType := MessageType(data[0])
	switch msgType {
	case MessageRequest:
		if len(data) < 7 {
			return Message{}, fmt.Errorf("request message too short")
		}
		return Message{
			Type:  msgType,
			SeqID: binary.BigEndian.Uint16(data[1:3]),
			CmdID: binary.BigEndian.Uint32(data[3:7]),
			Body:  data[7:],
		}, nil
	case MessageResponse:
		if len(data) < 11 {
			return Message{}, fmt.Errorf("response message too short")
		}
		return Message{
			Type:    msgType,
			SeqID:   binary.BigEndian.Uint16(data[1:3]),
			CmdID:   binary.BigEndian.Uint32(data[3:7]),
			ErrCode: errcode.ErrCode(binary.BigEndian.Uint32(data[7:11])),
			Body:    data[11:],
		}, nil
	case MessageNotify:
		if len(data) < 5 {
			return Message{}, fmt.Errorf("notify message too short")
		}
		return Message{
			Type:  msgType,
			CmdID: binary.BigEndian.Uint32(data[1:5]),
			Body:  data[5:],
		}, nil
	default:
		return Message{}, fmt.Errorf("message: unknown type %d", msgType)
	}
}
