package codec

import "projectbid/server/conn/packet"

// ParseHeader 解析 4 字节 Pomelo 协议头，返回数据长度和包类型。
func ParseHeader(header []byte) (int, packet.Type, error) {
	if len(header) != HeadLength {
		return 0, 0x00, packet.ErrInvalidPomeloHeader
	}

	typ := header[0]
	if typ < byte(packet.Handshake) || typ > byte(packet.Kick) {
		return 0, 0x00, packet.ErrWrongPomeloPacketType
	}

	size := BytesToInt(header[1:])

	if size > MaxPacketSize {
		return 0, 0x00, ErrPacketSizeExcced
	}

	return size, packet.Type(typ), nil
}

// BytesToInt 将 3 字节大端序转换为 int。
func BytesToInt(b []byte) int {
	result := 0
	for _, v := range b {
		result = result<<8 + int(v)
	}
	return result
}

// IntToBytes 将 int 转换为 3 字节大端序。
func IntToBytes(n int) []byte {
	buf := make([]byte, 3)
	buf[0] = byte((n >> 16) & 0xFF)
	buf[1] = byte((n >> 8) & 0xFF)
	buf[2] = byte(n & 0xFF)
	return buf
}
