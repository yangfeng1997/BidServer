package gatesvr

import (
	"encoding/binary"

	"project/internal/core/codec"
	"project/internal/core/conn"
	"project/internal/core/errcode"
	"project/internal/core/session"
)

// 客户端握手版本号
const HandshakeVersion uint16 = 1

// 默认心跳间隔（秒）
const DefaultHeartbeatInterval uint16 = 10

// 客户端发来的握手包体
type handshakeBody struct {
	Version  uint16
	Platform uint8
}

// 服务端返回的握手确认包体
type HandshakeAckBody struct {
	ErrCode           uint32
	HeartbeatInterval uint16
	ServerTime        int64
}

// encodeHandshakeAck 将握手确认编码为字节切片
func encodeHandshakeAck(body HandshakeAckBody) []byte {
	buf := make([]byte, 4+2+8)
	binary.BigEndian.PutUint32(buf[0:4], body.ErrCode)
	binary.BigEndian.PutUint16(buf[4:6], body.HeartbeatInterval)
	binary.BigEndian.PutUint64(buf[6:14], uint64(body.ServerTime))
	return buf
}

// decodeHandshake 解码客户端握手包体
func decodeHandshake(data []byte) (handshakeBody, error) {
	if len(data) < 3 {
		return handshakeBody{}, errcode.New(errcode.ERR_UNMARSHAL, "handshake body too short")
	}
	return handshakeBody{
		Version:  binary.BigEndian.Uint16(data[0:2]),
		Platform: data[2],
	}, nil
}

// handleHandshake 处理客户端握手帧
// 返回 true 表示握手成功
func (m *Module) handleHandshake(c conn.Connection, body []byte) bool {
	hs, err := decodeHandshake(body)
	if err != nil {
		m.sendHandshakeAck(c, errcode.ERR_UNMARSHAL, 0)
		return false
	}
	// 版本检查
	if hs.Version != HandshakeVersion {
		m.sendHandshakeAck(c, errcode.ERR_INTERNAL, 0)
		return false
	}
	// 创建 session
	sess := m.sessions.OnConnect(c)
	if sess == nil {
		m.sendHandshakeAck(c, errcode.ERR_INTERNAL, 0)
		return false
	}
	// 发送成功确认
	m.sendHandshakeAck(c, errcode.OK, DefaultHeartbeatInterval)
	_ = sess
	return true
}

func (m *Module) sendHandshakeAck(c conn.Connection, code errcode.ErrCode, interval uint16) {
	body := encodeHandshakeAck(HandshakeAckBody{
		ErrCode:           uint32(code),
		HeartbeatInterval: interval,
		ServerTime:        0,
	})
	pkt, err := codec.EncodePacket(codec.Packet{
		Type: codec.PacketHandshakeAck,
		Body: body,
	})
	if err != nil {
		return
	}
	c.Send(pkt)
	// 握手失败则关闭连接
	if code != errcode.OK {
		_ = c.Close()
	}
}

// ensureSession 确保连接有对应的 session
func (m *Module) ensureSession(c conn.Connection) *session.Session {
	sess := m.sessions.GetByConnID(c.RemoteAddr())
	if sess == nil {
		sess = m.sessions.OnConnect(c)
	}
	return sess
}
