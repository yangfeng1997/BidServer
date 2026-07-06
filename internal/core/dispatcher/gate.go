package dispatcher

import (
	"project/internal/core/codec"
	"project/internal/core/conn"
	"project/internal/core/errcode"
	"project/internal/core/session"
)

// gate 入口流量处理
type GateDispatcher struct {
	*Dispatcher
	sessions         *session.SessionManager
	handshakeHandler func(conn.Connection, []byte) bool
}

// 创建 gate 分发器
func NewGateDispatcher(selfServerType uint32, sessions *session.SessionManager) *GateDispatcher {
	return &GateDispatcher{
		Dispatcher: New(selfServerType),
		sessions:   sessions,
	}
}

// 设置握手回调
func (g *GateDispatcher) SetHandshakeHandler(fn func(conn.Connection, []byte) bool) {
	g.handshakeHandler = fn
}

// 处理来自连接的 packet
func (g *GateDispatcher) HandlePacket(c conn.Connection, pkt *codec.Packet) error {
	if pkt == nil {
		return errcode.New(errcode.ERR_UNMARSHAL, "nil packet")
	}
	if pkt.Type == codec.PacketData {
		sess := g.sessions.GetByConnID(c.RemoteAddr())
		return g.HandleSessionPacket(sess, c, *pkt)
	}
	return g.HandleSessionPacket(nil, c, *pkt)
}

// HandleSessionPacket 处理已解析 session 的 packet，避免热路径重复计算连接地址。
func (g *GateDispatcher) HandleSessionPacket(sess *session.Session, c conn.Connection, pkt codec.Packet) error {
	switch pkt.Type {
	case codec.PacketHandshake:
		if g.handshakeHandler != nil {
			g.handshakeHandler(c, pkt.Body)
		}
		return nil
	case codec.PacketHandshakeAck:
		// 客户端不应发 HandshakeAck，忽略
		return nil
	case codec.PacketHeartbeat:
		c.TouchRecv()
		return nil
	case codec.PacketData:
		msg, err := codec.DecodeMessage(pkt.Body)
		if err != nil {
			return errcode.New(errcode.ERR_UNMARSHAL, err.Error())
		}
		if sess == nil {
			return errcode.New(errcode.ERR_UNAUTHED, "session not found")
		}
		return g.Dispatcher.Dispatch(sess, &msg)
	default:
		return nil
	}
}
