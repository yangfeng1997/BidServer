package routeragent

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// RA 透传的 RPC 头部
type RPCWireHeader struct {
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

// 编码头部为字节切片
func EncodeRPCWireHeader(h RPCWireHeader) []byte {
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

// 解码字节切片为头部
func DecodeRPCWireHeader(data []byte) (RPCWireHeader, error) {
	if len(data) < 8+4+1+8+8+4+4+2+2 {
		return RPCWireHeader{}, fmt.Errorf("rpc header too short")
	}
	pos := 0
	h := RPCWireHeader{}
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
		return RPCWireHeader{}, fmt.Errorf("rpc header key length mismatch")
	}
	h.RoutingKey = string(bytes.Clone(data[pos : pos+keyLen]))
	pos += keyLen
	routeLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+routeLen {
		return RPCWireHeader{}, fmt.Errorf("rpc header route length mismatch")
	}
	h.Route = string(bytes.Clone(data[pos : pos+routeLen]))
	return h, nil
}
