package internal

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"project/src/common/taskqueue"
)

// fakeMailStore 同步内存实现 MailStore，供单测复用。
type fakeMailStore struct {
	docs map[primitive.ObjectID]*MailDoc
	seq  int
}

func newFakeMailStore() *fakeMailStore {
	return &fakeMailStore{docs: map[primitive.ObjectID]*MailDoc{}}
}

var _ MailStore = (*fakeMailStore)(nil)

func (f *fakeMailStore) Insert(_ taskqueue.Dispatcher, m *MailDoc, done func(error)) {
	f.seq++
	id := primitive.ObjectID{}
	id[11] = byte(f.seq) // 确定性伪 id
	cp := *m
	cp.ID = id
	f.docs[id] = &cp
	done(nil)
}

func (f *fakeMailStore) List(_ taskqueue.Dispatcher, to int64, _ int64, done func([]MailDoc, error)) {
	var out []MailDoc
	for _, m := range f.docs {
		if m.To == to {
			out = append(out, *m)
		}
	}
	done(out, nil)
}

func (f *fakeMailStore) Claim(_ taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	m, ok := f.docs[id]
	if !ok || m.To != to || m.Claimed {
		done(false, nil, nil)
		return
	}
	m.Claimed = true
	cp := *m
	done(true, &cp, nil)
}

func (f *fakeMailStore) PendingFriendAccepts(_ taskqueue.Dispatcher, to int64, done func([]MailDoc, error)) {
	var out []MailDoc
	for _, m := range f.docs {
		if m.To == to && m.Type == MailTypeFriendAccept && !m.Claimed {
			out = append(out, *m)
		}
	}
	done(out, nil)
}

func (f *fakeMailStore) Get(_ taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	m, ok := f.docs[id]
	if !ok || m.To != to || m.Claimed {
		done(false, nil, nil)
		return
	}
	cp := *m
	done(true, &cp, nil)
}

func (f *fakeMailStore) MarkClaimed(_ taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	if m, ok := f.docs[id]; ok {
		m.Claimed = true
	}
	done(nil)
}

// seedMailWithAttachment 向 fake 插入一封未领取附件邮件，返回其 hex id（测试钩子）
func seedMailWithAttachment(rt *Runtime, to int64, att Attachment) string {
	f := rt.mailStore.(*fakeMailStore)
	var id primitive.ObjectID
	f.Insert(nil, &MailDoc{To: to, From: 0, Type: MailTypeNormal, Attachments: []Attachment{att}}, func(error) {})
	for k, m := range f.docs {
		if m.To == to && !m.Claimed && len(m.Attachments) == 1 && m.Attachments[0] == att {
			id = k
		}
	}
	return id.Hex()
}

// seedMailWithAttachments 向 fake 插入一封含多个附件的未领取邮件，返回其 hex id（测试钩子）
func seedMailWithAttachments(rt *Runtime, to int64, atts ...Attachment) string {
	f := rt.mailStore.(*fakeMailStore)
	f.Insert(nil, &MailDoc{To: to, From: 0, Type: MailTypeNormal, Attachments: atts}, func(error) {})
	var id primitive.ObjectID
	for k, m := range f.docs {
		if m.To == to && !m.Claimed && len(m.Attachments) == len(atts) {
			id = k
		}
	}
	return id.Hex()
}

// forceMailUnclaimed 把某邮件置回未领取（模拟「grant 已落但 mark 前崩溃」，测试钩子）
func forceMailUnclaimed(rt *Runtime, hexID string) {
	id, _ := primitive.ObjectIDFromHex(hexID)
	if m, ok := rt.mailStore.(*fakeMailStore).docs[id]; ok {
		m.Claimed = false
	}
}

func TestMailbox_InsertListClaim_Contract(t *testing.T) {
	s := newFakeMailStore()
	var id primitive.ObjectID
	s.Insert(nil, &MailDoc{To: 2, From: 1, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "item", ID: 555, Count: 3}}}, func(error) {})
	s.List(nil, 2, 50, func(ms []MailDoc, _ error) {
		if len(ms) != 1 {
			t.Fatalf("list len=%d", len(ms))
		}
		id = ms[0].ID
	})
	// 首次 claim 成功，附件可见
	s.Claim(nil, id, 2, func(ok bool, m *MailDoc, _ error) {
		if !ok || len(m.Attachments) != 1 || m.Attachments[0].ID != 555 {
			t.Fatalf("claim1: %v %+v", ok, m)
		}
	})
	// 重复 claim 失败（幂等边界）
	s.Claim(nil, id, 2, func(ok bool, _ *MailDoc, _ error) {
		if ok {
			t.Fatal("dup claim must fail")
		}
	})
}
