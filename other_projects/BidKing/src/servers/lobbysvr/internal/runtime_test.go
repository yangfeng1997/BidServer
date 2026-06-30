package internal

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/taskqueue"
)

// newTestRuntimeWithStore 构造不依赖 cluster 的 Runtime：online 注册替换为往 channel 投递的桩
// （channel 而非 slice，避免跨 goroutine 读写竞态，-race 友好）。
func newTestRuntimeWithStore(store DocStore) (*Runtime, chan int64) {
	rt := NewRuntime(RuntimeConfig{
		NodeID:        "1.2.1",
		Cluster:       nil, // 不调用真实集群
		Store:         store,
		Tick:          5 * time.Millisecond,
		FlushInterval: time.Hour, // 周期 flush 不在本测试触发
	})
	regCh := make(chan int64, 16)
	rt.onlineRegister = func(uid int64, gw string) { regCh <- uid }
	rt.onlineUnregister = func(uid int64) {}
	return rt, regCh
}

func TestRuntime_SubmitSerial(t *testing.T) {
	rt, _ := newTestRuntimeWithStore(newFakeStore())
	rt.Start()
	defer rt.Stop()

	var mu sync.Mutex
	var order []int
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		i := i
		rt.Submit(func() {
			mu.Lock()
			order = append(order, i)
			n := len(order)
			mu.Unlock()
			if n == 50 {
				close(done)
			}
		})
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tasks not drained")
	}
	mu.Lock()
	defer mu.Unlock()
	for i, v := range order {
		if v != i {
			t.Fatalf("out of order at %d: %d", i, v)
		}
	}
}

func TestRuntime_Login_FreshPlayerAndReply(t *testing.T) {
	store := newFakeStore() // 无文档 → 建新档
	rt, regCh := newTestRuntimeWithStore(store)
	rt.Start()
	defer rt.Stop()

	type res struct {
		rsp *lobbypb.RPC_Login_Rsp
		err error
	}
	ch := make(chan res, 1)
	rt.Submit(func() {
		rt.Login(20002, "1.1.1", func(rsp *lobbypb.RPC_Login_Rsp, err error) {
			ch <- res{rsp, err}
		})
	})
	select {
	case r := <-ch:
		if r.err != nil || r.rsp.GetUid() != 20002 {
			t.Fatalf("login bad: rsp=%v err=%v", r.rsp, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login no reply")
	}
	// online 注册被调（channel，race-free）
	select {
	case uid := <-regCh:
		if uid != 20002 {
			t.Fatalf("online register uid=%d", uid)
		}
	case <-time.After(time.Second):
		t.Fatal("online register not called")
	}
	// 玩家在内存（经 Submit 在主循环读，结果回传 channel）
	inMem := make(chan bool, 1)
	rt.Submit(func() { _, ok := rt.players[20002]; inMem <- ok })
	if !<-inMem {
		t.Fatal("player not in memory")
	}
}

func TestRuntime_FlushClearsDirtyAndPersists(t *testing.T) {
	store := newFakeStore()
	rt, _ := newTestRuntimeWithStore(store)
	rt.Start()
	defer rt.Stop()

	// 准备一个已加载、背包变脏的玩家
	done := make(chan struct{})
	rt.Submit(func() {
		p := buildPlayer(30003, NewPlayerDoc(30003))
		rt.players[30003] = p
		p.Bag().Add("op1", 100, 5)
		rt.flushPlayer(30003, p, func(ok bool) { close(done) })
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush after not called")
	}
	rt.Submit(func() {
		if rt.players[30003].Bag().Dirty() {
			t.Error("bag still dirty after successful flush")
		}
	})
	time.Sleep(50 * time.Millisecond)
	if _, ok := store.flushed["30003:bag"]; !ok {
		t.Fatalf("bag not persisted: %v", store.flushed)
	}
}

func TestRuntime_TouchThrottle(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	var count int
	rt.onlineTouch = func(int64) { count++ }
	runOnLoop(t, rt, func() {
		rt.Touch(uid) // 首次：触发
		rt.Touch(uid) // 立即第二次：被节流
	})
	if count != 1 {
		t.Fatalf("throttle expected 1 touch, got %d", count)
	}
}

func TestRuntime_TouchUnknownPlayer_NoOp(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var count int
	rt.onlineTouch = func(int64) { count++ }
	runOnLoop(t, rt, func() { rt.Touch(99999) })
	if count != 0 {
		t.Fatal("touch on absent player must no-op")
	}
}

func TestFlushSoon_CoalescesToOneFlush(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	fs := rt.store.(*fakeStore)
	runOnLoop(t, rt, func() {
		rt.Player(uid).Currency().Gain("s", "gold", 10) // 标脏
		rt.FlushSoon(uid)
		rt.FlushSoon(uid)  // 两次标记合并为一次待 flush
		rt.coalesceFlush() // 手动触发一次合并 flush
	})
	if _, ok := fs.flushed["10001:currency"]; !ok {
		t.Fatal("currency not flushed by coalesceFlush")
	}
	runOnLoop(t, rt, func() {
		if len(rt.dirtyFlush) != 0 {
			t.Fatal("pending flush set not cleared")
		}
	})
}

// gatedStore：Load 同步；FlushFields 挂起 done，releaseAll 经 dispatcher 投递回调。
// pending/flushed 被 loop goroutine 与测试 goroutine 并发访问，用 mutex 守护（-race 安全）。
type gatedStore struct {
	mu      sync.Mutex
	docs    map[int64]*PlayerDoc
	flushed map[string]bool
	pending []func()
}

func newGatedStore() *gatedStore {
	return &gatedStore{docs: map[int64]*PlayerDoc{}, flushed: map[string]bool{}}
}

func (g *gatedStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	g.mu.Lock()
	d, ok := g.docs[uid]
	g.mu.Unlock()
	done(d, ok, nil)
}

func (g *gatedStore) FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	g.mu.Lock()
	g.pending = append(g.pending, func() {
		g.mu.Lock()
		for field := range fields {
			g.flushed[strconv.FormatInt(uid, 10)+":"+field] = true
		}
		g.mu.Unlock()
		d.Enqueue(func() { done(nil) })
	})
	g.mu.Unlock()
}

func (g *gatedStore) releaseAll() {
	g.mu.Lock()
	pending := g.pending
	g.pending = nil
	g.mu.Unlock()
	for _, p := range pending { // 在锁外执行（closure 内部会再取锁）
		p()
	}
}

func (g *gatedStore) flushedField(uid, field string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.flushed[uid+":"+field]
}

var _ DocStore = (*gatedStore)(nil)

func TestRuntime_StopWaitsForInflightFlush(t *testing.T) {
	gs := newGatedStore()
	rt := NewRuntime(RuntimeConfig{
		Store: gs, MailStore: newFakeMailStore(),
		Tick: 10 * time.Millisecond, FlushInterval: time.Hour,
	})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	runOnLoop(t, rt, func() { rt.Player(uid).Bag().Add("op", 1, 1) }) // 标脏

	stopped := make(chan struct{})
	go func() { rt.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before in-flight flush completed")
	case <-time.After(50 * time.Millisecond):
	}
	gs.releaseAll() // 释放挂起的 flush done
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after flush completed")
	}
	if !gs.flushedField("1", "bag") {
		t.Fatal("bag not flushed on shutdown")
	}
}

// failFieldStore：批中含 failField 且 failsLeft>0 时整批失败（原子，模拟单 $set 全不落）并自减；
// 否则成功。flushes 计数批量写次数。
type failFieldStore struct {
	docs      map[int64]*PlayerDoc
	failField string
	failsLeft int
	flushes   int
}

func (s *failFieldStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	d, ok := s.docs[uid]
	done(d, ok, nil)
}
func (s *failFieldStore) FlushFields(_ taskqueue.Dispatcher, _ int64, fields map[string]any, done func(error)) {
	s.flushes++
	if _, hit := fields[s.failField]; hit && s.failsLeft > 0 {
		s.failsLeft--
		done(errFlush) // 原子失败：整批不落
		return
	}
	done(nil)
}

var errFlush = errors.New("flush boom")

var _ DocStore = (*failFieldStore)(nil)

func TestRuntime_OfflineReplayOnLogin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		fos.docs[30003] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g3", Price: 60, Currency: "gold", ItemID: 5}}
		seedPlayerDoc(rt, 30003, "gold", 200)
	})
	loadPlayerSync(t, rt, 30003)
	runOnLoop(t, rt, func() {
		p := rt.players[30003]
		if p.Currency().Balance("gold") != 140 || p.Bag().Count(5) != 1 {
			t.Fatalf("replay should charge+grant: bal=%d item=%d", p.Currency().Balance("gold"), p.Bag().Count(5))
		}
		if len(fos.docs[30003]) != 0 {
			t.Fatalf("inbox should be acked(pulled) after replay")
		}
	})
}

func TestRuntime_OfflineReplaySkipsAlreadyApplied(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		seedPlayerDocWithOp(rt, 40004, "gold", 100, "g4") // 余额已扣后状态 + ops 含 g4
		fos.docs[40004] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g4", Price: 60, Currency: "gold", ItemID: 5}}
	})
	loadPlayerSync(t, rt, 40004)
	runOnLoop(t, rt, func() {
		if rt.players[40004].Currency().Balance("gold") != 100 {
			t.Fatalf("already-applied gameId must not double-charge, bal=%d", rt.players[40004].Currency().Balance("gold"))
		}
	})
}

func TestFlushPlayer_AtomicBatchFailureRemarksAllDirtyAndFiresAfterOnce(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	afterCount, gotOK := 0, true
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		p.Bag().Add("o1", 1, 1)            // 脏
		p.Currency().Gain("o2", "gold", 5) // 脏（批含 currency → 整批失败）
		p.Friend().Add(2)                  // 脏
		rt.flushPlayer(uid, p, func(ok bool) { afterCount++; gotOK = ok })
	})
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		if !p.Currency().Dirty() || !p.Bag().Dirty() || !p.Friend().Dirty() {
			t.Fatal("原子批失败必须把全部组件重标脏（无半落）")
		}
		if store.flushes != 1 {
			t.Fatalf("应为一次批量 FlushFields，实得 %d", store.flushes)
		}
	})
	if afterCount != 1 {
		t.Fatalf("after 必须恰触发一次，实得 %d", afterCount)
	}
	if gotOK {
		t.Fatal("批失败时 after(ok) 必须收到 ok=false")
	}
}

func TestReconnect_AliveRejoin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	runOnLoop(t, rt, func() {
		rt.presence = fp
		rt.queryOnline = func(uid int64, done func(string, string)) { done("1.7.1", "g1") }
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			if room != "1.7.1" || game != "g1" || newLobby != rt.nodeID {
				t.Errorf("rejoin args mismatch: room=%s game=%s newLobby=%s", room, game, newLobby)
			}
			done(rejoinResult{code: 0, hb: 120, hbr: 10001, rem: 25, itemID: 9, currency: "gold"})
		}
	})
	loadPlayerSync(t, rt, 10001) // 触发 Login 重连分支
	runOnLoop(t, rt, func() {
		aff := rt.players[10001].RoomAffinity()
		if aff == nil || aff.roomNodeID != "1.7.1" || aff.gameID != "g1" || aff.currency != "gold" {
			t.Fatalf("reconnect should rebuild affinity, got %+v", aff)
		}
	})
	waitFor(t, func() bool { return fp.LastPushMsgID() == msgIDSCReconnectAuction })
}

func TestReconnect_VoidWhenRoomDead(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	var unbound atomic.Int64
	runOnLoop(t, rt, func() {
		rt.presence = fp
		rt.unbindRoomFn = func(int64) { unbound.Add(1) }
		rt.queryOnline = func(uid int64, done func(string, string)) { done("1.7.1", "g1") }
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			done(rejoinResult{code: 2}) // room 不可达/局不存在
		}
	})
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("dead room reconnect must NOT set affinity")
		}
	})
	if unbound.Load() != 1 {
		t.Fatalf("void should unbindRoom once, got %d", unbound.Load())
	}
	waitFor(t, func() bool { return fp.LastPushMsgID() == msgIDSCReconnectAuction })
}

func TestReconnect_NoBindingNormalLogin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var rejoinCalled atomic.Int64
	runOnLoop(t, rt, func() {
		rt.queryOnline = func(uid int64, done func(string, string)) { done("", "") } // 无绑定
		rt.rejoinRoom = func(uid int64, room, game, newLobby string, done func(rejoinResult)) {
			rejoinCalled.Add(1)
			done(rejoinResult{code: 0})
		}
	})
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("no-binding login must not set affinity")
		}
	})
	if rejoinCalled.Load() != 0 {
		t.Fatalf("no binding ⇒ rejoin must not be called, got %d", rejoinCalled.Load())
	}
}

func TestDisconnect_InGamePreservesOnlineEntry(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var unregistered atomic.Int64
	runOnLoop(t, rt, func() { rt.onlineUnregister = func(int64) { unregistered.Add(1) } })
	loadPlayerSync(t, rt, 10001)
	// in-game：置亲和后断连 → 不应注销在线条目（靠 TTL 过期作宽限窗）
	runOnLoop(t, rt, func() { rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold") })
	disconnectSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		if rt.players[10001] != nil {
			t.Fatalf("disconnect should evict in-memory player")
		}
	})
	if unregistered.Load() != 0 {
		t.Fatalf("in-game disconnect must NOT unregister online entry, got %d", unregistered.Load())
	}
	// 非 in-game：断连 → 立即注销
	loadPlayerSync(t, rt, 20002)
	disconnectSync(t, rt, 20002)
	if unregistered.Load() != 1 {
		t.Fatalf("non-in-game disconnect must unregister once, got %d", unregistered.Load())
	}
}

func TestDisconnect_FlushFailureKeepsPlayerForRetry(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: BagField, failsLeft: 1 << 30}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	runOnLoop(t, rt, func() { rt.Player(uid).Bag().Add("o1", 1, 1) }) // 脏（flush 必失败）
	disconnectSync(t, rt, uid)
	runOnLoop(t, rt, func() {
		if rt.players[uid] == nil {
			t.Fatal("落库失败必须不剔除玩家（防丢脏内存态）")
		}
		if !rt.players[uid].Bag().Dirty() {
			t.Fatal("失败的 flush 必须保留脏以便重试")
		}
	})
}

func TestSettle_OnlineWinner_FlushFailure_ThenRetryExactlyOnce(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1}
	store.docs[1] = func() *PlayerDoc {
		d := NewPlayerDoc(1)
		d.Currency = CurrencyState{Balances: map[string]int64{"gold": 1000}}
		return d
	}()
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	var codes []int32
	runOnLoop(t, rt, func() {
		rt.Settle(uid, "g1", uid, 60, "gold", 5, func(code int32) { codes = append(codes, code) }) // winner=uid
	})
	runOnLoop(t, rt, func() {
		rt.Settle(uid, "g1", uid, 60, "gold", 5, func(code int32) { codes = append(codes, code) }) // room 重投
	})
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		if p.Currency().Balance("gold") != 940 {
			t.Fatalf("重投必须经 opID=gameId 去重，恰扣一次，bal=%d", p.Currency().Balance("gold"))
		}
		if p.Bag().Count(5) != 1 {
			t.Fatalf("奖品恰发一次，count=%d", p.Bag().Count(5))
		}
	})
	if len(codes) != 2 || codes[0] != 1 || codes[1] != 0 {
		t.Fatalf("期望 [1,0]（首发落库失败 room 重投，重投成功 ack），实得 %v", codes)
	}
}

func TestReplay_FlushFailure_KeepsInboxAndContinuesLogin(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1 << 30}
	store.docs[30005] = func() *PlayerDoc {
		d := NewPlayerDoc(30005)
		d.Currency = CurrencyState{Balances: map[string]int64{"gold": 200}}
		return d
	}()
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		fos.docs[30005] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g5", Price: 60, Currency: "gold", ItemID: 5}}
	})
	var code int32 = -1
	done := make(chan struct{})
	rt.Submit(func() {
		rt.Login(30005, "0.2.1", func(rsp *lobbypb.RPC_Login_Rsp, _ error) {
			if rsp != nil {
				code = rsp.Code
			}
			close(done)
		})
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("login timeout")
	}
	runOnLoop(t, rt, func() {}) // barrier
	if code != 0 {
		t.Fatalf("落库失败不应阻塞登录，reply code=%d", code)
	}
	runOnLoop(t, rt, func() {
		if len(fos.docs[30005]) != 1 {
			t.Fatalf("落库失败必须不 $pull，inbox=%d", len(fos.docs[30005]))
		}
		p := rt.players[30005]
		if p == nil || p.Currency().Balance("gold") != 140 || p.Bag().Count(5) != 1 {
			t.Fatal("重放后内存应已应用 grants（下次登录靠 opID 去重收敛）")
		}
	})
}

// TestReplayOffline_NoDoubleApplyAfterEviction 复现 ack-fail + 淘汰压力 → double-apply 窄窗（⑤）。
// 场景：$pull 失败（消息滞留）→ 130 次无关 op 施加淘汰压力 → opID "G" 若未被保护会从环中淘汰
// → 下次重放时去重失效 → double-apply。修复后：replayOffline 在 Ack 成功前保护 opID，
// 故 "G" 在淘汰压力下仍留环，重放幂等，不再 double-apply。
func TestReplayOffline_NoDoubleApplyAfterEviction(t *testing.T) {
	const uid = int64(55001)
	const initialGold = int64(500)
	const price = int64(50)
	const itemID = int32(7)

	rt := newTestRuntime(t)
	defer rt.Stop()

	// 1. 注入 failAck=true 的 fakeOfflineStore：Ack 返回错误且消息不删除
	fos := &fakeOfflineStore{
		docs:    map[int64][]OfflineMsg{uid: {{Type: OfflineMsgSettle, OpID: "G", Price: price, Currency: "gold", ItemID: itemID}}},
		failAck: true,
	}

	// 2. 在主循环内置 fakeStore 文档（gold=500），并注入 offlineStore
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		doc := NewPlayerDoc(uid)
		doc.Currency = CurrencyState{Balances: map[string]int64{"gold": initialGold}}
		rt.store.(*fakeStore).docs[uid] = doc
	})

	// 3. 驱动登录（触发 replayOffline：Load→apply→flush→Ack(fail)）
	loadPlayerSync(t, rt, uid)

	// 4. 读取重放后余额/物品（期望 gold=450, item7=1）
	var bal1 int64
	var cnt1 int32
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		bal1 = p.Currency().Balance("gold")
		cnt1 = p.Bag().Count(itemID)
	})
	if bal1 != initialGold-price {
		t.Fatalf("step4: 重放后 gold 应扣减 %d，实得 %d", price, bal1)
	}
	if cnt1 != 1 {
		t.Fatalf("step4: 重放后 item7 应为 1，实得 %d", cnt1)
	}

	// 5. 施加 130 次与 "G" 无关的 op（超过环容量 128），产生淘汰压力
	//    每个 op 各加 1 gold（保持余额非负）
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		for i := 0; i < 130; i++ {
			opID := fmt.Sprintf("op-%d", i)
			p.Currency().Gain(opID, "gold", 1)
			p.Bag().Add(opID, int32(200+i), 1)
		}
	})

	// 6. 断言 "G" 仍在 Currency 和 Bag 的持久快照 ops 中（保护使其存活）
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		curSnap := p.Currency().Snapshot().(CurrencyState)
		bagSnap := p.Bag().Snapshot().(BagState)

		foundInCur := false
		for _, id := range curSnap.Ops {
			if id == "G" {
				foundInCur = true
				break
			}
		}
		foundInBag := false
		for _, id := range bagSnap.Ops {
			if id == "G" {
				foundInBag = true
				break
			}
		}
		if !foundInCur {
			t.Errorf("step6: 'G' 应在 CurrencyState.Ops 中（受保护），实际 ops=%v", curSnap.Ops)
		}
		if !foundInBag {
			t.Errorf("step6: 'G' 应在 BagState.Ops 中（受保护），实际 ops=%v", bagSnap.Ops)
		}
	})

	// 7. 从快照重建 Currency/Bag（模拟重登 loadFrom），验证 "G" 仍在去重环中
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		curSnap := p.Currency().Snapshot().(CurrencyState)
		bagSnap := p.Bag().Snapshot().(BagState)

		cur2 := NewCurrency()
		cur2.Load(&curSnap)

		bag2 := NewBag()
		bag2.Load(&bagSnap)

		// 验证 cur2/bag2 中 "G" 被 seen（重建后去重环含 "G"）
		curSnap2 := cur2.Snapshot().(CurrencyState)
		bagSnap2 := bag2.Snapshot().(BagState)
		foundInCur2 := false
		for _, id := range curSnap2.Ops {
			if id == "G" {
				foundInCur2 = true
				break
			}
		}
		foundInBag2 := false
		for _, id := range bagSnap2.Ops {
			if id == "G" {
				foundInBag2 = true
				break
			}
		}
		if !foundInCur2 {
			t.Errorf("step7: 重建 Currency 后 'G' 应仍在 ops 中，实际=%v", curSnap2.Ops)
		}
		if !foundInBag2 {
			t.Errorf("step7: 重建 Bag 后 'G' 应仍在 ops 中，实际=%v", bagSnap2.Ops)
		}
	})

	// 8. 对同一 Player（消息仍滞留）再次调用 replayOffline；断言不 double-apply
	var balAfterReplay int64
	var cntAfterReplay int32
	replayDone := make(chan struct{})
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		rt.replayOffline(uid, p, func() {
			close(replayDone)
		})
	})
	select {
	case <-replayDone:
	case <-time.After(2 * time.Second):
		t.Fatal("step8: replayOffline 超时")
	}
	runOnLoop(t, rt, func() {
		p := rt.players[uid]
		// 130 op 各加 1 gold → gold = (500-50)+130 = 580；
		// 重放不应再扣 50 或再加 item7
		balAfterReplay = p.Currency().Balance("gold")
		cntAfterReplay = p.Bag().Count(itemID)
	})
	// 经过 130 个 Gain +1，实际余额应为 (500-50)+130=580；不再被扣减
	expectedBal := (initialGold - price) + 130
	if balAfterReplay != expectedBal {
		t.Fatalf("step8: double-apply 检测失败——gold 应为 %d（不再扣减），实得 %d", expectedBal, balAfterReplay)
	}
	if cntAfterReplay != 1 {
		t.Fatalf("step8: double-apply 检测失败——item7 应仍为 1，实得 %d（重复发放）", cntAfterReplay)
	}
}
