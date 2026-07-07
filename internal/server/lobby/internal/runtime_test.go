package internal

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"project/pkg/taskqueue"
)

func TestOpDedupProtectsOldEntry(t *testing.T) {
	op := newOpDedup(2)
	op.protect("a")
	op.remember("a")
	op.remember("b")
	op.remember("c")
	if !op.seen("a") {
		t.Fatalf("protected op was evicted")
	}
	if op.seen("b") {
		t.Fatalf("old unprotected op should be evicted")
	}
	op.unprotect("a")
	op.remember("d")
	if op.seen("a") {
		t.Fatalf("unprotected old op should be evicted")
	}
}

func TestBagAndCurrencyAreIdempotent(t *testing.T) {
	bag := NewBag()
	if got := bag.Add("op-1", 1001, 2); got != 2 {
		t.Fatalf("bag add got %d", got)
	}
	if got := bag.Add("op-1", 1001, 2); got != 2 {
		t.Fatalf("duplicate bag add got %d", got)
	}
	if !bag.Dirty() {
		t.Fatalf("bag should be dirty")
	}

	cur := NewCurrency()
	if balance, changed := cur.Gain("op-2", "gold", 10); balance != 10 || !changed {
		t.Fatalf("currency gain balance=%d changed=%v", balance, changed)
	}
	if balance, changed := cur.Gain("op-2", "gold", 10); balance != 10 || changed {
		t.Fatalf("duplicate gain balance=%d changed=%v", balance, changed)
	}
	if balance, ok := cur.Spend("op-3", "gold", 4); balance != 6 || !ok {
		t.Fatalf("currency spend balance=%d ok=%v", balance, ok)
	}
	if balance, ok := cur.Spend("op-4", "gold", 7); balance != 6 || ok {
		t.Fatalf("overspend balance=%d ok=%v", balance, ok)
	}
}

func TestBuildPlayerRestoresComponents(t *testing.T) {
	doc := NewPlayerDoc(42)
	doc.Bag.Items["1001"] = 3
	doc.Currency.Balances["gold"] = 99
	doc.Friend.Friends = []int64{7, 8}
	doc.Rating.MMR = 1200

	p := BuildPlayerForTest(42, doc)
	if p.UID() != 42 || p.Bag().Count(1001) != 3 {
		t.Fatalf("player/bag not restored")
	}
	if got := p.Currency().Balance("gold"); got != 99 {
		t.Fatalf("currency got %d", got)
	}
	if !p.Friend().Has(7) || !p.Friend().Has(8) {
		t.Fatalf("friends not restored")
	}
	if got := p.Rating().MMR(); got != 1200 {
		t.Fatalf("rating got %d", got)
	}
}

func TestRuntimeLoginLoadsPlayerAndPublishesEvent(t *testing.T) {
	store := newFakeDocStore()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.2.3", Store: store, Tick: time.Millisecond, FlushInterval: time.Hour})

	loaded := make(chan int64, 1)
	rt.Events().PlayerLoaded.Subscribe(func(e PlayerLoaded) { loaded <- e.UID })

	var result LoginResult
	var err error
	rt.Login(10001, "0.1.1", func(r LoginResult, e error) {
		result = r
		err = e
	})

	if err != nil {
		t.Fatalf("login error: %v", err)
	}
	if result.UID != 10001 || result.LobbyNodeID != "1.2.3" {
		t.Fatalf("unexpected login result: %+v", result)
	}
	select {
	case uid := <-loaded:
		if uid != 10001 {
			t.Fatalf("loaded uid=%d", uid)
		}
	default:
		t.Fatalf("missing PlayerLoaded event")
	}
	if rt.Player(10001) == nil {
		t.Fatalf("player not cached")
	}
}

func TestRuntimeFlushSoonCoalescesDirtyFields(t *testing.T) {
	store := newFakeDocStore()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.2.3", Store: store, Tick: time.Millisecond, FlushInterval: time.Hour})
	rt.Login(10002, "0.1.1", func(LoginResult, error) {})

	p := rt.Player(10002)
	p.Bag().Add("op-bag", 1001, 2)
	p.Currency().Gain("op-cur", "gold", 5)
	rt.FlushSoon(10002)
	rt.FlushSoon(10002)
	rt.coalesceFlush()

	fields := store.lastFlush(10002)
	if _, ok := fields[BagField]; !ok {
		t.Fatalf("missing bag flush: %#v", fields)
	}
	if _, ok := fields[CurrencyField]; !ok {
		t.Fatalf("missing currency flush: %#v", fields)
	}
	if store.flushCount(10002) != 1 {
		t.Fatalf("flush count=%d", store.flushCount(10002))
	}
	if p.Bag().Dirty() || p.Currency().Dirty() {
		t.Fatalf("dirty flags were not cleared")
	}
}

func TestRuntimeFlushFailureMarksDirty(t *testing.T) {
	store := newFakeDocStore()
	store.flushErr = errors.New("boom")
	rt := NewRuntime(RuntimeConfig{NodeID: "1.2.3", Store: store, Tick: time.Millisecond, FlushInterval: time.Hour})
	rt.Login(10003, "0.1.1", func(LoginResult, error) {})

	p := rt.Player(10003)
	p.Currency().Gain("op-cur", "gold", 5)
	rt.FlushSoon(10003)
	rt.coalesceFlush()
	if !p.Currency().Dirty() {
		t.Fatalf("currency should be dirty after failed flush")
	}
}

func TestRuntimeDisconnectCallsUnregister(t *testing.T) {
	store := newFakeDocStore()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.2.3", Store: store, Tick: time.Millisecond, FlushInterval: time.Hour})
	rt.Login(10004, "0.1.1", func(LoginResult, error) {})

	var unregistered int64
	rt.onlineUnregister = func(uid int64) { unregistered = uid }
	rt.Disconnect(10004)
	if unregistered != 10004 {
		t.Fatalf("unregistered uid=%d", unregistered)
	}
	if rt.Player(10004) != nil {
		t.Fatalf("player should be removed after clean disconnect")
	}
}

type fakeDocStore struct {
	mu       sync.Mutex
	docs     map[int64]*PlayerDoc
	flushes  map[int64][]map[string]any
	flushErr error
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{docs: make(map[int64]*PlayerDoc), flushes: make(map[int64][]map[string]any)}
}

func (s *fakeDocStore) Load(_ taskqueue.TaskQueue, uid int64, done func(*PlayerDoc, bool, error)) {
	s.mu.Lock()
	doc, ok := s.docs[uid]
	s.mu.Unlock()
	if !ok {
		done(nil, false, nil)
		return
	}
	cp := *doc
	done(&cp, true, nil)
}

func (s *fakeDocStore) FlushFields(_ taskqueue.TaskQueue, uid int64, fields map[string]any, done func(error)) {
	s.mu.Lock()
	cp := make(map[string]any, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	s.flushes[uid] = append(s.flushes[uid], cp)
	err := s.flushErr
	s.mu.Unlock()
	done(err)
}

func (s *fakeDocStore) lastFlush(uid int64) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.flushes[uid]
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func (s *fakeDocStore) flushCount(uid int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.flushes[uid])
}

func TestFriendSnapshotIsValueCopy(t *testing.T) {
	f := NewFriend()
	f.Add(1)
	snap := f.Snapshot().(FriendState)
	if !reflect.DeepEqual(snap.Friends, []int64{1}) {
		t.Fatalf("snapshot=%v", snap.Friends)
	}
	snap.Friends[0] = 2
	if !f.Has(1) || f.Has(2) {
		t.Fatalf("snapshot should not mutate component")
	}
}
