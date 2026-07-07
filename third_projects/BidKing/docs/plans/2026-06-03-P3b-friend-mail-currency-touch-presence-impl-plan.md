# P3b 实现计划：好友/邮件/货币组件 + Touch/presence 接线 + flush 加固

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 P3a 的 lobby 单主循环 + EC 框架之上，落地货币/好友/邮件三组件、心跳→Touch 接线、Hybrid presence、跨组件购买编排与 flush 加固。

**Architecture:** 基石「players 文档单写者（绝对 `$set`）+ 一切跨玩家经 `mailbox` 集合（insert-only）」。货币/好友为内嵌 flush 组件；邮件为 mailbox 后端组件（不入 flush）。心跳经 lobby 中转节流 Touch onlinesvr；presence 由 lobby 驱动 fan-out（pull 快照 + 登录/断连 push delta），复用泛化的 gate 推送。购买走主循环显式编排，事件总线只承载 `CurrencyChanged` 副作用。详见设计 Spec [`2026-06-03-P3b-friend-mail-currency-touch-presence.md`](2026-06-03-P3b-friend-mail-currency-touch-presence.md)。

**Tech Stack:** Go、protobuf（集群 wire）、BSON（存储）、MongoDB driver、NATS cluster、etcd discovery。模块名 `project`。

---

## 关键既定事实（实现前必读）

- **gen_routes 约定**：请求消息须带 `option (options.msg_id)`（**奇数**）+ `server_type` + `handler_method`；响应须在 **req msg_id + 1**（偶数）。`gen_routes` 据此生成 `MsgRouteTable`/`ForwardTable`/`RespMsgIDTable`。纯推送消息（`SC_FriendPresence`/`SC_MailNew`）**只给 msg_id、不给 server_type/handler_method** → 不入任何表，仅作客户端路由用，Go 侧以常量引用其 msg_id。
- **集群 RPC 序列化**：发送端硬编码 `proto.Marshal`。后端服务（lobby/online）序列化器是 protobuf；**gate registry 序列化器是 json**。故 gate 接收集群 Cast 的 handler 用 `func(ctx, []byte)` raw 签名 + 手动 `proto.Unmarshal`（见既有 `GateHandler.KickSession`）。
- **gate 推送 body**：客户端连接侧用 json。lobby 产出的客户端推送 body 须用 **client 序列化器（`project/src/common/serialize/json`）** marshal，gate 仅按 uid 透传不透明字节。
- **延迟回包**：lobby handler 捕获 `cluster.ReplierFromCtx(ctx)` + `cluster.SessionFromCtx(ctx).Uid`，`Submit` 进主循环后返回 `cluster.ErrDeferredReply`；主循环内经 `Replier.Reply(data, err)` 回包。Notify（无回包）只 `Submit` 不返回哨兵。
- **off-loop IO 模式**：Mongo 异步经 `mongo.Client` 方法（回调走 dispatcher 回主循环）；出站 cluster 同步 RPC（`routerclient.CallViaSync` / `cls.CastRaw`）放 off-loop goroutine，best-effort，不阻塞主循环（见既有 `Runtime.registerOnline`）。
- **protoc 生成**（沙箱借用，见 dev-workflow 记忆）：
  ```bash
  PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
  INC=/game/dev/silver-server/3rd/protobuf/include
  $PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$INC protocal/lobby.proto protocal/gate.proto
  go run ./tools/gen_routes
  ```
- **沙箱无 Docker**：依赖 Mongo/NATS/etcd 的集成测试 `//go:build integration`，仅 `go vet -tags integration ./...` 编译验证；实跑在有 Docker 的机器。
- **分支**：本计划在 feature 分支实现（如 `feat/p3b-...`），PR 合入 main；合入前 rebase 最新 origin/main。每个 Step 5 的 commit 用 Conventional Commits + 中文。

---

## 文件结构总览

**新建：**
- `src/servers/lobbysvr/internal/op_dedup.go`(+`_test`) — 共享 op-id 去重 helper（抽自 Bag）
- `src/servers/lobbysvr/internal/component_currency.go`(+`_test`) — 货币组件
- `src/servers/lobbysvr/internal/component_friend.go`(+`_test`) — 好友组件
- `src/servers/lobbysvr/internal/mailbox_store.go`(+`_test`) — MailDoc/Attachment + MailStore 接口 + mongoMailStore
- `src/servers/lobbysvr/internal/component_mail.go`(+`_test`) — Mail 组件（mailbox 后端，不入 flush）
- `src/servers/lobbysvr/internal/presence.go`(+`_test`) — presence/push 的 off-loop fan-out helper

**修改：**
- `src/common/mongo/mongo.go` — 新增 `InsertOne`/`Find`/`FindOneAndUpdate`
- `protocal/lobby.proto`、`protocal/gate.proto`（+ 重新生成 pb 与 routes）
- `src/servers/lobbysvr/internal/component_bag.go` — 改用 `opDedup`
- `src/servers/lobbysvr/internal/player.go` — `mail` 字段、`lastTouchAt`
- `src/servers/lobbysvr/internal/store.go` — `PlayerDoc.currency/friend`、`buildPlayer`、`Currency/Friend/Mail` 访问器
- `src/servers/lobbysvr/internal/events.go` — `CurrencyChanged`
- `src/servers/lobbysvr/internal/runtime.go` — Touch、flushSoon、停机 drain、push fan-out、mailStore、事件订阅
- `src/servers/lobbysvr/internal/lobby_handler.go` — 新 handler 方法
- `src/servers/lobbysvr/main.go` — Mongo/mailStore/事件订阅 装配
- `src/servers/gatesvr/internal/gate_handler.go` — Heartbeat 转发 Touch、`PushToClient`
- `src/servers/gatesvr/main.go`（如需）— 注册 `PushToClient` route

---

## Stage A — 框架地基（mongo 异步原语 + opDedup）

### Task A1: mongo 新增 InsertOne / Find / FindOneAndUpdate

**Files:**
- Modify: `src/common/mongo/mongo.go`
- Test: `src/common/mongo/mongo_test.go`

mailbox 需要：插入投递、按 `to` 查列表、原子领取。`FindByID`/`UpsertSetByID` 不够，补三个异步方法（同款 `runAsync` 回调投递语义）。真实 Mongo 行为由集成测试覆盖（沙箱编译验证）；单测验证 dispatcher 投递契约。

- [ ] **Step 1: 写失败测试**（验证三方法都经 dispatcher 投递回调；用 nil client 不触发真实 IO，仅断言「回调在队列里执行」需要真实 mongo，故此处只测签名+plumbing 不可行 → 改测 `runAsync` 已有覆盖，三新方法补**编译期签名测试**）

```go
// 追加到 src/common/mongo/mongo_test.go
// 编译期断言三新方法签名存在且类型正确（真实行为由 lobbysvr 集成测试覆盖）。
func TestMongoMethodSignatures_Compile(t *testing.T) {
	var c *Client
	var d taskqueue.Dispatcher
	_ = func() {
		c.InsertOne(d, "coll", struct{}{}, func(id any, err error) {})
		c.Find(d, "coll", bson.M{"to": int64(1)}, bson.D{{Key: "ts", Value: -1}}, 50, &[]struct{}{}, func(err error) {})
		c.FindOneAndUpdate(d, "coll", bson.M{"_id": 1}, bson.M{"$set": bson.M{"claimed": true}}, true, &struct{}{}, func(found bool, err error) {})
	}
	_ = c
}
```

（在 import 增加 `"go.mongodb.org/mongo-driver/bson"`。）

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/common/mongo/ -run TestMongoMethodSignatures_Compile`
Expected: 编译失败 `c.InsertOne undefined`。

- [ ] **Step 3: 实现三方法**

```go
// 追加到 src/common/mongo/mongo.go（import 增加 "go.mongodb.org/mongo-driver/mongo/options" 已有）

// InsertOne 异步插入单文档；done(insertedID, err) 在 dispatcher 执行。
func (c *Client) InsertOne(d taskqueue.Dispatcher, coll string, doc any, done func(insertedID any, err error)) {
	var insertedID any
	runAsync(d, func() error {
		res, err := c.db.Collection(coll).InsertOne(context.Background(), doc)
		if err != nil {
			return err
		}
		insertedID = res.InsertedID
		return nil
	}, func(err error) { done(insertedID, err) })
}

// Find 异步按 filter 查多条，sort/limit 可选（limit<=0 不限），结果解码进 out（指向切片的指针）；
// done(err) 在 dispatcher 执行。
func (c *Client) Find(d taskqueue.Dispatcher, coll string, filter bson.M, sort bson.D, limit int64, out any, done func(error)) {
	runAsync(d, func() error {
		opt := options.Find()
		if len(sort) > 0 {
			opt.SetSort(sort)
		}
		if limit > 0 {
			opt.SetLimit(limit)
		}
		cur, err := c.db.Collection(coll).Find(context.Background(), filter, opt)
		if err != nil {
			return err
		}
		return cur.All(context.Background(), out)
	}, done)
}

// FindOneAndUpdate 异步原子「查并更新」：匹配 filter 的单文档应用 update，
// returnUpdated=true 时 out 解码为更新后文档，否则更新前。无匹配时 found=false、err=nil。
// done(found, err) 在 dispatcher 执行。
func (c *Client) FindOneAndUpdate(d taskqueue.Dispatcher, coll string, filter bson.M, update bson.M, returnUpdated bool, out any, done func(found bool, err error)) {
	found := false
	runAsync(d, func() error {
		opt := options.FindOneAndUpdate()
		if returnUpdated {
			opt.SetReturnDocument(options.After)
		}
		err := c.db.Collection(coll).FindOneAndUpdate(context.Background(), filter, update, opt).Decode(out)
		if errors.Is(err, driver.ErrNoDocuments) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	}, func(err error) { done(found, err) })
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/common/mongo/ && go vet ./src/common/mongo/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/common/mongo/mongo.go src/common/mongo/mongo_test.go
git commit -m "feat(mongo): 新增 InsertOne/Find/FindOneAndUpdate 异步原语（供 mailbox）"
```

### Task A2: opDedup 共享 helper + Bag 改用

**Files:**
- Create: `src/servers/lobbysvr/internal/op_dedup.go`
- Test: `src/servers/lobbysvr/internal/op_dedup_test.go`
- Modify: `src/servers/lobbysvr/internal/component_bag.go`

把 Bag 的 `recentOps`/`opOrder`/`rememberOp` 抽成可复用 `opDedup`（Currency 也要用）。拆 `seen`/`remember` 两步：`Spend` 需「成功才 remember」（失败不记，留重试余地）。空 `opID` 永不去重。

- [ ] **Step 1: 写失败测试**

```go
// src/servers/lobbysvr/internal/op_dedup_test.go
package internal

import "testing"

func TestOpDedup_SeenRemember(t *testing.T) {
	o := newOpDedup(3)
	if o.seen("a") {
		t.Fatal("unseen should be false")
	}
	o.remember("a")
	if !o.seen("a") {
		t.Fatal("remembered should be seen")
	}
}

func TestOpDedup_EmptyNeverDedups(t *testing.T) {
	o := newOpDedup(3)
	o.remember("")
	if o.seen("") {
		t.Fatal("empty opID must never be seen")
	}
}

func TestOpDedup_BoundedEviction(t *testing.T) {
	o := newOpDedup(2)
	o.remember("a")
	o.remember("b")
	o.remember("c") // 淘汰最旧 a
	if o.seen("a") {
		t.Fatal("a should be evicted")
	}
	if !o.seen("b") || !o.seen("c") {
		t.Fatal("b,c should remain")
	}
}

func TestOpDedup_RememberIdempotent(t *testing.T) {
	o := newOpDedup(2)
	o.remember("a")
	o.remember("a") // 重复 remember 不重复入队（否则环计数错乱）
	o.remember("b")
	if !o.seen("a") || !o.seen("b") {
		t.Fatal("a,b should both remain (no double-count)")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup`
Expected: 编译失败 `newOpDedup undefined`。

- [ ] **Step 3: 实现 opDedup**

```go
// src/servers/lobbysvr/internal/op_dedup.go
package internal

// opDedup 有界 op-id 去重环：seen/remember 两步分离，调用方决定何时 remember
// （如「仅操作成功才记」），空 opID 永不去重。仅主循环用，零锁。
type opDedup struct {
	seenSet map[string]struct{}
	order   []string
	max     int
}

func newOpDedup(max int) *opDedup {
	if max <= 0 {
		max = 128
	}
	return &opDedup{seenSet: make(map[string]struct{}), max: max}
}

// seen 报告 opID 是否已记录（空 opID 恒 false）。
func (o *opDedup) seen(opID string) bool {
	if opID == "" {
		return false
	}
	_, ok := o.seenSet[opID]
	return ok
}

// remember 记录 opID 并维护有界环（超界淘汰最旧）；空或重复 opID 是 no-op。
func (o *opDedup) remember(opID string) {
	if opID == "" {
		return
	}
	if _, ok := o.seenSet[opID]; ok {
		return
	}
	o.seenSet[opID] = struct{}{}
	o.order = append(o.order, opID)
	if len(o.order) > o.max {
		old := o.order[0]
		o.order = o.order[1:]
		delete(o.seenSet, old)
	}
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup`
Expected: PASS。

- [ ] **Step 5: Bag 改用 opDedup（保持 Bag 行为不变）**

把 `component_bag.go` 中 `recentOps map[string]struct{}` / `opOrder []string` / `rememberOp` 替换为 `ops *opDedup`：

```go
// 改 Bag 结构体
type Bag struct {
	items map[int32]int32
	ops   *opDedup
	dirty bool
}

func NewBag() *Bag {
	return &Bag{items: make(map[int32]int32), ops: newOpDedup(maxRecentOps)}
}

// 改 Add：seen→直接返回；apply 后 remember
func (b *Bag) Add(opID string, itemID, count int32) int32 {
	if b.ops.seen(opID) {
		return b.items[itemID]
	}
	if count != 0 {
		b.items[itemID] += count
		if b.items[itemID] <= 0 {
			delete(b.items, itemID)
		}
		b.dirty = true
	}
	b.ops.remember(opID)
	return b.items[itemID]
}
```

删除 `rememberOp` 方法。`maxRecentOps = 128` 常量保留。

- [ ] **Step 6: 运行验证 Bag 测试仍通过 + 全包**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestBag|TestOpDedup' && go build ./...`
Expected: PASS（Bag 既有测试 `TestBag_AddIdempotent` 等不变）。

- [ ] **Step 7: Commit**

```bash
git add src/servers/lobbysvr/internal/op_dedup.go src/servers/lobbysvr/internal/op_dedup_test.go src/servers/lobbysvr/internal/component_bag.go
git commit -m "refactor(lobby): 抽取共享 opDedup 去重 helper，Bag 改用（供 Currency 复用）"
```

---

## Stage B — proto + gen_routes

### Task B1: 扩 lobby.proto / gate.proto + 重新生成

**Files:**
- Modify: `protocal/lobby.proto`, `protocal/gate.proto`
- Generated: `protocal/gen/lobby/*.pb.go`, `protocal/gen/gate/*.pb.go`, `protocal/gen/routes/routes.go`

msg_id 分配（CS 奇 / SC 偶=CS+1；推送独立）：CurrencyQuery 2007/2008、Purchase 2009/2010、FriendList 2011/2012、FriendAdd 2013/2014、FriendRespond 2015/2016、FriendRemove 2017/2018、MailList 2019/2020、MailClaim 2021/2022；推送 SC_FriendPresence 2031、SC_MailNew 2033。Cluster-only（无 msg_id）：`RPC_Touch_Notify`（lobby）、`RPC_PushToClient`（gate）。

- [ ] **Step 1: 追加到 `protocal/lobby.proto`**

```protobuf
// --- gateway → lobby：活跃心跳中转（route="LobbyHandler.touch"，单向，无 msg_id，按 route Cast） ---
message RPC_Touch_Notify {
  int64 uid = 1;
}

// --- 客户端 ↔ lobby 货币 ---
message CurrencyAmount {
  string kind   = 1; // 币种，如 "gold"/"diamond"
  int64  amount = 2;
}

message CS_CurrencyQuery {
  option (options.msg_id)         = 2007;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.currencyquery";
}
message SC_CurrencyQuery {
  option (options.msg_id) = 2008;
  repeated CurrencyAmount balances = 1;
}

// CS_Purchase 购买道具（显式编排：扣币 + 加道具，同 op_id 幂等）
message CS_Purchase {
  option (options.msg_id)         = 2009;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.purchase";
  string op_id   = 1;
  string kind    = 2; // 支付币种
  int64  price   = 3;
  int32  item_id = 4;
}
message SC_Purchase {
  option (options.msg_id) = 2010;
  int32 code        = 1; // 0=成功，1=余额不足
  int64 balance     = 2; // 该币种新余额
  int32 item_count  = 3; // 该道具新数量
}

// --- 客户端 ↔ lobby 好友 ---
message FriendEntry {
  int64 uid    = 1;
  bool  online = 2;
}
message CS_FriendList {
  option (options.msg_id)         = 2011;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.friendlist";
}
message SC_FriendList {
  option (options.msg_id) = 2012;
  repeated FriendEntry friends = 1;
}

message CS_FriendAdd {
  option (options.msg_id)         = 2013;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.friendadd";
  int64 target = 1;
}
message SC_FriendAdd {
  option (options.msg_id) = 2014;
  int32 code = 1; // 0=请求已发出，1=非法目标，2=已是好友
}

message CS_FriendRespond {
  option (options.msg_id)         = 2015;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.friendrespond";
  string mail_id = 1; // friend_req 邮件 id（hex）
  bool   accept  = 2;
}
message SC_FriendRespond {
  option (options.msg_id) = 2016;
  int32 code = 1; // 0=成功，1=邮件不存在/已处理
}

message CS_FriendRemove {
  option (options.msg_id)         = 2017;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.friendremove";
  int64 target = 1;
}
message SC_FriendRemove {
  option (options.msg_id) = 2018;
  int32 code = 1;
}

// --- 客户端 ↔ lobby 邮件 ---
message Attachment {
  string kind  = 1; // "currency"/"item"
  int64  id    = 2; // 币种走 kind+0；道具走 item_id
  int64  count = 3;
}
message MailItem {
  string mail_id = 1; // hex
  int64  from    = 2;
  string type    = 3; // normal/friend_req/friend_accept
  string body    = 4;
  int64  ts      = 5;
  bool   claimed = 6;
  repeated Attachment attachments = 7;
}
message CS_MailList {
  option (options.msg_id)         = 2019;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.maillist";
}
message SC_MailList {
  option (options.msg_id) = 2020;
  repeated MailItem mails = 1;
}

message CS_MailClaim {
  option (options.msg_id)         = 2021;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.mailclaim";
  string mail_id = 1;
}
message SC_MailClaim {
  option (options.msg_id) = 2022;
  int32 code = 1; // 0=已领取，1=不存在/已领取
  repeated Attachment granted = 2;
}

// --- 服务器 → 客户端 推送（仅 msg_id，gate 据此推；无 server_type/handler_method） ---
message SC_FriendPresence {
  option (options.msg_id) = 2031;
  int64 uid    = 1; // 上/下线的好友 uid
  bool  online = 2;
}
message SC_MailNew {
  option (options.msg_id) = 2033;
  int64 from = 1;
  string type = 2;
}
```

- [ ] **Step 2: 追加到 `protocal/gate.proto`**

```protobuf
// RPC_PushToClient 后端 → gate：把已按 client 序列化器(json) marshal 的 body 推给 uid
// （route="GateHandler.pushtoclient"，单向，无 msg_id，按 route Cast 到具体 gate 节点）
message RPC_PushToClient {
  int64  uid    = 1;
  uint32 msg_id = 2;
  bytes  body   = 3;
}
```

- [ ] **Step 3: 生成 pb + routes**

Run:
```bash
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
INC=/game/dev/silver-server/3rd/protobuf/include
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$INC protocal/lobby.proto protocal/gate.proto
go run ./tools/gen_routes
```
Expected: 无错误；`protocal/gen/routes/routes.go` 出现 2007/2009/.../2021 的 `MsgRouteTable`+`ForwardTable`+`RespMsgIDTable`（如 `2009: 2010`），2031/2033 **不**出现在任何表。

- [ ] **Step 4: 验证生成正确**

Run: `go build ./... && grep -E '2009: 2010|2021: 2022' protocal/gen/routes/routes.go`
Expected: 编译通过；grep 命中两条 resp 映射。再确认 `grep -E '2031|2033' protocal/gen/routes/routes.go` **无输出**（推送不入表）。

- [ ] **Step 5: Commit**

```bash
git add protocal/lobby.proto protocal/gate.proto protocal/gen/
git commit -m "feat(proto): 货币/好友/邮件/购买 CS-SC + Touch/Push/presence 消息 + 重生成 routes"
```

---

## Stage C — Currency 组件 + 购买编排

### Task C1: Currency 组件

**Files:**
- Create: `src/servers/lobbysvr/internal/component_currency.go`
- Test: `src/servers/lobbysvr/internal/component_currency_test.go`

多币种 `map[string]int64` 内嵌组件，op-id 去重。`Spend` 仅在「非 dup 且足额」时扣减并 `remember`（dup→幂等成功；不足→不 remember 留重试）。

- [ ] **Step 1: 写失败测试**

```go
// src/servers/lobbysvr/internal/component_currency_test.go
package internal

import "testing"

func TestCurrency_GainSpend(t *testing.T) {
	c := NewCurrency()
	if bal, changed := c.Gain("op1", "gold", 100); bal != 100 || !changed {
		t.Fatalf("gain: %d %v", bal, changed)
	}
	if !c.CanAfford("gold", 100) || c.CanAfford("gold", 101) {
		t.Fatal("CanAfford wrong")
	}
	if bal, ok := c.Spend("op2", "gold", 30); bal != 70 || !ok {
		t.Fatalf("spend: %d %v", bal, ok)
	}
}

func TestCurrency_SpendInsufficient_NoRememberNoChange(t *testing.T) {
	c := NewCurrency()
	c.Gain("g", "gold", 10)
	bal, ok := c.Spend("op", "gold", 50)
	if ok || bal != 10 {
		t.Fatalf("insufficient spend must fail clean: %d %v", bal, ok)
	}
	// 不足未 remember：充值后同 opID 可成功
	c.Gain("g2", "gold", 100)
	if bal, ok := c.Spend("op", "gold", 50); !ok || bal != 60 {
		t.Fatalf("retry after topup should succeed: %d %v", bal, ok)
	}
}

func TestCurrency_SpendIdempotent(t *testing.T) {
	c := NewCurrency()
	c.Gain("g", "gold", 100)
	c.Spend("opX", "gold", 40) // 60
	if bal, ok := c.Spend("opX", "gold", 40); !ok || bal != 60 {
		t.Fatalf("dup spend must be idempotent success: %d %v", bal, ok)
	}
}

func TestCurrency_LoadSnapshotDirty(t *testing.T) {
	c := NewCurrency()
	c.Load(&CurrencyState{Balances: map[string]int64{"gold": 5}})
	if c.Dirty() {
		t.Fatal("freshly loaded must be clean")
	}
	c.Gain("op", "gold", 1)
	if !c.Dirty() {
		t.Fatal("gain should dirty")
	}
	snap := c.Snapshot().(CurrencyState)
	if snap.Balances["gold"] != 6 {
		t.Fatalf("snapshot: %v", snap.Balances)
	}
	if c.Name() != CurrencyComponentName || c.Field() != CurrencyField {
		t.Fatal("names")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestCurrency`
Expected: 编译失败 `NewCurrency undefined`。

- [ ] **Step 3: 实现 Currency**

```go
// src/servers/lobbysvr/internal/component_currency.go
package internal

const (
	CurrencyComponentName = "currency"
	CurrencyField         = "currency"
	maxCurrencyOps        = 128
)

// CurrencyState 货币存储态（内嵌 players 文档 currency 子文档）
type CurrencyState struct {
	Balances map[string]int64 `bson:"balances"`
}

func NewCurrencyState() CurrencyState { return CurrencyState{Balances: map[string]int64{}} }

// Currency 多币种钱包；op-id 去重保幂等。仅主循环用，零锁。
type Currency struct {
	balances map[string]int64
	ops      *opDedup
	dirty    bool
}

func NewCurrency() *Currency {
	return &Currency{balances: make(map[string]int64), ops: newOpDedup(maxCurrencyOps)}
}

func (c *Currency) Name() string  { return CurrencyComponentName }
func (c *Currency) Field() string { return CurrencyField }
func (c *Currency) Dirty() bool   { return c.dirty }
func (c *Currency) ClearDirty()   { c.dirty = false }
func (c *Currency) MarkDirty()    { c.dirty = true }

// Load 从存储态恢复（覆盖、清脏）
func (c *Currency) Load(s *CurrencyState) {
	c.balances = make(map[string]int64, len(s.Balances))
	for k, v := range s.Balances {
		c.balances[k] = v
	}
	c.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (c *Currency) Snapshot() any {
	out := make(map[string]int64, len(c.balances))
	for k, v := range c.balances {
		out[k] = v
	}
	return CurrencyState{Balances: out}
}

// Balance 返回某币种余额
func (c *Currency) Balance(kind string) int64 { return c.balances[kind] }

// Balances 返回余额副本
func (c *Currency) Balances() map[string]int64 {
	out := make(map[string]int64, len(c.balances))
	for k, v := range c.balances {
		out[k] = v
	}
	return out
}

// CanAfford 判断 kind 余额是否 >= amt
func (c *Currency) CanAfford(kind string, amt int64) bool { return c.balances[kind] >= amt }

// Gain 幂等加币：dup→返回当前余额；否则增并标脏、remember。返回(新余额, 是否变更)。
func (c *Currency) Gain(opID, kind string, amt int64) (int64, bool) {
	if c.ops.seen(opID) {
		return c.balances[kind], false
	}
	if amt != 0 {
		c.balances[kind] += amt
		c.dirty = true
	}
	c.ops.remember(opID)
	return c.balances[kind], amt != 0
}

// Spend 幂等扣币：dup→幂等成功(返回当前余额,true)；不足→(当前余额,false)且不 remember（留重试）；
// 足额→扣减、标脏、remember，返回(新余额,true)。
func (c *Currency) Spend(opID, kind string, amt int64) (int64, bool) {
	if c.ops.seen(opID) {
		return c.balances[kind], true
	}
	if c.balances[kind] < amt {
		return c.balances[kind], false
	}
	c.balances[kind] -= amt
	c.dirty = true
	c.ops.remember(opID)
	return c.balances[kind], true
}

var _ Component = (*Currency)(nil)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestCurrency`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/component_currency.go src/servers/lobbysvr/internal/component_currency_test.go
git commit -m "feat(lobby): Currency 多币种组件（CanAfford/Gain/Spend + op-id 去重）"
```

### Task C2: PlayerDoc.currency + buildPlayer 装配 + Currency() 访问器

**Files:**
- Modify: `src/servers/lobbysvr/internal/store.go`
- Test: `src/servers/lobbysvr/internal/store_test.go`

- [ ] **Step 1: 写失败测试**（追加到 store_test.go）

```go
func TestBuildPlayer_LoadsCurrency(t *testing.T) {
	doc := NewPlayerDoc(10001)
	doc.Currency = CurrencyState{Balances: map[string]int64{"gold": 42}}
	p := buildPlayer(10001, doc)
	if p.Currency() == nil || p.Currency().Balance("gold") != 42 {
		t.Fatalf("currency not loaded")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBuildPlayer_LoadsCurrency`
Expected: 编译失败 `doc.Currency undefined`。

- [ ] **Step 3: 实现**

`store.go` 改 `PlayerDoc` + `NewPlayerDoc` + `buildPlayer` + 加访问器：

```go
type PlayerDoc struct {
	ID       int64         `bson:"_id"`
	Bag      BagState      `bson:"bag"`
	Currency CurrencyState `bson:"currency"`
}

func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{ID: uid, Bag: NewBagState(), Currency: NewCurrencyState()}
}

// buildPlayer 中追加 currency 组件装配（在 bag 之后）：
//	cur := NewCurrency()
//	cur.Load(&doc.Currency)
//	p.AddComponent(cur)

// 追加访问器：
func (p *Player) Currency() *Currency {
	c, _ := p.Component(CurrencyComponentName).(*Currency)
	return c
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestBuildPlayer'`
Expected: PASS（含既有 `TestBuildPlayer_FreshDoc`/`_LoadsBag`）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/store_test.go
git commit -m "feat(lobby): PlayerDoc 内嵌 currency 子文档 + buildPlayer 装配 Currency"
```

### Task C3: CurrencyChanged 事件 + 全局审计订阅者

**Files:**
- Modify: `src/servers/lobbysvr/internal/events.go`
- Modify: `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/events_test.go`

事件总线只承载「不会失败的副作用」。`CurrencyChanged` 由**主循环/编排层**在 Currency 成功变更后发布（组件保持纯净，不持 bus）。Runtime 启动时注册一个安全订阅者（审计日志）作示范。

- [ ] **Step 1: 写失败测试**（追加 events_test.go）

```go
func TestEvents_CurrencyChanged(t *testing.T) {
	ev := NewEvents()
	var got CurrencyChanged
	ev.CurrencyChanged.Subscribe(func(e CurrencyChanged) { got = e })
	ev.CurrencyChanged.Publish(CurrencyChanged{UID: 7, Kind: "gold", Delta: -30})
	if got.UID != 7 || got.Kind != "gold" || got.Delta != -30 {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestEvents_CurrencyChanged`
Expected: 编译失败 `CurrencyChanged undefined`。

- [ ] **Step 3: 实现**

`events.go`：

```go
// PlayerLoaded 之外追加：
// CurrencyChanged 货币变动事件（不会失败的副作用通知，如审计/任务统计）
type CurrencyChanged struct {
	UID   int64
	Kind  string
	Delta int64
}

type Events struct {
	PlayerLoaded    *event.Bus[PlayerLoaded]
	CurrencyChanged *event.Bus[CurrencyChanged]
}

func NewEvents() *Events {
	return &Events{
		PlayerLoaded:    event.NewBus[PlayerLoaded](),
		CurrencyChanged: event.NewBus[CurrencyChanged](),
	}
}
```

`runtime.go` 的 `Start()` 注册审计订阅者（在 `tw.Tick` 之前）：

```go
func (rt *Runtime) Start() {
	rt.events.CurrencyChanged.Subscribe(func(e CurrencyChanged) {
		logger.Info("currency changed",
			logger.Int64("uid", e.UID), logger.String("kind", e.Kind), logger.Int64("delta", e.Delta))
	})
	rt.tw.Tick(rt.flushInterval, rt.flushAllDirty)
	go rt.loop()
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestEvents && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/events.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/events_test.go
git commit -m "feat(lobby): CurrencyChanged 事件 + Runtime 注册审计订阅者（事件总线副作用示范）"
```

### Task C4: LobbyHandler.Currencyquery + Purchase 编排

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

购买 = 主循环显式编排：`CanAfford`→`Spend`→`Bag.Add`（同 opID），成功后发 `CurrencyChanged`。`Currencyquery` 读余额回包。两者经 Replier 异步回包。Purchase 成功后调 `rt.flushSoon(uid)`（G1 引入；本任务先调，G1 实现该方法——若 G1 尚未做，用临时空方法占位见 Step 3 注释）。

- [ ] **Step 1: 写失败测试**（追加 lobby_handler_test.go；参照既有 handler 测试用 fake cluster + 直接驱动 Runtime）

```go
func TestPurchase_Orchestration(t *testing.T) {
	rt := newTestRuntime(t) // 见既有 runtime_test 辅助：构造带 fakeStore 的 Runtime 并 Start
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid) // 见辅助：同步登录加载玩家
	// 预置货币
	runOnLoop(t, rt, func() { rt.Player(uid).Currency().Gain("seed", "gold", 100) })

	rsp := purchaseSync(t, rt, uid, "p1", "gold", 30, 555) // 辅助：构造 CS_Purchase 经 handler 驱动
	if rsp.Code != 0 || rsp.Balance != 70 || rsp.ItemCount != 1 {
		t.Fatalf("purchase: %+v", rsp)
	}
	// 幂等：重复 opID 不双扣双发
	rsp2 := purchaseSync(t, rt, uid, "p1", "gold", 30, 555)
	if rsp2.Balance != 70 || rsp2.ItemCount != 1 {
		t.Fatalf("dup purchase not idempotent: %+v", rsp2)
	}
	// 余额不足拒绝
	rsp3 := purchaseSync(t, rt, uid, "p2", "gold", 1000, 555)
	if rsp3.Code != 1 || rsp3.Balance != 70 {
		t.Fatalf("insufficient not rejected: %+v", rsp3)
	}
}
```

> 注：`newTestRuntime`/`loadPlayerSync`/`runOnLoop`/`purchaseSync` 等辅助若 runtime_test.go/lobby_handler_test.go 未提供，则在本任务一并新增（同步驱动主循环：`Submit` 后用 channel 等待 continuation 完成）。`purchaseSync` 内部：构造 `*lobbypb.CS_Purchase`，注入带 fake `Replier`+`ClusterSession{Uid:uid}` 的 ctx，调 `h.Purchase(ctx, req)`，等 fake Replier 收到字节后 `proto.Unmarshal` 成 `SC_Purchase` 返回。

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPurchase`
Expected: 编译失败 `h.Purchase undefined`。

- [ ] **Step 3: 实现 handlers**

```go
// 追加到 lobby_handler.go

// Currencyquery route="LobbyHandler.currencyquery"
func (h *LobbyHandler) Currencyquery(ctx context.Context, _ *lobbypb.CS_CurrencyQuery) (*lobbypb.SC_CurrencyQuery, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		rsp := &lobbypb.SC_CurrencyQuery{}
		for kind, amt := range p.Currency().Balances() {
			rsp.Balances = append(rsp.Balances, &lobbypb.CurrencyAmount{Kind: kind, Amount: amt})
		}
		replyProto(replier, rsp, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Purchase route="LobbyHandler.purchase"：显式编排 扣币+加道具（同 op_id 幂等）
func (h *LobbyHandler) Purchase(ctx context.Context, req *lobbypb.CS_Purchase) (*lobbypb.SC_Purchase, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		cur := p.Currency()
		if !cur.CanAfford(req.Kind, req.Price) {
			replyProto(replier, &lobbypb.SC_Purchase{Code: 1, Balance: cur.Balance(req.Kind)}, nil)
			return
		}
		bal, ok := cur.Spend(req.OpId, req.Kind, req.Price)
		if !ok { // 理论不达（已 CanAfford），防御
			replyProto(replier, &lobbypb.SC_Purchase{Code: 1, Balance: bal}, nil)
			return
		}
		h.rt.PublishCurrencyChanged(uid, req.Kind, -req.Price)
		n := p.Bag().Add(req.OpId, req.ItemId, 1)
		h.rt.FlushSoon(uid)
		replyProto(replier, &lobbypb.SC_Purchase{Code: 0, Balance: bal, ItemCount: int32(n)}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

在 `runtime.go` 加薄封装（`FlushSoon` 占位，G1 替换为真实合并实现）：

```go
// PublishCurrencyChanged 主循环内发布货币变动事件（副作用通知）
func (rt *Runtime) PublishCurrencyChanged(uid int64, kind string, delta int64) {
	rt.events.CurrencyChanged.Publish(CurrencyChanged{UID: uid, Kind: kind, Delta: delta})
}

// FlushSoon 键点 flush 占位（G1 实现合并写）；当前直接异步 flush 该玩家
func (rt *Runtime) FlushSoon(uid int64) {
	if p, ok := rt.players[uid]; ok {
		rt.flushPlayer(uid, p, nil)
	}
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPurchase && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): CurrencyQuery + Purchase 显式编排（扣币+加道具，同 op_id 幂等，发 CurrencyChanged）"
```

---

## Stage D — mailbox 集合 + Mail 组件

### Task D1: mailbox 存储（MailDoc/Attachment + MailStore + mongoMailStore）

**Files:**
- Create: `src/servers/lobbysvr/internal/mailbox_store.go`
- Test: `src/servers/lobbysvr/internal/mailbox_store_test.go`

`mailbox` 集合 insert-only + 原子 claim。定义 `MailStore` 接口（便于 fake）+ `mongoMailStore`（用 A1 方法）。`fakeMailStore`（同步内存）供 D2/D3 单测复用，本任务用契约测试锁定语义（Insert→List→Claim→重复 Claim 失败）。

- [ ] **Step 1: 写失败测试**

```go
// src/servers/lobbysvr/internal/mailbox_store_test.go
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
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestMailbox`
Expected: 编译失败 `MailDoc undefined`。

- [ ] **Step 3: 实现 mailbox_store.go**

```go
// src/servers/lobbysvr/internal/mailbox_store.go
package internal

import (
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"project/src/common/mongo"
	"project/src/common/taskqueue"
)

const mailboxColl = "mailbox"

// 邮件类型
const (
	MailTypeNormal       = "normal"
	MailTypeFriendReq    = "friend_req"
	MailTypeFriendAccept = "friend_accept"
)

// Attachment 邮件附件：Kind=="item" 走背包(ID=itemID)；否则 Kind 视作币种名走货币(Count=数量)
type Attachment struct {
	Kind  string `bson:"kind"`
	ID    int64  `bson:"id"`
	Count int64  `bson:"count"`
}

// MailDoc mailbox 集合文档（独立于 players，insert-only + 原子 claim）
type MailDoc struct {
	ID          primitive.ObjectID `bson:"_id,omitempty"`
	To          int64              `bson:"to"`
	From        int64              `bson:"from"`
	Type        string             `bson:"type"`
	Attachments []Attachment       `bson:"attachments,omitempty"`
	Body        string             `bson:"body,omitempty"`
	Ts          int64              `bson:"ts"`
	Read        bool               `bson:"read"`
	Claimed     bool               `bson:"claimed"`
}

// MailStore mailbox 持久化抽象（便于 fake 替换真实 Mongo）
type MailStore interface {
	Insert(d taskqueue.Dispatcher, m *MailDoc, done func(error))
	List(d taskqueue.Dispatcher, to int64, limit int64, done func([]MailDoc, error))
	Claim(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(claimed bool, m *MailDoc, err error))
	PendingFriendAccepts(d taskqueue.Dispatcher, to int64, done func([]MailDoc, error))
}

// mongoMailStore 基于 src/common/mongo 的 MailStore 实现
type mongoMailStore struct{ c *mongo.Client }

// NewMongoMailStore 用已连接的 mongo.Client 构建
func NewMongoMailStore(c *mongo.Client) MailStore { return &mongoMailStore{c: c} }

func (s *mongoMailStore) Insert(d taskqueue.Dispatcher, m *MailDoc, done func(error)) {
	s.c.InsertOne(d, mailboxColl, m, func(_ any, err error) { done(err) })
}

func (s *mongoMailStore) List(d taskqueue.Dispatcher, to int64, limit int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl, bson.M{"to": to}, bson.D{{Key: "ts", Value: -1}}, limit, &out,
		func(err error) { done(out, err) })
}

// Claim 原子领取：匹配 {_id,to,claimed:false} 置 claimed:true 并返回该文档；无匹配 claimed=false。
func (s *mongoMailStore) Claim(d taskqueue.Dispatcher, id primitive.ObjectID, to int64, done func(bool, *MailDoc, error)) {
	var m MailDoc
	s.c.FindOneAndUpdate(d, mailboxColl,
		bson.M{"_id": id, "to": to, "claimed": false},
		bson.M{"$set": bson.M{"claimed": true}}, true, &m,
		func(found bool, err error) {
			if err != nil || !found {
				done(false, nil, err)
				return
			}
			done(true, &m, nil)
		})
}

func (s *mongoMailStore) PendingFriendAccepts(d taskqueue.Dispatcher, to int64, done func([]MailDoc, error)) {
	var out []MailDoc
	s.c.Find(d, mailboxColl,
		bson.M{"to": to, "type": MailTypeFriendAccept, "claimed": false},
		bson.D{{Key: "ts", Value: 1}}, 0, &out,
		func(err error) { done(out, err) })
}

var _ MailStore = (*mongoMailStore)(nil)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestMailbox && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/mailbox_store.go src/servers/lobbysvr/internal/mailbox_store_test.go
git commit -m "feat(lobby): mailbox 集合存储（MailDoc + MailStore + mongoMailStore，insert-only + 原子 claim）"
```

### Task D2: Mail 组件 + Player 装配 + Runtime.mailStore

**Files:**
- Modify: `src/servers/lobbysvr/internal/player.go`, `src/servers/lobbysvr/internal/store.go`, `src/servers/lobbysvr/internal/runtime.go`
- Create: `src/servers/lobbysvr/internal/component_mail.go`
- Test: `src/servers/lobbysvr/internal/component_mail_test.go`

Mail **不是** flush 组件（不实现 `Component` 接口、不入 `Components()`）。Player 单独持 `mail` 字段，`attachMail` 在 Runtime.Login 装配（Mail 依赖 runtime 的 mailStore，故不放 `buildPlayer`，保持其纯 EC）。

- [ ] **Step 1: 写失败测试**

```go
// src/servers/lobbysvr/internal/component_mail_test.go
package internal

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestMail_ListClaim(t *testing.T) {
	store := newFakeMailStore()
	store.Insert(nil, &MailDoc{To: 10001, From: 1, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "gold", Count: 50}}}, func(error) {})
	m := NewMail(10001, store)

	var id primitive.ObjectID
	m.List(nil, 50, func(ms []MailDoc, _ error) {
		if len(ms) != 1 {
			t.Fatalf("len=%d", len(ms))
		}
		id = ms[0].ID
	})
	m.Claim(nil, id, func(ok bool, doc *MailDoc, _ error) {
		if !ok || doc.Attachments[0].Kind != "gold" {
			t.Fatalf("claim: %v %+v", ok, doc)
		}
	})
}

func TestPlayer_AttachMail(t *testing.T) {
	p := buildPlayer(10001, NewPlayerDoc(10001))
	if p.Mail() != nil {
		t.Fatal("mail should be nil before attach")
	}
	p.attachMail(newFakeMailStore())
	if p.Mail() == nil {
		t.Fatal("mail should be set after attach")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestMail_|TestPlayer_AttachMail'`
Expected: 编译失败 `NewMail undefined`。

- [ ] **Step 3: 实现 Mail + Player 装配 + Runtime 字段**

`component_mail.go`：

```go
// src/servers/lobbysvr/internal/component_mail.go
package internal

import (
	"go.mongodb.org/mongo-driver/bson/primitive"

	"project/src/common/taskqueue"
)

// Mail 邮箱组件：对 mailbox 集合的异步 I/O 句柄。
// 非 flush 组件（不实现 Component 接口、不入 Components()）；持自己的 uid + MailStore。
type Mail struct {
	uid   int64
	store MailStore
}

func NewMail(uid int64, store MailStore) *Mail { return &Mail{uid: uid, store: store} }

// List 拉本玩家收件（limit 上限，按 ts 倒序）
func (m *Mail) List(d taskqueue.Dispatcher, limit int64, done func([]MailDoc, error)) {
	m.store.List(d, m.uid, limit, done)
}

// Claim 领取本玩家某邮件（原子幂等）
func (m *Mail) Claim(d taskqueue.Dispatcher, id primitive.ObjectID, done func(bool, *MailDoc, error)) {
	m.store.Claim(d, id, m.uid, done)
}
```

`player.go`：Player 加 `mail *Mail` 字段 + `attachMail` + `Mail()`：

```go
type Player struct {
	uid        int64
	components map[string]Component
	order      []string
	mail       *Mail
	lastTouch  int64 // 上次 Touch onlinesvr 的 Unix 纳秒（Touch 节流用，F2）
}

// attachMail 由 Runtime 在加载后装配邮箱（Mail 依赖 runtime mailStore）
func (p *Player) attachMail(store MailStore) { p.mail = NewMail(p.uid, store) }

// Mail 返回邮箱组件（未 attach 返回 nil）
func (p *Player) Mail() *Mail { return p.mail }
```

`runtime.go`：`RuntimeConfig` 加 `MailStore MailStore`；`Runtime` 加 `mailStore MailStore`；`NewRuntime` 赋值；`Login` 加载后 `p.attachMail(rt.mailStore)`：

```go
// RuntimeConfig 加字段：
//	MailStore MailStore
// Runtime 加字段：
//	mailStore MailStore
// NewRuntime 内 rt 构造加：
//	mailStore: cfg.MailStore,
// Login 的 load done continuation 内，buildPlayer 之后、events 之前：
//	rt.players[uid] = buildPlayer(uid, doc)
//	rt.players[uid].attachMail(rt.mailStore)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestMail_|TestPlayer_AttachMail|TestBuildPlayer' && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/component_mail.go src/servers/lobbysvr/internal/player.go src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/component_mail_test.go
git commit -m "feat(lobby): Mail 邮箱组件（mailbox 后端，不入 flush）+ Player.attachMail + Runtime.mailStore"
```

### Task D3: LobbyHandler.Maillist + Mailclaim（领取附件编排）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

`Maillist` 拉收件回包。`Mailclaim` 原子 claim → 命中则把附件编排进 Bag/Currency（`opID=""`，幂等边界=claim 原子性）→ `FlushSoon` → 回 granted。

- [ ] **Step 1: 写失败测试**

```go
func TestMailClaim_GrantsAttachments(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	// 直接向 mailbox 投递一封带附件的邮件
	store := rt.mailStore.(*fakeMailStore)
	store.Insert(nil, &MailDoc{To: uid, From: 0, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "gold", Count: 50}, {Kind: "item", ID: 777, Count: 2}}}, func(error) {})
	var id primitive.ObjectID
	store.List(nil, uid, 50, func(ms []MailDoc, _ error) { id = ms[0].ID })

	rsp := mailClaimSync(t, rt, uid, id.Hex())
	if rsp.Code != 0 || len(rsp.Granted) != 2 {
		t.Fatalf("claim: %+v", rsp)
	}
	runOnLoop(t, rt, func() {
		if rt.Player(uid).Currency().Balance("gold") != 50 || rt.Player(uid).Bag().Count(777) != 2 {
			t.Fatal("attachments not granted to player state")
		}
	})
	// 重复 claim 不再发放
	rsp2 := mailClaimSync(t, rt, uid, id.Hex())
	if rsp2.Code != 1 {
		t.Fatalf("dup claim must fail: %+v", rsp2)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestMailClaim`
Expected: 编译失败 `h.Mailclaim undefined`。

- [ ] **Step 3: 实现**

```go
// lobby_handler.go 追加（import 增加 "go.mongodb.org/mongo-driver/bson/primitive"）

// Maillist route="LobbyHandler.maillist"
func (h *LobbyHandler) Maillist(ctx context.Context, _ *lobbypb.CS_MailList) (*lobbypb.SC_MailList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		p.Mail().List(h.rt.tq, mailListLimit, func(mails []MailDoc, err error) {
			if err != nil {
				replyProto(replier, nil, err)
				return
			}
			rsp := &lobbypb.SC_MailList{}
			for _, m := range mails {
				rsp.Mails = append(rsp.Mails, mailToProto(m))
			}
			replyProto(replier, rsp, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Mailclaim route="LobbyHandler.mailclaim"：原子 claim → 命中则编排发放附件
func (h *LobbyHandler) Mailclaim(ctx context.Context, req *lobbypb.CS_MailClaim) (*lobbypb.SC_MailClaim, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	id, err := primitive.ObjectIDFromHex(req.MailId)
	if err != nil {
		h.rt.Submit(func() { replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		p.Mail().Claim(h.rt.tq, id, func(ok bool, m *MailDoc, cerr error) {
			if cerr != nil {
				replyProto(replier, nil, cerr)
				return
			}
			if !ok {
				replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil)
				return
			}
			h.rt.grantAttachments(uid, p, m.Attachments) // opID="" ，幂等边界=claim 原子性
			h.rt.FlushSoon(uid)
			rsp := &lobbypb.SC_MailClaim{Code: 0}
			for _, a := range m.Attachments {
				rsp.Granted = append(rsp.Granted, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
			}
			replyProto(replier, rsp, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// mailToProto MailDoc → proto MailItem
func mailToProto(m MailDoc) *lobbypb.MailItem {
	mi := &lobbypb.MailItem{
		MailId: m.ID.Hex(), From: m.From, Type: m.Type, Body: m.Body, Ts: m.Ts, Claimed: m.Claimed,
	}
	for _, a := range m.Attachments {
		mi.Attachments = append(mi.Attachments, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
	}
	return mi
}
```

`runtime.go` 加 `grantAttachments` + `mailListLimit` 常量：

```go
const mailListLimit = 50

// grantAttachments 主循环内把附件发放进玩家组件（Kind=="item" 走背包，否则视作币种走货币）。
// opID="" 不走组件去重；幂等由 mailbox Claim 原子性保证（已 claim 的邮件不会再发放）。
func (rt *Runtime) grantAttachments(uid int64, p *Player, atts []Attachment) {
	for _, a := range atts {
		if a.Kind == "item" {
			p.Bag().Add("", int32(a.ID), int32(a.Count))
		} else {
			p.Currency().Gain("", a.Kind, a.Count)
			rt.PublishCurrencyChanged(uid, a.Kind, a.Count)
		}
	}
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestMailClaim && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): MailList + MailClaim（原子领取，附件编排进背包/货币，幂等边界=claim）"
```

---

## Stage E — Friend 组件 + 握手 + presence

### Task E1: Friend 组件

**Files:**
- Create: `src/servers/lobbysvr/internal/component_friend.go`
- Test: `src/servers/lobbysvr/internal/component_friend_test.go`

好友 uid 集，内嵌 flush 组件，全本地写。

- [ ] **Step 1: 写失败测试**

```go
// src/servers/lobbysvr/internal/component_friend_test.go
package internal

import "testing"

func TestFriend_AddRemoveHasList(t *testing.T) {
	f := NewFriend()
	if f.Has(2) {
		t.Fatal("empty")
	}
	if !f.Add(2) || f.Add(2) { // 首次加返回 true，重复加返回 false（已存在）
		t.Fatal("Add idempotent semantics")
	}
	if !f.Has(2) || !f.Dirty() {
		t.Fatal("after add")
	}
	f.ClearDirty()
	if !f.Remove(2) || f.Remove(2) {
		t.Fatal("Remove semantics")
	}
	if f.Has(2) || !f.Dirty() {
		t.Fatal("after remove")
	}
}

func TestFriend_LoadSnapshot(t *testing.T) {
	f := NewFriend()
	f.Load(&FriendState{Friends: []int64{5, 9}})
	if !f.Has(5) || !f.Has(9) || f.Dirty() {
		t.Fatal("load")
	}
	snap := f.Snapshot().(FriendState)
	if len(snap.Friends) != 2 {
		t.Fatalf("snapshot: %v", snap.Friends)
	}
	if f.Name() != FriendComponentName || f.Field() != FriendField {
		t.Fatal("names")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFriend`
Expected: 编译失败 `NewFriend undefined`。

- [ ] **Step 3: 实现 Friend**

```go
// src/servers/lobbysvr/internal/component_friend.go
package internal

const (
	FriendComponentName = "friend"
	FriendField         = "friend"
)

// FriendState 好友存储态（内嵌 players 文档 friend 子文档）
type FriendState struct {
	Friends []int64 `bson:"friends"`
}

func NewFriendState() FriendState { return FriendState{Friends: []int64{}} }

// Friend 好友组件：uid 集合，全本地写。仅主循环用，零锁。
type Friend struct {
	friends map[int64]struct{}
	dirty   bool
}

func NewFriend() *Friend { return &Friend{friends: make(map[int64]struct{})} }

func (f *Friend) Name() string  { return FriendComponentName }
func (f *Friend) Field() string { return FriendField }
func (f *Friend) Dirty() bool   { return f.dirty }
func (f *Friend) ClearDirty()   { f.dirty = false }
func (f *Friend) MarkDirty()    { f.dirty = true }

func (f *Friend) Load(s *FriendState) {
	f.friends = make(map[int64]struct{}, len(s.Friends))
	for _, u := range s.Friends {
		f.friends[u] = struct{}{}
	}
	f.dirty = false
}

func (f *Friend) Snapshot() any {
	out := make([]int64, 0, len(f.friends))
	for u := range f.friends {
		out = append(out, u)
	}
	return FriendState{Friends: out}
}

func (f *Friend) Has(uid int64) bool { _, ok := f.friends[uid]; return ok }

// Add 加好友，已存在返回 false（不重复标脏）；新增返回 true 并标脏。
func (f *Friend) Add(uid int64) bool {
	if _, ok := f.friends[uid]; ok {
		return false
	}
	f.friends[uid] = struct{}{}
	f.dirty = true
	return true
}

// Remove 删好友，存在则删并标脏返回 true；否则 false。
func (f *Friend) Remove(uid int64) bool {
	if _, ok := f.friends[uid]; !ok {
		return false
	}
	delete(f.friends, uid)
	f.dirty = true
	return true
}

// List 返回好友 uid 副本
func (f *Friend) List() []int64 {
	out := make([]int64, 0, len(f.friends))
	for u := range f.friends {
		out = append(out, u)
	}
	return out
}

var _ Component = (*Friend)(nil)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFriend`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/component_friend.go src/servers/lobbysvr/internal/component_friend_test.go
git commit -m "feat(lobby): Friend 好友组件（uid 集，Add/Remove/Has/List，全本地写）"
```

### Task E2: PlayerDoc.friend + buildPlayer 装配 + Friend() 访问器

**Files:**
- Modify: `src/servers/lobbysvr/internal/store.go`
- Test: `src/servers/lobbysvr/internal/store_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestBuildPlayer_LoadsFriend(t *testing.T) {
	doc := NewPlayerDoc(10001)
	doc.Friend = FriendState{Friends: []int64{42}}
	p := buildPlayer(10001, doc)
	if p.Friend() == nil || !p.Friend().Has(42) {
		t.Fatal("friend not loaded")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBuildPlayer_LoadsFriend`
Expected: 编译失败 `doc.Friend undefined`。

- [ ] **Step 3: 实现**

`store.go`：`PlayerDoc` 加 `Friend FriendState`、`NewPlayerDoc` 初始化、`buildPlayer` 装配 Friend、加访问器：

```go
type PlayerDoc struct {
	ID       int64         `bson:"_id"`
	Bag      BagState      `bson:"bag"`
	Currency CurrencyState `bson:"currency"`
	Friend   FriendState   `bson:"friend"`
}

func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{ID: uid, Bag: NewBagState(), Currency: NewCurrencyState(), Friend: NewFriendState()}
}

// buildPlayer 追加（currency 之后）：
//	fr := NewFriend()
//	fr.Load(&doc.Friend)
//	p.AddComponent(fr)

func (p *Player) Friend() *Friend {
	f, _ := p.Component(FriendComponentName).(*Friend)
	return f
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBuildPlayer && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/store_test.go
git commit -m "feat(lobby): PlayerDoc 内嵌 friend 子文档 + buildPlayer 装配 Friend"
```

### Task E3: 好友握手 handlers + 登录 accept-scan

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`, `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

好友请求/接受/拒绝/删除经 mailbox 握手（零 player-doc 跨写）；登录扫描 friend_accept 补全好友图（D7）。`nowNano()` 用 `time.Now().UnixNano()`。

- [ ] **Step 1: 写失败测试**（最终一致握手：A 发请求 → B 接受 → B 立即含 A；A 登录扫描后含 B）

```go
func TestFriendHandshake_EventualConsistency(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	a, b := int64(1001), int64(1002)
	loadPlayerSync(t, rt, a)
	loadPlayerSync(t, rt, b)

	// A 发好友请求给 B
	if rsp := friendAddSync(t, rt, a, b); rsp.Code != 0 {
		t.Fatalf("friendadd: %+v", rsp)
	}
	// B 收到 friend_req，取 mailID
	var reqID string
	mails := mailListSync(t, rt, b)
	for _, m := range mails.Mails {
		if m.Type == MailTypeFriendReq && m.From == a {
			reqID = m.MailId
		}
	}
	if reqID == "" {
		t.Fatal("B did not receive friend_req")
	}
	// B 接受 → B 立即含 A
	if rsp := friendRespondSync(t, rt, b, reqID, true); rsp.Code != 0 {
		t.Fatalf("respond: %+v", rsp)
	}
	runOnLoop(t, rt, func() {
		if !rt.Player(b).Friend().Has(a) {
			t.Fatal("B should have A immediately after accept")
		}
	})
	// A 重登 → 扫描 friend_accept → 含 B（最终一致）
	disconnectSync(t, rt, a)
	loadPlayerSync(t, rt, a)
	scanAcceptsSync(t, rt, a) // 辅助：等 accept-scan 完成
	runOnLoop(t, rt, func() {
		if !rt.Player(a).Friend().Has(b) {
			t.Fatal("A should have B after re-login accept-scan")
		}
	})
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFriendHandshake`
Expected: 编译失败 `h.Friendadd undefined`。

- [ ] **Step 3: 实现 handlers + accept-scan**

```go
// lobby_handler.go 追加（import 增加 "time"）

// Friendadd route="LobbyHandler.friendadd"：投递 friend_req 邮件给目标
func (h *LobbyHandler) Friendadd(ctx context.Context, req *lobbypb.CS_FriendAdd) (*lobbypb.SC_FriendAdd, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		if req.Target == uid || req.Target <= 0 {
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 1}, nil)
			return
		}
		if p.Friend().Has(req.Target) {
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 2}, nil)
			return
		}
		h.rt.mailStore.Insert(h.rt.tq, &MailDoc{
			To: req.Target, From: uid, Type: MailTypeFriendReq, Ts: time.Now().UnixNano(),
		}, func(err error) {
			if err != nil {
				replyProto(replier, nil, err)
				return
			}
			replyProto(replier, &lobbypb.SC_FriendAdd{Code: 0}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Friendrespond route="LobbyHandler.friendrespond"：claim friend_req；accept 则加好友 + 回投 friend_accept
func (h *LobbyHandler) Friendrespond(ctx context.Context, req *lobbypb.CS_FriendRespond) (*lobbypb.SC_FriendRespond, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	id, err := primitive.ObjectIDFromHex(req.MailId)
	if err != nil {
		h.rt.Submit(func() { replyProto(replier, &lobbypb.SC_FriendRespond{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	accept := req.Accept
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		p.Mail().Claim(h.rt.tq, id, func(ok bool, m *MailDoc, cerr error) {
			if cerr != nil {
				replyProto(replier, nil, cerr)
				return
			}
			if !ok || m.Type != MailTypeFriendReq {
				replyProto(replier, &lobbypb.SC_FriendRespond{Code: 1}, nil)
				return
			}
			if accept {
				p.Friend().Add(m.From)
				h.rt.FlushSoon(uid)
				h.rt.mailStore.Insert(h.rt.tq, &MailDoc{
					To: m.From, From: uid, Type: MailTypeFriendAccept, Ts: time.Now().UnixNano(),
				}, func(error) {})
			}
			replyProto(replier, &lobbypb.SC_FriendRespond{Code: 0}, nil)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// Friendremove route="LobbyHandler.friendremove"：单边删除（MVP）
func (h *LobbyHandler) Friendremove(ctx context.Context, req *lobbypb.CS_FriendRemove) (*lobbypb.SC_FriendRemove, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		if p.Friend().Remove(req.Target) {
			h.rt.FlushSoon(uid)
		}
		replyProto(replier, &lobbypb.SC_FriendRemove{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

`runtime.go` 加 `scanFriendAccepts`（登录后补全好友图，完成后调 after）：

```go
// scanFriendAccepts 扫描 to=uid 的未处理 friend_accept，逐条加好友 + claim；完成调 after（可 nil）。
func (rt *Runtime) scanFriendAccepts(uid int64, p *Player, after func()) {
	rt.mailStore.PendingFriendAccepts(rt.tq, uid, func(mails []MailDoc, err error) {
		if err != nil {
			logger.Warn("scan friend_accept failed", logger.Int64("uid", uid), logger.Err(err))
			if after != nil {
				after()
			}
			return
		}
		changed := false
		for _, m := range mails {
			if p.Friend().Add(m.From) {
				changed = true
			}
			rt.mailStore.Claim(rt.tq, m.ID, uid, func(bool, *MailDoc, error) {})
		}
		if changed {
			rt.FlushSoon(uid)
		}
		if after != nil {
			after()
		}
	})
}
```

`Login` 的 load done continuation 末尾（reply 之后）触发扫描（presence fan-out 在 E5 接到 after）：

```go
// 在 Login continuation 内 reply 之后追加：
//	rt.scanFriendAccepts(uid, rt.players[uid], nil) // E5 把 nil 换成 presence fan-out
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFriendHandshake && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): 好友请求/接受/拒绝/删除（mailbox 握手）+ 登录 accept-scan（最终一致）"
```

### Task E4: gate PushToClient handler

**Files:**
- Modify: `src/servers/gatesvr/internal/gate_handler.go`
- Modify: `src/servers/gatesvr/main.go`（如 handler 未自动注册）
- Test: `src/servers/gatesvr/internal/gate_handler_test.go`

泛化 `KickSession`：gate 收 `RPC_PushToClient`（raw proto）→ `ByUID` → `agent.Push(msg_id, body)`，body 是已 json-marshal 的不透明字节。

- [ ] **Step 1: 写失败测试**（参照既有 gate_handler_test 对 KickSession 的测法：fake agent.Map + session）

```go
func TestPushToClient_PushesByUID(t *testing.T) {
	h, mod := newTestGateHandler(t) // 辅助：构造带 sessions/agents 的 GateHandler（同 KickSession 测试）
	uid := int64(10001)
	ag := bindFakeAgent(t, mod, uid) // 辅助：建会话+绑定 uid+注册 fake agent，返回可断言 Push 的 fake
	env := &gatepb.RPC_PushToClient{Uid: uid, MsgId: 2031, Body: []byte(`{"uid":5,"online":true}`)}
	raw, _ := proto.Marshal(env)
	h.PushToClient(context.Background(), raw)
	if ag.lastPushMsgID != 2031 || string(ag.lastPushBody) != `{"uid":5,"online":true}` {
		t.Fatalf("push: %d %s", ag.lastPushMsgID, ag.lastPushBody)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/gatesvr/internal/ -run TestPushToClient`
Expected: 编译失败 `h.PushToClient undefined`。

- [ ] **Step 3: 实现**（gate_handler.go，import 增加已有 onlinepb/gatepb；新增对 gatepb.RPC_PushToClient 的引用）

```go
// PushToClient 处理后端推送（route="GateHandler.pushtoclient"）。
// raw 入参同 KickSession：cluster 字节是 proto、gate registry 是 json，故手动 Unmarshal。
// body 已是 client 序列化器(json) 字节，按 uid 透传推给客户端。
func (h *GateHandler) PushToClient(_ context.Context, raw []byte) {
	var req gatepb.RPC_PushToClient
	if err := proto.Unmarshal(raw, &req); err != nil {
		logger.Warn("gate push: unmarshal failed", logger.Err(err))
		return
	}
	s, ok := h.module.Sessions().ByUID(req.Uid)
	if !ok {
		return // 连接可能已断
	}
	if h.module.Agents() == nil {
		return
	}
	ag, ok := h.module.Agents().Load(s.ID())
	if !ok {
		return
	}
	if err := ag.Push(req.MsgId, req.Body); err != nil {
		logger.Warn("gate push: push failed", logger.Int64("uid", req.Uid), logger.Err(err))
	}
}
```

确认 gate `RegisterHandler(GateHandler, ...)` 已覆盖新方法（反射注册按方法名 → route，KickSession 已在用同一注册；无需改 main.go。若 main.go 用显式方法列表注册，则把 `PushToClient` 加入）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/gatesvr/internal/ -run TestPushToClient && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/gatesvr/internal/gate_handler.go src/servers/gatesvr/internal/gate_handler_test.go
git commit -m "feat(gate): PushToClient 通用推送 route（泛化 KickSession，body 按 uid 透传）"
```

### Task E5: presence — FriendList 快照 + 登录/断连 push delta

**Files:**
- Create: `src/servers/lobbysvr/internal/presence.go`
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`, `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/presence_test.go`

`CS_FriendList` off-loop 逐好友 `Query` 取在线快照回包；登录（accept-scan 后）/断连 off-loop fan-out push delta。封装 `queryOnline`/`pushToClient`/`fanoutPresence`，用接口便于注入 fake。

- [ ] **Step 1: 写失败测试**（用可注入的 presence 客户端 fake，断言 fan-out 对在线好友发 push、对离线不发）

```go
// src/servers/lobbysvr/internal/presence_test.go
package internal

import "testing"

// fakePresence 记录 Query/Push 调用，注入 Runtime 替换真实 router/gate 出站。
type fakePresence struct {
	online map[int64]string // uid → gatewayNodeID（在线）
	pushes []presencePush
}
type presencePush struct {
	gateway string
	uid     int64
	msgID   uint32
}

func (f *fakePresence) Query(uid int64) (gatewayNodeID string, online bool) {
	gw, ok := f.online[uid]
	return gw, ok
}
func (f *fakePresence) Push(gatewayNodeID string, uid int64, msgID uint32, body []byte) {
	f.pushes = append(f.pushes, presencePush{gatewayNodeID, uid, msgID})
}

func TestFanoutPresence_OnlyOnlineFriends(t *testing.T) {
	fp := &fakePresence{online: map[int64]string{2: "0.1.1"}} // 2 在线，3 离线
	fanoutPresence(fp, 1, []int64{2, 3}, true)
	if len(fp.pushes) != 1 || fp.pushes[0].uid != 2 || fp.pushes[0].msgID != msgIDSCFriendPresence {
		t.Fatalf("pushes: %+v", fp.pushes)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFanoutPresence`
Expected: 编译失败 `fanoutPresence undefined`。

- [ ] **Step 3: 实现 presence.go**

```go
// src/servers/lobbysvr/internal/presence.go
package internal

import (
	"context"
	"strconv"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/serialize/json"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

const (
	msgIDSCFriendPresence uint32 = 2031
	msgIDSCMailNew        uint32 = 2033
)

var clientSerializer = json.NewSerializer() // 推送 body 用 client 序列化器(json)

// presenceClient 抽象在线查询与客户端推送，便于单测注入 fake。
type presenceClient interface {
	Query(uid int64) (gatewayNodeID string, online bool)
	Push(gatewayNodeID string, uid int64, msgID uint32, body []byte)
}

// clusterPresence 基于真实 router/gate 的 presenceClient
type clusterPresence struct{ cls cluster.Cluster }

func (c clusterPresence) Query(uid int64) (string, bool) {
	ctx := cluster.WithCluster(context.Background(), c.cls)
	rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Query_Rsp](
		ctx, c.cls, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
		"OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: uid})
	if err != nil {
		logger.Warn("presence query failed", logger.Int64("uid", uid), logger.Err(err))
		return "", false
	}
	if !rsp.Online || rsp.Entry == nil {
		return "", false
	}
	return rsp.Entry.GatewayNodeId, true
}

func (c clusterPresence) Push(gatewayNodeID string, uid int64, msgID uint32, body []byte) {
	gw, err := cluster.ParseNodeID(gatewayNodeID)
	if err != nil {
		logger.Warn("presence push: bad gateway nodeID", logger.String("nodeID", gatewayNodeID))
		return
	}
	if err := c.cls.Cast(context.Background(), gw, "GateHandler.pushtoclient",
		&gatepb.RPC_PushToClient{Uid: uid, MsgId: msgID, Body: body}); err != nil {
		logger.Warn("presence push: cast failed", logger.Int64("uid", uid), logger.Err(err))
	}
}

// fanoutPresence 对每个 friend 查在线，在线则推 SC_FriendPresence{self, online}。
// 同步执行（调用方在 off-loop goroutine 调用，避免阻塞主循环）。
func fanoutPresence(pc presenceClient, self int64, friends []int64, online bool) {
	if len(friends) == 0 {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_FriendPresence{Uid: self, Online: online})
	if err != nil {
		logger.Warn("presence: marshal body failed", logger.Err(err))
		return
	}
	for _, f := range friends {
		if gw, ok := pc.Query(f); ok {
			pc.Push(gw, f, msgIDSCFriendPresence, body)
		}
	}
}

// snapshotFriends 逐好友查在线，构造 SC_FriendList（同步，off-loop 调用）。
func snapshotFriends(pc presenceClient, friends []int64) *lobbypb.SC_FriendList {
	rsp := &lobbypb.SC_FriendList{}
	for _, f := range friends {
		_, online := pc.Query(f)
		rsp.Friends = append(rsp.Friends, &lobbypb.FriendEntry{Uid: f, Online: online})
	}
	return rsp
}

var _ = proto.Marshal // 保留 import（pushToClient 经 cls.Cast 已用 proto 编码，无需手动）
```

> 注：`var _ = proto.Marshal` 仅占位避免未用 import；若 `goimports` 报未用，删除该行与 `proto` import。

- [ ] **Step 4: 接线 Runtime + FriendList handler**

`runtime.go`：`Runtime` 加 `presence presenceClient` 字段；`NewRuntime` 默认 `clusterPresence{cls: cfg.Cluster}`（cfg.Cluster 为 nil 时置 nil-safe，测试可替换 `rt.presence`）。Login/Disconnect 接 fan-out：

```go
// NewRuntime 内（rt 构造后）：
//	if cfg.Cluster != nil { rt.presence = clusterPresence{cls: cfg.Cluster} }

// Login continuation 末尾，把 scanFriendAccepts 的 after 接到 presence fan-out：
//	p := rt.players[uid]
//	rt.scanFriendAccepts(uid, p, func() {
//		if rt.presence != nil {
//			friends := p.Friend().List()
//			go fanoutPresence(rt.presence, uid, friends, true)
//		}
//	})

// Disconnect 内，剔除前捕获好友并 fan-out 离线：
//	if ok && rt.presence != nil {
//		friends := p.Friend().List()
//		go fanoutPresence(rt.presence, uid, friends, false)
//	}
```

`lobby_handler.go` 加 `Friendlist`：

```go
// Friendlist route="LobbyHandler.friendlist"：off-loop 逐好友查在线，回 SC_FriendList
func (h *LobbyHandler) Friendlist(ctx context.Context, _ *lobbypb.CS_FriendList) (*lobbypb.SC_FriendList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		friends := p.Friend().List()
		pc := h.rt.presence
		if pc == nil {
			replyProto(replier, &lobbypb.SC_FriendList{}, nil)
			return
		}
		go func() {
			rsp := snapshotFriends(pc, friends)
			replyProto(replier, rsp, nil)
		}()
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 5: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestFanoutPresence|TestFriend' && go build ./...`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/presence.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/presence_test.go
git commit -m "feat(lobby): presence（FriendList 在线快照 + 登录/断连 push delta，lobby 驱动 fan-out）"
```

---

## Stage F — 心跳 → Touch 接线

### Task F1: gate 心跳转发 RPC_Touch_Notify 到绑定 lobby

**Files:**
- Modify: `src/servers/gatesvr/internal/gate_handler.go`, `src/servers/gatesvr/internal/gate_module.go`
- Test: `src/servers/gatesvr/internal/gate_handler_test.go`

`GateHandler.Heartbeat` 从「仅 Debug 日志」改为转发活跃信号给绑定 lobby（镜像 `GateModule.notifyPlayerOffline`）。节流在 lobby 侧（F2），gate 每跳都转发（廉价 Cast）。

- [ ] **Step 1: 写失败测试**（用记录 Cast 的 fakeCluster；复用 gate 既有测试 harness 构造 GateModule + 绑定会话）

```go
func TestHeartbeat_ForwardsTouchToLobby(t *testing.T) {
	fc := &fakeClusterRec{} // 记录 Cast(target, route, msg)；复用/新增于 gate 测试 harness
	mod := newTestGateModule(t, fc)
	s := bindSession(t, mod, 10001, "0.2.1") // 绑定 uid + BindNode("lobbysvr","0.2.1")
	h := NewGateHandler(mod)
	h.Heartbeat(ctxWithSession(s), &gatepb.CS_Heartbeat_OneWay{ClientTime: 123})
	if fc.lastRoute != "LobbyHandler.touch" {
		t.Fatalf("route=%q", fc.lastRoute)
	}
	tn, ok := fc.lastMsg.(*lobbypb.RPC_Touch_Notify)
	if !ok || tn.Uid != 10001 {
		t.Fatalf("msg=%+v", fc.lastMsg)
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/gatesvr/internal/ -run TestHeartbeat_ForwardsTouch`
Expected: 编译失败 `mod.ForwardTouch undefined` 或断言失败（当前 Heartbeat 不转发）。

- [ ] **Step 3: 实现**

`gate_module.go` 加 `ForwardTouch`（import 已有 lobbypb）：

```go
// ForwardTouch 把活跃信号转发给绑定 lobby（lobby 侧节流 + Touch onlinesvr）
func (g *GateModule) ForwardTouch(s *session.Session) {
	lobbyNode, ok := s.BoundNode("lobbysvr")
	if !ok {
		return
	}
	target, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("gate touch: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	if err := g.cls.Cast(g.ctx, target, "LobbyHandler.touch",
		&lobbypb.RPC_Touch_Notify{Uid: s.UID()}); err != nil {
		logger.Warn("gate touch: cast failed", logger.Int64("uid", s.UID()), logger.Err(err))
	}
}
```

`gate_handler.go` 改 `Heartbeat`：

```go
// Heartbeat 处理客户端心跳（msgID=1003，OneWay）：转发活跃信号给绑定 lobby
func (h *GateHandler) Heartbeat(ctx context.Context, _ *gatepb.CS_Heartbeat_OneWay) {
	sessionID := handler.SessionIDFromCtx(ctx)
	s, ok := h.module.Sessions().ByID(sessionID)
	if !ok || !s.IsBound() {
		return
	}
	h.module.ForwardTouch(s)
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/gatesvr/internal/ -run TestHeartbeat && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/gatesvr/internal/gate_handler.go src/servers/gatesvr/internal/gate_module.go src/servers/gatesvr/internal/gate_handler_test.go
git commit -m "feat(gate): 心跳转发 RPC_Touch_Notify 给绑定 lobby（活跃信号接线）"
```

### Task F2: lobby Touch handler + Runtime.Touch 节流

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`, `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

`LobbyHandler.Touch`（Notify）Submit → `Runtime.Touch`：`Player.lastTouch` 节流（< 2min 跳过），过阈值才 off-loop `onlinesvr.Touch`。`onlineTouch` 设为可替换字段（同 `onlineRegister`）便于单测。

- [ ] **Step 1: 写失败测试**

```go
func TestRuntime_TouchThrottle(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	var count int
	rt.onlineTouch = func(int64) { count++ }
	runOnLoop(t, rt, func() {
		rt.Touch(uid) // 首次：触发
		rt.Touch(uid) // 立即第二次：被节流
	})
	if count != 1 {
		t.Fatalf("throttle expected 1 touch, got %d", count)
	}
}

func TestRuntime_TouchUnknownPlayer_NoOp(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	var count int
	rt.onlineTouch = func(int64) { count++ }
	runOnLoop(t, rt, func() { rt.Touch(99999) })
	if count != 0 {
		t.Fatal("touch on absent player must no-op")
	}
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_Touch`
Expected: 编译失败 `rt.onlineTouch undefined`。

- [ ] **Step 3: 实现**

`runtime.go`：`Runtime` 加 `onlineTouch func(uid int64)` 字段；`NewRuntime` 设 `rt.onlineTouch = rt.touchOnline`；加常量与方法：

```go
const touchThrottle = 2 * time.Minute

// Touch 主循环内刷新活跃：节流后 off-loop Touch onlinesvr。
func (rt *Runtime) Touch(uid int64) {
	p, ok := rt.players[uid]
	if !ok {
		return
	}
	now := time.Now().UnixNano()
	if p.lastTouch != 0 && now-p.lastTouch < int64(touchThrottle) {
		return
	}
	p.lastTouch = now
	rt.onlineTouch(uid)
}

// touchOnline 经 router 向 onlinesvr 刷新活跃（best-effort，off-loop）
func (rt *Runtime) touchOnline(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Touch_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.touch", &onlinepb.RPC_Touch_Req{Uid: uid}); err != nil {
			logger.Warn("lobby touch: online touch failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}
```

`lobby_handler.go` 加 `Touch`（Notify）：

```go
// Touch route="LobbyHandler.touch"（Notify，无回包）
func (h *LobbyHandler) Touch(_ context.Context, req *lobbypb.RPC_Touch_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.Touch(uid) })
}
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_Touch && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): Touch handler + Runtime.Touch 节流（Player.lastTouch，off-loop onlinesvr.Touch）"
```

---

## Stage G — flush 加固

### Task G1: 键点 flush（flushSoon 合并）

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

把 C4 占位的 `FlushSoon`（即时 flush）升级为**合并写**：标记待 flush 玩家集，由短周期 coalesce tick 统一 flush，避免高频资源操作的写放大。

- [ ] **Step 1: 写失败测试**

```go
func TestFlushSoon_CoalescesToOneFlush(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()
	uid := int64(10001)
	loadPlayerSync(t, rt, uid)
	fs := rt.store.(*fakeStore)
	runOnLoop(t, rt, func() {
		rt.Player(uid).Currency().Gain("s", "gold", 10) // 标脏
		rt.FlushSoon(uid)
		rt.FlushSoon(uid) // 两次标记合并为一次待 flush
		rt.coalesceFlush() // 手动触发一次合并 flush
	})
	if _, ok := fs.flushed["10001:currency"]; !ok {
		t.Fatal("currency not flushed by coalesceFlush")
	}
	runOnLoop(t, rt, func() {
		if len(rt.dirtyFlush) != 0 {
			t.Fatal("pending flush set not cleared")
		}
	})
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFlushSoon_Coalesces`
Expected: 编译失败 `rt.coalesceFlush undefined`（`dirtyFlush` 字段亦未定义）。

- [ ] **Step 3: 实现**

`runtime.go`：`Runtime` 加 `dirtyFlush map[int64]bool`；`NewRuntime` 初始化 `dirtyFlush: make(map[int64]bool)`；常量；替换 `FlushSoon`；加 `coalesceFlush`；`Start` 注册 coalesce tick：

```go
const coalesceFlushInterval = 1 * time.Second

// FlushSoon 键点 flush：标记 uid 待 flush，由 coalesceFlush 合并落库（避免写放大）。
func (rt *Runtime) FlushSoon(uid int64) { rt.dirtyFlush[uid] = true }

// coalesceFlush 合并 flush 所有待 flush 玩家（coalesce tick 回调，主循环执行）。
func (rt *Runtime) coalesceFlush() {
	for uid := range rt.dirtyFlush {
		if p, ok := rt.players[uid]; ok {
			rt.flushPlayer(uid, p, nil)
		}
		delete(rt.dirtyFlush, uid)
	}
}
```

`Start` 内追加（在周期 flush tick 旁）：

```go
	rt.tw.Tick(coalesceFlushInterval, rt.coalesceFlush)
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestFlushSoon|TestPurchase|TestMailClaim' && go build ./...`
Expected: PASS（Purchase/MailClaim 仍走 FlushSoon，行为不变）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): 键点 flush 改为 coalesceFlush 合并写（dirtyFlush 集 + 短周期 tick）"
```

### Task G2: 优雅停机 drain（等 in-flight flush）+ Mongo 可取消 ctx

**Files:**
- Modify: `src/servers/lobbysvr/internal/runtime.go`, `src/common/mongo/mongo.go`
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

`Stop` 现仅排空 taskqueue；加固为**先 flushAllDirty，再排空 tq 直到在途 flush 回调全部完成**（或 drain 超时）。Mongo Client 改用可取消 base ctx（`Close` 取消），兜底真正卡死的 op。

- [ ] **Step 1: 写失败测试**（gatedStore：FlushField 把 done 挂起，releaseAll 后才经 dispatcher 回调；断言 Stop 阻塞等待）

```go
func TestRuntime_StopWaitsForInflightFlush(t *testing.T) {
	gs := newGatedStore()
	rt := NewRuntime(RuntimeConfig{
		Store: gs, MailStore: newFakeMailStore(),
		Tick: 10 * time.Millisecond, FlushInterval: time.Hour,
	})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	runOnLoop(t, rt, func() { rt.Player(uid).Bag().Add("op", 1, 1) }) // 标脏

	stopped := make(chan struct{})
	go func() { rt.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned before in-flight flush completed")
	case <-time.After(50 * time.Millisecond):
	}
	gs.releaseAll() // 释放挂起的 flush done
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after flush completed")
	}
	if !gs.flushedField("1", "bag") {
		t.Fatal("bag not flushed on shutdown")
	}
}
```

`gatedStore`（追加到 runtime_test.go 或 store_test.go）：

```go
// gatedStore：Load 同步；FlushField 挂起 done，releaseAll 经 dispatcher 投递回调。
type gatedStore struct {
	docs    map[int64]*PlayerDoc
	flushed map[string]bool
	pending []func()
}

func newGatedStore() *gatedStore {
	return &gatedStore{docs: map[int64]*PlayerDoc{}, flushed: map[string]bool{}}
}
func (g *gatedStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	d, ok := g.docs[uid]
	done(d, ok, nil)
}
func (g *gatedStore) FlushField(d taskqueue.Dispatcher, uid int64, field string, _ any, done func(error)) {
	g.pending = append(g.pending, func() {
		g.flushed[strconv.FormatInt(uid, 10)+":"+field] = true
		d.Enqueue(func() { done(nil) })
	})
}
func (g *gatedStore) releaseAll() {
	for _, p := range g.pending {
		p()
	}
	g.pending = nil
}
func (g *gatedStore) flushedField(uid, field string) bool { return g.flushed[uid+":"+field] }

var _ DocStore = (*gatedStore)(nil)
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_StopWaits`
Expected: 失败——当前 `Stop` 不等在途 flush（`stopped` 立即返回）。

- [ ] **Step 3: 实现 drain**

`runtime.go`：`Runtime` 加 `inflight atomic.Int64`（import `sync/atomic`）；`flushPlayer` 的 store 调用前后计数；loop 的 stop 分支改为 flush + drain：

```go
// flushPlayer 内，每个 FlushField 调用包裹 inflight 计数：
//	rt.inflight.Add(1)
//	rt.store.FlushField(rt.tq, uid, comp.Field(), state, func(err error) {
//		defer rt.inflight.Add(-1)
//		... 原有 done 逻辑 ...
//	})

const drainTimeout = 5 * time.Second

// loop 的 stop 分支替换：
//	case <-rt.stopCh:
//		rt.flushAllDirty()
//		rt.drain()
//		return

// drain 排空 tq 直到在途 flush 回调清零（或超时兜底）。
func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("lobby drain timeout, abandoning in-flight",
				logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}
```

`mongo.go`：Client 加可取消 base ctx（`Close` 取消），各 op 用 `c.ctx` 替换 `context.Background()`：

```go
// Client 加字段：
//	ctx    context.Context
//	cancel context.CancelFunc
// Connect 内连接成功后：
//	bctx, cancel := context.WithCancel(context.Background())
//	return &Client{cli: cli, db: cli.Database(dbName), ctx: bctx, cancel: cancel}, nil
// Close 内 Disconnect 前：
//	c.cancel()
// FindByID/UpsertSetByID/InsertOne/Find/FindOneAndUpdate 内的 context.Background() 全部替换为 c.ctx。
```

> 注：连接超时仍用独立 `context.WithTimeout`（Connect/Close 内），仅**异步 CRUD** 用 `c.ctx`。drain 在 `mongo.Close` 之前完成（main 关闭顺序：先 `Runtime.Stop`→后 `mongoClient.Close`），故正常路径在途 flush 不被取消；`c.cancel()` 只兜底卡死 op。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_Stop -race && go test ./src/common/mongo/ && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go src/common/mongo/mongo.go
git commit -m "feat(lobby): 优雅停机 drain 等在途 flush + Mongo 可取消 base ctx（补 P3a 欠账）"
```

### Task G3: 多组件 flush 健壮性

**Files:**
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

验证 3+ 脏组件并发 flush 下：失败组件重标脏重试、成功组件清脏、`after`（剔除）只触发一次。纯测试（不改实现，验证 P3a `fireAfter` 守护 + P3b 多组件路径）。

- [ ] **Step 1: 写测试**（failFieldStore：对指定 field 的 FlushField 返回 error）

```go
// failFieldStore：对 failField 的 flush 返回错误（同步 done），其余成功。
type failFieldStore struct {
	docs      map[int64]*PlayerDoc
	failField string
	flushes   int
}

func (s *failFieldStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	d, ok := s.docs[uid]
	done(d, ok, nil)
}
func (s *failFieldStore) FlushField(_ taskqueue.Dispatcher, _ int64, field string, _ any, done func(error)) {
	s.flushes++
	if field == s.failField {
		done(errFlush)
		return
	}
	done(nil)
}

var errFlush = errorsNew("flush boom") // 用 errors.New；此处占位说明

func TestFlushPlayer_PartialFailureRemarksDirtyAndFiresAfterOnce(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	afterCount := 0
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		p.Bag().Add("o1", 1, 1)          // 脏
		p.Currency().Gain("o2", "gold", 5) // 脏（flush 会失败）
		p.Friend().Add(2)                 // 脏
		rt.flushPlayer(uid, p, func() { afterCount++ })
	})
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		if p.Currency().Dirty() == false {
			t.Fatal("failed currency flush must re-mark dirty")
		}
		if p.Bag().Dirty() || p.Friend().Dirty() {
			t.Fatal("succeeded components must be clean")
		}
	})
	if afterCount != 1 {
		t.Fatalf("after must fire exactly once, got %d", afterCount)
	}
}
```

> 注：把 `errorsNew`/`errFlush` 用标准 `errors.New("flush boom")` 实现（import `"errors"`）；上方占位仅为说明。

- [ ] **Step 2: 运行验证通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestFlushPlayer_PartialFailure -race`
Expected: PASS（验证既有 flush 逻辑在多组件下正确；如失败说明 `fireAfter`/重标脏有回归，需修 `runtime.go`）。

- [ ] **Step 3: Commit**

```bash
git add src/servers/lobbysvr/internal/runtime_test.go
git commit -m "test(lobby): 多组件 flush 健壮性（部分失败重标脏 + after 仅触发一次）"
```

---

## Stage H — 装配 + 集成

### Task H1: main/module 装配 MailStore

**Files:**
- Modify: `src/servers/lobbysvr/main.go`（或构造 `RuntimeConfig` 处，可能在 `lobby_module.go`）

新 handler 方法经既有 `RegisterHandler(NewLobbyHandler(rt))` 反射注册自动覆盖（route 同方法名）；gate `PushToClient` 同理。本任务只需把 `MailStore` 接进 `RuntimeConfig`。

- [ ] **Step 1: 定位 `NewRuntime(RuntimeConfig{...})` 构造处**

Run: `grep -rn "NewRuntime(RuntimeConfig" src/servers/lobbysvr/`
Expected: 命中 main.go 或 lobby_module.go 中的构造点（P3a 已在此连接 Mongo 并构造 `DocStore`）。

- [ ] **Step 2: 接入 MailStore**

在该构造点，复用已连接的 `*mongo.Client`（P3a 已有，用于 `NewMongoStore`）构造 `NewMongoMailStore(client)` 并填入 `RuntimeConfig.MailStore`：

```go
// 例（按实际变量名调整）：
//	mongoClient, err := mongo.Connect(cfg.Mongo.URI, cfg.Mongo.DB, cfg.Mongo.ConnectTimeout())
//	...
//	rt := internal.NewRuntime(internal.RuntimeConfig{
//		NodeID:    cfg.Node.NodeID(),
//		Cluster:   natsCluster,
//		Store:     internal.NewMongoStore(mongoClient),
//		MailStore: internal.NewMongoMailStore(mongoClient), // 新增
//		...
//	})
```

确认 gate 侧无需改动：`PushToClient` 随 `RegisterHandler(NewGateHandler(...))` 反射注册（与 `KickSession` 同路径）。若 lobbysvr/gatesvr 的注册是显式方法白名单而非整对象反射，则把新方法名补入。

- [ ] **Step 3: 验证编译 + 全量测试**

Run: `go build ./... && go vet ./... && go test ./src/... -race`
Expected: 全绿。

- [ ] **Step 4: Commit**

```bash
git add src/servers/lobbysvr/
git commit -m "chore(lobby): main 装配 MailStore（NewMongoMailStore 接入 Runtime）"
```

### Task H2: 集成测试（//go:build integration，沙箱编译验证）

**Files:**
- Modify/Create: `src/servers/lobbysvr/internal/lobby_ec_integration_test.go`（扩展 P3a 既有集成测试）

容器 NATS+etcd+MongoDB 下端到端验证；沙箱仅 `go vet -tags integration` 编译验证。复用 P3a 既有集成 harness（起 lobby + gate 集群边界驱动）。

- [ ] **Step 1: 加购买落库回读集成测试**

```go
//go:build integration

// 在既有集成 harness 上追加：登录 → 充值 → 购买（扣币+加道具）→ 断连 flush → 重登读回。
func TestIntegration_PurchasePersistsAndReloads(t *testing.T) {
	h := startLobbyCluster(t) // 复用 P3a harness：返回可发集群 RPC 的句柄 + 清库
	defer h.Stop()
	uid := int64(20001)
	h.login(t, uid)
	h.gainCurrency(t, uid, "gold", 100) // 经 RPC 或直接 mongo 预置
	rsp := h.purchase(t, uid, "buy1", "gold", 30, 555)
	if rsp.Code != 0 || rsp.Balance != 70 || rsp.ItemCount != 1 {
		t.Fatalf("purchase: %+v", rsp)
	}
	h.disconnect(t, uid) // 触发 flush + 剔除
	h.login(t, uid)      // 重登从 Mongo 读回
	cur := h.currencyQuery(t, uid)
	bag := h.bagList(t, uid)
	if balanceOf(cur, "gold") != 70 || countOf(bag, 555) != 1 {
		t.Fatalf("reload mismatch: cur=%v bag=%v", cur, bag)
	}
	// 幂等：重复 opID 不双扣双发
	rsp2 := h.purchase(t, uid, "buy1", "gold", 30, 555)
	if rsp2.Balance != 70 || rsp2.ItemCount != 1 {
		t.Fatalf("dup purchase not idempotent: %+v", rsp2)
	}
}
```

- [ ] **Step 2: 加离线邮件投递 + 幂等领取集成测试**

```go
//go:build integration

// 给离线玩家投递带附件邮件 → 该玩家登录 List 可见 → Claim 发放且重复 Claim 不双发。
func TestIntegration_OfflineMailDeliveryAndClaim(t *testing.T) {
	h := startLobbyCluster(t)
	defer h.Stop()
	recipient := int64(20002)
	// 收件人离线时直接向 mailbox 投递（模拟系统/他人投递）
	h.insertMail(t, &MailDoc{To: recipient, From: 0, Type: MailTypeNormal,
		Attachments: []Attachment{{Kind: "gold", Count: 50}}})
	h.login(t, recipient)
	mails := h.mailList(t, recipient)
	if len(mails) != 1 {
		t.Fatalf("offline mail not visible: %d", len(mails))
	}
	id := mails[0].MailId
	r1 := h.mailClaim(t, recipient, id)
	if r1.Code != 0 || len(r1.Granted) != 1 {
		t.Fatalf("claim1: %+v", r1)
	}
	r2 := h.mailClaim(t, recipient, id)
	if r2.Code != 1 {
		t.Fatalf("dup claim must fail: %+v", r2)
	}
}
```

- [ ] **Step 3: 加好友握手最终一致集成测试**

```go
//go:build integration

// A 加 B → B 接受（B 即含 A）→ A 重登 accept-scan（A 含 B）。
func TestIntegration_FriendHandshakeEventualConsistency(t *testing.T) {
	h := startLobbyCluster(t)
	defer h.Stop()
	a, b := int64(20003), int64(20004)
	h.login(t, a)
	h.login(t, b)
	h.friendAdd(t, a, b)
	reqID := h.findMail(t, b, MailTypeFriendReq, a)
	h.friendRespond(t, b, reqID, true)
	if !h.hasFriend(t, b, a) {
		t.Fatal("B should have A after accept")
	}
	h.disconnect(t, a)
	h.login(t, a) // accept-scan
	if !h.hasFriend(t, a, b) {
		t.Fatal("A should have B after re-login")
	}
}
```

> harness 方法（`startLobbyCluster`/`login`/`purchase`/`insertMail`/`friendAdd` 等）：扩展 P3a `lobby_ec_integration_test.go` 既有 harness；沿用其集群边界驱动（gate→lobby cluster RPC）。`insertMail` 经 `mongoMailStore.Insert` 直写 `mailbox`。

- [ ] **Step 4: 编译验证**

Run: `go vet -tags integration ./src/servers/lobbysvr/...`
Expected: 编译通过（沙箱不实跑；有 Docker 时 `go test -tags integration` 实跑）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_ec_integration_test.go
git commit -m "test(lobby): P3b 集成测试（购买落库回读 / 离线邮件 / 好友握手，编译验证）"
```

---

## 附录 A：共享测试 harness（`internal/testhelper_test.go`）

各 Stage 的 `*Sync` / `runOnLoop` / `loadPlayerSync` / `newTestRuntime` 在此一次性定义（避免重复）。**在 Stage C 第一个用到它们的任务前先建此文件。**

```go
// src/servers/lobbysvr/internal/testhelper_test.go
package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
)

// --- fake Replier：捕获主循环异步回包 ---
type replyResult struct {
	data []byte
	err  error
}
type fakeReplier struct{ ch chan replyResult }

func newFakeReplier() *fakeReplier { return &fakeReplier{ch: make(chan replyResult, 1)} }
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

// ctxWith 构造带 Replier + ClusterSession{uid} 的 handler ctx
func ctxWith(uid int64, r cluster.Replier) context.Context {
	ctx := cluster.WithReplier(context.Background(), r)
	return cluster.WithSession(ctx, &clusterpb.ClusterSession{Uid: uid})
}

// newTestRuntime 构造带 fakeStore + fakeMailStore 的 Runtime 并 Start；
// online register/unregister/touch 替换为 no-op；presence 默认 nil（presence 测试自行替换 rt.presence）。
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

// loadPlayerSync 驱动 uid 登录加载，并 barrier 等待登录 continuation（含 accept-scan）排空。
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

func purchaseSync(t *testing.T, rt *Runtime, uid int64, opID, kind string, price int64, itemID int32) *lobbypb.SC_Purchase {
	return driveReq[*lobbypb.SC_Purchase](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Purchase(ctx, &lobbypb.CS_Purchase{OpId: opID, Kind: kind, Price: price, ItemId: itemID})
	})
}
func mailListSync(t *testing.T, rt *Runtime, uid int64) *lobbypb.SC_MailList {
	return driveReq[*lobbypb.SC_MailList](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Maillist(ctx, &lobbypb.CS_MailList{})
	})
}
func mailClaimSync(t *testing.T, rt *Runtime, uid int64, mailID string) *lobbypb.SC_MailClaim {
	return driveReq[*lobbypb.SC_MailClaim](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Mailclaim(ctx, &lobbypb.CS_MailClaim{MailId: mailID})
	})
}
func friendAddSync(t *testing.T, rt *Runtime, uid, target int64) *lobbypb.SC_FriendAdd {
	return driveReq[*lobbypb.SC_FriendAdd](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Friendadd(ctx, &lobbypb.CS_FriendAdd{Target: target})
	})
}
func friendRespondSync(t *testing.T, rt *Runtime, uid int64, mailID string, accept bool) *lobbypb.SC_FriendRespond {
	return driveReq[*lobbypb.SC_FriendRespond](t, rt, uid, func(h *LobbyHandler, ctx context.Context) {
		h.Friendrespond(ctx, &lobbypb.CS_FriendRespond{MailId: mailID, Accept: accept})
	})
}
```

> `mailListSync` 返回 `*SC_MailList`（含 `MailId` hex）。测试里用 `ms.Mails[i].MailId` 取 id 传给 claim/respond。`time` import 仅 helper 用；若某 Stage 尚未引入对应 handler 方法，则该 `*Sync` 函数在引入该 handler 的任务里再加（避免未定义引用编译失败）——**按任务顺序，引入 handler 的任务同时加其 `*Sync` 驱动器到本文件**。

---

## 自检（Self-Review）

**Spec 覆盖**（逐节对照设计 Spec §3.1 范围内）：
- 基石不变式 + mailbox 集合 → Stage D（D1）+ §4 全程贯彻 ✓
- Currency/Friend/Mail 组件 → C1-C2 / E1-E2 / D1-D2 ✓
- 好友请求/接受/拒绝/删除 + 离线投递 → E3 + D ✓
- Touch 接线 → F1-F2 ✓
- presence（pull 快照 + push delta） → E5 ✓
- 通用 gate 推送 PushToClient → E4 ✓
- 购买显式编排 + CurrencyChanged 事件 → C3-C4 ✓
- flush 加固（键点/drain/多组件） → G1-G3 ✓
- op-id 去重抽取 → A2 ✓
- proto + gen_routes → B1 ✓

**类型一致性自检**：`opDedup.seen/remember`（A2）被 Bag/Currency 一致使用；`MailStore` 四方法（D1）被 fake/mongo/Mail/Runtime 一致调用；`presenceClient.Query/Push`（E5）被 clusterPresence/fakePresence/fanoutPresence/snapshotFriends 一致；`FlushSoon`（C4 占位→G1 合并实现）签名 `(uid int64)` 全程不变；`Runtime.onlineTouch`（F2）镜像 `onlineRegister` 字段模式；`buildPlayer(uid, doc)` 签名不变（Mail 经 `attachMail` 装配，不改 buildPlayer）。

**占位扫描**：无 TBD/TODO；测试 harness 在附录 A 完整定义；`presence.go` 的 `var _ = proto.Marshal` 已注明按 goimports 取舍；G3 的 `errorsNew` 已注明用标准 `errors.New`。

**已知 P3b 范围外（设计 Spec §3.2 / §11 风险，留后续）**：mailbox 归档/分页；presence fan-out 批量优化；跨重登 op-id 去重；客户端↔gate 转发响应 json/proto（推送方向已定，请求-响应方向仍留 P3a 遗留）；框架风险 backlog 其余项（P5）。

---

## 执行交接

计划完成。两种执行方式：

**1. Subagent-Driven（推荐）** — 每任务派新 subagent，任务间评审，快速迭代（沿用 P3a 节奏：核心任务 spec+质量双评审 + 整支 `-race` 终审）。

**2. Inline Execution** — 本会话内按 `executing-plans` 批量执行 + 检查点。

选哪种？
</content>
