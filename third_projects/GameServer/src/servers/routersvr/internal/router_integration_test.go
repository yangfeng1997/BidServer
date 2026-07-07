//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
	"project/src/framework/cluster/transport"
)

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

// OnlineHandler stub：回 Query，data 里带上自己 nodeID 以便断言"一致路由"。
type OnlineHandler struct{ nodeID string }

func (h *OnlineHandler) Query(_ context.Context, req *onlinepb.RPC_Query_Req) (*onlinepb.RPC_Query_Rsp, error) {
	return &onlinepb.RPC_Query_Rsp{Online: true, Entry: &onlinepb.OnlineEntry{
		Uid: req.Uid, GatewayNodeId: h.nodeID, // 借字段回传处理实例 nodeID
	}}, nil
}

func startNode(t *testing.T, id, typ string, async bool, register func(app *application.Application, cls *transport.NatsCluster)) *transport.NatsCluster {
	t.Helper()
	self, err := cluster.ParseNodeID(id)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints: etcdEndpoints, NatsURLs: natsURLs,
		SelfAddr: "127.0.0.1:0", ServerTypeName: typ, AsyncDispatch: async,
	})
	if err != nil {
		t.Fatalf("cluster %s: %v", id, err)
	}
	app := application.NewBuilder().
		NodeID(id).NodeType(typ).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).Build()
	register(app, cls)
	app.Start()
	if err := cls.Init(); err != nil {
		t.Fatalf("init %s: %v", id, err)
	}
	return cls
}

func TestRouter_ForwardConsistent_EndToEnd(t *testing.T) {
	// 2 个 stub online 实例
	for _, id := range []string{"1.5.1", "1.5.2"} {
		nodeID := id
		cls := startNode(t, id, "onlinesvr", false, func(app *application.Application, _ *transport.NatsCluster) {
			if err := app.RegisterHandler(&OnlineHandler{nodeID: nodeID}, nil); err != nil {
				t.Fatalf("register online stub: %v", err)
			}
		})
		defer cls.Stop()
	}
	// 2 个 router 实例（asyncDispatch）
	for _, id := range []string{"1.6.1", "1.6.2"} {
		cls := startNode(t, id, "routersvr", true, func(app *application.Application, c *transport.NatsCluster) {
			mod := NewRouterModule(c.Discovery(), app.Cluster(), nil)
			app.Register(mod)
			if err := app.RegisterHandler(NewRouterHandler(mod), nil); err != nil {
				t.Fatalf("register router handler: %v", err)
			}
		})
		defer cls.Stop()
	}
	// lobby 模拟节点
	lobbyCls := startNode(t, "1.2.250", "lobbysvr", false, func(app *application.Application, _ *transport.NatsCluster) {})
	defer lobbyCls.Stop()

	// 等发现 router 与 online
	if !waitForType(lobbyCls, "routersvr", 5*time.Second) || !waitForType(lobbyCls, "onlinesvr", 5*time.Second) {
		t.Fatal("router/online not discovered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 同一 uid 多次经 router 转发，应稳定落到同一 online 实例（一致性哈希）
	var first string
	for i := 0; i < 5; i++ {
		rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Query_Rsp](
			ctx, lobbyCls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001",
			"OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001})
		if err != nil {
			t.Fatalf("forward query: %v", err)
		}
		got := rsp.Entry.GatewayNodeId // stub 回传的处理实例 nodeID
		if got != "1.5.1" && got != "1.5.2" {
			t.Fatalf("unexpected online instance: %s", got)
		}
		if first == "" {
			first = got
		} else if got != first {
			t.Fatalf("consistent hash unstable: %s vs %s", got, first)
		}
	}
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

var _ = proto.Marshal // 保留 proto import（如未直接使用）
