package internal

import (
	"errors"
	"testing"

	"project/src/common/taskqueue"
)

// fakeOfflineStore 内存离线消息 store（按行为约定，单测用）。
// failAck=true 时 Ack 返回错误且不删除消息（模拟 $pull 失败 / 消息滞留）。
type fakeOfflineStore struct {
	docs    map[int64][]OfflineMsg
	failAck bool
}

func newFakeOfflineStore() *fakeOfflineStore {
	return &fakeOfflineStore{docs: map[int64][]OfflineMsg{}}
}

func (s *fakeOfflineStore) Push(d taskqueue.Dispatcher, uid int64, m OfflineMsg, done func(error)) {
	s.docs[uid] = append(s.docs[uid], m)
	d.Enqueue(func() { done(nil) })
}
func (s *fakeOfflineStore) Load(d taskqueue.Dispatcher, uid int64, done func([]OfflineMsg, error)) {
	cp := append([]OfflineMsg(nil), s.docs[uid]...)
	d.Enqueue(func() { done(cp, nil) })
}
func (s *fakeOfflineStore) Ack(d taskqueue.Dispatcher, uid int64, opIDs []string, done func(error)) {
	if s.failAck {
		d.Enqueue(func() { done(errors.New("ack failed")) })
		return
	}
	keep := s.docs[uid][:0]
	drop := map[string]bool{}
	for _, id := range opIDs {
		drop[id] = true
	}
	for _, m := range s.docs[uid] {
		if !drop[m.OpID] {
			keep = append(keep, m)
		}
	}
	s.docs[uid] = keep
	d.Enqueue(func() { done(nil) })
}

var _ OfflineStore = (*fakeOfflineStore)(nil)

func TestOfflineMsg_Envelope(t *testing.T) {
	m := OfflineMsg{Type: OfflineMsgSettle, OpID: "g1", Price: 50, Currency: "gold", ItemID: 7}
	if m.Type != "settle" {
		t.Fatalf("settle const mismatch")
	}
}
