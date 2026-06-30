# P1 行走骨架：gateway→lobby 登录纵切 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打通"客户端 → gateway → (NATS 集群 RPC) → lobbysvr → 校验 token 返回 uid+节点 → gateway 绑定会话并回包"的端到端登录链路，作为新架构的第一个可运行可测里程碑。

**Architecture:** gateway 保持前端连接层（json 序列化），登录请求 `CS_Login` 仍由 gateway 本地 handler 接收，但 handler 不再本地校验，而是通过 `cluster.CallAnySync` 把登录转发给任一 lobbysvr；lobbysvr 是新建后端服务（protobuf 序列化），其 `LobbyHandler.Login` 校验 token（P1 stub）并返回 uid 与自身节点 ID；gateway 据响应 `Bind(uid)` + `BindNode("lobbysvr", nodeID)`。两端都接入真实 `NatsCluster`（现有 gatesvr 用的是 noopCluster，本计划改为 NATS）。

**Tech Stack:** Go 1.26、NATS（集群 RPC）、etcd（服务发现）、protobuf（集群消息）、现有 `src/framework`（application/handler/cluster/session）。集成测试依赖容器化 NATS+etcd。

**前置说明（务必先读）：**
- 集群 RPC 在发送端硬编码 `proto.Marshal`（见 `transport/nats_cluster.go`），故 **lobbysvr 序列化器必须用 protobuf**；gatesvr 客户端侧仍用 json。
- `Application` **不管理** cluster 生命周期，须在 main 里 `cls.Init()` / `defer cls.Stop()`。统一顺序：`app.Start()`（注入集群 handler）→ `cls.Init()`（订阅+注册）→ `app.Run()`，避免 nil-handler 窗口。
- gateway 采用同样顺序，存在亚秒级启动竞态（acceptor 先于 discovery 就绪）：极早登录会因"no nodes found"失败，客户端重试即可；集成测试通过 `waitForType` 规避。

---

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `protocal/lobby.proto` | lobby 集群 RPC 协议（P1 仅登录） | 新建 |
| `protocal/gen/lobby/lobby.pb.go` | 生成的 Go 类型 | 生成 |
| `protocal/gen/routes/routes.go` | 路由表（含 lobby 条目） | 重新生成 |
| `src/servers/lobbysvr/internal/lobby_handler.go` | `LobbyHandler.Login`（校验 token stub） | 新建 |
| `src/servers/lobbysvr/internal/lobby_handler_test.go` | LobbyHandler 单测 | 新建 |
| `src/servers/lobbysvr/internal/lobby_module.go` | lobby 服务模块（生命周期占位） | 新建 |
| `src/servers/lobbysvr/main.go` | lobbysvr 入口（protobuf + NatsCluster + 生命周期） | 新建 |
| `conf/lobby.yaml` | lobbysvr 配置（node 1.2.1） | 新建 |
| `src/framework/handler/registry.go` | 新增导出 `WithSessionID`（测试可注入 sessionID） | 修改 |
| `src/servers/gatesvr/internal/gate_handler.go` | `Login` 改为转发 lobby + 绑定 | 修改 |
| `src/servers/gatesvr/internal/gate_handler_test.go` | gateway 登录单测（fakeCluster） | 新建 |
| `src/servers/gatesvr/main.go` | 接入 NatsCluster + 生命周期 | 修改 |
| `test/docker-compose.yaml` | 集成测试用 NATS+etcd | 新建 |
| `test/integration/login_test.go` | 端到端登录 RPC 集成测试（build tag） | 新建 |

---

## Task 1：lobby.proto 与生成代码

**Files:**
- Create: `protocal/lobby.proto`
- Generate: `protocal/gen/lobby/lobby.pb.go`, `protocal/gen/routes/routes.go`

- [ ] **Step 1: 写 lobby.proto**

创建 `protocal/lobby.proto`：

```proto
syntax = "proto3";

package lobby;

option go_package = "project/protocal/gen/lobby";

import "protocal/options.proto";

// --- gateway ↔ lobby 集群 RPC（RPC_ 前缀，服务间调用） ---

// RPC_Login_Req gateway 转发给 lobby 的登录请求
message RPC_Login_Req {
  option (options.msg_id)         = 2001;
  option (options.server_type)    = "lobbysvr";
  option (options.handler_method) = "LobbyHandler.login";
  string token    = 1;
  string platform = 2;
}

// RPC_Login_Rsp lobby 返回给 gateway 的登录响应
message RPC_Login_Rsp {
  option (options.msg_id) = 2002;
  int32  code          = 1; // 0=成功，负数=失败
  int64  uid           = 2;
  string lobby_node_id = 3; // 处理本次登录的 lobby 节点 ID（点分），gateway 据此绑定
}
```

- [ ] **Step 2: 生成 Go 代码与路由表**

Run: `./tools/gen_proto.sh protocal/lobby.proto`
Expected: 输出 `generating protocal/lobby.proto ...` 与 `regenerating route tables ...`，`generated N message routes → protocal/gen/routes/routes.go`（N 含新增 2001/2002）。

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 无错误（`protocal/gen/lobby` 包生成、routes.go 更新且可编译）。

- [ ] **Step 4: 提交**

```bash
git add protocal/lobby.proto protocal/gen/lobby/ protocal/gen/routes/routes.go
git commit -m "feat(proto): 新增 lobby.proto 登录 RPC（RPC_Login_Req/Rsp）"
```

---

## Task 2：lobbysvr 的 LobbyHandler 与单测

**Files:**
- Create: `src/servers/lobbysvr/internal/lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

创建 `src/servers/lobbysvr/internal/lobby_handler_test.go`：

```go
package internal

import (
	"context"
	"testing"

	lobbypb "project/protocal/gen/lobby"
)

func TestLobbyHandler_Login_OK(t *testing.T) {
	h := NewLobbyHandler("1.2.1")
	rsp, err := h.Login(context.Background(), &lobbypb.RPC_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 || rsp.LobbyNodeId != "1.2.1" {
		t.Fatalf("unexpected rsp: %+v", rsp)
	}
}

func TestLobbyHandler_Login_EmptyToken(t *testing.T) {
	h := NewLobbyHandler("1.2.1")
	rsp, err := h.Login(context.Background(), &lobbypb.RPC_Login_Req{Token: ""})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code == 0 {
		t.Fatalf("expected non-zero code for empty token, got %+v", rsp)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler -v`
Expected: 编译失败（`NewLobbyHandler` / `Login` 未定义）。

- [ ] **Step 3: 写实现**

创建 `src/servers/lobbysvr/internal/lobby_handler.go`：

```go
package internal

import (
	"context"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/logger"
)

// LobbyHandler 处理 lobby 的集群 RPC（P1：仅登录）
type LobbyHandler struct {
	nodeID string // 本 lobby 节点 ID（点分格式），登录响应里回给 gateway 用于绑定
}

func NewLobbyHandler(nodeID string) *LobbyHandler {
	return &LobbyHandler{nodeID: nodeID}
}

// Login 处理 gateway 转来的登录 RPC（route = "LobbyHandler.login"）。
// P1 stub：非空 token 即通过，返回固定 uid 与本节点 ID。
func (h *LobbyHandler) Login(_ context.Context, req *lobbypb.RPC_Login_Req) (*lobbypb.RPC_Login_Rsp, error) {
	uid, ok := verifyToken(req.Token)
	if !ok {
		logger.Warn("lobby login: invalid token", logger.String("platform", req.Platform))
		return &lobbypb.RPC_Login_Rsp{Code: -1}, nil
	}
	logger.Info("lobby login ok",
		logger.Int64("uid", uid),
		logger.String("node", h.nodeID))
	return &lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: h.nodeID}, nil
}

// verifyToken P1 stub：非空即通过，uid 占位为 10001。
// 后续阶段替换为真实无状态 token 校验（外部账号系统签发）。
func verifyToken(token string) (int64, bool) {
	if token == "" {
		return 0, false
	}
	return 10001, true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyHandler -v`
Expected: PASS（两个用例）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobbysvr): LobbyHandler.Login 登录 RPC（stub 校验）+ 单测"
```

---

## Task 3：lobbysvr 模块、入口与配置

**Files:**
- Create: `src/servers/lobbysvr/internal/lobby_module.go`
- Create: `src/servers/lobbysvr/main.go`
- Create: `conf/lobby.yaml`

- [ ] **Step 1: 写 LobbyModule**

创建 `src/servers/lobbysvr/internal/lobby_module.go`：

```go
package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// LobbyModule lobby 服务模块，P1 仅占位生命周期日志，后续承载 EC 与玩家状态
type LobbyModule struct {
	module.BaseModule
}

func NewLobbyModule() *LobbyModule { return &LobbyModule{} }

func (m *LobbyModule) Name() string { return "lobby" }

func (m *LobbyModule) Init() {
	logger.Info("lobby module initialized")
}
```

- [ ] **Step 2: 写 main.go**

创建 `src/servers/lobbysvr/main.go`：

```go
package main

import (
	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/lobbysvr/internal"
)

func main() {
	// 1. 配置
	cfg := config.MustLoad("conf/lobby.yaml")

	// 2. 日志
	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	// 3. 构造 NATS 集群（后端节点）
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

	// 4. Application：后端不调 Frontend()；集群 RPC 走 proto，序列化器必须 protobuf
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).
		Build()

	app.Register(internal.NewLobbyModule())
	if err := app.RegisterHandler(internal.NewLobbyHandler(cfg.Node.ID), nil); err != nil {
		panic(err)
	}

	// 5. 生命周期：Start() 先注入集群 handler，再 Init() 订阅+注册（避免 nil-handler 窗口）
	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	logger.Info("lobbysvr started", logger.String("nodeID", cfg.Node.ID))
	app.Run()
}
```

- [ ] **Step 3: 写 conf/lobby.yaml**

创建 `conf/lobby.yaml`：

```yaml
# lobbysvr 节点（worldID=1, serverTypeID=2, index=1）
node:
  id: "1.2.1"
  server_type_name: "lobbysvr"
  addr: "0.0.0.0:8801"

net:
  heartbeat_sec: 30
  shutdown_timeout_sec: 10

cluster:
  etcd:
    endpoints:
      - "localhost:2379"
  nats:
    urls:
      - "nats://localhost:4222"

log:
  level: "debug"
  dir: "./logs"
```

- [ ] **Step 4: 验证编译**

Run: `go build ./...`
Expected: 无错误（`src/servers/lobbysvr/...` 编译通过）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/lobby_module.go src/servers/lobbysvr/main.go conf/lobby.yaml
git commit -m "feat(lobbysvr): 模块/入口/配置（protobuf + NatsCluster + 生命周期）"
```

---

## Task 4：框架增加 WithSessionID + gateway 登录改造（含单测）

**Files:**
- Modify: `src/framework/handler/registry.go`（新增导出 `WithSessionID`）
- Modify: `src/servers/gatesvr/internal/gate_handler.go`
- Test: `src/servers/gatesvr/internal/gate_handler_test.go`

- [ ] **Step 1: 在 registry.go 新增导出 WithSessionID**

`src/servers/gatesvr/internal` 包无法访问 handler 包的未导出 ctx key，单测需要可注入 sessionID。在 `src/framework/handler/registry.go` 中 `injectSession` 函数下方新增：

```go
// WithSessionID 注入 sessionID 到 ctx，供自定义注入点与测试使用。
// 生产路径由 injectSession 自动注入；本函数让外部能构造带 sessionID 的 ctx。
func WithSessionID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxSessionIDKey{}, id)
}
```

Run: `go build ./src/framework/handler/`
Expected: 无错误。

- [ ] **Step 2: 写失败测试**

创建 `src/servers/gatesvr/internal/gate_handler_test.go`：

```go
package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	"project/src/framework/cluster"
	"project/src/framework/handler"
	"project/src/framework/session"
)

// fakeCluster 嵌入 noopCluster，仅覆写 CallAnySync 返回预设响应
type fakeCluster struct {
	cluster.Cluster
	rspData []byte
	err     error
}

func (f *fakeCluster) CallAnySync(_ context.Context, _ string, _ string, _ proto.Message) ([]byte, error) {
	return f.rspData, f.err
}

func TestGateHandler_Login_BindsLobby(t *testing.T) {
	lobbyRsp, _ := proto.Marshal(&lobbypb.RPC_Login_Rsp{Code: 0, Uid: 10001, LobbyNodeId: "1.2.1"})
	fc := &fakeCluster{Cluster: cluster.NewNoopCluster(), rspData: lobbyRsp}

	mgr := session.NewManager()
	m := NewGateModule(mgr, fc)
	h := NewGateHandler(m)

	s := mgr.New("127.0.0.1")
	ctx := handler.WithSessionID(context.Background(), s.ID())

	rsp, err := h.Login(ctx, &gatepb.CS_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 {
		t.Fatalf("unexpected rsp: %+v", rsp)
	}
	if !s.IsBound() || s.UID() != 10001 {
		t.Fatalf("session not bound: bound=%v uid=%d", s.IsBound(), s.UID())
	}
	if node, ok := s.BoundNode("lobbysvr"); !ok || node != "1.2.1" {
		t.Fatalf("lobby node not bound: ok=%v node=%q", ok, node)
	}
}

func TestGateHandler_Login_LobbyRejects(t *testing.T) {
	lobbyRsp, _ := proto.Marshal(&lobbypb.RPC_Login_Rsp{Code: -1})
	fc := &fakeCluster{Cluster: cluster.NewNoopCluster(), rspData: lobbyRsp}

	mgr := session.NewManager()
	h := NewGateHandler(NewGateModule(mgr, fc))
	s := mgr.New("127.0.0.1")
	ctx := handler.WithSessionID(context.Background(), s.ID())

	rsp, _ := h.Login(ctx, &gatepb.CS_Login_Req{Token: "bad"})
	if rsp.Code == 0 {
		t.Fatalf("expected rejection, got code 0")
	}
	if s.IsBound() {
		t.Fatalf("session should not be bound on rejection")
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./src/servers/gatesvr/internal/ -run TestGateHandler_Login -v`
Expected: 失败 —— 当前 `Login` 不调用 cluster、未绑定 `lobbysvr`，`BoundNode("lobbysvr")` 断言失败（或编译失败，因尚未 import lobbypb）。

- [ ] **Step 4: 改写 gate_handler.go 的 Login**

把 `src/servers/gatesvr/internal/gate_handler.go` 的 import 与 `Login` 改为如下，并删除 `verifyToken` / `assignBackendNodes` 两个 stub 方法：

import 块改为：

```go
import (
	"context"

	"google.golang.org/protobuf/proto"

	gatepb "project/protocal/gen/gate"
	lobbypb "project/protocal/gen/lobby"
	"project/src/common/logger"
	"project/src/framework/handler"
)
```

> 注：删除原先对 `project/src/framework/session` 的 import（`Login` 改造后 `Logout` 仍用到 `Sessions()`，但不直接引用 session 包类型；若 `go build` 报 session 未使用则删除，报已使用则保留）。

`Login` 方法替换为：

```go
// Login 处理登录请求（msgID=1001，Request，有返回）。
// 转发给任一 lobbysvr 校验，成功后绑定 uid 与归属 lobby 节点。
func (h *GateHandler) Login(ctx context.Context, req *gatepb.CS_Login_Req) (*gatepb.SC_Login_Rsp, error) {
	sessionID := handler.SessionIDFromCtx(ctx)
	s, ok := h.comp.Sessions().ByID(sessionID)
	if !ok {
		return &gatepb.SC_Login_Rsp{Code: -1, Message: "session not found"}, nil
	}

	// 转发登录到任一 lobby（集群 RPC，同步等结果）
	data, err := h.comp.Cluster().CallAnySync(ctx, "lobbysvr", "LobbyHandler.login",
		&lobbypb.RPC_Login_Req{Token: req.Token, Platform: req.Platform})
	if err != nil {
		logger.Warn("login: call lobby failed", logger.Err(err))
		return &gatepb.SC_Login_Rsp{Code: -2, Message: "login service unavailable"}, nil
	}

	var lrsp lobbypb.RPC_Login_Rsp
	if err := proto.Unmarshal(data, &lrsp); err != nil {
		logger.Warn("login: decode lobby rsp failed", logger.Err(err))
		return &gatepb.SC_Login_Rsp{Code: -2, Message: "login decode error"}, nil
	}
	if lrsp.Code != 0 {
		return &gatepb.SC_Login_Rsp{Code: lrsp.Code, Message: "login rejected"}, nil
	}

	// 绑定 uid 与归属 lobby 节点（后续该连接消息转发到此 lobby）
	if err := h.comp.Sessions().Bind(ctx, s, lrsp.Uid); err != nil {
		return &gatepb.SC_Login_Rsp{Code: -3, Message: err.Error()}, nil
	}
	s.BindNode("lobbysvr", lrsp.LobbyNodeId)

	logger.Info("player logged in",
		logger.Int64("uid", lrsp.Uid),
		logger.String("lobby", lrsp.LobbyNodeId),
		logger.String("platform", req.Platform))
	return &gatepb.SC_Login_Rsp{Code: 0, Uid: lrsp.Uid}, nil
}
```

> 同时删除文件末尾的 `verifyToken` 与 `assignBackendNodes` 方法（已不再使用）。`Heartbeat` / `Logout` 保持不变。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./src/servers/gatesvr/internal/ -run TestGateHandler_Login -v`
Expected: PASS（两个用例）。

- [ ] **Step 6: 全量编译**

Run: `go build ./... && go test ./src/... -count=1`
Expected: 无错误；既有测试与新单测全绿（不依赖 NATS/etcd 的部分）。

- [ ] **Step 7: 提交**

```bash
git add src/framework/handler/registry.go src/servers/gatesvr/internal/gate_handler.go src/servers/gatesvr/internal/gate_handler_test.go
git commit -m "feat(gatesvr): 登录改为转发 lobbysvr 并绑定会话；framework 增加 handler.WithSessionID"
```

---

## Task 5：gatesvr 入口接入 NatsCluster

**Files:**
- Modify: `src/servers/gatesvr/main.go`

- [ ] **Step 1: 改写 main.go**

将 `src/servers/gatesvr/main.go` 整体替换为：

```go
package main

import (
	"project/protocal/gen/routes"
	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/json"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/gatesvr/internal"
)

func main() {
	// 1. 配置
	cfg := config.MustLoad("conf/server.yaml")

	// 2. 日志
	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	// 3. 构造 NATS 集群（前端节点也接入，用于转发到后端）
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

	// 4. Application：前端节点，客户端侧 json 序列化
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Frontend(cfg.Node.Addr).
		Serializer("json", json.NewSerializer()).
		Routes(routes.Config()).
		Cluster(cls).
		Build()

	gateModule := internal.NewGateModule(app.Sessions(), app.Cluster())
	app.Register(gateModule)
	if err := app.RegisterHandler(internal.NewGateHandler(gateModule), nil); err != nil {
		panic(err)
	}

	// 5. 生命周期：Start() 注入集群 handler + 启动 acceptor → Init() 连接订阅
	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	logger.Info("gatesvr started",
		logger.String("nodeID", cfg.Node.ID),
		logger.String("addr", cfg.Node.Addr))
	app.Run()
}
```

- [ ] **Step 2: 编译**

Run: `go build ./...`
Expected: 无错误。

- [ ] **Step 3: 提交**

```bash
git add src/servers/gatesvr/main.go
git commit -m "feat(gatesvr): 入口接入 NatsCluster（替换 noopCluster）并管理其生命周期"
```

---

## Task 6：端到端集成测试（NATS+etcd 起 lobby，RPC 调 login）

**Files:**
- Create: `test/docker-compose.yaml`
- Create: `test/integration/login_test.go`

> 本测试验证：真实 NATS+etcd 下，lobbysvr 注册可被发现、其 `LobbyHandler.login` 经集群 RPC 可达、proto 编解码端到端正确。gateway 的登录逻辑已在 Task 4 单测覆盖，此处不重复 TCP/握手层。

- [ ] **Step 1: 写 docker-compose**

创建 `test/docker-compose.yaml`：

```yaml
services:
  nats:
    image: nats:2.10
    ports:
      - "4222:4222"
  etcd:
    image: quay.io/coreos/etcd:v3.5.13
    environment:
      - ETCD_ADVERTISE_CLIENT_URLS=http://0.0.0.0:2379
      - ETCD_LISTEN_CLIENT_URLS=http://0.0.0.0:2379
    ports:
      - "2379:2379"
```

- [ ] **Step 2: 写集成测试**

创建 `test/integration/login_test.go`：

```go
//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	lobbyinternal "project/src/servers/lobbysvr/internal"
)

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

func TestLoginRPC_EndToEnd(t *testing.T) {
	// 1. 起 lobbysvr（节点 1.2.1）
	lobbyID, err := cluster.ParseNodeID("1.2.1")
	if err != nil {
		t.Fatalf("parse lobby id: %v", err)
	}
	lobbyCls, err := transport.NewNatsCluster(lobbyID, transport.NatsClusterConfig{
		EtcdEndpoints:  etcdEndpoints,
		NatsURLs:       natsURLs,
		SelfAddr:       "127.0.0.1:8801",
		ServerTypeName: "lobbysvr",
	})
	if err != nil {
		t.Fatalf("lobby cluster: %v", err)
	}
	lobbyApp := application.NewBuilder().
		NodeID("1.2.1").
		NodeType("lobbysvr").
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(lobbyCls).
		Build()
	lobbyApp.Register(lobbyinternal.NewLobbyModule())
	if err := lobbyApp.RegisterHandler(lobbyinternal.NewLobbyHandler("1.2.1"), nil); err != nil {
		t.Fatalf("register lobby handler: %v", err)
	}
	lobbyApp.Start()
	if err := lobbyCls.Init(); err != nil {
		t.Fatalf("lobby cluster init: %v", err)
	}
	defer lobbyCls.Stop()

	// 2. 起测试客户端集群节点（模拟 gateway，节点 1.1.250）
	clientID, err := cluster.ParseNodeID("1.1.250")
	if err != nil {
		t.Fatalf("parse client id: %v", err)
	}
	clientCls, err := transport.NewNatsCluster(clientID, transport.NatsClusterConfig{
		EtcdEndpoints:  etcdEndpoints,
		NatsURLs:       natsURLs,
		SelfAddr:       "127.0.0.1:8899",
		ServerTypeName: "gatesvr",
	})
	if err != nil {
		t.Fatalf("client cluster: %v", err)
	}
	if err := clientCls.Init(); err != nil {
		t.Fatalf("client cluster init: %v", err)
	}
	defer clientCls.Stop()

	// 3. 等待发现 lobbysvr 注册
	if !waitForType(clientCls, "lobbysvr", 5*time.Second) {
		t.Fatal("lobbysvr not discovered within timeout")
	}

	// 4. 经集群 RPC 调 login，断言响应
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	data, err := clientCls.CallAnySync(ctx, "lobbysvr", "LobbyHandler.login",
		&lobbypb.RPC_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("call login: %v", err)
	}
	var rsp lobbypb.RPC_Login_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		t.Fatalf("unmarshal rsp: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 || rsp.LobbyNodeId != "1.2.1" {
		t.Fatalf("unexpected rsp: %+v", &rsp)
	}
}

func waitForType(c *transport.NatsCluster, typeName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(c.Discovery().ByType(typeName)) > 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
```

- [ ] **Step 3: 起基础设施并跑集成测试**

Run:
```bash
docker compose -f test/docker-compose.yaml up -d
sleep 2
go test -tags integration ./test/integration/ -run TestLoginRPC_EndToEnd -v
```
Expected: PASS（`ok project/test/integration`）。

- [ ] **Step 4: 收尾基础设施（可选）**

Run: `docker compose -f test/docker-compose.yaml down`

- [ ] **Step 5: 提交**

```bash
git add test/docker-compose.yaml test/integration/login_test.go
git commit -m "test(integration): NATS+etcd 下 gateway→lobby 登录 RPC 端到端"
```

---

## 验收标准（P1 完成判据）

- `go build ./...` 与 `go test ./src/... -count=1` 全绿（不依赖外部基础设施）。
- 起 NATS+etcd 后 `go test -tags integration ./test/integration/` 通过。
- lobbysvr 可独立启动并在 etcd 注册；gatesvr 接入 NATS 后，客户端登录经 gateway 转发到 lobby、回包成功并完成 `lobbysvr` 节点绑定（单测覆盖绑定逻辑，集成测试覆盖 RPC 链路）。

---

## Self-Review（计划自检结果）

**1. Spec 覆盖**：本计划对应设计稿 §3.1（gateway 转发登录）、§3.6（登录并入 lobby、gateway 选 lobby+记绑定）、§4（NATS+etcd 统一总线）、§6.1（登录流程）。P1 不覆盖 router/online/MongoDB/EC（属 P2/P3，设计稿 §9.2 已列）；登录的 token 校验为 stub（§3.6 假设无状态校验，P1 占位）。

**2. 占位符扫描**：无 TBD/TODO 残留；`verifyToken` 为 stub 但有明确实现与替换说明，非占位符。

**3. 类型一致性**：`RPC_Login_Req{Token, Platform}` / `RPC_Login_Rsp{Code, Uid, LobbyNodeId}` 在 proto、lobby handler、gate handler、单测、集成测试中字段名一致；route 字符串 `"LobbyHandler.login"`（gate 调用）与 lobby 反射注册的 `TypeName("LobbyHandler") + "." + lower("Login")` 一致；`cluster.CallAnySync(ctx, type, route, proto.Message) ([]byte, error)` 与接口定义一致；`transport.NatsClusterConfig` 字段（EtcdEndpoints/NatsURLs/SelfAddr/ServerTypeName）与源码一致。

**4. 已知风险/假设**：①gateway 启动顺序 Start→Init 存在亚秒级"无 lobby 可选"窗口，客户端重试规避，集成测试用 `waitForType` 规避；②`gate_handler.go` 是否仍需 import `session` 包，按 `go build` 提示增删（Step 4 已注明）；③protobuf/json `NewSerializer` 构造器名已核对源码确认。
