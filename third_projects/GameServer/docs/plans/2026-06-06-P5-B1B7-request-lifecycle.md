# P5 B1+B7 请求生命周期（断连 ctx 取消 + 停机强关连接）设计 Spec

> 关联：[`2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md) §9.4 框架风险 backlog（B1 draining / B7 每消息 `context.Background()` 无断连 cancel）。本切片把 B1、B7 合并为一个"请求生命周期"主题处理。

## 1. 背景与动机

§9.4 列出两项与请求生命周期相关的框架风险：

- **B7（确认真）**：`connAgent.handleData`（`src/framework/agent/agent.go`）对每条客户端消息构造 `ctx := context.Background()`，再传给本地 `registry.Dispatch` 与 gate 的 `forwardFn`。这个 ctx **与连接存活无任何关联**——客户端中途断连时，在途的转发 `cls.CallRaw(ctx, …)` 及一切尊重 ctx 的下游工作收不到取消信号，只能挂到默认 5s 超时才结束。agent 已有 `chDie`，存在干净的取消挂载点。
- **B1（确认真）**：`Application.Stop()` 停 acceptor 后等 `agentWg`（读 goroutine 的 WaitGroup）至多 10s。但：
  - **WS** 的 `acc.Stop()` 调 `http.Server.Close()` 会强关所有活动连接 → 读循环立即退出、`agentWg` 迅速归零；
  - **TCP** 的 `acc.Stop()` 只关 listener，**已建连接仍在**，读循环阻塞在 `ReadPacket` 上，`agentWg.Wait()` 必然阻塞到 10s 超时再"强制退出"。这是真实的不一致：**TCP 停机不优雅**。
  - `connAgent` 内 `pendingRequests`/`requestsDone` + `AcquireRequest`/`ReleaseRequest` 是一套**半成品、不承重**的"drain 在途"机器：计数器只在**同步的本地 `Dispatch`** 周围增减（`registry.go:143`），gate 转发路径**完全没接它**，且 `requestsDone` **写了但全仓无人读**。真正异步的在途工作（gate 转发 `CallRaw` 的 `done` 后到）根本没被跟踪。它给人"已实现优雅 drain"的错觉，是隐患。

**已确认的取舍（与用户敲定，方案 B "关闭 + 取消"）**：多 gate 部署的标准优雅重启是"LB 侧停新连 + 强断已有 → 客户端重连到别的 gate"，而非进程内请求 drain。后端在途正确性本就由各模块 `Runtime` 的 `OnStop`（lobby/room 主循环 drain）+ `cls.Stop()` 的 `conn.Drain()`（结清未完 NATS 请求）保证。因此本切片不补"先 drain 再关"的 draining 状态机（方案 A，未选），而是：
- 给每连接一个绑定连接存活的可取消 ctx（修 B7）；
- 停机时**主动强关所有活动连接**（让 TCP 与 WS 一致、并经 ctx 取消令在途转发快速中止）；
- **删掉**误导性的半成品 drain 机器。

## 2. 范围与非目标

### 范围内
- `connAgent` 每连接可取消 ctx（`Close()` 取消、`handleData` 改用之）。
- `Application.Stop()` 停机时遍历 AgentMap 强关所有活动连接；为此 `agent.Map` 增 `Range`。
- 移除半成品 drain 机器：`pendingRequests`、`requestsDone`、`AcquireRequest`/`ReleaseRequest`（`Agent` 接口、`SessionProvider` 接口、`registry.Dispatch` 调用点、factory init 一并清理）。

### 非目标（明确划出，避免"漏改"误读）
- **集群入站 `NatsRPC.handleMessage` 的 ctx**：现已从 `cm.Deadline` 还原调用方 **deadline**（`context.WithDeadline`），这是后端 RPC 能拿到的、有意义的取消传播；后端无"客户端连接"概念可绑，跨 NATS 的分布式取消令牌是另一个大特性。本切片**不动** `handleMessage` 的 ctx 构造。
- **"先 drain 再关"的 draining 状态机（方案 A）**：未选，不做。
- **accept-during-shutdown 窄窗**：acceptor 已 `Stop()`（connChan 关闭、消费循环退出）后，仍可能有"已从 connChan 取出但尚未进入我们 Range 的连接"漏网。此窄窗由现有 10s `shutdownTimeout` 兜底，**文档标注、本期不修**。

## 3. 逐项设计

### 3.1 B7 — 每连接可取消 ctx（核心，走 spec+质量双评审）

`connAgent` 新增两字段：

```go
type connAgent struct {
    ...
    ctx    context.Context
    cancel context.CancelFunc
    ...
}
```

- **创建**：`Factory.NewAgent` 内 `ctx, cancel := context.WithCancel(context.Background())`，赋给新建的 `connAgent`。每连接独立 ctx；不需要共享 app-root parent（停机经显式 `Close()` 逐个取消，见 3.2）。
- **取消**：`Close()` 的 `once.Do` 内、`close(a.chDie)` 之后调用 `a.cancel()`（一次性、幂等由 `once` 保证）。
- **使用**：`handleData` 把 `ctx := context.Background()` 改为直接用 `a.ctx`，传给 `registry.Dispatch` 与 `forwardFn`。

**效果**：连接关闭（客户端断连 / 写错误 / 心跳超时 / 停机强关）→ `a.ctx` 取消 → gate 转发 `cls.CallRaw(a.ctx, …)` 的 `RequestWithContext` 迅速返回 `context.Canceled` → `done(nil, err)` 记日志并跳过 `Response`（客户端已走，本就发不出）。本地 `Dispatch` 中尊重 ctx 的 handler 同样收到取消。

**注意**：`handleData` 仅在 **gate（唯一前端）** 上执行（后端经 cluster `handleMessage` 收消息），故本改动实际 blast radius 局限于 gate 入站路径，风险低。

### 3.2 B1 — 停机强关活动连接（触及停机生命周期，走聚焦 spec+质量复核）

**`agent.Map` 增 `Range`**（委托底层 `syncmap.Map.Range`，后者已具备）：

```go
func (m *Map) Range(f func(int64, Agent) bool) { m.m.Range(f) }
```

**`Application.Stop()`** 在停 acceptor 后、起 `agentWg.Wait()` 协程前，新增一步：遍历 AgentMap 逐个 `Close()`。

```go
for _, acc := range a.acceptors {
    acc.Stop()
}
// 主动关闭所有活动连接：TCP acceptor 的 Stop 只关 listener、不关已建连接，
// 不主动关则读循环挂在 ReadPacket 上直到 shutdownTimeout。Close 幂等（once），
// 对已被 WS srv.Close() 关掉的连接是 no-op。Close 同时取消每连接 ctx（见 3.1）。
a.agentFac.AgentMap().Range(func(_ int64, ag agent.Agent) bool {
    _ = ag.Close()
    return true
})
done := make(chan struct{})
go func() { a.agentWg.Wait(); close(done) }()
...
```

**顺序与并发**：先停 acceptor（不再有新连接经 connChan 进入），再 Range 强关。`Close` 内 `once.Do` 保证幂等；`Close` 会触发 OnClose 回调，即在 `Range` 回调里删 map——`syncmap.Map.Range` 委托标准库 `sync.Map.Range`（已核实），后者允许遍历中删除当前 key，安全。读循环退出后 `Handle` 走 `wg.Done()`，`agentWg` 归零，`done` 触发，停机继续。

**OnClose 回调在 Stop goroutine 同步执行但不阻塞**：`Close` 触发两类回调——agent 自身的 `agentMap.Delete`（O(1)），以及经 `sessions.Close` 触发的 session 级回调（gate 注册的 `notifyPlayerOffline` 用 `cls.Cast` 异步发，即 NATS Publish，不等返回，已核实）。故逐连接 `Close` 的 `Range` 不会卡在跨节点 RPC 上，停机迅速。此时 `cls` 仍在线（`cls.Stop()` 排在 `app.Stop()` 之后），离线通知 Cast 能正常发出。

### 3.3 删半成品 drain 机器（机械，随核心一并清理）

- `connAgent`：删字段 `pendingRequests atomic.Int64`、`requestsDone chan struct{}`；删方法 `AcquireRequest()`、`ReleaseRequest()`；删 `Close()` 内"无在途请求时通知 draining"那段（`pendingRequests.Load()==0` → 写 `requestsDone`）。
- `Agent` 接口（`agent.go`）：删 `AcquireRequest()`、`ReleaseRequest()` 两行声明。
- `SessionProvider` 接口（`handler/registry.go`）：删同名两行声明。
- `registry.Dispatch`：删 `sp.AcquireRequest()` + `defer sp.ReleaseRequest()`。
- `Factory.NewAgent`：删 `requestsDone: make(chan struct{}, 1)` 初始化。

**删除安全性核实**：全仓 `AcquireRequest`/`ReleaseRequest`/`requestsDone`/`pendingRequests` 仅出现在上述四处（`agent.go`/`factory.go`/`registry.go`），`Agent` 与 `SessionProvider` 均只有 `connAgent` 一个实现者，**无测试 mock 定义这两方法**（已 grep 核实）。`registry_test.go` 不经真实 `SessionProvider` 调 `Dispatch`。删除不破坏既有测试。

> 依 CLAUDE.md「既有死代码不主动删，提出来即可」——本删除已经用户在方案 B 中明确选定，属"被要求"，正当。

## 4. 验证（纯沙箱单元测试，无 Docker 依赖）

- **B7**
  - `handleData` 传入的 ctx 绑连接存活：构造最小 `connAgent`（含 `ctx`/`cancel`）+ 捕获 ctx 的 fake `forwardFn`；走一条转发；`Close()` 后断言被捕获的 `ctx.Err() == context.Canceled`。
  - 在途转发因断连而中止：fake 转发 `done` 阻塞在 `ctx.Done()` 上；`Close()` 后断言其经 `context.Canceled` 解除阻塞。
- **B1**
  - `agent.Map.Range` 遍历命中所有存入项、回调返回 `false` 可提前终止。
  - `Application.Stop()` 关活动连接：`New(WithSerializer(…))` 构造 app（无需真实网络），向 `AgentMap` 存入一个记录 `Close` 的 fake `agent.Agent`；`Stop()` 后断言其被 `Close`、且 `Stop` 在超时内返回。直接验证新增的「停机遍历 AgentMap 强关」行为；读循环退出→`agentWg` 归零是既有 `connAgent.Close`→`conn.Close`→`ReadPacket` 报错的结构性链路（非本期新增），故不再额外搭真实 acceptor/conn。
- **回归**：删除半成品 drain 机器后，`registry`/`agent`/`application` 包既有测试全绿。
- 全量 `go build ./...`、`go vet ./...`、`go vet -tags integration ./...`、`go test ./... -race`（agent/application/handler 包 `-count` 压测稳）。

## 5. 评审力度

本切片为"请求生命周期"专题、用户指定**最 meaty**：
- **3.1 B7 每连接 ctx** + **3.2 B1 停机强关** 走独立 **spec + 质量双评审**子代理；
- **3.3 删机器** 随核心改动一并复核（机械）；
- **整支 opus 终审**（端到端：断连→ctx 取消→在途中止链路、停机关连接×`agentWg` drain×模块 OnStop 顺序无竞态、删除无悬挂引用）；
- 全程 `go test ./... -race`。

## 6. 风险与回归边界

- **accept-during-shutdown 窄窗**（见 §2 非目标）：已从 connChan 取出但未入 Range 的连接漏关，由 10s `shutdownTimeout` 兜底。
- **ctx 取消对 fire-and-forget 副作用的影响**：gate 入站的本地 `Dispatch` 与转发都是请求-响应或 OneWay-即发，断连取消不会丢"必须落库"的副作用（那类落库在后端模块主循环执行，由模块自身 drain 守护，与 gate 连接 ctx 无关）。
- **`Close` 在 `Range` 回调内删 AgentMap**：依赖 `sync.Map.Range` 允许遍历中删当前 key 的语义，已确认安全。

## 7. Follow-up（不在本期）

- 真正的进程内"先 drain 再关"优雅停机（方案 A），若未来单实例部署有需求再评估。
- 跨 NATS 的分布式取消令牌（让原始客户端断连可取消后端在途 handler），属大特性。

## 8. 交付物清单（供 impl-plan 拆 Task）

1. `src/framework/agent/map.go`：`Map.Range`。
2. `src/framework/agent/agent.go`：`connAgent` 增 `ctx`/`cancel`；`Close()` 取消；`handleData` 用 `a.ctx`；删 `pendingRequests`/`requestsDone`/`AcquireRequest`/`ReleaseRequest` + `Agent` 接口两声明。
3. `src/framework/agent/factory.go`：`NewAgent` 建 `ctx`/`cancel`；删 `requestsDone` init。
4. `src/framework/handler/registry.go`：`SessionProvider` 删两声明；`Dispatch` 删 Acquire/Release 调用。
5. `src/framework/application/application.go`：`Stop()` 遍历 AgentMap 强关连接。
6. 测试：`agent`（B7 ctx 取消、Map.Range）、`application`（Stop 关连接 + drain）包新增/更新。
