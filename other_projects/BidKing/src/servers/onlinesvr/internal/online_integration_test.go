//go:build integration

package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
)

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

// stubGate 记录收到的顶号通知（raw handler，自行 proto.Unmarshal，绕开序列化器）
type stubGate struct{ kickedUID atomic.Int64 }

func (g *stubGate) KickSession(_ context.Context, raw []byte) {
	var n onlinepb.RPC_KickSession_Notify
	if err := proto.Unmarshal(raw, &n); err == nil {
		g.kickedUID.Store(n.Uid)
	}
}

func startNode(t *testing.T, id, typ string, register func(app *application.Application)) (*application.Application, *transport.NatsCluster) {
	t.Helper()
	self, err := cluster.ParseNodeID(id)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints: etcdEndpoints, NatsURLs: natsURLs,
		SelfAddr: "127.0.0.1:0", ServerTypeName: typ,
	})
	if err != nil {
		t.Fatalf("cluster %s: %v", id, err)
	}
	app := application.NewBuilder().
		NodeID(id).NodeType(typ).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).Build()
	register(app)
	app.Start()
	if err := cls.Init(); err != nil {
		t.Fatalf("init %s: %v", id, err)
	}
	return app, cls
}

func waitForType(c *transport.NatsCluster, typ string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(c.Discovery().ByType(typ)) > 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestOnline_RegisterQueryKick_EndToEnd(t *testing.T) {
	// online 节点
	onlineApp, onlineCls := startNode(t, "1.5.1", "onlinesvr", func(app *application.Application) {
		mod := NewOnlineModule(500 * time.Millisecond) // 短 TTL 便于过期断言
		app.Register(mod)
		if err := app.RegisterHandler(NewOnlineHandler(mod.Directory(), app.Cluster()), nil); err != nil {
			t.Fatalf("register online handler: %v", err)
		}
	})
	defer onlineCls.Stop()
	_ = onlineApp

	// stub gateway（节点 1.1.201），注册 GateHandler.kicksession 观察顶号
	sg := &stubGate{}
	_, gateCls := startNode(t, "1.1.201", "gatesvr", func(app *application.Application) {
		// stubGate 复用 GateHandler 类型名以生成 route "GateHandler.kicksession"
		if err := app.RegisterHandler(&GateHandler{stub: sg}, nil); err != nil {
			t.Fatalf("register stub gate: %v", err)
		}
	})
	defer gateCls.Stop()

	// 模拟 lobby 的客户端节点（节点 1.2.250）
	_, lobbyCls := startNode(t, "1.2.250", "lobbysvr", func(app *application.Application) {})
	defer lobbyCls.Stop()

	if !waitForType(lobbyCls, "onlinesvr", 5*time.Second) {
		t.Fatal("onlinesvr not discovered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. 注册（gateway=1.1.201）
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.201", LobbyNodeId: "1.2.250"},
		&onlinepb.RPC_Register_Rsp{})

	// 2. Query 命中
	var q onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001}, &q)
	if !q.Online || q.Entry.GatewayNodeId != "1.1.201" {
		t.Fatalf("query: %+v", &q)
	}

	// 3. 同 uid 从另一 gateway 再注册 → 顶号下发到旧 gateway(1.1.201)
	var r2 onlinepb.RPC_Register_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.202", LobbyNodeId: "1.2.250"}, &r2)
	if !r2.KickedOld {
		t.Fatal("expected kicked_old on dup login")
	}
	// 等顶号 Cast 异步到达 stub gateway
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sg.kickedUID.Load() != 10001 {
		time.Sleep(50 * time.Millisecond)
	}
	if sg.kickedUID.Load() != 10001 {
		t.Fatal("stub gateway did not receive kick notify")
	}

	// 4. Unregister → Query 落空
	callOnline(t, ctx, lobbyCls, "OnlineHandler.unregister", &onlinepb.RPC_Unregister_Req{Uid: 10001}, &onlinepb.RPC_Unregister_Rsp{})
	var q2 onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001}, &q2)
	if q2.Online {
		t.Fatal("should be offline after unregister")
	}

	// 5. 过期：注册后等 > TTL，Query 落空
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 20002, GatewayNodeId: "1.1.201", LobbyNodeId: "1.2.250"}, &onlinepb.RPC_Register_Rsp{})
	time.Sleep(1500 * time.Millisecond) // > 500ms TTL + tick
	var q3 onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 20002}, &q3)
	if q3.Online {
		t.Fatal("entry should expire after TTL")
	}
}

func callOnline(t *testing.T, ctx context.Context, c *transport.NatsCluster, route string, req, rsp proto.Message) {
	t.Helper()
	data, err := c.CallAnySync(ctx, "onlinesvr", route, req)
	if err != nil {
		t.Fatalf("call %s: %v", route, err)
	}
	if err := proto.Unmarshal(data, rsp); err != nil {
		t.Fatalf("unmarshal %s rsp: %v", route, err)
	}
}

// GateHandler 测试桩：仅用于生成 "GateHandler.kicksession" route，验证顶号下发。
type GateHandler struct{ stub *stubGate }

func (h *GateHandler) KickSession(ctx context.Context, raw []byte) { h.stub.KickSession(ctx, raw) }
