# P5-B 框架健壮性三连（B4 / B5 / B6）设计 Spec

> 状态：设计已定稿，待 impl-plan。隶属 P5（路线图最后阶段）§9.4 框架风险 backlog。
> 前序：P5-A（§9.3 死代码/占位清理）已合入 main（PR #29/#30）。

## 1. 背景与动机

§9.4 列了 7 项框架风险 backlog（B1–B7）。本切片取其中三项**互不耦合、纯沙箱单测可验、低风险**的加固合一个 PR，沿 P5-A 轻量化节奏收口：

- **B4**：`NatsRPC` 在 `handler` 注入前被解引用会 nil-deref panic（防御性）。
- **B5**：集群 `DieChan()` 是**死线**——etcd/NATS 永久故障时集群发出 die 信号，但无人接收，进程沦为僵尸（真实 bug）。
- **B6**：`TCPAcceptor.ListenAndServe` 在 listen 失败时 `panic`，与 `WSAcceptor` 的优雅返回不一致；且 panic 发生在分离的 goroutine 中，无法 recover，会崩整进程。

执行前已逐项对照 main @ `ece4268` 代码文本重核（line 号会漂，下文引用为核实时的位置）。

## 2. 范围与非目标

**范围**：三处独立修法，各落一个文件：
- `src/framework/cluster/transport/nats_rpc.go`（B4）
- `src/framework/application/application.go`（B5）
- `src/framework/network/acceptor/tcp_acceptor.go`（B6）

**非目标（明确排除，避免范围蔓延）**：
- **B1 draining**（`agent.requestsDone` 写 never 读 / `Stop()` 不等在途请求）与 **B7 每消息 `context.Background()` 无断连 cancel 传播**：二者本是同一件事（请求生命周期 = drain + cancel-on-disconnect），留作专门切片合并设计，不在本期。
- **B2 `math/rand`→v2**：经复核 `pickOne`（`nats_cluster.go:194`）用的全局顶层 `rand.Intn` **本就带锁并发安全**，"并发不安全"措辞不成立；v2 迁移是纯现代化 churn 无功能收益 → **review-closed，不做**。
- **B3 taskqueue 满丢任务**：静默丢 `func()` 是潜在正确性隐患，但"修"需先定策略（阻塞背压 / 扩容 / 丢弃+metric+降级）→ 含设计决策，另开切片。
- **B6 fail-fast**：本期只去 panic、对齐 WS 现有行为；"前端节点 bind 失败应干净终止进程"（含 WS 当下同样的 silent-limp 隐患）属更大改动（acceptor 构造 + builder 接 die），列 §7 follow-up。

## 3. 逐项设计

### 3.1 B4 — NatsRPC handler nil-guard（防御性，机械）

**现状（核实）**：`NatsRPC.handler` 由 `Application.Start()` 经 `SetHandler` 注入（`nats_rpc.go:67`）。三处直接解引用 `r.handler(...)`：
- `Call` 本地短路（`nats_rpc.go:104`）
- `CallAsync` 本地短路（`nats_rpc.go:126`，且在 `go func` 内 → nil-deref 会在分离 goroutine 崩进程）
- `handleMessage` 订阅回调（`nats_rpc.go:227`，同样在 NATS 回调 goroutine 内）

**可达性结论（核实）**：标准生命周期下 nil **不可达**——`main.go` 先 `app.Start()`（其内 `a.cls.(HandlerSetter).SetHandler(...)` 注入 handler，`application.go:155`）后 `cls.Init()`（其内 `discovery.Init()` 再 `rpc.Start()` 订阅，`nats_cluster.go:52-56`），即 `SetHandler` 严格先于订阅。故本项是**防御性加固**：把"若注入顺序未来漂移 → 分离 goroutine 里 nil-deref 崩整进程、栈难读"降级为干净错误。

**修法**：
```go
var errHandlerNotSet = errors.New("cluster: handler not set")
```
- `Call`：本地短路前 `if r.handler == nil { return nil, errHandlerNotSet }`。
- `CallAsync`：本地短路前 `if r.handler == nil { done(nil, errHandlerNotSet); return }`。
- `handleMessage`：调 `r.handler` 前 `if r.handler == nil { logger.Warn("cluster: message received before handler set", logger.String("route", cm.Route)); publishReply(r.conn, cm.Route, natsMsg.Reply, nil, errHandlerNotSet); return }`——给请求方干净错误而非超时；oneway（`Reply==""`）时 `publishReply` 自然 no-op。

**测试策略**：结构体字面量 `&NatsRPC{subject:"x"}`（`handler` 默认 nil，绕过需真 NATS 连接的 `NewNatsRPC`）：
- `Call(ctx, 本地target, msg)` → 断言返回 `errHandlerNotSet`、不 panic。
- `CallAsync(...)` → 断言 `done` 收到 `errHandlerNotSet`、不 panic。
- `handleMessage` 用 oneway `nats.Msg{Reply:""}` → `publishReply` 提前返回不碰 `conn` → 断言不 panic、提前返回。（请求路径的 `publishReply` 行为已被既有 `TestPublishReply_*` 覆盖。）

### 3.2 B5 — 集群 DieChan 死线桥接（触及停机生命周期，本 Task 走聚焦 spec+质量复核）

**现状（核实，真实 bug）**：
- 各 `main.go` 建 `NewNatsCluster(self, NatsClusterConfig{...})` **未设 `DieChan`** → `cfg.DieChan==nil` → `NewNatsCluster` 自建 `dieCh = make(chan struct{}, 1)`（cap 1 缓冲，`nats_cluster.go:34-35`），交给 `NatsRPC` 与 `Discovery`。
- die 触发点：etcd lease 丢失（`discovery.go:206`）、etcd watch 超 max-retries（`discovery.go:269`）、NATS 永久关闭（`nats_rpc.go:48`），均 `select{ case dieCh<-struct{}{}: default: }`。
- `NatsCluster.DieChan()` 暴露该 channel（`nats_cluster.go:184`），但 `grep` 全仓确认**无任何接收方**。
- `app.Run()` 只 select `sigChan` 与 `a.dieChan`（`application.go:290-294`）；`a.dieChan` 是**另一条**独立 unbuffered channel，仅 `Stop()` 自己 `close`（`application.go:324`），无任何 sender。

**净效果**：etcd/NATS 永久故障 → 集群把 die 塞进无人消费的缓冲 channel；NATS `MaxReconnects(-1)` 永久重连 → 进程沦为僵尸，**自动停机永不触发**。唯一停机路径是 OS 信号。memory 旧判定"dieCh 缓冲 1 保首信号必达、大概率已足够"对缓冲判断正确，但漏看了**整条线没接收方**。

**修法（框架级，零 `main.go` 改动）**：能力断言 helper，镜像 `Start()` 已有的 `a.cls.(cluster.HandlerSetter)` 惯例（`application.go:155`）——`DieChan` 不进 `cluster.Cluster` 接口（noop/fake 无需实现），保持"可选能力"风格：
```go
// clusterDieChan 返回集群的 die 信号 channel（集群暴露该能力时），否则 nil。
// nil channel 在 select 中永久阻塞，对 noop / 无该能力的实现安全。
func (a *Application) clusterDieChan() <-chan struct{} {
    if dc, ok := a.cls.(interface{ DieChan() <-chan struct{} }); ok {
        return dc.DieChan()
    }
    return nil
}
```
停机等待抽成**无信号依赖的可测 seam**，`Run()` 调它后再 `Stop()`：
```go
// awaitDie 阻塞直到收到停机触发：OS 信号 / 自身 die / 集群 die。
func (a *Application) awaitDie(sigChan <-chan os.Signal) {
    select {
    case sig := <-sigChan:
        logger.Info("received signal", logger.String("signal", sig.String()))
    case <-a.dieChan:
    case <-a.clusterDieChan():
        logger.Warn("cluster signaled die, shutting down")
    }
}

func (a *Application) Run() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    a.awaitDie(sigChan)
    a.Stop()
}
```
桥接接通后：etcd/nats 永久故障 → 集群 cap-1 `dieCh`（已缓冲，覆盖"die 在 `Run` 开始 select 前触发"的启动窗口）→ `awaitDie` 醒 → `Stop` 干净停机。`a.dieChan` 维持原样（只 `Stop` 关、`Run` 收，无 sender，无需缓冲）。

**测试策略**：嵌入 `cluster.NewNoopCluster()` 的小 fake 补 `DieChan()`（嵌入接口值满足 `cluster.Cluster` 其余方法，只加一个 `DieChan`）：
```go
type fakeDieCluster struct {
    cluster.Cluster
    die chan struct{}
}
func (f *fakeDieCluster) DieChan() <-chan struct{} { return f.die }
```
- `clusterDieChan()`：注入 fake → 返回 `f.die`；注入 noop（无 `DieChan`）→ 返回 nil。
- `awaitDie(nil)`（sigChan 传 nil 永久阻塞、`a.dieChan` 不关）：另一 goroutine 向 `f.die` 发信号 → 断言 `awaitDie` 在超时内返回（桥接生效）。
- 端到端（可选）：`Run` 在 goroutine 跑、fire `f.die`、断言 `a.DieChan()` 在超时内关闭（`Stop` 已执行）。

**并发自检**：`a.cls` 在 `New`/`Build` 期一次性注入、`Run` 期只读，无写竞争；`awaitDie` 与 `Stop`（`sync.Once`）串行调用，无重入。全程跑 `-race`。

### 3.3 B6 — TCPAcceptor listen 优雅化（机械，对齐 WS）

**现状（核实）**：`tcp_acceptor.go:41-44` listen 失败 `panic("TCPAcceptor listen failed: "+err.Error())`；而 `ws_acceptor.go:109-115` 是 `logger.Error` + `running.Store(false)` + `close(connChan)` + `return`。`ListenAndServe` 在 `application.go:178` 经 `go acc.ListenAndServe()` 启动 → panic 在分离 goroutine 内，**无法 recover，崩整进程**且栈难读。

**修法**：把 panic 换成对齐 WS 的优雅返回：
```go
func (a *TCPAcceptor) ListenAndServe() {
    ln, err := net.Listen("tcp", a.addr)
    if err != nil {
        logger.Error("tcp listen failed", logger.String("addr", a.addr), logger.Err(err))
        close(a.connChan)
        return
    }
    a.listener.Store(&ln)
    a.running.Store(true)
    // ... 原 accept 循环不变
}
```
- 新增 `project/src/common/logger` import。
- 失败时 `running` 尚未置 true（其置位在 listen 成功后 `tcp_acceptor.go:46`），故省去 WS 那句冗余 `running.Store(false)`。
- 失败路径在注册 defer（`tcp_acceptor.go:47`，含另一处 `close(connChan)`）之前 `return` → **只我们这一处 close**，无 double-close。
- 行为对齐 WS：`close(connChan)` 令 `application.go:173` 的 `for conn := range acc.ConnChan()` 消费 goroutine 自然退出（与 WS 现行为一致，消费侧已能处理 channel 关闭）。

**测试策略**（acceptor 包首个测试文件）：预占端口制造确定性 listen 失败：
```go
busy, _ := net.Listen("tcp", "127.0.0.1:0")   // 占住一个端口
defer busy.Close()
a := NewTCPAcceptor(busy.Addr().String())     // 同 addr → listen 必失败
a.ListenAndServe()                             // 失败即返回，非阻塞
```
断言：不 panic（`recover` 兜底断言）、`ConnChan()` 已关闭（`_, ok := <-ch; ok==false`）、`IsRunning()==false`。

**silent-limp 取舍说明**：对齐 WS 后，前端节点 bind 失败仍会"进程存活但无监听"。这是 WS 当下既有行为，本切片刻意保持一致、不在此扩展；fail-fast 列 §7 follow-up。

## 4. 验证

- `go build ./...`
- `go vet ./...`
- `go vet -tags integration ./...`（集成测试仅编译验证，沙箱无 Docker）
- `go test ./... -race`（21 包全绿；B5 所在 application 包 `-count=3` 压稳）
- `gofmt`：只清本切片改动新增的 dirt；main 既有 dirt（如 `routes.go`/`session.go`/`online_module.go` 等）不动（CLAUDE.md「既有不主动改」）。

本切片**无 proto、无 config、无 Docker 依赖**——纯沙箱单测可全验。

## 5. 评审力度（沿 P5-A D-A3 轻量化）

- `subagent-driven-development` 逐 Task TDD，逐 Task 控制器复核 + 命令即测试。
- **B5 Task 触及 `Run`/`Stop` 停机生命周期 → 给一次聚焦 spec+质量复核**；B4/B6 机械 → 轻评审。
- **不需整支 opus 终审**（无 B1 级语义重写；三处加性/局部改动）。

## 6. 风险与回归边界

- **B4**：纯加性守卫，不改既有可达路径行为（nil 不可达）；零回归面。
- **B5**：`Run` 增一条 select 分支 + 一个只读 helper；`awaitDie` 抽取保持原 `Run` 行为不变（signal/dieChan 两路语义不动）。风险点=确保 `clusterDieChan()` 对 noop 返回 nil（select 永久阻塞而非 panic/busy-loop）——已在测试覆盖。
- **B6**：仅改 listen 失败这一条之前会 panic 的路径；成功路径字节不变。close(connChan) 的 double-close 风险经 return 时序排除。

## 7. Follow-up（不在本期）

- **B1 + B7**：请求生命周期专门切片（drain 在途 + 断连 cancel 传播），合并设计。
- **B3**：taskqueue 满策略（背压 / 扩容 / 丢弃+metric）。
- **B6 fail-fast**：前端节点 bind 失败干净终止进程（含修 WS silent-limp）——需 acceptor 接 die 信号 + builder 构造改动。
- **§C 文档同步**：architecture/network/cluster/development 的 world/scene 改写 + P5-A 延后的 `.pb.go` 注释镜像。
- **⑤ ops 环 128 淘汰边界**（正确性，narrow）。

## 8. 交付物清单（供 impl-plan 拆 Task）

| 文件 | 改动 | 类别 |
|---|---|---|
| `nats_rpc.go` | `errHandlerNotSet` + 三处 nil-guard | B4 机械 |
| `nats_rpc_test.go` | Call/CallAsync/handleMessage nil-guard 用例 | B4 测试 |
| `application.go` | `clusterDieChan()` + `awaitDie()` + `Run` 接线 | B5 生命周期 |
| `application_test.go`（新建） | `fakeDieCluster` + clusterDieChan/awaitDie 用例 | B5 测试 |
| `tcp_acceptor.go` | panic→优雅返回 + logger import | B6 机械 |
| `tcp_acceptor_test.go`（新建） | listen 失败优雅返回用例 | B6 测试 |
