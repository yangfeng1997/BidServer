# P4a 匹配凑桌 → 开局 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打通 `登录→发起匹配→匹配凑桌→开局→拿到 room 绑定` 的前半弧：新建 matchsvr/roomsvr，扩 lobby/router/online，首引 JetStream 匹配队列设施。

**Architecture:** 全部跨服寻址经 routersvr 转发（复用现成 `CONSISTENT_HASH`/`DIRECT` resolve）。lobby 发起匹配经 router 的新 `publishmatch` 写入 JetStream `MATCH` stream；matchsvr 单 goroutine 主循环（仿 lobby `Runtime`）从 durable consumer 消费 → `(uid,reqId)` 去重入 MMR 队列 → 凑齐 N 个窗口内玩家成桌生成 `gameId` → off-loop 经 router 一致性哈希(key=gameId)选 room 开局拿回 `room_node_id` → 经 router DIRECT 回告各 lobby；lobby 置内存 `roomAffinity` + 同步 online + 推 `SC_MatchFound`。matchsvr/roomsvr 共享态全部经各自单主循环串行（零锁），off-loop 网络 IO 回调经 `Submit` 回环（仿 lobby `inflight`/drain）。

**Tech Stack:** Go 1.26；protobuf（proto3 + 自定义 options 扩展）；NATS core + `nats.go/jetstream` v1.52.0；etcd discovery；MongoDB（仅 lobby Rating 落库）；框架内建 `taskqueue`（跨 goroutine 任务队列）+ `timewheel`（帧驱动定时轮）。

---

## 关键设计决策（实现前先读，本计划据此核定 Spec §7/§9 留待 plan 的开放项）

1. **serverTypeID 分配**：已占用 `gate=1 / lobby=2 / online=5 / router=6`；本阶段 **roomsvr=7**（沿用 pre-P4 既见 `1.7.3`）、**matchsvr=8**。NodeID：matchsvr=`1.8.1`、roomsvr=`1.7.1`。唯一硬约束是 8-bit serverTypeID 全 world 内唯一；3/4 留给 world/scene（架构文档示例 `1.3.x`/`1.4.x`），不复用。
2. **gameId 生成方案**：matchsvr 单主循环内 `fmt.Sprintf("%s-%d", nodeID, gameSeq)`（如 `1.8.1-1`），`gameSeq` 主循环内单调自增。跨 matchsvr 实例天然唯一（nodeID 唯一），无需 uuid 依赖。
3. **`GameStarted` 命名纠偏**：Spec §7 写作 `RPC_GameStarted_Notify` 却同时给了 `_Rsp` —— 矛盾。§3 明确 match→lobby 改**同步 RPC 取 ack**，故定为 **`RPC_GameStarted_Req` / `RPC_GameStarted_Rsp`**（route=`LobbyHandler.gamestarted`），非 Notify。已偏离 Spec 字面，按"需要 ack"的设计意图取 Req/Rsp。
4. **JetStream 设施落点**：新建框架级包 `src/common/matchqueue/`（接口 `MatchQueue` + 内存 fake `MemoryQueue` 单测用 + 真实 `JetStreamQueue` 适配器**仅 `go vet` 编译验证**，沙箱无 Docker 不实跑）。stream/subject/durable 名为包级常量（router 发布与 matchsvr 消费共用同一真相源）。
5. **服务器间 RPC 无 msg_id**：`MatchRequest`/`RPC_PublishMatch_Rsp`/`RPC_GameStarted_*`/`RPC_OpenGame_*`/`RPC_BindRoom_*`/`RPC_UnbindRoom_*` 均为**纯服务间 RPC**，proto 不带任何 option（route 由 `app.RegisterHandler` 反射方法名注册，调用方传字符串 innerRoute）。**仅** lobby 客户端面 `CS_StartMatch`/`SC_StartMatch`/`SC_MatchFound` 带 `msg_id`（gen_routes 据此扩 gate 转发/响应表）。故 `match.proto`/`room.proto` 不 import `options.proto`。
6. **off-loop 编排可测性**：matchsvr/lobby 的跨服调用统一封装为 **Runtime 上可替换的函数 hook**（默认接真实 router，单测注入 fake），仿 lobby `onlineRegister`/`presence`。
7. **proto 生成命令**（沙箱 protoc 未装系统级，借用既有；见 [[gameserver-dev-workflow]]）：

   ```bash
   PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
   INC=/game/dev/silver-server/3rd/protobuf/include
   "$PROTOC" --go_out=. --go_opt=module=project --proto_path=. --proto_path="$INC" \
     protocal/options.proto protocal/match.proto protocal/room.proto protocal/lobby.proto protocal/online.proto
   go run ./tools/gen_routes
   ```

**默认常量**（实现时定义）：matchsvr `matchSize=2`、`mmrWindow=200`；开局 `defaultItemID=1`、`defaultCountdownSec=30`。

---

## 文件结构图（创建 / 修改）

**proto（Task 1）**
- Create: `protocal/match.proto`、`protocal/room.proto`
- Modify: `protocal/lobby.proto`（+CS/SC_StartMatch、SC_MatchFound）、`protocal/online.proto`（+OnlineEntry room 字段、bindroom/unbindroom）
- 生成产物（勿手改）：`protocal/gen/match/`、`protocal/gen/room/`、`protocal/gen/lobby/`、`protocal/gen/online/`、`protocal/gen/routes/routes.go`

**JetStream 设施（Task 2-3）**
- Create: `src/common/matchqueue/matchqueue.go`（接口+常量）、`memory.go`（fake）、`memory_test.go`、`jetstream.go`（真实适配器，vet-only）

**online 扩展（Task 4-5）**
- Modify: `src/servers/onlinesvr/internal/directory.go`（Entry +room 字段、BindRoom/UnbindRoom）、`online_handler.go`（Query 带 room、+Bindroom/Unbindroom）
- Test: `directory_test.go`、`online_handler_test.go`

**router 扩展（Task 6）**
- Modify: `src/servers/routersvr/internal/router_module.go`（+mq 字段、PublishMatch）、`router_handler.go`（+Publishmatch）、`src/servers/routersvr/main.go`（注入真实 JetStream publisher）
- Test: `router_handler_test.go`（+publishmatch；更新既有 `NewRouterModule` 调用点传 nil mq）

**lobby 扩展（Task 7-10）**
- Create: `src/servers/lobbysvr/internal/component_rating.go`、`component_rating_test.go`
- Modify: `store.go`（PlayerDoc +Rating、buildPlayer 注册、accessor）、`player.go`（+roomAffinity）、`lobby_handler.go`（+Startmatch、Gamestarted）、`runtime.go`（+publishMatch/bindRoom hook、StartMatch/BindRoom/PushMatchFound、reqSeq）、`presence.go`（+SC_MatchFound 推送 helper + msgID 常量）
- Test: `player_test.go`、`lobby_handler_test.go`、`runtime_test.go`、`presence_test.go`

**roomsvr 新建（Task 11-12）**
- Create: `src/servers/roomsvr/main.go`、`internal/game.go`、`internal/runtime.go`、`internal/room_handler.go`、`internal/room_module.go`、`internal/reply.go`
- Test: `internal/game_test.go`、`runtime_test.go`、`room_handler_test.go`
- Create: `conf/room.yaml`

**matchsvr 新建（Task 13-15）**
- Create: `src/servers/matchsvr/main.go`、`internal/queue.go`、`internal/runtime.go`、`internal/match_consumer.go`、`internal/match_module.go`
- Test: `internal/queue_test.go`、`runtime_test.go`、`match_consumer_test.go`
- Create: `conf/match.yaml`

**集成 + 文档（Task 16-17）**
- Create: `src/servers/matchsvr/internal/match_integration_test.go`（`//go:build integration`，沙箱仅编译验证）
- Modify: `architecture.md`、`cluster.md`、`development.md`

---

## Task 1: proto 定义 + 路由表生成

定义全部新增/扩展 proto 并重生成。生成代码非 TDD —— 验收 = `go build ./...` 通过 + 路由表含预期条目。**此任务必须最先完成**（后续所有 Go 代码依赖生成类型）。

**Files:**
- Create: `protocal/match.proto`、`protocal/room.proto`
- Modify: `protocal/lobby.proto`、`protocal/online.proto`

- [ ] **Step 1: 写 `protocal/match.proto`**

```proto
syntax = "proto3";

package match;

option go_package = "project/protocal/gen/match";

// MatchRequest 匹配请求：既是 JetStream payload，也是 router publishmatch 入参（纯服务间，无 msg_id）
message MatchRequest {
  int64  uid           = 1;
  string req_id        = 2; // 幂等键：matchsvr 消费侧按 (uid, req_id) 去重
  int64  mmr           = 3;
  string lobby_node_id = 4; // 发起匹配的 lobby NodeID 串（回告 DIRECT 的 routing_key）
}

// RPC_PublishMatch_Rsp router 发布响应（req 直接复用 MatchRequest，route="RouterHandler.publishmatch"）
message RPC_PublishMatch_Rsp {
  int32 code = 1; // 0=已发布到 MATCH stream；非 0=发布失败
}

// RPC_GameStarted_Req match → lobby 开局回告（经 router DIRECT，route="LobbyHandler.gamestarted"）
message RPC_GameStarted_Req {
  int64  uid          = 1;
  string game_id      = 2;
  string room_node_id = 3;
}
message RPC_GameStarted_Rsp {
  int32 code = 1; // 0=lobby 已置亲和；负=玩家未加载等失败
}
```

- [ ] **Step 2: 写 `protocal/room.proto`**

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
}
message RPC_OpenGame_Rsp {
  int32  code         = 1; // 0=已建局（含幂等命中）；非 0=参数非法等
  string room_node_id = 2; // room 自身 NodeID 串，match 据此回告 lobby
}
```

- [ ] **Step 3: 扩 `protocal/lobby.proto`** —— 在文件末尾「服务器 → 客户端 推送」段之前（即 `SC_MailClaim` 之后、`SC_FriendPresence` 之前的位置）插入匹配 CS/SC，再在推送段加 `SC_MatchFound`。**msg_id 续 2000 段：2034/2035/2036（已确认空闲，当前最大 2033）。**

在 `SC_MailClaim`（msg_id 2022）之后插入：

```proto
// --- 客户端 ↔ lobby 匹配 ---

// CS_StartMatch 发起匹配请求（无业务字段；reqId 由 lobby 生成）
message CS_StartMatch {
  option (options.msg_id)         = 2034;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.startmatch";
}

// SC_StartMatch 发起匹配响应
message SC_StartMatch {
  option (options.msg_id) = 2035;
  int32 code = 1; // 0=已入队；1=已在局中；负=失败（未加载等）
}
```

在文件末尾推送段（`SC_MailNew` 之后）追加：

```proto
// SC_MatchFound 匹配成功推送（仅 msg_id，gate 据此推；无 server_type/handler_method）
message SC_MatchFound {
  option (options.msg_id) = 2036;
  string room_node_id = 1;
  string game_id      = 2;
}
```

- [ ] **Step 4: 扩 `protocal/online.proto`** —— `OnlineEntry` 加 room 字段；新增 BindRoom/UnbindRoom RPC（纯服务间，无 option）。

`OnlineEntry` 改为（追加字段 6/7）：

```proto
message OnlineEntry {
  int64  uid             = 1;
  string gateway_node_id = 2;
  string lobby_node_id   = 3;
  int64  login_time      = 4; // Unix 纳秒
  int64  last_active     = 5; // Unix 纳秒
  string room_node_id    = 6; // 当前所属 room NodeID 串（未在局为空）
  string game_id         = 7; // 当前对局 gameId（未在局为空）
}
```

在文件末尾 `RPC_KickSession_Notify` 之后追加：

```proto
// RPC_BindRoom_Req 绑定 room 亲和（route="OnlineHandler.bindroom"，绝对覆盖写）
message RPC_BindRoom_Req {
  int64  uid          = 1;
  string room_node_id = 2;
  string game_id      = 3;
}
message RPC_BindRoom_Rsp {
  int32 code = 1; // 0=已绑定；非 0=条目不在线
}

// RPC_UnbindRoom_Req 清除 room 亲和（route="OnlineHandler.unbindroom"；P4a 仅建 handler，wiring 留 P4b）
message RPC_UnbindRoom_Req {
  int64 uid = 1;
}
message RPC_UnbindRoom_Rsp {
  int32 code = 1;
}
```

- [ ] **Step 5: 生成代码 + 路由表**

Run（见上「proto 生成命令」）：

```bash
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
INC=/game/dev/silver-server/3rd/protobuf/include
"$PROTOC" --go_out=. --go_opt=module=project --proto_path=. --proto_path="$INC" \
  protocal/options.proto protocal/match.proto protocal/room.proto protocal/lobby.proto protocal/online.proto
go run ./tools/gen_routes
```

Expected: 生成 `protocal/gen/match/match.pb.go`、`protocal/gen/room/room.pb.go`，更新 `lobby/online` pb 与 `routes/routes.go`，无报错。

- [ ] **Step 6: 验证生成结果**

Run:

```bash
go build ./...
grep -E '2034|2035|2036' protocal/gen/routes/routes.go
```

Expected:
- `go build ./...` 通过（生成类型可编译）。
- `routes.go` 含：`MsgRouteTable` 有 `2034: "LobbyHandler.startmatch"`；`ForwardTable` 有 `2034: "lobbysvr"`；`RespMsgIDTable` 有 `2034: 2035`。`2036`（SC_MatchFound）因无 server_type/handler_method 不入任何表（推送按 msgId 直发，符合预期）。

- [ ] **Step 7: Commit**

```bash
git add protocal/
git commit -m "feat: P4a proto——新增 match/room、扩 lobby(StartMatch/MatchFound)/online(room 绑定)，重生成路由表"
```

---

## Task 2: MatchQueue 接口 + 内存 fake

框架级 JetStream 设施的抽象与单测用内存实现。

**Files:**
- Create: `src/common/matchqueue/matchqueue.go`、`src/common/matchqueue/memory.go`、`src/common/matchqueue/memory_test.go`

- [ ] **Step 1: 写失败测试 `memory_test.go`**

```go
package matchqueue

import (
	"context"
	"testing"

	matchpb "project/protocal/gen/match"
	"google.golang.org/protobuf/proto"
)

func TestMemoryQueue_PublishConsumeAck(t *testing.T) {
	q := NewMemoryQueue()
	var got []*matchpb.MatchRequest
	if err := q.Consume(context.Background(), DurableMatchsvr, func(_ context.Context, data []byte) error {
		var req matchpb.MatchRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			return err
		}
		got = append(got, &req)
		return nil // ack
	}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if err := q.Publish(context.Background(), SubjectMatchRequest, &matchpb.MatchRequest{Uid: 7, ReqId: "r1", Mmr: 1000}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(got) != 1 || got[0].Uid != 7 || got[0].ReqId != "r1" {
		t.Fatalf("want 1 msg uid=7 r1, got %+v", got)
	}
	if n := len(q.Published()); n != 1 {
		t.Fatalf("want 1 published, got %d", n)
	}
}

func TestMemoryQueue_Redeliver(t *testing.T) {
	q := NewMemoryQueue()
	calls := 0
	_ = q.Consume(context.Background(), DurableMatchsvr, func(_ context.Context, _ []byte) error {
		calls++
		return nil
	})
	_ = q.Publish(context.Background(), SubjectMatchRequest, &matchpb.MatchRequest{Uid: 1, ReqId: "r1"})
	if err := q.Redeliver(context.Background(), 0); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 handler calls (publish + redeliver), got %d", calls)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./src/common/matchqueue/ -run TestMemoryQueue -v`
Expected: FAIL（`matchqueue` 包不存在 / undefined `NewMemoryQueue`）。

- [ ] **Step 3: 写 `matchqueue.go`（接口 + 常量）**

```go
// Package matchqueue 提供匹配请求队列设施：router 侧发布、matchsvr 侧 durable 消费。
// 唯一真实现走 JetStream（at-least-once，匹配请求不丢）；单测用内存 fake。
// stream/subject/durable 名为包级常量，发布端与消费端共用同一真相源。
package matchqueue

import (
	"context"

	"google.golang.org/protobuf/proto"
)

const (
	StreamMatch         = "MATCH"          // JetStream stream 名
	SubjectMatchRequest = "match.request"  // 匹配请求 subject
	DurableMatchsvr     = "matchsvr"       // matchsvr durable consumer 名（queue group）
)

// MatchQueue 匹配请求队列抽象。
type MatchQueue interface {
	// Publish 把 msg 序列化后发布到 subject。
	Publish(ctx context.Context, subject string, msg proto.Message) error
	// Consume 注册 durable consumer：每条消息回调 handler；handler 返回 nil → ack，非 nil → 不 ack 留重投。
	Consume(ctx context.Context, durable string, handler func(ctx context.Context, data []byte) error) error
	// Close 释放底层连接。
	Close() error
}
```

- [ ] **Step 4: 写 `memory.go`（fake）**

```go
package matchqueue

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
)

// MemoryQueue 单测用内存实现：Publish 入内存并立即投递给已注册 handler（同进程）；
// Redeliver 手动重投，验消费侧幂等。非并发安全设计目标——仅供单测主循环驱动。
type MemoryQueue struct {
	mu        sync.Mutex
	published [][]byte
	handler   func(context.Context, []byte) error
}

func NewMemoryQueue() *MemoryQueue { return &MemoryQueue{} }

func (q *MemoryQueue) Publish(ctx context.Context, _ string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	q.mu.Lock()
	q.published = append(q.published, data)
	h := q.handler
	q.mu.Unlock()
	if h != nil {
		return h(ctx, data)
	}
	return nil
}

func (q *MemoryQueue) Consume(_ context.Context, _ string, handler func(context.Context, []byte) error) error {
	q.mu.Lock()
	q.handler = handler
	q.mu.Unlock()
	return nil
}

// Published 返回已发布消息字节副本（测试断言用）。
func (q *MemoryQueue) Published() [][]byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([][]byte, len(q.published))
	copy(out, q.published)
	return out
}

// Redeliver 把第 i 条已发布消息再投递一次给 handler（验重投幂等）。
func (q *MemoryQueue) Redeliver(ctx context.Context, i int) error {
	q.mu.Lock()
	if i < 0 || i >= len(q.published) {
		q.mu.Unlock()
		return fmt.Errorf("matchqueue: redeliver index %d out of range", i)
	}
	data := q.published[i]
	h := q.handler
	q.mu.Unlock()
	if h != nil {
		return h(ctx, data)
	}
	return nil
}

func (q *MemoryQueue) Close() error { return nil }

var _ MatchQueue = (*MemoryQueue)(nil)
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./src/common/matchqueue/ -run TestMemoryQueue -v`
Expected: PASS（两个用例）。

- [ ] **Step 6: Commit**

```bash
git add src/common/matchqueue/matchqueue.go src/common/matchqueue/memory.go src/common/matchqueue/memory_test.go
git commit -m "feat: matchqueue 设施——MatchQueue 接口 + 内存 fake（publish-consume-ack + 重投）"
```

---

## Task 3: MatchQueue JetStream 真实适配器（vet-only）

真实 `nats.go/jetstream` 适配器。沙箱无 Docker 不实跑，验收 = `go vet` 编译通过（同 P2/P3 集成测试欠账，见 [[gameserver-dev-workflow]]）。

**Files:**
- Create: `src/common/matchqueue/jetstream.go`

- [ ] **Step 1: 写 `jetstream.go`**

> 实现要点：`jetstream.New(nc)`；`CreateOrUpdateStream`（`WorkQueuePolicy`——匹配请求一次性消费即删）；`CreateOrUpdateConsumer`（`AckExplicitPolicy`）；`consumer.Consume(cb)` 推送回调，handler 返回 nil 才 `msg.Ack()`。所有类型名以 module cache `github.com/nats-io/nats.go@v1.52.0/jetstream/` 为准，最终以 `go vet` 编译通过为准。

```go
package matchqueue

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

// JetStreamQueue 真实 JetStream 适配器。沙箱不实跑，仅编译验证；实跑需 NATS+JetStream 环境。
type JetStreamQueue struct {
	nc *nats.Conn
	js jetstream.JetStream
}

// NewJetStreamQueue 连接 NATS 并建 JetStream 句柄。
func NewJetStreamQueue(urls []string) (*JetStreamQueue, error) {
	url := nats.DefaultURL
	if len(urls) > 0 {
		url = urls[0]
	}
	nc, err := nats.Connect(url, nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, fmt.Errorf("matchqueue connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("matchqueue jetstream: %w", err)
	}
	return &JetStreamQueue{nc: nc, js: js}, nil
}

// ensureStream 幂等建/确保 MATCH stream（WorkQueue：消费后删，匹配请求一次性）。
func (q *JetStreamQueue) ensureStream(ctx context.Context) error {
	_, err := q.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      StreamMatch,
		Subjects:  []string{SubjectMatchRequest},
		Retention: jetstream.WorkQueuePolicy,
	})
	return err
}

func (q *JetStreamQueue) Publish(ctx context.Context, subject string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	if err := q.ensureStream(ctx); err != nil {
		return err
	}
	_, err = q.js.Publish(ctx, subject, data)
	return err
}

func (q *JetStreamQueue) Consume(ctx context.Context, durable string, handler func(context.Context, []byte) error) error {
	if err := q.ensureStream(ctx); err != nil {
		return err
	}
	cons, err := q.js.CreateOrUpdateConsumer(ctx, StreamMatch, jetstream.ConsumerConfig{
		Durable:   durable,
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return err
	}
	_, err = cons.Consume(func(m jetstream.Msg) {
		if herr := handler(ctx, m.Data()); herr != nil {
			return // 不 ack，留重投
		}
		_ = m.Ack()
	})
	return err
}

func (q *JetStreamQueue) Close() error {
	if q.nc != nil {
		q.nc.Close()
	}
	return nil
}

var _ MatchQueue = (*JetStreamQueue)(nil)
```

- [ ] **Step 2: 编译验证**

Run: `go vet ./src/common/matchqueue/`
Expected: 无报错（若 jetstream API 名有出入，按编译器提示对照 module cache 修正后再次 vet 通过）。

- [ ] **Step 3: Commit**

```bash
git add src/common/matchqueue/jetstream.go
git commit -m "feat: matchqueue JetStream 真实适配器（vet 编译验证，沙箱无 Docker 不实跑）"
```

---

## Task 4: online Directory 加 room 绑定字段与方法

**Files:**
- Modify: `src/servers/onlinesvr/internal/directory.go`
- Test: `src/servers/onlinesvr/internal/directory_test.go`

- [ ] **Step 1: 写失败测试（追加到 `directory_test.go`）**

```go
func TestDirectory_BindRoom(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	dir.Register(7, "1.1.1", "1.2.1", time.Now().UnixNano())

	if ok := dir.BindRoom(7, "1.7.1", "1.8.1-1"); !ok {
		t.Fatalf("BindRoom on online entry should succeed")
	}
	e, ok := dir.Query(7)
	if !ok || e.RoomNodeID != "1.7.1" || e.GameID != "1.8.1-1" {
		t.Fatalf("want room=1.7.1 game=1.8.1-1, got %+v", e)
	}

	if ok := dir.UnbindRoom(7); !ok {
		t.Fatalf("UnbindRoom should succeed")
	}
	e, _ = dir.Query(7)
	if e.RoomNodeID != "" || e.GameID != "" {
		t.Fatalf("want cleared room binding, got %+v", e)
	}
}

func TestDirectory_BindRoom_NotOnline(t *testing.T) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	if ok := dir.BindRoom(99, "1.7.1", "g"); ok {
		t.Fatalf("BindRoom on absent entry should return false")
	}
}
```

(若 `directory_test.go` 尚未 import `time`/`timewheel`，按既有测试文件惯例补 import。)

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestDirectory_BindRoom -v`
Expected: FAIL（`e.RoomNodeID` undefined / `dir.BindRoom` undefined）。

- [ ] **Step 3: 改 `directory.go`**

`Entry` 结构追加两字段（在 `LastActive` 后）：

```go
type Entry struct {
	Uid           int64
	GatewayNodeID string
	LobbyNodeID   string
	LoginTime     int64  // Unix 纳秒
	LastActive    int64  // Unix 纳秒
	RoomNodeID    string // 当前所属 room NodeID 串（未在局为空）
	GameID        string // 当前对局 gameId（未在局为空）
}
```

在文件末尾追加两方法：

```go
// BindRoom 在在线条目上绝对覆盖写 room 绑定字段；条目不在线返回 false。幂等（重复同值安全）。
func (d *Directory) BindRoom(uid int64, roomNodeID, gameID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.RoomNodeID = roomNodeID
	e.GameID = gameID
	return true
}

// UnbindRoom 清除 room 绑定字段；条目不在线返回 false（幂等）。P4a 仅建好，wiring 留 P4b 结算清亲和。
func (d *Directory) UnbindRoom(uid int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.RoomNodeID = ""
	e.GameID = ""
	return true
}
```

> 注：`Register` 覆盖建新条目时 room 字段自然清空（新建 `&Entry{...}` 不带 room）—— 重登录即解绑，符合「online 为权威重连源 + TTL 自愈」（Spec §6.2/P4a-5）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestDirectory_BindRoom -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/onlinesvr/internal/directory.go src/servers/onlinesvr/internal/directory_test.go
git commit -m "feat: online Directory 加 room 亲和字段 + BindRoom/UnbindRoom（绝对覆盖写）"
```

---

## Task 5: online Handler 的 bindroom/unbindroom + Query 带 room

**Files:**
- Modify: `src/servers/onlinesvr/internal/online_handler.go`
- Test: `src/servers/onlinesvr/internal/online_handler_test.go`

- [ ] **Step 1: 写失败测试（追加到 `online_handler_test.go`）**

> 复用既有 `newTestHandler()`（返回 `*OnlineHandler, *fakeKicker`，见文件顶部）。

```go
func TestOnlineHandler_BindRoom(t *testing.T) {
	h, _ := newTestHandler()
	h.Register(context.Background(), &onlinepb.RPC_Register_Req{Uid: 7, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})

	rsp, err := h.Bindroom(context.Background(), &onlinepb.RPC_BindRoom_Req{Uid: 7, RoomNodeId: "1.7.1", GameId: "1.8.1-1"})
	if err != nil || rsp.Code != 0 {
		t.Fatalf("bindroom want code 0, got code=%d err=%v", rsp.GetCode(), err)
	}
	q, _ := h.Query(context.Background(), &onlinepb.RPC_Query_Req{Uid: 7})
	if q.Entry.RoomNodeId != "1.7.1" || q.Entry.GameId != "1.8.1-1" {
		t.Fatalf("query should see room binding, got %+v", q.Entry)
	}

	un, err := h.Unbindroom(context.Background(), &onlinepb.RPC_UnbindRoom_Req{Uid: 7})
	if err != nil || un.Code != 0 {
		t.Fatalf("unbindroom want code 0, got code=%d err=%v", un.GetCode(), err)
	}
	q, _ = h.Query(context.Background(), &onlinepb.RPC_Query_Req{Uid: 7})
	if q.Entry.RoomNodeId != "" || q.Entry.GameId != "" {
		t.Fatalf("query should see cleared binding, got %+v", q.Entry)
	}
}

func TestOnlineHandler_BindRoom_NotOnline(t *testing.T) {
	h, _ := newTestHandler()
	rsp, _ := h.Bindroom(context.Background(), &onlinepb.RPC_BindRoom_Req{Uid: 99, RoomNodeId: "1.7.1", GameId: "g"})
	if rsp.Code == 0 {
		t.Fatalf("bindroom on offline uid should return non-zero code, got 0")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestOnlineHandler_BindRoom -v`
Expected: FAIL（`h.Bindroom` undefined / `q.Entry.RoomNodeId` undefined）。

- [ ] **Step 3: 改 `online_handler.go`**

`Query` 的 `OnlineEntry` 映射补 room 两字段：

```go
	return &onlinepb.RPC_Query_Rsp{Online: true, Entry: &onlinepb.OnlineEntry{
		Uid: e.Uid, GatewayNodeId: e.GatewayNodeID, LobbyNodeId: e.LobbyNodeID,
		LoginTime: e.LoginTime, LastActive: e.LastActive,
		RoomNodeId: e.RoomNodeID, GameId: e.GameID,
	}}, nil
```

文件末尾追加两 handler（route 由方法名小写得 `OnlineHandler.bindroom`/`OnlineHandler.unbindroom`）：

```go
// Bindroom 绑定 room 亲和（绝对覆盖写）；条目不在线返回 code≠0。
func (h *OnlineHandler) Bindroom(_ context.Context, req *onlinepb.RPC_BindRoom_Req) (*onlinepb.RPC_BindRoom_Rsp, error) {
	if ok := h.dir.BindRoom(req.Uid, req.RoomNodeId, req.GameId); !ok {
		logger.Warn("online: bindroom on offline uid", logger.Int64("uid", req.Uid))
		return &onlinepb.RPC_BindRoom_Rsp{Code: 1}, nil
	}
	return &onlinepb.RPC_BindRoom_Rsp{Code: 0}, nil
}

// Unbindroom 清除 room 亲和（幂等）。P4a 仅建 handler，wiring 留 P4b 结算清亲和。
func (h *OnlineHandler) Unbindroom(_ context.Context, req *onlinepb.RPC_UnbindRoom_Req) (*onlinepb.RPC_UnbindRoom_Rsp, error) {
	h.dir.UnbindRoom(req.Uid)
	return &onlinepb.RPC_UnbindRoom_Rsp{Code: 0}, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestOnlineHandler -v`
Expected: PASS（含既有 Register/Query/Touch 用例不回归）。

- [ ] **Step 5: 注册 handler（无需改动）**

`onlinesvr/main.go` 已 `app.RegisterHandler(NewOnlineHandler(...))`——反射注册自动纳入新方法 `Bindroom`/`Unbindroom`，**不需改 main.go**。确认 `go build ./...` 通过。

- [ ] **Step 6: Commit**

```bash
git add src/servers/onlinesvr/internal/online_handler.go src/servers/onlinesvr/internal/online_handler_test.go
git commit -m "feat: OnlineHandler bindroom/unbindroom + Query 带 room 绑定"
```

---

## Task 6: router publishmatch + JetStream publisher

router 持 `MatchQueue` publisher，新增 `RouterHandler.publishmatch` 把匹配请求写入 `MATCH` stream。

**Files:**
- Modify: `src/servers/routersvr/internal/router_module.go`、`router_handler.go`、`src/servers/routersvr/main.go`
- Test: `src/servers/routersvr/internal/router_handler_test.go`

- [ ] **Step 1: 写失败测试（追加到 `router_handler_test.go`）**

```go
import (
	// ...既有 import...
	matchpb "project/protocal/gen/match"
	"project/src/common/matchqueue"
)

func TestRouterHandler_PublishMatch(t *testing.T) {
	mq := matchqueue.NewMemoryQueue()
	m := NewRouterModule(&fakeDisc{}, nil, mq)
	h := NewRouterHandler(m)

	rsp, err := h.Publishmatch(context.Background(), &matchpb.MatchRequest{Uid: 7, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
	if err != nil || rsp.Code != 0 {
		t.Fatalf("publishmatch want code 0, got code=%d err=%v", rsp.GetCode(), err)
	}
	pub := mq.Published()
	if len(pub) != 1 {
		t.Fatalf("want 1 published msg, got %d", len(pub))
	}
	var got matchpb.MatchRequest
	if err := proto.Unmarshal(pub[0], &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Uid != 7 || got.ReqId != "r1" || got.LobbyNodeId != "1.2.1" {
		t.Fatalf("published payload mismatch: %+v", &got)
	}
}
```

(若文件未 import `proto`，补 `"google.golang.org/protobuf/proto"`。)

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/routersvr/internal/ -run TestRouterHandler_PublishMatch -v`
Expected: FAIL（`NewRouterModule` 参数不符 / `h.Publishmatch` undefined）。

- [ ] **Step 3: 改 `router_module.go`** —— 加 `mq` 字段 + 构造参数 + `PublishMatch` 方法

```go
import (
	"context"
	"fmt"
	// ...既有 import...
	"project/src/common/matchqueue"
)

type RouterModule struct {
	module.BaseModule
	disc Discoverer
	cls  cluster.Cluster
	mq   matchqueue.MatchQueue // 匹配请求 publisher（可为 nil：不接 JetStream 的实例/单测）
}

func NewRouterModule(disc Discoverer, cls cluster.Cluster, mq matchqueue.MatchQueue) *RouterModule {
	return &RouterModule{disc: disc, cls: cls, mq: mq}
}

// PublishMatch 把匹配请求发布到 MATCH stream。
func (m *RouterModule) PublishMatch(ctx context.Context, req *matchpb.MatchRequest) error {
	if m.mq == nil {
		return fmt.Errorf("router: match queue not configured")
	}
	return m.mq.Publish(ctx, matchqueue.SubjectMatchRequest, req)
}
```

(补 import `matchpb "project/protocal/gen/match"`。)

- [ ] **Step 4: 改 `router_handler.go`** —— 加 `Publishmatch`（route=`RouterHandler.publishmatch`）

```go
import (
	// ...既有 import...
	matchpb "project/protocal/gen/match"
	"project/src/common/logger"
)

// Publishmatch 把匹配请求写入 JetStream MATCH stream（route="RouterHandler.publishmatch"）。
func (h *RouterHandler) Publishmatch(ctx context.Context, req *matchpb.MatchRequest) (*matchpb.RPC_PublishMatch_Rsp, error) {
	if err := h.module.PublishMatch(ctx, req); err != nil {
		logger.Warn("router publishmatch failed", logger.Int64("uid", req.Uid), logger.Err(err))
		return &matchpb.RPC_PublishMatch_Rsp{Code: 1}, nil
	}
	return &matchpb.RPC_PublishMatch_Rsp{Code: 0}, nil
}
```

- [ ] **Step 5: 更新既有 `NewRouterModule` 调用点（API 签名变更）**

Run: `grep -rn "NewRouterModule(" src/`
逐处补第三参 `nil`（单测/集成）或真实 mq（main.go，下一步）。已知调用点：
- `router_handler_test.go` 中既有 `NewRouterModule(disc, nil)` → `NewRouterModule(disc, nil, nil)`。
- `router_handler_test.go` 中 `NewRouterModule(&fakeDisc{}, nil)`（DIRECT 测试）→ `NewRouterModule(&fakeDisc{}, nil, nil)`。
- `router_integration_test.go`（若有调用）→ 补 `nil`。

- [ ] **Step 6: 改 `main.go`** —— 注入真实 JetStream publisher（vet-only）

在 `cls.Init()` 成功后、`app.Run()` 前构造 mq 并注入。**注意**：mq 须在 `NewRouterModule` 前构造（module 构造即持有），故调整顺序——

```go
	// JetStream publisher（真实适配器；沙箱不实跑，vet 编译验证）
	mq, err := matchqueue.NewJetStreamQueue(cfg.Cluster.Nats.URLs)
	if err != nil {
		panic(err)
	}
	defer mq.Close()

	mod := internal.NewRouterModule(cls.Discovery(), app.Cluster(), mq)
	app.Register(mod)
	if err := app.RegisterHandler(internal.NewRouterHandler(mod), nil); err != nil {
		panic(err)
	}
```

(补 import `"project/src/common/matchqueue"`。其余 main.go 不变。)

- [ ] **Step 7: 运行测试 + 构建 + vet**

Run:
```bash
go test ./src/servers/routersvr/internal/ -run TestRouter -v
go build ./...
go vet ./src/servers/routersvr/...
```
Expected: 测试 PASS（含既有 Resolve/Forward 不回归）；build/vet 通过。

- [ ] **Step 8: Commit**

```bash
git add src/servers/routersvr/
git commit -m "feat: router publishmatch + JetStream publisher 注入（MATCH stream 发布点）"
```

---

## Task 7: lobby Rating 组件

最小持久 Component，仿 `component_currency.go`；默认 mmr=1000，本阶段只读（无 mutator，赛后改分留 P4+）。

**Files:**
- Create: `src/servers/lobbysvr/internal/component_rating.go`、`component_rating_test.go`
- Modify: `src/servers/lobbysvr/internal/store.go`

- [ ] **Step 1: 写失败测试 `component_rating_test.go`**

```go
package internal

import "testing"

func TestRating_SeedDefault(t *testing.T) {
	r := NewRating()
	if r.MMR() != defaultMMR {
		t.Fatalf("new rating want mmr=%d, got %d", defaultMMR, r.MMR())
	}
}

func TestRating_LoadLegacyZeroSeedsDefault(t *testing.T) {
	r := NewRating()
	r.Load(&RatingState{MMR: 0}) // 旧档缺 rating 字段 → 解码为零值
	if r.MMR() != defaultMMR {
		t.Fatalf("legacy zero mmr should seed default %d, got %d", defaultMMR, r.MMR())
	}
}

func TestRating_LoadExisting(t *testing.T) {
	r := NewRating()
	r.Load(&RatingState{MMR: 1500})
	if r.MMR() != 1500 {
		t.Fatalf("want 1500, got %d", r.MMR())
	}
	if r.Dirty() {
		t.Fatalf("load should clear dirty")
	}
	if s, ok := r.Snapshot().(RatingState); !ok || s.MMR != 1500 {
		t.Fatalf("snapshot mismatch: %#v", r.Snapshot())
	}
}

func TestRating_ComponentContract(t *testing.T) {
	r := NewRating()
	if r.Name() != RatingComponentName || r.Field() != RatingField {
		t.Fatalf("name/field mismatch: %s/%s", r.Name(), r.Field())
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRating -v`
Expected: FAIL（undefined `NewRating` 等）。

- [ ] **Step 3: 写 `component_rating.go`**

```go
// src/servers/lobbysvr/internal/component_rating.go
package internal

const (
	RatingComponentName = "rating"
	RatingField         = "rating"
	defaultMMR          = 1000
)

// RatingState 评分存储态（内嵌 players 文档 rating 子文档）
type RatingState struct {
	MMR int64 `bson:"mmr"`
}

func NewRatingState() RatingState { return RatingState{MMR: defaultMMR} }

// Rating 最小评分组件；本阶段只读（无 mutator，赛后改分留扩展点）。仅主循环用，零锁。
type Rating struct {
	mmr   int64
	dirty bool
}

func NewRating() *Rating { return &Rating{mmr: defaultMMR} }

func (r *Rating) Name() string  { return RatingComponentName }
func (r *Rating) Field() string { return RatingField }
func (r *Rating) Dirty() bool   { return r.dirty }
func (r *Rating) ClearDirty()   { r.dirty = false }
func (r *Rating) MarkDirty()    { r.dirty = true }

// Load 从存储态恢复；mmr 为 0（旧档缺字段）时回填默认 1000。
func (r *Rating) Load(s *RatingState) {
	r.mmr = s.MMR
	if r.mmr == 0 {
		r.mmr = defaultMMR
	}
	r.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (r *Rating) Snapshot() any { return RatingState{MMR: r.mmr} }

// MMR 返回当前评分（发起匹配时读取填入请求）
func (r *Rating) MMR() int64 { return r.mmr }

// 编译期断言 Rating 满足 Component
var _ Component = (*Rating)(nil)
```

- [ ] **Step 4: 改 `store.go`** —— PlayerDoc 加字段、NewPlayerDoc 种子、buildPlayer 注册、accessor

`PlayerDoc` 追加：

```go
type PlayerDoc struct {
	ID       int64         `bson:"_id"`
	Bag      BagState      `bson:"bag"`
	Currency CurrencyState `bson:"currency"`
	Friend   FriendState   `bson:"friend"`
	Rating   RatingState   `bson:"rating"`
}
```

`NewPlayerDoc`：

```go
func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{ID: uid, Bag: NewBagState(), Currency: NewCurrencyState(), Friend: NewFriendState(), Rating: NewRatingState()}
}
```

`buildPlayer` 末尾（`return p` 前）追加：

```go
	rating := NewRating()
	rating.Load(&doc.Rating)
	p.AddComponent(rating)
```

文件末尾追加 accessor：

```go
// Rating 返回玩家评分组件（不存在或类型不符返回 nil）
func (p *Player) Rating() *Rating {
	r, _ := p.Component(RatingComponentName).(*Rating)
	return r
}
```

- [ ] **Step 5: 写 buildPlayer 注册的回归测试（追加到 `component_rating_test.go`）**

```go
func TestBuildPlayer_RegistersRating(t *testing.T) {
	p := buildPlayer(1, NewPlayerDoc(1))
	if p.Rating() == nil {
		t.Fatalf("buildPlayer should register rating component")
	}
	if p.Rating().MMR() != defaultMMR {
		t.Fatalf("new player mmr want %d, got %d", defaultMMR, p.Rating().MMR())
	}
}
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestRating|TestBuildPlayer_RegistersRating' -v`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add src/servers/lobbysvr/internal/component_rating.go src/servers/lobbysvr/internal/component_rating_test.go src/servers/lobbysvr/internal/store.go
git commit -m "feat: lobby Rating 组件（默认 mmr=1000，只读）+ PlayerDoc/buildPlayer 注册"
```

---

## Task 8: lobby Player.roomAffinity 内存运行态字段

**Files:**
- Modify: `src/servers/lobbysvr/internal/player.go`
- Test: `src/servers/lobbysvr/internal/player_test.go`

- [ ] **Step 1: 写失败测试（追加到 `player_test.go`）**

```go
func TestPlayer_RoomAffinity(t *testing.T) {
	p := NewPlayer(1)
	if p.RoomAffinity() != nil {
		t.Fatalf("new player should have nil room affinity")
	}
	p.SetRoomAffinity("1.7.1", "1.8.1-1")
	rb := p.RoomAffinity()
	if rb == nil || rb.roomNodeID != "1.7.1" || rb.gameID != "1.8.1-1" {
		t.Fatalf("set room affinity mismatch: %+v", rb)
	}
	// 绝对写幂等：重复 set 覆盖
	p.SetRoomAffinity("1.7.2", "1.8.1-2")
	if p.RoomAffinity().roomNodeID != "1.7.2" {
		t.Fatalf("set should overwrite")
	}
	p.ClearRoomAffinity()
	if p.RoomAffinity() != nil {
		t.Fatalf("clear should reset to nil")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPlayer_RoomAffinity -v`
Expected: FAIL（undefined `p.RoomAffinity`）。

- [ ] **Step 3: 改 `player.go`** —— Player 结构加字段 + 类型 + 方法

`Player` struct 追加字段（在 `lastTouch` 后）：`roomAffinity *roomBinding`。

文件末尾追加：

```go
// roomBinding 玩家 room 亲和（内存运行态，不持久到 PlayerDoc；权威重连源是 online）。
// 登录加载置 nil；GameStarted 回告置值；P4b 结算清空。
type roomBinding struct {
	roomNodeID string
	gameID     string
}

// RoomAffinity 返回当前 room 亲和（未在局返回 nil）
func (p *Player) RoomAffinity() *roomBinding { return p.roomAffinity }

// SetRoomAffinity 置 room 亲和（绝对写，幂等）
func (p *Player) SetRoomAffinity(roomNodeID, gameID string) {
	p.roomAffinity = &roomBinding{roomNodeID: roomNodeID, gameID: gameID}
}

// ClearRoomAffinity 清空 room 亲和
func (p *Player) ClearRoomAffinity() { p.roomAffinity = nil }
```

> `NewPlayer` 不需改：`roomAffinity` 零值即 nil。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPlayer_RoomAffinity -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/player.go src/servers/lobbysvr/internal/player_test.go
git commit -m "feat: lobby Player.roomAffinity 内存运行态字段（不持久）"
```

---

## Task 9: lobby CS_StartMatch handler + 发起匹配发布

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`、`runtime.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`、`runtime_test.go`

- [ ] **Step 1: 写失败测试（追加到 `lobby_handler_test.go`）** —— 校验三分支 + 发布 hook 被调

```go
func TestLobbyHandler_StartMatch(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()

	// 注入可观察的 publishMatch hook —— 经 runOnLoop 在主循环 goroutine 写入，避免与
	// StartMatch 内的读发生数据竞争（newTestRuntime 已 Start()，禁止从测试 goroutine 裸赋值）。
	// published 在 off-loop hook goroutine 写、测试 goroutine 读 → 用 atomic.Pointer。
	var published atomic.Pointer[matchpb.MatchRequest]
	runOnLoop(t, rt, func() {
		rt.publishMatch = func(req *matchpb.MatchRequest) error { published.Store(req); return nil }
	})

	// 未加载玩家 → code<0
	if rsp := startMatchSync(t, rt, 10001); rsp.Code >= 0 {
		t.Fatalf("not-loaded should be negative code, got %d", rsp.Code)
	}

	// 加载玩家（默认 mmr=1000）
	runOnLoop(t, rt, func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	if rsp := startMatchSync(t, rt, 10001); rsp.Code != 0 {
		t.Fatalf("loaded+not-in-game should be code 0, got %d", rsp.Code)
	}
	// publish hook 在 off-loop goroutine 触发，轮询等待
	waitFor(t, func() bool { return published.Load() != nil })
	p := published.Load()
	if p.Uid != 10001 || p.Mmr != 1000 || p.LobbyNodeId == "" || p.ReqId == "" {
		t.Fatalf("published MatchRequest mismatch: %+v", p)
	}

	// 已在局中 → code 1
	runOnLoop(t, rt, func() { rt.players[10001].SetRoomAffinity("1.7.1", "g") })
	if rsp := startMatchSync(t, rt, 10001); rsp.Code != 1 {
		t.Fatalf("in-game should be code 1, got %d", rsp.Code)
	}
}
```

需新增测试辅助（追加到 `lobby_handler_test.go` 或 `testhelper_test.go`，仿既有 `driveReq`/`purchaseSync`）：

```go
func startMatchSync(t *testing.T, rt *Runtime, uid int64) *lobbypb.SC_StartMatch {
	t.Helper()
	r := &fakeReplier{ch: make(chan replyResult, 1)}
	h := NewLobbyHandler(rt)
	if _, err := h.Startmatch(ctxWith(uid, r), &lobbypb.CS_StartMatch{}); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := r.wait(t)
	if res.err != nil {
		t.Fatalf("startmatch err: %v", res.err)
	}
	var out lobbypb.SC_StartMatch
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}

// waitFor 轮询条件至多 2s（off-loop hook 触发用）
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout")
}
```

(确认 `lobby_handler_test.go` import `matchpb "project/protocal/gen/match"`、`time`、`sync/atomic`。`runOnLoop`/`newTestRuntime`/`ctxWith`/`fakeReplier`/`replyResult` 复用既有 testhelper —— **`fakeReplier` 的构造（构造器或字段名）以 `testhelper_test.go` 既有定义为准**，上方 `&fakeReplier{ch: ...}` 字面量为示意，按既有签名对齐。)

> **并发纪律（全计划的 hook 测试通用）**：① Runtime hook 字段（`publishMatch`/`bindRoom`/`openGame`/`notifyGameStarted`）由主循环 goroutine 读，故**必须经 `runOnLoop`/`matchRunOnLoop` 在主循环内赋值**，不可在 `Start()` 后从测试 goroutine 裸赋值（否则 `-race` 判 race）。② off-loop hook goroutine 写、测试 goroutine 读的捕获变量用 `sync/atomic`（指针用 `atomic.Pointer[T]`）或互斥。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_StartMatch -v`
Expected: FAIL（`rt.publishMatch` / `h.Startmatch` undefined）。

- [ ] **Step 3: 改 `runtime.go`** —— 加 hook 字段、reqSeq、StartMatch、默认实现

`Runtime` struct 追加字段：

```go
	reqSeq       int64                                  // 匹配请求序号（主循环内自增，拼 reqId）
	publishMatch func(req *matchpb.MatchRequest) error  // 发布匹配请求（默认经 router；测试可替换）
```

`NewRuntime` 末尾（`return rt` 前，`if cfg.Cluster != nil` 块内）追加：

```go
	if cfg.Cluster != nil {
		rt.presence = clusterPresence{cls: cfg.Cluster}
		rt.publishMatch = rt.publishMatchViaRouter
	}
```

(import 补 `matchpb "project/protocal/gen/match"`、`"google.golang.org/protobuf/proto"`。)

新增方法：

```go
// StartMatch 主循环内发起匹配：生成幂等 reqId，off-loop 经 router 发布到 JetStream（best-effort，不阻塞主循环）。
func (rt *Runtime) StartMatch(uid, mmr int64) {
	if rt.publishMatch == nil {
		return
	}
	rt.reqSeq++
	reqID := fmt.Sprintf("%s-%d-%d", rt.nodeID, uid, rt.reqSeq)
	req := &matchpb.MatchRequest{Uid: uid, ReqId: reqID, Mmr: mmr, LobbyNodeId: rt.nodeID}
	pub := rt.publishMatch
	go func() {
		if err := pub(req); err != nil {
			logger.Warn("lobby start match: publish failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// publishMatchViaRouter 经 router publishmatch 发布匹配请求（CallAnySync 到任一 routersvr）。
func (rt *Runtime) publishMatchViaRouter(req *matchpb.MatchRequest) error {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	data, err := rt.cls.CallAnySync(ctx, "routersvr", "RouterHandler.publishmatch", req)
	if err != nil {
		return err
	}
	var rsp matchpb.RPC_PublishMatch_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("router publishmatch code=%d", rsp.Code)
	}
	return nil
}
```

(import 补 `"fmt"`。)

- [ ] **Step 4: 改 `lobby_handler.go`** —— 加 `Startmatch`（route=`LobbyHandler.startmatch`）

```go
// Startmatch route="LobbyHandler.startmatch"：校验可匹配 + 读 mmr + off-loop 发布匹配请求
func (h *LobbyHandler) Startmatch(ctx context.Context, _ *lobbypb.CS_StartMatch) (*lobbypb.SC_StartMatch, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &lobbypb.SC_StartMatch{Code: -1}, nil)
			return
		}
		if p.RoomAffinity() != nil {
			replyProto(replier, &lobbypb.SC_StartMatch{Code: 1}, nil) // 已在局中
			return
		}
		h.rt.StartMatch(uid, p.Rating().MMR())
		replyProto(replier, &lobbypb.SC_StartMatch{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_StartMatch -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler_test.go src/servers/lobbysvr/internal/testhelper_test.go
git commit -m "feat: lobby CS_StartMatch——校验可匹配+读 mmr+off-loop 经 router 发布匹配请求"
```

---

## Task 10: lobby GameStarted 回告 handler（置亲和 + BindRoom + SC_MatchFound）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`、`runtime.go`、`presence.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`、`presence_test.go`

- [ ] **Step 1: 写 presence 推送 helper 的失败测试（追加到 `presence_test.go`）**

> 复用既有 fake `presenceClient`（见 `presence_test.go` 顶部；若名为 `fakePresence` 则沿用）。

```go
func TestPushMatchFound(t *testing.T) {
	fp := newFakePresence() // 既有 fake：记录 Query 返回 + 捕获 Push 调用
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
```

> 若既有 `presence_test.go` 的 fake 未捕获 Push 调用，给它加**互斥保护的**捕获字段 + 访问器 `LastPushUID()`/`LastPushMsgID()`/`LastPushBody()`（Push 在 off-loop goroutine 调，`TestLobbyHandler_GameStarted` 从测试 goroutine 读，须 `-race` 安全）。这些是 fake 自身辅助，非生产代码。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPushMatchFound -v`
Expected: FAIL（undefined `pushMatchFound` / `msgIDSCMatchFound`）。

- [ ] **Step 3: 改 `presence.go`** —— 加 msgID 常量 + 推送 helper

常量段（`msgIDSCMailNew` 后）追加：

```go
	msgIDSCMatchFound uint32 = 2036
```

文件末尾追加（仿 `notifyNewMail`）：

```go
// pushMatchFound 若玩家在线，推 SC_MatchFound{room, game}（同步，off-loop 调用）。
func pushMatchFound(pc presenceClient, uid int64, roomNodeID, gameID string) {
	gw, online := pc.Query(uid)
	if !online {
		return
	}
	body, err := clientSerializer.Marshal(&lobbypb.SC_MatchFound{RoomNodeId: roomNodeID, GameId: gameID})
	if err != nil {
		logger.Warn("push match found: marshal failed", logger.Int64("uid", uid), logger.Err(err))
		return
	}
	pc.Push(gw, uid, msgIDSCMatchFound, body)
}
```

- [ ] **Step 4: 写 GameStarted handler 的失败测试（追加到 `lobby_handler_test.go`）**

```go
type bindCall struct {
	uid        int64
	room, game string
}

func TestLobbyHandler_GameStarted(t *testing.T) {
	rt := newTestRuntime(t)
	defer rt.Stop()

	// 注入可观察 bindRoom hook + fake presence —— 经 runOnLoop 在主循环内赋值（避免 hook 字段 race）。
	// bound 在 off-loop hook goroutine 写、测试读 → atomic.Pointer。
	var bound atomic.Pointer[bindCall]
	fp := newFakePresence() // 既有 fake；其 Push 捕获字段须 -race 安全（见下注）
	fp.online[10001] = "1.1.1"
	runOnLoop(t, rt, func() {
		rt.bindRoom = func(uid int64, room, game string) { bound.Store(&bindCall{uid, room, game}) }
		rt.presence = fp
	})

	// 未加载 → code<0
	if rsp := gameStartedSync(t, rt, &matchpb.RPC_GameStarted_Req{Uid: 10001, GameId: "1.8.1-1", RoomNodeId: "1.7.1"}); rsp.Code >= 0 {
		t.Fatalf("not-loaded want negative code, got %d", rsp.Code)
	}

	runOnLoop(t, rt, func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	if rsp := gameStartedSync(t, rt, &matchpb.RPC_GameStarted_Req{Uid: 10001, GameId: "1.8.1-1", RoomNodeId: "1.7.1"}); rsp.Code != 0 {
		t.Fatalf("want code 0, got %d", rsp.Code)
	}
	// 亲和已置（主循环内断言）
	runOnLoop(t, rt, func() {
		rb := rt.players[10001].RoomAffinity()
		if rb == nil || rb.roomNodeID != "1.7.1" || rb.gameID != "1.8.1-1" {
			t.Fatalf("room affinity not set: %+v", rb)
		}
	})
	// BindRoom hook 被调
	waitFor(t, func() bool { return bound.Load() != nil })
	if b := bound.Load(); b.uid != 10001 || b.room != "1.7.1" || b.game != "1.8.1-1" {
		t.Fatalf("bindRoom args mismatch: %+v", b)
	}
	// SC_MatchFound 已推（fp 的捕获访问器须 -race 安全）
	waitFor(t, func() bool { return fp.LastPushUID() == 10001 })
	if fp.LastPushMsgID() != msgIDSCMatchFound {
		t.Fatalf("want SC_MatchFound push, got msgID %d", fp.LastPushMsgID())
	}
}

func gameStartedSync(t *testing.T, rt *Runtime, req *matchpb.RPC_GameStarted_Req) *matchpb.RPC_GameStarted_Rsp {
	t.Helper()
	r := &fakeReplier{ch: make(chan replyResult, 1)}
	h := NewLobbyHandler(rt)
	if _, err := h.Gamestarted(ctxWith(req.Uid, r), req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	res := r.wait(t)
	if res.err != nil {
		t.Fatalf("gamestarted err: %v", res.err)
	}
	var out matchpb.RPC_GameStarted_Rsp
	if err := proto.Unmarshal(res.data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}
```

(同 Task 9 的 `-race` 提示：`boundUID` 等用 atomic/mutex 处理。)

- [ ] **Step 5: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler_GameStarted -v`
Expected: FAIL（`rt.bindRoom` / `h.Gamestarted` undefined）。

- [ ] **Step 6: 改 `runtime.go`** —— 加 bindRoom hook + 默认实现 + BindRoom/PushMatchFound

`Runtime` struct 追加字段：

```go
	bindRoom func(uid int64, roomNodeID, gameID string) // 同步 online room 绑定（默认经 router；测试可替换）
```

`NewRuntime` 的 `if cfg.Cluster != nil` 块内追加：`rt.bindRoom = rt.bindRoomViaRouter`。

新增方法：

```go
// BindRoom 经注入的 hook 同步 online room 绑定（GameStarted 内调用）。
func (rt *Runtime) BindRoom(uid int64, roomNodeID, gameID string) {
	if rt.bindRoom != nil {
		rt.bindRoom(uid, roomNodeID, gameID)
	}
}

// bindRoomViaRouter 经 router CONSISTENT_HASH(uid) 调 OnlineHandler.bindroom 绝对写 room 绑定（best-effort，off-loop）。
func (rt *Runtime) bindRoomViaRouter(uid int64, roomNodeID, gameID string) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_BindRoom_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.bindroom",
			&onlinepb.RPC_BindRoom_Req{Uid: uid, RoomNodeId: roomNodeID, GameId: gameID},
		); err != nil {
			logger.Warn("lobby gamestarted: bind room failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// PushMatchFound 若玩家在线，off-loop 推 SC_MatchFound（best-effort，不阻塞主循环）。
func (rt *Runtime) PushMatchFound(uid int64, roomNodeID, gameID string) {
	if rt.presence == nil {
		return
	}
	pc := rt.presence
	go pushMatchFound(pc, uid, roomNodeID, gameID)
}
```

(`onlinepb`/`routerpb`/`strconv`/`routerclient` 已在 runtime.go import。)

- [ ] **Step 7: 改 `lobby_handler.go`** —— 加 `Gamestarted`（route=`LobbyHandler.gamestarted`）

```go
// Gamestarted route="LobbyHandler.gamestarted"：match→lobby 开局回告（经 router DIRECT 到本节点，同步 RPC 返回 ack）
func (h *LobbyHandler) Gamestarted(ctx context.Context, req *matchpb.RPC_GameStarted_Req) (*matchpb.RPC_GameStarted_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid, gameID, roomNodeID := req.Uid, req.GameId, req.RoomNodeId
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: -1}, nil)
			return
		}
		p.SetRoomAffinity(roomNodeID, gameID)        // 内存亲和（绝对写，幂等）
		h.rt.BindRoom(uid, roomNodeID, gameID)       // off-loop 同步 online
		h.rt.PushMatchFound(uid, roomNodeID, gameID) // off-loop 推 SC_MatchFound
		replyProto(replier, &matchpb.RPC_GameStarted_Rsp{Code: 0}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

(`matchpb` 已在 Task 9 import。)

- [ ] **Step 8: 运行确认通过 + 全 lobby 包回归**

Run:
```bash
go test ./src/servers/lobbysvr/internal/ -run 'TestPushMatchFound|TestLobbyHandler_GameStarted' -v
go test ./src/servers/lobbysvr/...
```
Expected: PASS（含既有 login/purchase/friend/mail 等不回归）。

- [ ] **Step 9: Commit**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/presence.go src/servers/lobbysvr/internal/lobby_handler_test.go src/servers/lobbysvr/internal/presence_test.go
git commit -m "feat: lobby GameStarted 回告——置内存亲和+经 router BindRoom 同步 online+推 SC_MatchFound"
```

---

## Task 11: roomsvr Game 对象 + opengame 业务逻辑

**Files:**
- Create: `src/servers/roomsvr/internal/game.go`、`src/servers/roomsvr/internal/runtime.go`、`src/servers/roomsvr/internal/game_test.go`、`src/servers/roomsvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试 `game_test.go`**

```go
package internal

import "testing"

func TestNewGame(t *testing.T) {
	g := NewGame("1.8.1-1", 5, 30, []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}})
	if g.GameID != "1.8.1-1" || g.ItemID != 5 || g.CountdownSec != 30 {
		t.Fatalf("game fields mismatch: %+v", g)
	}
	if len(g.Participants) != 2 || g.Participants[0].UID != 1 {
		t.Fatalf("participants mismatch: %+v", g.Participants)
	}
}
```

- [ ] **Step 2: 写失败测试 `runtime_test.go`（OpenGame 幂等 + 多局隔离）**

```go
package internal

import (
	"testing"
	"time"
)

func newTestRoomRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	return rt
}

func roomRunOnLoop(t *testing.T, rt *Runtime, fn func()) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("roomRunOnLoop timeout")
	}
}

func TestRuntime_OpenGameIdempotent(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	parts := []Participant{{UID: 1, LobbyNodeID: "1.2.1"}, {UID: 2, LobbyNodeID: "1.2.1"}}
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("1.8.1-1", 5, 30, parts)
		rt.OpenGame("1.8.1-1", 9, 99, parts) // 同 gameId 幂等：不覆盖
	})
	roomRunOnLoop(t, rt, func() {
		g := rt.Game("1.8.1-1")
		if g == nil || g.ItemID != 5 || g.CountdownSec != 30 {
			t.Fatalf("idempotent open should keep first game, got %+v", g)
		}
	})
}

func TestRuntime_MultiGameIsolation(t *testing.T) {
	rt := newTestRoomRuntime(t)
	defer rt.Stop()
	roomRunOnLoop(t, rt, func() {
		rt.OpenGame("g1", 1, 30, []Participant{{UID: 1}})
		rt.OpenGame("g2", 2, 30, []Participant{{UID: 2}})
	})
	roomRunOnLoop(t, rt, func() {
		if rt.Game("g1").ItemID != 1 || rt.Game("g2").ItemID != 2 {
			t.Fatalf("games not isolated")
		}
	})
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./src/servers/roomsvr/internal/ -v`
Expected: FAIL（包无 Go 文件 / undefined）。

- [ ] **Step 4: 写 `game.go`**

```go
package internal

// Participant 局内参与者（与 roompb.Participant 解耦的内部态）
type Participant struct {
	UID         int64
	LobbyNodeID string
}

// Game 拍卖局对象。P4a 仅骨架：participants / 拍品占位 / 倒计时；
// 出价/最高价/结算（HighestBid/HighestBidder）留 P4b。多 gameId 并存隔离。
type Game struct {
	GameID       string
	Participants []Participant
	ItemID       int32
	CountdownSec int32
}

// NewGame 建局
func NewGame(gameID string, itemID, countdownSec int32, parts []Participant) *Game {
	return &Game{GameID: gameID, ItemID: itemID, CountdownSec: countdownSec, Participants: parts}
}
```

- [ ] **Step 5: 写 `runtime.go`（仿 lobby Runtime 单主循环，无 mongo/online）**

```go
package internal

import (
	"sync"
	"time"

	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
)

// RuntimeConfig roomsvr 主循环配置
type RuntimeConfig struct {
	NodeID    string
	QueueSize int
	Tick      time.Duration
}

// Runtime roomsvr 帧驱动单主循环：串行承载多局拍卖（零锁）。P4a 仅开局 + tick 骨架（不结算）。
type Runtime struct {
	nodeID string
	tq     *taskqueue.Queue
	tw     *timewheel.TimeWheel
	games  map[string]*Game

	tick     time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	return &Runtime{
		nodeID: cfg.NodeID,
		tq:     taskqueue.New(cfg.QueueSize),
		tw:     timewheel.New(cfg.Tick, 512),
		games:  make(map[string]*Game),
		tick:   cfg.Tick,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// NodeID 返回本 room 节点 ID（opengame 回带）。
func (rt *Runtime) NodeID() string { return rt.nodeID }

func (rt *Runtime) Start() { go rt.loop() }

func (rt *Runtime) Stop() {
	rt.stopOnce.Do(func() { close(rt.stopCh) })
	<-rt.doneCh
}

func (rt *Runtime) loop() {
	defer close(rt.doneCh)
	ticker := time.NewTicker(rt.tick)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopCh:
			return
		case fn := <-rt.tq.C():
			fn()
		case <-ticker.C:
			rt.tw.Advance() // tick 骨架推进各局倒计时；P4a 到点仅日志，不结算（P4b 填）
		}
	}
}

// OpenGame 主循环内建局（按 gameId 幂等：已存在则不覆盖）。P4a 倒计时到点仅日志。
func (rt *Runtime) OpenGame(gameID string, itemID, countdownSec int32, parts []Participant) {
	if _, ok := rt.games[gameID]; ok {
		return // 幂等：同 gameId 重投不覆盖
	}
	rt.games[gameID] = NewGame(gameID, itemID, countdownSec, parts)
	rt.tw.AfterFunc(time.Duration(countdownSec)*time.Second, func() {
		logger.Info("room game countdown elapsed (P4a no settle)", logger.String("gameId", gameID))
	})
	logger.Info("room game opened", logger.String("gameId", gameID), logger.Int("participants", len(parts)))
}

// Game 主循环内取局（不存在返回 nil）
func (rt *Runtime) Game(gameID string) *Game { return rt.games[gameID] }
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./src/servers/roomsvr/internal/ -v`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add src/servers/roomsvr/internal/game.go src/servers/roomsvr/internal/runtime.go src/servers/roomsvr/internal/game_test.go src/servers/roomsvr/internal/runtime_test.go
git commit -m "feat: roomsvr Game 对象 + Runtime 单主循环（开局幂等 + 多局隔离 + tick 骨架）"
```

---

## Task 12: roomsvr RoomHandler + module + main + conf

**Files:**
- Create: `src/servers/roomsvr/internal/reply.go`、`room_handler.go`、`room_module.go`、`room_handler_test.go`、`src/servers/roomsvr/main.go`、`conf/room.yaml`

- [ ] **Step 1: 写失败测试 `room_handler_test.go`**

```go
package internal

import (
	"context"
	"testing"
	"time"

	roompb "project/protocal/gen/room"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/cluster"
	"google.golang.org/protobuf/proto"
)

// capReplier 捕获延迟回包
type capReplier struct{ ch chan struct {
	data []byte
	err  error
} }

func newCapReplier() *capReplier {
	return &capReplier{ch: make(chan struct {
		data []byte
		err  error
	}, 1)}
}
func (r *capReplier) Reply(data []byte, err error) { r.ch <- struct {
	data []byte
	err  error
}{data, err} }

func openGameSync(t *testing.T, rt *Runtime, req *roompb.RPC_OpenGame_Req) *roompb.RPC_OpenGame_Rsp {
	t.Helper()
	r := newCapReplier()
	ctx := cluster.WithReplier(cluster.WithSession(context.Background(), &clusterpb.ClusterSession{}), r)
	h := NewRoomHandler(rt)
	if _, err := h.Opengame(ctx, req); err != cluster.ErrDeferredReply {
		t.Fatalf("want ErrDeferredReply, got %v", err)
	}
	select {
	case res := <-r.ch:
		if res.err != nil {
			t.Fatalf("opengame err: %v", res.err)
		}
		var out roompb.RPC_OpenGame_Rsp
		if err := proto.Unmarshal(res.data, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return &out
	case <-time.After(2 * time.Second):
		t.Fatalf("opengame timeout")
		return nil
	}
}

func TestRoomHandler_OpenGame(t *testing.T) {
	rt := NewRuntime(RuntimeConfig{NodeID: "1.7.1", Tick: time.Millisecond})
	rt.Start()
	defer rt.Stop()

	rsp := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{
		GameId: "1.8.1-1", ItemId: 5, CountdownSec: 30,
		Participants: []*roompb.Participant{{Uid: 1, LobbyNodeId: "1.2.1"}, {Uid: 2, LobbyNodeId: "1.2.1"}},
	})
	if rsp.Code != 0 || rsp.RoomNodeId != "1.7.1" {
		t.Fatalf("want code 0 room=1.7.1, got code=%d room=%s", rsp.Code, rsp.RoomNodeId)
	}

	// 同 gameId 幂等：再开返回同 room_node_id、code 0
	rsp2 := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{GameId: "1.8.1-1", Participants: []*roompb.Participant{{Uid: 1}}})
	if rsp2.Code != 0 || rsp2.RoomNodeId != "1.7.1" {
		t.Fatalf("idempotent open mismatch: code=%d room=%s", rsp2.Code, rsp2.RoomNodeId)
	}

	// 参数非法：空 gameId / 空 participants → code 1
	bad := openGameSync(t, rt, &roompb.RPC_OpenGame_Req{GameId: "", Participants: nil})
	if bad.Code == 0 {
		t.Fatalf("empty gameId/participants should be non-zero code, got 0")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/roomsvr/internal/ -run TestRoomHandler -v`
Expected: FAIL（undefined `NewRoomHandler`）。

- [ ] **Step 3: 写 `reply.go`（包内延迟回包 helper，仿 lobby `replyProto`）**

```go
package internal

import (
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
)

// replyProto marshal 业务响应并经 Replier 异步回包（err 非 nil 时回错误）。nil-replier 安全。
func replyProto(r cluster.Replier, msg proto.Message, err error) {
	if r == nil {
		return
	}
	if err != nil {
		r.Reply(nil, err)
		return
	}
	data, merr := proto.Marshal(msg)
	if merr != nil {
		r.Reply(nil, merr)
		return
	}
	r.Reply(data, nil)
}
```

- [ ] **Step 4: 写 `room_handler.go`**

```go
package internal

import (
	"context"

	roompb "project/protocal/gen/room"
	"project/src/framework/cluster"
)

// RoomHandler roomsvr 集群 RPC handler 薄壳：捕获 Replier + Submit 进主循环 + 延迟回包。
type RoomHandler struct {
	rt *Runtime
}

func NewRoomHandler(rt *Runtime) *RoomHandler { return &RoomHandler{rt: rt} }

// Opengame route="RoomHandler.opengame"：按 gameId 建局（幂等），Rsp 回带 room_node_id=self。
func (h *RoomHandler) Opengame(ctx context.Context, req *roompb.RPC_OpenGame_Req) (*roompb.RPC_OpenGame_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	if req.GameId == "" || len(req.Participants) == 0 {
		h.rt.Submit(func() { replyProto(replier, &roompb.RPC_OpenGame_Rsp{Code: 1}, nil) })
		return nil, cluster.ErrDeferredReply
	}
	parts := make([]Participant, 0, len(req.Participants))
	for _, p := range req.Participants {
		parts = append(parts, Participant{UID: p.Uid, LobbyNodeID: p.LobbyNodeId})
	}
	gameID, itemID, countdown := req.GameId, req.ItemId, req.CountdownSec
	h.rt.Submit(func() {
		h.rt.OpenGame(gameID, itemID, countdown, parts)
		replyProto(replier, &roompb.RPC_OpenGame_Rsp{Code: 0, RoomNodeId: h.rt.NodeID()}, nil)
	})
	return nil, cluster.ErrDeferredReply
}
```

- [ ] **Step 5: 写 `room_module.go`（仿 lobby_module.go）**

```go
package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// RoomModule roomsvr 模块：持主循环 Runtime，Init 启动、OnStop 停止。
type RoomModule struct {
	module.BaseModule
	rt *Runtime
}

func NewRoomModule(rt *Runtime) *RoomModule { return &RoomModule{rt: rt} }

func (m *RoomModule) Name() string { return "room" }

func (m *RoomModule) Init() {
	m.rt.Start()
	logger.Info("room module initialized")
}

func (m *RoomModule) OnStop() {
	m.rt.Stop()
	logger.Info("room module stopped")
}
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./src/servers/roomsvr/internal/ -v`
Expected: PASS。

- [ ] **Step 7: 写 `main.go`（仿 onlinesvr/main.go，backend 装配）**

```go
package main

import (
	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/roomsvr/internal"
)

func main() {
	cfg := config.MustLoad("conf/room.yaml")

	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	self, err := cluster.ParseNodeID(cfg.Node.ID)
	if err != nil {
		panic(err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints:  cfg.Cluster.Etcd.Endpoints,
		NatsURLs:       cfg.Cluster.Nats.URLs,
		SelfAddr:       cfg.Node.Addr,
		ServerTypeName: cfg.Node.ServerTypeName,
	})
	if err != nil {
		panic(err)
	}

	// 有状态后端：protobuf 序列化器，不调 Frontend()
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).
		Build()

	rt := internal.NewRuntime(internal.RuntimeConfig{NodeID: cfg.Node.ID})
	app.Register(internal.NewRoomModule(rt))
	if err := app.RegisterHandler(internal.NewRoomHandler(rt), nil); err != nil {
		panic(err)
	}

	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	logger.Info("roomsvr started", logger.String("nodeID", cfg.Node.ID))
	app.Run()
}
```

- [ ] **Step 8: 写 `conf/room.yaml`（仿 conf/online.yaml）**

```yaml
# roomsvr 节点（worldID=1, serverTypeID=7, index=1）
node:
  id: "1.7.1"
  server_type_name: "roomsvr"
  addr: "0.0.0.0:8871"
cluster:
  etcd:
    endpoints: ["localhost:2379"]
  nats:
    urls: ["nats://localhost:4222"]
log:
  level: "info"
  dir: "./logs"
```

- [ ] **Step 9: 构建 + vet**

Run:
```bash
go build ./...
go vet ./src/servers/roomsvr/...
```
Expected: 通过。

- [ ] **Step 10: Commit**

```bash
git add src/servers/roomsvr/ conf/room.yaml
git commit -m "feat: roomsvr RoomHandler.opengame + module + main + conf（开局回带 room_node_id，幂等）"
```

---

## Task 13: matchsvr MMR 队列（去重 + 凑桌）

**Files:**
- Create: `src/servers/matchsvr/internal/queue.go`、`src/servers/matchsvr/internal/queue_test.go`

- [ ] **Step 1: 写失败测试 `queue_test.go`**

```go
package internal

import "testing"

func TestMatchQueue_DedupEnqueue(t *testing.T) {
	q := newMatchQueue(2, 200)
	if !q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000}) {
		t.Fatalf("first enqueue should be new")
	}
	if q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000}) {
		t.Fatalf("duplicate (uid,reqId) should not re-enqueue")
	}
	if q.Len() != 1 {
		t.Fatalf("want 1 waiting, got %d", q.Len())
	}
}

func TestMatchQueue_FormTableWithinWindow(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	if _, ok := q.FormTable(); ok {
		t.Fatalf("one player should not form a table")
	}
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1100}) // diff 100 <= 200
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("two in-window players should form a table, got ok=%v n=%d", ok, len(table))
	}
	if q.Len() != 0 {
		t.Fatalf("formed players should be removed, remaining %d", q.Len())
	}
}

func TestMatchQueue_OutOfWindowNoTable(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1500}) // diff 500 > 200
	if _, ok := q.FormTable(); ok {
		t.Fatalf("out-of-window players should not form a table")
	}
	if q.Len() != 2 {
		t.Fatalf("both should remain queued, got %d", q.Len())
	}
}

func TestMatchQueue_FormTablePicksClosest(t *testing.T) {
	q := newMatchQueue(2, 200)
	q.Enqueue(waiting{uid: 1, reqID: "r1", mmr: 1000})
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1900}) // 远
	q.Enqueue(waiting{uid: 3, reqID: "r3", mmr: 1050}) // 与 1 近
	table, ok := q.FormTable()
	if !ok || len(table) != 2 {
		t.Fatalf("should form table from closest pair")
	}
	// 1 与 3 成桌，2 留队
	if q.Len() != 1 {
		t.Fatalf("one player should remain, got %d", q.Len())
	}
}

func TestMatchQueue_Requeue(t *testing.T) {
	q := newMatchQueue(2, 200)
	w := waiting{uid: 1, reqID: "r1", mmr: 1000}
	q.Enqueue(w)
	q.Enqueue(waiting{uid: 2, reqID: "r2", mmr: 1000})
	table, _ := q.FormTable()
	for _, x := range table {
		q.Requeue(x) // 开局失败放回
	}
	if q.Len() != 2 {
		t.Fatalf("requeued players should be back in queue, got %d", q.Len())
	}
	// 已在 seen，重投不重复入队
	if q.Enqueue(w) {
		t.Fatalf("requeued player still in seen set; redelivery should not re-enqueue")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/matchsvr/internal/ -run TestMatchQueue -v`
Expected: FAIL（包无 Go 文件 / undefined）。

- [ ] **Step 3: 写 `queue.go`**

```go
package internal

import (
	"fmt"
	"sort"
)

// waiting 等待匹配的玩家
type waiting struct {
	uid       int64
	reqID     string
	mmr       int64
	lobbyNode string
}

// matchQueue MMR 匹配队列（仅主循环用，零锁）：(uid,reqId) 去重 + 滑窗凑桌。
// 注：dedup set 为 matchsvr 会话内内存态（纯内存无持久），足够覆盖 JetStream 重投；
// 持久去重（跨重启）不在 P4a。seen 不随成桌清除——防同一请求成桌后被重投再处理。
type matchQueue struct {
	waitingList []waiting
	seen        map[string]bool // key=uid:reqId
	size        int             // 成桌人数 N
	window      int64           // MMR 窗口 W
}

func newMatchQueue(size int, window int64) *matchQueue {
	if size < 1 {
		size = 2
	}
	return &matchQueue{seen: make(map[string]bool), size: size, window: window}
}

func dedupKey(uid int64, reqID string) string { return fmt.Sprintf("%d:%s", uid, reqID) }

// Enqueue 去重入队；新入队返回 true，重复 (uid,reqId) 返回 false（仍应 ack）。
func (q *matchQueue) Enqueue(w waiting) bool {
	k := dedupKey(w.uid, w.reqID)
	if q.seen[k] {
		return false
	}
	q.seen[k] = true
	q.waitingList = append(q.waitingList, w)
	return true
}

// Requeue 把成桌后开局失败的玩家放回等待队列（不动 seen，已在其中）。
func (q *matchQueue) Requeue(w waiting) { q.waitingList = append(q.waitingList, w) }

// Len 当前等待人数
func (q *matchQueue) Len() int { return len(q.waitingList) }

// FormTable 尝试凑齐 size 个 MMR 窗口内（max-min<=window）的玩家：
// 按 mmr 排序滑窗，命中则从等待队列移除并返回该桌；不足/无窗口返回 (nil,false)。
func (q *matchQueue) FormTable() ([]waiting, bool) {
	if len(q.waitingList) < q.size {
		return nil, false
	}
	sorted := make([]waiting, len(q.waitingList))
	copy(sorted, q.waitingList)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].mmr < sorted[j].mmr })
	for i := 0; i+q.size <= len(sorted); i++ {
		if sorted[i+q.size-1].mmr-sorted[i].mmr <= q.window {
			table := make([]waiting, q.size)
			copy(table, sorted[i:i+q.size])
			q.removeAll(table)
			return table, true
		}
	}
	return nil, false
}

// removeAll 从等待队列移除 table 中的玩家（按 (uid,reqId) 匹配）。
func (q *matchQueue) removeAll(table []waiting) {
	drop := make(map[string]bool, len(table))
	for _, w := range table {
		drop[dedupKey(w.uid, w.reqID)] = true
	}
	kept := q.waitingList[:0]
	for _, w := range q.waitingList {
		if !drop[dedupKey(w.uid, w.reqID)] {
			kept = append(kept, w)
		}
	}
	q.waitingList = kept
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/servers/matchsvr/internal/ -run TestMatchQueue -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/matchsvr/internal/queue.go src/servers/matchsvr/internal/queue_test.go
git commit -m "feat: matchsvr MMR 队列——(uid,reqId) 去重 + 滑窗凑桌 + 失败放回"
```

---

## Task 14: matchsvr Runtime 主循环 + off-loop 编排

**Files:**
- Create: `src/servers/matchsvr/internal/runtime.go`、`src/servers/matchsvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试 `runtime_test.go`** —— OnRequest 去重 + 凑桌触发编排 + gameId 唯一 + 开局失败放回

```go
package internal

import (
	"sync"
	"testing"
	"time"

	matchpb "project/protocal/gen/match"
)

func newTestMatchRuntime(t *testing.T) *Runtime {
	t.Helper()
	rt := NewRuntime(RuntimeConfig{NodeID: "1.8.1", MatchSize: 2, MMRWindow: 200, Tick: time.Millisecond})
	rt.Start()
	return rt
}

func matchRunOnLoop(t *testing.T, rt *Runtime, fn func()) {
	t.Helper()
	done := make(chan struct{})
	rt.Submit(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("matchRunOnLoop timeout")
	}
}

// orchestrationSpy 捕获编排调用（mutex 保护；只读取计数与首个 gameId，故不存完整内容）。
type orchestrationSpy struct {
	mu        sync.Mutex
	openGames []string // 调用过的 gameId（按调用序）
	notified  int      // notifyGameStarted 调用次数
}

func (s *orchestrationSpy) recOpen(gameID string) {
	s.mu.Lock()
	s.openGames = append(s.openGames, gameID)
	s.mu.Unlock()
}
func (s *orchestrationSpy) recNotify() { s.mu.Lock(); s.notified++; s.mu.Unlock() }
func (s *orchestrationSpy) openCount() int {
	s.mu.Lock(); defer s.mu.Unlock(); return len(s.openGames)
}
func (s *orchestrationSpy) notifyCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.notified }
func (s *orchestrationSpy) openGameID(i int) string {
	s.mu.Lock(); defer s.mu.Unlock(); return s.openGames[i]
}

func TestRuntime_FormsTableAndOrchestrates(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	// hook 经 matchRunOnLoop 在主循环内赋值（避免 hook 字段 race）。
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "1.7.1", nil }
		rt.notifyGameStarted = func(string, int64, string, string) error { spy.recNotify(); return nil }
	})

	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"}) // 重复，去重
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "r2", Mmr: 1100, LobbyNodeId: "1.2.2"})
	})

	waitForSpy(t, func() bool { return spy.openCount() == 1 && spy.notifyCount() == 2 })
	if spy.openGameID(0) != "1.8.1-1" {
		t.Fatalf("gameId want 1.8.1-1, got %s", spy.openGameID(0))
	}
}

func TestRuntime_GameIDUnique(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "1.7.1", nil }
		rt.notifyGameStarted = func(string, int64, string, string) error { return nil }
	})
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "b", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 3, ReqId: "c", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 4, ReqId: "d", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	waitForSpy(t, func() bool { return spy.openCount() == 2 })
	if spy.openGameID(0) == spy.openGameID(1) {
		t.Fatalf("gameIds must be unique, both %s", spy.openGameID(0))
	}
}

func TestRuntime_OpenGameFailRequeues(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	spy := &orchestrationSpy{}
	matchRunOnLoop(t, rt, func() {
		rt.openGame = func(gameID string, _ []waiting) (string, error) { spy.recOpen(gameID); return "", errTest }
		rt.notifyGameStarted = func(string, int64, string, string) error { return nil }
	})
	matchRunOnLoop(t, rt, func() {
		rt.OnRequest(&matchpb.MatchRequest{Uid: 1, ReqId: "a", Mmr: 1000, LobbyNodeId: "1.2.1"})
		rt.OnRequest(&matchpb.MatchRequest{Uid: 2, ReqId: "b", Mmr: 1000, LobbyNodeId: "1.2.1"})
	})
	waitForSpy(t, func() bool { return spy.openCount() == 1 })
	// 开局失败后两人放回队列。Requeue 在 off-loop goroutine 于 openGame 返回 error 后才 Submit 回主循环，
	// 相对 openCount==1 是异步落定的 —— 必须轮询 queueLen，不能一次性断言（否则 flaky）。
	waitForSpy(t, func() bool {
		got := 0
		matchRunOnLoop(t, rt, func() { got = rt.queueLen() })
		return got == 2
	})
}
```

辅助（同文件，import 补 `errors`）：

```go
var errTest = errors.New("test open fail")

func waitForSpy(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waitForSpy timeout")
}
```

> 测试需读队列长度——为此在 `runtime.go` 提供 `queueLen()`（仅供主循环/测试，见实现）。`runtime_test.go` import：`sync`、`testing`、`time`、`errors`、`matchpb`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/matchsvr/internal/ -run TestRuntime -v`
Expected: FAIL（undefined `NewRuntime`/`rt.openGame` 等）。

- [ ] **Step 3: 写 `runtime.go`**

```go
package internal

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	matchpb "project/protocal/gen/match"
	roompb "project/protocal/gen/room"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

const (
	defaultMatchSize     = 2
	defaultMMRWindow     = 200
	defaultItemID        = 1
	defaultCountdownSec  = 30
)

// RuntimeConfig matchsvr 主循环配置
type RuntimeConfig struct {
	NodeID    string
	Cluster   cluster.Cluster
	MatchSize int
	MMRWindow int64
	QueueSize int
	Tick      time.Duration
}

// Runtime matchsvr 单主循环：串行承载 MMR 队列 + 凑桌（零锁）。
// off-loop 编排（开局/回告）经 go func 发起，回调经 Submit 回环；inflight 计停机 drain。
type Runtime struct {
	nodeID  string
	cls     cluster.Cluster
	tq      *taskqueue.Queue
	tw      *timewheel.TimeWheel
	queue   *matchQueue
	gameSeq int64

	tick     time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	inflight atomic.Int64

	// off-loop 编排 hook（默认接真实 router；测试可替换）
	openGame          func(gameID string, table []waiting) (roomNodeID string, err error)
	notifyGameStarted func(lobbyNode string, uid int64, gameID, roomNodeID string) error
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.MatchSize <= 0 {
		cfg.MatchSize = defaultMatchSize
	}
	if cfg.MMRWindow <= 0 {
		cfg.MMRWindow = defaultMMRWindow
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	rt := &Runtime{
		nodeID: cfg.NodeID,
		cls:    cfg.Cluster,
		tq:     taskqueue.New(cfg.QueueSize),
		tw:     timewheel.New(cfg.Tick, 512),
		queue:  newMatchQueue(cfg.MatchSize, cfg.MMRWindow),
		tick:   cfg.Tick,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	if cfg.Cluster != nil {
		rt.openGame = rt.openGameViaRouter
		rt.notifyGameStarted = rt.gameStartedViaRouter
	}
	return rt
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行。
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

func (rt *Runtime) Start() { go rt.loop() }

func (rt *Runtime) Stop() {
	rt.stopOnce.Do(func() { close(rt.stopCh) })
	<-rt.doneCh
}

func (rt *Runtime) loop() {
	defer close(rt.doneCh)
	ticker := time.NewTicker(rt.tick)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopCh:
			rt.drain()
			return
		case fn := <-rt.tq.C():
			fn()
		case <-ticker.C:
			rt.tw.Advance() // 超时/窗口放宽 tick 留后续；P4a 骨架
		}
	}
}

const drainTimeout = 5 * time.Second

// drain 排空 tq 直到在途编排回调清零（或超时兜底）。
func (rt *Runtime) drain() {
	deadline := time.After(drainTimeout)
	for rt.inflight.Load() > 0 {
		select {
		case fn := <-rt.tq.C():
			fn()
		case <-deadline:
			logger.Warn("match drain timeout, abandoning in-flight", logger.Int64("inflight", rt.inflight.Load()))
			return
		}
	}
}

// OnRequest 主循环内处理一条匹配请求：去重入队 + 尝试凑桌。
func (rt *Runtime) OnRequest(req *matchpb.MatchRequest) {
	w := waiting{uid: req.Uid, reqID: req.ReqId, mmr: req.Mmr, lobbyNode: req.LobbyNodeId}
	if !rt.queue.Enqueue(w) {
		return // 重复请求（JetStream 重投），已 ack 即可
	}
	rt.tryFormTables()
}

// tryFormTables 主循环内反复凑桌直到无法再成桌，每桌交 off-loop 编排。
func (rt *Runtime) tryFormTables() {
	for {
		table, ok := rt.queue.FormTable()
		if !ok {
			return
		}
		rt.gameSeq++
		gameID := fmt.Sprintf("%s-%d", rt.nodeID, rt.gameSeq)
		rt.orchestrate(gameID, table)
	}
}

// orchestrate off-loop 编排开局 + 回告：⑥开局拿 room_node_id，⑦对每 participant 回告 lobby。
func (rt *Runtime) orchestrate(gameID string, table []waiting) {
	og, ngs := rt.openGame, rt.notifyGameStarted
	if og == nil {
		return
	}
	rt.inflight.Add(1)
	go func() {
		defer rt.inflight.Add(-1)
		roomNodeID, err := og(gameID, table)
		if err != nil {
			logger.Warn("match open game failed", logger.String("gameId", gameID), logger.Int("participants", len(table)), logger.Err(err))
			// participants 已脱离 JetStream（ack 在前），放回内存队列等下条请求触发重凑（不立即重扫，避免与 room 长时不可达打紧循环）
			rt.Submit(func() {
				for _, w := range table {
					rt.queue.Requeue(w)
				}
			})
			return
		}
		for _, w := range table {
			if ngs == nil {
				continue
			}
			if err := ngs(w.lobbyNode, w.uid, gameID, roomNodeID); err != nil {
				// P4a：记录 + 继续（room 仍持该 participant），缺失绑定由 P4c 重连兜底
				logger.Warn("match notify gamestarted failed", logger.Int64("uid", w.uid), logger.String("lobby", w.lobbyNode), logger.String("gameId", gameID), logger.Err(err))
			}
		}
	}()
}

// queueLen 仅主循环/测试用：当前等待人数。
func (rt *Runtime) queueLen() int { return rt.queue.Len() }

// openGameViaRouter ⑥ 经 router CONSISTENT_HASH(gameId) 调 RoomHandler.opengame，拿回 room_node_id。
func (rt *Runtime) openGameViaRouter(gameID string, table []waiting) (string, error) {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	parts := make([]*roompb.Participant, 0, len(table))
	for _, w := range table {
		parts = append(parts, &roompb.Participant{Uid: w.uid, LobbyNodeId: w.lobbyNode})
	}
	rsp, err := routerclient.CallViaSync[*roompb.RPC_OpenGame_Rsp](
		ctx, rt.cls, "roomsvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, gameID,
		"RoomHandler.opengame",
		&roompb.RPC_OpenGame_Req{GameId: gameID, ItemId: defaultItemID, CountdownSec: defaultCountdownSec, Participants: parts},
	)
	if err != nil {
		return "", err
	}
	if rsp.Code != 0 {
		return "", fmt.Errorf("opengame code=%d", rsp.Code)
	}
	return rsp.RoomNodeId, nil
}

// gameStartedViaRouter ⑦ 经 router DIRECT(lobbyNode) 调 LobbyHandler.gamestarted 回告。
func (rt *Runtime) gameStartedViaRouter(lobbyNode string, uid int64, gameID, roomNodeID string) error {
	ctx := cluster.WithCluster(context.Background(), rt.cls)
	rsp, err := routerclient.CallViaSync[*matchpb.RPC_GameStarted_Rsp](
		ctx, rt.cls, "lobbysvr",
		routerpb.RoutingMode_ROUTING_DIRECT, lobbyNode,
		"LobbyHandler.gamestarted",
		&matchpb.RPC_GameStarted_Req{Uid: uid, GameId: gameID, RoomNodeId: roomNodeID},
	)
	if err != nil {
		return err
	}
	if rsp.Code != 0 {
		return fmt.Errorf("gamestarted code=%d", rsp.Code)
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过（含 -race）**

Run:
```bash
go test ./src/servers/matchsvr/internal/ -run TestRuntime -v
go test -race ./src/servers/matchsvr/internal/ -run TestRuntime
```
Expected: PASS，无 race（spy 全程持锁，hook 在 off-loop goroutine 调）。

- [ ] **Step 5: Commit**

```bash
git add src/servers/matchsvr/internal/runtime.go src/servers/matchsvr/internal/runtime_test.go
git commit -m "feat: matchsvr Runtime 主循环——OnRequest 去重入队+凑桌+off-loop 开局/回告编排（gameId 唯一，失败放回）"
```

---

## Task 15: matchsvr JetStream 消费桥接 + module + main + conf

**Files:**
- Create: `src/servers/matchsvr/internal/match_consumer.go`、`match_consumer_test.go`、`match_module.go`、`src/servers/matchsvr/main.go`、`conf/match.yaml`

- [ ] **Step 1: 写失败测试 `match_consumer_test.go`** —— 消费 → Submit → 入队；重投幂等；坏消息 ack

```go
package internal

import (
	"context"
	"testing"

	matchpb "project/protocal/gen/match"
	"project/src/common/matchqueue"
)

func TestStartConsumer_EnqueuesAndDedups(t *testing.T) {
	rt := newTestMatchRuntime(t) // MatchSize=2 → 单条不成桌，留队可观察
	defer rt.Stop()
	mq := matchqueue.NewMemoryQueue()
	if err := rt.StartConsumer(context.Background(), mq); err != nil {
		t.Fatalf("start consumer: %v", err)
	}

	_ = mq.Publish(context.Background(), matchqueue.SubjectMatchRequest,
		&matchpb.MatchRequest{Uid: 1, ReqId: "r1", Mmr: 1000, LobbyNodeId: "1.2.1"})
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 1 {
			t.Fatalf("consumed request should be enqueued, got %d", rt.queueLen())
		}
	})

	// 重投同一条 → (uid,reqId) 去重，不重复入队
	if err := mq.Redeliver(context.Background(), 0); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 1 {
			t.Fatalf("redelivery should dedup, got %d", rt.queueLen())
		}
	})
}

func TestStartConsumer_EmptyFieldsDropped(t *testing.T) {
	rt := newTestMatchRuntime(t)
	defer rt.Stop()
	mq := matchqueue.NewMemoryQueue()
	if err := rt.StartConsumer(context.Background(), mq); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	// 空字段消息（uid=0/reqId="")应被消费侧校验丢弃（返回 nil 即 ack），不入队。
	if err := mq.Publish(context.Background(), matchqueue.SubjectMatchRequest, &matchpb.MatchRequest{Uid: 0}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	matchRunOnLoop(t, rt, func() {
		if rt.queueLen() != 0 {
			t.Fatalf("empty/invalid request should not enqueue, got %d", rt.queueLen())
		}
	})
}
```

> 说明：`MemoryQueue.Publish` 同步调用已注册 handler（消费桥接），handler 内 `Submit` 给主循环并 `<-done` 等入队完成后返回（即 ack）。故 `Publish`/`Redeliver` 返回后队列状态已落定，`matchRunOnLoop` 断言即可。空字段消息校验丢弃（ack），不入队。`match_consumer_test.go` 的 `matchRunOnLoop`/`newTestMatchRuntime` 复用 `runtime_test.go` 同包定义。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/matchsvr/internal/ -run TestStartConsumer -v`
Expected: FAIL（undefined `rt.StartConsumer`）。

- [ ] **Step 3: 写 `match_consumer.go`**

```go
package internal

import (
	"context"

	matchpb "project/protocal/gen/match"
	"project/src/common/logger"
	"project/src/common/matchqueue"
	"google.golang.org/protobuf/proto"
)

// StartConsumer 启动 JetStream 消费：每条 MatchRequest 经 Submit 进主循环处理，
// 入队完成后才返回（即 ack，遵循 umbrella §5.3「入队即 ack」）。坏/空消息校验丢弃（ack），避免毒丸重投。
func (rt *Runtime) StartConsumer(ctx context.Context, mq matchqueue.MatchQueue) error {
	return mq.Consume(ctx, matchqueue.DurableMatchsvr, func(_ context.Context, data []byte) error {
		var req matchpb.MatchRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			logger.Warn("match consume: bad payload", logger.Err(err))
			return nil // ack 丢弃
		}
		if req.Uid == 0 || req.ReqId == "" {
			logger.Warn("match consume: empty fields", logger.Int64("uid", req.Uid), logger.String("reqId", req.ReqId))
			return nil // ack 丢弃
		}
		done := make(chan struct{})
		rt.Submit(func() {
			rt.OnRequest(&req)
			close(done)
		})
		<-done // 入队完成后 ack
		return nil
	})
}
```

- [ ] **Step 4: 运行确认通过（含 -race）**

Run:
```bash
go test ./src/servers/matchsvr/internal/ -run TestStartConsumer -v
go test -race ./src/servers/matchsvr/internal/
```
Expected: PASS，无 race。

- [ ] **Step 5: 写 `match_module.go`（仅管 Runtime 生命周期；消费在 main 起，见下）**

```go
package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// MatchModule matchsvr 模块：持主循环 Runtime，Init 启动、OnStop 停止。
// JetStream 消费在 main.go 于 cls.Init() 后启动（编排需 discovery 就绪）。
type MatchModule struct {
	module.BaseModule
	rt *Runtime
}

func NewMatchModule(rt *Runtime) *MatchModule { return &MatchModule{rt: rt} }

func (m *MatchModule) Name() string { return "match" }

func (m *MatchModule) Init() {
	m.rt.Start()
	logger.Info("match module initialized")
}

func (m *MatchModule) OnStop() {
	m.rt.Stop()
	logger.Info("match module stopped")
}
```

- [ ] **Step 6: 写 `main.go`**

> matchsvr 无入站集群 RPC（请求走 JetStream），故不 `RegisterHandler`。消费在 `cls.Init()` 后启动——off-loop 编排经 router 调 room/lobby 需 discovery 就绪（`app.Start()` 在前会先于 `cls.Init()` 跑 module Init，故消费不能放 module Init）。

```go
package main

import (
	"context"

	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/matchqueue"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/matchsvr/internal"
)

func main() {
	cfg := config.MustLoad("conf/match.yaml")

	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	self, err := cluster.ParseNodeID(cfg.Node.ID)
	if err != nil {
		panic(err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints:  cfg.Cluster.Etcd.Endpoints,
		NatsURLs:       cfg.Cluster.Nats.URLs,
		SelfAddr:       cfg.Node.Addr,
		ServerTypeName: cfg.Node.ServerTypeName,
	})
	if err != nil {
		panic(err)
	}

	// 有状态后端：protobuf 序列化器，不调 Frontend()
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).
		Build()

	rt := internal.NewRuntime(internal.RuntimeConfig{NodeID: cfg.Node.ID, Cluster: app.Cluster()})
	app.Register(internal.NewMatchModule(rt))

	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	// JetStream 消费（真实适配器；沙箱不实跑，vet 编译验证）。cls.Init 后启动——编排需 discovery 就绪。
	mq, err := matchqueue.NewJetStreamQueue(cfg.Cluster.Nats.URLs)
	if err != nil {
		panic(err)
	}
	defer mq.Close()
	if err := rt.StartConsumer(context.Background(), mq); err != nil {
		panic(err)
	}

	logger.Info("matchsvr started", logger.String("nodeID", cfg.Node.ID))
	app.Run()
}
```

- [ ] **Step 7: 写 `conf/match.yaml`**

```yaml
# matchsvr 节点（worldID=1, serverTypeID=8, index=1）
node:
  id: "1.8.1"
  server_type_name: "matchsvr"
  addr: "0.0.0.0:8881"
cluster:
  etcd:
    endpoints: ["localhost:2379"]
  nats:
    urls: ["nats://localhost:4222"]
log:
  level: "info"
  dir: "./logs"
```

- [ ] **Step 8: 构建 + vet + 全量测试**

Run:
```bash
go build ./...
go vet ./src/servers/matchsvr/...
go test ./...
```
Expected: 全部通过。

- [ ] **Step 9: Commit**

```bash
git add src/servers/matchsvr/ conf/match.yaml
git commit -m "feat: matchsvr JetStream 消费桥接（入队即 ack）+ module + main + conf"
```

---

## Task 16: 端到端集成测试（build-tag，沙箱仅编译验证）

凑桌→开局→lobby 拿 room 绑定→online `Query` 可见 room 绑定的全链路集成。沙箱无 Docker，仅 `go vet -tags integration` 编译验证；实跑需容器 NATS+JetStream+etcd+MongoDB（同 P2/P3）。

**Files:**
- Create: `src/servers/matchsvr/internal/match_integration_test.go`

- [ ] **Step 1: 写集成测试骨架**

> 仿 `src/servers/onlinesvr/internal/online_integration_test.go` 的 `startNode` 模式：起 router(1.6.1)/online(1.5.1)/lobby(1.2.1)/room(1.7.1)/match(1.8.1) 五节点 + 真实 `matchqueue.NewJetStreamQueue`；登录两玩家→各发 CS_StartMatch（经 lobby Startmatch 发布）→等 matchsvr 凑桌开局回告→断言两 lobby 玩家 `RoomAffinity()!=nil` 且 online `Query` 可见相同 `RoomNodeId/GameId`。

```go
//go:build integration

package internal

import (
	"testing"
	// 真实多节点装配 import 见 online_integration_test.go 同款
)

// TestP4a_MatchToOpenGame 全链路：登录→发起匹配→凑桌→开局→lobby 拿 room 绑定→online 可见。
// 沙箱无 Docker 不实跑（go vet -tags integration 编译验证）；实跑需 NATS+JetStream+etcd+MongoDB。
func TestP4a_MatchToOpenGame(t *testing.T) {
	t.Skip("requires NATS+JetStream+etcd+MongoDB; sandbox compile-verify only")
	// 1. 起 router/online/lobby/room/match 五节点（startNode 模式，见 online_integration_test.go）
	// 2. mq := matchqueue.NewJetStreamQueue(natsURLs); 注入 router publisher + match consumer
	// 3. 两玩家登录 lobby（token="1001"/"1002"），各触发 LobbyHandler.startmatch
	// 4. 轮询：两玩家 lobby Player(uid).RoomAffinity() != nil（同 gameId/room）
	// 5. online RPC_Query：两 uid 的 Entry.RoomNodeId/GameId 一致且非空
}
```

- [ ] **Step 2: 编译验证**

Run: `go vet -tags integration ./src/servers/matchsvr/...`
Expected: 通过（`t.Skip` 保证不实跑）。同时确认全仓 `go vet -tags integration ./...` 不破。

- [ ] **Step 3: Commit**

```bash
git add src/servers/matchsvr/internal/match_integration_test.go
git commit -m "test: P4a 全链路集成测试骨架（build-tag，沙箱仅编译验证）"
```

> **已知欠账（Spec §8/§9）**：JetStream durable consumer / ack / 重复投递 / stream 建立的真实语义需在 JetStream 环境专门验证；本骨架补齐编译期接线，实跑留 JetStream 环境。

---

## Task 17: 文档同步（CLAUDE.md 维护约定）

新增 matchsvr/roomsvr 目录、JetStream 设施、新 conf 影响 `architecture.md`/`cluster.md`/`development.md`，按 CLAUDE.md 维护约定同步。

**Files:**
- Modify: `architecture.md`、`cluster.md`、`development.md`

- [ ] **Step 1: 改 `architecture.md`** —— 目录树补 `src/servers/matchsvr`(填充)/`roomsvr`(填充)、`src/common/matchqueue`；分发主干补「匹配请求经 router→JetStream→matchsvr 消费→凑桌→开局→回告」一节（含寻址表：lobby→router publishmatch / match→room CONSISTENT_HASH(gameId) / match→lobby DIRECT / lobby→online CONSISTENT_HASH(uid)）。

- [ ] **Step 2: 改 `cluster.md`** —— 补 `RoutingMode_ROUTING_DIRECT` 落地用例（match→lobby）、`publishmatch` 非 forward 的 router 原生 route、JetStream 设施 `matchqueue`（接口/真实适配器/fake、stream/subject/durable 常量、入队即 ack 边界）。

- [ ] **Step 3: 改 `development.md`** —— 补 matchsvr/roomsvr 构建运行命令、`conf/match.yaml`/`conf/room.yaml`、serverTypeID 分配表（room=7/match=8）、proto 新增（match/room）与 JetStream（无 Docker 仅 vet）说明、protoc 借用路径备注（见 [[gameserver-dev-workflow]]）。

- [ ] **Step 4: 验证 + Commit**

Run: `go build ./... && go test ./...`（确认文档改动未夹带代码破坏）
```bash
git add architecture.md cluster.md development.md
git commit -m "docs: 同步 P4a——matchsvr/roomsvr/matchqueue 目录、寻址、JetStream 设施、serverTypeID 分配、新 conf"
```

---

## 终审（全部任务后，CLAUDE.md「终稿前自检」+ Spec 附录要求）

- [ ] **整支 `-race` 终审**：`go test -race ./...`（P2/P3 反复证明 verbatim 计划代码会偏离/不全，终审必做）。重点：matchsvr/lobby 的 off-loop hook 与主循环间共享态、测试 spy 的并发读写。
- [ ] **集成编译**：`go vet -tags integration ./...` 全绿。
- [ ] **自检清单**：输入校验（StartMatch 三分支 / opengame 参数 / 消费空字段）、状态检查（roomAffinity 判可匹配）、幂等（gameId 开局 / (uid,reqId) 去重 / BindRoom 绝对写 / 亲和绝对写）、并发安全（各 svr 单主循环零锁 + off-loop Submit 回环）、一致性（亲和内存 + online 同步，重连读 online 权威）、失败路径（开局失败放回、回告失败记录+继续）。
- [ ] **rebase**：合 PR 前 `git fetch && rebase origin/main`（见 [[gameserver-dev-workflow]]，远端 main 实现期会前进）。
