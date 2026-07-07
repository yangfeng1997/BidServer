package routeragent

import (
	"encoding/binary"
	"fmt"
)

// RA 传输帧类型
type FrameType uint8

const (
	FrameHandshake     FrameType = 0x01
	FrameHandshakeAck  FrameType = 0x02
	FrameRpcRequest    FrameType = 0x03
	FrameRpcResponse   FrameType = 0x04
	FrameRpcNotify     FrameType = 0x05
	FrameHeartbeat     FrameType = 0x06
	FrameBroadcastSent FrameType = 0x07
)

// RA 传输帧
type Frame struct {
	Type   FrameType
	Header []byte
	Body   []byte
}

// 编码帧为字节切片
func EncodeFrame(f Frame) ([]byte, error) {
	headLen := len(f.Header)
	bodyLen := len(f.Body)
	if headLen > 0xFFFF {
		return nil, fmt.Errorf("frame header too large: %d", headLen)
	}
	length := 1 + 2 + headLen + bodyLen
	out := make([]byte, 4+length)
	binary.BigEndian.PutUint32(out[:4], uint32(length))
	out[4] = byte(f.Type)
	binary.BigEndian.PutUint16(out[5:7], uint16(headLen))
	pos := 7
	copy(out[pos:pos+headLen], f.Header)
	pos += headLen
	copy(out[pos:pos+bodyLen], f.Body)
	return out, nil
}

// 编码 RPC 帧
func EncodeRPCFrame(typ FrameType, header, body []byte) ([]byte, error) {
	return EncodeFrame(Frame{Type: typ, Header: header, Body: body})
}

// 解码字节切片为帧
func DecodeFrame(data []byte) (Frame, error) {
	if len(data) < 7 {
		return Frame{}, fmt.Errorf("frame too short")
	}
	length := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) != 4+length {
		return Frame{}, fmt.Errorf("frame length mismatch")
	}
	headLen := int(binary.BigEndian.Uint16(data[5:7]))
	bodyLen := length - 3 - headLen
	if bodyLen < 0 {
		return Frame{}, fmt.Errorf("frame body length invalid")
	}
	pos := 7
	return Frame{
		Type:   FrameType(data[4]),
		Header: append([]byte(nil), data[pos:pos+headLen]...),
		Body:   append([]byte(nil), data[pos+headLen:pos+headLen+bodyLen]...),
	}, nil
}

// 编码 nodeID+payload
func EncodeRouteBody(nodeID uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out[:4], nodeID)
	copy(out[4:], payload)
	return out
}

// 解码为 nodeID 和 payload
func DecodeRouteBody(body []byte) (uint32, []byte, error) {
	if len(body) < 4 {
		return 0, nil, fmt.Errorf("route body too short")
	}
	return binary.BigEndian.Uint32(body[:4]), append([]byte(nil), body[4:]...), nil
}
