# P4b 对局（竞拍）+ 结算 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 P4a `匹配→开局→room 绑定` 之上落地竞拍出价 + 结算闭环（含持久幂等发放设施、离线消息机制、matchsvr 双发/超时封堵）。

**Architecture:** roomsvr 帧驱动单主循环承载竞拍状态机（timewheel 倒计时到点 settle，回调在主循环）；出价走 A 路径（gate→lobby 校验扣减→router DIRECT→room→广播）；结算 room→lobby 经 router DIRECT 回告（at-least-once 重试），赢家在线即 `Spend+Add`、离线投 `offline_messages` 登录重放；幂等以**持久化进各组件 BSON 子文档的 opDedup 环**为真边界（`opID=gameId` / `opID=mailID`），ack/清理一律置于 flush 落库回调之后（§6.6 不变式）。

**Tech Stack:** Go；NATS/etcd/MongoDB；既有 framework（cluster/routerclient/timewheel/taskqueue/mongo）；proto3 + `gen_routes` 反射路由。

**参考 Spec：** [`2026-06-04-P4b-auction-settlement.md`](2026-06-04-P4b-auction-settlement.md)（§编号本计划沿用）。

---

## 文件结构

| 文件 | 责任 | 任务 |
|---|---|---|
| `protocal/{room,match,lobby}.proto` + `protocal/gen/**` + `protocal/gen/routes/*` | 竞拍/结算/超时消息 + 路由 | A1 |
| `src/servers/lobbysvr/internal/op_dedup.go` | opDedup 环 snapshot/loadFrom | B1 |
| `src/servers/lobbysvr/internal/component_currency.go` / `component_bag.go` | State 加 `Ops`，Load/Snapshot 带 ops | B2/B3 |
| `src/common/mongo/mongo.go` | `UpdateByID`（$push/$pull/$set + upsert）原语 | C1 |
| `src/servers/lobbysvr/internal/offline_store.go`（新） | `offline_messages` collection + `OfflineStore` | C2 |
| `src/servers/lobbysvr/internal/lobby_handler.go` | 大厅禁购、mail-claim ① 重排、bid/auctionstate/settle/matchtimeout handler | D1/D2/F2-F4/F7 |
| `src/servers/lobbysvr/internal/mailbox_store.go` | `Get`/`MarkClaimed`（① 重排用） | D2 |
| `src/servers/roomsvr/internal/game.go` / `runtime.go` / `room_handler.go` | 竞拍字段 + bid + settle + 广播 + cls/inflight/drain | E1-E5 |
| `src/servers/roomsvr/main.go` | 传 cls | E6 |
| `src/servers/lobbysvr/internal/player.go` / `runtime.go` | `roomBinding.currency`、unbindRoom hook、offline replay | F1/F4-F6 |
| `src/servers/matchsvr/internal/queue.go` / `runtime.go` | pendingUids 封双发 + 超时 reap | G1/G2 |
| `src/servers/*/internal/*_integration_test.go` | 集成骨架（build-tag） | H1 |
| `architecture.md` / `cluster.md` / `development.md` | 文档同步 | H2 |

**依赖序**：A(proto) → B(持久 ops) → C(mongo/offline) → D(禁购/①) → E(roomsvr) → F(lobby 结算/出价/重放) → G(matchsvr) → H(集成/文档)。B/C 是 F 的前置；D 独立可并行；E 与 F 经 proto 契约解耦。

---

## Stage A — proto / gen_routes

### Task A1: 扩 room/match/lobby proto + 重跑 gen_routes

**Files:**
- Modify: `protocal/room.proto`、`protocal/match.proto`、`protocal/lobby.proto`
- Generated: `protocal/gen/{room,match,lobby}/*.pb.go`、`protocal/gen/routes/*`

- [ ] **Step 1: 改 `protocal/room.proto`（加 currency / Bid / AuctionState / Settle）**

```proto
syntax = "proto3";

package room;

option go_package = "project/protocal/gen/room";

// Participant 开局参与者
message Participant {
  int64  uid           = 1;
  string lobby_node_id = 2;
}

// RPC_OpenGame_Req match → room 开局（经 router CONSISTENT_HASH key=game_id，route="RoomHandler.opengame"）
message RPC_OpenGame_Req {
  string               game_id       = 1;
  int32                item_id       = 2;
  int32                countdown_sec = 3;
  repeated Participant participants  = 4;
  string               currency      = 5; // 竞拍币种（matchsvr 设，MVP 默认 "gold"）
}
message RPC_OpenGame_Rsp {
  int32  code         = 1; // 0=已建局（含幂等命中）；非 0=参数非法等
  string room_node_id = 2; // room 自身 NodeID 串，match 据此回告 lobby
}

// RPC_Bid_Req lobby → room 出价（经 router DIRECT key=room_node_id，route="RoomHandler.bid"）
message RPC_Bid_Req {
  string game_id = 1;
  int64  uid     = 2;
  int64  amount  = 3;
}
message RPC_Bid_Rsp {
  int32 code           = 1; // 0=已接受；1=已封盘；2=非参与者/局不存在；3=未高于当前最高价
  int64 highest_bid    = 2;
  int64 highest_bidder = 3;
}

// RPC_AuctionState_Notify room → lobby 广播拍卖态（经 router DIRECT key=lobby_node_id，route="LobbyHandler.auctionstate"，Cast 无回包）
message RPC_AuctionState_Notify {
  int64  uid                 = 1;
  string game_id             = 2;
  int64  highest_bid         = 3;
  int64  highest_bidder      = 4;
  int32  countdown_remaining = 5;
}

// RPC_Settle_Req room → lobby 结算回告（经 router DIRECT key=lobby_node_id，route="LobbyHandler.settle"）
message RPC_Settle_Req {
  int64  uid      = 1;
  string game_id  = 2;
  int64  winner   = 3;
  int64  price    = 4;
  int32  item_id  = 5;
  string currency = 6;
}
message RPC_Settle_Rsp {
  int32 code = 1; // 0=已落地（在线扣发或离线投递成功）；非 0=投递失败，room 应重投
}
```

- [ ] **Step 2: 改 `protocal/match.proto`（GameStarted 加 currency；加 MatchTimeout）**

把 `RPC_GameStarted_Req` 替换为下列（加字段 4），并在文件末尾追加 `RPC_MatchTimeout_Notify`：

```proto
// RPC_GameStarted_Req match → lobby 开局回告（经 router DIRECT，route="LobbyHandler.gamestarted"）
message RPC_GameStarted_Req {
  int64  uid          = 1;
  string game_id      = 2;
  string room_node_id = 3;
  string currency     = 4; // 竞拍币种，lobby 置入 roomAffinity 供出价 CanAfford
}
message RPC_GameStarted_Rsp {
  int32 code = 1; // 0=lobby 已置亲和；负=玩家未加载等失败
}

// RPC_MatchTimeout_Notify matchsvr → lobby 匹配超时回告（经 router DIRECT，route="LobbyHandler.matchtimeout"，Cast）
message RPC_MatchTimeout_Notify {
  int64  uid    = 1;
  string req_id = 2;
}
```

- [ ] **Step 3: 改 `protocal/lobby.proto`（追加 2037-2041）**

在 `SC_MatchFound`（2036）之后追加：

```proto
// --- 客户端 ↔ lobby 竞拍出价 ---
message CS_Bid {
  option (options.msg_id)         = 2037;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.bid";
  string game_id = 1;
  int64  amount  = 2;
}
message SC_Bid {
  option (options.msg_id) = 2038;
  int32 code        = 1; // 0=接受；1=已封盘；2=非局中/亲和不符；3=未高于最高价；4=余额不足
  int64 highest_bid = 2;
}
// SC_AuctionState 拍卖态广播（仅 msg_id，gate 据此推）
message SC_AuctionState {
  option (options.msg_id) = 2039;
  string game_id             = 1;
  int64  highest_bid         = 2;
  int64  highest_bidder      = 3;
  int32  countdown_remaining = 4;
}
// SC_AuctionResult 结算结果广播（仅 msg_id）
message SC_AuctionResult {
  option (options.msg_id) = 2040;
  string game_id  = 1;
  int64  winner   = 2;
  int64  price    = 3;
  int32  item_id  = 4;
  string currency = 5;
}
// SC_MatchTimeout 匹配超时推送（仅 msg_id）
message SC_MatchTimeout {
  option (options.msg_id) = 2041;
}
```

- [ ] **Step 4: 重新生成 pb + 路由**

Run:
```bash
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
INC=/game/dev/silver-server/3rd/protobuf/include
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$INC \
  protocal/room.proto protocal/match.proto protocal/lobby.proto
go run ./tools/gen_routes
```
Expected: 无报错；`protocal/gen/room`、`protocal/gen/match`、`protocal/gen/lobby` 更新；`protocal/gen/routes/*` 含 `LobbyHandler.bid`/`LobbyHandler.auctionstate`/`LobbyHandler.settle`/`LobbyHandler.matchtimeout`/`RoomHandler.bid` 路由与 2037-2041 msg_id。

- [ ] **Step 5: 编译校验**

Run: `go build ./...`
Expected: PASS（仅生成代码改动，尚无引用）。

- [ ] **Step 6: Commit**

```bash
git add protocal/room.proto protocal/match.proto protocal/lobby.proto protocal/gen
git commit -m "feat(proto): P4b 竞拍/结算/超时消息 + room.currency + gen_routes"
```

---

## Stage B — 持久幂等发放设施（opDedup 环持久化）

### Task B1: opDedup 加 snapshot / loadFrom

**Files:**
- Modify: `src/servers/lobbysvr/internal/op_dedup.go`
- Test: `src/servers/lobbysvr/internal/op_dedup_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `op_dedup_test.go`：

```go
func TestOpDedup_SnapshotLoadRoundtrip(t *testing.T) {
	o := newOpDedup(128)
	o.remember("a")
	o.remember("b")
	snap := o.snapshot()
	if len(snap) != 2 || snap[0] != "a" || snap[1] != "b" {
		t.Fatalf("snapshot mismatch: %v", snap)
	}
	o2 := newOpDedup(128)
	o2.loadFrom(snap)
	if !o2.seen("a") || !o2.seen("b") || o2.seen("c") {
		t.Fatalf("loadFrom did not rebuild dedup set")
	}
}

func TestOpDedup_LoadFromRespectsBound(t *testing.T) {
	o := newOpDedup(2)
	o.loadFrom([]string{"a", "b", "c"}) // 超界：淘汰最旧 a
	if o.seen("a") || !o.seen("b") || !o.seen("c") {
		t.Fatalf("loadFrom should keep bound, evicting oldest")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup_Snapshot -v`
Expected: FAIL（`o.snapshot undefined`）。

- [ ] **Step 3: 实现**

追加到 `op_dedup.go`：

```go
// snapshot 返回 op-id 环的有序快照（落库用，值拷贝）。
func (o *opDedup) snapshot() []string {
	out := make([]string, len(o.order))
	copy(out, o.order)
	return out
}

// loadFrom 用持久化的 op-id 序列重建去重环（覆盖现状，维持有界淘汰）。
func (o *opDedup) loadFrom(ops []string) {
	o.seenSet = make(map[string]struct{}, len(ops))
	o.order = o.order[:0]
	for _, id := range ops {
		o.remember(id)
	}
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/op_dedup.go src/servers/lobbysvr/internal/op_dedup_test.go
git commit -m "feat(lobby): opDedup snapshot/loadFrom（持久 ops 基础）"
```

### Task B2: CurrencyState 加 Ops，Load/Snapshot 带 ops

**Files:**
- Modify: `src/servers/lobbysvr/internal/component_currency.go`
- Test: `src/servers/lobbysvr/internal/component_currency_test.go`

- [ ] **Step 1: 写失败测试**

追加：

```go
func TestCurrency_OpsPersistRoundtrip(t *testing.T) {
	c := NewCurrency()
	c.Gain("op1", "gold", 100)
	c.Spend("op2", "gold", 30)
	snap := c.Snapshot().(CurrencyState)
	if len(snap.Ops) != 2 {
		t.Fatalf("want 2 persisted ops, got %v", snap.Ops)
	}
	// 重建：op1/op2 应被去重（幂等）
	c2 := NewCurrency()
	c2.Load(&snap)
	if _, changed := c2.Gain("op1", "gold", 999); changed {
		t.Fatalf("op1 should be deduped after Load")
	}
	if bal, ok := c2.Spend("op2", "gold", 999); !ok || bal != snap.Balances["gold"] {
		t.Fatalf("op2 should be idempotent-success after Load")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestCurrency_OpsPersist -v`
Expected: FAIL（`snap.Ops undefined`）。

- [ ] **Step 3: 实现**

`CurrencyState` 加字段；`Load`/`Snapshot` 带 ops：

```go
// CurrencyState 货币存储态（内嵌 players 文档 currency 子文档）
type CurrencyState struct {
	Balances map[string]int64 `bson:"balances"`
	Ops      []string         `bson:"ops,omitempty"` // 持久化 op-id 去重环（跨会话/重投/重放幂等）
}
```

`Load` 末尾（`c.dirty = false` 之前）加 `c.ops.loadFrom(s.Ops)`：

```go
func (c *Currency) Load(s *CurrencyState) {
	c.balances = make(map[string]int64, len(s.Balances))
	for k, v := range s.Balances {
		c.balances[k] = v
	}
	c.ops.loadFrom(s.Ops)
	c.dirty = false
}
```

`Snapshot` 带 ops：

```go
func (c *Currency) Snapshot() any {
	out := make(map[string]int64, len(c.balances))
	for k, v := range c.balances {
		out[k] = v
	}
	return CurrencyState{Balances: out, Ops: c.ops.snapshot()}
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestCurrency' -v`
Expected: PASS（含既有用例）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/component_currency.go src/servers/lobbysvr/internal/component_currency_test.go
git commit -m "feat(lobby): Currency 持久化 op-dedup 环（结算跨重登幂等）"
```

### Task B3: BagState 加 Ops，Load/Snapshot 带 ops

**Files:**
- Modify: `src/servers/lobbysvr/internal/component_bag.go`
- Test: `src/servers/lobbysvr/internal/component_bag_test.go`

- [ ] **Step 1: 写失败测试**

追加：

```go
func TestBag_OpsPersistRoundtrip(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 2)
	snap := b.Snapshot().(BagState)
	if len(snap.Ops) != 1 {
		t.Fatalf("want 1 persisted op, got %v", snap.Ops)
	}
	b2 := NewBag()
	b2.Load(&snap)
	if n := b2.Add("op1", 100, 999); n != 2 {
		t.Fatalf("op1 should be deduped after Load, count=%d", n)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBag_OpsPersist -v`
Expected: FAIL（`snap.Ops undefined`）。

- [ ] **Step 3: 实现**

```go
// BagState 背包的存储态（内嵌 players 文档；bson map 键须为 string）
type BagState struct {
	Items map[string]int32 `bson:"items"`
	Ops   []string         `bson:"ops,omitempty"` // 持久化 op-id 去重环
}
```

`Load` 在 `b.dirty = false` 之前加 `b.ops.loadFrom(s.Ops)`；`Snapshot` 返回 `BagState{Items: items, Ops: b.ops.snapshot()}`。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestBag' -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/component_bag.go src/servers/lobbysvr/internal/component_bag_test.go
git commit -m "feat(lobby): Bag 持久化 op-dedup 环"
```

---

## Stage C — mongo 原语 + 离线消息 store

### Task C1: mongo `UpdateByID`（$push/$pull/$set + upsert）

**Files:**
- Modify: `src/common/mongo/mongo.go`
- Test: `src/common/mongo/mongo_test.go`（`//go:build integration` 仅编译验证；沙箱无 Docker）

- [ ] **Step 1: 写失败测试（编译验证）**

追加到 `mongo_test.go`（沿用文件既有 build tag 与 harness；若文件为 integration tag，则本测试同 tag）：

```go
func TestClient_UpdateByID_Compiles(t *testing.T) {
	// 编译验证：签名存在即可（沙箱无 Docker 不实跑）
	var c *Client
	_ = func() {
		c.UpdateByID(nil, "coll", int64(1),
			bson.M{"$push": bson.M{"messages": bson.M{"op_id": "x"}}}, true, func(error) {})
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go vet -tags integration ./src/common/mongo/`
Expected: FAIL（`c.UpdateByID undefined`）。

- [ ] **Step 3: 实现**

追加到 `mongo.go`：

```go
// UpdateByID 异步对 _id 文档应用任意 update（如 $push/$pull/$set），upsert 可选；done(err) 在 dispatcher 执行。
// 用于离线消息 append($push)/确认($pull) 等增量原子写。
func (c *Client) UpdateByID(d taskqueue.Dispatcher, coll string, id any, update bson.M, upsert bool, done func(error)) {
	runAsync(d, func() error {
		_, err := c.db.Collection(coll).UpdateByID(c.ctx, id, update, options.Update().SetUpsert(upsert))
		return err
	}, done)
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go vet -tags integration ./src/common/mongo/ && go build ./src/common/mongo/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/common/mongo/mongo.go src/common/mongo/mongo_test.go
git commit -m "feat(mongo): UpdateByID（$push/$pull/$set + upsert）原语"
```

### Task C2: 离线消息 store（`offline_messages` + `OfflineStore`）

**Files:**
- Create: `src/servers/lobbysvr/internal/offline_store.go`
- Test: `src/servers/lobbysvr/internal/offline_store_test.go`（fake 单测）

- [ ] **Step 1: 写失败测试（用 fake + 行为约定）**

`offline_store_test.go`：

```go
package internal

import (
	"testing"

	"project/src/common/taskqueue"
)

// fakeOfflineStore 内存离线消息 store（按行为约定，单测用）
type fakeOfflineStore struct{ docs map[int64][]OfflineMsg }

func newFakeOfflineStore() *fakeOfflineStore { return &fakeOfflineStore{docs: map[int64][]OfflineMsg{}} }

func (s *fakeOfflineStore) Push(d taskqueue.Dispatcher, uid int64, m OfflineMsg, done func(error)) {
	s.docs[uid] = append(s.docs[uid], m)
	d.Enqueue(func() { done(nil) })
}
func (s *fakeOfflineStore) Load(d taskqueue.Dispatcher, uid int64, done func([]OfflineMsg, error)) {
	cp := append([]OfflineMsg(nil), s.docs[uid]...)
	d.Enqueue(func() { done(cp, nil) })
}
func (s *fakeOfflineStore) Ack(d taskqueue.Dispatcher, uid int64, opIDs []string, done func(error)) {
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
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOfflineMsg -v`
Expected: FAIL（`OfflineMsg`/`OfflineStore` undefined）。

- [ ] **Step 3: 实现 `offline_store.go`**

```go
package internal

import (
	"go.mongodb.org/mongo-driver/bson"

	"project/src/common/mongo"
	"project/src/common/taskqueue"
)

const offlineColl = "offline_messages"

// 离线消息类型（本期只 settle）
const OfflineMsgSettle = "settle"

// OfflineMsg 离线消息信封（带 type，便于后续复用）。settle: 离线赢家结算扣币+发物。
type OfflineMsg struct {
	Type     string `bson:"type"`
	OpID     string `bson:"op_id"` // 幂等键（settle=gameId）
	Price    int64  `bson:"price,omitempty"`
	Currency string `bson:"currency,omitempty"`
	ItemID   int32  `bson:"item_id,omitempty"`
}

// OfflineDoc offline_messages 文档：每玩家一 doc，多写者 append-only
type OfflineDoc struct {
	ID       int64        `bson:"_id"`
	Messages []OfflineMsg `bson:"messages"`
}

// OfflineStore 离线消息持久化抽象（便于 fake 替换）
type OfflineStore interface {
	Push(d taskqueue.Dispatcher, uid int64, msg OfflineMsg, done func(error))
	Load(d taskqueue.Dispatcher, uid int64, done func([]OfflineMsg, error))
	Ack(d taskqueue.Dispatcher, uid int64, opIDs []string, done func(error)) // $pull 已处理
}

// mongoOfflineStore 基于 src/common/mongo 的实现
type mongoOfflineStore struct{ c *mongo.Client }

// NewMongoOfflineStore 用已连接的 mongo.Client 构建
func NewMongoOfflineStore(c *mongo.Client) OfflineStore { return &mongoOfflineStore{c: c} }

func (s *mongoOfflineStore) Push(d taskqueue.Dispatcher, uid int64, msg OfflineMsg, done func(error)) {
	s.c.UpdateByID(d, offlineColl, uid, bson.M{"$push": bson.M{"messages": msg}}, true, done)
}

func (s *mongoOfflineStore) Load(d taskqueue.Dispatcher, uid int64, done func([]OfflineMsg, error)) {
	doc := &OfflineDoc{}
	s.c.FindByID(d, offlineColl, uid, doc, func(found bool, err error) {
		if err != nil || !found {
			done(nil, err)
			return
		}
		done(doc.Messages, nil)
	})
}

func (s *mongoOfflineStore) Ack(d taskqueue.Dispatcher, uid int64, opIDs []string, done func(error)) {
	if len(opIDs) == 0 {
		d.Enqueue(func() { done(nil) })
		return
	}
	s.c.UpdateByID(d, offlineColl, uid,
		bson.M{"$pull": bson.M{"messages": bson.M{"op_id": bson.M{"$in": opIDs}}}}, false, done)
}

var _ OfflineStore = (*mongoOfflineStore)(nil)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOfflineMsg -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/offline_store.go src/servers/lobbysvr/internal/offline_store_test.go
git commit -m "feat(lobby): 离线消息 store（offline_messages collection + OfflineStore）"
```

---

## Stage D — 大厅禁购（D6）+ mail-claim ① 重排

### Task D1: 对局期禁大厅消费（D6）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`（`Purchase`）
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestLobbyHandler_PurchaseRejectedDuringGame(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 1000)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1")
	})
	rsp := purchaseSync(t, rt, 10001, "p1", "gold", 10, 5)
	if rsp.Code != 2 {
		t.Fatalf("purchase during game should be rejected code=2, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 1000 {
			t.Fatalf("balance must be untouched when purchase rejected")
		}
	})
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_PurchaseRejectedDuringGame -v`
Expected: FAIL（当前 Purchase 不查 roomAffinity，会扣款返回 code=0）。

- [ ] **Step 3: 实现**

`Purchase` 的 `h.rt.Submit` 内、取到 `p` 之后、`cur := p.Currency()` 之前插入：

```go
		if p.RoomAffinity() != nil {
			replyProto(replier, &lobbypb.SC_Purchase{Code: 2}, nil) // 2=对局中禁大厅消费（D6）
			return
		}
```

并在 `protocal/lobby.proto` 的 `SC_Purchase.code` 注释补 `2=对局中`（无需改字段，注释即可；如已重生成不必再跑）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestLobbyHandler_Purchase' -v`
Expected: PASS（含既有购买用例）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go
git commit -m "feat(lobby): D6 对局期禁大厅消费（Purchase 查 roomAffinity）"
```

### Task D2: mail-claim ① 重排（grant→persist→mark-claimed，opID=mailID）

**Files:**
- Modify: `src/servers/lobbysvr/internal/mailbox_store.go`（`MailStore` 加 `Get`/`MarkClaimed`）
- Modify: `src/servers/lobbysvr/internal/component_mail.go`（`Mail.Get`/`Mail.MarkClaimed`）
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`（`Mailclaim`）
- Modify: `src/servers/lobbysvr/internal/runtime.go`（`grantAttachments` 收 opID）
- Test: `src/servers/lobbysvr/internal/mailbox_store_test.go`（fake 加方法）、`lobby_handler_test.go`

- [ ] **Step 1: fake/接口加 `Get`/`MarkClaimed`（先让 fake 满足新接口）**

`mailbox_store_test.go` 的 `fakeMailStore` 追加（按其既有内部 map 字段命名适配；下例假设 `mails map[primitive.ObjectID]*MailDoc`）：

```go
func (s *fakeMailStore) Get(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	m, ok := s.mails[id]
	d.Enqueue(func() {
		if !ok || m.To != to || m.Claimed {
			done(false, nil, nil)
			return
		}
		cp := *m
		done(true, &cp, nil)
	})
}
func (s *fakeMailStore) MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	if m, ok := s.mails[id]; ok {
		m.Claimed = true
	}
	d.Enqueue(func() { done(nil) })
}
```

> 注：若既有 `fakeMailStore` 内部不是 `mails map[ObjectID]*MailDoc`，按其实际结构等价实现「读未领取副本 / 置 claimed」。

- [ ] **Step 2: 写失败测试（① 持久幂等：grant 落库后重放不双发）**

```go
func TestLobbyHandler_MailClaimReorder_IdempotentReplay(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	// 预置一封带附件邮件
	var mid string
	runOnLoop(t, rt, func() {
		mid = seedMailWithAttachment(rt, 10001, Attachment{Kind: "gold", ID: 0, Count: 50})
	})
	rsp := mailClaimSync(t, rt, 10001, mid)
	if rsp.Code != 0 {
		t.Fatalf("first claim should succeed, code=%d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 50 {
			t.Fatalf("attachment should be granted once")
		}
	})
	// 模拟"已 grant 但 mark 前崩溃→重领"：mail 仍 claimed=false 时再领，应经 ops(mailID) 去重不双发
	runOnLoop(t, rt, func() { forceMailUnclaimed(rt, mid) }) // 测试钩子：把 mail 置回 claimed=false
	rsp2 := mailClaimSync(t, rt, 10001, mid)
	if rsp2.Code != 0 {
		t.Fatalf("replay claim should succeed idempotently, code=%d", rsp2.Code)
	}
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 50 {
			t.Fatalf("replay must NOT double-grant, bal=%d", rt.players[10001].Currency().Balance("gold"))
		}
	})
}
```

> `seedMailWithAttachment` / `forceMailUnclaimed` 为测试钩子，加在 `mailbox_store_test.go`：前者向 fake 插入一封 `To=uid, Claimed=false, Attachments=[...]` 邮件返回 hex id；后者把 fake 中该 mail 的 `Claimed` 置回 false。

- [ ] **Step 3: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_MailClaimReorder -v`
Expected: FAIL（`Get`/`MarkClaimed` 未在接口；`Mailclaim` 仍用 Claim+opID=""）。

- [ ] **Step 4: 实现接口与 store**

`mailbox_store.go` 的 `MailStore` 接口加：

```go
	Get(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(found bool, m *MailDoc, err error))
	MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error))
```

`mongoMailStore` 实现：

```go
// Get 读未领取邮件副本（不改状态）：匹配 {_id,to,claimed:false}。
func (s *mongoMailStore) Get(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl, bson.M{"_id": id, "to": to, "claimed": false}, nil, 1, &out, func(err error) {
		if err != nil {
			done(false, nil, err)
			return
		}
		if len(out) == 0 {
			done(false, nil, nil)
			return
		}
		done(true, &out[0], nil)
	})
}

// MarkClaimed 标记已领取（grant 落库后调用；单写者下无并发双标）。
func (s *mongoMailStore) MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	s.c.UpdateByID(d, mailboxColl, id, bson.M{"$set": bson.M{"claimed": true}}, false, done)
}
```

`component_mail.go` 加：

```go
// Get 读未领取邮件副本（① 重排用）
func (m *Mail) Get(d taskqueue.Dispatcher, id primitive.ObjectID, done func(bool, *MailDoc, error)) {
	m.store.Get(d, id, m.uid, done)
}

// MarkClaimed grant 落库后标记已领取
func (m *Mail) MarkClaimed(d taskqueue.Dispatcher, id primitive.ObjectID, done func(error)) {
	m.store.MarkClaimed(d, id, done)
}
```

- [ ] **Step 5: 改 `grantAttachments` 收 opID + `Mailclaim` 重排**

`runtime.go` 的 `grantAttachments` 改签名收 `opID`：

```go
// grantAttachments 主循环内把附件发放进玩家组件，以 opID 持久幂等去重（重排后 mailID 为键）。
func (rt *Runtime) grantAttachments(uid int64, p *Player, opID string, atts []Attachment) {
	for _, a := range atts {
		if a.Kind == "item" {
			p.Bag().Add(opID, int32(a.ID), int32(a.Count))
		} else {
			p.Currency().Gain(opID, a.Kind, a.Count)
			rt.PublishCurrencyChanged(uid, a.Kind, a.Count)
		}
	}
}
```

`lobby_handler.go` 的 `Mailclaim` 改为读→去重→grant→flush→mark（替换 `p.Mail().Claim(...)` 整段）：

```go
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		opID := req.MailId // 持久幂等键（hex）
		p.Mail().Get(h.rt.tq, id, func(ok bool, m *MailDoc, gerr error) {
			if gerr != nil {
				replyProto(replier, nil, gerr)
				return
			}
			if !ok {
				replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) // 不存在/已领取
				return
			}
			atts := m.Attachments
			h.rt.grantAttachments(uid, p, opID, atts) // 幂等：重领经 ops(mailID) 跳过
			h.rt.flushPlayer(uid, p, func() {
				p.Mail().MarkClaimed(h.rt.tq, id, func(merr error) {
					if merr != nil {
						logger.Warn("mailclaim: mark claimed failed (grant 已落，最终一致)",
							logger.Int64("uid", uid), logger.String("mailId", opID), logger.Err(merr))
					}
				})
				rsp := &lobbypb.SC_MailClaim{Code: 0}
				for _, a := range atts {
					rsp.Granted = append(rsp.Granted, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
				}
				replyProto(replier, rsp, nil)
			})
		})
	})
```

> `logger` 已在 `lobby_handler.go` import；若未，按既有 import 块补 `"project/src/common/logger"`。

- [ ] **Step 6: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestLobbyHandler_MailClaim' -race -v`
Expected: PASS（含既有 claim 用例，若既有用例断言旧 Claim 行为则按"读→grant→mark"语义等价更新断言，不迁就实现）。

- [ ] **Step 7: Commit**

```bash
git add src/servers/lobbysvr/internal/mailbox_store.go src/servers/lobbysvr/internal/component_mail.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/mailbox_store_test.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): mail-claim ① 重排（grant→persist→mark-claimed，opID=mailID 持久幂等）"
```

---

## Stage E — roomsvr 竞拍状态机

### Task E1: `Game` 竞拍字段 + `OpenGame` 收 currency

**Files:**
- Modify: `src/servers/roomsvr/internal/game.go`、`runtime.go`、`room_handler.go`
- Test: `src/servers/roomsvr/internal/runtime_test.go`、`room_handler_test.go`（更新既有调用签名）

- [ ] **Step 1: 写失败测试**

追加到 `runtime_test.go`：

```go
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
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/roomsvr/internal/ -run TestRuntime_OpenGameCarriesCurrency -v`
Expected: FAIL（`OpenGame` 参数个数不符、`g.Currency` undefined）。

- [ ] **Step 3: 实现 `game.go`**

```go
package internal

import "time"

// Participant 局内参与者（与 roompb.Participant 解耦的内部态）
type Participant struct {
	UID         int64
	LobbyNodeID string
}

// Game 拍卖局对象。P4b：加最高价/赢家/币种/封盘 + 倒计时 deadline。多 gameId 并存隔离。
type Game struct {
	GameID        string
	Participants  []Participant
	ItemID        int32
	CountdownSec  int32
	Currency      string
	HighestBid    int64
	HighestBidder int64
	closed        bool
	deadline      time.Time // 倒计时到点（广播剩余秒用）
}

// NewGame 建局
func NewGame(gameID string, itemID, countdownSec int32, currency string, parts []Participant) *Game {
	return &Game{GameID: gameID, ItemID: itemID, CountdownSec: countdownSec, Currency: currency, Participants: parts}
}

// isParticipant 判断 uid 是否本局参与者
func (g *Game) isParticipant(uid int64) bool {
	for _, p := range g.Participants {
		if p.UID == uid {
			return true
		}
	}
	return false
}

// remaining 倒计时剩余秒（非负）
func (g *Game) remaining() int32 {
	d := int32(time.Until(g.deadline).Seconds())
	if d < 0 {
		return 0
	}
	return d
}
```

- [ ] **Step 4: 改 `runtime.go` 的 `OpenGame` 签名 + 设 deadline**

```go
// OpenGame 主循环内建局（按 gameId 幂等：已存在则不覆盖）。
func (rt *Runtime) OpenGame(gameID string, itemID, countdownSec int32, currency string, parts []Participant) {
	if _, ok := rt.games[gameID]; ok {
		return
	}
	g := NewGame(gameID, itemID, countdownSec, currency, parts)
	g.deadline = time.Now().Add(time.Duration(countdownSec) * time.Second)
	rt.games[gameID] = g
	gid := gameID
	rt.tw.AfterFunc(time.Duration(countdownSec)*time.Second, func() { rt.settle(gid) })
	logger.Info("room game opened", logger.String("gameId", gameID), logger.Int("participants", len(parts)))
}
```
（`rt.settle` 在 Task E5 实现；本任务先建空桩 `func (rt *Runtime) settle(string) {}` 以编译，E5 替换。）

- [ ] **Step 5: 改 `room_handler.go` 的 `Opengame` 传 currency**

把建局 `Submit` 内 `h.rt.OpenGame(gameID, itemID, countdown, parts)` 改为收 currency：

```go
	gameID, itemID, countdown, currency := req.GameId, req.ItemId, req.CountdownSec, req.Currency
	h.rt.Submit(func() {
		h.rt.OpenGame(gameID, itemID, countdown, currency, parts)
		replyProto(replier, &roompb.RPC_OpenGame_Rsp{Code: 0, RoomNodeId: h.rt.NodeID()}, nil)
	})
```

- [ ] **Step 6: 更新既有测试调用签名**

`runtime_test.go` 既有 `rt.OpenGame("1.8.1-1", 5, 30, parts)` → `rt.OpenGame("1.8.1-1", 5, 30, "gold", parts)`（两处 + MultiGameIsolation 两处）。`room_handler_test.go` 的 `RPC_OpenGame_Req` 可加 `Currency: "gold"`（非必须）。

- [ ] **Step 7: 运行验证通过**

Run: `go test ./src/servers/roomsvr/internal/ -v`
Expected: PASS。

- [ ] **Step 8: Commit**

```bash
git add src/servers/roomsvr/internal/
git commit -m "feat(room): Game 竞拍字段 + OpenGame 收 currency + settle 桩"
```

### Task E2: 出价逻辑 `Runtime.Bid`

**Files:**
- Modify: `src/servers/roomsvr/internal/runtime.go`
- Test: `src/servers/roomsvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试**

```go
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
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/roomsvr/internal/ -run TestRuntime_Bid -v`
Expected: FAIL（`rt.Bid undefined`）。

- [ ] **Step 3: 实现**

```go
// Bid 主循环内出价：校验并更新最高价。返回 (code, highestBid, highestBidder)。
// code: 0=接受；1=已封盘；2=局不存在/非参与者；3=未严格高于当前最高价。
func (rt *Runtime) Bid(gameID string, uid, amount int64) (int32, int64, int64) {
	g := rt.games[gameID]
	if g == nil || !g.isParticipant(uid) {
		return 2, 0, 0
	}
	if g.closed {
		return 1, g.HighestBid, g.HighestBidder
	}
	if amount <= g.HighestBid {
		return 3, g.HighestBid, g.HighestBidder
	}
	g.HighestBid = amount
	g.HighestBidder = uid
	return 0, g.HighestBid, g.HighestBidder
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/roomsvr/internal/ -run TestRuntime_Bid -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/roomsvr/internal/runtime.go src/servers/roomsvr/internal/runtime_test.go
git commit -m "feat(room): Runtime.Bid 出价校验 + 最高价更新"
```

### Task E3: `Runtime` 加 cls + 广播/结算 hook + inflight/drain；`RoomHandler.bid`；E5 settle 回告

> 本任务把 room 的 off-loop 编排接好（cls、广播、结算回告、inflight/drain），并实现 `RoomHandler.bid` 与 E5 的 settle。合并以保证 hook 字段一次成型、可单测注 fake。

**Files:**
- Modify: `src/servers/roomsvr/internal/runtime.go`、`room_handler.go`
- Test: `src/servers/roomsvr/internal/runtime_test.go`、`room_handler_test.go`

- [ ] **Step 1: 写失败测试（fake hook 验广播扇出 + settle 定赢家）**

`runtime_test.go`：

```go
func newTestRoomRuntimeWithHooks(t *testing.T) (*Runtime, *roomHookRec) {
	t.Helper()
	rec := &roomHookRec{}
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.broadcast = func(lobby string, uid int64, game string, hb, hbr int64, rem int32) {
		rec.mu.Lock(); rec.bcasts = append(rec.bcasts, [2]int64{uid, hb}); rec.mu.Unlock()
	}
	rt.notifySettle = func(lobby string, uid, winner, price int64, game string, item int32, cur string) error {
		rec.mu.Lock(); rec.settles = append(rec.settles, settleRec{uid, winner, price}); rec.mu.Unlock()
		return nil
	}
	rt.Start()
	return rt, rec
}

type settleRec struct{ uid, winner, price int64 }
type roomHookRec struct {
	mu      sync.Mutex
	bcasts  [][2]int64
	settles []settleRec
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
	// 等 off-loop settle 回告
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
```
（需在 `runtime_test.go` import `sync`。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/roomsvr/internal/ -run TestRuntime_SettleDeterminesWinner -v`
Expected: FAIL（`rt.broadcast`/`rt.notifySettle`/有效 `settle` undefined）。

- [ ] **Step 3: 实现 `runtime.go`（cls / hooks / inflight / drain / broadcastState / settle）**

`RuntimeConfig` 加 `Cluster cluster.Cluster`；`Runtime` 加字段：

```go
	cls      cluster.Cluster
	inflight atomic.Int64

	// off-loop 编排 hook（默认接真实 router；测试可替换）
	broadcast    func(lobbyNode string, uid int64, gameID string, hb, hbr int64, remaining int32)
	notifySettle func(lobbyNode string, uid, winner, price int64, gameID string, itemID int32, currency string) error
```
（import 增 `context`、`sync/atomic`、`project/src/framework/cluster`、`project/src/framework/cluster/routerclient`、`roompb "project/protocal/gen/room"`、`routerpb "project/protocal/gen/router"`。）

`NewRuntime` 末尾（return 前）加：

```go
	rt.cls = cfg.Cluster
	if cfg.Cluster != nil {
		rt.broadcast = rt.broadcastViaRouter
		rt.notifySettle = rt.settleViaRouter
	}
```

`loop()` 的 `case <-rt.stopCh:` 改为先 drain：

```go
		case <-rt.stopCh:
			rt.drain()
			return
```

加 drain（仿 matchsvr）：

```go
const drainTimeout = 5 * time.Second

func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("room drain timeout, abandoning in-flight", logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}
```

广播（主循环快照 + off-loop 扇出）：

```go
// broadcastState 主循环内快照拍卖态，off-loop 向各 participant lobby 广播（best-effort）。
func (rt *Runtime) broadcastState(gameID string) {
	g := rt.games[gameID]
	if g == nil || rt.broadcast == nil {
		return
	}
	type tgt struct {
		uid   int64
		lobby string
	}
	targets := make([]tgt, 0, len(g.Participants))
	for _, p := range g.Participants {
		targets = append(targets, tgt{p.UID, p.LobbyNodeID})
	}
	hb, hbr, rem := g.HighestBid, g.HighestBidder, g.remaining()
	bs := rt.broadcast
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, t := range targets {
			bs(t.lobby, t.uid, gameID, hb, hbr, rem)
		}
	}()
}

func (rt *Runtime) broadcastViaRouter(lobbyNode string, uid int64, gameID string, hb, hbr int64, remaining int32) {
	node, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("room broadcast: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	if err := rt.cls.Cast(ctx, node, "LobbyHandler.auctionstate",
		&roompb.RPC_AuctionState_Notify{Uid: uid, GameId: gameID, HighestBid: hb, HighestBidder: hbr, CountdownRemaining: remaining}); err != nil {
		logger.Warn("room broadcast: cast failed", logger.Int64("uid", uid), logger.Err(err))
	}
}
```

settle（替换 E1 的空桩）：

```go
const (
	settleRetries      = 3
	settleRetryBackoff = 200 * time.Millisecond
)

// settle 倒计时到点（主循环回调，§3 在主循环执行）：封盘定赢家 + off-loop 回告各 participant（at-least-once）。
func (rt *Runtime) settle(gameID string) {
	g := rt.games[gameID]
	if g == nil || g.closed {
		return // 幂等：已封盘不重复
	}
	g.closed = true
	winner, price, itemID, currency := g.HighestBidder, g.HighestBid, g.ItemID, g.Currency
	parts := append([]Participant(nil), g.Participants...)
	ns := rt.notifySettle
	if ns == nil {
		return
	}
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, p := range parts {
			for attempt := 0; attempt < settleRetries; attempt++ {
				if err := ns(p.LobbyNodeID, p.UID, gameID, winner, price, itemID, currency); err == nil {
					break
				} else if attempt == settleRetries-1 {
					logger.Warn("room settle notify exhausted retries",
						logger.Int64("uid", p.UID), logger.String("gameId", gameID), logger.Err(err))
				} else {
					time.Sleep(settleRetryBackoff)
				}
			}
		}
	}()
}

func (rt *Runtime) settleViaRouter(lobbyNode string, uid, winner, price int64, gameID string, itemID int32, currency string) error {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	rsp, err := routerclient.CallViaSync[*roompb.RPC_Settle_Rsp](
		ctx, rt.cls, "lobbysvr", routerpb.RoutingMode_ROUTING_DIRECT, lobbyNode, "LobbyHandler.settle",
		&roompb.RPC_Settle_Req{Uid: uid, GameId: gameID, Winner: winner, Price: price, ItemId: itemID, Currency: currency})
	if err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("settle code=%d", rsp.Code)
	}
	return nil
}
```
（import 增 `fmt`。）

- [ ] **Step 4: 实现 `RoomHandler.bid`**

`room_handler.go` 加：

```go
// Bid route="RoomHandler.bid"：记价 + 更新最高价 + 接受则广播。
func (h *RoomHandler) Bid(ctx context.Context, req *roompb.RPC_Bid_Req) (*roompb.RPC_Bid_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	gameID, uid, amount := req.GameId, req.Uid, req.Amount
	h.rt.Submit(func() {
		code, hb, hbr := h.rt.Bid(gameID, uid, amount)
		if code == 0 {
			h.rt.broadcastState(gameID)
		}
		replyProto(replier, &roompb.RPC_Bid_Rsp{Code: code, HighestBid: hb, HighestBidder: hbr}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 5: 运行验证通过（含 -race）**

Run: `go test ./src/servers/roomsvr/internal/ -race -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/roomsvr/internal/
git commit -m "feat(room): cls/inflight/drain + 广播 + settle 回告(at-least-once) + RoomHandler.bid"
```

### Task E4: roomsvr main 传 cls + 注册 bid handler

**Files:**
- Modify: `src/servers/roomsvr/main.go`

- [ ] **Step 1: 改 main**

`NewRuntime` 传 cls：

```go
	rt := internal.NewRuntime(internal.RuntimeConfig{NodeID: cfg.RoomCfg.NodeId, Cluster: app.Cluster()})
```
（`RegisterHandler(internal.NewRoomHandler(rt), nil)` 既有；`Bid` 方法随反射注册，无需额外改。）

- [ ] **Step 2: 编译校验**

Run: `go build ./src/servers/roomsvr/...`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add src/servers/roomsvr/main.go
git commit -m "feat(room): main 传 cls（启用广播/结算回告）"
```

---

## Stage F — lobby 出价编排 / 结算落地 / 离线重放

### Task F1: `roomBinding.currency` + `GameStarted` 携 currency

**Files:**
- Modify: `src/servers/lobbysvr/internal/player.go`、`lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/player_test.go`、`lobby_handler_test.go`（更新签名）

- [ ] **Step 1: 写失败测试**

`player_test.go` 既有 `p.SetRoomAffinity("1.7.1", "1.8.1-1")` 两处改为带 currency；追加断言：

```go
func TestPlayer_RoomAffinityCurrency(t *testing.T) {
	p := NewPlayer(1)
	p.SetRoomAffinity("1.7.1", "g1", "gold")
	if p.RoomAffinity().currency != "gold" || p.RoomAffinity().gameID != "g1" {
		t.Fatalf("affinity currency/game wrong: %+v", p.RoomAffinity())
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPlayer_RoomAffinityCurrency -v`
Expected: FAIL（`SetRoomAffinity` 参数不符 / `currency` 字段不存在）。

- [ ] **Step 3: 实现 `player.go`**

```go
type roomBinding struct {
	roomNodeID string
	gameID     string
	currency   string
}

// SetRoomAffinity 置 room 亲和（绝对写，幂等）
func (p *Player) SetRoomAffinity(roomNodeID, gameID, currency string) {
	p.roomAffinity = &roomBinding{roomNodeID: roomNodeID, gameID: gameID, currency: currency}
}
```

- [ ] **Step 4: 改 `Gamestarted` 传 currency**

`lobby_handler.go` 的 `Gamestarted` 内：

```go
	uid, gameID, roomNodeID, currency := req.Uid, req.GameId, req.RoomNodeId, req.Currency
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: -1}, nil)
			return
		}
		p.SetRoomAffinity(roomNodeID, gameID, currency)
		h.rt.BindRoom(uid, roomNodeID, gameID)
		h.rt.PushMatchFound(uid, roomNodeID, gameID)
		replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: 0}, nil)
	})
```

并更新 matchsvr `gameStartedViaRouter` 传 currency（matchsvr `runtime.go`）：`&matchpb.RPC_GameStarted_Req{Uid: uid, GameId: gameID, RoomNodeId: roomNodeID, Currency: defaultCurrency}`，并在 matchsvr `runtime.go` 加 `const defaultCurrency = "gold"`，`openGameViaRouter` 的 `RPC_OpenGame_Req` 也带 `Currency: defaultCurrency`。`gameStartedViaRouter` / `notifyGameStarted` hook 签名增 currency 参数，链路一致。

> 具体：matchsvr `Runtime.notifyGameStarted func(lobbyNode string, uid int64, gameID, roomNodeID string) error` 改为多带 `currency string`；`gameStartedViaRouter` 与 orchestrate 调用处同步；测试 fake 同步。

- [ ] **Step 5: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ ./src/servers/matchsvr/internal/ -v`
Expected: PASS（更新既有 GameStarted/StartMatch 用例签名）。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/player.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/player_test.go src/servers/lobbysvr/internal/lobby_handler_test.go src/servers/matchsvr/internal/
git commit -m "feat: GameStarted 携 currency → lobby roomAffinity（出价 CanAfford 用）"
```

### Task F2: lobby Runtime 新字段（offlineStore / unbindRoomFn / forwardBid）+ presence 推送

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go`、`presence.go`
- Test: 由 F3/F4 覆盖

- [ ] **Step 1: 实现 Runtime 字段 + hook 默认接线**

`RuntimeConfig` 加 `OfflineStore OfflineStore`；`Runtime` 加：

```go
	offlineStore OfflineStore
	unbindRoomFn func(uid int64)
	forwardBid   func(uid int64, roomNodeID, gameID string, amount int64, done func(code int32, highest int64))
```

`NewRuntime` 内 `rt.store = cfg.Store` 邻近加 `rt.offlineStore = cfg.OfflineStore`；`if cfg.Cluster != nil { ... }` 块加：

```go
		rt.unbindRoomFn = rt.unbindRoomViaRouter
		rt.forwardBid = rt.forwardBidViaRouter
```

加方法：

```go
// unbindRoom 经注入 hook 清 online room 绑定（结算/作废调用）。
func (rt *Runtime) unbindRoom(uid int64) {
	if rt.unbindRoomFn != nil {
		rt.unbindRoomFn(uid)
	}
}

func (rt *Runtime) unbindRoomViaRouter(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_UnbindRoom_Rsp](
			ctx, cls, "onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.unbindroom", &onlinepb.RPC_UnbindRoom_Req{Uid: uid}); err != nil {
			logger.Warn("settle: unbind room failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

func (rt *Runtime) forwardBidViaRouter(uid int64, roomNodeID, gameID string, amount int64, done func(int32, int64)) {
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		rsp, err := routerclient.CallViaSync[*roompb.RPC_Bid_Rsp](
			ctx, cls, "roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, roomNodeID, "RoomHandler.bid",
			&roompb.RPC_Bid_Req{GameId: gameID, Uid: uid, Amount: amount})
		if err != nil {
			logger.Warn("lobby bid: forward failed, voiding affinity", logger.Int64("uid", uid), logger.Err(err))
			rt.Submit(func() { // room 不可达 → 轻量作废（§8.3）
				if p := rt.players[uid]; p != nil {
					p.ClearRoomAffinity()
				}
				rt.unbindRoom(uid)
			})
			done(2, 0)
			return
		}
		done(rsp.Code, rsp.HighestBid)
	}()
}

// PushAuctionState 若在线推 SC_AuctionState（off-loop）
func (rt *Runtime) PushAuctionState(uid int64, gameID string, hb, hbr int64, rem int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushAuctionState(pc, uid, gameID, hb, hbr, rem)
}

// PushAuctionResult 若在线推 SC_AuctionResult（off-loop）
func (rt *Runtime) PushAuctionResult(uid int64, gameID string, winner, price int64, currency string, itemID int32) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushAuctionResult(pc, uid, gameID, winner, price, currency, itemID)
}

// PushMatchTimeout 若在线推 SC_MatchTimeout（off-loop）
func (rt *Runtime) PushMatchTimeout(uid int64) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushMatchTimeout(pc, uid)
}
```
（import 增 `roompb "project/protocal/gen/room"`。）

`presence.go` 加常量与推送函数：

```go
const (
	msgIDSCAuctionState  uint32 = 2039
	msgIDSCAuctionResult uint32 = 2040
	msgIDSCMatchTimeout  uint32 = 2041
)

func pushAuctionState(pc presenceClient, uid int64, gameID string, hb, hbr int64, rem int32) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_AuctionState{GameId: gameID, HighestBid: hb, HighestBidder: hbr, CountdownRemaining: rem})
	if err != nil {
		logger.Warn("push auction state: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCAuctionState, body)
}

func pushAuctionResult(pc presenceClient, uid int64, gameID string, winner, price int64, currency string, itemID int32) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_AuctionResult{GameId: gameID, Winner: winner, Price: price, ItemId: itemID, Currency: currency})
	if err != nil {
		logger.Warn("push auction result: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCAuctionResult, body)
}

func pushMatchTimeout(pc presenceClient, uid int64) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_MatchTimeout{})
	if err != nil {
		logger.Warn("push match timeout: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCMatchTimeout, body)
}
```

- [ ] **Step 2: 编译校验**

Run: `go build ./src/servers/lobbysvr/...`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/presence.go
git commit -m "feat(lobby): bid 转发/unbindRoom/竞拍推送 hook + presence 推送函数"
```

### Task F3: `LobbyHandler.bid` + `LobbyHandler.auctionstate`

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestLobbyHandler_Bid(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	var forwarded atomic.Int64
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 1000)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.forwardBid = func(uid int64, room, game string, amt int64, done func(int32, int64)) {
			forwarded.Add(1)
			done(0, amt)
		}
	})
	// 余额不足
	if rsp := bidSync(t, rt, 10001, "g1", 5000); rsp.Code != 4 {
		t.Fatalf("insufficient should be code=4, got %d", rsp.Code)
	}
	// 亲和不符
	if rsp := bidSync(t, rt, 10001, "other", 10); rsp.Code != 2 {
		t.Fatalf("wrong game should be code=2, got %d", rsp.Code)
	}
	// 正常转发
	if rsp := bidSync(t, rt, 10001, "g1", 100); rsp.Code != 0 || rsp.HighestBid != 100 {
		t.Fatalf("valid bid should forward, code=%d hb=%d", rsp.Code, rsp.HighestBid)
	}
	if forwarded.Load() != 1 {
		t.Fatalf("forward should happen exactly once, got %d", forwarded.Load())
	}
}

func bidSync(t *testing.T, rt *Runtime, uid int64, game string, amount int64) *lobbypb.SC_Bid {
	return driveReq[*lobbypb.SC_Bid](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Bid(ctx, &lobbypb.CS_Bid{GameId: game, Amount: amount})
	})
}
```
（import `sync/atomic`。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_Bid -v`
Expected: FAIL（`h.Bid undefined`）。

- [ ] **Step 3: 实现**

`lobby_handler.go` 加（import 增 `roompb "project/protocal/gen/room"`）：

```go
// Bid route="LobbyHandler.bid"：校验亲和 + CanAfford → off-loop 转发 room。
func (h *LobbyHandler) Bid(ctx context.Context, req *lobbypb.CS_Bid) (*lobbypb.SC_Bid, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	gameID, amount := req.GameId, req.Amount
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil)
			return
		}
		aff := p.RoomAffinity()
		if aff == nil || aff.gameID != gameID {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil) // 非局中/亲和不符
			return
		}
		if amount <= 0 || !p.Currency().CanAfford(aff.currency, amount) {
			replyProto(replier, &lobbypb.SC_Bid{Code: 4}, nil) // 余额不足/非法额
			return
		}
		if h.rt.forwardBid == nil {
			replyProto(replier, &lobbypb.SC_Bid{Code: 2}, nil)
			return
		}
		h.rt.forwardBid(uid, aff.roomNodeID, gameID, amount, func(code int32, highest int64) {
			replyProto(replier, &lobbypb.SC_Bid{Code: code, HighestBid: highest}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Auctionstate route="LobbyHandler.auctionstate"（room→lobby Cast，无回包）：推 SC_AuctionState 给客户端。
func (h *LobbyHandler) Auctionstate(_ context.Context, req *roompb.RPC_AuctionState_Notify) {
	uid, gameID, hb, hbr, rem := req.Uid, req.GameId, req.HighestBid, req.HighestBidder, req.CountdownRemaining
	h.rt.Submit(func() { h.rt.PushAuctionState(uid, gameID, hb, hbr, rem) })
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_Bid -race -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): LobbyHandler.bid 编排 + auctionstate 推送"
```

### Task F4: `LobbyHandler.settle` + `Runtime.Settle`（在线扣发 / 离线投递 / 清亲和+unbind+push）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`、`runtime.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`、`runtime_test.go`

- [ ] **Step 1: 写失败测试（在线赢家 + 离线赢家 + 输家）**

```go
func TestLobbyHandler_SettleOnlineWinner(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	var unbound atomic.Int64
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 500)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.unbindRoomFn = func(int64) { unbound.Add(1) }
	})
	rsp := settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 10001, Price: 150, ItemId: 7, Currency: "gold"})
	if rsp.Code != 0 {
		t.Fatalf("settle should ack 0, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		p := rt.players[10001]
		if p.Currency().Balance("gold") != 350 || p.Bag().Count(7) != 1 {
			t.Fatalf("winner should be charged+granted: bal=%d item=%d", p.Currency().Balance("gold"), p.Bag().Count(7))
		}
		if p.RoomAffinity() != nil {
			t.Fatalf("affinity should be cleared")
		}
	})
	if unbound.Load() != 1 {
		t.Fatalf("unbindRoom should be called once")
	}
	// 幂等：重复结算同 gameId 不双扣
	settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 10001, Price: 150, ItemId: 7, Currency: "gold"})
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 350 {
			t.Fatalf("replay must not double-charge")
		}
	})
}

func TestLobbyHandler_SettleOfflineWinner(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() { rt.offlineStore = fos; rt.unbindRoomFn = func(int64) {} })
	// uid 未加载（离线）
	rsp := settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 20002, GameId: "g2", Winner: 20002, Price: 80, ItemId: 9, Currency: "gold"})
	if rsp.Code != 0 {
		t.Fatalf("offline settle should ack 0, got %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		if len(fos.docs[20002]) != 1 || fos.docs[20002][0].OpID != "g2" {
			t.Fatalf("offline winner should get inbox msg, got %+v", fos.docs[20002])
		}
	})
}

func TestLobbyHandler_SettleLoser(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	loadPlayerSync(t, rt, 10001)
	runOnLoop(t, rt, func() {
		rt.players[10001].Currency().Gain("seed", "gold", 500)
		rt.players[10001].SetRoomAffinity("1.7.1", "g1", "gold")
		rt.unbindRoomFn = func(int64) {}
	})
	settleSync(t, rt, &roompb.RPC_Settle_Req{Uid: 10001, GameId: "g1", Winner: 99999, Price: 150, ItemId: 7, Currency: "gold"})
	runOnLoop(t, rt, func() {
		if rt.players[10001].Currency().Balance("gold") != 500 {
			t.Fatalf("loser must not be charged")
		}
		if rt.players[10001].RoomAffinity() != nil {
			t.Fatalf("loser affinity must be cleared")
		}
	})
}

func settleSync(t *testing.T, rt *Runtime, req *roompb.RPC_Settle_Req) *roompb.RPC_Settle_Rsp {
	t.Helper()
	r := newFakeReplier()
	h := NewLobbyHandler(rt)
	if _, err := h.Settle(cluster.WithReplier(context.Background(), r), req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	rr := r.wait(t)
	var out roompb.RPC_Settle_Rsp
	if len(rr.data) > 0 {
		_ = proto.Unmarshal(rr.data, &out)
	}
	return &out
}
```
（`runtime_test.go`/`lobby_handler_test.go` import `roompb`、`cluster`、`proto`、`sync/atomic` 按需。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestLobbyHandler_Settle' -v`
Expected: FAIL（`h.Settle`/`rt.Settle`/`offlinePush` undefined）。

- [ ] **Step 3: 实现 `Runtime.Settle` + `offlinePush`**

`runtime.go`：

```go
// Settle 主循环内结算落地：清亲和 + 清 online 绑定 + 推结果；赢家在线扣发(持久 ops)、离线投 inbox。
// done(code)：0=已落地（持久落库后才 ack）；1=离线投递失败（room 重投）。§6.6 不变式。
func (rt *Runtime) Settle(uid int64, gameID string, winner, price int64, currency string, itemID int32, done func(int32)) {
	p := rt.players[uid]
	if p != nil {
		p.ClearRoomAffinity()
	}
	rt.unbindRoom(uid)
	rt.PushAuctionResult(uid, gameID, winner, price, currency, itemID)
	if uid != winner {
		done(0) // 输家无经济副作用
		return
	}
	if p != nil { // 在线赢家
		if price > 0 {
			if _, ok := p.Currency().Spend(gameID, currency, price); ok {
				rt.PublishCurrencyChanged(uid, currency, -price)
			} else {
				logger.Warn("settle online: insufficient, item granted, charge waived",
					logger.Int64("uid", uid), logger.String("gameId", gameID))
			}
		}
		p.Bag().Add(gameID, itemID, 1)
		rt.flushPlayer(uid, p, func() { done(0) }) // 落库后才 ack
		return
	}
	rt.offlinePush(uid, OfflineMsg{Type: OfflineMsgSettle, OpID: gameID, Price: price, Currency: currency, ItemID: itemID}, done)
}

// offlinePush off-loop 投递离线消息，push 成功才 ack(0)，失败 ack(1) 令 room 重投。
func (rt *Runtime) offlinePush(uid int64, msg OfflineMsg, done func(int32)) {
	if rt.offlineStore == nil {
		done(0)
		return
	}
	rt.offlineStore.Push(rt.tq, uid, msg, func(err error) {
		if err != nil {
			logger.Warn("settle offline push failed", logger.Int64("uid", uid), logger.String("gameId", msg.OpID), logger.Err(err))
			done(1)
			return
		}
		done(0)
	})
}
```

- [ ] **Step 4: 实现 `LobbyHandler.settle`**

`lobby_handler.go`：

```go
// Settle route="LobbyHandler.settle"（room→lobby CallViaSync）：结算落地，ack 经持久落库后发出（§6.6）。
func (h *LobbyHandler) Settle(ctx context.Context, req *roompb.RPC_Settle_Req) (*roompb.RPC_Settle_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid, gameID, winner, price, currency, itemID := req.Uid, req.GameId, req.Winner, req.Price, req.Currency, req.ItemId
	h.rt.Submit(func() {
		h.rt.Settle(uid, gameID, winner, price, currency, itemID, func(code int32) {
			replyProto(replier, &roompb.RPC_Settle_Rsp{Code: code}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 5: 运行验证通过（含 -race）**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestLobbyHandler_Settle' -race -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime_test.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): 结算落地（在线扣发持久幂等/离线投 inbox/清亲和+unbind+push，落库后 ack）"
```

### Task F5: 离线消息登录重放（`replayOffline`，replay 先于玩家可操作）

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go`（`Login` 链 + `replayOffline` + `applyOfflineMsg`）
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试（离线投递 → 重登补发 + 跨路不双发）**

```go
func TestRuntime_OfflineReplayOnLogin(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		fos.docs[30003] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g3", Price: 60, Currency: "gold", ItemID: 5}}
		// 预置余额：玩家离线前余额（doc），这里直接写 fakeStore doc
		seedPlayerDoc(rt, 30003, "gold", 200)
	})
	loadPlayerSync(t, rt, 30003)
	runOnLoop(t, rt, func() {
		p := rt.players[30003]
		if p.Currency().Balance("gold") != 140 || p.Bag().Count(5) != 1 {
			t.Fatalf("replay should charge+grant: bal=%d item=%d", p.Currency().Balance("gold"), p.Bag().Count(5))
		}
		if len(fos.docs[30003]) != 0 {
			t.Fatalf("inbox should be acked(pulled) after replay")
		}
	})
}
```
（`seedPlayerDoc` 测试钩子：向 fakeStore 写一份 `PlayerDoc{ID:uid, Currency:{Balances:{kind:amt}}}`，加在 `store_test.go`。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_OfflineReplayOnLogin -v`
Expected: FAIL（`replayOffline` 未接入 Login，inbox 不被消费）。

- [ ] **Step 3: 实现**

`runtime.go` 的 `Login`：把 `rt.onlineRegister(...)` + `reply(...)` + `scanFriendAccepts(...)` 包进 `replayOffline` 的 after：

```go
		rt.players[uid] = buildPlayer(uid, doc)
		rt.players[uid].attachMail(rt.mailStore)
		rt.events.PlayerLoaded.Publish(PlayerLoaded{UID: uid})
		p := rt.players[uid]
		rt.replayOffline(uid, p, func() { // 重放先于放行：维持 CanAfford⇒Spend必成
			rt.onlineRegister(uid, gatewayNodeID)
			reply(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: rt.nodeID}, nil)
			rt.scanFriendAccepts(uid, p, func() {
				if rt.presence != nil {
					friends := p.Friend().List()
					go fanoutPresence(rt.presence, uid, friends, true)
				}
			})
		})
```

加方法：

```go
// replayOffline 登录链：加载离线消息逐条重放（Spend+Add ops=opID），落库后 $pull，再调 after。
func (rt *Runtime) replayOffline(uid int64, p *Player, after func()) {
	if rt.offlineStore == nil {
		after()
		return
	}
	rt.offlineStore.Load(rt.tq, uid, func(msgs []OfflineMsg, err error) {
		if err != nil {
			logger.Warn("replay offline: load failed", logger.Int64("uid", uid), logger.Err(err))
			after()
			return
		}
		if len(msgs) == 0 {
			after()
			return
		}
		opIDs := make([]string, 0, len(msgs))
		for _, m := range msgs {
			rt.applyOfflineMsg(uid, p, m)
			opIDs = append(opIDs, m.OpID)
		}
		rt.flushPlayer(uid, p, func() { // 落库后才 $pull（§6.6）
			rt.offlineStore.Ack(rt.tq, uid, opIDs, func(aerr error) {
				if aerr != nil {
					logger.Warn("replay offline: ack failed", logger.Int64("uid", uid), logger.Err(aerr))
				}
			})
			after()
		})
	})
}

// applyOfflineMsg 重放一条离线消息（持久 ops 去重，跨路/重投幂等）。
func (rt *Runtime) applyOfflineMsg(uid int64, p *Player, m OfflineMsg) {
	switch m.Type {
	case OfflineMsgSettle:
		if m.Price > 0 {
			if _, ok := p.Currency().Spend(m.OpID, m.Currency, m.Price); ok {
				rt.PublishCurrencyChanged(uid, m.Currency, -m.Price)
			} else {
				logger.Warn("replay offline settle: insufficient, item granted, charge waived",
					logger.Int64("uid", uid), logger.String("gameId", m.OpID))
			}
		}
		p.Bag().Add(m.OpID, m.ItemID, 1)
	default:
		logger.Warn("replay offline: unknown msg type", logger.Int64("uid", uid), logger.String("type", m.Type))
	}
}
```

- [ ] **Step 4: 运行验证通过（含 -race）**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_OfflineReplay -race -v`
Expected: PASS。

- [ ] **Step 5: 跨路双发用例（持久 ops 防护）**

追加测试：online 已扣（持久 ops=g4 落 fakeStore），再以 inbox 重放同 gameId → 不双扣：

```go
func TestRuntime_OfflineReplaySkipsAlreadyApplied(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		seedPlayerDocWithOp(rt, 40004, "gold", 100, "g4") // 余额已扣后状态 + ops 含 g4
		fos.docs[40004] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g4", Price: 60, Currency: "gold", ItemID: 5}}
	})
	loadPlayerSync(t, rt, 40004)
	runOnLoop(t, rt, func() {
		if rt.players[40004].Currency().Balance("gold") != 100 {
			t.Fatalf("already-applied gameId must not double-charge, bal=%d", rt.players[40004].Currency().Balance("gold"))
		}
	})
}
```
（`seedPlayerDocWithOp`：写 `PlayerDoc{Currency:{Balances:{gold:100}, Ops:["g4"]}}`。）

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_OfflineReplaySkips -race -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go src/servers/lobbysvr/internal/store_test.go
git commit -m "feat(lobby): 离线消息登录重放（replay 先于放行 + 持久 ops 跨路去重）"
```

### Task F6: `LobbyHandler.matchtimeout` + lobby main 接 offlineStore

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`、`src/servers/lobbysvr/main.go`（或 module 装配处）
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestLobbyHandler_MatchTimeout(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var pushed atomic.Int64
	rt.presence = fakePresence{pushFn: func(uid int64, msgID uint32) {
		if msgID == 2041 {
			pushed.Add(1)
		}
	}}
	h := NewLobbyHandler(rt)
	h.Matchtimeout(context.Background(), &matchpb.RPC_MatchTimeout_Notify{Uid: 10001, ReqId: "r1"})
	runOnLoop(t, rt, func() {})
	time.Sleep(50 * time.Millisecond)
	if pushed.Load() != 1 {
		t.Fatalf("match timeout should push SC_MatchTimeout once")
	}
}
```
（`fakePresence` 若不存在则在测试中定义一个最小 `presenceClient`：`Query` 恒返回 `("0.1.1", true)`，`Push` 调 `pushFn(uid, msgID)`。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_MatchTimeout -v`
Expected: FAIL（`h.Matchtimeout undefined`）。

- [ ] **Step 3: 实现 handler**

`lobby_handler.go`（import 已有 `matchpb`）：

```go
// Matchtimeout route="LobbyHandler.matchtimeout"（matchsvr→lobby Cast）：推 SC_MatchTimeout。
func (h *LobbyHandler) Matchtimeout(_ context.Context, req *matchpb.RPC_MatchTimeout_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.PushMatchTimeout(uid) })
}
```

- [ ] **Step 4: lobby main 接 offlineStore**

`src/servers/lobbysvr/main.go`（或 `NewRuntime` 装配处）在已建 `mongo.Client`（与 Store/MailStore 同源）后传：

```go
	OfflineStore: internal.NewMongoOfflineStore(mongoCli),
```
（与既有 `Store: internal.NewMongoStore(mongoCli)` / `MailStore: internal.NewMongoMailStore(mongoCli)` 并列。）

- [ ] **Step 5: 运行验证通过 + 编译**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_MatchTimeout -v && go build ./src/servers/lobbysvr/...`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/main.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): matchtimeout 推送 + main 接 OfflineStore"
```

---

## Stage G — matchsvr 双发/超时封堵

### Task G1: `pendingUids` 封残余双发窗口

**Files:**
- Modify: `src/servers/matchsvr/internal/queue.go`、`runtime.go`
- Test: `src/servers/matchsvr/internal/queue_test.go`、`runtime_test.go`

- [ ] **Step 1: 写失败测试**

`queue_test.go`：

```go
func TestMatchQueue_PendingBlocksDoubleSend(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "a", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "b", mmr: 1000})
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("should form table")
	}
	// 成桌后 uid 进 pending：同 uid 新 reqId 应被拒
	if q.Enqueue(waiting{uid: 1, reqID: "c", mmr: 1000}) {
		t.Fatalf("uid in pending must be rejected (double-send window)")
	}
	// 清 pending 后可再入
	q.clearPending(1)
	if !q.Enqueue(waiting{uid: 1, reqID: "c", mmr: 1000}) {
		t.Fatalf("after clearPending, uid can re-enqueue")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/matchsvr/internal/ -run TestMatchQueue_PendingBlocks -v`
Expected: FAIL（`pendingUids`/`clearPending` undefined）。

- [ ] **Step 3: 实现 `queue.go`**

`matchQueue` 加字段 `pendingUids map[int64]bool`；`newMatchQueue` 初始化 `pendingUids: make(map[int64]bool)`。`Enqueue` 在 `waitingUids` 检查后加：

```go
	if q.pendingUids[w.uid] {
		return false // 成桌→回告未落定期间的再次发起：封堵残余双发窗口
	}
```

`removeAll` 把 `delete(q.waitingUids, w.uid)` 改为转入 pending：

```go
		delete(q.waitingUids, w.uid)
		q.pendingUids[w.uid] = true // 编排中
```

加 `clearPending`，`Requeue` 先清 pending：

```go
// clearPending 编排成功后清除 uid 的 pending 守护（GameStarted 已 ack ⇒ lobby roomAffinity 已置）。
func (q *matchQueue) clearPending(uid int64) { delete(q.pendingUids, uid) }
```
`Requeue` 改：

```go
func (q *matchQueue) Requeue(w waiting) {
	delete(q.pendingUids, w.uid)
	q.waitingUids[w.uid] = true
	q.waitingList = append(q.waitingList, w)
}
```

- [ ] **Step 4: `runtime.go` orchestrate 成功清 pending**

`orchestrate` 的 go func，成功路径（notifyGameStarted 循环之后）加：

```go
		rt.Submit(func() {
			for _, w := range table {
				rt.queue.clearPending(w.uid)
			}
		})
```
（失败路径已走 `rt.queue.Requeue(w)`，已清 pending。）

- [ ] **Step 5: 运行验证通过**

Run: `go test ./src/servers/matchsvr/internal/ -race -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/matchsvr/internal/queue.go src/servers/matchsvr/internal/runtime.go src/servers/matchsvr/internal/queue_test.go
git commit -m "feat(match): pendingUids 封残余双发窗口（成桌→回告未落定）"
```

### Task G2: 匹配等待超时 reap + 回告

**Files:**
- Modify: `src/servers/matchsvr/internal/queue.go`、`runtime.go`
- Test: `src/servers/matchsvr/internal/queue_test.go`、`runtime_test.go`

- [ ] **Step 1: 写失败测试**

`queue_test.go`：

```go
func TestMatchQueue_ReapExpired(t *testing.T) {
	q := newMatchQueue(2, 200)
	now := time.Now()
	q.enqueueAt(waiting{uid: 1, reqID: "a", mmr: 1000}, now.Add(-10*time.Second))
	q.enqueueAt(waiting{uid: 2, reqID: "b", mmr: 1000}, now)
	expired := q.ReapExpired(now, 5*time.Second)
	if len(expired) != 1 || expired[0].uid != 1 {
		t.Fatalf("only uid 1 should expire, got %+v", expired)
	}
	if q.waitingUids[1] {
		t.Fatalf("expired uid removed from waitingUids")
	}
	if !q.seen[dedupKey(1, "a")] {
		t.Fatalf("seen retained to block reqId replay")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/matchsvr/internal/ -run TestMatchQueue_ReapExpired -v`
Expected: FAIL（`enqueueAt`/`ReapExpired` undefined）。

- [ ] **Step 3: 实现 `queue.go`**

`waiting` 加 `enqueuedAt time.Time`（import `time`）。`Enqueue` 设 `w.enqueuedAt = time.Now()`（在 append 前）；`Requeue` 同设。加测试用 `enqueueAt`（注入时间）与 `ReapExpired`：

```go
// enqueueAt 测试用：以指定入队时间入队（绕过 time.Now）。
func (q *matchQueue) enqueueAt(w waiting, at time.Time) bool {
	k := dedupKey(w.uid, w.reqID)
	if q.seen[k] || q.waitingUids[w.uid] || q.pendingUids[w.uid] {
		return false
	}
	w.enqueuedAt = at
	q.seen[k] = true
	q.waitingUids[w.uid] = true
	q.waitingList = append(q.waitingList, w)
	return true
}

// ReapExpired 移除等待超过 maxWait 的玩家（保留 seen 防同 reqId 复活），返回被移除者。
func (q *matchQueue) ReapExpired(now time.Time, maxWait time.Duration) []waiting {
	if len(q.waitingList) == 0 {
		return nil
	}
	var expired []waiting
	kept := q.waitingList[:0]
	for _, w := range q.waitingList {
		if now.Sub(w.enqueuedAt) >= maxWait {
			expired = append(expired, w)
			delete(q.waitingUids, w.uid)
		} else {
			kept = append(kept, w)
		}
	}
	q.waitingList = kept
	return expired
}
```
并把既有 `Enqueue` 改为复用 `enqueueAt`：

```go
func (q *matchQueue) Enqueue(w waiting) bool { return q.enqueueAt(w, time.Now()) }
```
`Requeue` 设 `w.enqueuedAt = time.Now()`（重置等待）。

- [ ] **Step 4: `runtime.go` 加 reap tick + notifyTimeout hook**

`RuntimeConfig` 可加 `MaxWait time.Duration`；`Runtime` 加：

```go
	maxWait      time.Duration
	notifyTimeout func(lobbyNode string, uid int64, reqID string) error
```
`NewRuntime`：`if cfg.MaxWait <= 0 { cfg.MaxWait = 30 * time.Second }`；`rt.maxWait = cfg.MaxWait`；`if cfg.Cluster != nil { rt.notifyTimeout = rt.timeoutViaRouter }`。`Start` 在 `go rt.loop()` 前注册 tick：

```go
func (rt *Runtime) Start() {
	rt.tw.Tick(reapInterval, rt.reapExpired)
	go rt.loop()
}
```
加：

```go
const reapInterval = 1 * time.Second

func (rt *Runtime) reapExpired() {
	expired := rt.queue.ReapExpired(time.Now(), rt.maxWait)
	if len(expired) == 0 || rt.notifyTimeout == nil {
		return
	}
	nt := rt.notifyTimeout
	rt.inflight.Add(1)
	go func() {
		defer func() { rt.inflight.Add(-1); rt.Submit(func() {}) }()
		for _, w := range expired {
			if err := nt(w.lobbyNode, w.uid, w.reqID); err != nil {
				logger.Warn("match timeout notify failed", logger.Int64("uid", w.uid), logger.Err(err))
			}
		}
	}()
}

func (rt *Runtime) timeoutViaRouter(lobbyNode string, uid int64, reqID string) error {
	node, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		return err
	}
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	return rt.cls.Cast(ctx, node, "LobbyHandler.matchtimeout", &matchpb.RPC_MatchTimeout_Notify{Uid: uid, ReqId: reqID})
}
```
（`reapExpired` 在主循环（tw.Advance 回调）执行，读 queue 安全；扇出 off-loop。）

- [ ] **Step 5: 写 runtime 级测试（注 fake notifyTimeout，验超时回告）**

`runtime_test.go`：

```go
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
```
（沿用 matchsvr 既有 `matchRunOnLoop` helper；若无则仿 roomRunOnLoop 加。）

- [ ] **Step 6: 运行验证通过（含 -race）**

Run: `go test ./src/servers/matchsvr/internal/ -race -v`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add src/servers/matchsvr/internal/queue.go src/servers/matchsvr/internal/runtime.go src/servers/matchsvr/internal/queue_test.go src/servers/matchsvr/internal/runtime_test.go
git commit -m "feat(match): 等待超时 reap + matchtimeout 回告（cancel 语义）"
```

---

## Stage H — 集成骨架 + 文档 + 全量回归

### Task H1: 集成测试骨架（build-tag，t.Skip）

**Files:**
- Create/Modify: `src/servers/roomsvr/internal/room_integration_test.go`（`//go:build integration`）

- [ ] **Step 1: 写编译验证骨架**

```go
//go:build integration

package internal

import "testing"

// TestIntegration_AuctionSettle 凑桌→开局→出价→广播→结算→重登读回；离线赢家→inbox→重登补发。
// 沙箱无 Docker，仅编译验证（umbrella D10）；实跑需容器 NATS+etcd+MongoDB。
func TestIntegration_AuctionSettle(t *testing.T) {
	t.Skip("requires Docker: NATS+etcd+MongoDB; compile-only in sandbox")
}
```

- [ ] **Step 2: 编译验证**

Run: `go vet -tags integration ./src/servers/...`
Expected: PASS。

- [ ] **Step 3: Commit**

```bash
git add src/servers/roomsvr/internal/room_integration_test.go
git commit -m "test(room): P4b 集成骨架（build-tag，沙箱仅编译验证）"
```

### Task H2: 文档同步

**Files:**
- Modify: `architecture.md`、`cluster.md`、`development.md`

- [ ] **Step 1: 更新**

- `architecture.md`：roomsvr 段补"竞拍状态机（bid/settle/广播）"；lobby 段补"持久 ops、离线消息机制（offline_messages collection，登录重放）、结算落地、大厅禁购"。
- `cluster.md`：补 P4b 路由（`RoomHandler.bid`、`LobbyHandler.{auctionstate,settle,matchtimeout}`、`OnlineHandler.unbindroom` 接线）。
- `development.md`：新 collection `offline_messages`；proto msg_id 2037-2041。

- [ ] **Step 2: Commit**

```bash
git add architecture.md cluster.md development.md
git commit -m "docs: P4b 同步架构/集群/开发文档（竞拍/结算/离线消息）"
```

### Task H3: 全量回归 + 终审

- [ ] **Step 1: 全量构建/静态/竞态**

Run:
```bash
go build ./... && go vet ./... && go vet -tags integration ./... && go test ./... -race
```
Expected: 全绿。

- [ ] **Step 2: 终审清单（核心/并发任务走 opus spec+质量双评审）**

- §6.6 不变式落实：结算在线 ack 在 `flushPlayer` after、离线 push 成功才 ack、replay `$pull` 在 flush after、mail-claim mark 在 flush after。
- 持久 ops 跨路：在线已扣 + inbox 重放同 gameId 不双扣（`TestRuntime_OfflineReplaySkipsAlreadyApplied`）。
- timewheel settle 回调在主循环（§3），off-loop 仅网络 IO；`-race` 干净。
- pendingUids 生命周期：成桌入、成功清、失败 Requeue 清。
- D6：对局期 Purchase 拒、余额零动。

- [ ] **Step 3: push 开 code PR**（docs PR：spec+plan 单独一个；本 code PR 另开）

```bash
git fetch origin && git rebase origin/main   # 执行期 main 可能前进
go build ./... && go test ./... -race        # rebase 后复验
git push -u origin feat/p4b-auction-settlement
```
（沙箱无 gh/token → push 后在 GitHub UI 手动开 PR。）

---

## Self-Review（计划对 Spec 覆盖核对）

- **Spec §1 范围内**：竞拍状态机=E1/E2/E3；出价路径=F3+E3；结算闭环=F4+E3(E5)；持久 ops=B1/B2/B3；离线消息=C1/C2/F4/F5；D6 禁购=D1；matchsvr 封堵=G1/G2；unbindRoom 接线=F2/F4；proto=A1。✓
- **Spec §6.6 不变式**：F4(在线 ack 在 flush after / 离线 push 成功才 ack)、F5(replay $pull 在 flush after)、D2(mark 在 flush after)。✓
- **Spec §7 proto 2037-2041 + room/match 扩**：A1。✓
- **Spec §8.2 幂等表**：持久 ops(B)、离线 $pull(C2/F5)、mail-claim(D2)、pendingUids(G1)、UnbindRoom(F4)。✓
- **Spec §8.3 失败路径**：bid 转发失败→作废(F2 forwardBidViaRouter)；settle 重试(E3 settleViaRouter)；离线 push 失败 done(1)(F4)。✓
- **Spec §10 测试**：roomsvr(E1-E3)、lobby(D1/D2/F3-F5)、matchsvr(G1/G2)、mongo(C1)、跨路防护(F5)、集成骨架(H1)。✓
- **类型一致性**：`OpenGame(...,currency,parts)`/`Bid(gameID,uid,amount)`/`Settle(uid,gameID,winner,price,currency,itemID,done)`/`SetRoomAffinity(room,game,currency)`/`grantAttachments(uid,p,opID,atts)`/`OfflineMsg{Type,OpID,Price,Currency,ItemID}` 各任务签名一致。✓
- **占位扫描**：无 TODO/TBD；每代码步含完整代码。✓

> 已知 MVP 取舍（Spec §9 记录，非计划缺口）：持久 ops 环淘汰极迟重投、room 轻量作废依赖 bid-failure 触发、settle 重试耗尽后丢（D4 作废）、mail mark 滞后 UI 瑕疵。重连接回/改投/robust room-death 探测=P4c。

---

## Execution Handoff

计划已存 `docs/plans/2026-06-04-P4b-auction-settlement-impl-plan.md`。两种执行方式：

1. **Subagent-Driven（推荐）** — 每任务派新 subagent，任务间双阶段评审（核心/并发任务 spec+质量双评审 + 整支 `-race` opus 终审），沿 P4a 节奏。
2. **Inline Execution** — 本会话内分批执行 + checkpoint。

选哪种？
