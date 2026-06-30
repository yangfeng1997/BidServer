//go:build integration

package internal

import (
	"fmt"
	"testing"
	"time"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/mongo"
	"project/src/framework/cluster"
)

// 跨路结算 funnel 端到端集成测试（真实 MongoDB）：离线赢家结算落 offline_messages，
// 重登重放恰一次（opID=gameId 持久去重），再登不双发——验证 P4b 结算持久 + 跨会话
// 「恰一次」不变式在真实 Mongo 下成立。
//
// 仅需 MongoDB：结算的客户端推送 / 房间解绑是 best-effort 集群 Cast，noop 集群下无副作用，
// 故本测试用 noop 集群 + 真实 Mongo（不依赖 NATS/etcd）。
//
// 运行：
//
//	docker compose -f test/docker-compose.yaml up -d
//	go test -tags integration ./src/servers/lobbysvr/internal/ -run TestSettlementFunnel -v
//
// 沙箱无 Docker：仅 `go vet -tags integration ./...` 编译验证；实跑留 Docker host。
// 与本服务实现同包（package internal），以直接构造真实 Runtime/Store（Go internal 包可见性
// 禁止从外部 test 包导入）。

const (
	itestMongoURI = "mongodb://localhost:27017"
	itestMongoDB  = "lobby_itest"
)

// newFunnelRuntime 构造接真实 Mongo store 的 lobby Runtime（noop 集群）并启动主循环。
func newFunnelRuntime(mc *mongo.Client) *Runtime {
	rt := NewRuntime(RuntimeConfig{
		NodeID:       "1.2.1",
		Cluster:      cluster.NewNoopCluster(),
		Store:        NewMongoStore(mc),
		MailStore:    NewMongoMailStore(mc),
		OfflineStore: NewMongoOfflineStore(mc),
	})
	rt.Start()
	return rt
}

// loginAndWait 在主循环登录 uid 并等待 reply（reply 在 replayOffline 应用 + flush 之后触发）。
func loginAndWait(t *testing.T, rt *Runtime, uid int64) {
	t.Helper()
	type res struct {
		rsp *lobbypb.RPC_Login_Rsp
		err error
	}
	ch := make(chan res, 1)
	rt.Submit(func() {
		rt.Login(uid, "1.1.1", func(rsp *lobbypb.RPC_Login_Rsp, err error) {
			ch <- res{rsp, err}
		})
	})
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("login uid=%d: %v", uid, r.err)
		}
		if r.rsp == nil || r.rsp.Code != 0 || r.rsp.Uid != uid {
			t.Fatalf("login uid=%d unexpected rsp: %+v", uid, r.rsp)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("login uid=%d timed out", uid)
	}
}

// bagCount 在主循环读 uid 的某道具数量。
func bagCount(t *testing.T, rt *Runtime, uid int64, itemID int32) int32 {
	t.Helper()
	ch := make(chan int32, 1)
	rt.Submit(func() {
		p := rt.Player(uid)
		if p == nil {
			ch <- -1
			return
		}
		ch <- p.Bag().Count(itemID)
	})
	select {
	case n := <-ch:
		return n
	case <-time.After(5 * time.Second):
		t.Fatalf("bagCount uid=%d timed out", uid)
		return -1
	}
}

func TestSettlementFunnel_OfflineReplayExactlyOnce(t *testing.T) {
	mc, err := mongo.Connect(itestMongoURI, itestMongoDB, 10*time.Second)
	if err != nil {
		t.Fatalf("mongo connect (need `docker compose -f test/docker-compose.yaml up -d`): %v", err)
	}
	defer mc.Close()

	// 唯一 uid/gameId：避免与历史测试数据碰撞，无需清库。
	uid := int64(770000) + time.Now().UnixNano()%100000
	gameID := fmt.Sprintf("g-itest-%d", uid)
	const item = int32(7)

	// 1. 离线结算：uid 未登录（players 无此 uid，p==nil）→ Settle 走离线分支推 offline_messages。
	//    price=0 免去预置 gold（applyOfflineMsg 仅 Bag.Add，断言聚焦道具恰一次）。
	rt1 := newFunnelRuntime(mc)
	settleCh := make(chan int32, 1)
	rt1.Submit(func() {
		rt1.Settle(uid, gameID, uid /*winner*/, 0 /*price*/, "gold", item, func(code int32) {
			settleCh <- code
		})
	})
	select {
	case code := <-settleCh:
		if code != 0 {
			t.Fatalf("offline settle code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("offline settle timed out")
	}
	time.Sleep(300 * time.Millisecond) // 等 offline_messages $push 落定（off-loop）
	rt1.Stop()

	// 2. 首次重登 → replayOffline 应用一次 → Bag 含 item==1，flush 落库，$pull（异步紧随 reply）。
	rt2 := newFunnelRuntime(mc)
	loginAndWait(t, rt2, uid)
	if n := bagCount(t, rt2, uid, item); n != 1 {
		t.Fatalf("after first replay: item %d count = %d, want 1", item, n)
	}
	time.Sleep(500 * time.Millisecond) // 等 $pull 落定
	rt2.Stop()

	// 3. 再次重登 → 从 Mongo 加载（item==1），即便离线消息因 $pull 时序仍滞留，
	//    持久 opID=gameId 去重使 replay 不再加道具 → 恰一次（不双发）。
	rt3 := newFunnelRuntime(mc)
	loginAndWait(t, rt3, uid)
	if n := bagCount(t, rt3, uid, item); n != 1 {
		t.Fatalf("after re-login: item %d count = %d, want 1 (exactly-once funnel)", item, n)
	}
	rt3.Stop()
}
