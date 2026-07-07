package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	matchpb "project/protocal/gen/match"
	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/matchqueue"
	clusterpb "project/src/framework/cluster/pb"
)

// fakeDisc 提供成员列表
type fakeDisc struct{ nodes map[string][]*clusterpb.NodeInfo }

func (d *fakeDisc) ByType(typ string) []*clusterpb.NodeInfo { return d.nodes[typ] }

func TestRouterModule_ResolveConsistentHash(t *testing.T) {
	disc := &fakeDisc{nodes: map[string][]*clusterpb.NodeInfo{
		"onlinesvr": {{NodeId: "1.5.1"}, {NodeId: "1.5.2"}, {NodeId: "1.5.3"}},
	}}
	m := NewRouterModule(disc, nil, nil)
	// 同 key 稳定解析到某个 online 实例
	id1, ok := m.Resolve("onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001")
	if !ok {
		t.Fatal("resolve should succeed with members")
	}
	id2, _ := m.Resolve("onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001")
	if id1 != id2 {
		t.Fatal("resolve must be stable for same key")
	}
	// 无成员 → 解析失败
	if _, ok := m.Resolve("matchsvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "x"); ok {
		t.Fatal("resolve should fail when no members")
	}
}

func TestRouterModule_ResolveDirect(t *testing.T) {
	m := NewRouterModule(&fakeDisc{}, nil, nil)
	id, ok := m.Resolve("roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, "1.7.3")
	if !ok {
		t.Fatal("direct resolve should parse nodeID")
	}
	if id.String() != "1.7.3" {
		t.Fatalf("direct nodeID = %s", id.String())
	}
}

func TestRouterHandler_ForwardNoTarget(t *testing.T) {
	m := NewRouterModule(&fakeDisc{}, nil, nil) // 空 discovery
	h := NewRouterHandler(m)
	inner, _ := proto.Marshal(&onlinepb.RPC_Register_Req{Uid: 1})
	rsp, err := h.Forward(context.Background(), &routerpb.RPC_RouterForward_Req{
		RoutingMode: routerpb.RoutingMode_ROUTING_CONSISTENT_HASH,
		TargetType:  "onlinesvr", RoutingKey: "1",
		InnerRoute: "OnlineHandler.register", InnerData: inner,
	})
	if err != nil {
		t.Fatalf("forward err: %v", err)
	}
	if rsp.Code == 0 {
		t.Fatal("forward should report error code when no target")
	}
}

func TestRouterHandler_PublishMatch(t *testing.T) {
	mq := matchqueue.NewMemoryQueue()
	m := NewRouterModule(&fakeDisc{}, nil, mq)
	h := NewRouterHandler(m)

	rsp, err := h.Publishmatch(context.Background(), &matchpb.MatchRequest{Uid: 7, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
	if err != nil || rsp.Code != 0 {
		t.Fatalf("publishmatch want code 0, got code=%d err=%v", rsp.GetCode(), err)
	}
	pub := mq.Published()
	if len(pub) != 1 {
		t.Fatalf("want 1 published msg, got %d", len(pub))
	}
	var got matchpb.MatchRequest
	if err := proto.Unmarshal(pub[0], &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Uid != 7 || got.ReqId != "r1" || got.LobbyNodeId != "1.2.1" {
		t.Fatalf("published payload mismatch: %+v", &got)
	}
}
