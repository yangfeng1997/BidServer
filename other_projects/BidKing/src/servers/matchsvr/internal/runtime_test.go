package internal

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	matchpb "project/protocal/gen/match"
)

func newTestMatchRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.8.1", MatchSize: 2, MMRWindow: 200, Tick: time.Millisecond})
	rt.Start()
	return rt
}

func matchRunOnLoop(t *testing.T, rt *Runtime, fn func()) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("matchRunOnLoop timeout")
	}
}

// orchestrationSpy 捕获编排调用（mutex 保护；只读取计数与首个 gameId，故不存完整内容）。
type orchestrationSpy struct {
	mu        sync.Mutex
	openGames []string // 调用过的 gameId（按调用序）
	notified  int      // notifyGameStarted 调用次数
}

func (s *orchestrationSpy) recOpen(gameID string) {
	s.mu.Lock()
	s.openGames = append(s.openGames, gameID)
	s.mu.Unlock()
}
func (s *orchestrationSpy) recNotify() { s.mu.Lock(); s.notified++; s.mu.Unlock() }
func (s *orchestrationSpy) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.openGames)
}
func (s *orchestrationSpy) notifyCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.notified }
func (s *orchestrationSpy) openGameID(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.openGames[i]
}

func TestRuntime_FormsTableAndOrchestrates(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "1.7.1", nil }
		rt.notifyGameStarted = func(string, int64, string, string, string) error { spy.recNotify(); return nil }
	})

	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"}) // 重复，去重
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "r2", Mmr: 1100, LobbyNodeId: "1.2.2"})
	})

	waitForSpy(t, func() bool { return spy.openCount() == 1 && spy.notifyCount() == 2 })
	if spy.openGameID(0) != "1.8.1-1" {
		t.Fatalf("gameId want 1.8.1-1, got %s", spy.openGameID(0))
	}
}

func TestRuntime_GameIDUnique(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "1.7.1", nil }
		rt.notifyGameStarted = func(string, int64, string, string, string) error { return nil }
	})
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "b", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 3, ReqId: "c", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 4, ReqId: "d", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	waitForSpy(t, func() bool { return spy.openCount() == 2 })
	if spy.openGameID(0) == spy.openGameID(1) {
		t.Fatalf("gameIds must be unique, both %s", spy.openGameID(0))
	}
}

func TestRuntime_OpenGameFailRequeues(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "", errTest }
		rt.notifyGameStarted = func(string, int64, string, string, string) error { return nil }
	})
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "b", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	waitForSpy(t, func() bool { return spy.openCount() == 1 })
	// 开局失败后两人放回队列。Requeue 在 off-loop goroutine 于 openGame 返回 error 后才 Submit 回主循环，
	// 相对 openCount==1 是异步落定的 —— 必须轮询 queueLen，不能一次性断言（否则 flaky）。
	waitForSpy(t, func() bool {
		got := 0
		matchRunOnLoop(t, rt, func() { got = rt.queueLen() })
		return got == 2
	})
}

func TestRuntime_StopDrainsPromptlyOnSuccess(t *testing.T) {
	rt := newTestMatchRuntime(t)
	// openGame 成功但耗时 ~30ms（模拟在途网络 IO）；notify 立即成功。
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(string, []waiting) (string, error) { time.Sleep(30 * time.Millisecond); return "1.7.1", nil }
		rt.notifyGameStarted = func(string, int64, string, string, string) error { return nil }
	})
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "b", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	// 此刻编排 goroutine 正在 openGame 的 30ms sleep 中（in-flight）。Stop 应在编排完成后立即返回，
	// 而非空等 drainTimeout(5s)。给 2s 上限：修复后 ~tens of ms，未修复则 ~5s。
	done := make(chan struct{})
	go func() { rt.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop() did not return promptly — drain stalled on the success path")
	}
}

func TestRuntime_ReapNotifiesTimeout(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.8.1", MaxWait: 20 * time.Millisecond, Tick: time.Millisecond})
	var timedOut atomic.Int64
	rt.notifyTimeout = func(lobby string, uid int64, reqID string) error { timedOut.Add(1); return nil }
	rt.Start()
	defer rt.Stop()
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	deadline := time.After(2 * time.Second)
	for timedOut.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("expected timeout notify")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

var errTest = errors.New("test open fail")

func waitForSpy(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitForSpy timeout")
}
