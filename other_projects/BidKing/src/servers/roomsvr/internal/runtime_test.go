package internal

import (
	"sync"
	"testing"
	"time"
)

func newTestRoomRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	return rt
}

func roomRunOnLoop(t *testing.T, rt *Runtime, fn func()) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("roomRunOnLoop timeout")
	}
}

func TestRuntime_OpenGameIdempotent(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	parts := []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}}
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("1.8.1-1", 5, 30, "gold", parts)
		rt.OpenGame("1.8.1-1", 9, 99, "gold", parts) // 同 gameId 幂等：不覆盖
	})
	roomRunOnLoop(t, rt, func() {
		g := rt.Game("1.8.1-1")
		if g == nil || g.ItemID != 5 || g.CountdownSec != 30 {
			t.Fatalf("idempotent open should keep first game, got %+v", g)
		}
	})
}

func TestRuntime_MultiGameIsolation(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 1, 30, "gold", []Participant{{UID: 1}})
		rt.OpenGame("g2", 2, 30, "gold", []Participant{{UID: 2}})
	})
	roomRunOnLoop(t, rt, func() {
		if rt.Game("g1").ItemID != 1 || rt.Game("g2").ItemID != 2 {
			t.Fatalf("games not isolated")
		}
	})
}

func TestRuntime_Bid(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 1, 30, "gold", []Participant{{UID: 1}, {UID: 2}})
	})
	roomRunOnLoop(t, rt, func() {
		if code, hb, _ := rt.Bid("g1", 1, 100); code != 0 || hb != 100 {
			t.Fatalf("first bid should accept, code=%d hb=%d", code, hb)
		}
		if code, _, _ := rt.Bid("g1", 2, 50); code != 3 {
			t.Fatalf("lower bid should be rejected code=3, got %d", code)
		}
		if code, hb, hbr := rt.Bid("g1", 2, 150); code != 0 || hb != 150 || hbr != 2 {
			t.Fatalf("higher bid should accept, code=%d hb=%d hbr=%d", code, hb, hbr)
		}
		if code, _, _ := rt.Bid("g1", 99, 999); code != 2 {
			t.Fatalf("non-participant should be code=2, got %d", code)
		}
		if code, _, _ := rt.Bid("missing", 1, 10); code != 2 {
			t.Fatalf("missing game should be code=2, got %d", code)
		}
	})
}

func TestRuntime_OpenGameCarriesCurrency(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 30, "gold", []Participant{{UID: 1}, {UID: 2}})
	})
	roomRunOnLoop(t, rt, func() {
		g := rt.Game("g1")
		if g.Currency != "gold" || g.HighestBid != 0 || g.HighestBidder != 0 || g.closed {
			t.Fatalf("game initial state wrong: %+v", g)
		}
		if !g.isParticipant(1) || g.isParticipant(99) {
			t.Fatalf("isParticipant wrong")
		}
	})
}

type settleRec struct{ uid, winner, price int64 }
type roomHookRec struct {
	mu      sync.Mutex
	bcasts  [][2]int64
	settles []settleRec
}

func newTestRoomRuntimeWithHooks(t *testing.T) (*Runtime, *roomHookRec) {
	t.Helper()
	rec := &roomHookRec{}
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.broadcast = func(lobby string, uid int64, game string, hb, hbr int64, rem int32) {
		rec.mu.Lock()
		rec.bcasts = append(rec.bcasts, [2]int64{uid, hb})
		rec.mu.Unlock()
	}
	rt.notifySettle = func(lobby string, uid, winner, price int64, game string, item int32, cur string) error {
		rec.mu.Lock()
		rec.settles = append(rec.settles, settleRec{uid, winner, price})
		rec.mu.Unlock()
		return nil
	}
	rt.Start()
	return rt, rec
}

func TestRuntime_SettleDeterminesWinner(t *testing.T) {
	rt, rec := newTestRoomRuntimeWithHooks(t)
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 1, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.2"}})
		rt.Bid("g1", 1, 100)
		rt.Bid("g1", 2, 150)
		rt.settle("g1") // 直接触发（不等 timer）
	})
	time.Sleep(100 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.settles) != 2 {
		t.Fatalf("settle should notify both participants, got %d", len(rec.settles))
	}
	for _, s := range rec.settles {
		if s.winner != 2 || s.price != 150 {
			t.Fatalf("winner/price wrong: %+v", s)
		}
	}
	roomRunOnLoop(t, rt, func() {
		if !rt.Game("g1").closed {
			t.Fatalf("game should be closed after settle")
		}
	})
}

func TestRuntime_Rejoin(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 30, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}})
		rt.Bid("g1", 1, 100)
	})
	// 接回：改投 uid=1 到新 lobby 1.2.9 + 回当前快照
	var code int32
	var hb, hbr int64
	var rem, itemID int32
	var currency string
	var newLobby string
	roomRunOnLoop(t, rt, func() {
		code, hb, hbr, rem, itemID, currency = rt.Rejoin("g1", 1, "1.2.9")
		newLobby = rt.Game("g1").Participants[0].LobbyNodeID
	})
	if code != 0 || hb != 100 || hbr != 1 || itemID != 7 || currency != "gold" {
		t.Fatalf("rejoin alive snapshot mismatch: code=%d hb=%d hbr=%d item=%d cur=%s rem=%d", code, hb, hbr, itemID, currency, rem)
	}
	if newLobby != "1.2.9" {
		t.Fatalf("rejoin must re-route participant lobbyNode to 1.2.9, got %q", newLobby)
	}
	// 局不存在 → code 2；非参与者 → code 2
	roomRunOnLoop(t, rt, func() {
		if c, _, _, _, _, _ := rt.Rejoin("nope", 1, "1.2.9"); c != 2 {
			t.Fatalf("absent game should be code 2, got %d", c)
		}
		if c, _, _, _, _, _ := rt.Rejoin("g1", 999, "1.2.9"); c != 2 {
			t.Fatalf("non-participant should be code 2, got %d", c)
		}
	})
}

func TestRuntime_RejoinClosed(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 7, 30, "gold", []Participant{{UID: 1, LobbyNodeID: "1.2.1"}})
		rt.Game("g1").closed = true // 模拟已封盘
	})
	roomRunOnLoop(t, rt, func() {
		if c, _, _, _, _, _ := rt.Rejoin("g1", 1, "1.2.9"); c != 1 {
			t.Fatalf("closed game rejoin should be code 1, got %d", c)
		}
	})
}
