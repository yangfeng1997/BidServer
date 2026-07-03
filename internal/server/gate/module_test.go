package gate

import (
	"testing"

	"project/internal/core/codec"
	corerpc "project/internal/core/rpc"
	"project/internal/core/session"
	remotepb "project/protocol/remote"
)

type testConn struct {
	addr string
	sent [][]byte
}

func (c *testConn) Send(data []byte) {
	c.sent = append(c.sent, append([]byte(nil), data...))
}
func (c *testConn) Close() error               { return nil }
func (c *testConn) RemoteAddr() string         { return c.addr }
func (c *testConn) Done() <-chan struct{}      { return nil }
func (c *testConn) LastRecvUnixNano() int64    { return 0 }
func (c *testConn) TouchRecv()                 {}
func (c *testConn) Recv() <-chan *codec.Packet { return nil }

func TestGateRemoteBindSetBoundAndSendToClient(t *testing.T) {
	m := NewModule()
	m.sessions = session.NewSessionManager()
	c := &testConn{addr: "client:1"}
	sess := m.sessions.OnConnect(c)

	m.BindSession(corerpc.Background(), &remotepb.RPC_BindSession_Ntf{
		SessionId:  sess.ConnID,
		Uid:        1001,
		BoundNodes: map[uint32]uint32{2: 2001},
	})
	m.SetBound(corerpc.Background(), &remotepb.RPC_SetBound_Ntf{Uid: 1001, ServerType: 3, NodeId: 3001})

	bound := m.sessions.GetByUID(1001)
	if bound == nil || !bound.Authed {
		t.Fatal("session should be bound and authed")
	}
	if got := bound.BoundNodes[2]; got != 2001 {
		t.Fatalf("BoundNodes[2]=%d, want 2001", got)
	}
	if got := bound.BoundNodes[3]; got != 3001 {
		t.Fatalf("BoundNodes[3]=%d, want 3001", got)
	}

	m.SendToClient(corerpc.Background(), &remotepb.RPC_SendToClient_Ntf{Uid: 1001, CmdId: 9001, Payload: []byte("payload")})
	if len(c.sent) != 1 {
		t.Fatalf("sent packets=%d, want 1", len(c.sent))
	}
	pkt, err := codec.DecodePacket(c.sent[0])
	if err != nil {
		t.Fatalf("decode packet: %v", err)
	}
	msg, err := codec.DecodeMessage(pkt.Body)
	if err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if msg.Type != codec.MessageNotify || msg.CmdID != 9001 || string(msg.Body) != "payload" {
		t.Fatalf("unexpected downstream message: %+v", msg)
	}
}
