package internal

import (
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
)

// fakePresence 记录 Query/Push 调用，注入 Runtime 替换真实 router/gate 出站。
// Push 可从 off-loop goroutine 调用，所有写字段均由 mu 保护，供测试 goroutine 安全读取。
type fakePresence struct {
	online map[int64]string // uid → gatewayNodeID（在线）；仅主循环或测试前写

	mu     sync.Mutex     // 保护 pushes 及快捷访问字段
	pushes []presencePush // 所有 Push 调用记录（顺序）
}
type presencePush struct {
	gateway string
	uid     int64
	msgID   uint32
	body    []byte
}

// newFakePresence 构造空 fakePresence。
func newFakePresence() *fakePresence {
	return &fakePresence{online: make(map[int64]string)}
}

func (f *fakePresence) Query(uid int64) (gatewayNodeID string, online bool) {
	gw, ok := f.online[uid]
	return gw, ok
}

func (f *fakePresence) Push(gatewayNodeID string, uid int64, msgID uint32, body []byte) {
	f.mu.Lock()
	f.pushes = append(f.pushes, presencePush{gatewayNodeID, uid, msgID, body})
	f.mu.Unlock()
}

// Pushes 返回所有 Push 调用记录的副本（线程安全）。
func (f *fakePresence) Pushes() []presencePush {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]presencePush, len(f.pushes))
	copy(cp, f.pushes)
	return cp
}

// LastPushUID 返回最近一次 Push 调用的 uid（线程安全）。
func (f *fakePresence) LastPushUID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pushes) == 0 {
		return 0
	}
	return f.pushes[len(f.pushes)-1].uid
}

// LastPushMsgID 返回最近一次 Push 调用的 msgID（线程安全）。
func (f *fakePresence) LastPushMsgID() uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pushes) == 0 {
		return 0
	}
	return f.pushes[len(f.pushes)-1].msgID
}

// LastPushBody 返回最近一次 Push 调用的 body 副本（线程安全）。
func (f *fakePresence) LastPushBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pushes) == 0 {
		return nil
	}
	src := f.pushes[len(f.pushes)-1].body
	b := make([]byte, len(src))
	copy(b, src)
	return b
}

func TestFanoutPresence_OnlyOnlineFriends(t *testing.T) {
	fp := newFakePresence()
	fp.online[2] = "0.1.1" // 2 在线，3 离线
	fanoutPresence(fp, 1, []int64{2, 3}, true)
	pushes := fp.Pushes()
	if len(pushes) != 1 || pushes[0].uid != 2 || pushes[0].msgID != msgIDSCFriendPresence {
		t.Fatalf("pushes: %+v", pushes)
	}
}

func TestNotifyNewMail_OnlineRecipientPushed(t *testing.T) {
	fp := newFakePresence()
	fp.online[7] = "0.1.1" // 7 在线
	notifyNewMail(fp, 7, 99, MailTypeFriendReq)
	pushes := fp.Pushes()
	if len(pushes) != 1 || pushes[0].uid != 7 || pushes[0].msgID != msgIDSCMailNew {
		t.Fatalf("online push: %+v", pushes)
	}
}

func TestNotifyNewMail_OfflineRecipientNoPush(t *testing.T) {
	fp := newFakePresence() // 7 离线
	notifyNewMail(fp, 7, 99, MailTypeNormal)
	pushes := fp.Pushes()
	if len(pushes) != 0 {
		t.Fatalf("offline must not push: %+v", pushes)
	}
}

func TestSnapshotFriends_MarksOnline(t *testing.T) {
	fp := newFakePresence()
	fp.online[2] = "0.1.1"
	rsp := snapshotFriends(fp, []int64{2, 3})
	if len(rsp.Friends) != 2 {
		t.Fatalf("friends: %+v", rsp.Friends)
	}
	var got2, got3 bool
	for _, f := range rsp.Friends {
		if f.Uid == 2 {
			got2 = f.Online
		}
		if f.Uid == 3 {
			got3 = f.Online
		}
	}
	if !got2 || got3 {
		t.Fatalf("online flags wrong: %+v", rsp.Friends)
	}
}

func TestFanoutPresence_BodyIsProtoEncoded(t *testing.T) {
	fp := newFakePresence()
	fp.online[2] = "0.1.1"
	fanoutPresence(fp, 1, []int64{2}, true)
	pushes := fp.Pushes()
	if len(pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(pushes))
	}
	var sc lobbypb.SC_FriendPresence
	if err := proto.Unmarshal(pushes[0].body, &sc); err != nil {
		t.Fatalf("body must be proto SC_FriendPresence, got unmarshal err: %v", err)
	}
	if sc.Uid != 1 || !sc.Online {
		t.Fatalf("decoded SC_FriendPresence wrong: uid=%d online=%v", sc.Uid, sc.Online)
	}
}

func TestNotifyNewMail_BodyIsProtoEncoded(t *testing.T) {
	fp := newFakePresence()
	fp.online[7] = "0.1.1"
	notifyNewMail(fp, 7, 99, MailTypeFriendReq)
	pushes := fp.Pushes()
	if len(pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(pushes))
	}
	var sc lobbypb.SC_MailNew
	if err := proto.Unmarshal(pushes[0].body, &sc); err != nil {
		t.Fatalf("body must be proto SC_MailNew, got unmarshal err: %v", err)
	}
	if sc.From != 99 || sc.Type != MailTypeFriendReq {
		t.Fatalf("decoded SC_MailNew wrong: from=%d type=%s", sc.From, sc.Type)
	}
}

func TestPushMatchFound(t *testing.T) {
	fp := newFakePresence()
	fp.online[7] = "1.1.1"
	pushMatchFound(fp, 7, "1.7.1", "1.8.1-1") // 同步调用，单 goroutine
	if fp.LastPushUID() != 7 || fp.LastPushMsgID() != msgIDSCMatchFound {
		t.Fatalf("want push uid=7 msgID=%d, got uid=%d msgID=%d", msgIDSCMatchFound, fp.LastPushUID(), fp.LastPushMsgID())
	}
	var sc lobbypb.SC_MatchFound
	if err := clientSerializer.Unmarshal(fp.LastPushBody(), &sc); err != nil {
		t.Fatalf("unmarshal pushed body: %v", err)
	}
	if sc.RoomNodeId != "1.7.1" || sc.GameId != "1.8.1-1" {
		t.Fatalf("pushed SC_MatchFound mismatch: %+v", &sc)
	}
}

func TestPushReconnectAuction(t *testing.T) {
	fp := newFakePresence()
	fp.online[10001] = "1.1.1"
	pushReconnectAuction(fp, 10001, "g1", 120, 7, 25, 9, "gold", 0)
	pushes := fp.Pushes()
	if len(pushes) != 1 || pushes[0].msgID != msgIDSCReconnectAuction || pushes[0].uid != 10001 {
		t.Fatalf("want one SC_ReconnectAuction push to 10001, got %+v", pushes)
	}
	var sc lobbypb.SC_ReconnectAuction
	if err := proto.Unmarshal(pushes[0].body, &sc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sc.GameId != "g1" || sc.HighestBid != 120 || sc.ItemId != 9 || sc.Currency != "gold" || sc.Status != 0 {
		t.Fatalf("reconnect snapshot body mismatch: %+v", &sc)
	}
	// 离线玩家：不推
	pushReconnectAuction(fp, 20002, "g2", 0, 0, 0, 0, "", 1)
	if len(fp.Pushes()) != 1 {
		t.Fatalf("offline player should not be pushed")
	}
}
