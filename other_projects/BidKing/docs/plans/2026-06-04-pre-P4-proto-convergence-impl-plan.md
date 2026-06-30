# pre-P4 加固 Implementation Plan（⑤ 全链路 proto 收敛 + ⑥ agent 转发单测）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把客户端↔gate↔后端的序列化边界统一为 **proto 全链路**（消除 P3b 遗留的 req/rsp 方向 json/proto 不一致），并补 `agent.handleData` 按绑定转发的单测覆盖。

**Architecture:** 当前客户端按 json 序列化、gate 不透明转发、后端却用 proto 解码 → req/rsp 方向潜在失败（无 Docker 从未实跑暴露）。本计划按用户决策 **C（全链路 proto）**收敛：把 gate Application 序列化器、gate `kickSerializer`、lobby `clientSerializer` 三处 json 换成 proto；req/rsp 方向因后端 registry 本就 proto 而自动打通；推送方向（presence/kick/login）body 改为 proto marshal。gate 仍是**不透明透传**，不做转码。

**Tech Stack:** Go；`google.golang.org/protobuf/proto`；`project/src/common/serialize/protobuf`；现有 `handler.Registry` / `agent` / `session` / `message`。

**关键设计事实（实读代码确认）：**
- gate `handleData`（`agent.go:274-319`）：非本地 route 的消息查 `forwardTable[msgID]` → 构 `ForwardContext` → `forwardFn`；`forwardFn`（`application.go:216-284`）对 Request `CallRaw(...fctx.Data...)`、对 OneWay `CastRaw`，把 client 字节**不透明**转发。
- 后端收包 `DispatchCluster → invokeCore`（`registry.go:198,210-242`）用**单一** registry 序列化器 `ser.Unmarshal/Marshal` 处理有类型 proto 入参；lobby/gate registry 序列化器由 `main.go` 的 `Serializer(...)` 决定。
- 故："客户端 json + 后端 proto registry" ⇒ req/rsp 双向都坏；只有 `IsRawArg []byte` 的 push/kick handler 绕过序列化器才能工作。改 gate 序列化器为 proto 后，req/rsp 自动一致。
- **无 Docker 约束**：`//go:build integration` 测试只能编译验证，实跑需容器；本计划全部用单元测试（沙箱可跑）。
- **客户端协调（范围外提醒）**：gate 握手将改为通告 `protobuf`，真实客户端须同步改用 proto 序列化；本计划只改服务端。

---

## File Structure

| 文件 | 责任 | 改动 |
|---|---|---|
| `src/servers/lobbysvr/internal/presence.go` | presence 推送 body 构造 | `clientSerializer` json→proto（Task 1） |
| `src/servers/lobbysvr/internal/presence_test.go` | presence 单测 | 扩 `fakePresence` 捕获 body + 加 proto 编码断言（Task 1） |
| `src/servers/gatesvr/internal/gate_handler.go` | gate 本地 handler + push/kick | `kickSerializer` json→proto + 注释（Task 2、Task 3） |
| `src/servers/gatesvr/internal/kick_test.go` | gate kick 单测 | 加 kick body proto 编码断言（Task 2） |
| `src/servers/gatesvr/main.go` | gate Application 装配 | `Serializer("json",…)`→`Serializer("protobuf",…)`（Task 3） |
| `src/framework/agent/agent_test.go` | **新建** agent 单测 | `handleData` 转发路径白盒单测（Task 4） |

> 序列化器保留命名 seam（`clientSerializer`/`kickSerializer`），现都指向 proto——最小 diff，且若未来客户端协议再分化只需改一行。

---

## Task 1: lobby presence 推送 body 改 proto（⑤a）

**Files:**
- Modify: `src/servers/lobbysvr/internal/presence.go:13` `:23`
- Test: `src/servers/lobbysvr/internal/presence_test.go`

- [ ] **Step 1: 写失败测试**

在 `presence_test.go` 把 `presencePush` 增加 `body` 字段、`Push` 捕获 body，并新增 proto 编码断言测试。先改 import 块与结构：

```go
package internal

import (
	"testing"

	"google.golang.org/protobuf/proto"

	lobbypb "project/protocal/gen/lobby"
)

// fakePresence 记录 Query/Push 调用，注入 Runtime 替换真实 router/gate 出站。
type fakePresence struct {
	online map[int64]string // uid → gatewayNodeID（在线）
	pushes []presencePush
}
type presencePush struct {
	gateway string
	uid     int64
	msgID   uint32
	body    []byte
}

func (f *fakePresence) Query(uid int64) (gatewayNodeID string, online bool) {
	gw, ok := f.online[uid]
	return gw, ok
}
func (f *fakePresence) Push(gatewayNodeID string, uid int64, msgID uint32, body []byte) {
	f.pushes = append(f.pushes, presencePush{gatewayNodeID, uid, msgID, body})
}
```

并在文件末尾追加：

```go
func TestFanoutPresence_BodyIsProtoEncoded(t *testing.T) {
	fp := &fakePresence{online: map[int64]string{2: "0.1.1"}}
	fanoutPresence(fp, 1, []int64{2}, true)
	if len(fp.pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(fp.pushes))
	}
	var sc lobbypb.SC_FriendPresence
	if err := proto.Unmarshal(fp.pushes[0].body, &sc); err != nil {
		t.Fatalf("body must be proto SC_FriendPresence, got unmarshal err: %v", err)
	}
	if sc.Uid != 1 || !sc.Online {
		t.Fatalf("decoded SC_FriendPresence wrong: uid=%d online=%v", sc.Uid, sc.Online)
	}
}

func TestNotifyNewMail_BodyIsProtoEncoded(t *testing.T) {
	fp := &fakePresence{online: map[int64]string{7: "0.1.1"}}
	notifyNewMail(fp, 7, 99, MailTypeFriendReq)
	if len(fp.pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(fp.pushes))
	}
	var sc lobbypb.SC_MailNew
	if err := proto.Unmarshal(fp.pushes[0].body, &sc); err != nil {
		t.Fatalf("body must be proto SC_MailNew, got unmarshal err: %v", err)
	}
	if sc.From != 99 || sc.Type != MailTypeFriendReq {
		t.Fatalf("decoded SC_MailNew wrong: from=%d type=%s", sc.From, sc.Type)
	}
}
```

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestFanoutPresence_BodyIsProtoEncoded|TestNotifyNewMail_BodyIsProtoEncoded' -v`
Expected: FAIL —— `clientSerializer` 仍是 json，body 是 json 字节，`proto.Unmarshal` 报错（或字段不符）。

- [ ] **Step 3: 改实现 —— `clientSerializer` 换 proto**

`presence.go` 顶部 import：把
```go
	"project/src/common/serialize/json"
```
换成
```go
	"project/src/common/serialize/protobuf"
```
并把
```go
var clientSerializer = json.NewSerializer() // 推送 body 用 client 序列化器(json)
```
改为
```go
var clientSerializer = protobuf.NewSerializer() // 推送 body 用 client 序列化器(proto，全链路 proto)
```

- [ ] **Step 4: 跑测试验证通过（含既有 presence 测试不回归）**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'Presence|NotifyNewMail|SnapshotFriends' -v`
Expected: PASS（新增 2 个 proto 断言 + 既有 `TestFanoutPresence_OnlyOnlineFriends` / `TestNotifyNewMail_*` / `TestSnapshotFriends_MarksOnline` 全绿）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/lobbysvr/internal/presence.go src/servers/lobbysvr/internal/presence_test.go
git commit -m "fix(lobby): presence 推送 body 改 proto（全链路 proto 收敛 ⑤a）"
```

---

## Task 2: gate kick 推送 body 改 proto（⑤b）

**Files:**
- Modify: `src/servers/gatesvr/internal/gate_handler.go:13` `:21` `:122-124`
- Test: `src/servers/gatesvr/internal/kick_test.go`

- [ ] **Step 1: 写失败测试**

`kick_test.go` 现有 `TestKickSession_ClosesSessionByUID` 用 `agents=nil`（不走 body 路径）。新增一个带真实 agents + `fakePushAgent`（定义在同包 `push_test.go`）捕获 kick body 的测试。先确保 import 含 `agent` 与 `gatepb`，在文件末尾追加：

```go
func TestKickSession_BodyIsProtoEncoded(t *testing.T) {
	sm := session.NewManager()
	agents := agent.NewMap()
	m := NewGateModule("1.1.1", sm, &kickFakeCluster{}, agents)
	h := NewGateHandler(m)

	uid := int64(10001)
	s := sm.New("127.0.0.1:1234")
	_ = sm.Bind(context.Background(), s, uid)
	ag := &fakePushAgent{}
	agents.Store(s.ID(), ag)

	raw, _ := proto.Marshal(&onlinepb.RPC_KickSession_Notify{Uid: uid, Reason: 1})
	h.KickSession(context.Background(), raw)

	if ag.lastPushMsgID != msgIDSCKick {
		t.Fatalf("kick push msgID=%d want %d", ag.lastPushMsgID, msgIDSCKick)
	}
	var sc gatepb.SC_Kick
	if err := proto.Unmarshal(ag.lastPushBody, &sc); err != nil {
		t.Fatalf("kick body must be proto SC_Kick, got unmarshal err: %v", err)
	}
	if sc.Reason != 1 {
		t.Fatalf("decoded SC_Kick reason=%d want 1", sc.Reason)
	}
}
```

在 `kick_test.go` 的 import 块补上（若尚无）：
```go
	gatepb "project/protocal/gen/gate"
	"project/src/framework/agent"
```
（`context`/`testing`/`proto`/`lobbypb`/`onlinepb`/`cluster`/`session` 已在；`lobbypb`、`cluster` 保留——`kickFakeCluster` 用到。）

- [ ] **Step 2: 跑测试验证失败**

Run: `go test ./src/servers/gatesvr/internal/ -run TestKickSession_BodyIsProtoEncoded -v`
Expected: FAIL —— `kickSerializer` 仍是 json，`proto.Unmarshal` 报错。

- [ ] **Step 3: 改实现 —— `kickSerializer` 换 proto + 注释**

`gate_handler.go` import：把
```go
	"project/src/common/serialize/json"
```
换成
```go
	"project/src/common/serialize/protobuf"
```
把
```go
var kickSerializer serialize.Serializer = json.NewSerializer() // 推给客户端用 json（与连接侧一致）
```
改为
```go
var kickSerializer serialize.Serializer = protobuf.NewSerializer() // 推给客户端用 proto（全链路 proto）
```
并把 `KickSession` 上方注释（`:122-124`）
```go
// KickSession 处理 onlinesvr 直达的顶号通知（route="GateHandler.kicksession"）。
// 用 raw []byte 入参：cluster 字节是 proto，而 gate registry 序列化器是 json，
// 故手动 proto.Unmarshal 绕开序列化器（见计划"关键设计事实#1"）。
```
改为
```go
// KickSession 处理 onlinesvr 直达的顶号通知（route="GateHandler.kicksession"）。
// 用 raw []byte 入参手动 proto.Unmarshal（与 PushToClient 一致，最小改动）；
// gate registry 现为 proto，SC_Kick body 亦按 proto marshal 后推给客户端。
```

- [ ] **Step 4: 跑测试验证通过（含既有 kick 测试不回归）**

Run: `go test ./src/servers/gatesvr/internal/ -run 'Kick|PushToClient|NotifyPlayerOffline' -v`
Expected: PASS（新增 proto 断言 + 既有 `TestKickSession_ClosesSessionByUID` / `TestPushToClient_PushesByUID` / `TestNotifyPlayerOffline_CastsToBoundLobby` 全绿）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/gatesvr/internal/gate_handler.go src/servers/gatesvr/internal/kick_test.go
git commit -m "fix(gate): kick 推送 body 改 proto（全链路 proto 收敛 ⑤b）"
```

---

## Task 3: gate Application 序列化器 json→proto（⑤c）

**Files:**
- Modify: `src/servers/gatesvr/main.go:7` `:37` `:42`
- Modify: `src/servers/gatesvr/internal/gate_handler.go:97-99`（PushToClient 注释，随 registry 变 proto 一并修正）

- [ ] **Step 1: 改 gate 序列化器为 proto**

`main.go` import：把
```go
	"project/src/common/serialize/json"
```
换成
```go
	"project/src/common/serialize/protobuf"
```
把
```go
	// 4. Application：前端节点，客户端侧 json 序列化
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Frontend(cfg.Node.Addr).
		Serializer("json", json.NewSerializer()).
```
改为
```go
	// 4. Application：前端节点，全链路 proto 序列化（握手通告 protobuf，客户端须用 proto）
	app := application.NewBuilder().
		NodeID(cfg.Node.ID).
		NodeType(cfg.Node.ServerTypeName).
		Frontend(cfg.Node.Addr).
		Serializer("protobuf", protobuf.NewSerializer()).
```

- [ ] **Step 2: 修正 PushToClient 注释（registry 现为 proto）**

`gate_handler.go` 把 `PushToClient` 上方注释（`:97-99`）
```go
// PushToClient 处理后端推送（route="GateHandler.pushtoclient"）。
// raw 入参同 KickSession：cluster 字节是 proto、gate registry 是 json，故手动 Unmarshal。
// body 已是 client 序列化器(json) 字节，按 uid 透传推给客户端。
```
改为
```go
// PushToClient 处理后端推送（route="GateHandler.pushtoclient"）。
// raw []byte 入参：cluster 信封是 proto，手动 Unmarshal 取出不透明 body。
// body 已是 client 序列化器(proto) 字节，按 uid 原样透传推给客户端（gate 不转码）。
```

- [ ] **Step 3: 构建 + vet 验证（main.go 无单测，靠 build/vet + 全量回归）**

Run: `go build ./... && go vet ./src/servers/gatesvr/...`
Expected: 无输出（成功）。`json` 包不再被 gatesvr 引用，`protobuf` 已引入，无悬挂 import。

- [ ] **Step 4: gate 全量单测不回归**

Run: `go test ./src/servers/gatesvr/... -count=1`
Expected: PASS（gate handler 单测用类型化入参、绕过序列化器，不受影响）。

- [ ] **Step 5: 提交**

```bash
git add src/servers/gatesvr/main.go src/servers/gatesvr/internal/gate_handler.go
git commit -m "fix(gate): Application 序列化器 json→proto（全链路 proto 收敛 ⑤c）

握手改为通告 protobuf；req/rsp 方向因后端 registry 本就 proto 而自动一致。
客户端须同步改用 proto（外部协调，超出本仓库范围）。"
```

---

## Task 4: agent.handleData 转发路径单测（⑥）

**Files:**
- Create: `src/framework/agent/agent_test.go`

> `agent/` 当前无任何测试。本任务为 `handleData` 转发分支补**白盒**特征测试（`package agent`），覆盖 P3b 遗留的转发地基测试空白。这些用例验证**既有行为**，预期首次即 PASS；若 FAIL 则暴露真实转发 bug。

- [ ] **Step 1: 写测试（新建文件）**

```go
package agent

import (
	"context"
	"testing"

	"project/src/framework/network/message"
	"project/src/framework/session"
)

// 构造最小可测 connAgent：仅填 handleData 转发分支会触碰的字段。
// conn/chSend/registry 等未填字段在转发成功路径上不被访问。
func newForwardTestAgent(forward map[uint32]string, resp map[uint32]uint32,
	fn func(context.Context, *ForwardContext)) *connAgent {
	sm := session.NewManager()
	return &connAgent{
		session:        sm.New("127.0.0.1:1000"),
		forwardTable:   forward,
		respMsgIDTable: resp,
		forwardFn:      fn,
	}
}

func TestHandleData_ForwardsBoundRequest(t *testing.T) {
	var got *ForwardContext
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		map[uint32]uint32{42: 43},
		func(_ context.Context, fctx *ForwardContext) { got = fctx },
	)
	body, err := message.Encode(message.NewRequest(7, 42, []byte("payload")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if got == nil {
		t.Fatal("forwardFn not called for bound msgID")
	}
	if got.ServerType != "lobbysvr" || got.MsgID != 42 || got.MID != 7 ||
		got.RespMsgID != 43 || got.MsgType != uint8(message.Request) ||
		string(got.Data) != "payload" {
		t.Fatalf("ForwardContext wrong: %+v", got)
	}
}

func TestHandleData_ForwardsBoundOneWay(t *testing.T) {
	var got *ForwardContext
	a := newForwardTestAgent(
		map[uint32]string{50: "roomsvr"},
		nil,
		func(_ context.Context, fctx *ForwardContext) { got = fctx },
	)
	body, err := message.Encode(message.NewOneWay(50, []byte("op")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if got == nil || got.ServerType != "roomsvr" || got.MsgID != 50 ||
		got.MID != 0 || got.MsgType != uint8(message.OneWay) || string(got.Data) != "op" {
		t.Fatalf("ForwardContext wrong: %+v", got)
	}
}

func TestHandleData_UnboundMsgIDNoForward(t *testing.T) {
	called := false
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		nil,
		func(_ context.Context, _ *ForwardContext) { called = true },
	)
	body, err := message.Encode(message.NewRequest(1, 999, nil))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if called {
		t.Fatal("forwardFn must not be called for unbound msgID")
	}
}
```

- [ ] **Step 2: 跑测试**

Run: `go test ./src/framework/agent/ -v`
Expected: PASS（三个用例验证既有转发行为）。**若任一 FAIL**：说明 `handleData` 转发分支有真实 bug，停下报告，不要改测试迁就实现。

- [ ] **Step 3: 提交**

```bash
git add src/framework/agent/agent_test.go
git commit -m "test(agent): 补 handleData 按绑定转发的白盒单测（⑥）"
```

---

## Task 5: 全量回归 + 集成测试编译验证

**Files:** 无（验证任务）

- [ ] **Step 1: 全量构建 + vet**

Run: `go build ./... && go vet ./...`
Expected: 无输出（成功）；无悬挂 json import / 未用变量。

- [ ] **Step 2: 全量单测**

Run: `go test ./src/... -count=1`
Expected: PASS（全绿）。

- [ ] **Step 3: 集成测试编译验证（无 Docker，仅编译）**

Run: `go build -tags integration ./src/...`
Expected: 成功编译（`//go:build integration` 测试不实跑——沙箱无容器，与 P3a/P3b 一致）。

- [ ] **Step 4:（可选）确认 json 包仅余应有引用**

Run: `grep -rn "serialize/json" src/ || echo "no json serializer references remain"`
Expected: 仅可能在框架通用层（如 `application`/测试 helper）保留对 json 包的中立引用；`gatesvr`/`lobbysvr` 业务侧不再引用 json 序列化器。若有遗漏，补正。

---

## Self-Review（写完计划后对照 spec 自检）

**1. Spec 覆盖（对照 §6 / §3.3 / §14）**
- ⑤（req/rsp json/proto 收敛，决策 C 全链路 proto）→ Task 1（presence proto）+ Task 2（kick proto）+ Task 3（gate 序列化器 proto，req/rsp 自动打通）。✓
- ⑥（agent.handleData D9 转发单测）→ Task 4。✓
- ①（claim-before-persist）**不在本计划**——已移 P4b 与 ④a 合并（spec §6 注）。✓ 无遗漏。

**2. Placeholder 扫描**：无 TBD/TODO；每个改动步骤都给了 import 与代码原文。✓

**3. 类型/命名一致性**：`clientSerializer`/`kickSerializer` 全程同名；`protobuf.NewSerializer()`、`proto.Unmarshal`、`message.NewRequest/NewOneWay/Encode`、`session.NewManager().New(addr)`、`agent.NewMap()`、`fakePushAgent`（push_test.go 定义，同包复用）、`kickFakeCluster`、`msgIDSCKick`、`msgIDSCFriendPresence`/`msgIDSCMailNew`、`MailTypeFriendReq`/`MailTypeNormal`——均与实读代码一致。✓

**4. 歧义检查**：gate 保持不透明透传（不转码）；后端 registry 始终 proto；序列化边界统一 proto——单一规则，无双向不对称。✓

---

## Execution Handoff

**Plan complete and saved to `docs/plans/2026-06-04-pre-P4-proto-convergence-impl-plan.md`. Two execution options:**

**1. Subagent-Driven（推荐）** —— 每个 Task 派新 subagent，任务间双阶段评审，快速迭代。

**2. Inline Execution** —— 本会话内按 `executing-plans` 批量执行 + 检查点评审。

**Which approach?**
