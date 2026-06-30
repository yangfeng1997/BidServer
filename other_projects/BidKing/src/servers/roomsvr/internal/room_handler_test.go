package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	roompb "project/protocal/gen/room"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
)

// capReplier 捕获延迟回包
type capReplier struct {
	ch chan struct {
		data []byte
		err  error
	}
}

func newCapReplier() *capReplier {
	return &capReplier{ch: make(chan struct {
		data []byte
		err  error
	}, 1)}
}

func (r *capReplier) Reply(data []byte, err error) {
	r.ch <- struct {
		data []byte
		err  error
	}{data, err}
}

func openGameSync(t *testing.T, rt *Runtime, req *roompb.RPC_OpenGame_Req) *roompb.RPC_OpenGame_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Opengame(ctx, req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	select {
	case res := <-r.ch:
		if res.err != nil {
			t.Fatalf("opengame err: %v", res.err)
		}
		var out roompb.RPC_OpenGame_Rsp
		if err := proto.Unmarshal(res.data, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return &out
	case <-time.After(2 * time.Second):
		t.Fatalf("opengame timeout")
		return nil
	}
}

func TestRoomHandler_OpenGame(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()

	rsp := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "1.8.1-1", ItemId: 5, CountdownSec: 30,
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}, {Uid: 2, LobbyNodeId: "1.2.1"}},
	})
	if rsp.Code != 0 || rsp.RoomNodeId != "1.7.1" {
		t.Fatalf("want code 0 room=1.7.1, got code=%d room=%s", rsp.Code, rsp.RoomNodeId)
	}

	// 同 gameId 幂等：再开返回同 room_node_id、code 0
	rsp2 := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{GameId: "1.8.1-1", Participants: []*roompb.Participant{{Uid: 1}}})
	if rsp2.Code != 0 || rsp2.RoomNodeId != "1.7.1" {
		t.Fatalf("idempotent open mismatch: code=%d room=%s", rsp2.Code, rsp2.RoomNodeId)
	}

	// 参数非法：空 gameId / 空 participants → code 1
	bad := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{GameId: "", Participants: nil})
	if bad.Code == 0 {
		t.Fatalf("empty gameId/participants should be non-zero code, got 0")
	}
}

func rejoinSync(t *testing.T, rt *Runtime, req *roompb.RPC_Rejoin_Req) *roompb.RPC_Rejoin_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Rejoin(ctx, req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	select {
	case res := <-r.ch:
		if res.err != nil {
			t.Fatalf("rejoin err: %v", res.err)
		}
		var out roompb.RPC_Rejoin_Rsp
		if err := proto.Unmarshal(res.data, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return &out
	case <-time.After(2 * time.Second):
		t.Fatalf("rejoin timeout")
		return nil
	}
}

func TestRoomHandler_Rejoin(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "g1", ItemId: 7, CountdownSec: 30, Currency: "gold",
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}},
	})
	// place a bid so the snapshot has non-zero highest bid/bidder — guards against
	// any field transposition when mapping the 6-tuple into RPC_Rejoin_Rsp.
	roomRunOnLoop(t, rt, func() { rt.Bid("g1", 1, 150) })
	rsp := rejoinSync(t, rt, &roompb.RPC_Rejoin_Req{GameId: "g1", Uid: 1, NewLobbyNode: "1.2.9"})
	if rsp.Code != 0 || rsp.ItemId != 7 || rsp.Currency != "gold" {
		t.Fatalf("rejoin handler should return alive snapshot, got %+v", rsp)
	}
	if rsp.HighestBid != 150 || rsp.HighestBidder != 1 {
		t.Fatalf("rejoin snapshot must map highest bid/bidder, got hb=%d hbr=%d", rsp.HighestBid, rsp.HighestBidder)
	}
	// re-route actually applied on the runtime
	roomRunOnLoop(t, rt, func() {
		if got := rt.Game("g1").Participants[0].LobbyNodeID; got != "1.2.9" {
			t.Fatalf("handler rejoin must re-route participant lobbyNode, got %q", got)
		}
	})
	// 局不存在 → code 2
	if bad := rejoinSync(t, rt, &roompb.RPC_Rejoin_Req{GameId: "nope", Uid: 1, NewLobbyNode: "1.2.9"}); bad.Code != 2 {
		t.Fatalf("absent game should be code 2, got %d", bad.Code)
	}
}

func queryGameSync(t *testing.T, rt *Runtime, gameID string) *roompb.RPC_QueryGame_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Querygame(ctx, &roompb.RPC_QueryGame_Req{GameId: gameID}); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := <-r.ch
	if res.err != nil {
		t.Fatalf("querygame err: %v", res.err)
	}
	var out roompb.RPC_QueryGame_Rsp
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

func TestRoomHandler_QueryGame(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "g1", ItemId: 7, CountdownSec: 30, Currency: "gold",
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}},
	})
	if q := queryGameSync(t, rt, "g1"); !q.Exists || q.Closed {
		t.Fatalf("open game should be exists&&!closed, got %+v", q)
	}
	if q := queryGameSync(t, rt, "nope"); q.Exists {
		t.Fatalf("absent game should be exists=false, got %+v", q)
	}
	roomRunOnLoop(t, rt, func() { rt.Game("g1").closed = true })
	if q := queryGameSync(t, rt, "g1"); !q.Exists || !q.Closed {
		t.Fatalf("closed game should be exists&&closed, got %+v", q)
	}
}
