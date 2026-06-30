package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
)

// fakeKicker 仅捕获 Cast（顶号下发）
type fakeKicker struct {
	cluster.Cluster
	castTarget cluster.NodeID
	castRoute  string
	castUID    int64
	castCount  int
}

func (f *fakeKicker) Cast(_ context.Context, target cluster.NodeID, route string, msg proto.Message) error {
	f.castTarget, f.castRoute = target, route
	if n, ok := msg.(*onlinepb.RPC_KickSession_Notify); ok {
		f.castUID = n.Uid
	}
	f.castCount++
	return nil
}

func newTestHandler() (*OnlineHandler, *fakeKicker) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	fk := &fakeKicker{}
	return NewOnlineHandler(dir, fk), fk
}

func TestOnlineHandler_RegisterQuery(t *testing.T) {
	h, fk := newTestHandler()
	ctx := context.Background()
	rsp, err := h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	if err != nil || rsp.Code != 0 || rsp.KickedOld {
		t.Fatalf("register: %+v %v", rsp, err)
	}
	if fk.castCount != 0 {
		t.Fatal("first register should not kick")
	}
	q, _ := h.Query(ctx, &onlinepb.RPC_Query_Req{Uid: 10001})
	if !q.Online || q.Entry.GatewayNodeId != "1.1.1" {
		t.Fatalf("query: %+v", q)
	}
}

func TestOnlineHandler_DupLoginKicksOldGateway(t *testing.T) {
	h, fk := newTestHandler()
	ctx := context.Background()
	h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	rsp, _ := h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.2", LobbyNodeId: "1.2.1"})
	if !rsp.KickedOld {
		t.Fatal("dup login should report kicked_old")
	}
	if fk.castCount != 1 || fk.castRoute != "GateHandler.kicksession" || fk.castUID != 10001 {
		t.Fatalf("kick cast wrong: count=%d route=%s uid=%d", fk.castCount, fk.castRoute, fk.castUID)
	}
	want, _ := cluster.ParseNodeID("1.1.1") // 踢的是旧 gateway
	if fk.castTarget != want {
		t.Fatalf("kick target = %v, want old gateway %v", fk.castTarget, want)
	}
}

func TestOnlineHandler_UnregisterTouch(t *testing.T) {
	h, _ := newTestHandler()
	ctx := context.Background()
	h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	tr, _ := h.Touch(ctx, &onlinepb.RPC_Touch_Req{Uid: 10001})
	if !tr.Online {
		t.Fatal("touch online should be true")
	}
	h.Unregister(ctx, &onlinepb.RPC_Unregister_Req{Uid: 10001})
	q, _ := h.Query(ctx, &onlinepb.RPC_Query_Req{Uid: 10001})
	if q.Online {
		t.Fatal("should be offline after unregister")
	}
	tr2, _ := h.Touch(ctx, &onlinepb.RPC_Touch_Req{Uid: 10001})
	if tr2.Online {
		t.Fatal("touch after unregister should report offline")
	}
}

func TestOnlineHandler_BindRoom(t *testing.T) {
	h, _ := newTestHandler()
	h.Register(context.Background(), &onlinepb.RPC_Register_Req{Uid: 7, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})

	rsp, err := h.Bindroom(context.Background(), &onlinepb.RPC_BindRoom_Req{Uid: 7, RoomNodeId: "1.7.1", GameId: "1.8.1-1"})
	if err != nil || rsp.Code != 0 {
		t.Fatalf("bindroom want code 0, got code=%d err=%v", rsp.GetCode(), err)
	}
	q, _ := h.Query(context.Background(), &onlinepb.RPC_Query_Req{Uid: 7})
	if q.Entry.RoomNodeId != "1.7.1" || q.Entry.GameId != "1.8.1-1" {
		t.Fatalf("query should see room binding, got %+v", q.Entry)
	}

	un, err := h.Unbindroom(context.Background(), &onlinepb.RPC_UnbindRoom_Req{Uid: 7})
	if err != nil || un.Code != 0 {
		t.Fatalf("unbindroom want code 0, got code=%d err=%v", un.GetCode(), err)
	}
	q, _ = h.Query(context.Background(), &onlinepb.RPC_Query_Req{Uid: 7})
	if q.Entry.RoomNodeId != "" || q.Entry.GameId != "" {
		t.Fatalf("query should see cleared binding, got %+v", q.Entry)
	}
}

func TestOnlineHandler_BindRoom_NotOnline(t *testing.T) {
	h, _ := newTestHandler()
	rsp, _ := h.Bindroom(context.Background(), &onlinepb.RPC_BindRoom_Req{Uid: 99, RoomNodeId: "1.7.1", GameId: "g"})
	if rsp.Code == 0 {
		t.Fatalf("bindroom on offline uid should return non-zero code, got 0")
	}
}
