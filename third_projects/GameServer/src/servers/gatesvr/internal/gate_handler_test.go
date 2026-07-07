package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	"project/src/framework/cluster"
	"project/src/framework/handler"
	"project/src/framework/session"
)

// fakeCluster 嵌入 noopCluster，仅覆写 CallAnySync 返回预设响应
type fakeCluster struct {
	cluster.Cluster
	rspData []byte
	err     error
}

func (f *fakeCluster) CallAnySync(_ context.Context, _ string, _ string, _ proto.Message) ([]byte, error) {
	return f.rspData, f.err
}

func TestGateHandler_Login_BindsLobby(t *testing.T) {
	lobbyRsp, _ := proto.Marshal(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: 10001, LobbyNodeId: "1.2.1"})
	fc := &fakeCluster{Cluster: cluster.NewNoopCluster(), rspData: lobbyRsp}

	mgr := session.NewManager()
	m := NewGateModule("1.1.1", mgr, fc, nil)
	h := NewGateHandler(m)

	s := mgr.New("127.0.0.1")
	ctx := handler.WithSessionID(context.Background(), s.ID())

	rsp, err := h.Login(ctx, &gatepb.CS_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 {
		t.Fatalf("unexpected rsp: %+v", rsp)
	}
	if !s.IsBound() || s.UID() != 10001 {
		t.Fatalf("session not bound: bound=%v uid=%d", s.IsBound(), s.UID())
	}
	if node, ok := s.BoundNode("lobbysvr"); !ok || node != "1.2.1" {
		t.Fatalf("lobby node not bound: ok=%v node=%q", ok, node)
	}
}

func TestGateHandler_Login_LobbyRejects(t *testing.T) {
	lobbyRsp, _ := proto.Marshal(&lobbypb.RPC_Login_Rsp{Code: -1})
	fc := &fakeCluster{Cluster: cluster.NewNoopCluster(), rspData: lobbyRsp}

	mgr := session.NewManager()
	h := NewGateHandler(NewGateModule("1.1.1", mgr, fc, nil))
	s := mgr.New("127.0.0.1")
	ctx := handler.WithSessionID(context.Background(), s.ID())

	rsp, _ := h.Login(ctx, &gatepb.CS_Login_Req{Token: "bad"})
	if rsp.Code == 0 {
		t.Fatalf("expected rejection, got code 0")
	}
	if s.IsBound() {
		t.Fatalf("session should not be bound on rejection")
	}
}

// touchRecCluster 捕获 Cast 的 route 与 msg（验证心跳转发 Touch）。
type touchRecCluster struct {
	cluster.Cluster
	lastRoute string
	lastMsg   proto.Message
}

func (f *touchRecCluster) Cast(_ context.Context, _ cluster.NodeID, route string, msg proto.Message) error {
	f.lastRoute = route
	f.lastMsg = msg
	return nil
}

func TestHeartbeat_ForwardsTouchToLobby(t *testing.T) {
	fc := &touchRecCluster{}
	sm := session.NewManager()
	m := NewGateModule("1.1.1", sm, fc, nil)
	m.Init() // 构造 g.ctx（注入 cluster）

	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, 10001)
	s.BindNode("lobbysvr", "0.2.1")

	h := NewGateHandler(m)
	ctx := handler.WithSessionID(context.Background(), s.ID())
	h.Heartbeat(ctx, &gatepb.CS_Heartbeat_OneWay{ClientTime: 123})

	if fc.lastRoute != "LobbyHandler.touch" {
		t.Fatalf("route=%q", fc.lastRoute)
	}
	tn, ok := fc.lastMsg.(*lobbypb.RPC_Touch_Notify)
	if !ok || tn.Uid != 10001 {
		t.Fatalf("msg=%+v", fc.lastMsg)
	}
}
