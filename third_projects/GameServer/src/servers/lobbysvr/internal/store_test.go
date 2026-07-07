package internal

import (
	"strconv"
	"testing"

	"project/src/common/taskqueue"
)

// fakeStore 同步实现 DocStore（不经真实 Mongo），供 store/runtime 单测复用。
type fakeStore struct {
	docs    map[int64]*PlayerDoc
	flushed map[string]any // "uid:field" → state
}

func newFakeStore() *fakeStore {
	return &fakeStore{docs: map[int64]*PlayerDoc{}, flushed: map[string]any{}}
}

// 编译期断言 fakeStore 满足 DocStore
var _ DocStore = (*fakeStore)(nil)

func (f *fakeStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	doc, ok := f.docs[uid]
	done(doc, ok, nil)
}

func (f *fakeStore) FlushFields(_ taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	for field, state := range fields {
		f.flushed[strconv.FormatInt(uid, 10)+":"+field] = state
	}
	done(nil)
}

// seedPlayerDoc 向 fakeStore 写一份带货币余额的玩家文档（测试钩子）
func seedPlayerDoc(rt *Runtime, uid int64, kind string, amt int64) {
	doc := NewPlayerDoc(uid)
	doc.Currency = CurrencyState{Balances: map[string]int64{kind: amt}}
	rt.store.(*fakeStore).docs[uid] = doc
}

// seedPlayerDocWithOp 写一份「已扣减后」状态 + ops 含某 gameId 的文档（跨路去重测试钩子）
func seedPlayerDocWithOp(rt *Runtime, uid int64, kind string, amt int64, op string) {
	doc := NewPlayerDoc(uid)
	doc.Currency = CurrencyState{Balances: map[string]int64{kind: amt}, Ops: []string{op}}
	rt.store.(*fakeStore).docs[uid] = doc
}

func TestBuildPlayer_FreshDoc(t *testing.T) {
	p := buildPlayer(10001, NewPlayerDoc(10001))
	if p.UID() != 10001 {
		t.Fatalf("uid=%d", p.UID())
	}
	if p.Bag() == nil {
		t.Fatal("bag component missing")
	}
}

func TestBuildPlayer_LoadsBag(t *testing.T) {
	doc := NewPlayerDoc(10001)
	doc.Bag = BagState{Items: map[string]int32{"100": 9}}
	p := buildPlayer(10001, doc)
	if p.Bag().Count(100) != 9 {
		t.Fatalf("bag not loaded: %v", p.Bag().Items())
	}
}

func TestBuildPlayer_LoadsCurrency(t *testing.T) {
	doc := NewPlayerDoc(10001)
	doc.Currency = CurrencyState{Balances: map[string]int64{"gold": 42}}
	p := buildPlayer(10001, doc)
	if p.Currency() == nil || p.Currency().Balance("gold") != 42 {
		t.Fatalf("currency not loaded")
	}
}

func TestBuildPlayer_LoadsFriend(t *testing.T) {
	doc := NewPlayerDoc(10001)
	doc.Friend = FriendState{Friends: []int64{42}}
	p := buildPlayer(10001, doc)
	if p.Friend() == nil || !p.Friend().Has(42) {
		t.Fatal("friend not loaded")
	}
}
