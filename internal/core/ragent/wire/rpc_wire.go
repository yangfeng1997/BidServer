package wire

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// RA 透传的 RPC 头部
type RPCWireHeader struct {
	SeqID       uint64
	ServerType  uint32
	RoutingMode uint8
	DeadlineMs  int64
	WaiterID    uint64
	SrcNodeID   uint32
	DestNodeID  uint32
	ErrCode     uint32
	RoutingKey  string
	Route       string
}

// 编码头部为字节切片
func EncodeRPCWireHeader(h RPCWireHeader) []byte {
	out, _ := AppendRPCWireHeader(nil, h)
	return out
}

func RPCWireHeaderLen(h RPCWireHeader) (int, error) {
	keyLen := len(h.RoutingKey)
	routeLen := len(h.Route)
	if keyLen > 0xFFFF {
		return 0, fmt.Errorf("rpc header routing key too large: %d", keyLen)
	}
	if routeLen > 0xFFFF {
		return 0, fmt.Errorf("rpc header route too large: %d", routeLen)
	}
	return 8 + 4 + 1 + 8 + 8 + 4 + 4 + 4 + 2 + keyLen + 2 + routeLen, nil
}

// AppendRPCWireHeader 将 RPC 头编码追加到 dst，wire format 与 EncodeRPCWireHeader 完全一致。
func AppendRPCWireHeader(dst []byte, h RPCWireHeader) ([]byte, error) {
	headerLen, err := RPCWireHeaderLen(h)
	if err != nil {
		return nil, err
	}
	keyLen := len(h.RoutingKey)
	routeLen := len(h.Route)
	pos := len(dst)
	need := pos + headerLen
	if cap(dst) < need {
		newCap := cap(dst) * 2
		if newCap < need {
			newCap = need
		}
		grown := make([]byte, need, newCap)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:need]
	}
	binary.BigEndian.PutUint64(dst[pos:pos+8], h.SeqID)
	pos += 8
	binary.BigEndian.PutUint32(dst[pos:pos+4], h.ServerType)
	pos += 4
	dst[pos] = h.RoutingMode
	pos++
	binary.BigEndian.PutUint64(dst[pos:pos+8], uint64(h.DeadlineMs))
	pos += 8
	binary.BigEndian.PutUint64(dst[pos:pos+8], h.WaiterID)
	pos += 8
	binary.BigEndian.PutUint32(dst[pos:pos+4], h.SrcNodeID)
	pos += 4
	binary.BigEndian.PutUint32(dst[pos:pos+4], h.DestNodeID)
	pos += 4
	binary.BigEndian.PutUint32(dst[pos:pos+4], h.ErrCode)
	pos += 4
	binary.BigEndian.PutUint16(dst[pos:pos+2], uint16(keyLen))
	pos += 2
	copy(dst[pos:pos+keyLen], h.RoutingKey)
	pos += keyLen
	binary.BigEndian.PutUint16(dst[pos:pos+2], uint16(routeLen))
	pos += 2
	copy(dst[pos:pos+routeLen], h.Route)
	return dst, nil
}

// 解码字节切片为头部
func DecodeRPCWireHeader(data []byte) (RPCWireHeader, error) {
	if len(data) < 8+4+1+8+8+4+4+4+2+2 {
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
	h.SrcNodeID = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	h.DestNodeID = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	h.ErrCode = binary.BigEndian.Uint32(data[pos : pos+4])
	pos += 4
	keyLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+keyLen+2 {
		return RPCWireHeader{}, fmt.Errorf("rpc header key length mismatch")
	}
	h.RoutingKey = bytesToString(data[pos : pos+keyLen])
	pos += keyLen
	routeLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if len(data) < pos+routeLen {
		return RPCWireHeader{}, fmt.Errorf("rpc header route length mismatch")
	}
	h.Route = bytesToString(data[pos : pos+routeLen])
	return h, nil
}

func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}
