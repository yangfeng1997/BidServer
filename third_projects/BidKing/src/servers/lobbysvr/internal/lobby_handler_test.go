package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/protobuf/proto"
	lobbypb "project/protocal/gen/lobby"
	matchpb "project/protocal/gen/match"
	roompb "project/protocal/gen/room"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
)

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
func (c *capReplier) Reply(data []byte, err error) {
	c.ch <- struct {
		data []byte
		err  error
	}{data, err}
}

func ctxWith(uid int64, r cluster.Replier) context.Context {
	ctx := cluster.WithSession(context.Background(), &clusterpb.ClusterSession{Uid: uid, FrontendId: "1.1.1"})
	return cluster.WithReplier(ctx, r)
}

func TestLobbyHandler_Additem_DeferredReply(t *testing.T) {
	rt, _ := newTestRuntimeWithStore(newFakeStore())
	rt.Start()
	defer rt.Stop()
	// 预置已加载玩家
	rt.Submit(func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	time.Sleep(20 * time.Millisecond)

	h := NewLobbyHandler(rt)
	r := newCapReplier()
	_, err := h.Additem(ctxWith(10001, r), &lobbypb.CS_AddItem{OpId: "op1", ItemId: 100, Count: 5})
	if err != cluster.ErrDeferredReply {
		t.Fatalf("expected deferred sentinel, got %v", err)
	}
	select {
	case got := <-r.ch:
		if got.err != nil {
			t.Fatalf("reply err: %v", got.err)
		}
		var rsp lobbypb.SC_AddItem
		if e := proto.Unmarshal(got.data, &rsp); e != nil {
			t.Fatal(e)
		}
		if rsp.ItemId != 100 || rsp.Count != 5 {
			t.Fatalf("bad rsp: %+v", &rsp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
}

func TestPurchase_Orchestration(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	// 预置货币
	runOnLoop(t, rt, func() { rt.Player(uid).Currency().Gain("seed", "gold", 100) })

	rsp := purchaseSync(t, rt, uid, "p1", "gold", 30, 555)
	if rsp.Code != 0 || rsp.Balance != 70 || rsp.ItemCount != 1 {
		t.Fatalf("purchase: %+v", rsp)
	}
	// 幂等：重复 opID 不双扣双发
	rsp2 := purchaseSync(t, rt, uid, "p1", "gold", 30, 555)
	if rsp2.Balance != 70 || rsp2.ItemCount != 1 {
		t.Fatalf("dup purchase not idempotent: %+v", rsp2)
	}
	// 余额不足拒绝
	rsp3 := purchaseSync(t, rt, uid, "p2", "gold", 1000, 555)
	if rsp3.Code != 1 || rsp3.Balance != 70 {
		t.Fatalf("insufficient not rejected: %+v", rsp3)
	}
}

func TestMailClaim_GrantsAttachments(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	// 直接向 mailbox 投递一封带附件的邮件
	store := rt.mailStore.(*fakeMailStore)
	store.Insert(nil, &MailDoc{To: uid, From: 0, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "gold", Count: 50}, {Kind: "item", ID: 777, Count: 2}}}, func(error) {})
	var id primitive.ObjectID
	store.List(nil, uid, 50, func(ms []MailDoc, _ error) { id = ms[0].ID })

	rsp := mailClaimSync(t, rt, uid, id.Hex())
	if rsp.Code != 0 || len(rsp.Granted) != 2 {
		t.Fatalf("claim: %+v", rsp)
	}
	runOnLoop(t, rt, func() {
		if rt.Player(uid).Currency().Balance("gold") != 50 || rt.Player(uid).Bag().Count(777) != 2 {
			t.Fatal("attachments not granted to player state")
		}
	})
	// 重复 claim 不再发放
	rsp2 := mailClaimSync(t, rt, uid, id.Hex())
	if rsp2.Code != 1 {
		t.Fatalf("dup claim must fail: %+v", rsp2)
	}
}

func TestFriendHandshake_EventualConsistency(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	a, b := int64(1001), int64(1002)
	loadPlayerSync(t, rt, a)
	loadPlayerSync(t, rt, b)

	if rsp := friendAddSync(t, rt, a, b); rsp.Code != 0 {
		t.Fatalf("friendadd: %+v", rsp)
	}
	var reqID string
	mails := mailListSync(t, rt, b)
	for _, m := range mails.Mails {
		if m.Type == MailTypeFriendReq && m.From == a {
			reqID = m.MailId
		}
	}
	if reqID == "" {
		t.Fatal("B did not receive friend_req")
	}
	if rsp := friendRespondSync(t, rt, b, reqID, true); rsp.Code != 0 {
		t.Fatalf("respond: %+v", rsp)
	}
	runOnLoop(t, rt, func() {
		if !rt.Player(b).Friend().Has(a) {
			t.Fatal("B should have A immediately after accept")
		}
	})
	disconnectSync(t, rt, a)
	loadPlayerSync(t, rt, a)
	scanAcceptsSync(t, rt, a)
	runOnLoop(t, rt, func() {
		if !rt.Player(a).Friend().Has(b) {
			t.Fatal("A should have B after re-login accept-scan")
		}
	})
}

func TestLobbyHandler_Additem_PlayerNotLoaded(t *testing.T) {
	rt, _ := newTestRuntimeWithStore(newFakeStore())
	rt.Start()
	defer rt.Stop()
	h := NewLobbyHandler(rt)
	r := newCapReplier()
	_, _ = h.Additem(ctxWith(99999, r), &lobbypb.CS_AddItem{OpId: "o", ItemId: 1, Count: 1})
	select {
	case got := <-r.ch:
		if got.err == nil {
			t.Fatal("expected error for unloaded player")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
}

// startMatchSync 驱动 Startmatch 并同步取回 SC_StartMatch
func startMatchSync(t *testing.T, rt *Runtime, uid int64) *lobbypb.SC_StartMatch {
	t.Helper()
	r := newFakeReplier()
	h := NewLobbyHandler(rt)
	if _, err := h.Startmatch(ctxWith(uid, r), &lobbypb.CS_StartMatch{}); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := r.wait(t)
	if res.err != nil {
		t.Fatalf("startmatch err: %v", res.err)
	}
	var out lobbypb.SC_StartMatch
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

// waitFor 轮询条件至多 2s（off-loop hook 触发用）
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout")
}

type bindCall struct {
	uid        int64
	room, game string
}

func TestLobbyHandler_GameStarted(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()

	var bound atomic.Pointer[bindCall]
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	runOnLoop(t, rt, func() {
		rt.bindRoom = func(uid int64, room, game string) { bound.Store(&bindCall{uid, room, game}) }
		rt.presence = fp
	})

	// 未加载 → code<0
	if rsp := gameStartedSync(t, rt, &matchpb.RPC_GameStarted_Req{Uid: 10001, GameId: "1.8.1-1", RoomNodeId: "1.7.1"}); rsp.Code >= 0 {
		t.Fatalf("not-loaded want negative code, got %d", rsp.Code)
	}

	runOnLoop(t, rt, func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	if rsp := gameStartedSync(t, rt, &matchpb.RPC_GameStarted_Req{Uid: 10001, GameId: "1.8.1-1", RoomNodeId: "1.7.1", Currency: "gold"}); rsp.Code != 0 {
		t.Fatalf("want code 0, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		rb := rt.players[10001].RoomAffinity()
		if rb == nil || rb.roomNodeID != "1.7.1" || rb.gameID != "1.8.1-1" || rb.currency != "gold" {
			t.Fatalf("room affinity not set: %+v", rb)
		}
	})
	waitFor(t, func() bool { return bound.Load() != nil })
	if b := bound.Load(); b.uid != 10001 || b.room != "1.7.1" || b.game != "1.8.1-1" {
		t.Fatalf("bindRoom args mismatch: %+v", b)
	}
	waitFor(t, func() bool { return fp.LastPushUID() == 10001 })
	if fp.LastPushMsgID() != msgIDSCMatchFound {
		t.Fatalf("want SC_MatchFound push, got msgID %d", fp.LastPushMsgID())
	}
}

func gameStartedSync(t *testing.T, rt *Runtime, req *matchpb.RPC_GameStarted_Req) *matchpb.RPC_GameStarted_Rsp {
	t.Helper()
	r := newFakeReplier()
	h := NewLobbyHandler(rt)
	if _, err := h.Gamestarted(ctxWith(req.Uid, r), req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := r.wait(t)
	if res.err != nil {
		t.Fatalf("gamestarted err: %v", res.err)
	}
	var out matchpb.RPC_GameStarted_Rsp
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

func TestLobbyHandler_PurchaseRejectedDuringGame(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 1000)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
	})
	rsp := purchaseSync(t, rt, 10001, "p1", "gold", 10, 5)
	if rsp.Code != 2 {
		t.Fatalf("purchase during game should be rejected code=2, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 1000 {
			t.Fatalf("balance must be untouched when purchase rejected")
		}
	})
}

func TestLobbyHandler_StartMatch(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()

	// 注入可观察的 publishMatch hook —— 经 runOnLoop 在主循环 goroutine 写入，避免与
	// StartMatch 内的读发生数据竞争。published 在 off-loop hook goroutine 写、测试 goroutine 读 → atomic.Pointer。
	var published atomic.Pointer[matchpb.MatchRequest]
	runOnLoop(t, rt, func() {
		rt.publishMatch = func(req *matchpb.MatchRequest) error { published.Store(req); return nil }
	})

	// 未加载玩家 → code<0
	if rsp := startMatchSync(t, rt, 10001); rsp.Code >= 0 {
		t.Fatalf("not-loaded should be negative code, got %d", rsp.Code)
	}

	// 加载玩家（默认 mmr=1000）
	runOnLoop(t, rt, func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	if rsp := startMatchSync(t, rt, 10001); rsp.Code != 0 {
		t.Fatalf("loaded+not-in-game should be code 0, got %d", rsp.Code)
	}
	waitFor(t, func() bool { return published.Load() != nil })
	p := published.Load()
	if p.Uid != 10001 || p.Mmr != 1000 || p.LobbyNodeId == "" || p.ReqId == "" {
		t.Fatalf("published MatchRequest mismatch: %+v", p)
	}

	// 已在局中 → code 1
	runOnLoop(t, rt, func() { rt.players[10001].SetRoomAffinity("1.7.1", "g", "gold") })
	if rsp := startMatchSync(t, rt, 10001); rsp.Code != 1 {
		t.Fatalf("in-game should be code 1, got %d", rsp.Code)
	}
}

func TestLobbyHandler_MailClaim_MultiSameComponentAttachments(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 11001)
	var mid string
	runOnLoop(t, rt, func() {
		mid = seedMailWithAttachments(rt, 11001,
			Attachment{Kind: "gold", ID: 0, Count: 100},
			Attachment{Kind: "diamond", ID: 0, Count: 50},
			Attachment{Kind: "item", ID: 7, Count: 1},
			Attachment{Kind: "item", ID: 8, Count: 1},
		)
	})
	if rsp := mailClaimSync(t, rt, 11001, mid); rsp.Code != 0 {
		t.Fatalf("claim should succeed, code=%d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		p := rt.players[11001]
		if p.Currency().Balance("gold") != 100 || p.Currency().Balance("diamond") != 50 {
			t.Fatalf("both currency attachments must be granted: gold=%d diamond=%d",
				p.Currency().Balance("gold"), p.Currency().Balance("diamond"))
		}
		if p.Bag().Count(7) != 1 || p.Bag().Count(8) != 1 {
			t.Fatalf("both item attachments must be granted: i7=%d i8=%d", p.Bag().Count(7), p.Bag().Count(8))
		}
	})
	// 重放（模拟 grant 落库但 mark 前崩溃）：不得双发
	runOnLoop(t, rt, func() { forceMailUnclaimed(rt, mid) })
	if rsp := mailClaimSync(t, rt, 11001, mid); rsp.Code != 0 {
		t.Fatalf("replay claim should succeed idempotently, code=%d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		p := rt.players[11001]
		if p.Currency().Balance("gold") != 100 || p.Currency().Balance("diamond") != 50 ||
			p.Bag().Count(7) != 1 || p.Bag().Count(8) != 1 {
			t.Fatalf("replay must NOT double-grant: gold=%d diamond=%d i7=%d i8=%d",
				p.Currency().Balance("gold"), p.Currency().Balance("diamond"), p.Bag().Count(7), p.Bag().Count(8))
		}
	})
}

func TestLobbyHandler_MailClaimReorder_IdempotentReplay(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	var mid string
	runOnLoop(t, rt, func() {
		mid = seedMailWithAttachment(rt, 10001, Attachment{Kind: "gold", ID: 0, Count: 50})
	})
	rsp := mailClaimSync(t, rt, 10001, mid)
	if rsp.Code != 0 {
		t.Fatalf("first claim should succeed, code=%d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 50 {
			t.Fatalf("attachment should be granted once")
		}
	})
	// 模拟"已 grant 但 mark 前崩溃→重领"：mail 仍 claimed=false 时再领，应经 ops(mailID) 去重不双发
	runOnLoop(t, rt, func() { forceMailUnclaimed(rt, mid) })
	rsp2 := mailClaimSync(t, rt, 10001, mid)
	if rsp2.Code != 0 {
		t.Fatalf("replay claim should succeed idempotently, code=%d", rsp2.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 50 {
			t.Fatalf("replay must NOT double-grant, bal=%d", rt.players[10001].Currency().Balance("gold"))
		}
	})
}

func TestLobbyHandler_Bid(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	var forwarded atomic.Int64
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 1000)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.forwardBid = func(uid int64, room, game string, amt int64, done func(int32, int64)) {
			forwarded.Add(1)
			done(0, amt)
		}
	})
	// 余额不足
	if rsp := bidSync(t, rt, 10001, "g1", 5000); rsp.Code != 4 {
		t.Fatalf("insufficient should be code=4, got %d", rsp.Code)
	}
	// 亲和不符
	if rsp := bidSync(t, rt, 10001, "other", 10); rsp.Code != 2 {
		t.Fatalf("wrong game should be code=2, got %d", rsp.Code)
	}
	// 正常转发
	if rsp := bidSync(t, rt, 10001, "g1", 100); rsp.Code != 0 || rsp.HighestBid != 100 {
		t.Fatalf("valid bid should forward, code=%d hb=%d", rsp.Code, rsp.HighestBid)
	}
	if forwarded.Load() != 1 {
		t.Fatalf("forward should happen exactly once, got %d", forwarded.Load())
	}
}

func bidSync(t *testing.T, rt *Runtime, uid int64, game string, amount int64) *lobbypb.SC_Bid {
	return driveReq[*lobbypb.SC_Bid](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Bid(ctx, &lobbypb.CS_Bid{GameId: game, Amount: amount})
	})
}

func TestMailclaim_FlushFailure_RepliesRetryAndDoesNotMarkClaimed(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1 << 30}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(12001)
	loadPlayerSync(t, rt, uid)
	mid := seedMailWithAttachment(rt, uid, Attachment{Kind: "gold", ID: 0, Count: 50}) // gold→currency 组件，flush 必失败
	rsp := mailClaimSync(t, rt, uid, mid)
	if rsp.Code != 1 {
		t.Fatalf("落库失败必须 reply Code:1（客户端重领），实得 %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		ms := rt.mailStore.(*fakeMailStore)
		id, _ := primitive.ObjectIDFromHex(mid)
		ms.Get(rt.tq, id, uid, func(ok bool, m *MailDoc, _ error) {
			if !ok || m.Claimed {
				t.Fatal("落库失败时邮件不得被标记领取")
			}
		})
	})
}

func TestLobbyHandler_SettleOnlineWinner(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	var unbound atomic.Int64
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 500)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.unbindRoomFn = func(int64) { unbound.Add(1) }
	})
	rsp := settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 10001, Price: 150, ItemId: 7, Currency: "gold"})
	if rsp.Code != 0 {
		t.Fatalf("settle should ack 0, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		p := rt.players[10001]
		if p.Currency().Balance("gold") != 350 || p.Bag().Count(7) != 1 {
			t.Fatalf("winner should be charged+granted: bal=%d item=%d", p.Currency().Balance("gold"), p.Bag().Count(7))
		}
		if p.RoomAffinity() != nil {
			t.Fatalf("affinity should be cleared")
		}
	})
	if unbound.Load() != 1 {
		t.Fatalf("unbindRoom should be called once")
	}
	// 幂等：重复结算同 gameId 不双扣
	settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 10001, Price: 150, ItemId: 7, Currency: "gold"})
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 350 {
			t.Fatalf("replay must not double-charge")
		}
	})
}

func TestLobbyHandler_SettleOfflineWinner(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() { rt.offlineStore = fos; rt.unbindRoomFn = func(int64) {} })
	rsp := settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 20002, GameId: "g2", Winner: 20002, Price: 80, ItemId: 9, Currency: "gold"})
	if rsp.Code != 0 {
		t.Fatalf("offline settle should ack 0, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if len(fos.docs[20002]) != 1 || fos.docs[20002][0].OpID != "g2" {
			t.Fatalf("offline winner should get inbox msg, got %+v", fos.docs[20002])
		}
	})
}

func TestLobbyHandler_SettleLoser(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 500)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.unbindRoomFn = func(int64) {}
	})
	settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 99999, Price: 150, ItemId: 7, Currency: "gold"})
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 500 {
			t.Fatalf("loser must not be charged")
		}
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("loser affinity must be cleared")
		}
	})
}

func TestLobbyHandler_MatchTimeout(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	runOnLoop(t, rt, func() { rt.presence = fp })
	h := NewLobbyHandler(rt)
	h.Matchtimeout(context.Background(), &matchpb.RPC_MatchTimeout_Notify{Uid: 10001, ReqId: "r1"})
	waitFor(t, func() bool { return fp.LastPushMsgID() == msgIDSCMatchTimeout })
	if fp.LastPushUID() != 10001 {
		t.Fatalf("match timeout should push SC_MatchTimeout to uid 10001, got uid=%d", fp.LastPushUID())
	}
}

func settleSync(t *testing.T, rt *Runtime, req *roompb.RPC_Settle_Req) *roompb.RPC_Settle_Rsp {
	t.Helper()
	r := newFakeReplier()
	h := NewLobbyHandler(rt)
	if _, err := h.Settle(cluster.WithReplier(context.Background(), r), req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	rr := r.wait(t)
	var out roompb.RPC_Settle_Rsp
	if len(rr.data) > 0 {
		_ = proto.Unmarshal(rr.data, &out)
	}
	return &out
}

func TestPurchase_ProbeRoomDeath(t *testing.T) {
	// room 仍活 → 维持禁购 code 2
	t.Run("alive keeps blocked", func(t *testing.T) {
		rt := newTestRuntime(t)
		defer rt.Stop()
		loadPlayerSync(t, rt, 10001)
		runOnLoop(t, rt, func() {
			rt.players[10001].Currency().Gain("seed", "gold", 500)
			rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
			rt.queryGame = func(room, game string, done func(bool)) { done(true) } // 仍活
		})
		if rsp := purchaseSync(t, rt, 10001, "op1", "gold", 10, 7); rsp.Code != 2 {
			t.Fatalf("alive room should keep purchase blocked (code 2), got %d", rsp.Code)
		}
		runOnLoop(t, rt, func() {
			if rt.players[10001].RoomAffinity() == nil {
				t.Fatalf("alive room must NOT clear affinity")
			}
		})
	})
	// room 死 → 清亲和 + unbind + code 3（请重试）
	t.Run("dead clears affinity", func(t *testing.T) {
		rt := newTestRuntime(t)
		defer rt.Stop()
		loadPlayerSync(t, rt, 10001)
		var unbound atomic.Int64
		runOnLoop(t, rt, func() {
			rt.players[10001].Currency().Gain("seed", "gold", 500)
			rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
			rt.unbindRoomFn = func(int64) { unbound.Add(1) }
			rt.queryGame = func(room, game string, done func(bool)) { done(false) } // 已死
		})
		if rsp := purchaseSync(t, rt, 10001, "op1", "gold", 10, 7); rsp.Code != 3 {
			t.Fatalf("dead room should return retry (code 3), got %d", rsp.Code)
		}
		runOnLoop(t, rt, func() {
			if rt.players[10001].RoomAffinity() != nil {
				t.Fatalf("dead room must clear affinity")
			}
		})
		if unbound.Load() != 1 {
			t.Fatalf("dead room should unbindRoom once, got %d", unbound.Load())
		}
	})
}
