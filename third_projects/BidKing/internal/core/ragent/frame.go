package ragent

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"project/internal/core/rpc"
)

// FrameType 表示 RA 传输帧类型
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

// Frame 表示 RA 传输帧
type Frame struct {
	Type   FrameType
	Header []byte
	Body   []byte
}

type rpcWireHeader struct {
	SeqID       uint64
	ServerType  uint32
	RoutingMode uint8
	DeadlineMs  int64
	WaiterID    uint64
	FromNodeID  uint32
	ErrCode     uint32
	RoutingKey  string
	Route       string
}

func encodeRAFrame(frameType FrameType, header, body []byte) ([]byte, error) {
	headLen := len(header)
	bodyLen := len(body)
	if headLen > 0xFFFF {
		return nil, fmt.Errorf("ragent frame header too large: %d", headLen)
	}
	length := 1 + 2 + headLen + bodyLen
	out := make([]byte, 4+length)
	binary.BigEndian.PutUint32(out[:4], uint32(length))
	out[4] = byte(frameType)
	binary.BigEndian.PutUint16(out[5:7], uint16(headLen))
	pos := 7
	copy(out[pos:pos+headLen], header)
	pos += headLen
	copy(out[pos:pos+bodyLen], body)
	return out, nil
}

func decodeRAFrame(data []byte) (Frame, error) {
	if len(data) < 7 {
		return Frame{}, fmt.Errorf("ragent frame too short")
	}
	length := int(binary.BigEndian.Uint32(data[:4]))
	if len(data) != 4+length {
		return Frame{}, fmt.Errorf("ragent frame length mismatch")
	}
	headLen := int(binary.BigEndian.Uint16(data[5:7]))
	if headLen < 0 || headLen > length-3 {
		return Frame{}, fmt.Errorf("ragent frame header length invalid")
	}
	bodyLen := length - 3 - headLen
	if bodyLen < 0 {
		return Frame{}, fmt.Errorf("ragent frame body length invalid")
	}
	pos := 7
	return Frame{
		Type:   FrameType(data[4]),
		Header: append([]byte(nil), data[pos:pos+headLen]...),
		Body:   append([]byte(nil), data[pos+headLen:pos+headLen+bodyLen]...),
	}, nil
}

func encodeRPCWireHeader(h rpcWireHeader) []byte {
	keyLen := len(h.RoutingKey)
	routeLen := len(h.Route)
	out := make([]byte, 8+4+1+8+8+4+4+2+keyLen+2+routeLen)
	pos := 0
	binary.BigEndian.PutUint64(out[pos:pos+8], h.SeqID)
	pos += 8
	binary.BigEndian.PutUint32(out[pos:pos+4], h.ServerType)
	pos += 4
	out[pos] = h.RoutingMode
	pos++
	binary.BigEndian.PutUint64(out[pos:pos+8], uint64(h.DeadlineMs))
	pos += 8
	binary.BigEndian.PutUint64(out[pos:pos+8], h.WaiterID)
	pos += 8
	binary.BigEndian.PutUint32(out[pos:pos+4], h.FromNodeID)
	pos += 4
	binary.BigEndian.PutUint32(out[pos:pos+4], h.ErrCode)
	pos += 4
	binary.BigEndian.PutUint16(out[pos:pos+2], uint16(keyLen))
	pos += 2
	copy(out[pos:pos+keyLen], h.RoutingKey)
	pos += keyLen
	binary.BigEndian.PutUint16(out[pos:pos+2], uint16(routeLen))
	pos += 2
	copy(out[pos:pos+routeLen], h.Route)
	return out
}

func decodeRPCWireHeader(data []byte) (rpcWireHeader, error) {
	if len(data) < 8+4+1+8+8+4+4+2+2 {
		return rpcWireHeader{}, fmt.Errorf("rpc header too short")
	}
	pos := 0
	h := rpcWireHeader{}
	h.SeqID = binary.BigEndian.Uint64(data[pos : pos+8])
	pos += 8
	h.ServerType = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	h.RoutingMode = data[pos]
	pos++
	h.DeadlineMs = int64(binary.BigEndian.Uint64(data[pos : pos+8]))
	pos += 8
	h.WaiterID = binary.BigEndian.Uint64(data[pos : pos+8])
	pos += 8
	h.FromNodeID = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	h.ErrCode = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	keyLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+keyLen+2 {
		return rpcWireHeader{}, fmt.Errorf("rpc header key length mismatch")
	}
	h.RoutingKey = string(bytes.Clone(data[pos : pos+keyLen]))
	pos += keyLen
	routeLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+routeLen {
		return rpcWireHeader{}, fmt.Errorf("rpc header route length mismatch")
	}
	h.Route = string(bytes.Clone(data[pos : pos+routeLen]))
	return h, nil
}

func rpcHeaderFromTarget(target rpc.Target, header rpc.Header, fromNodeID uint32) rpcWireHeader {
	wire := rpcWireHeader{
		SeqID:       header.SeqID,
		ServerType:  header.ServerType,
		RoutingMode: uint8(target.Mode),
		DeadlineMs:  header.DeadlineMs,
		WaiterID:    header.WaiterID,
		FromNodeID:  fromNodeID,
		ErrCode:     0,
		RoutingKey:  header.RoutingKey,
		Route:       header.Route,
	}
	if wire.RoutingMode == uint8(rpc.RoutingDirect) && wire.RoutingKey == "" && target.NodeID != 0 {
		wire.RoutingKey = fmt.Sprintf("%d", target.NodeID)
	}
	return wire
}
