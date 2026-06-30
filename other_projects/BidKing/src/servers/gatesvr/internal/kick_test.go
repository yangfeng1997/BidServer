package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	"project/src/framework/agent"
	"project/src/framework/cluster"
	"project/src/framework/session"
)

// kickFakeCluster 捕获 Cast（断连通知）。命名带 kick 前缀避免与
// gate_handler_test.go 既有的 fakeCluster 冲突（同 package internal）。
type kickFakeCluster struct {
	cluster.Cluster
	castRoute  string
	castTarget cluster.NodeID
	castUID    int64
}

func (f *kickFakeCluster) Cast(_ context.Context, target cluster.NodeID, route string, msg proto.Message) error {
	f.castTarget, f.castRoute = target, route
	if n, ok := msg.(*lobbypb.RPC_PlayerDisconnect_Notify); ok {
		f.castUID = n.Uid
	}
	return nil
}

func TestNotifyPlayerOffline_CastsToBoundLobby(t *testing.T) {
	fc := &kickFakeCluster{}
	sm := session.NewManager()
	m := NewGateModule("1.1.1", sm, fc, nil)
	m.Init() // 构造 g.ctx（注入 cluster）

	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, 10001)
	s.BindNode("lobbysvr", "1.2.7")

	m.notifyPlayerOffline(s)

	if fc.castRoute != "LobbyHandler.playerdisconnect" || fc.castUID != 10001 {
		t.Fatalf("offline cast wrong: route=%s uid=%d", fc.castRoute, fc.castUID)
	}
	want, _ := cluster.ParseNodeID("1.2.7")
	if fc.castTarget != want {
		t.Fatalf("offline cast target=%v want %v", fc.castTarget, want)
	}
}

func TestKickSession_ClosesSessionByUID(t *testing.T) {
	sm := session.NewManager()
	m := NewGateModule("1.1.1", sm, &kickFakeCluster{}, nil) // agents=nil：无连接可推，仅验证 Close
	h := NewGateHandler(m)

	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, 10001)

	raw, _ := proto.Marshal(&onlinepb.RPC_KickSession_Notify{Uid: 10001, Reason: 1})
	h.KickSession(context.Background(), raw)

	if _, ok := sm.ByUID(10001); ok {
		t.Fatal("session should be closed after kick")
	}
}

func TestKickSession_BodyIsProtoEncoded(t *testing.T) {
	sm := session.NewManager()
	agents := agent.NewMap()
	m := NewGateModule("1.1.1", sm, &kickFakeCluster{}, agents)
	h := NewGateHandler(m)

	uid := int64(10001)
	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, uid)
	ag := &fakePushAgent{}
	agents.Store(s.ID(), ag)

	raw, _ := proto.Marshal(&onlinepb.RPC_KickSession_Notify{Uid: uid, Reason: 1})
	h.KickSession(context.Background(), raw)

	if ag.lastPushMsgID != msgIDSCKick {
		t.Fatalf("kick push msgID=%d want %d", ag.lastPushMsgID, msgIDSCKick)
	}
	var sc gatepb.SC_Kick
	if err := proto.Unmarshal(ag.lastPushBody, &sc); err != nil {
		t.Fatalf("kick body must be proto SC_Kick, got unmarshal err: %v", err)
	}
	if sc.Reason != 1 {
		t.Fatalf("decoded SC_Kick reason=%d want 1", sc.Reason)
	}
}
