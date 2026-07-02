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
	switch m.Type {
	case MessageRequest:
		out := make([]byte, 1+2+4+len(m.Body))
		out[0] = byte(m.Type)
		binary.BigEndian.PutUint16(out[1:3], m.SeqID)
		binary.BigEndian.PutUint32(out[3:7], m.CmdID)
		copy(out[7:], m.Body)
		return out, nil
	case MessageResponse:
		out := make([]byte, 1+2+4+4+len(m.Body))
		out[0] = byte(m.Type)
		binary.BigEndian.PutUint16(out[1:3], m.SeqID)
		binary.BigEndian.PutUint32(out[3:7], m.CmdID)
		binary.BigEndian.PutUint32(out[7:11], uint32(m.ErrCode))
		copy(out[11:], m.Body)
		return out, nil
	case MessageNotify:
		out := make([]byte, 1+4+len(m.Body))
		out[0] = byte(m.Type)
		binary.BigEndian.PutUint32(out[1:5], m.CmdID)
		copy(out[5:], m.Body)
		return out, nil
	default:
		return nil, fmt.Errorf("message: unknown type %d", m.Type)
	}
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
			Body:  append([]byte(nil), data[7:]...),
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
			Body:    append([]byte(nil), data[11:]...),
		}, nil
	case MessageNotify:
		if len(data) < 5 {
			return Message{}, fmt.Errorf("notify message too short")
		}
		return Message{
			Type:  msgType,
			CmdID: binary.BigEndian.Uint32(data[1:5]),
			Body:  append([]byte(nil), data[5:]...),
		}, nil
	default:
		return Message{}, fmt.Errorf("message: unknown type %d", msgType)
	}
}
