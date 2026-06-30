# P2 router + onlinesvr Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Repo conventions:** feature branch + PR（禁止直接 push main）；Conventional Commits + 中文描述；每个 commit 结尾加 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`（下文 commit 步骤为简洁省略，执行时务必补上）。模块路径前缀 `project`。后端服务序列化器一律 **protobuf**。
>
> **Spec:** [`docs/plans/2026-06-02-P2-router-online.md`](2026-06-02-P2-router-online.md)。本计划是其任务级落地。

**Goal:** 打通 lobby ─►router─►onlinesvr 纵切：登录时经 router（Jump Hash 选 online 分片）注册在线、跨 gateway 重复登录踢旧会话、可查询/注销/过期，router 异步转发不串行。

**Architecture:** 新增无状态 `routersvr`（普通 cluster 节点，opt-in 异步转发，按 Jump Consistent Hash 把 `{target_type,key}` 解析到具体实例后同步 relay 并回传）与有状态 `onlinesvr`（纯内存目录 + timewheel 过期 + 顶号直达旧 gateway）。lobby 经新增 `routerclient` 助手统一发起。框架仅加 `CallRawSync`、`asyncDispatch` 开关、`jumphash` 工具。

**Tech Stack:** Go、NATS（cluster RPC）、etcd（discovery）、protobuf、现有 `framework`（application/handler/cluster/agent/session/module）+ `common`（timewheel/config/logger）。

---

## 关键设计事实（实现前必读，均已核对源码）

1. **cluster RPC 反序列化用 registry 的序列化器**（`handler/registry.go:202` `ser.Unmarshal`）。gatesvr 的序列化器是 **json**，而 cluster 字节是 **proto**。因此 gateway 接收的顶号通知 **必须用 raw `[]byte` handler**（`func(ctx, raw []byte)`，`handler.Extract` 支持 raw notify）内部手动 `proto.Unmarshal`，绕开 json 反序列化。
2. **`DispatchCluster` 不传 `SessionProvider`**（`registry.go:164`），故 gateway 顶号 handler 不能用 `sp.Push`，须经 `Application.AgentMap().Load(sessionID)` 拿到 `agent.Agent` 再 `Push`。`AgentMap` 由 factory 自动维护（`agent/factory.go:95`）。
3. **`session.Manager.ByUID(uid)` 已存在**（`session/manager.go:59`）；**gate `OnClose` 钩子已存在**并调用 `notifyPlayerOffline(s)` 桩（`gatesvr/internal/gate_module.go:36,64`）——本计划填充该桩，无需新增钩子。
4. **`gen_routes` 跳过无 `msg_id` 的 message**（`tools/gen_routes/main.go:101`）。router/online 的 RPC 走 cluster route 串分发（`RegisterHandler` 反射注册 `TypeName.methodname`），**不需要 `msg_id`/`server_type`**，故不进 routes 表。
5. **`gate.proto` 已有 `SC_Kick`（msg_id 1004，字段 reason/message）**，顶号推送复用它，gate.proto 无需改动。
6. **P1 `gate.Login` 未给 cluster 调用附 `ClusterSession`**，lobby 拿不到 gateway nodeID。本计划在 `gate.Login` 显式附 `ClusterSession{FrontendId=gate nodeID}`。
7. 测试无 yaml：集成测试用 `transport.NewNatsCluster` + `application.NewBuilder` **在进程内构造**节点（见 P1 `lobbysvr/internal/login_integration_test.go`）；本沙箱无 Docker，集成测试仅 `go vet -tags integration ./...` 编译验证，实跑需有 Docker 机器起 `test/docker-compose.yaml`。
8. **Go internal 包可见性**：跨服务集成测试无法同时 import 两个服务的 `internal`。故 router 与 online 各自在自己 `internal` 包内集成测试（对端用测试内的 stub 节点）。
9. **服务类型名**：`routersvr`、`onlinesvr`。测试 NodeID 约定：gate 模拟 `1.1.2xx`，lobby `1.2.x`，online `1.5.x`，router `1.6.x`（serverType 字节仅作标识，路由按 discovery 的 server_type_name，不依赖该字节）。

---

## 与 spec 的范围说明（实现期收敛）

- **`RPC_PlayerOffline_Notify`（online→旧 lobby）暂不实现**：spec §7 提到顶号时通知旧 lobby 清理绑定，但 P2 lobby 仅登录 stub、无 per-player 内存状态可清理（该通道"先打通"价值为零）。顶号的可观察行为（踢旧 gateway 连接）已由 `KickSession` 覆盖。待 P3 lobby 持玩家工作副本后再补该通知与旧 lobby 卸载逻辑。
- **活跃 Touch 的 lobby 侧触发暂不接线**：P2 无玩家业务消息可"顺带 Touch"。`Touch` RPC + `Directory.Touch` 已实现并单测；lobby 侧 Touch 调用待 P3 玩家功能上线接入（spec §8.3 已注明"主要由单测/集成驱动"）。
- **一致性哈希精简为无状态 `Pick`**：router 每次转发从 discovery 实时取 onlinesvr 成员、用 `jumphash.Pick(members,key)` 选分片，不缓存 Picker、不注册 SDListener（discovery 即成员真相，始终一致）。这是对 spec §5/§3.1 的实现期简化（已同步回 spec）。

## 文件结构

| 文件 | 职责 |
|---|---|
| `src/common/jumphash/jumphash.go` | Jump Consistent Hash 算法 + `Pick(members,key)` 无状态选择器 |
| `src/common/jumphash/jumphash_test.go` | 单测 |
| `src/framework/cluster/cluster.go` | `Cluster` 接口加 `CallRawSync` |
| `src/framework/cluster/noop.go` | noop 实现 `CallRawSync` |
| `src/framework/cluster/transport/nats_cluster.go` | `NatsClusterConfig.AsyncDispatch` + `CallRawSync` 实现 |
| `src/framework/cluster/transport/nats_rpc.go` | `asyncDispatch` 字段 + `onMessage` 拆 `handleMessage` |
| `protocal/router.proto` / `protocal/online.proto` | 转发信封 / 在线目录 RPC + 顶号通知 |
| `protocal/lobby.proto` | 加 `RPC_PlayerDisconnect_Notify` |
| `src/framework/cluster/routerclient/routerclient.go` | lobby 侧「经 router 调微服务」统一助手 |
| `src/framework/cluster/routerclient/routerclient_test.go` | 单测 |
| `src/servers/onlinesvr/internal/directory.go` | 纯内存目录（map+timewheel+ttl），可测 |
| `src/servers/onlinesvr/internal/online_handler.go` | Register/Query/Unregister/Touch + 顶号 Cast |
| `src/servers/onlinesvr/internal/online_module.go` | 生命周期（启停 timewheel） |
| `src/servers/onlinesvr/main.go` | 启动 |
| `src/servers/onlinesvr/internal/*_test.go` | 单测 + 集成测试 |
| `src/servers/routersvr/internal/router_module.go` | 目标解析（Jump Hash 读 discovery） |
| `src/servers/routersvr/internal/router_handler.go` | `Forward` 转发 handler |
| `src/servers/routersvr/main.go` | 启动（AsyncDispatch=true） |
| `src/servers/routersvr/internal/*_test.go` | 单测 + 集成测试 |
| `src/servers/gatesvr/internal/gate_module.go` | 加 nodeID/agentMap，填 `notifyPlayerOffline` |
| `src/servers/gatesvr/internal/gate_handler.go` | `gate.Login` 附 ClusterSession；加 `KickSession` raw handler |
| `src/servers/gatesvr/main.go` | 传 nodeID/AgentMap 给 module |
| `src/servers/lobbysvr/internal/lobby_handler.go` | `Login` 注册 online；加 `PlayerDisconnect` handler |
| `src/servers/lobbysvr/main.go` | 传 cls 给 handler |

---

# Phase A — 框架基础（无服务依赖，纯单测）

## Task 1: jumphash 工具

**Files:**
- Create: `src/common/jumphash/jumphash.go`
- Test: `src/common/jumphash/jumphash_test.go`

- [ ] **Step 1: 写失败测试**

`src/common/jumphash/jumphash_test.go`:
```go
package jumphash

import (
	"fmt"
	"testing"
)

func TestJump_Range(t *testing.T) {
	for _, n := range []int{1, 2, 5, 16, 1024} {
		for k := uint64(0); k < 1000; k++ {
			b := Jump(k, n)
			if b < 0 || int(b) >= n {
				t.Fatalf("Jump(%d,%d)=%d out of [0,%d)", k, n, b, n)
			}
		}
	}
	if got := Jump(123, 0); got != -1 {
		t.Fatalf("Jump with 0 buckets = %d, want -1", got)
	}
}

func TestPick_EmptyAndStable(t *testing.T) {
	if _, ok := Pick(nil, "u1"); ok {
		t.Fatal("Pick on empty members should return ok=false")
	}
	members := []string{"1.5.3", "1.5.1", "1.5.2"}
	got1, ok := Pick(members, "uid-42")
	if !ok {
		t.Fatal("expected ok")
	}
	// 乱序输入应得相同结果（内部排序，跨实例一致）
	got2, _ := Pick([]string{"1.5.2", "1.5.3", "1.5.1"}, "uid-42")
	if got1 != got2 {
		t.Fatalf("Pick not order-independent: %s vs %s", got1, got2)
	}
	// 去重：重复成员不影响结果
	got3, _ := Pick([]string{"1.5.1", "1.5.2", "1.5.3", "1.5.2"}, "uid-42")
	if got1 != got3 {
		t.Fatalf("Pick not dedup-stable: %s vs %s", got1, got3)
	}
}

func TestPick_TailAddMovesFew(t *testing.T) {
	base := []string{"1.5.1", "1.5.2", "1.5.3", "1.5.4"}
	// 在排序尾部追加一个成员（"1.5.5" 排在最后）
	grown := append([]string{}, base...)
	grown = append(grown, "1.5.5")
	moved := 0
	const total = 10000
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("uid-%d", i)
		a, _ := Pick(base, key)
		b, _ := Pick(grown, key)
		if a != b {
			moved++
		}
	}
	// Jump Hash 尾部增节点理论迁移 ~1/N（N=5 → 20%），留宽松上界 30%
	if moved > total*30/100 {
		t.Fatalf("tail add moved %d/%d keys, expected ~1/N", moved, total)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/common/jumphash/ -run TestJump -v`
Expected: 编译失败（`undefined: Jump` / `Pick`）。

- [ ] **Step 3: 实现**

`src/common/jumphash/jumphash.go`:
```go
// Package jumphash 实现 Jump Consistent Hash（Lamping & Veach, 2014）：
// 无环、无虚拟节点、零额外内存、计算极快、分布均衡。
//
// 取舍：尾部增删（成员排序后变动的是最末元素）仅迁移 ~1/N 的 key；
// 非尾部成员变动会使其后成员桶号顺移、迁移较多 key。在线态纯内存、
// 重建廉价，可接受（见 spec §12）。
package jumphash

import (
	"hash/fnv"
	"sort"
)

// Jump 计算 key 落到 [0, numBuckets) 的桶号；numBuckets<=0 返回 -1。
func Jump(key uint64, numBuckets int) int32 {
	if numBuckets <= 0 {
		return -1
	}
	var b, j int64 = -1, 0
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int32(b)
}

// Pick 把 members 去重升序后，用 Jump Hash(fnv1a64(key)) 选一个成员。
// members 为空返回 ("", false)。排序保证调用方（不同 router 实例）对
// 同一 key 选出同一成员，与传入顺序无关。
func Pick(members []string, key string) (string, bool) {
	uniq := dedupSorted(members)
	n := len(uniq)
	if n == 0 {
		return "", false
	}
	b := Jump(hashKey(key), n)
	if b < 0 || int(b) >= n {
		return "", false
	}
	return uniq[b], true
}

func hashKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

func dedupSorted(members []string) []string {
	if len(members) == 0 {
		return nil
	}
	cp := append([]string(nil), members...)
	sort.Strings(cp)
	out := cp[:1]
	for _, m := range cp[1:] {
		if m != out[len(out)-1] {
			out = append(out, m)
		}
	}
	return out
}
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/common/jumphash/ -v`
Expected: PASS（4 个测试全过）。

- [ ] **Step 5: 提交**

```bash
git add src/common/jumphash/
git commit -m "feat(p2): 新增 jumphash（Jump Consistent Hash）通用工具"
```

---

## Task 2: cluster.CallRawSync

**Files:**
- Modify: `src/framework/cluster/cluster.go`（接口加方法）
- Modify: `src/framework/cluster/noop.go`（noop 实现）
- Modify: `src/framework/cluster/transport/nats_cluster.go`（实现）

- [ ] **Step 1: 接口加方法**

在 `src/framework/cluster/cluster.go` 的 `Cluster` 接口中，`CallRaw` 之后加一行：
```go
	// CallRawSync 指定节点，同步有返回，data 为已序列化 []byte（router 转发用）
	CallRawSync(ctx context.Context, target NodeID, route string, data []byte) ([]byte, error)
```

- [ ] **Step 2: noop 实现**

在 `src/framework/cluster/noop.go` 的 `CallRaw` 方法后加：
```go
func (nc *noopCluster) CallRawSync(_ context.Context, _ NodeID, _ string, _ []byte) ([]byte, error) {
	return nil, errNoopCluster
}
```

- [ ] **Step 3: NatsCluster 实现**

在 `src/framework/cluster/transport/nats_cluster.go` 的 `CallRaw` 方法后加：
```go
// CallRawSync 指定节点，同步有返回，data 为已序列化 []byte（转发场景用）
func (c *NatsCluster) CallRawSync(ctx context.Context, target cluster.NodeID, route string, data []byte) ([]byte, error) {
	return c.rpc.Call(ctx, target, buildMsg(ctx, route, data))
}
```
（`rpc.Call` 即 `nats_rpc.go:97` 的同步 `RequestWithContext`+`parseResponse`，含本地短路与 deadline 透传。）

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 通过（所有 `Cluster` 实现都补齐了新方法）。

- [ ] **Step 5: 提交**

```bash
git add src/framework/cluster/
git commit -m "feat(p2): cluster 新增 CallRawSync（同步 raw 调用，router 转发用）"
```

---

## Task 3: 传输层 asyncDispatch（router 异步转发，不串行）

**Files:**
- Modify: `src/framework/cluster/transport/nats_rpc.go`
- Modify: `src/framework/cluster/transport/nats_cluster.go`

- [ ] **Step 1: NatsRPC 加字段 + 构造参数**

`nats_rpc.go`：给 `NatsRPC` struct 加字段（在 `dieCh` 后）：
```go
	asyncDispatch bool // 入站消息每条独立 goroutine 处理（仅无状态的 router 开启）
```
修改 `NewNatsRPC` 签名与赋值：
```go
func NewNatsRPC(urls []string, self cluster.NodeID, dieCh chan struct{}, asyncDispatch bool) (*NatsRPC, error) {
	r := &NatsRPC{
		subject:       self.Subject(),
		dieCh:         dieCh,
		asyncDispatch: asyncDispatch,
	}
	// ...（其余不变）
```

- [ ] **Step 2: onMessage 拆出 handleMessage**

把 `nats_rpc.go` 现有 `onMessage` 整体重命名为 `handleMessage`，并新增一个调度入口 `onMessage`：
```go
// onMessage 订阅回调：异步节点（router）每条消息独立 goroutine 处理，
// 订阅 goroutine 立即返回取下一条，转发互不阻塞；其余节点保持顺序处理，
// 保住"同目标有序"与帧驱动语义。
func (r *NatsRPC) onMessage(natsMsg *nats.Msg) {
	if r.asyncDispatch {
		go r.handleMessage(natsMsg)
		return
	}
	r.handleMessage(natsMsg)
}

// handleMessage 解信封 → 重建 ctx → 调 handler → 回包（原 onMessage 函数体，逻辑不变）
func (r *NatsRPC) handleMessage(natsMsg *nats.Msg) {
	// ...（原 onMessage 全部函数体，一字不改）
}
```

- [ ] **Step 3: NatsClusterConfig 加开关并透传**

`nats_cluster.go`：给 `NatsClusterConfig` 加字段：
```go
	AsyncDispatch bool // 入站消息并发处理（仅 router 设 true）
```
修改 `NewNatsCluster` 里创建 rpc 的那行：
```go
	rpc, err := NewNatsRPC(cfg.NatsURLs, self, dieCh, cfg.AsyncDispatch)
```

- [ ] **Step 4: 编译验证**

Run: `go build ./...`
Expected: 通过（`NewNatsRPC` 唯一调用方是 `NewNatsCluster`，已同步）。

- [ ] **Step 5: 提交**

```bash
git add src/framework/cluster/transport/
git commit -m "feat(p2): 传输层 opt-in asyncDispatch（router 异步转发不串行）"
```

---

# Phase B — proto

## Task 4: 新增 router.proto / online.proto + lobby.proto 扩充 + 生成

**Files:**
- Create: `protocal/router.proto`, `protocal/online.proto`
- Modify: `protocal/lobby.proto`
- Generate: `protocal/gen/router/*.pb.go`, `protocal/gen/online/*.pb.go`, `protocal/gen/lobby/*.pb.go`, `protocal/gen/routes/routes.go`

- [ ] **Step 1: 创建 `protocal/router.proto`**
```proto
syntax = "proto3";

package router;

option go_package = "project/protocal/gen/router";

// RoutingMode 转发路由模式（P2 仅用 CONSISTENT_HASH；ANY/DIRECT 预留 P4 match/room）
enum RoutingMode {
  ROUTING_CONSISTENT_HASH = 0; // 按 routing_key 一致性哈希选分片
  ROUTING_ANY             = 1; // 随机一个该类型实例（P4）
  ROUTING_DIRECT          = 2; // routing_key 即目标 NodeID 串（P4）
}

// RPC_RouterForward_Req lobby → router 的统一转发信封（route="RouterHandler.forward"）
message RPC_RouterForward_Req {
  RoutingMode routing_mode = 1;
  string      target_type  = 2; // 目标服务类型名，如 "onlinesvr"
  string      routing_key  = 3; // CONSISTENT_HASH:uid 串；DIRECT:NodeID 串
  string      inner_route  = 4; // 真实业务 route，如 "OnlineHandler.register"
  bytes       inner_data   = 5; // 已序列化的真实业务请求
}

// RPC_RouterForward_Rsp router 回 lobby
message RPC_RouterForward_Rsp {
  int32  code       = 1; // 0=ok；非 0=router 侧错误
  string err_msg    = 2;
  bytes  inner_data = 3; // 真实业务响应原样透传
}
```

- [ ] **Step 2: 创建 `protocal/online.proto`**
```proto
syntax = "proto3";

package online;

option go_package = "project/protocal/gen/online";

// OnlineEntry 在线条目（Query 返回用）
message OnlineEntry {
  int64  uid             = 1;
  string gateway_node_id = 2;
  string lobby_node_id   = 3;
  int64  login_time      = 4; // Unix 纳秒
  int64  last_active     = 5; // Unix 纳秒
}

// RPC_Register_Req lobby（经 router）注册在线（route="OnlineHandler.register"）
message RPC_Register_Req {
  int64  uid             = 1;
  string gateway_node_id = 2;
  string lobby_node_id   = 3;
}
message RPC_Register_Rsp {
  int32 code       = 1;
  bool  kicked_old = 2; // 是否触发了顶号
}

// RPC_Query_Req 定位玩家（route="OnlineHandler.query"）
message RPC_Query_Req {
  int64 uid = 1;
}
message RPC_Query_Rsp {
  bool        online = 1;
  OnlineEntry entry  = 2;
}

// RPC_Unregister_Req 注销（route="OnlineHandler.unregister"）
message RPC_Unregister_Req {
  int64 uid = 1;
}
message RPC_Unregister_Rsp {
  int32 code = 1;
}

// RPC_Touch_Req 刷新活跃（route="OnlineHandler.touch"）
message RPC_Touch_Req {
  int64 uid = 1;
}
message RPC_Touch_Rsp {
  int32 code   = 1;
  bool  online = 2; // 目标不在线则 false
}

// RPC_KickSession_Notify online → 旧 gateway 的顶号通知（route="GateHandler.kicksession"，单向）
message RPC_KickSession_Notify {
  int64 uid    = 1;
  int32 reason = 2; // 1=重复登录被顶
}
```

- [ ] **Step 3: 扩充 `protocal/lobby.proto`**

在文件末尾追加：
```proto
// RPC_PlayerDisconnect_Notify gateway → lobby 的断连通知（route="LobbyHandler.playerdisconnect"，单向）
message RPC_PlayerDisconnect_Notify {
  int64 uid = 1;
}
```

- [ ] **Step 4: 生成 pb 与路由表**

Run（protoc 借用，见 spec §9 / 开发环境约定）：
```bash
PROTOC=/game/dev/silver-server/tools/server_excel_tool/protoc
INC=/game/dev/silver-server/3rd/protobuf/include
$PROTOC --go_out=. --go_opt=module=project --proto_path=. --proto_path=$INC \
  protocal/router.proto protocal/online.proto protocal/lobby.proto
go run ./tools/gen_routes
```
Expected: 生成 `protocal/gen/router/router.pb.go`、`protocal/gen/online/online.pb.go`、更新 `protocal/gen/lobby/lobby.pb.go`；`gen_routes` 打印生成条数（router/online 无 msg_id，不进表，routes.go 内容应与之前一致）。

- [ ] **Step 5: 编译验证**

Run: `go build ./...`
Expected: 通过（生成代码可编译）。

- [ ] **Step 6: 提交**

```bash
git add protocal/
git commit -m "feat(p2): 新增 router/online proto 与 lobby 断连通知，重生成路由"
```

---

# Phase C — routerclient

## Task 5: routerclient 助手

**Files:**
- Create: `src/framework/cluster/routerclient/routerclient.go`
- Test: `src/framework/cluster/routerclient/routerclient_test.go`

- [ ] **Step 1: 写失败测试**

`src/framework/cluster/routerclient/routerclient_test.go`:
```go
package routerclient

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/framework/cluster"
)

// fakeCluster 仅实现 CallAnySync，其余方法继承接口（不被调用）
type fakeCluster struct {
	cluster.Cluster
	gotType, gotRoute string
	gotReq            *routerpb.RPC_RouterForward_Req
	rsp               *routerpb.RPC_RouterForward_Rsp
}

func (f *fakeCluster) CallAnySync(_ context.Context, serverType, route string, req proto.Message) ([]byte, error) {
	f.gotType, f.gotRoute = serverType, route
	f.gotReq = req.(*routerpb.RPC_RouterForward_Req)
	return proto.Marshal(f.rsp)
}

func TestCallViaSync_WrapsAndUnwraps(t *testing.T) {
	innerRsp := &onlinepb.RPC_Register_Rsp{Code: 0, KickedOld: true}
	innerBytes, _ := proto.Marshal(innerRsp)
	fc := &fakeCluster{rsp: &routerpb.RPC_RouterForward_Rsp{Code: 0, InnerData: innerBytes}}

	out, err := CallViaSync[*onlinepb.RPC_Register_Rsp](
		context.Background(), fc, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001",
		"OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !out.KickedOld {
		t.Fatalf("inner rsp not unwrapped: %+v", out)
	}
	if fc.gotType != "routersvr" || fc.gotRoute != "RouterHandler.forward" {
		t.Fatalf("wrong dispatch target: %s/%s", fc.gotType, fc.gotRoute)
	}
	if fc.gotReq.TargetType != "onlinesvr" || fc.gotReq.RoutingKey != "10001" ||
		fc.gotReq.InnerRoute != "OnlineHandler.register" ||
		fc.gotReq.RoutingMode != routerpb.RoutingMode_ROUTING_CONSISTENT_HASH {
		t.Fatalf("envelope wrong: %+v", fc.gotReq)
	}
}

func TestCallViaSync_RouterError(t *testing.T) {
	fc := &fakeCluster{rsp: &routerpb.RPC_RouterForward_Rsp{Code: 1, ErrMsg: "no target"}}
	_, err := CallViaSync[*onlinepb.RPC_Register_Rsp](
		context.Background(), fc, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "1", "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 1})
	if err == nil {
		t.Fatal("expected error when router code != 0")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/framework/cluster/routerclient/ -v`
Expected: 编译失败（`undefined: CallViaSync`）。

- [ ] **Step 3: 实现**

`src/framework/cluster/routerclient/routerclient.go`:
```go
// Package routerclient 提供 lobby 侧「经 router 调用微服务」的统一姿势：
// 把 {目标类型, 路由模式, 路由 key, 真实 route, 真实请求} 封进转发信封，
// 经 CallAny 发到任一 routersvr，由 router 解析目标实例转发并回传响应。
package routerclient

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	routerpb "project/protocal/gen/router"
	"project/src/framework/cluster"
)

const (
	routerServerType = "routersvr"
	forwardRoute     = "RouterHandler.forward"
)

// CallViaSync 经 router 同步调用某类微服务的某个 route，返回具体响应类型 R。
//   - targetType: 目标服务类型名，如 "onlinesvr"
//   - mode/key:   路由模式与 key（CONSISTENT_HASH 时 key 为 uid 串）
//   - innerRoute: 真实业务 route，如 "OnlineHandler.register"
func CallViaSync[R proto.Message](
	ctx context.Context, cls cluster.Cluster,
	targetType string, mode routerpb.RoutingMode, key, innerRoute string,
	req proto.Message,
) (R, error) {
	var zero R
	inner, err := proto.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("routerclient: marshal inner req: %w", err)
	}
	env := &routerpb.RPC_RouterForward_Req{
		RoutingMode: mode,
		TargetType:  targetType,
		RoutingKey:  key,
		InnerRoute:  innerRoute,
		InnerData:   inner,
	}
	data, err := cls.CallAnySync(ctx, routerServerType, forwardRoute, env)
	if err != nil {
		return zero, fmt.Errorf("routerclient: call router: %w", err)
	}
	var rsp routerpb.RPC_RouterForward_Rsp
	if err := proto.Unmarshal(data, &rsp); err != nil {
		return zero, fmt.Errorf("routerclient: unmarshal forward rsp: %w", err)
	}
	if rsp.Code != 0 {
		return zero, fmt.Errorf("routerclient: router forward failed code=%d: %s", rsp.Code, rsp.ErrMsg)
	}
	out := newProto[R]()
	if len(rsp.InnerData) > 0 {
		if err := proto.Unmarshal(rsp.InnerData, out); err != nil {
			return zero, fmt.Errorf("routerclient: unmarshal inner rsp: %w", err)
		}
	}
	return out, nil
}

// newProto 构造 R 的新实例（R 为指针类型的 proto.Message）
func newProto[R proto.Message]() R {
	var r R
	return r.ProtoReflect().New().Interface().(R)
}
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/framework/cluster/routerclient/ -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/framework/cluster/routerclient/
git commit -m "feat(p2): 新增 routerclient（lobby 经 router 调微服务统一助手）"
```

---

# Phase D — onlinesvr

## Task 6: 在线目录 Directory（纯内存，可测）

**Files:**
- Create: `src/servers/onlinesvr/internal/directory.go`
- Test: `src/servers/onlinesvr/internal/directory_test.go`

- [ ] **Step 1: 写失败测试**

`src/servers/onlinesvr/internal/directory_test.go`:
```go
package internal

import (
	"testing"
	"time"

	"project/src/common/timewheel"
)

// 手动推进的时间轮：tick=1ms，便于确定性测试过期
func newTestDir(ttl time.Duration) (*Directory, *timewheel.TimeWheel) {
	tw := timewheel.New(time.Millisecond, 64)
	return NewDirectory(tw, ttl), tw
}

func TestDirectory_RegisterQueryUnregister(t *testing.T) {
	d, _ := newTestDir(5 * time.Millisecond)
	old, replaced := d.Register(10001, "1.1.1", "1.2.1", 100)
	if replaced || old != nil {
		t.Fatalf("first register should not replace")
	}
	e, ok := d.Query(10001)
	if !ok || e.GatewayNodeID != "1.1.1" || e.LobbyNodeID != "1.2.1" {
		t.Fatalf("query miss/wrong: %+v %v", e, ok)
	}
	if !d.Unregister(10001) {
		t.Fatal("unregister should report removed")
	}
	if _, ok := d.Query(10001); ok {
		t.Fatal("query should miss after unregister")
	}
	if d.Unregister(10001) {
		t.Fatal("unregister non-existent should report false (idempotent)")
	}
}

func TestDirectory_DupLoginReturnsOld(t *testing.T) {
	d, _ := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	old, replaced := d.Register(10001, "1.1.2", "1.2.1", 200) // 不同 gateway
	if !replaced || old == nil || old.GatewayNodeID != "1.1.1" {
		t.Fatalf("dup login should return old gateway entry, got %+v replaced=%v", old, replaced)
	}
	e, _ := d.Query(10001)
	if e.GatewayNodeID != "1.1.2" {
		t.Fatalf("entry should be overwritten to new gateway, got %s", e.GatewayNodeID)
	}
	// 同 gateway 重复注册不算顶号
	_, replaced2 := d.Register(10001, "1.1.2", "1.2.1", 300)
	if replaced2 {
		t.Fatal("same gateway re-register should not be a kick")
	}
}

func TestDirectory_Expire(t *testing.T) {
	d, tw := newTestDir(5 * time.Millisecond) // 5 ticks
	d.Register(10001, "1.1.1", "1.2.1", 100)
	for i := 0; i < 6; i++ {
		tw.Advance()
	}
	if _, ok := d.Query(10001); ok {
		t.Fatal("entry should expire after ttl")
	}
}

func TestDirectory_TouchResetsExpiry(t *testing.T) {
	d, tw := newTestDir(5 * time.Millisecond)
	d.Register(10001, "1.1.1", "1.2.1", 100)
	tw.Advance()
	tw.Advance() // 2 ticks，未到期
	if !d.Touch(10001, 200) {
		t.Fatal("touch on existing should return true")
	}
	for i := 0; i < 4; i++ { // 再推 4 tick（距 touch 4 ticks，<5，仍在）
		tw.Advance()
	}
	if _, ok := d.Query(10001); !ok {
		t.Fatal("entry should survive within ttl after touch")
	}
	tw.Advance() // 距 touch 第 5 tick，到期
	tw.Advance()
	if _, ok := d.Query(10001); ok {
		t.Fatal("entry should expire ttl after last touch")
	}
	if d.Touch(99999, 1) {
		t.Fatal("touch on missing should return false")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestDirectory -v`
Expected: 编译失败（`undefined: NewDirectory` / `Directory`）。

- [ ] **Step 3: 实现**

`src/servers/onlinesvr/internal/directory.go`:
```go
package internal

import (
	"sync"
	"time"

	"project/src/common/timewheel"
)

// Entry 在线目录条目（值拷贝返回给调用方，避免外部改内部状态）
type Entry struct {
	Uid           int64
	GatewayNodeID string
	LobbyNodeID   string
	LoginTime     int64 // Unix 纳秒
	LastActive    int64 // Unix 纳秒
}

// Directory 全局在线目录的纯内存实现：map + 单锁 + timewheel 过期。
// 不依赖 cluster，便于单测；顶号的 Cast 由 OnlineHandler 处理。
type Directory struct {
	mu      sync.Mutex
	entries map[int64]*Entry
	timers  map[int64]*timewheel.Timer
	tw      *timewheel.TimeWheel
	ttl     time.Duration
}

func NewDirectory(tw *timewheel.TimeWheel, ttl time.Duration) *Directory {
	return &Directory{
		entries: make(map[int64]*Entry),
		timers:  make(map[int64]*timewheel.Timer),
		tw:      tw,
		ttl:     ttl,
	}
}

// Register 注册/刷新在线条目。若已存在且 gateway 不同（跨 gateway 重复登录），
// 返回旧条目副本与 replaced=true，调用方据此踢旧 gateway。
func (d *Directory) Register(uid int64, gw, lobby string, nowNano int64) (old *Entry, replaced bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if e, ok := d.entries[uid]; ok {
		if e.GatewayNodeID != gw {
			cp := *e
			old, replaced = &cp, true
		}
		if t := d.timers[uid]; t != nil {
			d.tw.Stop(t)
		}
	}
	d.entries[uid] = &Entry{
		Uid: uid, GatewayNodeID: gw, LobbyNodeID: lobby,
		LoginTime: nowNano, LastActive: nowNano,
	}
	d.timers[uid] = d.tw.AfterFunc(d.ttl, func() { d.expire(uid) })
	return old, replaced
}

// Query 返回在线条目副本。
func (d *Directory) Query(uid int64) (Entry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Unregister 删除条目，返回是否确实删除（幂等）。
func (d *Directory) Unregister(uid int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.entries[uid]; !ok {
		return false
	}
	if t := d.timers[uid]; t != nil {
		d.tw.Stop(t)
	}
	delete(d.entries, uid)
	delete(d.timers, uid)
	return true
}

// Touch 刷新活跃并重置过期定时器，返回目标是否在线。
func (d *Directory) Touch(uid int64, nowNano int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[uid]
	if !ok {
		return false
	}
	e.LastActive = nowNano
	if t := d.timers[uid]; t != nil {
		d.tw.Stop(t)
	}
	d.timers[uid] = d.tw.AfterFunc(d.ttl, func() { d.expire(uid) })
	return true
}

// expire 过期清理（timewheel 回调，运行在 tw 推进 goroutine）。
// Touch/Unregister 已 Stop 旧 timer，正常不会误删；极少数 Touch/expire 竞态
// 至多导致一次误过期，下次活跃自然重建（在线态纯内存，可接受，见 spec §12）。
func (d *Directory) expire(uid int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.entries, uid)
	delete(d.timers, uid)
}
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestDirectory -v`
Expected: PASS（4 个测试）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/onlinesvr/internal/directory.go src/servers/onlinesvr/internal/directory_test.go
git commit -m "feat(p2): onlinesvr 在线目录 Directory（map+timewheel 过期）"
```

---

## Task 7: OnlineHandler（4 个 RPC + 顶号 Cast）

**Files:**
- Create: `src/servers/onlinesvr/internal/online_handler.go`
- Test: `src/servers/onlinesvr/internal/online_handler_test.go`

- [ ] **Step 1: 写失败测试**

`src/servers/onlinesvr/internal/online_handler_test.go`:
```go
package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	"project/src/common/timewheel"
	"project/src/framework/cluster"
)

// fakeKicker 仅捕获 Cast（顶号下发）
type fakeKicker struct {
	cluster.Cluster
	castTarget cluster.NodeID
	castRoute  string
	castUID    int64
	castCount  int
}

func (f *fakeKicker) Cast(_ context.Context, target cluster.NodeID, route string, msg proto.Message) error {
	f.castTarget, f.castRoute = target, route
	if n, ok := msg.(*onlinepb.RPC_KickSession_Notify); ok {
		f.castUID = n.Uid
	}
	f.castCount++
	return nil
}

func newTestHandler() (*OnlineHandler, *fakeKicker) {
	tw := timewheel.New(time.Millisecond, 64)
	dir := NewDirectory(tw, time.Second)
	fk := &fakeKicker{}
	return NewOnlineHandler(dir, fk), fk
}

func TestOnlineHandler_RegisterQuery(t *testing.T) {
	h, fk := newTestHandler()
	ctx := context.Background()
	rsp, err := h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	if err != nil || rsp.Code != 0 || rsp.KickedOld {
		t.Fatalf("register: %+v %v", rsp, err)
	}
	if fk.castCount != 0 {
		t.Fatal("first register should not kick")
	}
	q, _ := h.Query(ctx, &onlinepb.RPC_Query_Req{Uid: 10001})
	if !q.Online || q.Entry.GatewayNodeId != "1.1.1" {
		t.Fatalf("query: %+v", q)
	}
}

func TestOnlineHandler_DupLoginKicksOldGateway(t *testing.T) {
	h, fk := newTestHandler()
	ctx := context.Background()
	h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	rsp, _ := h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.2", LobbyNodeId: "1.2.1"})
	if !rsp.KickedOld {
		t.Fatal("dup login should report kicked_old")
	}
	if fk.castCount != 1 || fk.castRoute != "GateHandler.kicksession" || fk.castUID != 10001 {
		t.Fatalf("kick cast wrong: count=%d route=%s uid=%d", fk.castCount, fk.castRoute, fk.castUID)
	}
	want, _ := cluster.ParseNodeID("1.1.1") // 踢的是旧 gateway
	if fk.castTarget != want {
		t.Fatalf("kick target = %v, want old gateway %v", fk.castTarget, want)
	}
}

func TestOnlineHandler_UnregisterTouch(t *testing.T) {
	h, _ := newTestHandler()
	ctx := context.Background()
	h.Register(ctx, &onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.1", LobbyNodeId: "1.2.1"})
	tr, _ := h.Touch(ctx, &onlinepb.RPC_Touch_Req{Uid: 10001})
	if !tr.Online {
		t.Fatal("touch online should be true")
	}
	h.Unregister(ctx, &onlinepb.RPC_Unregister_Req{Uid: 10001})
	q, _ := h.Query(ctx, &onlinepb.RPC_Query_Req{Uid: 10001})
	if q.Online {
		t.Fatal("should be offline after unregister")
	}
	tr2, _ := h.Touch(ctx, &onlinepb.RPC_Touch_Req{Uid: 10001})
	if tr2.Online {
		t.Fatal("touch after unregister should report offline")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestOnlineHandler -v`
Expected: 编译失败（`undefined: NewOnlineHandler`）。

- [ ] **Step 3: 实现**

`src/servers/onlinesvr/internal/online_handler.go`:
```go
package internal

import (
	"context"
	"time"

	onlinepb "project/protocal/gen/online"
	"project/src/common/logger"
	"project/src/framework/cluster"
)

const kickReasonDupLogin int32 = 1

// OnlineHandler 在线目录的集群 RPC handler。
// 持 Directory（状态）+ cluster（顶号 Cast 直达旧 gateway）。
type OnlineHandler struct {
	dir *Directory
	cls cluster.Cluster
}

func NewOnlineHandler(dir *Directory, cls cluster.Cluster) *OnlineHandler {
	return &OnlineHandler{dir: dir, cls: cls}
}

// Register 注册在线；检测到跨 gateway 重复登录则直达旧 gateway 踢号。
func (h *OnlineHandler) Register(ctx context.Context, req *onlinepb.RPC_Register_Req) (*onlinepb.RPC_Register_Rsp, error) {
	old, replaced := h.dir.Register(req.Uid, req.GatewayNodeId, req.LobbyNodeId, time.Now().UnixNano())
	if replaced && old != nil {
		gw, err := cluster.ParseNodeID(old.GatewayNodeID)
		if err != nil {
			logger.Warn("online: bad old gateway nodeID",
				logger.Int64("uid", req.Uid), logger.String("nodeID", old.GatewayNodeID))
		} else if err := h.cls.Cast(ctx, gw, "GateHandler.kicksession",
			&onlinepb.RPC_KickSession_Notify{Uid: req.Uid, Reason: kickReasonDupLogin}); err != nil {
			logger.Warn("online: kick cast failed", logger.Int64("uid", req.Uid), logger.Err(err))
		}
	}
	return &onlinepb.RPC_Register_Rsp{Code: 0, KickedOld: replaced}, nil
}

// Query 定位玩家。
func (h *OnlineHandler) Query(_ context.Context, req *onlinepb.RPC_Query_Req) (*onlinepb.RPC_Query_Rsp, error) {
	e, ok := h.dir.Query(req.Uid)
	if !ok {
		return &onlinepb.RPC_Query_Rsp{Online: false}, nil
	}
	return &onlinepb.RPC_Query_Rsp{Online: true, Entry: &onlinepb.OnlineEntry{
		Uid: e.Uid, GatewayNodeId: e.GatewayNodeID, LobbyNodeId: e.LobbyNodeID,
		LoginTime: e.LoginTime, LastActive: e.LastActive,
	}}, nil
}

// Unregister 注销（幂等）。
func (h *OnlineHandler) Unregister(_ context.Context, req *onlinepb.RPC_Unregister_Req) (*onlinepb.RPC_Unregister_Rsp, error) {
	h.dir.Unregister(req.Uid)
	return &onlinepb.RPC_Unregister_Rsp{Code: 0}, nil
}

// Touch 刷新活跃。
func (h *OnlineHandler) Touch(_ context.Context, req *onlinepb.RPC_Touch_Req) (*onlinepb.RPC_Touch_Rsp, error) {
	ok := h.dir.Touch(req.Uid, time.Now().UnixNano())
	return &onlinepb.RPC_Touch_Rsp{Code: 0, Online: ok}, nil
}
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/servers/onlinesvr/internal/ -run TestOnlineHandler -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add src/servers/onlinesvr/internal/online_handler.go src/servers/onlinesvr/internal/online_handler_test.go
git commit -m "feat(p2): OnlineHandler（注册/查询/注销/活跃 + 顶号直达旧 gateway）"
```

---

## Task 8: OnlineModule + main.go

**Files:**
- Create: `src/servers/onlinesvr/internal/online_module.go`
- Create: `src/servers/onlinesvr/main.go`
- Create: `conf/online.yaml`

- [ ] **Step 1: OnlineModule**

`src/servers/onlinesvr/internal/online_module.go`:
```go
package internal

import (
	"time"

	"project/src/common/logger"
	"project/src/common/timewheel"
	"project/src/framework/module"
)

// 在线过期 TTL（= 5min 重连宽限窗口雏形）。可通过 NewOnlineModule 注入便于测试。
const DefaultEntryTTL = 5 * time.Minute

// OnlineModule onlinesvr 生命周期：持有 timewheel 与 Directory，
// Init 启动自驱动时间轮，OnStop 关闭。
type OnlineModule struct {
	module.BaseModule
	tw  *timewheel.TimeWheel
	dir *Directory
}

func NewOnlineModule(ttl time.Duration) *OnlineModule {
	tw := timewheel.New(time.Second, 512) // 单圈 512s，覆盖 5min ttl
	return &OnlineModule{tw: tw, dir: NewDirectory(tw, ttl)}
}

func (m *OnlineModule) Name() string         { return "online" }
func (m *OnlineModule) Directory() *Directory { return m.dir }

func (m *OnlineModule) Init() {
	m.tw.Start()
	logger.Info("online module initialized", logger.Int("entryTTLsec", int(m.dir.ttl.Seconds())))
}

func (m *OnlineModule) OnStop() {
	m.tw.Close()
	logger.Info("online module stopped")
}
```

- [ ] **Step 2: main.go**

`src/servers/onlinesvr/main.go`:
```go
package main

import (
	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/onlinesvr/internal"
)

func main() {
	cfg := config.MustLoad("conf/online.yaml")

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

	mod := internal.NewOnlineModule(internal.DefaultEntryTTL)
	app.Register(mod)
	if err := app.RegisterHandler(internal.NewOnlineHandler(mod.Directory(), app.Cluster()), nil); err != nil {
		panic(err)
	}

	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	logger.Info("onlinesvr started", logger.String("nodeID", cfg.Node.ID))
	app.Run()
}
```

- [ ] **Step 3: conf/online.yaml**

`conf/online.yaml`（参照 `conf/lobby.yaml` 结构）:
```yaml
node:
  id: "1.5.1"
  server_type_name: "onlinesvr"
  addr: "0.0.0.0:8851"
cluster:
  etcd:
    endpoints: ["localhost:2379"]
  nats:
    urls: ["nats://localhost:4222"]
log:
  level: "info"
  dir: "./logs"
```

- [ ] **Step 4: 编译验证**

Run: `go build ./src/servers/onlinesvr/...`
Expected: 通过。

- [ ] **Step 5: 提交**

```bash
git add src/servers/onlinesvr/ conf/online.yaml
git commit -m "feat(p2): onlinesvr 模块与启动入口"
```

---

## Task 9: onlinesvr 集成测试（NATS+etcd）

**Files:**
- Create: `src/servers/onlinesvr/internal/online_integration_test.go`

> 在 onlinesvr 自己的 internal 包内，验证 online 经真实 cluster 的 Register/Query/Unregister/顶号。模拟「lobby」直接调 `OnlineHandler.*`（不经 router，router 转发在 routersvr 集成测试覆盖）；顶号下发到一个测试内的「stub gateway」节点观察。

- [ ] **Step 1: 写集成测试**

`src/servers/onlinesvr/internal/online_integration_test.go`:
```go
//go:build integration

package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
)

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

// stubGate 记录收到的顶号通知（raw handler，自行 proto.Unmarshal，绕开序列化器）
type stubGate struct{ kickedUID atomic.Int64 }

func (g *stubGate) KickSession(_ context.Context, raw []byte) {
	var n onlinepb.RPC_KickSession_Notify
	if err := proto.Unmarshal(raw, &n); err == nil {
		g.kickedUID.Store(n.Uid)
	}
}

func startNode(t *testing.T, id, typ string, register func(app *application.Application)) (*application.Application, *transport.NatsCluster) {
	t.Helper()
	self, err := cluster.ParseNodeID(id)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints: etcdEndpoints, NatsURLs: natsURLs,
		SelfAddr: "127.0.0.1:0", ServerTypeName: typ,
	})
	if err != nil {
		t.Fatalf("cluster %s: %v", id, err)
	}
	app := application.NewBuilder().
		NodeID(id).NodeType(typ).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).Build()
	register(app)
	app.Start()
	if err := cls.Init(); err != nil {
		t.Fatalf("init %s: %v", id, err)
	}
	return app, cls
}

func waitForType(c *transport.NatsCluster, typ string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(c.Discovery().ByType(typ)) > 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestOnline_RegisterQueryKick_EndToEnd(t *testing.T) {
	// online 节点
	onlineApp, onlineCls := startNode(t, "1.5.1", "onlinesvr", func(app *application.Application) {
		mod := NewOnlineModule(500 * time.Millisecond) // 短 TTL 便于过期断言
		app.Register(mod)
		if err := app.RegisterHandler(NewOnlineHandler(mod.Directory(), app.Cluster()), nil); err != nil {
			t.Fatalf("register online handler: %v", err)
		}
	})
	defer onlineCls.Stop()
	_ = onlineApp

	// stub gateway（节点 1.1.201），注册 GateHandler.kicksession 观察顶号
	sg := &stubGate{}
	_, gateCls := startNode(t, "1.1.201", "gatesvr", func(app *application.Application) {
		// stubGate 复用 GateHandler 类型名以生成 route "GateHandler.kicksession"
		if err := app.RegisterHandler(&GateHandler{stub: sg}, nil); err != nil {
			t.Fatalf("register stub gate: %v", err)
		}
	})
	defer gateCls.Stop()

	// 模拟 lobby 的客户端节点（节点 1.2.250）
	_, lobbyCls := startNode(t, "1.2.250", "lobbysvr", func(app *application.Application) {})
	defer lobbyCls.Stop()

	if !waitForType(lobbyCls, "onlinesvr", 5*time.Second) {
		t.Fatal("onlinesvr not discovered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. 注册（gateway=1.1.201）
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.201", LobbyNodeId: "1.2.250"},
		&onlinepb.RPC_Register_Rsp{})

	// 2. Query 命中
	var q onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001}, &q)
	if !q.Online || q.Entry.GatewayNodeId != "1.1.201" {
		t.Fatalf("query: %+v", &q)
	}

	// 3. 同 uid 从另一 gateway 再注册 → 顶号下发到旧 gateway(1.1.201)
	var r2 onlinepb.RPC_Register_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 10001, GatewayNodeId: "1.1.202", LobbyNodeId: "1.2.250"}, &r2)
	if !r2.KickedOld {
		t.Fatal("expected kicked_old on dup login")
	}
	// 等顶号 Cast 异步到达 stub gateway
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sg.kickedUID.Load() != 10001 {
		time.Sleep(50 * time.Millisecond)
	}
	if sg.kickedUID.Load() != 10001 {
		t.Fatal("stub gateway did not receive kick notify")
	}

	// 4. Unregister → Query 落空
	callOnline(t, ctx, lobbyCls, "OnlineHandler.unregister", &onlinepb.RPC_Unregister_Req{Uid: 10001}, &onlinepb.RPC_Unregister_Rsp{})
	var q2 onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001}, &q2)
	if q2.Online {
		t.Fatal("should be offline after unregister")
	}

	// 5. 过期：注册后等 > TTL，Query 落空
	callOnline(t, ctx, lobbyCls, "OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: 20002, GatewayNodeId: "1.1.201", LobbyNodeId: "1.2.250"}, &onlinepb.RPC_Register_Rsp{})
	time.Sleep(1500 * time.Millisecond) // > 500ms TTL + tick
	var q3 onlinepb.RPC_Query_Rsp
	callOnline(t, ctx, lobbyCls, "OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 20002}, &q3)
	if q3.Online {
		t.Fatal("entry should expire after TTL")
	}
}

func callOnline(t *testing.T, ctx context.Context, c *transport.NatsCluster, route string, req, rsp proto.Message) {
	t.Helper()
	data, err := c.CallAnySync(ctx, "onlinesvr", route, req)
	if err != nil {
		t.Fatalf("call %s: %v", route, err)
	}
	if err := proto.Unmarshal(data, rsp); err != nil {
		t.Fatalf("unmarshal %s rsp: %v", route, err)
	}
}
```

- [ ] **Step 2: stub gateway handler 需要 GateHandler 类型**

测试用 `&GateHandler{stub: sg}` 生成 route `GateHandler.kicksession`。在 onlinesvr internal 包内**新增测试辅助类型**（同文件即可，仅 integration tag 下编译），避免 import gatesvr internal：
```go
// GateHandler 测试桩：仅用于生成 "GateHandler.kicksession" route，验证顶号下发。
type GateHandler struct{ stub *stubGate }

func (h *GateHandler) KickSession(ctx context.Context, raw []byte) { h.stub.KickSession(ctx, raw) }
```

- [ ] **Step 3: 编译验证（无 Docker，编译即可）**

Run: `go vet -tags integration ./src/servers/onlinesvr/...`
Expected: 通过（无法实跑，需 Docker 环境 `go test -tags integration ./src/servers/onlinesvr/internal/ -run TestOnline_RegisterQueryKick_EndToEnd -v`）。

- [ ] **Step 4: 提交**

```bash
git add src/servers/onlinesvr/internal/online_integration_test.go
git commit -m "test(p2): onlinesvr 集成测试（注册/查询/顶号/注销/过期）"
```

---

# Phase E — routersvr

## Task 10: RouterModule（目标解析）+ RouterHandler（转发）

**Files:**
- Create: `src/servers/routersvr/internal/router_module.go`
- Create: `src/servers/routersvr/internal/router_handler.go`
- Test: `src/servers/routersvr/internal/router_handler_test.go`

- [ ] **Step 1: 写失败测试**

`src/servers/routersvr/internal/router_handler_test.go`:
```go
package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	clusterpb "project/src/framework/cluster/pb"
)

// fakeDisc 提供成员列表
type fakeDisc struct{ nodes map[string][]*clusterpb.NodeInfo }

func (d *fakeDisc) ByType(typ string) []*clusterpb.NodeInfo { return d.nodes[typ] }

func TestRouterModule_ResolveConsistentHash(t *testing.T) {
	disc := &fakeDisc{nodes: map[string][]*clusterpb.NodeInfo{
		"onlinesvr": {{NodeId: "1.5.1"}, {NodeId: "1.5.2"}, {NodeId: "1.5.3"}},
	}}
	m := NewRouterModule(disc, nil)
	// 同 key 稳定解析到某个 online 实例
	id1, ok := m.Resolve("onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001")
	if !ok {
		t.Fatal("resolve should succeed with members")
	}
	id2, _ := m.Resolve("onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001")
	if id1 != id2 {
		t.Fatal("resolve must be stable for same key")
	}
	// 无成员 → 解析失败
	if _, ok := m.Resolve("matchsvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "x"); ok {
		t.Fatal("resolve should fail when no members")
	}
}

func TestRouterModule_ResolveDirect(t *testing.T) {
	m := NewRouterModule(&fakeDisc{}, nil)
	id, ok := m.Resolve("roomsvr", routerpb.RoutingMode_ROUTING_DIRECT, "1.7.3")
	if !ok {
		t.Fatal("direct resolve should parse nodeID")
	}
	if id.String() != "1.7.3" {
		t.Fatalf("direct nodeID = %s", id.String())
	}
}

func TestRouterHandler_ForwardNoTarget(t *testing.T) {
	m := NewRouterModule(&fakeDisc{}, nil) // 空 discovery
	h := NewRouterHandler(m)
	inner, _ := proto.Marshal(&onlinepb.RPC_Register_Req{Uid: 1})
	rsp, err := h.Forward(context.Background(), &routerpb.RPC_RouterForward_Req{
		RoutingMode: routerpb.RoutingMode_ROUTING_CONSISTENT_HASH,
		TargetType:  "onlinesvr", RoutingKey: "1",
		InnerRoute: "OnlineHandler.register", InnerData: inner,
	})
	if err != nil {
		t.Fatalf("forward err: %v", err)
	}
	if rsp.Code == 0 {
		t.Fatal("forward should report error code when no target")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/routersvr/internal/ -v`
Expected: 编译失败（`undefined: NewRouterModule` / `NewRouterHandler`）。

- [ ] **Step 3: 实现 RouterModule**

`src/servers/routersvr/internal/router_module.go`:
```go
package internal

import (
	routerpb "project/protocal/gen/router"
	"project/src/common/jumphash"
	"project/src/common/logger"
	clusterpb "project/src/framework/cluster/pb"
	"project/src/framework/cluster"
	"project/src/framework/module"
)

// Discoverer 提供按类型枚举实例（*transport.NatsCluster.Discovery() 满足）
type Discoverer interface {
	ByType(serverTypeName string) []*clusterpb.NodeInfo
}

// RouterModule 无状态：按 RoutingMode 把逻辑目标解析到具体实例。
// 一致性哈希成员每次从 discovery 实时读取（discovery 是成员真相，
// 由 watch+周期对账维护），用无状态 jumphash.Pick 选择，始终一致、无缓存。
type RouterModule struct {
	module.BaseModule
	disc Discoverer
	cls  cluster.Cluster
}

func NewRouterModule(disc Discoverer, cls cluster.Cluster) *RouterModule {
	return &RouterModule{disc: disc, cls: cls}
}

func (m *RouterModule) Name() string             { return "router" }
func (m *RouterModule) Cluster() cluster.Cluster { return m.cls }

func (m *RouterModule) Init() { logger.Info("router module initialized") }

// Resolve 把 {目标类型, 模式, key} 解析为具体目标 NodeID。
func (m *RouterModule) Resolve(targetType string, mode routerpb.RoutingMode, key string) (cluster.NodeID, bool) {
	switch mode {
	case routerpb.RoutingMode_ROUTING_CONSISTENT_HASH:
		nodes := m.disc.ByType(targetType)
		members := make([]string, 0, len(nodes))
		for _, n := range nodes {
			members = append(members, n.NodeId)
		}
		node, ok := jumphash.Pick(members, key)
		if !ok {
			return 0, false
		}
		id, err := cluster.ParseNodeID(node)
		return id, err == nil
	case routerpb.RoutingMode_ROUTING_DIRECT:
		id, err := cluster.ParseNodeID(key)
		return id, err == nil
	default:
		// ROUTING_ANY 等 P4（match/room）再实现
		return 0, false
	}
}
```

- [ ] **Step 4: 实现 RouterHandler**

`src/servers/routersvr/internal/router_handler.go`:
```go
package internal

import (
	"context"

	routerpb "project/protocal/gen/router"
	"project/src/common/logger"
)

// RouterHandler 唯一转发 handler（route="RouterHandler.forward"）。
// 解析目标实例 → 同步 relay（CallRawSync）→ 响应原样回传。
// 因 routersvr 开启 asyncDispatch，每条转发各跑 goroutine，互不串行。
type RouterHandler struct {
	module *RouterModule
}

func NewRouterHandler(m *RouterModule) *RouterHandler {
	return &RouterHandler{module: m}
}

func (h *RouterHandler) Forward(ctx context.Context, req *routerpb.RPC_RouterForward_Req) (*routerpb.RPC_RouterForward_Rsp, error) {
	target, ok := h.module.Resolve(req.TargetType, req.RoutingMode, req.RoutingKey)
	if !ok {
		logger.Warn("router: no target",
			logger.String("type", req.TargetType), logger.String("key", req.RoutingKey))
		return &routerpb.RPC_RouterForward_Rsp{Code: 1, ErrMsg: "no target for " + req.TargetType}, nil
	}
	respData, err := h.module.Cluster().CallRawSync(ctx, target, req.InnerRoute, req.InnerData)
	if err != nil {
		logger.Warn("router: forward failed",
			logger.String("target", target.String()), logger.String("route", req.InnerRoute), logger.Err(err))
		return &routerpb.RPC_RouterForward_Rsp{Code: 2, ErrMsg: err.Error()}, nil
	}
	return &routerpb.RPC_RouterForward_Rsp{Code: 0, InnerData: respData}, nil
}
```

- [ ] **Step 5: 运行通过**

Run: `go test ./src/servers/routersvr/internal/ -v`
Expected: PASS（3 个测试）。

- [ ] **Step 6: 提交**

```bash
git add src/servers/routersvr/internal/router_module.go src/servers/routersvr/internal/router_handler.go src/servers/routersvr/internal/router_handler_test.go
git commit -m "feat(p2): routersvr 目标解析（Jump Hash）与转发 handler"
```

---

## Task 11: routersvr main.go（AsyncDispatch）

**Files:**
- Create: `src/servers/routersvr/main.go`
- Create: `conf/router.yaml`

- [ ] **Step 1: main.go**

`src/servers/routersvr/main.go`:
```go
package main

import (
	"project/src/common/config"
	"project/src/common/logger"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/transport"
	"project/src/servers/routersvr/internal"
)

func main() {
	cfg := config.MustLoad("conf/router.yaml")

	log, _ := logger.NewZapDevelopment()
	logger.SetGlobal(log)

	self, err := cluster.ParseNodeID(cfg.Node.ID)
	if err != nil {
		panic(err)
	}
	// AsyncDispatch=true：入站转发并发处理，不串行
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints:  cfg.Cluster.Etcd.Endpoints,
		NatsURLs:       cfg.Cluster.Nats.URLs,
		SelfAddr:       cfg.Node.Addr,
		ServerTypeName: cfg.Node.ServerTypeName,
		AsyncDispatch:  true,
	})
	if err != nil {
		panic(err)
	}

	// 无状态转发：序列化器 protobuf（转发信封是 proto），不调 Frontend()
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).
		Build()

	mod := internal.NewRouterModule(cls.Discovery(), app.Cluster())
	app.Register(mod)
	if err := app.RegisterHandler(internal.NewRouterHandler(mod), nil); err != nil {
		panic(err)
	}

	app.Start()
	if err := cls.Init(); err != nil {
		panic(err)
	}
	defer cls.Stop()

	logger.Info("routersvr started", logger.String("nodeID", cfg.Node.ID))
	app.Run()
}
```

- [ ] **Step 2: conf/router.yaml**

`conf/router.yaml`:
```yaml
node:
  id: "1.6.1"
  server_type_name: "routersvr"
  addr: "0.0.0.0:8861"
cluster:
  etcd:
    endpoints: ["localhost:2379"]
  nats:
    urls: ["nats://localhost:4222"]
log:
  level: "info"
  dir: "./logs"
```

- [ ] **Step 3: 编译验证**

Run: `go build ./src/servers/routersvr/...`
Expected: 通过。`cls.Discovery()` 返回的 `*discovery.Discovery` 满足 `internal.Discoverer`（有 `ByType(string) []*clusterpb.NodeInfo`，注意 `discovery.NodeInfo = pb.NodeInfo` 别名）。

- [ ] **Step 4: 提交**

```bash
git add src/servers/routersvr/ conf/router.yaml
git commit -m "feat(p2): routersvr 启动入口（asyncDispatch 异步转发）"
```

---

## Task 12: routersvr 集成测试（≥2 router + 转发到 stub backend）

**Files:**
- Create: `src/servers/routersvr/internal/router_integration_test.go`

> 验证 router 机制：lobby 经 `routerclient` → 任一 router（起 2 实例）→ 一致性哈希定位到 stub「onlinesvr」实例 → 响应回传。stub backend 在测试内定义（避免 import onlinesvr internal）。

- [ ] **Step 1: 写集成测试**

`src/servers/routersvr/internal/router_integration_test.go`:
```go
//go:build integration

package internal

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/common/serialize/protobuf"
	"project/src/framework/application"
	"project/src/framework/cluster"
	"project/src/framework/cluster/routerclient"
	"project/src/framework/cluster/transport"
)

var (
	etcdEndpoints = []string{"localhost:2379"}
	natsURLs      = []string{"nats://localhost:4222"}
)

// OnlineHandler stub：回 Query，data 里带上自己 nodeID 以便断言"一致路由"。
type OnlineHandler struct{ nodeID string }

func (h *OnlineHandler) Query(_ context.Context, req *onlinepb.RPC_Query_Req) (*onlinepb.RPC_Query_Rsp, error) {
	return &onlinepb.RPC_Query_Rsp{Online: true, Entry: &onlinepb.OnlineEntry{
		Uid: req.Uid, GatewayNodeId: h.nodeID, // 借字段回传处理实例 nodeID
	}}, nil
}

func startNode(t *testing.T, id, typ string, async bool, register func(app *application.Application, cls *transport.NatsCluster)) *transport.NatsCluster {
	t.Helper()
	self, err := cluster.ParseNodeID(id)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	cls, err := transport.NewNatsCluster(self, transport.NatsClusterConfig{
		EtcdEndpoints: etcdEndpoints, NatsURLs: natsURLs,
		SelfAddr: "127.0.0.1:0", ServerTypeName: typ, AsyncDispatch: async,
	})
	if err != nil {
		t.Fatalf("cluster %s: %v", id, err)
	}
	app := application.NewBuilder().
		NodeID(id).NodeType(typ).
		Serializer("protobuf", protobuf.NewSerializer()).
		Cluster(cls).Build()
	register(app, cls)
	app.Start()
	if err := cls.Init(); err != nil {
		t.Fatalf("init %s: %v", id, err)
	}
	return cls
}

func TestRouter_ForwardConsistent_EndToEnd(t *testing.T) {
	// 2 个 stub online 实例
	for _, id := range []string{"1.5.1", "1.5.2"} {
		nodeID := id
		cls := startNode(t, id, "onlinesvr", false, func(app *application.Application, _ *transport.NatsCluster) {
			if err := app.RegisterHandler(&OnlineHandler{nodeID: nodeID}, nil); err != nil {
				t.Fatalf("register online stub: %v", err)
			}
		})
		defer cls.Stop()
	}
	// 2 个 router 实例（asyncDispatch）
	for _, id := range []string{"1.6.1", "1.6.2"} {
		cls := startNode(t, id, "routersvr", true, func(app *application.Application, c *transport.NatsCluster) {
			mod := NewRouterModule(c.Discovery(), app.Cluster())
			app.Register(mod)
			if err := app.RegisterHandler(NewRouterHandler(mod), nil); err != nil {
				t.Fatalf("register router handler: %v", err)
			}
		})
		defer cls.Stop()
	}
	// lobby 模拟节点
	lobbyCls := startNode(t, "1.2.250", "lobbysvr", false, func(app *application.Application, _ *transport.NatsCluster) {})
	defer lobbyCls.Stop()

	// 等发现 router 与 online
	if !waitForType(lobbyCls, "routersvr", 5*time.Second) || !waitForType(lobbyCls, "onlinesvr", 5*time.Second) {
		t.Fatal("router/online not discovered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 同一 uid 多次经 router 转发，应稳定落到同一 online 实例（一致性哈希）
	var first string
	for i := 0; i < 5; i++ {
		rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Query_Rsp](
			ctx, lobbyCls, "onlinesvr",
			routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, "10001",
			"OnlineHandler.query", &onlinepb.RPC_Query_Req{Uid: 10001})
		if err != nil {
			t.Fatalf("forward query: %v", err)
		}
		got := rsp.Entry.GatewayNodeId // stub 回传的处理实例 nodeID
		if got != "1.5.1" && got != "1.5.2" {
			t.Fatalf("unexpected online instance: %s", got)
		}
		if first == "" {
			first = got
		} else if got != first {
			t.Fatalf("consistent hash unstable: %s vs %s", got, first)
		}
	}
}

func waitForType(c *transport.NatsCluster, typ string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(c.Discovery().ByType(typ)) > 0 {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

var _ = proto.Marshal // 保留 proto import（如未直接使用）
```

- [ ] **Step 2: 编译验证**

Run: `go vet -tags integration ./src/servers/routersvr/...`
Expected: 通过（实跑需 Docker：`go test -tags integration ./src/servers/routersvr/internal/ -run TestRouter_ForwardConsistent_EndToEnd -v`）。若 `proto` 未被使用导致 vet 报未用 import，删掉最后一行与 import。

- [ ] **Step 3: 提交**

```bash
git add src/servers/routersvr/internal/router_integration_test.go
git commit -m "test(p2): routersvr 集成测试（≥2 实例 + 一致性哈希转发）"
```

---

# Phase F — gateway / lobby 接回

## Task 13: gate 传 nodeID/AgentMap，Login 附 ClusterSession

**Files:**
- Modify: `src/servers/gatesvr/internal/gate_module.go`
- Modify: `src/servers/gatesvr/internal/gate_handler.go`
- Modify: `src/servers/gatesvr/main.go`

- [ ] **Step 1: GateModule 加 nodeID/agentMap，构造签名变更**

`gate_module.go`：import 加 `"project/src/framework/agent"`；struct 加字段：
```go
	nodeID   string
	agents   *agent.Map
```
构造与访问器：
```go
func NewGateModule(nodeID string, sessions *session.Manager, cls cluster.Cluster, agents *agent.Map) *GateModule {
	return &GateModule{nodeID: nodeID, sessions: sessions, cls: cls, agents: agents}
}

// NodeID 本 gateway 节点 ID（点分），登录转发时填入 ClusterSession.FrontendId
func (g *GateModule) NodeID() string { return g.nodeID }

// Agents 返回 sessionID → Agent 索引，供顶号推送
func (g *GateModule) Agents() *agent.Map { return g.agents }
```

- [ ] **Step 2: main.go 传入 nodeID/AgentMap**

`gatesvr/main.go`：把
```go
	gateModule := internal.NewGateModule(app.Sessions(), app.Cluster())
```
改为
```go
	gateModule := internal.NewGateModule(cfg.Node.ID, app.Sessions(), app.Cluster(), app.AgentMap())
```

- [ ] **Step 3: gate.Login 附 ClusterSession**

`gate_handler.go`：import 加 `"project/src/framework/cluster"` 与 `clusterpb "project/src/framework/cluster/pb"`。把转发前的代码改为先注入 ClusterSession：
```go
	// 附 ClusterSession，让 lobby 拿到本 gateway nodeID（用于在线注册定位）
	ctx = cluster.WithSession(ctx, &clusterpb.ClusterSession{
		Id:         sessionID,
		Ip:         s.IP(),
		FrontendId: h.module.NodeID(),
	})
	data, err := h.module.Cluster().CallAnySync(ctx, "lobbysvr", "LobbyHandler.login",
		&lobbypb.RPC_Login_Req{Token: req.Token, Platform: req.Platform})
```
（其余逻辑不变。）

- [ ] **Step 4: 同步修正既有 gate 单测构造调用**

`gate_handler_test.go` 已有两处旧签名 `NewGateModule(mgr, fc)`，改新签名（补 nodeID 与 nil AgentMap）：
- `TestGateHandler_Login_BindsLobby`：`m := NewGateModule(mgr, fc)` → `m := NewGateModule("1.1.1", mgr, fc, nil)`
- `TestGateHandler_Login_LobbyRejects`：`NewGateHandler(NewGateModule(mgr, fc))` → `NewGateHandler(NewGateModule("1.1.1", mgr, fc, nil))`

Run: `go build ./... && go test ./src/servers/gatesvr/... -count=1`
Expected: 通过（既有两个登录测试 + 编译均过）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/gatesvr/
git commit -m "feat(p2): gate 持 nodeID/AgentMap，登录转发附 ClusterSession"
```

---

## Task 14: gate KickSession handler + 填 notifyPlayerOffline

**Files:**
- Modify: `src/servers/gatesvr/internal/gate_handler.go`（加 KickSession）
- Modify: `src/servers/gatesvr/internal/gate_module.go`（填 notifyPlayerOffline）
- Test: `src/servers/gatesvr/internal/kick_test.go`

- [ ] **Step 1: 写失败测试**

`src/servers/gatesvr/internal/kick_test.go`:
```go
package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	"project/src/framework/cluster"
	"project/src/framework/session"
)

// kickFakeCluster 捕获 Cast（断连通知）。命名带 kick 前缀避免与
// gate_handler_test.go 既有的 fakeCluster 冲突（同 package internal）。
type kickFakeCluster struct {
	cluster.Cluster
	castRoute  string
	castTarget cluster.NodeID
	castUID    int64
}

func (f *kickFakeCluster) Cast(_ context.Context, target cluster.NodeID, route string, msg proto.Message) error {
	f.castTarget, f.castRoute = target, route
	if n, ok := msg.(*lobbypb.RPC_PlayerDisconnect_Notify); ok {
		f.castUID = n.Uid
	}
	return nil
}

func TestNotifyPlayerOffline_CastsToBoundLobby(t *testing.T) {
	fc := &kickFakeCluster{}
	sm := session.NewManager()
	m := NewGateModule("1.1.1", sm, fc, nil)
	m.Init() // 构造 g.ctx（注入 cluster）

	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, 10001)
	s.BindNode("lobbysvr", "1.2.7")

	m.notifyPlayerOffline(s)

	if fc.castRoute != "LobbyHandler.playerdisconnect" || fc.castUID != 10001 {
		t.Fatalf("offline cast wrong: route=%s uid=%d", fc.castRoute, fc.castUID)
	}
	want, _ := cluster.ParseNodeID("1.2.7")
	if fc.castTarget != want {
		t.Fatalf("offline cast target=%v want %v", fc.castTarget, want)
	}
}

func TestKickSession_ClosesSessionByUID(t *testing.T) {
	sm := session.NewManager()
	m := NewGateModule("1.1.1", sm, &kickFakeCluster{}, nil) // agents=nil：无连接可推，仅验证 Close
	h := NewGateHandler(m)

	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, 10001)

	raw, _ := proto.Marshal(&onlinepb.RPC_KickSession_Notify{Uid: 10001, Reason: 1})
	h.KickSession(context.Background(), raw)

	if _, ok := sm.ByUID(10001); ok {
		t.Fatal("session should be closed after kick")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/gatesvr/internal/ -run 'TestNotifyPlayerOffline|TestKickSession' -v`
Expected: 编译失败（`KickSession` 未定义；`notifyPlayerOffline` 当前为桩，断言失败）。

- [ ] **Step 3: 填 notifyPlayerOffline**

`gate_module.go`：import 加 `lobbypb "project/protocal/gen/lobby"` 与 `"project/src/framework/cluster"`（cluster 已可能导入，去重）。把 `notifyPlayerOffline` 桩替换为：
```go
func (g *GateModule) notifyPlayerOffline(s *session.Session) {
	lobbyNode, ok := s.BoundNode("lobbysvr")
	if !ok {
		return
	}
	target, err := cluster.ParseNodeID(lobbyNode)
	if err != nil {
		logger.Warn("gate offline: bad lobby nodeID", logger.String("nodeID", lobbyNode))
		return
	}
	if err := g.cls.Cast(g.ctx, target, "LobbyHandler.playerdisconnect",
		&lobbypb.RPC_PlayerDisconnect_Notify{Uid: s.UID()}); err != nil {
		logger.Warn("gate offline: cast failed", logger.Int64("uid", s.UID()), logger.Err(err))
	}
}
```

- [ ] **Step 4: 加 KickSession handler**

`gate_handler.go`：import 加 `onlinepb "project/protocal/gen/online"`、`gatepb "project/protocal/gen/gate"`（已有）、`"project/src/common/serialize/json"`、`"project/src/common/serialize"`。包级变量与方法：
```go
const msgIDSCKick uint32 = 1004 // 对应 gate.proto SC_Kick.msg_id，保持同步

var kickSerializer serialize.Serializer = json.NewSerializer() // 推给客户端用 json（与连接侧一致）

// KickSession 处理 onlinesvr 直达的顶号通知（route="GateHandler.kicksession"）。
// 用 raw []byte 入参：cluster 字节是 proto，而 gate registry 序列化器是 json，
// 故手动 proto.Unmarshal 绕开序列化器（见计划"关键设计事实#1"）。
func (h *GateHandler) KickSession(_ context.Context, raw []byte) {
	var req onlinepb.RPC_KickSession_Notify
	if err := proto.Unmarshal(raw, &req); err != nil {
		logger.Warn("gate kick: unmarshal failed", logger.Err(err))
		return
	}
	s, ok := h.module.Sessions().ByUID(req.Uid)
	if !ok {
		return // 连接可能已在别处断开
	}
	if h.module.Agents() != nil {
		if ag, ok := h.module.Agents().Load(s.ID()); ok {
			if body, err := kickSerializer.Marshal(&gatepb.SC_Kick{Reason: req.Reason, Message: "logged in elsewhere"}); err == nil {
				_ = ag.Push(msgIDSCKick, body)
			}
		}
	}
	logger.Info("gate kick: closing old session", logger.Int64("uid", req.Uid))
	h.module.Sessions().Close(s)
}
```

- [ ] **Step 5: 运行通过 + 编译**

Run: `go test ./src/servers/gatesvr/internal/ -count=1 && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 6: 提交**

```bash
git add src/servers/gatesvr/internal/
git commit -m "feat(p2): gate 顶号 KickSession handler + 断连通知 lobby"
```

---

## Task 15: lobby.Login 注册 online

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`
- Modify: `src/servers/lobbysvr/main.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`

- [ ] **Step 1: 写失败测试**

> 注意：`lobby_handler_test.go` **已存在**（含 `TestLobbyHandler_Login_OK`/`_EmptyToken`，调用旧签名 `NewLobbyHandler("1.2.1")`）。本步骤**整体替换该文件**：更新既有两处构造调用（补 noop cls）+ 追加 fake 与新测试。改后完整内容：

`src/servers/lobbysvr/internal/lobby_handler_test.go`:
```go
package internal

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
	onlinepb "project/protocal/gen/online"
	routerpb "project/protocal/gen/router"
	"project/src/framework/cluster"
	clusterpb "project/src/framework/cluster/pb"
)

func TestLobbyHandler_Login_OK(t *testing.T) {
	// online 注册为 best-effort：noop cluster 调用失败被吞，登录仍成功
	h := NewLobbyHandler("1.2.1", cluster.NewNoopCluster())
	rsp, err := h.Login(context.Background(), &lobbypb.RPC_Login_Req{Token: "valid", Platform: "ios"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code != 0 || rsp.Uid != 10001 || rsp.LobbyNodeId != "1.2.1" {
		t.Fatalf("unexpected rsp: %+v", rsp)
	}
}

func TestLobbyHandler_Login_EmptyToken(t *testing.T) {
	h := NewLobbyHandler("1.2.1", cluster.NewNoopCluster())
	rsp, err := h.Login(context.Background(), &lobbypb.RPC_Login_Req{Token: ""})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rsp.Code == 0 {
		t.Fatalf("expected non-zero code for empty token, got %+v", rsp)
	}
}

// lobbyFakeCluster 捕获经 router 的转发（CallAnySync 到 routersvr），回预设响应。
// 命名带 lobby 前缀以与其他包的同类 fake 区分。
type lobbyFakeCluster struct {
	cluster.Cluster
	gotType, gotRoute string
	gotEnv            *routerpb.RPC_RouterForward_Req
}

func (f *lobbyFakeCluster) CallAnySync(_ context.Context, serverType, route string, req proto.Message) ([]byte, error) {
	f.gotType, f.gotRoute = serverType, route
	f.gotEnv = req.(*routerpb.RPC_RouterForward_Req)
	innerRsp, _ := proto.Marshal(&onlinepb.RPC_Register_Rsp{Code: 0})
	return proto.Marshal(&routerpb.RPC_RouterForward_Rsp{Code: 0, InnerData: innerRsp})
}

func TestLobbyLogin_RegistersOnlineViaRouter(t *testing.T) {
	fc := &lobbyFakeCluster{}
	h := NewLobbyHandler("1.2.1", fc)
	// 注入 gateway nodeID 的 ClusterSession（模拟 gate 转发）
	ctx := cluster.WithSession(context.Background(), &clusterpb.ClusterSession{FrontendId: "1.1.9"})

	rsp, err := h.Login(ctx, &lobbypb.RPC_Login_Req{Token: "validtoken"})
	if err != nil || rsp.Code != 0 || rsp.Uid != 10001 || rsp.LobbyNodeId != "1.2.1" {
		t.Fatalf("login rsp: %+v err=%v", rsp, err)
	}
	if fc.gotType != "routersvr" || fc.gotRoute != "RouterHandler.forward" {
		t.Fatalf("should forward via router: %s/%s", fc.gotType, fc.gotRoute)
	}
	if fc.gotEnv.TargetType != "onlinesvr" || fc.gotEnv.InnerRoute != "OnlineHandler.register" {
		t.Fatalf("envelope target wrong: %+v", fc.gotEnv)
	}
	var inner onlinepb.RPC_Register_Req
	if err := proto.Unmarshal(fc.gotEnv.InnerData, &inner); err != nil {
		t.Fatalf("inner unmarshal: %v", err)
	}
	if inner.Uid != 10001 || inner.GatewayNodeId != "1.1.9" || inner.LobbyNodeId != "1.2.1" {
		t.Fatalf("register req wrong: %+v", &inner)
	}
	if fc.gotEnv.RoutingKey != "10001" || fc.gotEnv.RoutingMode != routerpb.RoutingMode_ROUTING_CONSISTENT_HASH {
		t.Fatalf("routing wrong: key=%s mode=%v", fc.gotEnv.RoutingKey, fc.gotEnv.RoutingMode)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyLogin -v`
Expected: 编译失败（`NewLobbyHandler` 只接受 1 个参数）。

- [ ] **Step 3: 改 LobbyHandler**

`lobby_handler.go`：import 加 `"strconv"`、`onlinepb "project/protocal/gen/online"`、`routerpb "project/protocal/gen/router"`、`"project/src/framework/cluster"`、`"project/src/framework/cluster/routerclient"`。改 struct 与构造：
```go
type LobbyHandler struct {
	nodeID string
	cls    cluster.Cluster
}

func NewLobbyHandler(nodeID string, cls cluster.Cluster) *LobbyHandler {
	return &LobbyHandler{nodeID: nodeID, cls: cls}
}
```
改 `Login`（保留 token stub，新增在线注册）：
```go
func (h *LobbyHandler) Login(ctx context.Context, req *lobbypb.RPC_Login_Req) (*lobbypb.RPC_Login_Rsp, error) {
	uid, ok := verifyToken(req.Token)
	if !ok {
		logger.Warn("lobby login: invalid token", logger.String("platform", req.Platform))
		return &lobbypb.RPC_Login_Rsp{Code: -1}, nil
	}

	// 取 gateway nodeID（gate 转发时附在 ClusterSession 上）
	var gatewayNodeID string
	if cs := cluster.SessionFromCtx(ctx); cs != nil {
		gatewayNodeID = cs.FrontendId
	}

	// 经 router 向 onlinesvr 注册在线（含顶号）。best-effort：失败不阻断登录，
	// online 是易失在线态，下次活跃/重连可重建（见 spec §10 风险）。
	if _, err := routerclient.CallViaSync[*onlinepb.RPC_Register_Rsp](
		ctx, h.cls, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(uid, 10),
		"OnlineHandler.register",
		&onlinepb.RPC_Register_Req{Uid: uid, GatewayNodeId: gatewayNodeID, LobbyNodeId: h.nodeID},
	); err != nil {
		logger.Warn("lobby login: online register failed", logger.Int64("uid", uid), logger.Err(err))
	}

	logger.Info("lobby login ok", logger.Int64("uid", uid), logger.String("node", h.nodeID))
	return &lobbypb.RPC_Login_Rsp{Code: 0, Uid: uid, LobbyNodeId: h.nodeID}, nil
}
```

- [ ] **Step 4: main.go 传 cls**

`lobbysvr/main.go`：把
```go
	if err := app.RegisterHandler(internal.NewLobbyHandler(cfg.Node.ID), nil); err != nil {
```
改为
```go
	if err := app.RegisterHandler(internal.NewLobbyHandler(cfg.Node.ID, app.Cluster()), nil); err != nil {
```

- [ ] **Step 5: 运行通过 + 编译**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyLogin -count=1 && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 6: 修 P1 集成测试构造调用**

`lobbysvr/internal/login_integration_test.go` 里 `NewLobbyHandler("1.2.1")` 改为 `NewLobbyHandler("1.2.1", lobbyCls)`（lobbyCls 已在上文构造）。
Run: `go vet -tags integration ./src/servers/lobbysvr/...`
Expected: 通过。

- [ ] **Step 7: 提交**

```bash
git add src/servers/lobbysvr/
git commit -m "feat(p2): lobby 登录经 router 注册 online（接回顶号）"
```

---

## Task 16: lobby PlayerDisconnect handler（断连注销）

**Files:**
- Modify: `src/servers/lobbysvr/internal/lobby_handler.go`
- Test: `src/servers/lobbysvr/internal/lobby_handler_test.go`（加用例）

- [ ] **Step 1: 写失败测试**

在 `lobby_handler_test.go` 追加（复用 Task 15 的 `lobbyFakeCluster`）：
```go
func TestLobbyPlayerDisconnect_UnregistersOnline(t *testing.T) {
	fc := &lobbyFakeCluster{}
	h := NewLobbyHandler("1.2.1", fc)
	h.PlayerDisconnect(context.Background(), &lobbypb.RPC_PlayerDisconnect_Notify{Uid: 10001})

	if fc.gotEnv == nil || fc.gotEnv.TargetType != "onlinesvr" || fc.gotEnv.InnerRoute != "OnlineHandler.unregister" {
		t.Fatalf("should unregister via router: %+v", fc.gotEnv)
	}
	if fc.gotEnv.RoutingKey != "10001" {
		t.Fatalf("routing key = %s", fc.gotEnv.RoutingKey)
	}
}
```
> 说明：`lobbyFakeCluster.CallAnySync` 固定回 `RPC_Register_Rsp{Code:0}`（marshal 为空），但 `routerclient` 仅校验外层 `Code==0` 且对空 `InnerData` 跳过反序列化，断言只看 `gotEnv`，故无需为 unregister 改 fake。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestLobbyPlayerDisconnect -v`
Expected: 编译失败（`PlayerDisconnect` 未定义）。

- [ ] **Step 3: 实现 handler**

`lobby_handler.go` 追加：
```go
// PlayerDisconnect 处理 gateway 断连通知（route="LobbyHandler.playerdisconnect"，单向）：
// 经 router 向 onlinesvr 注销在线。
func (h *LobbyHandler) PlayerDisconnect(ctx context.Context, req *lobbypb.RPC_PlayerDisconnect_Notify) {
	if _, err := routerclient.CallViaSync[*onlinepb.RPC_Unregister_Rsp](
		ctx, h.cls, "onlinesvr",
		routerpb.RoutingMode_ROUTING_CONSISTENT_HASH, strconv.FormatInt(req.Uid, 10),
		"OnlineHandler.unregister",
		&onlinepb.RPC_Unregister_Req{Uid: req.Uid},
	); err != nil {
		logger.Warn("lobby disconnect: online unregister failed", logger.Int64("uid", req.Uid), logger.Err(err))
	}
}
```

- [ ] **Step 4: 运行通过 + 全量回归**

Run: `go build ./... && go vet ./... && go test ./src/... -count=1`
Expected: 全绿。

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/
git commit -m "feat(p2): lobby 断连经 router 注销 online"
```

---

# 收尾验收（执行完所有 Task 后）

- [ ] **全量构建测试**

Run:
```bash
go build ./...
go vet ./...
go test ./src/... -count=1
go vet -tags integration ./src/...
```
Expected: 全部通过（集成测试仅编译；实跑需 Docker 机器 `docker compose -f test/docker-compose.yaml up -d` 后 `go test -tags integration ./src/servers/onlinesvr/internal/ ./src/servers/routersvr/internal/ -v`）。

- [ ] **对照 spec 验收口径（§10）**逐项核对：jumphash/router/online/session 单测齐全；router≥2 实例一致性哈希转发、online 注册/查询/顶号/注销/过期集成测试齐全；Register/Unregister/Touch 幂等。

- [ ] **文档欠账**：spec §10.3 要求同步 `cluster.md`（新增 router/online 与 asyncDispatch/CallRawSync）、`development.md`（routersvr/onlinesvr 构建测试命令、新增 proto 步骤、conf/router.yaml·conf/online.yaml）。本阶段可在 PR 描述中记为欠账，统一 P5 收口（spec §0.5）。

- [ ] **开 PR**（rebase 最新 origin/main 后）：`feat/p2-router-online` → `main`，PR 描述列出交付物（spec §13）与文档欠账。
