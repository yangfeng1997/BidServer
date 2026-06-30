# P5 B1+B7 请求生命周期 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐 Task 实现。步骤用 checkbox（`- [ ]`）跟踪。
> 关联设计 Spec：[`2026-06-06-P5-B1B7-request-lifecycle.md`](2026-06-06-P5-B1B7-request-lifecycle.md)。

**Goal:** 给 gate 每条连接绑定可取消 ctx（断连即取消在途下游工作），停机时主动强关所有活动连接（TCP 与 WS 一致地迅速 drain），并删除从不被读的半成品 in-flight drain 机器。

**Architecture:** 方案 B「关闭 + 取消」。`connAgent` 持 `ctx`/`cancel`（`Close()` 取消、`handleData` 用之）；`Application.Stop()` 经 `agent.Map.Range` 逐连接 `Close()`；移除 `pendingRequests`/`requestsDone`/`AcquireRequest`/`ReleaseRequest`。后端在途正确性仍由模块 `OnStop` + `cls.Stop()` 的 `conn.Drain()` 保证。

**Tech Stack:** Go、`context`、`sync.Map`（经 `syncmap` 泛型封装）、既有 `agent`/`handler`/`application` 框架包。

---

## 文件结构

| 文件 | 责任 | 改动 |
|---|---|---|
| `src/framework/agent/map.go` | sessionID→Agent 索引 | 增 `Range` |
| `src/framework/agent/agent.go` | 连接业务抽象 | 增 `ctx`/`cancel`（B7）；删 drain 字段/方法 + `Agent` 接口两声明 |
| `src/framework/agent/factory.go` | 连接工厂 | `NewAgent` 建 `ctx`/`cancel`；删 `requestsDone` init |
| `src/framework/handler/registry.go` | 反射路由 + Dispatch | `SessionProvider` 删两声明；`Dispatch` 删 Acquire/Release 调用 |
| `src/framework/application/application.go` | 进程生命周期编排 | `Stop()` 遍历 AgentMap 强关连接（B1） |
| `src/framework/agent/agent_test.go` | agent 单测 | 扩 fake 支持 Close；新增 B7 ctx 取消测试 |
| `src/framework/agent/map_test.go` | Map 单测（新建） | `Range` 测试 |
| `src/framework/application/application_test.go` | application 单测 | 新增 Stop 关连接测试 + fakeAgent |

**Task 顺序（每个 commit 都编译通过 + 测试绿）：** 1 `Map.Range`（additive）→ 2 per-connection ctx（additive，B7）→ 3 删 drain 机器（atomic 删除）→ 4 `Stop()` 关连接（B1，依赖 Task 1 的 `Range`）。

---

### Task 1: `agent.Map.Range`

**Files:**
- Modify: `src/framework/agent/map.go`
- Test: `src/framework/agent/map_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `src/framework/agent/map_test.go`：

```go
package agent

import "testing"

func TestMap_RangeVisitsAll(t *testing.T) {
	m := NewMap()
	m.Store(1, nil)
	m.Store(2, nil)
	m.Store(3, nil)

	seen := map[int64]bool{}
	m.Range(func(id int64, _ Agent) bool {
		seen[id] = true
		return true
	})
	for _, id := range []int64{1, 2, 3} {
		if !seen[id] {
			t.Fatalf("Range missed key %d", id)
		}
	}
}

func TestMap_RangeEarlyStop(t *testing.T) {
	m := NewMap()
	m.Store(1, nil)
	m.Store(2, nil)

	count := 0
	m.Range(func(int64, Agent) bool {
		count++
		return false // 第一个就停
	})
	if count != 1 {
		t.Fatalf("Range should stop after f returns false, visited %d", count)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/framework/agent/ -run TestMap_Range -v`
Expected: 编译失败 `m.Range undefined`。

- [ ] **Step 3: 实现**

在 `src/framework/agent/map.go` 的 `Delete` 方法后追加：

```go
// Range 遍历所有连接，f 返回 false 时停止。委托底层 syncmap.Map.Range
// （sync.Map.Range 语义：允许遍历中删除当前 key，停机逐连接 Close 触发的
// agentMap.Delete 因此安全）。
func (m *Map) Range(f func(int64, Agent) bool) { m.m.Range(f) }
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/framework/agent/ -run TestMap_Range -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add src/framework/agent/map.go src/framework/agent/map_test.go
git commit -m "feat(framework): agent.Map 增 Range，供停机遍历连接（B1 前置）"
```

---

### Task 2: per-connection 可取消 ctx（B7）

**Files:**
- Modify: `src/framework/agent/agent.go`（struct 增 `ctx`/`cancel`；`Close()` 取消；`handleData` 用 `a.ctx`）
- Modify: `src/framework/agent/factory.go`（`NewAgent` 建 ctx）
- Test: `src/framework/agent/agent_test.go`（扩 fake + 新测试）

- [ ] **Step 1: 写失败测试**

在 `src/framework/agent/agent_test.go`：① 顶部 import 增 `"errors"`；② 把现有 `newForwardTestAgent` 扩成「可 Close」的完整 fake（增 `fakeConn` + 填 `conn`/`chDie`/`ctx`/`cancel`）；③ 新增 B7 测试。

替换现有 `newForwardTestAgent`（保留签名，补字段），并新增 `fakeConn`：

```go
// fakeConn 最小 ClientConn：仅 Close 被 connAgent.Close 调用，其余方法不触碰。
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// newForwardTestAgent 构造可测 connAgent：填 handleData 转发分支 + Close 会触碰的字段。
func newForwardTestAgent(forward map[uint32]string, resp map[uint32]uint32,
	fn func(context.Context, *ForwardContext)) *connAgent {
	sm := session.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	return &connAgent{
		conn:           fakeConn{},
		session:        sm.New("127.0.0.1:1000"),
		sessions:       sm,
		chDie:          make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		forwardTable:   forward,
		respMsgIDTable: resp,
		forwardFn:      fn,
	}
}
```

import 块需含 `"net"`（fakeConn 嵌入 `net.Conn`）。新增测试：

```go
func TestHandleData_CtxCanceledOnClose(t *testing.T) {
	var captured context.Context
	a := newForwardTestAgent(
		map[uint32]string{42: "lobbysvr"},
		map[uint32]uint32{42: 43},
		func(ctx context.Context, _ *ForwardContext) { captured = ctx },
	)
	body, err := message.Encode(message.NewRequest(7, 42, []byte("p")))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := a.handleData(body); err != nil {
		t.Fatalf("handleData: %v", err)
	}
	if captured == nil {
		t.Fatal("forwardFn not called")
	}
	if captured.Err() != nil {
		t.Fatalf("ctx should be live before Close, got %v", captured.Err())
	}
	_ = a.Close()
	if !errors.Is(captured.Err(), context.Canceled) {
		t.Fatalf("ctx should be canceled after Close, got %v", captured.Err())
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/framework/agent/ -run TestHandleData_CtxCanceledOnClose -v`
Expected: 编译失败 `unknown field ctx`/`cancel`（struct 尚无字段）。

- [ ] **Step 3: 实现 connAgent 字段 + Close 取消 + handleData 用 a.ctx**

`src/framework/agent/agent.go`，在 struct 的「握手配置」段前（即 `onCloseCallbacks` 一组附近）新增字段：

```go
	// 每连接生命周期 ctx：连接关闭（断连/写错误/心跳超时/停机强关）时取消，
	// 令在途下游工作（gate 转发 CallRaw、尊重 ctx 的本地 handler）迅速中止。
	ctx    context.Context
	cancel context.CancelFunc
```

`Close()` 内 `close(a.chDie)` 之后立即取消（`once` 保证只取消一次）：

```go
func (a *connAgent) Close() error {
	a.once.Do(func() {
		a.setStatus(statusClosed)
		close(a.chDie)
		a.cancel()
		a.conn.Close()
		a.sessions.Close(a.session)
		for _, cb := range a.onCloseCallbacks {
			a.safeCloseCallback(cb)
		}
		// 无在途请求时立即通知 draining
		if a.pendingRequests.Load() == 0 {
			select {
			case a.requestsDone <- struct{}{}:
			default:
			}
		}
	})
	return nil
}
```

（drain 那段本 Task 暂留，Task 3 删。）

`handleData` 把 `ctx := context.Background()` 改为：

```go
	ctx := a.ctx
```

- [ ] **Step 4: 实现 factory 建 ctx**

`src/framework/agent/factory.go` 的 `NewAgent`，在 `f.wg.Add(1)` 后、构造 `ag` 前建 ctx，并把字段填入字面量：

```go
	f.wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	ag := &connAgent{
		conn:           conn,
		session:        s,
		sessions:       f.sessions,
		chSend:         make(chan []byte, sendChanSize),
		chDie:          make(chan struct{}),
		requestsDone:   make(chan struct{}, 1),
		ctx:            ctx,
		cancel:         cancel,
		wg:             f.wg,
		registry:       f.registry,
		validators:     f.validators,
		heartbeatSec:   f.heartbeatSec,
		serializerName: f.serializerName,
		msgRouteTable:  f.msgRouteTable,
		forwardTable:   f.forwardTable,
		respMsgIDTable: f.respMsgIDTable,
		forwardFn:      f.forwardFn,
	}
```

`factory.go` 已 import `"context"`（现有 SetRouteTables 用），无需新增。

- [ ] **Step 5: 运行确认通过 + 全 agent 包回归**

Run: `go test ./src/framework/agent/ -race -v`
Expected: 新测试 + 3 个既有 `TestHandleData_*` 全 PASS（既有 fake 现也带 ctx）。

- [ ] **Step 6: Commit**

```bash
git add src/framework/agent/agent.go src/framework/agent/factory.go src/framework/agent/agent_test.go
git commit -m "feat(framework): connAgent 每连接可取消 ctx，断连即取消在途下游工作（B7）"
```

---

### Task 3: 删除半成品 in-flight drain 机器

**Files:**
- Modify: `src/framework/agent/agent.go`（删 `Agent` 接口两声明、struct 两字段、两方法、`Close` 内 drain 段）
- Modify: `src/framework/agent/factory.go`（删 `requestsDone` init）
- Modify: `src/framework/handler/registry.go`（删 `SessionProvider` 两声明 + `Dispatch` 两调用）

> 纯删除（删的全是不被读/不承重的代码）：无新增失败测试，回归即「编译通过 + 既有全套测试绿」。已 grep 核实四处之外无引用、`Agent`/`SessionProvider` 仅 `connAgent` 实现、无测试 mock 定义这两方法。

- [ ] **Step 1: 删 `Agent` 接口声明**

`agent.go` 的 `Agent` interface 内删去：

```go
	AcquireRequest()
	ReleaseRequest()
```

- [ ] **Step 2: 删 connAgent 字段**

`agent.go` struct 内删去（保留 `onCloseCallbacks` 及其注释）：

```go
	pendingRequests  atomic.Int64
	requestsDone     chan struct{}
```

- [ ] **Step 3: 删两方法**

`agent.go` 删去整段 `AcquireRequest` 与 `ReleaseRequest`（含其上方注释 `// AcquireRequest ...` / `// ReleaseRequest ...`）。

- [ ] **Step 4: 删 `Close` 内 drain 段**

`Close()` 内删去：

```go
		// 无在途请求时立即通知 draining
		if a.pendingRequests.Load() == 0 {
			select {
			case a.requestsDone <- struct{}{}:
			default:
			}
		}
```

删后 `Close()` 末尾为 `for _, cb := range a.onCloseCallbacks { a.safeCloseCallback(cb) }`。

- [ ] **Step 5: 删 factory init**

`factory.go` 的 `NewAgent` 字面量内删去 `requestsDone: make(chan struct{}, 1),`（Task 2 新增的 `ctx`/`cancel` 保留）。

- [ ] **Step 6: 删 `SessionProvider` 声明 + `Dispatch` 调用**

`handler/registry.go` 的 `SessionProvider` interface 删去：

```go
	AcquireRequest()
	ReleaseRequest()
```

`Dispatch` 内删去（在 `ctx = injectSession(ctx, sp)` 之后）：

```go
	sp.AcquireRequest()
	defer sp.ReleaseRequest()
```

- [ ] **Step 7: 编译 + 全量回归**

Run: `go build ./... && go test ./src/framework/... -race`
Expected: 全绿。注意确认 `agent.go` 的 `sync/atomic` 仍被 `state`/`lastAt` 使用（不应产生未用 import）。

- [ ] **Step 8: Commit**

```bash
git add src/framework/agent/agent.go src/framework/agent/factory.go src/framework/handler/registry.go
git commit -m "refactor(framework): 删从不被读的半成品 in-flight drain 机器（requestsDone/Acquire/Release）"
```

---

### Task 4: `Application.Stop()` 强关活动连接（B1）

**Files:**
- Modify: `src/framework/application/application.go`（`Stop()` 增遍历 AgentMap 关连接）
- Test: `src/framework/application/application_test.go`（新增测试 + fakeAgent）

- [ ] **Step 1: 写失败测试**

`src/framework/application/application_test.go`：import 增 `"net"`、`"sync/atomic"`、`"project/src/common/serialize/json"`、`"project/src/framework/agent"`、`"project/src/framework/session"`。新增 fakeAgent + 测试：

```go
// fakeAgent 实现 agent.Agent，仅记录 Close 是否被调用。
type fakeAgent struct{ closed atomic.Bool }

func (f *fakeAgent) Session() *session.Session            { return nil }
func (f *fakeAgent) Push(uint32, []byte) error            { return nil }
func (f *fakeAgent) Response(uint32, uint32, []byte) error { return nil }
func (f *fakeAgent) ResponseErr(uint32, uint32, int32) error { return nil }
func (f *fakeAgent) Close() error                         { f.closed.Store(true); return nil }
func (f *fakeAgent) RemoteAddr() net.Addr                 { return nil }
func (f *fakeAgent) IsAlive() bool                        { return false }
func (f *fakeAgent) OnClose(func(*session.Session))       {}

func TestStop_ClosesLiveAgents(t *testing.T) {
	a := New(WithSerializer("json", json.NewSerializer()))
	fa := &fakeAgent{}
	a.AgentMap().Store(1, fa)

	done := make(chan struct{})
	go func() { a.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
	if !fa.closed.Load() {
		t.Fatal("Stop should close every live agent in AgentMap")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/framework/application/ -run TestStop_ClosesLiveAgents -v`
Expected: FAIL —— `fa.closed` 仍为 false（`Stop()` 当前不关 AgentMap 中的连接）。

- [ ] **Step 3: 实现**

`application.go` 的 `Stop()`，在 `for _, acc := range a.acceptors { acc.Stop() }` 之后、`done := make(chan struct{})` 之前插入：

```go
		// 主动关闭所有活动连接：TCP acceptor 的 Stop 只关 listener、不关已建连接，
		// 不主动关则读循环挂在 ReadPacket 上直到 shutdownTimeout。Close 幂等（once），
		// 对已被 WS srv.Close() 关掉的连接是 no-op；Close 同时取消每连接 ctx，
		// 中止在途转发。OnClose 回调（agentMap.Delete / notifyPlayerOffline 的 Cast）
		// 不阻塞，故此遍历迅速。
		if a.agentFac != nil {
			a.agentFac.AgentMap().Range(func(_ int64, ag agent.Agent) bool {
				_ = ag.Close()
				return true
			})
		}
```

`application.go` 已 import `"project/src/framework/agent"`（现有 `agent.Factory`/`agent.ForwardContext` 用），无需新增。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/framework/application/ -run TestStop_ClosesLiveAgents -race -v`
Expected: PASS。

- [ ] **Step 5: 全量回归**

Run: `go build ./... && go vet ./... && go vet -tags integration ./... && go test ./... -race`
Expected: 全绿（21 包）。

- [ ] **Step 6: Commit**

```bash
git add src/framework/application/application.go src/framework/application/application_test.go
git commit -m "feat(framework): 停机强关所有活动连接，TCP 与 WS 一致迅速 drain（B1）"
```

---

## 验证清单（终审前自检）

- [ ] B7：断连 → `a.ctx` 取消 → gate 转发 `CallRaw` 因 `context.Canceled` 迅速返回（非挂满 5s）；`handleData` 不再用 `context.Background()`。
- [ ] B1：`Stop()` 关掉 AgentMap 中每个连接；TCP 风格连接读循环退出、`agentWg` 在 `shutdownTimeout` 内归零。
- [ ] 删除：`AcquireRequest`/`ReleaseRequest`/`requestsDone`/`pendingRequests` 全仓无残留引用；`Agent`/`SessionProvider` 接口干净；`registry.Dispatch` 不再调用。
- [ ] 范围外未动：`NatsRPC.handleMessage` 的 ctx（deadline 传播）保持不变。
- [ ] 全量 `go build`/`go vet`/`go vet -tags integration`/`go test ./... -race` 全绿；gofmt 干净（不碰 main 既有 dirt）。

## 评审力度（沿用户对 B1+B7「最 meaty」要求）

- Task 2（B7 ctx）、Task 4（B1 停机）走独立 **spec + 质量双评审**子代理；
- Task 1（Range）、Task 3（删除）随核心一并复核（机械）；
- **整支 opus 终审**：断连→ctx 取消→在途中止链路；停机关连接 × `agentWg` drain × 模块 `OnStop` 顺序无竞态；删除无悬挂引用；`handleMessage` 未被误改。
- 全程 `-race`，agent/application 包 `-count` 压测稳。
