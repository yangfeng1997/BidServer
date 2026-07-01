package gatesvr

import (
	"fmt"
	"time"

	"project/internal/core/codec"
	"project/internal/core/dispatcher"
	"project/internal/core/errcode"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
)

// 将客户端消息转发到后端业务服
func (m *Module) forwardToBackend(sess *session.Session, msg *codec.Message, entry dispatcher.RouteEntry) error {
	if sess == nil || sess.Conn == nil {
		return fmt.Errorf("session not found")
	}
	core := corerpc.Default()
	if core == nil {
		return fmt.Errorf("rpc core not initialized")
	}
	target := corerpc.Target{ServerType: entry.ServerType}
	if nodeID := sess.BoundNodes[entry.ServerType]; nodeID != 0 {
		target = target.At(nodeID)
	} else {
		target = target.ByHash(sess.ConnID)
	}

	ctx := corerpc.Background().WithFromNode(0).WithClientMeta(struct {
		UID       int64
		SessionID string
		ConnID    string
	}{UID: sess.UID, SessionID: sess.ID, ConnID: sess.ConnID})

	if msg.Type == codec.MessageNotify || entry.RspCmdID == 0 {
		core.Send(target, entry.Route, msg.Body, ctx)
		return nil
	}

	pendingID := m.pending.Alloc(sess.Conn, msg.SeqID, entry.RspCmdID, 3*time.Second, func(id uint64) {
		m.poster.Post(func() { m.onPendingTimeout(id) })
	})
	core.Call(target, entry.Route, msg.Body, ctx, func(payload []byte, code errcode.ErrCode) {
		m.poster.Post(func() {
			m.onPendingReply(pendingID, payload, code)
		})
	})
	return nil
}

func (m *Module) onPendingTimeout(id uint64) {
	entry := m.pending.Pop(id)
	if entry == nil || entry.conn == nil {
		return
	}
	msg, _ := codec.EncodeMessage(codec.Message{
		Type:    codec.MessageResponse,
		SeqID:   entry.seqID,
		CmdID:   entry.rspCmdID,
		ErrCode: errcode.ERR_TIMEOUT,
	})
	pkt, _ := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: msg})
	entry.conn.Send(pkt)
}

func (m *Module) onPendingReply(id uint64, payload []byte, code errcode.ErrCode) {
	entry := m.pending.Pop(id)
	if entry == nil || entry.conn == nil {
		return
	}
	msg, _ := codec.EncodeMessage(codec.Message{
		Type:    codec.MessageResponse,
		SeqID:   entry.seqID,
		CmdID:   entry.rspCmdID,
		ErrCode: code,
		Body:    payload,
	})
	pkt, _ := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: msg})
	entry.conn.Send(pkt)
}

// 绑定 UID 和亲和节点
func (m *Module) BindSession(connID string, uid int64, bound map[uint32]uint32) *session.Session {
	return m.sessions.BindSession(connID, uid, bound)
}

// 向指定 uid 客户端发送消息
func (m *Module) SendToClient(uid int64, cmdID uint32, body []byte) error {
	sess := m.sessions.GetByUID(uid)
	if sess == nil || sess.Conn == nil {
		return fmt.Errorf("session not found for uid %d", uid)
	}
	msg, _ := codec.EncodeMessage(codec.Message{
		Type:  codec.MessageNotify,
		CmdID: cmdID,
		Body:  body,
	})
	pkt, _ := codec.EncodePacket(codec.Packet{Type: codec.PacketData, Body: msg})
	sess.Conn.Send(pkt)
	return nil
}
