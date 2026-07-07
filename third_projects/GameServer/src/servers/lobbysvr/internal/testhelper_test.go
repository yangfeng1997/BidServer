// src/servers/lobbysvr/internal/testhelper_test.go
package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
)

// --- fake Replier：捕获主循环异步回包 ---
type replyResult struct {
	data []byte
	err  error
}
type fakeReplier struct{ ch chan replyResult }

func newFakeReplier() *fakeReplier                  { return &fakeReplier{ch: make(chan replyResult, 1)} }
func (r *fakeReplier) Reply(data []byte, err error) { r.ch <- replyResult{data, err} }
func (r *fakeReplier) wait(t *testing.T) replyResult {
	t.Helper()
	select {
	case rr := <-r.ch:
		return rr
	case <-time.After(2 * time.Second):
		t.Fatal("reply timeout")
		return replyResult{}
	}
}

// newTestRuntime 构造带 fakeStore 的 Runtime 并 Start；online register/unregister 替换为 no-op。
// （MailStore / onlineTouch 字段在后续任务引入后再回填本 helper。）
func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(RuntimeConfig{
		NodeID: "0.3.1", Store: newFakeStore(), MailStore: newFakeMailStore(),
		Tick: 10 * time.Millisecond, FlushInterval: time.Hour,
	})
	rt.onlineRegister = func(int64, string) {}
	rt.onlineUnregister = func(int64) {}
	rt.onlineTouch = func(int64) {}
	rt.Start()
	return rt
}

// runOnLoop 把 fn 投递到主循环并等待其执行完成（也用作「barrier」：等此前所有 loop 工作排空）。
func runOnLoop(t *testing.T, rt *Runtime, fn func()) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runOnLoop timeout")
	}
}

// loadPlayerSync 驱动 uid 登录加载，并 barrier 等待登录 continuation 排空。
func loadPlayerSync(t *testing.T, rt *Runtime, uid int64) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() {
		rt.Login(uid, "0.2.1", func(*lobbypb.RPC_Login_Rsp, error) { close(done) })
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("login timeout")
	}
	runOnLoop(t, rt, func() {}) // barrier：确保 reply 之后的 scan/fan-out 也排空
}

// disconnectSync 驱动断连（flush + 剔除，fakeStore 同步）
func disconnectSync(t *testing.T, rt *Runtime, uid int64) {
	runOnLoop(t, rt, func() { rt.Disconnect(uid) })
}

// purchaseSync 驱动 Purchase 编排并同步取回 SC_Purchase
func purchaseSync(t *testing.T, rt *Runtime, uid int64, opID, kind string, price int64, itemID int32) *lobbypb.SC_Purchase {
	return driveReq[*lobbypb.SC_Purchase](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Purchase(ctx, &lobbypb.CS_Purchase{OpId: opID, Kind: kind, Price: price, ItemId: itemID})
	})
}

// mailListSync 驱动 Maillist 并同步取回 SC_MailList
func mailListSync(t *testing.T, rt *Runtime, uid int64) *lobbypb.SC_MailList {
	return driveReq[*lobbypb.SC_MailList](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Maillist(ctx, &lobbypb.CS_MailList{})
	})
}

// mailClaimSync 驱动 Mailclaim 并同步取回 SC_MailClaim
func mailClaimSync(t *testing.T, rt *Runtime, uid int64, mailID string) *lobbypb.SC_MailClaim {
	return driveReq[*lobbypb.SC_MailClaim](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Mailclaim(ctx, &lobbypb.CS_MailClaim{MailId: mailID})
	})
}

// friendAddSync 驱动 Friendadd 并同步取回 SC_FriendAdd
func friendAddSync(t *testing.T, rt *Runtime, uid, target int64) *lobbypb.SC_FriendAdd {
	return driveReq[*lobbypb.SC_FriendAdd](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Friendadd(ctx, &lobbypb.CS_FriendAdd{Target: target})
	})
}

// friendRespondSync 驱动 Friendrespond 并同步取回 SC_FriendRespond
func friendRespondSync(t *testing.T, rt *Runtime, uid int64, mailID string, accept bool) *lobbypb.SC_FriendRespond {
	return driveReq[*lobbypb.SC_FriendRespond](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Friendrespond(ctx, &lobbypb.CS_FriendRespond{MailId: mailID, Accept: accept})
	})
}

// scanAcceptsSync barrier：登录 accept-scan 已在 loadPlayerSync 内 barrier，这里再确保一次
func scanAcceptsSync(t *testing.T, rt *Runtime, uid int64) { runOnLoop(t, rt, func() {}) }

// --- handler 同步驱动器：构造 CS、调 handler、等 Reply、Unmarshal SC ---
func driveReq[R proto.Message](t *testing.T, rt *Runtime, uid int64, call func(h *LobbyHandler, ctx context.Context)) R {
	t.Helper()
	r := newFakeReplier()
	h := NewLobbyHandler(rt)
	call(h, ctxWith(uid, r))
	rr := r.wait(t)
	if rr.err != nil {
		t.Fatalf("handler err: %v", rr.err)
	}
	var msg R
	msg = msg.ProtoReflect().New().Interface().(R)
	if len(rr.data) > 0 {
		if err := proto.Unmarshal(rr.data, msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}
	return msg
}
