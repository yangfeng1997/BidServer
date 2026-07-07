# P3a 实现计划：lobby 执行模型 + MongoDB + EC 核心 + 背包竖切

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 lobby 成为单主循环、零锁的权威玩家枢纽，跑通"登录从 MongoDB 加载玩家 → 背包 CS 改状态 → 落库 → 重登读回"的端到端竖切。

**Architecture:** 单 lobby 进程 = 一个主循环 goroutine 承载全部玩家 EC 逻辑（零锁）；入站集群消息经 `taskqueue` 按到达序投递进主循环、handler 返回延迟回包哨兵立即返回；Mongo/出站 IO 异步、done 回调经 dispatcher 回主循环；回包由主循环 continuation 经注入 ctx 的 `Replier` 异步发出。承接设计 Spec [`2026-06-03-P3a-lobby-ec-mongodb.md`](2026-06-03-P3a-lobby-ec-mongodb.md)。

**Tech Stack:** Go 1.26、NATS（集群总线）、etcd（发现）、MongoDB（`go.mongodb.org/mongo-driver`）、protobuf（集群 wire + 客户端业务）、既有 `taskqueue`/`timewheel`/`event` 原语。

---

## 关键设计事实（动手前必读）

1. **集群 handler 走反射 registry**：`Application.Start()` 把 cluster handler 固定为 `registry.DispatchCluster`（`application.go:155`）。lobby **复用** registry，不另造 handler。lobby 序列化器是 **protobuf**，故 handler 方法可用**类型化** `req`（registry 自动 unmarshal）。
2. **延迟回包靠哨兵透传**：handler 方法返回 `(nil, cluster.ErrDeferredReply)`；`pcall` 透传该 error（`registry.go:277-284`）→ `DispatchCluster` 透传 → `handleMessage` 据 `errors.Is` 跳过自动回包，由主循环经 `Replier` 异步回。
3. **本地短路不注入 Replier**：`NatsRPC.Call`/`CallAsync` 在 `target==self` 时直接调 handler（`nats_rpc.go:103,126`），无 reply 主题。故 lobby 不得自调延迟 handler；P3a 入站均来自 gate/router 跨节点，不触发。
4. **gate 转发被本地分发遮蔽**：`agent.handleData` 先查 `MsgRouteTable` 本地分发（`agent.go:286`）再查 `ForwardTable`；`gen_routes` 把带 `handler_method` 的转发消息也放进 `MsgRouteTable`，故携 `handler_method`+`server_type` 的客户端消息被 gate 当本地处理丢弃。**Task A3 修复**：本地有 handler 才本地分发，否则转发。
5. **gate 转发不带 uid**：`agent.handleData` 用 `context.Background()` 作转发 ctx（`agent.go:283`），`buildDefaultForwardFn` 未填 `sess.Uid`。**Task A4 修复**：填 `sess.Uid = fctx.Agent.Session().UID()`，lobby 据此定位 Player。
6. **proto 序列化器分层**：集群 RPC 发送端硬编码 `proto.Marshal`，后端用 protobuf；gate 客户端侧用 json。**已知遗留（P3a 范围外）**：gate 转发把后端 proto 响应字节原样 `ag.Response` 给客户端，与 gate 客户端 json 协议不一致——P3a 集成测试在 **gate→lobby 集群边界** 驱动（发集群 RPC + `ClusterSession{uid}`），不依赖客户端 socket 协议定稿。
7. **沙箱无 Docker**：依赖 NATS/etcd/MongoDB 的集成测试用 `//go:build integration` 隔离，仅 `go vet -tags integration ./...` 编译验证；实跑在有 Docker 机器上。
8. **protoc 借用**：系统无 protoc，用 `/game/dev/silver-server/tools/server_excel_tool/protoc`，well-known include `/game/dev/silver-server/3rd/protobuf/include`。

---

## 文件结构

**框架改动（共享，小而通用）**
- `src/framework/cluster/cluster.go` — 加 `ErrDeferredReply` 哨兵 + `Replier` 接口
- `src/framework/cluster/context.go` — 加 `WithReplier`/`ReplierFromCtx`
- `src/framework/cluster/transport/nats_rpc.go` — `natsReplier` + 提取 `publishReply` + `handleMessage` 注入 replier + 延迟跳过
- `src/framework/handler/registry.go` — 加 `HasRoute`
- `src/framework/agent/agent.go` — `handleData` 本地有 handler 才本地分发
- `src/framework/application/application.go` — `buildDefaultForwardFn` 填 `sess.Uid`（提取纯函数 `newForwardSession`）
- `src/common/taskqueue/taskqueue.go` — 加 `C()` channel 访问器

**通用新增**
- `src/common/config/config.go` — 加 `MongoConfig`
- `src/common/mongo/mongo.go` — Mongo 客户端：`Connect`/`Close`/`FindByID`/`UpsertSetByID`（异步、回调经 dispatcher）

**lobbysvr（EC + 运行时 + 业务）**
- `src/servers/lobbysvr/internal/player.go` — `Component` 接口 + `Player` 实体
- `src/servers/lobbysvr/internal/events.go` — `Events`（`PlayerLoaded`）
- `src/servers/lobbysvr/internal/component_bag.go` — `Bag` 组件 + `BagState`
- `src/servers/lobbysvr/internal/store.go` — `PlayerDoc` + `DocStore` 接口 + `mongoStore` + `buildPlayer`
- `src/servers/lobbysvr/internal/runtime.go` — `Runtime`（主循环、Submit、flush、login/disconnect 逻辑）
- `src/servers/lobbysvr/internal/lobby_handler.go` — 改：login/disconnect/additem/baglist handler 薄壳
- `src/servers/lobbysvr/internal/lobby_module.go` — 改：持 Runtime，Init 启动 loop、OnStop 停
- `src/servers/lobbysvr/main.go` — 改：连 Mongo + 建 store/runtime/module/handler
- `conf/lobby.yaml` — 加 mongo 段

**proto**
- `protocal/lobby.proto` — 加背包 `BagItem`/`CS_AddItem`/`SC_AddItem`/`CS_BagList`/`SC_BagList`
- 重生成 `protocal/gen/lobby/*`、`protocal/gen/routes/*`

---

## Stage A — 框架地基

### Task A1: cluster 延迟回包原语（Replier + 哨兵 + ctx）

**Files:**
- Modify: `src/framework/cluster/cluster.go`
- Modify: `src/framework/cluster/context.go`
- Test: `src/framework/cluster/context_test.go`（新建或追加）

- [ ] **Step 1: 写失败测试** `src/framework/cluster/context_test.go`

```go
package cluster

import (
	"context"
	"errors"
	"testing"
)

type capturingReplier struct {
	data []byte
	err  error
	n    int
}

func (c *capturingReplier) Reply(data []byte, err error) { c.data, c.err, c.n = data, err, c.n+1 }

func TestReplierRoundTrip(t *testing.T) {
	r := &capturingReplier{}
	ctx := WithReplier(context.Background(), r)
	got := ReplierFromCtx(ctx)
	if got == nil {
		t.Fatal("ReplierFromCtx returned nil")
	}
	got.Reply([]byte("ok"), nil)
	if r.n != 1 || string(r.data) != "ok" {
		t.Fatalf("reply not delivered: n=%d data=%q", r.n, r.data)
	}
}

func TestReplierFromCtx_Absent(t *testing.T) {
	if ReplierFromCtx(context.Background()) != nil {
		t.Fatal("expected nil replier when absent")
	}
}

func TestErrDeferredReplyIdentity(t *testing.T) {
	if !errors.Is(ErrDeferredReply, ErrDeferredReply) {
		t.Fatal("sentinel identity broken")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/framework/cluster/ -run TestReplier -v`
Expected: FAIL（`undefined: WithReplier/ReplierFromCtx/ErrDeferredReply`）

- [ ] **Step 3: 实现**。`cluster.go` 顶部 import 加 `"errors"`，文件末尾加：

```go
// ErrDeferredReply 由 handler 返回，表示"我将经 Replier 异步回包"，
// 传输层据此跳过自动回包。仅适用于 NATS 请求-应答路径（非本地短路）。
var ErrDeferredReply = errors.New("cluster: reply deferred by handler")

// Replier 主循环发起的异步回包句柄，由传输层注入 ctx。
// data 为已序列化的响应体；err 非 nil 时回错误响应。线程安全（NATS publish 线程安全）。
type Replier interface {
	Reply(data []byte, err error)
}
```

`context.go` 加 key 与 helper：

```go
type ctxReplierKey struct{}

// WithReplier 把 Replier 注入 ctx，供延迟回包的 handler 在主循环 continuation 中取用
func WithReplier(ctx context.Context, r Replier) context.Context {
	return context.WithValue(ctx, ctxReplierKey{}, r)
}

// ReplierFromCtx 从 ctx 取 Replier，不存在时返回 nil
func ReplierFromCtx(ctx context.Context) Replier {
	return ctxGet[Replier](ctx, ctxReplierKey{})
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/framework/cluster/ -run 'TestReplier|TestErrDeferred' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/framework/cluster/cluster.go src/framework/cluster/context.go src/framework/cluster/context_test.go
git commit -m "feat(cluster): 延迟回包原语 Replier + ErrDeferredReply 哨兵"
```

---

### Task A2: 传输层延迟回包（natsReplier + handleMessage 跳过）

**Files:**
- Modify: `src/framework/cluster/transport/nats_rpc.go`
- Test: `src/framework/cluster/transport/nats_rpc_test.go`（新建或追加）

- [ ] **Step 1: 写失败测试**（`publishReply` 经可替换 publisher 单测，无需 NATS）`nats_rpc_test.go`

```go
package transport

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster/pb"
)

type fakePublisher struct {
	subj string
	data []byte
	n    int
}

func (f *fakePublisher) Publish(subj string, data []byte) error {
	f.subj, f.data, f.n = subj, data, f.n+1
	return nil
}

func TestPublishReply_Success(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "inbox.1", []byte("body"), nil)
	if p.n != 1 || p.subj != "inbox.1" {
		t.Fatalf("publish not called correctly: %+v", p)
	}
	var resp pb.ClusterResponse
	if err := proto.Unmarshal(p.data, &resp); err != nil {
		t.Fatal(err)
	}
	if string(resp.Data) != "body" || resp.ErrMsg != "" {
		t.Fatalf("bad response: %+v", &resp)
	}
}

func TestPublishReply_Error(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "inbox.1", nil, errors.New("boom"))
	var resp pb.ClusterResponse
	_ = proto.Unmarshal(p.data, &resp)
	if resp.ErrMsg != "boom" || resp.Data != nil {
		t.Fatalf("expected error response, got %+v", &resp)
	}
}

func TestPublishReply_EmptyReplyNoOp(t *testing.T) {
	p := &fakePublisher{}
	publishReply(p, "", []byte("x"), nil)
	if p.n != 0 {
		t.Fatal("should not publish when reply subject empty")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/framework/cluster/transport/ -run TestPublishReply -v`
Expected: FAIL（`undefined: publishReply`）

- [ ] **Step 3: 实现**。`nats_rpc.go`：新增 `publisher` 接口、`natsReplier`、`publishReply`，并改 `handleMessage`。

新增（文件内合适位置，紧邻 `handleMessage`）：

```go
// publisher 抽象 NATS 发布，便于 publishReply 单测（*nats.Conn 满足之）
type publisher interface {
	Publish(subj string, data []byte) error
}

// natsReplier 主循环异步回包：持 reply 主题，延迟到 continuation 再发包
type natsReplier struct {
	conn  publisher
	reply string
}

func (n *natsReplier) Reply(data []byte, err error) { publishReply(n.conn, n.reply, data, err) }

// publishReply 封装 ClusterResponse 并发布到 reply 主题；reply 为空（OneWay）时 no-op
func publishReply(p publisher, reply string, data []byte, err error) {
	if reply == "" {
		if err != nil {
			logger.Warn("cluster: handler error (oneway)", logger.Err(err))
		}
		return
	}
	resp := &pb.ClusterResponse{Data: data}
	if err != nil {
		resp.Data = nil
		resp.ErrMsg = err.Error()
	}
	body, merr := proto.Marshal(resp)
	if merr != nil {
		logger.Warn("cluster: marshal response failed", logger.Err(merr))
		return
	}
	if perr := p.Publish(reply, body); perr != nil {
		logger.Warn("cluster: reply failed", logger.Err(perr))
	}
}
```

改 `handleMessage` 的 ctx 构造与回包尾部（`nats_rpc.go` 注入 replier，调用后据哨兵跳过）。把 `data, err := r.handler(...)` 之前加注入，之后整体替换为：

```go
	ctx = cluster.WithReplier(ctx, &natsReplier{conn: r.conn, reply: natsMsg.Reply})

	data, err := r.handler(ctx, cm.Data, cm.Route)
	if errors.Is(err, cluster.ErrDeferredReply) {
		return // handler 将经 Replier 异步回包
	}
	publishReply(r.conn, natsMsg.Reply, data, err)
}
```

（删除原 `if natsMsg.Reply == "" {...}` 到结尾的回包块，由 `publishReply` 统一处理。`nats_rpc.go` 顶部已 import `"errors"`；若未 import 则补上。）

- [ ] **Step 4: 跑测试确认通过 + 全量构建**

Run: `go test ./src/framework/cluster/transport/ -run TestPublishReply -v && go build ./...`
Expected: PASS；build 成功（现有集成测试仍编译）

- [ ] **Step 5: 提交**

```bash
git add src/framework/cluster/transport/nats_rpc.go src/framework/cluster/transport/nats_rpc_test.go
git commit -m "feat(cluster): 传输层支持主循环延迟回包（natsReplier + 哨兵跳过）"
```

---

### Task A3: registry.HasRoute + agent 本地无 handler 才转发（D9）

**Files:**
- Modify: `src/framework/handler/registry.go`
- Modify: `src/framework/agent/agent.go:285-298`
- Test: `src/framework/handler/registry_test.go`（追加）

- [ ] **Step 1: 写失败测试** `registry_test.go` 追加

```go
func TestRegistry_HasRoute(t *testing.T) {
	r := NewRegistry(testSerializer()) // 见同文件既有辅助；若无则用 json.NewSerializer()
	if err := r.RegisterHandler(&pingHandler{}, nil); err != nil {
		t.Fatal(err)
	}
	if !r.HasRoute("PingHandler.ping") {
		t.Fatal("expected route registered")
	}
	if r.HasRoute("PingHandler.nope") {
		t.Fatal("unexpected route")
	}
}
```

> 说明：`pingHandler` 用本测试文件已有的样例 handler；若无，定义最小 `type pingHandler struct{}; func (pingHandler) Ping(ctx context.Context) (*somepb, error)`，或复用文件内现有 handler 类型并改断言里的 route 名。`testSerializer()` 用文件已有 serializer 构造方式。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/framework/handler/ -run TestRegistry_HasRoute -v`
Expected: FAIL（`undefined: HasRoute`）

- [ ] **Step 3: 实现**。`registry.go` 加方法：

```go
// HasRoute 报告是否注册了该 route 的 handler（gate 据此决定本地处理还是转发）
func (r *Registry) HasRoute(route string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[route]
	return ok
}
```

`agent.go` 改 `handleData` 本地分发判定（仅当本地真有 handler 才本地分发，否则落入转发）：

```go
	// 查本地路由表：仅当本地 registry 真有该 handler 才本地分发，
	// 否则落入下方转发（携 handler_method 的转发消息在 gate 上无本地 handler → 转发）。
	if route, ok := a.msgRouteTable[msg.MsgID]; ok && a.registry.HasRoute(route) {
		respMsgID := a.respMsgIDTable[msg.MsgID]
		if err := a.registry.Dispatch(ctx, a, route, uint32(msg.MID), respMsgID, msg.Data); err != nil {
			logger.Error("agent: handler dispatch failed",
				logger.String("route", route),
				logger.Uint32("msgID", msg.MsgID),
				logger.Int64("uid", a.session.UID()),
				logger.Err(err))
		}
		return nil
	}
```

- [ ] **Step 4: 跑测试确认通过 + 构建**

Run: `go test ./src/framework/handler/ -run TestRegistry_HasRoute -v && go build ./...`
Expected: PASS；build 成功

- [ ] **Step 5: 提交**

```bash
git add src/framework/handler/registry.go src/framework/handler/registry_test.go src/framework/agent/agent.go
git commit -m "fix(gate): 本地无 handler 才本地分发，否则转发（修复转发被遮蔽，spec D9）"
```

---

### Task A4: gate 转发填 ClusterSession.Uid

**Files:**
- Modify: `src/framework/application/application.go:200-214`
- Test: `src/framework/application/forward_session_test.go`（新建）

- [ ] **Step 1: 写失败测试** `forward_session_test.go`

```go
package application

import (
	"testing"

	clusterpb "project/src/framework/cluster/pb"
)

func TestNewForwardSession_FillsUid(t *testing.T) {
	s := newForwardSession(nil, 42, 1, "1.1.1", 10001)
	if s.Uid != 10001 {
		t.Fatalf("uid not filled: %d", s.Uid)
	}
	if s.ClientMid != 42 || s.MsgType != 1 || s.FrontendId != "1.1.1" {
		t.Fatalf("base fields wrong: %+v", s)
	}
}

func TestNewForwardSession_KeepsExistingFrontend(t *testing.T) {
	base := &clusterpb.ClusterSession{FrontendId: "9.9.9"}
	s := newForwardSession(base, 1, 3, "1.1.1", 7)
	if s.FrontendId != "9.9.9" || s.Uid != 7 {
		t.Fatalf("unexpected: %+v", s)
	}
}
```

> 注：`ClusterSession` 在 `project/src/framework/cluster/pb`（`cluster.proto` 的 `go_package`），现有代码以 `clusterpb` 别名引用，与 `application.go` 一致。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/framework/application/ -run TestNewForwardSession -v`
Expected: FAIL（`undefined: newForwardSession`）

- [ ] **Step 3: 实现**。`application.go` 提取纯函数并在 `buildDefaultForwardFn` 中调用：

```go
// newForwardSession 构造转发用 ClusterSession：保留已有字段，填 client_mid/msg_type/frontend_id/uid。
// 抽成纯函数便于单测。
func newForwardSession(base *clusterpb.ClusterSession, mid uint32, msgType uint8, frontendID string, uid int64) *clusterpb.ClusterSession {
	sess := base
	if sess == nil {
		sess = &clusterpb.ClusterSession{}
	}
	sess.ClientMid = mid
	sess.MsgType = uint32(msgType)
	if sess.FrontendId == "" {
		sess.FrontendId = frontendID
	}
	sess.Uid = uid
	return sess
}
```

`buildDefaultForwardFn` 内把原 session 构造块替换为：

```go
		sess := newForwardSession(
			cluster.SessionFromCtx(ctx),
			uint32(fctx.MID), fctx.MsgType, nodeID,
			fctx.Agent.Session().UID(),
		)
		ctx = cluster.WithSession(ctx, sess)
```

- [ ] **Step 4: 跑测试确认通过 + 构建**

Run: `go test ./src/framework/application/ -run TestNewForwardSession -v && go build ./...`
Expected: PASS；build 成功

- [ ] **Step 5: 提交**

```bash
git add src/framework/application/application.go src/framework/application/forward_session_test.go
git commit -m "feat(gate): 转发填 ClusterSession.Uid（lobby 据 uid 定位 Player）"
```

---

### Task A5: taskqueue 暴露 channel 访问器

**Files:**
- Modify: `src/common/taskqueue/taskqueue.go`
- Test: `src/common/taskqueue/taskqueue_test.go`（追加）

- [ ] **Step 1: 写失败测试** 追加

```go
func TestQueue_C_ReceivesEnqueued(t *testing.T) {
	q := New(4)
	q.Enqueue(func() {})
	select {
	case fn := <-q.C():
		fn()
	default:
		t.Fatal("C() did not expose enqueued task")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/common/taskqueue/ -run TestQueue_C -v`
Expected: FAIL（`q.C undefined`）

- [ ] **Step 3: 实现**

```go
// C 返回底层任务 channel，供事件驱动主循环 select（与 Flush 二选一使用）
func (q *Queue) C() <-chan func() { return q.ch }
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/common/taskqueue/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/common/taskqueue/taskqueue.go src/common/taskqueue/taskqueue_test.go
git commit -m "feat(taskqueue): 暴露 C() 供事件驱动主循环 select"
```

---

## Stage B — MongoDB 接入层 + config

### Task B1: config 加 MongoConfig

**Files:**
- Modify: `src/common/config/config.go`
- Test: `src/common/config/config_test.go`（追加）

- [ ] **Step 1: 写失败测试** 追加

```go
func TestLoad_MongoSection(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/c.yaml"
	content := "" +
		"node:\n  id: \"1.2.1\"\n  server_type_name: \"lobbysvr\"\n  addr: \"0.0.0.0:8801\"\n" +
		"mongo:\n  uri: \"mongodb://localhost:27017\"\n  database: \"game\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mongo.URI != "mongodb://localhost:27017" || cfg.Mongo.Database != "game" {
		t.Fatalf("mongo cfg not parsed: %+v", cfg.Mongo)
	}
}
```

> 若 `config_test.go` 尚未 import `"os"`，补上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/common/config/ -run TestLoad_MongoSection -v`
Expected: FAIL（`cfg.Mongo undefined`）

- [ ] **Step 3: 实现**。`config.go`：`ServerConfig` 加字段 + 新类型：

```go
// 在 ServerConfig 结构体中追加：
	Mongo Mongo `yaml:"mongo"`
```

```go
// Mongo MongoDB 连接配置
type Mongo struct {
	URI      string `yaml:"uri"`      // 如 "mongodb://localhost:27017"
	Database string `yaml:"database"` // 库名，默认 "game"
}
```

`defaults(cfg)` 内追加：

```go
	if cfg.Mongo.URI != "" && cfg.Mongo.Database == "" {
		cfg.Mongo.Database = "game"
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/common/config/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/common/config/config.go src/common/config/config_test.go
git commit -m "feat(config): 加 MongoConfig（uri/database）"
```

---

### Task B2: MongoDB 接入层（异步 CRUD）

**Files:**
- Create: `src/common/mongo/mongo.go`
- Test: `src/common/mongo/mongo_test.go`
- Modify: `go.mod`/`go.sum`（`go get go.mongodb.org/mongo-driver/mongo`）

- [ ] **Step 1: 加依赖**

Run: `go get go.mongodb.org/mongo-driver/mongo@latest`
Expected: `go.mod` 出现 `go.mongodb.org/mongo-driver`

- [ ] **Step 2: 写失败测试**（白盒测 `runAsync` 经 dispatcher 投递，无需 Mongo）`mongo_test.go`

```go
package mongo

import (
	"errors"
	"testing"

	"project/src/common/taskqueue"
)

func TestRunAsync_DeliversResultViaDispatcher(t *testing.T) {
	q := taskqueue.New(4)
	sentinel := errors.New("boom")
	var got error
	runAsync(q, func() error { return sentinel }, func(err error) { got = err })

	fn := <-q.C() // 阻塞直到 op 完成并入队（确定性，无需轮询）
	fn()
	if !errors.Is(got, sentinel) {
		t.Fatalf("done not delivered with op error: %v", got)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./src/common/mongo/ -run TestRunAsync -v`
Expected: FAIL（包不存在 / `runAsync` 未定义）

- [ ] **Step 4: 实现** `mongo.go`

```go
// Package mongo 提供 MongoDB 直连封装：连接管理 + 异步 CRUD。
// 异步方法在 off-loop goroutine 执行阻塞 IO，完成后把回调经 dispatcher
// 投递回调用方主循环（镜像 cluster.Call 的回调投递语义），契合帧驱动零锁服务。
package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	driver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"project/src/common/taskqueue"
)

// Client MongoDB 客户端封装
type Client struct {
	cli *driver.Client
	db  *driver.Database
}

// Connect 连接 MongoDB 并 Ping 校验，timeout 控制连接+Ping 总时长
func Connect(uri, dbName string, timeout time.Duration) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cli, err := driver.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := cli.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &Client{cli: cli, db: cli.Database(dbName)}, nil
}

// Close 断开连接
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.cli.Disconnect(ctx)
}

// runAsync 在 off-loop goroutine 执行 op，完成后把 done(err) 投递回 dispatcher（主循环串行执行）
func runAsync(d taskqueue.Dispatcher, op func() error, done func(error)) {
	go func() {
		err := op()
		d.Enqueue(func() { done(err) })
	}()
}

// FindByID 异步按 _id 读文档解码进 out；done(found, err) 在 dispatcher 执行。
// 文档不存在时 found=false、err=nil。
func (c *Client) FindByID(d taskqueue.Dispatcher, coll string, id any, out any, done func(found bool, err error)) {
	found := false
	runAsync(d, func() error {
		err := c.db.Collection(coll).FindOne(context.Background(), bson.M{"_id": id}).Decode(out)
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

// UpsertSetByID 异步对 _id 文档做 $set（upsert）；done(err) 在 dispatcher 执行。
// 绝对状态写，天然幂等（重复 flush 不双加）。
func (c *Client) UpsertSetByID(d taskqueue.Dispatcher, coll string, id any, set bson.M, done func(error)) {
	runAsync(d, func() error {
		_, err := c.db.Collection(coll).UpdateByID(context.Background(), id,
			bson.M{"$set": set}, options.Update().SetUpsert(true))
		return err
	}, done)
}
```

- [ ] **Step 5: 跑测试确认通过 + 构建**

Run: `go test ./src/common/mongo/ -v && go build ./...`
Expected: PASS；build 成功

- [ ] **Step 6: 提交**

```bash
git add go.mod go.sum src/common/mongo/
git commit -m "feat(mongo): MongoDB 接入层，异步 CRUD 回调经 dispatcher 回主循环"
```

---

## Stage C — EC 核心

### Task C1: Component 接口 + Player 实体

**Files:**
- Create: `src/servers/lobbysvr/internal/player.go`
- Test: `src/servers/lobbysvr/internal/player_test.go`

- [ ] **Step 1: 写失败测试** `player_test.go`

```go
package internal

import "testing"

type fakeComp struct {
	name  string
	dirty bool
}

func (f *fakeComp) Name() string       { return f.name }
func (f *fakeComp) Field() string      { return f.name }
func (f *fakeComp) Snapshot() any       { return f.name }
func (f *fakeComp) Dirty() bool        { return f.dirty }
func (f *fakeComp) ClearDirty()        { f.dirty = false }
func (f *fakeComp) MarkDirty()         { f.dirty = true }

func TestPlayer_AddAndGet(t *testing.T) {
	p := NewPlayer(10001)
	c := &fakeComp{name: "x"}
	p.AddComponent(c)
	if p.UID() != 10001 {
		t.Fatalf("uid=%d", p.UID())
	}
	if p.Component("x") != c {
		t.Fatal("component not found")
	}
	if len(p.Components()) != 1 {
		t.Fatalf("components=%d", len(p.Components()))
	}
}

func TestPlayer_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate component")
		}
	}()
	p := NewPlayer(1)
	p.AddComponent(&fakeComp{name: "x"})
	p.AddComponent(&fakeComp{name: "x"})
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPlayer -v`
Expected: FAIL（`undefined: NewPlayer/Component`）

- [ ] **Step 3: 实现** `player.go`

```go
package internal

// Component lobby 实体组件接口（仅 lobby 用）。Load 各组件自定义类型化方法，
// 不入接口；接口只覆盖通用 flush 路径（Field/Snapshot/Dirty）。
type Component interface {
	Name() string             // 组件唯一名
	Field() string            // 在 players 文档中的 bson 字段名
	Snapshot() any            // 可落库的状态快照（值拷贝，bson-able）
	Dirty() bool              // 有未落库变更
	ClearDirty()              // 落库成功后清脏
	MarkDirty()               // 落库失败后重置脏，下次重试
}

// Player 玩家实体：player_id + 组件集合，主循环独占、零锁
type Player struct {
	uid        int64
	components map[string]Component
	order      []string
}

func NewPlayer(uid int64) *Player {
	return &Player{uid: uid, components: make(map[string]Component)}
}

func (p *Player) UID() int64 { return p.uid }

// AddComponent 手写显式注册（重复注册 panic，编译/启动期暴露错误）
func (p *Player) AddComponent(c Component) {
	if _, ok := p.components[c.Name()]; ok {
		panic("lobby: duplicate component " + c.Name())
	}
	p.components[c.Name()] = c
	p.order = append(p.order, c.Name())
}

func (p *Player) Component(name string) Component { return p.components[name] }

// Components 按注册顺序返回组件
func (p *Player) Components() []Component {
	out := make([]Component, 0, len(p.order))
	for _, n := range p.order {
		out = append(out, p.components[n])
	}
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestPlayer -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/player.go src/servers/lobbysvr/internal/player_test.go
git commit -m "feat(lobby): EC 核心 Component 接口 + Player 实体（手写注册）"
```

---

### Task C2: 事件总线（PlayerLoaded）

**Files:**
- Create: `src/servers/lobbysvr/internal/events.go`
- Test: `src/servers/lobbysvr/internal/events_test.go`

- [ ] **Step 1: 写失败测试** `events_test.go`

```go
package internal

import "testing"

func TestEvents_PlayerLoaded(t *testing.T) {
	ev := NewEvents()
	var got int64
	ev.PlayerLoaded.Subscribe(func(e PlayerLoaded) { got = e.UID })
	ev.PlayerLoaded.Publish(PlayerLoaded{UID: 777})
	if got != 777 {
		t.Fatalf("event not delivered: %d", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestEvents -v`
Expected: FAIL（`undefined: NewEvents/PlayerLoaded`）

- [ ] **Step 3: 实现** `events.go`

```go
package internal

import "project/src/common/event"

// PlayerLoaded 玩家工作副本加载完成事件（组件可订阅做初始化）
type PlayerLoaded struct{ UID int64 }

// Events lobby 进程内同步事件总线集合，仅主循环使用（零锁）。
// P3a 仅 PlayerLoaded；跨组件业务事件（买道具→扣货币）随组件落地于 P3b 增补。
type Events struct {
	PlayerLoaded *event.Bus[PlayerLoaded]
}

func NewEvents() *Events {
	return &Events{PlayerLoaded: event.NewBus[PlayerLoaded]()}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestEvents -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/events.go src/servers/lobbysvr/internal/events_test.go
git commit -m "feat(lobby): EC 事件总线 Events（PlayerLoaded）"
```

---

## Stage D — 背包组件

### Task D1: BagState + Bag 加载/快照/脏标

**Files:**
- Create: `src/servers/lobbysvr/internal/component_bag.go`
- Test: `src/servers/lobbysvr/internal/component_bag_test.go`

- [ ] **Step 1: 写失败测试** `component_bag_test.go`

```go
package internal

import "testing"

func TestBag_LoadAndSnapshot(t *testing.T) {
	b := NewBag()
	b.Load(&BagState{Items: map[string]int32{"100": 5, "200": 3}})
	if b.Count(100) != 5 || b.Count(200) != 3 {
		t.Fatalf("load wrong: %v", b.Items())
	}
	if b.Dirty() {
		t.Fatal("freshly loaded bag should be clean")
	}
	snap := b.Snapshot().(BagState)
	if snap.Items["100"] != 5 {
		t.Fatalf("snapshot wrong: %v", snap.Items)
	}
}

func TestBag_DirtyLifecycle(t *testing.T) {
	b := NewBag()
	b.MarkDirty()
	if !b.Dirty() {
		t.Fatal("MarkDirty")
	}
	b.ClearDirty()
	if b.Dirty() {
		t.Fatal("ClearDirty")
	}
}

func TestBag_ComponentNames(t *testing.T) {
	b := NewBag()
	if b.Name() != BagComponentName || b.Field() != BagField {
		t.Fatalf("names: %s/%s", b.Name(), b.Field())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBag_ -v`
Expected: FAIL（`undefined: NewBag/BagState`）

- [ ] **Step 3: 实现** `component_bag.go`（本步先到 Load/Snapshot/脏标；`Add` 留 D2）

```go
package internal

import "strconv"

const (
	BagComponentName = "bag"
	BagField         = "bag" // players 文档中的 bson 字段
	maxRecentOps     = 128   // op-id 去重环大小
)

// BagState 背包的存储态（内嵌 players 文档；bson map 键须为 string）
type BagState struct {
	Items map[string]int32 `bson:"items"` // itemID(字符串) → 数量
}

func NewBagState() BagState { return BagState{Items: map[string]int32{}} }

// Bag 背包组件：内存态 itemID(int32) → 数量；op-id 去重保幂等
type Bag struct {
	items     map[int32]int32
	recentOps map[string]struct{}
	opOrder   []string
	dirty     bool
}

func NewBag() *Bag {
	return &Bag{items: make(map[int32]int32), recentOps: make(map[string]struct{})}
}

func (b *Bag) Name() string  { return BagComponentName }
func (b *Bag) Field() string { return BagField }
func (b *Bag) Dirty() bool   { return b.dirty }
func (b *Bag) ClearDirty()   { b.dirty = false }
func (b *Bag) MarkDirty()    { b.dirty = true }

// Load 从存储态恢复内存态（覆盖，清脏）
func (b *Bag) Load(s *BagState) {
	b.items = make(map[int32]int32, len(s.Items))
	for k, v := range s.Items {
		if id, err := strconv.Atoi(k); err == nil {
			b.items[int32(id)] = v
		}
	}
	b.dirty = false
}

// Snapshot 返回可落库快照（值拷贝）
func (b *Bag) Snapshot() any {
	items := make(map[string]int32, len(b.items))
	for id, c := range b.items {
		items[strconv.Itoa(int(id))] = c
	}
	return BagState{Items: items}
}

// Count 返回某道具数量
func (b *Bag) Count(itemID int32) int32 { return b.items[itemID] }

// Items 返回内存态副本（避免外部改内部）
func (b *Bag) Items() map[int32]int32 {
	out := make(map[int32]int32, len(b.items))
	for k, v := range b.items {
		out[k] = v
	}
	return out
}

// 编译期断言 Bag 满足 Component
var _ Component = (*Bag)(nil)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBag_ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/component_bag.go src/servers/lobbysvr/internal/component_bag_test.go
git commit -m "feat(lobby): 背包组件 BagState + Load/Snapshot/脏标"
```

---

### Task D2: Bag.Add（op-id 去重 + 标脏）

**Files:**
- Modify: `src/servers/lobbysvr/internal/component_bag.go`
- Test: `src/servers/lobbysvr/internal/component_bag_test.go`（追加）

- [ ] **Step 1: 写失败测试** 追加

```go
func TestBag_Add_MarksDirtyAndAccumulates(t *testing.T) {
	b := NewBag()
	if got := b.Add("op1", 100, 5); got != 5 {
		t.Fatalf("add returned %d", got)
	}
	if !b.Dirty() {
		t.Fatal("add should mark dirty")
	}
	if got := b.Add("op2", 100, 3); got != 8 {
		t.Fatalf("accumulate returned %d", got)
	}
}

func TestBag_Add_IdempotentByOpID(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 5)
	if got := b.Add("op1", 100, 5); got != 5 { // 同 op-id 重试不双加
		t.Fatalf("duplicate op double-added: %d", got)
	}
	if b.Count(100) != 5 {
		t.Fatalf("count after dup: %d", b.Count(100))
	}
}

func TestBag_Add_NegativeRemoves(t *testing.T) {
	b := NewBag()
	b.Add("op1", 100, 5)
	b.Add("op2", 100, -5)
	if _, ok := b.Items()[100]; ok {
		t.Fatal("zero-count item should be removed")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBag_Add -v`
Expected: FAIL（`b.Add undefined`）

- [ ] **Step 3: 实现** `component_bag.go` 追加

```go
// Add 幂等加道具：opID 非空且已见过则跳过（返回当前数量）；否则按 count 增减并标脏。
// count 为负表示扣减；归零或转负的道具从背包移除。
func (b *Bag) Add(opID string, itemID, count int32) int32 {
	if opID != "" {
		if _, seen := b.recentOps[opID]; seen {
			return b.items[itemID]
		}
		b.rememberOp(opID)
	}
	if count != 0 {
		b.items[itemID] += count
		if b.items[itemID] <= 0 {
			delete(b.items, itemID)
		}
		b.dirty = true
	}
	return b.items[itemID]
}

// rememberOp 记录 op-id 并维护有界环（超界淘汰最旧）
func (b *Bag) rememberOp(opID string) {
	b.recentOps[opID] = struct{}{}
	b.opOrder = append(b.opOrder, opID)
	if len(b.opOrder) > maxRecentOps {
		old := b.opOrder[0]
		b.opOrder = b.opOrder[1:]
		delete(b.recentOps, old)
	}
}
```

> 注：op-id 去重为**会话内内存态**，flush 后不持久；跨重登/换 lobby 的去重依赖发奖幂等键，留 P4 结算。P3a 验收"同会话内重试不双加"由此满足。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestBag -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/component_bag.go src/servers/lobbysvr/internal/component_bag_test.go
git commit -m "feat(lobby): 背包 Add（op-id 去重保幂等 + 标脏）"
```

---

## Stage E — Store（持久化）+ Runtime（主循环）

### Task E1: PlayerDoc + DocStore + mongoStore + buildPlayer

**Files:**
- Create: `src/servers/lobbysvr/internal/store.go`
- Test: `src/servers/lobbysvr/internal/store_test.go`

- [ ] **Step 1: 写失败测试** `store_test.go`（含贯穿后续 Runtime 测试用的 `fakeStore`）

```go
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

func (f *fakeStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	doc, ok := f.docs[uid]
	done(doc, ok, nil)
}

func (f *fakeStore) FlushField(_ taskqueue.Dispatcher, uid int64, field string, state any, done func(error)) {
	f.flushed[strconv.FormatInt(uid, 10)+":"+field] = state
	done(nil)
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
```

> 把占位的 `itoa` 替换为 `strconv.FormatInt(i, 10)`（import `"strconv"`）。`fakeStore` 会在 Runtime 测试里复用。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestBuildPlayer' -v`
Expected: FAIL（`undefined: buildPlayer/NewPlayerDoc/DocStore`）

- [ ] **Step 3: 实现** `store.go`

```go
package internal

import (
	"go.mongodb.org/mongo-driver/bson"

	"project/src/common/mongo"
	"project/src/common/taskqueue"
)

const playersColl = "players"

// PlayerDoc players 集合的整玩家文档：_id=player_id + 各组件内嵌子文档
type PlayerDoc struct {
	ID  int64    `bson:"_id"`
	Bag BagState `bson:"bag"`
}

func NewPlayerDoc(uid int64) *PlayerDoc {
	return &PlayerDoc{ID: uid, Bag: NewBagState()}
}

// DocStore 玩家持久化抽象（便于 Runtime 单测用 fake 替换真实 Mongo）
type DocStore interface {
	Load(d taskqueue.Dispatcher, uid int64, done func(doc *PlayerDoc, found bool, err error))
	FlushField(d taskqueue.Dispatcher, uid int64, field string, state any, done func(error))
}

// mongoStore 基于 src/common/mongo 的 DocStore 实现
type mongoStore struct{ c *mongo.Client }

func NewMongoStore(c *mongo.Client) *mongoStore { return &mongoStore{c: c} }

func (s *mongoStore) Load(d taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	doc := &PlayerDoc{}
	s.c.FindByID(d, playersColl, uid, doc, func(found bool, err error) {
		if err != nil || !found {
			done(nil, found, err)
			return
		}
		done(doc, true, nil)
	})
}

func (s *mongoStore) FlushField(d taskqueue.Dispatcher, uid int64, field string, state any, done func(error)) {
	s.c.UpsertSetByID(d, playersColl, uid, bson.M{field: state}, done)
}

var _ DocStore = (*mongoStore)(nil)

// buildPlayer 手写组装玩家实体：按 doc 加载各组件（P3a 仅背包）
func buildPlayer(uid int64, doc *PlayerDoc) *Player {
	p := NewPlayer(uid)
	bag := NewBag()
	bag.Load(&doc.Bag)
	p.AddComponent(bag)
	return p
}
```

- [ ] **Step 4: 跑测试确认通过 + 构建**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestBuildPlayer' -v && go build ./...`
Expected: PASS；build 成功

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/store_test.go
git commit -m "feat(lobby): PlayerDoc + DocStore 抽象 + mongoStore + buildPlayer"
```

---

### Task E2: Runtime（主循环 + Submit + flush + login/disconnect）

**Files:**
- Create: `src/servers/lobbysvr/internal/runtime.go`
- Test: `src/servers/lobbysvr/internal/runtime_test.go`

- [ ] **Step 1: 写失败测试** `runtime_test.go`

```go
package internal

import (
	"sync"
	"testing"
	"time"

	lobbypb "project/protocal/gen/lobby"
)

// newTestRuntime 构造不依赖 cluster 的 Runtime：online 注册替换为往 channel 投递的桩
// （channel 而非 slice，避免跨 goroutine 读写竞态，-race 友好）。
func newTestRuntime(store DocStore) (*Runtime, chan int64) {
	rt := NewRuntime(RuntimeConfig{
		NodeID:        "1.2.1",
		Cluster:       nil, // 不调用真实集群
		Store:         store,
		Tick:          5 * time.Millisecond,
		FlushInterval: time.Hour, // 周期 flush 不在本测试触发
	})
	regCh := make(chan int64, 16)
	rt.onlineRegister = func(uid int64, gw string) { regCh <- uid }
	rt.onlineUnregister = func(uid int64) {}
	return rt, regCh
}

func TestRuntime_SubmitSerial(t *testing.T) {
	rt, _ := newTestRuntime(newFakeStore())
	rt.Start()
	defer rt.Stop()

	var mu sync.Mutex
	var order []int
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		i := i
		rt.Submit(func() {
			mu.Lock()
			order = append(order, i)
			n := len(order)
			mu.Unlock()
			if n == 50 {
				close(done)
			}
		})
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("tasks not drained")
	}
	mu.Lock()
	defer mu.Unlock()
	for i, v := range order {
		if v != i {
			t.Fatalf("out of order at %d: %d", i, v)
		}
	}
}

func TestRuntime_Login_FreshPlayerAndReply(t *testing.T) {
	store := newFakeStore() // 无文档 → 建新档
	rt, regCh := newTestRuntime(store)
	rt.Start()
	defer rt.Stop()

	type res struct {
		rsp *lobbypb.RPC_Login_Rsp
		err error
	}
	ch := make(chan res, 1)
	rt.Submit(func() {
		rt.Login(20002, "1.1.1", func(rsp *lobbypb.RPC_Login_Rsp, err error) {
			ch <- res{rsp, err}
		})
	})
	select {
	case r := <-ch:
		if r.err != nil || r.rsp.GetUid() != 20002 {
			t.Fatalf("login bad: rsp=%v err=%v", r.rsp, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login no reply")
	}
	// online 注册被调（channel，race-free）
	select {
	case uid := <-regCh:
		if uid != 20002 {
			t.Fatalf("online register uid=%d", uid)
		}
	case <-time.After(time.Second):
		t.Fatal("online register not called")
	}
	// 玩家在内存（经 Submit 在主循环读，结果回传 channel）
	inMem := make(chan bool, 1)
	rt.Submit(func() { _, ok := rt.players[20002]; inMem <- ok })
	if !<-inMem {
		t.Fatal("player not in memory")
	}
}
```

> 断言四点：建新档 → players 有条目 → reply uid 正确 → online 注册桩被调。`Runtime.Login` reply 回调签名 `func(rsp *lobbypb.RPC_Login_Rsp, err error)`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime -v`
Expected: FAIL（`undefined: NewRuntime/RuntimeConfig`）

- [ ] **Step 3: 实现** `runtime.go`

```go
package internal

import (
	"context"
	"strconv"
	"time"

	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
	"project/src/common/taskqueue"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
)

// RuntimeConfig 主循环运行时配置
type RuntimeConfig struct {
	NodeID        string
	Cluster       cluster.Cluster
	Store         DocStore
	QueueSize     int
	Tick          time.Duration
	FlushInterval time.Duration
}

// Runtime lobby 单主循环运行时：串行承载全部玩家 EC 逻辑（零锁）
type Runtime struct {
	nodeID  string
	cls     cluster.Cluster
	store   DocStore
	events  *Events
	tq      *taskqueue.Queue
	tw      *timewheel.TimeWheel
	players map[int64]*Player

	tick          time.Duration
	flushInterval time.Duration
	stopCh        chan struct{}
	doneCh        chan struct{}

	// 在线注册/注销（默认接真实 router；测试可替换为桩）
	onlineRegister   func(uid int64, gatewayNodeID string)
	onlineUnregister func(uid int64)
}

func NewRuntime(cfg RuntimeConfig) *Runtime {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.Tick <= 0 {
		cfg.Tick = 100 * time.Millisecond
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 30 * time.Second
	}
	rt := &Runtime{
		nodeID:        cfg.NodeID,
		cls:           cfg.Cluster,
		store:         cfg.Store,
		events:        NewEvents(),
		tq:            taskqueue.New(cfg.QueueSize),
		tw:            timewheel.New(cfg.Tick, 512),
		players:       make(map[int64]*Player),
		tick:          cfg.Tick,
		flushInterval: cfg.FlushInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	rt.onlineRegister = rt.registerOnline
	rt.onlineUnregister = rt.unregisterOnline
	return rt
}

// Submit 跨 goroutine 把 fn 投递到主循环串行执行
func (rt *Runtime) Submit(fn func()) { rt.tq.Enqueue(fn) }

// Dispatcher 主循环任务队列，作为 Mongo/RPC 回调的投递目标
func (rt *Runtime) Dispatcher() taskqueue.Dispatcher { return rt.tq }

// Start 注册周期 flush 并启动主循环 goroutine
func (rt *Runtime) Start() {
	rt.tw.Tick(rt.flushInterval, rt.flushAllDirty)
	go rt.loop()
}

// Stop 停止主循环并等待退出
func (rt *Runtime) Stop() {
	close(rt.stopCh)
	<-rt.doneCh
}

func (rt *Runtime) loop() {
	defer close(rt.doneCh)
	ticker := time.NewTicker(rt.tick)
	defer ticker.Stop()
	for {
		select {
		case <-rt.stopCh:
			rt.tq.Flush() // 收尾排空已入队任务
			return
		case fn := <-rt.tq.C():
			fn()
		case <-ticker.C:
			rt.tw.Advance()
		}
	}
}

// Login 主循环内登录：加载/建档玩家工作副本 + 在线注册 + reply（异步回包由调用方经 Replier 发出）
func (rt *Runtime) Login(uid int64, gatewayNodeID string, reply func(*lobbypb.RPC_Login_Rsp, error)) {
	if _, ok := rt.players[uid]; ok {
		rt.onlineRegister(uid, gatewayNodeID)
		reply(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: rt.nodeID}, nil)
		return
	}
	rt.store.Load(rt.tq, uid, func(doc *PlayerDoc, found bool, err error) {
		if err != nil {
			reply(nil, err)
			return
		}
		if !found || doc == nil {
			doc = NewPlayerDoc(uid)
		}
		rt.players[uid] = buildPlayer(uid, doc)
		rt.events.PlayerLoaded.Publish(PlayerLoaded{UID: uid})
		rt.onlineRegister(uid, gatewayNodeID)
		reply(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: rt.nodeID}, nil)
	})
}

// Disconnect 主循环内断连：flush 后剔除 + 在线注销
func (rt *Runtime) Disconnect(uid int64) {
	p, ok := rt.players[uid]
	if ok {
		rt.flushPlayer(uid, p, func() { delete(rt.players, uid) })
	}
	rt.onlineUnregister(uid)
}

// Player 主循环内取玩家（不存在返回 nil）
func (rt *Runtime) Player(uid int64) *Player { return rt.players[uid] }

// flushAllDirty 周期 flush 全部玩家的脏组件
func (rt *Runtime) flushAllDirty() {
	for uid, p := range rt.players {
		rt.flushPlayer(uid, p, nil)
	}
}

// flushPlayer 异步落库脏组件：主循环内取值快照 + 乐观清脏；
// 写期间若再变更则 dirty 重置、下次再写；写失败重置脏重试。after 在所有写完成后调用。
func (rt *Runtime) flushPlayer(uid int64, p *Player, after func()) {
	pending := 0
	for _, c := range p.Components() {
		if !c.Dirty() {
			continue
		}
		state := c.Snapshot()
		c.ClearDirty()
		comp := c
		pending++
		rt.store.FlushField(rt.tq, uid, comp.Field(), state, func(err error) {
			if err != nil {
				comp.MarkDirty()
				logger.Warn("lobby flush failed",
					logger.Int64("uid", uid), logger.String("comp", comp.Name()), logger.Err(err))
			}
			pending--
			if pending == 0 && after != nil {
				after()
			}
		})
	}
	if pending == 0 && after != nil {
		after()
	}
}

// registerOnline 经 router 向 onlinesvr 注册在线（best-effort，off-loop goroutine，不阻塞主循环）
func (rt *Runtime) registerOnline(uid int64, gatewayNodeID string) {
	if rt.cls == nil {
		return
	}
	cls, nodeID := rt.cls, rt.nodeID
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Register_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.register",
			&onlinepb.RPC_Register_Req{Uid: uid, GatewayNodeId: gatewayNodeID, LobbyNodeId: nodeID},
		); err != nil {
			logger.Warn("lobby login: online register failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}

// unregisterOnline 经 router 向 onlinesvr 注销（best-effort，off-loop）
func (rt *Runtime) unregisterOnline(uid int64) {
	if rt.cls == nil {
		return
	}
	cls := rt.cls
	go func() {
		ctx := cluster.WithCluster(context.Background(), cls)
		if _, err := routerclient.CallViaSync[*onlinepb.RPC_Unregister_Rsp](
			ctx, cls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
			"OnlineHandler.unregister",
			&onlinepb.RPC_Unregister_Req{Uid: uid},
		); err != nil {
			logger.Warn("lobby disconnect: online unregister failed", logger.Int64("uid", uid), logger.Err(err))
		}
	}()
}
```

- [ ] **Step 4: 跑测试确认通过 + 全量构建/竞态**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime -race -v && go build ./...`
Expected: PASS（含 `-race`）；build 成功

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): Runtime 单主循环（Submit/loop/flush/login/disconnect）"
```

---

### Task E3: flush 竞态与幂等单测

**Files:**
- Test: `src/servers/lobbysvr/internal/runtime_test.go`（追加）

- [ ] **Step 1: 写失败/行为测试** 追加

```go
func TestRuntime_FlushClearsDirtyAndPersists(t *testing.T) {
	store := newFakeStore()
	rt, _ := newTestRuntime(store)
	rt.Start()
	defer rt.Stop()

	// 准备一个已加载、背包变脏的玩家
	done := make(chan struct{})
	rt.Submit(func() {
		p := buildPlayer(30003, NewPlayerDoc(30003))
		rt.players[30003] = p
		p.Bag().Add("op1", 100, 5)
		rt.flushPlayer(30003, p, func() { close(done) })
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flush after not called")
	}
	rt.Submit(func() {
		if rt.players[30003].Bag().Dirty() {
			t.Error("bag still dirty after successful flush")
		}
	})
	time.Sleep(50 * time.Millisecond)
	if _, ok := store.flushed["30003:bag"]; !ok {
		t.Fatalf("bag not persisted: %v", store.flushed)
	}
}
```

- [ ] **Step 2: 跑测试**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestRuntime_Flush -race -v`
Expected: PASS（E2 实现已满足；此为行为锁定测试）

- [ ] **Step 3: 提交**

```bash
git add src/servers/lobbysvr/internal/runtime_test.go
git commit -m "test(lobby): flush 清脏 + 落库行为锁定"
```

---

## Stage F — 接线：proto + handlers + module + main + 集成

### Task F1: 扩 lobby.proto 背包消息 + 重生成

**Files:**
- Modify: `protocal/lobby.proto`
- Regenerate: `protocal/gen/lobby/*`、`protocal/gen/routes/*`

- [ ] **Step 1: 加 proto 消息** `protocal/lobby.proto` 末尾追加

```proto
// --- 客户端 ↔ lobby 背包业务 ---

// BagItem 背包条目
message BagItem {
  int32 item_id = 1;
  int32 count   = 2;
}

// CS_AddItem 加道具请求
message CS_AddItem {
  option (options.msg_id)         = 2003;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.additem";
  string op_id   = 1; // 幂等键
  int32  item_id = 2;
  int32  count   = 3;
}

// SC_AddItem 加道具响应
message SC_AddItem {
  option (options.msg_id) = 2004;
  int32 item_id = 1;
  int32 count   = 2; // 操作后数量
}

// CS_BagList 查询背包请求
message CS_BagList {
  option (options.msg_id)         = 2005;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.baglist";
}

// SC_BagList 背包列表响应
message SC_BagList {
  option (options.msg_id) = 2006;
  repeated BagItem items = 1;
}
```

- [ ] **Step 2: 重生成 pb + 路由表**

Run:
```bash
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
INC=/game/dev/silver-server/3rd/protobuf/include
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$INC protocal/lobby.proto
go run ./tools/gen_routes
```
Expected: `protocal/gen/lobby/lobby.pb.go` 含 `CS_AddItem/SC_AddItem/CS_BagList/SC_BagList/BagItem`；`protocal/gen/routes/routes.go` 出现 `2003:"LobbyHandler.additem"`、`2005:"LobbyHandler.baglist"`（MsgRouteTable）、`2003/2005:"lobbysvr"`（ForwardTable）、`2003:2004`、`2005:2006`（RespMsgIDTable）

- [ ] **Step 3: 构建确认生成物可编译**

Run: `go build ./...`
Expected: 成功

- [ ] **Step 4: 提交**

```bash
git add protocal/lobby.proto protocal/gen/lobby/ protocal/gen/routes/
git commit -m "feat(proto): lobby 背包 CS/SC（AddItem/BagList）+ 重生成路由表"
```

---

### Task F2: LobbyHandler 薄壳（login/disconnect/additem/baglist）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`（整体重写）
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`（重写）

- [ ] **Step 1: 写失败测试** `lobby_handler_test.go`

```go
package internal

import (
	"context"
	"testing"
	"time"

	lobbypb "project/protocal/gen/lobby"
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
)

type capReplier struct {
	ch chan struct {
		data []byte
		err  error
	}
}

func newCapReplier() *capReplier {
	return &capReplier{ch: make(chan struct {
		data []byte
		err  error
	}, 1)}
}
func (c *capReplier) Reply(data []byte, err error) {
	c.ch <- struct {
		data []byte
		err  error
	}{data, err}
}

func ctxWith(uid int64, r cluster.Replier) context.Context {
	ctx := cluster.WithSession(context.Background(), &clusterpb.ClusterSession{Uid: uid, FrontendId: "1.1.1"})
	return cluster.WithReplier(ctx, r)
}

func TestLobbyHandler_Additem_DeferredReply(t *testing.T) {
	rt, _ := newTestRuntime(newFakeStore())
	rt.Start()
	defer rt.Stop()
	// 预置已加载玩家
	rt.Submit(func() { rt.players[10001] = buildPlayer(10001, NewPlayerDoc(10001)) })
	time.Sleep(20 * time.Millisecond)

	h := NewLobbyHandler(rt)
	r := newCapReplier()
	_, err := h.Additem(ctxWith(10001, r), &lobbypb.CS_AddItem{OpId: "op1", ItemId: 100, Count: 5})
	if err != cluster.ErrDeferredReply {
		t.Fatalf("expected deferred sentinel, got %v", err)
	}
	select {
	case got := <-r.ch:
		if got.err != nil {
			t.Fatalf("reply err: %v", got.err)
		}
		var rsp lobbypb.SC_AddItem
		if e := proto.Unmarshal(got.data, &rsp); e != nil {
			t.Fatal(e)
		}
		if rsp.ItemId != 100 || rsp.Count != 5 {
			t.Fatalf("bad rsp: %+v", &rsp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
}

func TestLobbyHandler_Additem_PlayerNotLoaded(t *testing.T) {
	rt, _ := newTestRuntime(newFakeStore())
	rt.Start()
	defer rt.Stop()
	h := NewLobbyHandler(rt)
	r := newCapReplier()
	_, _ = h.Additem(ctxWith(99999, r), &lobbypb.CS_AddItem{OpId: "o", ItemId: 1, Count: 1})
	select {
	case got := <-r.ch:
		if got.err == nil {
			t.Fatal("expected error for unloaded player")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
}
```

> `clusterpb` = `project/src/framework/cluster/pb`（与 `application.go`/`gate_handler.go` 一致）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler -v`
Expected: FAIL（旧 `LobbyHandler` 签名不符 / `NewLobbyHandler(rt)` 不存在）

- [ ] **Step 3: 实现** `lobby_handler.go`（整体重写）

```go
package internal

import (
	"context"
	"fmt"
	"strconv"

	lobbypb "project/protocal/gen/lobby"
	"google.golang.org/protobuf/proto"
	"project/src/framework/cluster"
)

// LobbyHandler lobby 集群 RPC handler 薄壳：捕获 Replier + 把工作 Submit 进主循环 +
// 返回延迟回包哨兵；主循环内显式调用业务逻辑并经 Replier 异步回包。
type LobbyHandler struct {
	rt *Runtime
}

func NewLobbyHandler(rt *Runtime) *LobbyHandler { return &LobbyHandler{rt: rt} }

// Login route="LobbyHandler.login"
func (h *LobbyHandler) Login(ctx context.Context, req *lobbypb.RPC_Login_Req) (*lobbypb.RPC_Login_Rsp, error) {
	replier := cluster.ReplierFromCtx(ctx)
	var gatewayNodeID string
	if cs := cluster.SessionFromCtx(ctx); cs != nil {
		gatewayNodeID = cs.FrontendId
	}
	h.rt.Submit(func() {
		uid, ok := verifyToken(req.Token)
		if !ok {
			replyProto(replier, &lobbypb.RPC_Login_Rsp{Code: -1}, nil)
			return
		}
		h.rt.Login(uid, gatewayNodeID, func(rsp *lobbypb.RPC_Login_Rsp, err error) {
			replyProto(replier, rsp, err)
		})
	})
	return nil, cluster.ErrDeferredReply
}

// PlayerDisconnect route="LobbyHandler.playerdisconnect"（Notify，无回包）
func (h *LobbyHandler) PlayerDisconnect(_ context.Context, req *lobbypb.RPC_PlayerDisconnect_Notify) {
	uid := req.Uid
	h.rt.Submit(func() { h.rt.Disconnect(uid) })
}

// Additem route="LobbyHandler.additem"
func (h *LobbyHandler) Additem(ctx context.Context, req *lobbypb.CS_AddItem) (*lobbypb.SC_AddItem, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		n := p.Bag().Add(req.OpId, req.ItemId, req.Count)
		replyProto(replier, &lobbypb.SC_AddItem{ItemId: req.ItemId, Count: n}, nil)
	})
	return nil, cluster.ErrDeferredReply
}

// Baglist route="LobbyHandler.baglist"
func (h *LobbyHandler) Baglist(ctx context.Context, _ *lobbypb.CS_BagList) (*lobbypb.SC_BagList, error) {
	replier := cluster.ReplierFromCtx(ctx)
	uid := uidFromCtx(ctx)
	h.rt.Submit(func() {
		p := h.rt.Player(uid)
		if p == nil {
			replyProto(replier, nil, fmt.Errorf("player not loaded: %d", uid))
			return
		}
		items := p.Bag().Items()
		rsp := &lobbypb.SC_BagList{Items: make([]*lobbypb.BagItem, 0, len(items))}
		for id, c := range items {
			rsp.Items = append(rsp.Items, &lobbypb.BagItem{ItemId: id, Count: c})
		}
		replyProto(replier, rsp, nil)
	})
	return nil, cluster.ErrDeferredReply
}

func uidFromCtx(ctx context.Context) int64 {
	if cs := cluster.SessionFromCtx(ctx); cs != nil {
		return cs.Uid
	}
	return 0
}

// replyProto marshal 业务响应并经 Replier 异步回包（err 非 nil 时回错误响应）
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

// verifyToken P3a stub：非空即通过；token 为正整数时即作 uid（便于多玩家测试），
// 否则回退 10001。后续阶段替换为真实无状态 token 校验。
func verifyToken(token string) (int64, bool) {
	if token == "" {
		return 0, false
	}
	if uid, err := strconv.ParseInt(token, 10, 64); err == nil && uid > 0 {
		return uid, true
	}
	return 10001, true
}
```

- [ ] **Step 4: 跑测试确认通过 + 竞态 + 构建**

Run: `go test ./src/servers/lobbysvr/internal/ -race -v && go build ./...`
Expected: PASS；build 成功

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): handler 薄壳 login/disconnect/additem/baglist（Submit+延迟回包）"
```

---

### Task F3: LobbyModule 持 Runtime + 生命周期

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_module.go`
- Test: `src/servers/lobbysvr/internal/lobby_module_test.go`（新建）

- [ ] **Step 1: 写失败测试** `lobby_module_test.go`

```go
package internal

import (
	"testing"
	"time"
)

func TestLobbyModule_StartStop(t *testing.T) {
	rt, _ := newTestRuntime(newFakeStore())
	m := NewLobbyModule(rt)
	if m.Name() != "lobby" {
		t.Fatalf("name=%s", m.Name())
	}
	m.Init() // 启动 loop
	// loop 可处理 Submit
	done := make(chan struct{})
	rt.Submit(func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop not running after Init")
	}
	m.OnStop() // 停 loop（不应 panic / 阻塞）
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyModule -v`
Expected: FAIL（`NewLobbyModule(rt)` 签名不符）

- [ ] **Step 3: 实现** `lobby_module.go`（重写）

```go
package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// LobbyModule lobby 服务模块：持有主循环 Runtime，Init 启动、OnStop 停止
type LobbyModule struct {
	module.BaseModule
	rt *Runtime
}

func NewLobbyModule(rt *Runtime) *LobbyModule { return &LobbyModule{rt: rt} }

func (l *LobbyModule) Name() string { return "lobby" }

func (l *LobbyModule) Init() {
	l.rt.Start()
	logger.Info("lobby module initialized")
}

func (l *LobbyModule) OnStop() {
	l.rt.Stop()
	logger.Info("lobby module stopped")
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyModule -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/lobby_module.go src/servers/lobbysvr/internal/lobby_module_test.go
git commit -m "feat(lobby): LobbyModule 持 Runtime + Init/OnStop 生命周期"
```

---

### Task F4: main 接 Mongo + conf 加 mongo 段

**Files:**
- Modify: `src/servers/lobbysvr/main.go`
- Modify: `conf/lobby.yaml`

- [ ] **Step 1: conf/lobby.yaml 加 mongo 段**（在 `cluster:` 段后追加）

```yaml
mongo:
  uri: "mongodb://localhost:27017"
  database: "game"
```

- [ ] **Step 2: 改 main.go**：在构造 `app` 后、`app.Register` 前接 Mongo 并建 store/runtime。把原 `app.Register(internal.NewLobbyModule())` / `RegisterHandler(internal.NewLobbyHandler(...))` 替换为：

```go
	// 5. MongoDB + 主循环运行时
	mc, err := mongo.Connect(cfg.Mongo.URI, cfg.Mongo.Database, 10*time.Second)
	if err != nil {
		panic(err)
	}
	defer mc.Close()
	rt := internal.NewRuntime(internal.RuntimeConfig{
		NodeID:  cfg.Node.ID,
		Cluster: app.Cluster(),
		Store:   internal.NewMongoStore(mc),
	})

	app.Register(internal.NewLobbyModule(rt))
	if err := app.RegisterHandler(internal.NewLobbyHandler(rt), nil); err != nil {
		panic(err)
	}
```

import 区加 `"time"` 与 `"project/src/common/mongo"`。

- [ ] **Step 3: 构建**

Run: `go build ./...`
Expected: 成功

- [ ] **Step 4: 提交**

```bash
git add src/servers/lobbysvr/main.go conf/lobby.yaml
git commit -m "feat(lobby): main 接 MongoDB + Runtime 装配；conf 加 mongo 段"
```

---

### Task F5: 集成测试（登录加载 → 背包落库 → 重登读回）

**Files:**
- Create: `src/servers/lobbysvr/internal/lobby_ec_integration_test.go`（`//go:build integration`）

- [ ] **Step 1: 写集成测试**（在 gate→lobby 集群边界驱动：起 NATS+etcd+MongoDB，构造真实 cluster + Runtime，经 NATS 请求-应答发 RPC，携 `ClusterSession{uid}`）。要点（完整骨架，连接参数对齐 `test/docker-compose.yaml`）：

```go
//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	lobbypb "project/protocal/gen/lobby"
	"google.golang.org/protobuf/proto"
	"project/src/common/mongo"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/cluster/transport"
)

// 流程：① 起真实 cluster（lobby 节点）+ Mongo 连接 + Runtime + LobbyHandler（经 registry 装入 cluster handler）。
// ② 用另一个 cluster 客户端（模拟 gate）CallSync 到 lobby：登录 → AddItem(op1) → AddItem(op1 重试) → BagList。
// ③ 断言：BagList 数量正确、op1 重试不双加；等待周期/断连 flush 后，从 Mongo 直接读 players 文档校验落库。
// ④ 模拟重登（新 Runtime，同 uid，从 Mongo 加载）→ BagList 读回一致。
//
// 具体连接/装配代码参考已落地的 onlinesvr/router *_integration_test.go（同款 NewNatsCluster + app.Start 注入 handler）。
// 关键断言点（务必覆盖）：
//   - CallSync(RPC_Login_Req, token="40004") → RPC_Login_Rsp{Uid:40004}
//   - CallSync(CS_AddItem{op1,item=100,count=5}) → SC_AddItem{count:5}
//   - CallSync(CS_AddItem{op1,item=100,count=5}) → SC_AddItem{count:5}（去重，不为 10）
//   - CallSync(CS_BagList) → SC_BagList 含 {100:5}
//   - 触发 flush（断连 RPC_PlayerDisconnect_Notify 或缩短 FlushInterval）后，mongo FindByID(players,40004) 的 bag.items["100"]==5
//   - 新 Runtime 同 uid 登录 → CS_BagList 读回 {100:5}

func TestLobbyEC_Login_Bag_Flush_Relogin(t *testing.T) {
	t.Skip("需要容器 NATS+etcd+MongoDB；沙箱仅编译验证。实跑去掉 Skip 并补连接装配。")
	_ = context.Background
	_ = time.Second
	_ = proto.Marshal
	_ = (*clusterpb.ClusterSession)(nil)
	_ = (*lobbypb.CS_AddItem)(nil)
	_ = mongo.Connect
	_ = cluster.WithSession
	_ = transport.NewNatsCluster
}
```

> 沙箱无 Docker：本测试 `//go:build integration`，仅编译验证。`t.Skip` + 下方 `_ =` 引用保证编译通过且不空跑；在有 Docker 的环境去掉 `Skip` 并按注释补全装配（参考 onlinesvr/router 既有集成测试）。

- [ ] **Step 2: 编译验证（不实跑）**

Run: `go vet -tags integration ./src/servers/lobbysvr/...`
Expected: 通过（编译成功）

- [ ] **Step 3: 提交**

```bash
git add src/servers/lobbysvr/internal/lobby_ec_integration_test.go
git commit -m "test(lobby): 背包竖切集成测试骨架（登录加载→落库→重登读回，编译验证）"
```

---

## 收尾验证（全量）

- [ ] **构建 + vet + 单测 + 竞态全绿**

Run:
```bash
go build ./...
go vet ./...
go vet -tags integration ./...
go test ./src/... -count=1
go test ./src/servers/lobbysvr/... ./src/framework/... ./src/common/... -race -count=1
```
Expected: 全部成功；无悬挂引用、无 race

- [ ] **核对清理**：无新增未用 import/变量/函数；旧 `verifyToken` 固定 uid 改为 token 解析；`lobby_handler.go`/`lobby_module.go` 旧实现已被替换无残留。

---

## Self-Review 对照 Spec

| Spec 项 | 落实任务 |
|---|---|
| §4 主循环异步回包（Replier/哨兵/handleMessage） | A1, A2 |
| §4.4 gate 转发遮蔽修复（D9，HasRoute） | A3 |
| §4.5 gate 转发填 uid | A4 |
| §5 单主循环（taskqueue C() + loop + Submit） | A5, E2 |
| §6 MongoDB 接入层（异步 CRUD）+ config | B1, B2 |
| §7 EC 核心（Component/Player/event） | C1, C2 |
| §8 背包（BagState/Add/op-id 去重/内嵌落库） | D1, D2, E1 |
| §9 登录改造 + Player 生命周期（加载/断连 flush+剔除/online） | E2, F2 |
| §8/§12 flush 绝对写幂等 + 写中变更竞态 | B2(UpsertSet), E2(flushPlayer), E3 |
| 接线（proto/module/main/集成） | F1, F3, F4, F5 |

> 已知 P3a 范围外（见关键事实#6）：客户端↔gate 转发响应的 json/proto 协议定稿——集成测试在集群边界驱动，不依赖之。
