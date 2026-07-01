package rpc

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"project/internal/core/errcode"
)

// fakeTransport 是内存内的 Transport 实现，用于测试
type fakeTransport struct {
	mu    sync.Mutex
	calls []fakeSend
}

type fakeSend struct {
	target Target
	header Header
	body   []byte
}

func (t *fakeTransport) SendFrame(target Target, header Header, body []byte) error {
	t.mu.Lock()
	t.calls = append(t.calls, fakeSend{target: target, header: header, body: body})
	t.mu.Unlock()
	return nil
}

func (t *fakeTransport) callsSnapshot() []fakeSend {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]fakeSend, len(t.calls))
	copy(out, t.calls)
	return out
}

type syncPoster struct {
	fns []func()
	mu  sync.Mutex
}

func (p *syncPoster) Post(fn func()) {
	p.mu.Lock()
	p.fns = append(p.fns, fn)
	p.mu.Unlock()
}

func (p *syncPoster) drain() {
	for {
		p.mu.Lock()
		fns := p.fns
		p.fns = nil
		p.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

func TestCoreCallAndOnResponse(t *testing.T) {
	trans := &fakeTransport{}
	p := &syncPoster{}
	core := New(trans, WithPoster(p))

	var gotPayload []byte
	var gotCode errcode.ErrCode
	var cbCalled atomic.Bool

	core.Call(Target{ServerType: 2}, "Test/Hello", []byte("req"), Background(),
		func(payload []byte, code errcode.ErrCode) {
			gotPayload = payload
			gotCode = code
			cbCalled.Store(true)
		})

	// transport 应该被调用了一次
	calls := trans.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SendFrame call, got %d", len(calls))
	}
	c := calls[0]
	if c.header.Route != "Test/Hello" {
		t.Errorf("route=%q, want Test/Hello", c.header.Route)
	}
	if c.header.SeqID != 1 {
		t.Errorf("seqID=%d, want 1", c.header.SeqID)
	}
	if string(c.body) != "req" {
		t.Errorf("body=%q, want req", c.body)
	}

	// 模拟回包
	core.OnResponse(c.header.SeqID, []byte("rsp"), errcode.OK)

	// drain poster
	p.drain()

	if !cbCalled.Load() {
		t.Fatal("callback was not called")
	}
	if string(gotPayload) != "rsp" {
		t.Errorf("payload=%q, want rsp", gotPayload)
	}
	if gotCode != errcode.OK {
		t.Errorf("code=%d, want OK", gotCode)
	}

	// pending 已清理
	if core.PendingLen() != 0 {
		t.Errorf("pending len=%d, want 0", core.PendingLen())
	}
}

func TestCoreSend(t *testing.T) {
	trans := &fakeTransport{}
	core := New(trans)

	core.Send(Target{ServerType: 3, Mode: RoutingDirect, NodeID: 42}, "Test/Ping", []byte("ping"), Background())

	calls := trans.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SendFrame call, got %d", len(calls))
	}
	c := calls[0]
	if c.header.SeqID != 0 {
		t.Errorf("Send should have seqID=0, got %d", c.header.SeqID)
	}
	if c.header.Route != "Test/Ping" {
		t.Errorf("route=%q, want Test/Ping", c.header.Route)
	}
	// Send 不应登记 pending
	if core.PendingLen() != 0 {
		t.Errorf("pending len after Send=%d, want 0", core.PendingLen())
	}
}

func TestCoreTimeout(t *testing.T) {
	trans := &fakeTransport{}
	p := &syncPoster{}
	core := New(trans, WithPoster(p), WithDefaultTimeout(10*time.Millisecond))

	var timedOut atomic.Bool

	core.Call(Target{ServerType: 2}, "Test/Slow", []byte("req"), Background(),
		func(payload []byte, code errcode.ErrCode) {
			if code == errcode.ERR_TIMEOUT {
				timedOut.Store(true)
			}
		})

	// 等待超时触发
	time.Sleep(50 * time.Millisecond)
	p.drain()

	if !timedOut.Load() {
		t.Error("expected timeout but callback was not called with ERR_TIMEOUT")
	}
	if core.PendingLen() != 0 {
		t.Errorf("pending len after timeout=%d, want 0", core.PendingLen())
	}
}

func TestCoreSeqIncrement(t *testing.T) {
	trans := &fakeTransport{}
	core := New(trans)

	var seqs []uint64
	for i := 0; i < 10; i++ {
		core.Call(Target{ServerType: 1}, "Test/N", []byte("x"), Background(),
			func([]byte, errcode.ErrCode) {})
	}
	calls := trans.callsSnapshot()
	if len(calls) != 10 {
		t.Fatalf("expected 10 calls, got %d", len(calls))
	}
	for _, c := range calls {
		seqs = append(seqs, c.header.SeqID)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq not monotonically increasing: seqs[%d]=%d, seqs[%d]=%d", i-1, seqs[i-1], i, seqs[i])
		}
	}
}

func TestCoreNilCallback(t *testing.T) {
	trans := &fakeTransport{}
	core := New(trans)

	// Call with nil callback should not register pending
	core.Call(Target{ServerType: 2}, "Test/X", []byte("x"), Background(), nil)
	if core.PendingLen() != 0 {
		t.Errorf("nil callback should not register pending, got %d", core.PendingLen())
	}
}

func TestCoreReplyAfterTimeout(t *testing.T) {
	trans := &fakeTransport{}
	p := &syncPoster{}
	core := New(trans, WithPoster(p), WithDefaultTimeout(10*time.Millisecond))

	callCount := atomic.Int32{}
	core.Call(Target{ServerType: 2}, "Test/Late", []byte("x"), Background(),
		func([]byte, errcode.ErrCode) { callCount.Add(1) })

	time.Sleep(50 * time.Millisecond)
	p.drain()

	// 迟到回包
	core.OnResponse(1, []byte("late"), errcode.OK)
	p.drain()

	if callCount.Load() != 1 {
		t.Errorf("callback should be called exactly once, got %d", callCount.Load())
	}
}

func TestCoreMultipleConcurrentCalls(t *testing.T) {
	trans := &fakeTransport{}
	p := &syncPoster{}
	core := New(trans, WithPoster(p))

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			core.Call(Target{ServerType: uint32(idx%5) + 1}, "Test/C", []byte("x"), Background(),
				func([]byte, errcode.ErrCode) {})
		}(i)
	}
	wg.Wait()

	if core.PendingLen() != n {
		t.Errorf("pending len=%d, want %d", core.PendingLen(), n)
	}

	// 逐个回包
	calls := trans.callsSnapshot()
	for _, c := range calls {
		core.OnResponse(c.header.SeqID, []byte("ok"), errcode.OK)
	}
	p.drain()

	if core.PendingLen() != 0 {
		t.Errorf("pending not empty after all replies: %d", core.PendingLen())
	}
}

func TestCoreDefault(t *testing.T) {
	core := New(&fakeTransport{})
	Init(core)
	if Default() != core {
		t.Error("Default() should return the initialized core")
	}
	Default()
	_ = MustDefault()
}

func TestCoreMustDefaultPanic(t *testing.T) {
	// 保存旧值并在测试后恢复
	old := Default()
	defaultCore.Store(nil)

	defer func() {
		_ = recover()
		defaultCore.Store(old)
	}()

	MustDefault()
	t.Error("MustDefault should panic when not initialized")
}

func TestReplyType(t *testing.T) {
	var called bool
	reply := Reply[int](func(v int, err error) {
		called = true
		if v != 42 {
			t.Errorf("reply value=%d, want 42", v)
		}
		if err != nil {
			t.Errorf("reply err=%v, want nil", err)
		}
	})
	reply(42, nil)
	if !called {
		t.Error("reply was not called")
	}
}

func TestReplyError(t *testing.T) {
	var called bool
	reply := Reply[string](func(v string, err error) {
		called = true
		if v != "" {
			t.Errorf("reply value=%q, want empty", v)
		}
		if err == nil {
			t.Error("expected non-nil error")
		}
	})
	reply("", errors.New("test error"))
	if !called {
		t.Error("reply was not called")
	}
}

func TestTargetAt(t *testing.T) {
	target := Target{ServerType: 2, Mode: RoutingAny}
	direct := target.At(42)
	if direct.Mode != RoutingDirect {
		t.Errorf("mode=%d, want RoutingDirect", direct.Mode)
	}
	if direct.NodeID != 42 {
		t.Errorf("nodeID=%d, want 42", direct.NodeID)
	}
	// 原 target 不应被修改
	if target.Mode != RoutingAny {
		t.Error("original target was mutated")
	}
}

func TestTargetByHash(t *testing.T) {
	target := Target{ServerType: 3}
	hash := target.ByHash("player_1")
	if hash.Mode != RoutingConsistentHash {
		t.Errorf("mode=%d, want RoutingConsistentHash", hash.Mode)
	}
	if hash.Key != "player_1" {
		t.Errorf("key=%q, want player_1", hash.Key)
	}
}

func TestTargetBroadcast(t *testing.T) {
	target := Target{ServerType: 2}
	bc := target.Broadcast()
	if bc.Mode != RoutingBroadcast {
		t.Errorf("mode=%d, want RoutingBroadcast", bc.Mode)
	}
}

func TestTargetTimeout(t *testing.T) {
	target := Target{ServerType: 2}
	withTimeout := target.Timeout(5 * time.Second)
	if withTimeout.Deadline != 5*time.Second {
		t.Errorf("deadline=%v, want 5s", withTimeout.Deadline)
	}
}

func TestCtxBackground(t *testing.T) {
	ctx := Background()
	if ctx.Remaining() > 0 {
		t.Error("Background ctx should have no deadline")
	}
	if ctx.FromNodeID() != 0 {
		t.Error("Background ctx fromNode should be 0")
	}
}

func TestCtxWithDeadline(t *testing.T) {
	ctx := Background().WithDeadline(100 * time.Millisecond)
	rem := ctx.Remaining()
	if rem <= 0 || rem > 150*time.Millisecond {
		t.Errorf("remaining=%v, expected ~100ms", rem)
	}
}

func TestCtxWithFromNode(t *testing.T) {
	ctx := Background().WithFromNode(12345)
	if ctx.FromNodeID() != 12345 {
		t.Errorf("fromNodeID=%d, want 12345", ctx.FromNodeID())
	}
}

func TestCtxStaleGuard(t *testing.T) {
	stale := false
	ctx := Background().WithStaleGuard(func() bool { return stale })
	if ctx.Stale() {
		t.Error("Stale() should return false initially")
	}
	stale = true
	if !ctx.Stale() {
		t.Error("Stale() should return true after flag set")
	}
}

func TestCtxClientMeta(t *testing.T) {
	type meta struct{ UID int64 }
	ctx := Background().WithClientMeta(&meta{UID: 100})
	m := ctx.ClientMeta()
	if m == nil {
		t.Fatal("ClientMeta returned nil")
	}
	if m.(*meta).UID != 100 {
		t.Errorf("UID=%d, want 100", m.(*meta).UID)
	}
}

func TestCtxSpan(t *testing.T) {
	ctx := Background()
	sp := ctx.Span()
	if sp == nil {
		t.Error("Span() should return nopSpan for background ctx")
	}
	sp.Child("test").Finish()
	sp.Finish()
}

func TestTransportInterface(t *testing.T) {
	// 验证 fakeTransport 实现了 Transport
	var _ Transport = (*fakeTransport)(nil)
}

func TestPosterInterface(t *testing.T) {
	// 验证 syncPoster 实现了 Poster
	var _ Poster = (*syncPoster)(nil)
}
