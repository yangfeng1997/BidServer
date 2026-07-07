# 游戏服务器框架设计文档

> 版本：v2.0
> 目标：单主循环串行 + 工业级异步 RPC 的分布式游戏服务器框架，注重设计审美与落地可行性。
> 本文档已逐条确定所有实现细节，可直接用于生成代码。

---

## 〇、全局工程约定

| 项 | 取值 |
|----|------|
| Go module 名 | `project`（所有 import 前缀为 `project/...`，项目根目录下 `go.mod` 声明） |
| Go 版本 | 1.26+ |
| proto 目录名 | `protocol/` |
| 配置格式 | YAML；proto 为单一真相源，gen_config 从 FileDescriptorSet 生成 Go struct；`pkg/configgen.Load[T]` 加载；cobra 解析 flag，无 viper；详见配置系统文档 |
| 日志 | 封装 zap |
| 错误码词根 | `errcode` / `ErrCode`（详见 §7.12） |

**核心设计哲学**：

1. **保序 + 异步**：游戏协议必须保序；同目标消息有序；异步用 `seq_id + callback map` 匹配，发送方持续发送不阻塞。
2. **单主循环串行**：所有业务逻辑在主循环内串行执行，跨 goroutine 通过 `taskqueue.Post(fn)` 投递，无锁无竞争。逻辑并发靠横向多进程 + K8s 多副本，不靠进程内多核。
3. **绝不阻塞主循环**：主循环内禁止任何同步阻塞调用。所有跨进程调用发起即返回，结果晚到时经"回调 + seq 配对 + 回投主循环"在主循环上执行。
4. **框架只认 NodeID**：worldID 已编码在 NodeID 高位，所有寻址一跳到位 `NodeID → ra_addr`，跨 world 对框架透明。所谓节点皆为 Node。

---

## 一、整体概览

Golang 分布式游戏服务器，所有服务共用同一套框架，框架提供横向能力，服务只实现自己的业务。

```
客户端
  │ TCP / WS
  ▼
gatesvr（网关）
  │ UDS → RouterAgent → UDS
  ▼
lobbysvr / roomsvr / onlinesvr / matchsvr
  │ UDS → RouterAgent → UDS
  ▼
（服务间互调）
```

---

## 二、目录结构

```
project/
├── cmd/                        # 各服务薄入口
│   ├── gatesvr/main.go
│   ├── lobbysvr/main.go
│   ├── roomsvr/main.go
│   ├── onlinesvr/main.go
│   ├── matchsvr/main.go
│   └── routeragent/main.go
│
├── internal/                   # 项目私有代码
│   ├── core/                   # 框架核心（跨服务复用）
│   │   ├── app/                # App + Module 生命周期、Poster 接口、GetModule
│   │   ├── bootstrap/          # bootstrap.New()：组装 config+log+ragent，各服务 main.go 一行启动
│   │   ├── config/             # core/config.Module（生命周期）+ 包级 Startup[T]()/Runtime[T]()；proto/YAML 加载见配置系统文档
│   │   ├── log/                # log.Module（生命周期）+ 包级命名实例 log.Main/Res/Tracing
│   │   ├── rpc/                # 异步 RPC 引擎：seq/pending/timer、Reply、自动 Guard、编排原语(Join/Each/Seq)；经 Transport 接口发送，不关心底层传输
│   │   ├── ragent/             # RouterAgent UDS 客户端：连 ra.sock、RA 帧编解码、路由封装（实现 rpc.Transport）
│   │   ├── acceptor/           # 前端连接监听 TCP/WS
│   │   ├── conn/               # Connection 接口（package conn）
│   │   ├── codec/              # 客户端帧编解码 Packet/Message
│   │   ├── session/            # Session（玩家连接状态）
│   │   ├── dispatcher/         # 入站消息分发（Dispatcher）+ Middleware；业务实现（XxxHandler/XxxService）在各业务服
│   │   ├── nodeid/             # NodeID 编解码
│   │   └── errcode/            # 错误码构造与接口（Error / New / From）；依赖 protocol/common 的 ErrCode，绑定本项目故归框架核心
│   ├── gatesvr/                # gatesvr 业务代码
│   ├── lobbysvr/
│   ├── roomsvr/
│   ├── onlinesvr/
│   ├── matchsvr/
│   └── routeragent/            # RouterAgent 服务器
│
├── pkg/                        # 通用库（可被任何项目复用，不含业务概念）
│   ├── logger/                 # zap 封装：NewLogger(cfg)/Logger 类型；不含命名实例，由 core/log 定义
│   ├── configgen/              # proto+YAML 加载引擎：Load[T]/CheckReloadable/expandUpperEnv；不含 Module
│   ├── serialize/
│   ├── taskqueue/              # channel-based MPSC 投递队列
│   ├── event/                  # 进程内同步事件总线（泛型）
│   └── timewheel/              # 多层时间轮（单 goroutine 契约，无锁）
│
├── protocol/                   # Proto 定义
│   ├── common/
│   │   ├── options.proto       # 框架 option 定义
│   │   └── errcode.proto       # ErrCode 错误码集中定义
│   ├── ra/
│   │   └── ra.proto            # RouterAgent UDS + TCP 协议
│   ├── cs/                     # 客户端-服务器消息（Handler 入口使用）
│   │   ├── lobby_cs.proto
│   │   ├── room_cs.proto
│   │   ├── online_cs.proto
│   │   ├── match_cs.proto
│   │   └── gate_cs.proto
│   ├── ss/                     # 服务器-服务器消息（Service RPC 使用）
│   │   ├── lobby_ss.proto
│   │   ├── room_ss.proto
│   │   ├── online_ss.proto
│   │   ├── match_ss.proto
│   │   └── gate_ss.proto
│   ├── handler/                # 客户端入口 service 定义
│   │   ├── lobby_handler.proto
│   │   ├── room_handler.proto
│   │   ├── online_handler.proto
│   │   ├── match_handler.proto
│   │   └── gate_handler.proto
│   ├── service/                # 服务间 RPC service 定义
│   │   ├── lobby_service.proto
│   │   ├── room_service.proto
│   │   ├── online_service.proto
│   │   ├── match_service.proto
│   │   └── gate_service.proto
│   └── gen/                    # 自动生成，不手改
│       ├── routes.go           # Handler 路由表 + 鉴权白名单（由 FRONTEND kind 生成）
│       ├── handler/            # 客户端入口生成代码
│       │   ├── lobby_handler.go
│       │   ├── room_handler.go
│       │   ├── online_handler.go
│       │   ├── match_handler.go
│       │   └── gate_handler.go
│       ├── service/            # 服务间 RPC 生成代码
│       │   ├── lobby_service.go
│       │   ├── room_service.go
│       │   ├── online_service.go
│       │   ├── match_service.go
│       │   └── gate_service.go
│       └── rpc.go              # 包级 Stub 变量 + Init(core)
│
├── tools/
│   ├── gen_proto.sh            # 一键生成所有代码
│   ├── gen_routes/             # 扫描 proto 生成路由表
│   └── protoc-gen-svcstub/     # 生成 Handler/Service stub
│
├── conf/                       # 配置源文件（进 Git）——完整目录结构见配置系统文档
│
└── docs/                       # 文档

# 不进 git（部署产物，由脚本生成）：
# run/
```

**落点判断原则**：
- `pkg/` — 逻辑上可被任何项目复用的通用库
- `internal/core/` — 框架核心，只属于本项目但跨服务复用
- `internal/xxxsvr(xxxagent)/` — 各服务业务代码，只属于该服务
- `cmd/xxx/main.go` — 各服务薄入口，只做装配

---

## 三、服务一览

| 服务 | 职责 |
|------|------|
| gatesvr | 接受客户端 TCP/WS 连接，转发消息到后端服务；持有 Session |
| lobbysvr | 大厅逻辑、发起匹配、玩家 room 亲和绑定、玩家状态加载/落库；登录逻辑（token 校验）并入此服 |
| onlinesvr | 在线状态 + 玩家位置（gate/lobby/room）；跨 gateway 重复登录踢号、定位玩家、重连恢复 |
| matchsvr | 匹配队列、凑桌、分配 room 实例、回告 lobby |
| roomsvr | 对局实例、战斗逻辑、帧广播、对局结算 |
| routeragent | Sidecar 进程，负责服务发现和路由转发，每台物理机一个 |

> 无独立 loginsvr，登录逻辑并入 lobbysvr。
> 持久化由各服务直连 MongoDB（落地走异步 worker 池，见 §7.9），目前无 dbsvr 等。
> **玩家在业务服上的 `Player` 对象**（挂业务状态 + 路由信息），gate 上有 `Session`，二者均持有 BoundNodes。

---

## 四、框架核心

### 4.1 App + Module 生命周期

#### 三层注册 API

基础设施按依赖层级分三段注册，执行顺序由 API 结构保证，不靠约定文档：

```
app.New(infra...)          → infraModules：零依赖地基，同步就绪（config、log）
app.RegisterInfra(m)       → frameworkModules：依赖地基、有 WaitReady（ragent）
app.Register(m)            → modules：业务模块，按注册顺序
```

三层语义：

| slice | 注册 API | 放什么 | 特征 |
|-------|---------|--------|------|
| `infraModules` | `app.New(infra...)` | config、log | 零依赖，OnAfterInit 同步就绪 |
| `frameworkModules` | `RegisterInfra(m)` | ragent | 依赖 infra，有 WaitReady 异步建连 |
| `modules` | `Register(m)` | lobby、battle 等 | 业务模块，可选，按需注册 |

执行时三段串行：`infraModules` 全部完成 → `frameworkModules` 全部完成 → `modules` 全部完成，每段内部再按注册顺序。

```go
// pkg/app/app.go
type App struct {
    infraModules     []Module          // app.New() 参数：零依赖地基
    frameworkModules []Module          // RegisterInfra() 追加：框架必有组件
    modules          []Module          // Register() 追加：业务模块
    tq               *taskqueue.Queue  // 消费端封在 runLoop，外部只通过 Poster 接口生产
    ticker           *time.Ticker
    quit             chan struct{}
    readyTimeout     time.Duration
}

func New(infra ...Module) *App
func (a *App) RegisterInfra(m Module)
func (a *App) Register(m Module)
```

实际用法：

```go
// core/bootstrap/bootstrap.go —— 所有服务共用，main.go 不再手写基础设施
func New(configFiles []string, opts ...Option) *app.App {
    a := app.New(
        config.NewModule(configFiles),
        log.NewModule(),
    )
    a.RegisterInfra(ragent.NewModule())
    for _, o := range opts { o(a) }
    return a
}

// cmd/lobbysvr/main.go —— 只写业务模块
func run(configFiles []string) error {
    a := bootstrap.New(configFiles)
    a.Register(storage.NewModule())      // 最底层，无业务依赖
    a.Register(player.NewModule())       // 依赖 storage
    return a.Run()
}
```

#### Module 接口与可选扩展

```go
// pkg/app/module.go
type Module interface {
    Init(a *App) error
    OnAfterInit() error
    OnBeforeStop()
    OnStop()
}

// BaseModule 默认空实现，各模块按需覆盖
type BaseModule struct{}
func (b *BaseModule) Init(*App) error    { return nil }
func (b *BaseModule) OnAfterInit() error { return nil }
func (b *BaseModule) OnBeforeStop()      {}
func (b *BaseModule) OnStop()            {}

// 可选：需要周期帧逻辑的模块实现此接口。
// 主循环 tick 到点时，App 按注册顺序依次调用所有实现了 Updater 的模块。
// 不实现则主循环跳过，零侵入。
// 何时用：帧广播（room 20Hz）、周期性状态检查、timewheel 无法覆盖的轻量轮询。
// 何时不用：ragent、config、log 等纯事件驱动模块——不实现即可。
// 详见 §4.2 / §4.3。
type Updater interface {
    Update(dt time.Duration)
}

// 可选：有异步就绪需求的模块实现此接口，App 按注册顺序串行等待
type ReadyWaiter interface {
    WaitReady(ctx context.Context) error
}
```

五步生命周期：

```
    ├─ 依次 Init()              各模块自身初始化，存 app 引用，不访问其他模块
    ├─ 依次 OnAfterInit()       所有 Init 完成后，可安全取依赖、启动 goroutine
    ├─ 依次 WaitReady()         有异步就绪需求的模块顺序等待（可选，见 §10.4）
    ├─ 主循环（见 §4.2/§4.3）
    │      ├─ 任务到达 → fn()                    事件即时处理，不等帧
    │      └─ tick 到点（业务服）
    │             ├─ timewheel.Advance(dt)        推进多层时间轮
    │             └─ 依次 Update(dt)              仅实现了 Updater 的模块
    ├─ 依次 OnBeforeStop()      逆序，停止对外服务，revoke 外部资源
    └─ 依次 OnStop()            逆序，释放内部资源
```

**OnBeforeStop / OnStop 职责（写死）**：

| 钩子 | 职责 | 典型操作 |
|------|------|---------|
| `OnBeforeStop`（逆序） | **对外**：停止接收新请求，通知外部"我要停了" | Acceptor.Close、ragent revoke lease、停止 ticker |
| `OnStop`（逆序） | **对内**：释放内部资源，此时外部已无新流量 | 关闭连接、flush 日志、释放内存、关 DB 连接池 |

> 逆序保证：业务模块先于 ragent 停对外服务，ragent 先于 log 停，log 最后 flush。

#### Poster 接口与 taskqueue

taskqueue 由 App 内置持有，消费端封在 `runLoop` 内部。对外只暴露 `Poster` 接口：

```go
// pkg/app/poster.go
type Poster interface {
    Post(fn func())
}

// App 实现 Poster 接口
func (a *App) Post(fn func()) { a.tq.Post(fn) }
```

模块在 `Init` 里存 `*App`（即 `Poster`），goroutine 里通过它投递任务：

```go
func (m *ragentModule) Init(a *app.App) error {
    m.poster = a   // *App 实现了 Poster
    return nil
}

func (m *ragentModule) readLoop() {
    for {
        msg := readMsg()
        m.poster.Post(func() { m.handleMsg(msg) })
    }
}
```

#### 基础设施包级访问

**进程级单例**（config、log、ragent）生命周期归 Module 管理，使用侧走包级函数——全服务直接调，无需 GetModule、无需注入：

| 包 | 包级访问 | 说明 |
|----|---------|------|
| `core/config` | `config.Common[T]()` / `config.Startup[T]()` | Common 从 common.yaml 加载（不热更），Startup 从服务 yaml 加载（SIGHUP 更） |
| `core/log` | `log.Main` / `log.Res` / `log.Tracing` | 命名实例，OnAfterInit 后设置 |
| `core/ragent` | `ragent.Send(dst, msg)` | 包级 def，OnAfterInit 后设置 |

```go
// core/config/module.go
var (
    globalStartup atomic.Pointer[proto.Message]
    globalRuntime atomic.Pointer[proto.Message]
)
func (m *Module) OnAfterInit() error {
    globalStartup.Store(&m.startupConf)
    globalRuntime.Store(&m.runtimeSnap)
    return nil
}
func Startup[T proto.Message]() T { return (*globalStartup.Load()).(T) }
func Runtime[T proto.Message]() T { return (*globalRuntime.Load()).(T) }

// core/log/module.go
var Main, Res, Tracing logger.Logger
func (m *Module) OnAfterInit() error {
    cfg := config.Startup[map[string]any]()
    logGroup := cfg["log"].(map[string]any)
    Main   = buildLogger(logGroup["main"])
    Res    = buildLogger(logGroup["res"])
    Tracing = buildLogger(logGroup["tracing"])
    logger.SetGlobal(Main)
    return nil
}
func (m *Module) OnStop() {
    for _, c := range m.closers { _ = c.Close() }
}

// core/ragent/module.go
var def *Module
func (m *Module) OnAfterInit() error {
    def = m
    go m.connectLoop()
    return nil
}
func Send(dst ServerID, msg proto.Message) error { return def.send(dst, msg) }
```

使用：
```go
log.Main.Info("server started", zap.Int("port", 8080))
log.Res.Info("item acquired", zap.Int64("uid", uid))
ragent.Send(dst, resp)
common := config.Common[*gen.CommonConfig]()          // 公共配置，不热更
cfg := config.Startup[*gen.GatesvrConfig]()  // 服务配置，含热更字段
```

#### 模块间依赖（业务模块）

**首选：构造函数注入**——依赖在构造时传入，main.go 集中可见，编译期检查，测试友好：

```go
// internal/lobbysvr/scene/module.go
type Module struct {
    app.BaseModule
    poster app.Poster
    player *player.Module   // 构造时注入
}

func NewModule(player *player.Module) *Module {
    return &Module{player: player}
}

func (m *Module) Init(a *app.App) error {
    m.poster = a   // App/Poster 在 Init 时才存在，这里取
    return nil
}

func (m *Module) handleMove(sess *session.Session, req *pb.MoveReq) {
    p, ok := m.player.GetPlayer(req.Uid)   // 直接调，无中间层
    // ...
}
```

main.go 里依赖图一眼看完，注册顺序即执行顺序：

```go
func run(configFiles []string) error {
    storageMod := storage.NewModule()
    playerMod  := player.NewModule(storageMod)
    sceneMod   := scene.NewModule(playerMod)
    itemMod    := item.NewModule(storageMod, playerMod)
    shopMod    := shop.NewModule(storageMod, itemMod, playerMod)

    a := bootstrap.New[*gen.LobbysvrConfig](configFiles)
    a.Register(storageMod)
    a.Register(playerMod)
    a.Register(sceneMod)
    a.Register(itemMod)
    a.Register(shopMod)
    return a.Run(app.WithTick(100 * time.Millisecond))
}
```

**`GetModule[T]`**：保留作 escape hatch，仅在真正无法在构造时确定依赖时使用（极少见）：

```go
func GetModule[T Module](a *App) T   // 找不到 panic（启动期装配 bug，尽早暴露）
```

**四条规则（写死）**：

| 依赖类型 | 获取方式 |
|---------|---------|
| 模块间依赖（业务模块） | 构造函数参数注入，main.go 显式传 |
| App / Poster | `Init(a *App)` 里取，构造时 App 尚不存在 |
| config / log / ragent | 包级函数直接调，不注入不传参 |
| 动态/循环依赖（escape hatch） | `GetModule[T]` in `OnAfterInit`，取一次存字段 |

### 4.2 运行模式：混合主循环（事件即时 + 定时 tick）

`App` 只有一个 `Run` 入口，主循环是**混合模型**：用 `select` 同时等待"任务到达"与"tick 到点"两个信号——**任务来了立刻处理（不等帧），tick 到点才跑 `Update`**。是否注入 tick 决定有没有第二条轨：

```go
// 业务服务：事件即时 + 定时 Update（频率按服务而定）
app.Run(app.WithTick(50 * time.Millisecond))   // room 20Hz
app.Run(app.WithTick(100 * time.Millisecond))  // gate/lobby/online/match 低频

// RouterAgent：纯事件即时（无 Update 帧）
app.Run()
```

**统一约定**：
- **所有业务服都注入 tick**（频率不同），tick 驱动 timewheel 推进 + 周期 `Update`。
- **RouterAgent 是唯一无 tick 的纯事件驱动进程**（超时用 `time.AfterFunc`，不用 timewheel，见 §6.2）。

| 模式 | select 轨道 | 行为 |
|------|------------|------|
| 注入 tick（所有业务服） | `任务` + `ticker` + `退出` | 任务即时处理（每次一个）；tick 时推进 timewheel + 跑 `Update` |
| 无 tick（仅 RA） | `任务` + `退出` | 任务即时处理；无周期 `Update`，超时走 `time.AfterFunc` |

**关键：消息不等帧。** 帧驱动 ≠ "消息排队到下一帧"。消息一进 taskqueue，`select` 立刻唤醒处理，延迟≈0；tick 只驱动周期性的 `Update` 与 timewheel 推进。这消除了"每经过一个帧驱动节点叠加 50ms"的链路延迟问题。

**按服务选 tick 频率**（同一主循环，不同配置）：

| 服务 | tick | 说明 |
|------|------|------|
| gate | 100ms（低频） | 纯转发，事件即时；tick 仅推进 timewheel（pending/心跳超时），业务 `Update` 为空 |
| lobby / match / online | 100ms（低频） | 请求-响应为主，周期逻辑少；tick 推进 timewheel + 轻量 `Update` |
| room（战斗/帧同步） | 50ms（20Hz） | 帧广播、物理推进必须按帧，`Update` 是主角 |
| routeragent | 无 tick | 纯事件，超时用 `time.AfterFunc`（见 §6.2） |

### 4.3 主循环实现

```go
for {
    select {
    case fn := <-taskqueue.ch:        // ① 任务到达：立刻处理一个（低延迟，不等帧）
        fn()
    case <-ticker.C:                  // ② tick 到点（仅业务服有此轨）：跑周期逻辑
        timewheel.Advance(dt)         //    推进多层时间轮（心跳/pending 超时回调在此触发）
        for _, m := range modules {   //    跑各模块 Update
            if u, ok := m.(Updater); ok {
                u.Update(dt)
            }
        }
    case <-quit:                      // ③ 退出信号
        return
    }
}
```

**每次处理一个任务**——轨 ① 每次 `select` 唤醒只处理一个任务，处理完回到顶部重新 select。理由：

- **吞吐绰绰有余**：单任务执行约 1~10μs，select+channel 开销约 0.1μs，一帧（50ms）理论可处理数千个任务，远超正常游戏负载下单帧实际消息量（几十条）。
- **Update 不会饿死**：正常负载下任务量远不及填满一帧，轨 ① 很快掏空 `ch`；`ch` 一空，select 只剩轨 ② 可选，`Update` 必然执行。
- **延迟最低、实现最简**：任务来一个处理一个，延迟≈0；轨 ② 只跑 `Update` + 推进 timewheel。
- 二者由同一 `select` 串行调度，仍是**单 goroutine 串行**，无锁、保序不变。
- RA 无轨 ②，退化为纯事件循环（`select { case 任务; case 退出 }`）。

> **过载场景**（任务持续涌入、一帧掏不空 `ch`）列入后续"过载保护"（每帧任务配额 maxPerTick + 丢帧策略）。

跨 goroutine 回调通过 `taskqueue.Post(fn)` 投递，保证所有逻辑在主循环内串行执行。

**timewheel 由业务服在轨 ② 推进**（`timewheel.Advance`，见 §11）。因此 timewheel 的所有插入/重置/删除/到期回调都发生在主循环这一个 goroutine 内，回调内可直接操作共享状态，**无需再 `taskqueue.Post`**。RA 不用 timewheel（用 `time.AfterFunc`），故可纯无 tick。

### 4.4 主循环红线：回调里禁止重活

所有回调（RPC 回包、DB 回包、定时器、客户端消息处理）都在主循环上串行执行。**任何重 CPU（大循环 / 大计算）或阻塞 IO（同步磁盘 / 同步 DB）都会卡死全进程所有玩家。**

纪律：
- 回调里**只做轻量状态更新**。
- 重 CPU / 阻塞 IO **一律丢 worker goroutine**，算完把结果 `taskqueue.Post` 回主循环（DB 落地见 §7.9）。

### 4.5 taskqueue 实现

`pkg/taskqueue` 为 **channel-based MPSC**（多生产者、主循环单消费者）：

```go
type Queue interface {
    Post(fn func())   // 任意 goroutine 调用，并发安全
    Len() int         // 取当前积压任务数（len(ch)），监控用
    Cap() int         // 容量
    Pop() func()      // 主循环调用，取一个任务
}
```

底层为 buffered channel（容量默认 8192，可配置）。接口抽象使后续可无缝替换为真正的无锁 MPSC 实现而不影响调用方。

**满载策略（雏形：阻塞背压 + 限频错误日志）**

`taskqueue` 满载 = 生产速度持续超过主循环消费速度，即主循环已过载。此时**绝不能丢任务**——队列里混着 RPC 回包 / DB 回包，丢弃会导致调用方那条链路永远不回、挂到超时甚至层层雪崩。故雏形阶段满载时**阻塞写（背压）**，宁可慢不可丢，并打错误日志留脚印：

```go
func (q *Queue) Post(fn func()) {
    select {
    case q.ch <- fn:                       // 正常入队
    default:                               // channel 已满，这一条放不进
        now := time.Now().UnixNano()
        last := q.lastFullLog.Load()
        // 满载通常持续，限频：最多每秒打一条，避免日志自身成为新的过载源
        if now-last > int64(time.Second) && q.lastFullLog.CompareAndSwap(last, now) {
            logger.Error("taskqueue full, blocking",
                zap.Int("cap", cap(q.ch)),
                zap.Uint64("total_full_hits", q.fullHits.Load()))
        }
        q.fullHits.Add(1)                  // 累计满载次数，限频日志里带上，不丢规模信息
        q.ch <- fn                         // 阻塞等待（背压）——不丢任务，尤其保 RPC 回包
    }
}
```

- `select + default` 是检测 channel 满的标准写法：入得进就入，满了走 default。
- `lastFullLog`（`atomic.Int64`）限频，`fullHits`（`atomic.Uint64`）累计，满载狂刷时一秒一条但不丢规模。
- 背压的死锁安全性：满载时阻塞的是生产者 goroutine（RA read goroutine 等），只会让"读下一个消息"变慢（背压传到上游），不会与主循环形成循环等待——主循环只消费任务，不反向等生产者。

> **生产阶段再优化**（见 §十二 过载保护）：任务分级（回包不可丢 / 客户端上行可丢）双队列、每帧任务配额 `maxPerTick` 保 `Update` 不被饿死、入口限流、队列深度/峰值(highWater)/丢弃计数接 Prometheus + 帧抖动指标告警。雏形阶段仅"阻塞背压 + 限频日志"即可，上面几个字段后续直接复用。

---

## 五、寻址：NodeID

```
NodeID（uint32）= | 16位 worldID | 8位 serverType | 8位 serverIndex |
debug 点分展示：worldID.serverType.serverIndex（各段十进制），例如 111.1.3
```

- NodeID 底层为 `uint32`，点分格式**仅用于日志/调试展示**，不用于传输或解析。
- `internal/core/nodeid` 提供编解码：`Encode(world, sType, index) uint32`、`Decode(uint32) (world, sType, index)`、`String() string`。
- **同目标消息有序**。
- `target == self` 时本地短路，不经过 RouterAgent。
- **框架只认 NodeID**：worldID 已在 NodeID 内，框架不引入独立的 world 路由维度。`At(完整NodeID)` 天然可达任意节点（含其他 world）；`ByHash/默认/Broadcast` 按 serverType 在全局成员表中选节点（见 §6.5）。

**进程身份指定**：每个进程启动时从配置文件/CLI flag 读取 `worldID / serverType / serverIndex` 拼成 NodeID。serverType 由业务模块硬编码（如 lobbysvr 固定 `ST_LOBBYSVR`），serverIndex 由部署/配置分配。启动后不变。

---

## 六、集群通信：RouterAgent

### 6.1 物理拓扑

每台物理机部署一个 RouterAgent 进程（Sidecar），本机所有业务进程通过 **UDS** 与之通信，跨机 RouterAgent 之间使用 **TCP**，RA 之间**按需单向建连**（懒建连，不预连全网，不分 world）。

```
┌─────────────────────────────────────────────┐
│  物理机 A                                    │
│                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  │
│  │ gatesvr  │  │ lobbysvr │  │  其他进程 │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  │
│       │UDS           │UDS          │UDS      │
│       └──────────────┴─────────────┘        │
│                      │                      │
│              ┌───────▼────────┐             │
│              │  RouterAgent A │◄──── etcd   │
│              └───────┬────────┘             │
└──────────────────────┼──────────────────────┘
                       │ TCP（跨机通信）
```

UDS socket 路径：`/run/routeragent/ra.sock`（固定路径，本机所有业务进程连同一个 socket，Handshake 里的 node_id 区分各连接）。

### 6.2 RouterAgent 并发模型

RouterAgent 复用 App+Module 生命周期，用 `Run()`（**事件驱动**）运行，**单主循环串行**：

- 各 UDS 连接、各 TCP peer 连接的 read goroutine 把收到的消息 `taskqueue.Post` 到主循环；
- etcd watch 事件同样 `Post` 到主循环；
- 路由决策、member_table 读写、remoteSeq pending map 读写，**全部在主循环这一个 goroutine 内串行执行**，天然无锁、天然保证 §5 的"同目标有序"。

**超时机制**：RA 是事件驱动、无帧 tick，故**不使用 timewheel**。RA 的超时（keepalive 检查、remoteSeq pending 清理）用 Go 标准库 `time.AfterFunc` 实现，**回调内 `taskqueue.Post` 到主循环执行**，维持串行不变量。

> **keepalive 与 heartbeat 词义边界**：`keepalive.go` 仅服务于"RA 与本机业务进程 UDS 链路健康"（进程崩溃/卡死检测）；客户端 Heartbeat（gatesvr §8 Heartbeat 模块）服务于"玩家客户端连接保活"。两者目的、对象、周期均不同，不得混用名称。

```
internal/routeragent/
├── uds_server.go     # 监听本机业务进程 UDS 连接，处理 Handshake
├── member_table.go   # 全局成员表（nodes 的本地缓存：nodeID→NodeInfo、serverType→[]NodeInfo 两个索引），etcd Watch 维护
├── resolver.go       # 路由决策：Hash/Direct/Any/Broadcast
├── peer_mgr.go       # 管理跨机 RouterAgent TCP 连接，懒建连 + 防双向重复建连
├── etcd_client.go    # 全量 Watch nodes/，代业务进程注册/注销节点
└── keepalive.go      # RA↔业务进程 UDS 链路健康检查（5s Ping，10s 未收 Pong 强制断开）；与客户端 Heartbeat 无关
```

RouterAgent **无业务逻辑、无状态**，成员表是 etcd `nodes/` 的本地缓存，可随时重建。

### 6.3 UDS 消息帧格式

```
| length(4B, big-endian) | type(1B) | header_len(2B) | header(protobuf) | body(raw bytes) |
```

- `length` = 1 + 2 + header_len + body_len（不含 length 本身）
- RpcRequest/RpcNotify/RpcResponse 有 header，其余消息 header_len=0，body 为完整 protobuf

| 值 | 名称 | 方向 | 说明 |
|----|------|------|------|
| 0x01 | Handshake | 业务 → RA | 连接后第一条，携带身份信息 |
| 0x02 | HandshakeAck | RA → 业务 | RA 确认注册成功 |
| 0x03 | RpcRequest | 业务 → RA | 业务发起 RPC 调用 |
| 0x04 | RpcResponse | RA → 业务 | RPC 回包 |
| 0x05 | RpcNotify | 业务 → RA | RPC 通知（无需回包） |
| 0x06 | Heartbeat | 双向 | 心跳 |
| 0x07 | BroadcastSent | RA → 业务 | 广播完成，携带 waiter_id 及实际发送节点 ID 列表；仅当 RpcNotify 头携带 waiter_id 时触发，不用于普通 RPC |

### 6.4 RouterAgent TCP 帧格式

复用 UDS 帧格式（header + body 拆分相同），type 只有四种：

| 值 | 名称 | 说明 |
|----|------|------|
| 0x01 | Handshake | 建连后双方各发一条，携带**自己的监听地址**（稳定标识），各自独立决策防双向重复建连，无需 Ack |
| 0x02 | RpcRequest | 转发业务请求 |
| 0x03 | RpcResponse | 转发业务回包 |
| 0x04 | RpcNotify | 转发业务单向通知（无需回包） |

### 6.5 路由决策

路由意图通过**带定向的 Stub 调用链对象**表达，不塞进 ctx。底层映射到 RouterAgent 的四种路由模式：

| 链式 API | 路由模式 | 说明 | wire |
|----------|---------|------|------|
| （默认裸调） | ANY | 随机取一个存活节点 | routing_mode=ANY |
| `.At(nodeID)` | DIRECT | 直接指定目标 node_id（可跨 world） | routing_key=nodeID |
| `.ByHash(key)` | CONSISTENT_HASH | ketama 哈希环按 key 选节点，同 key 稳定落同一节点 | routing_key=key |
| `.Broadcast()` | BROADCAST | fanout 到同 serverType 所有节点 | routing_mode=BROADCAST |
| `.Timeout(d)` | （正交） | 覆盖本次调用默认超时 | deadline_ms |

- CONSISTENT_HASH 和 DIRECT 共用 `routing_key` 字段，routing_mode 区分语义。
- **一致性哈希算法为 ketama 哈希环（带虚拟节点）**：每个存活节点按 nodeID 在环上放若干虚拟点，key 顺时针找最近节点。支持任意 nodeID 动态集合，节点增删扰动最小。
  > 说明：CONSISTENT_HASH 实际只在"首次未绑定时选一次节点"用到（选完即写入 BoundNodes 转 DIRECT，见 §8.6），但 ketama 语义干净、一次做对。
- `ByHash/默认/Broadcast` 在全局成员表中**按 serverType 选节点**，不引入 world 过滤维度（跨 world 仅通过 `At(完整nodeID)`）。
- **目标 serverType 由调用链对象本身决定**：`rpc.Lobby.Xxx(...)` 的 serverType 已经是 `ST_LOBBYSVR`，链式动词只控制"选哪个节点"，不控制"选哪类服务"。

**路由与上下文分离（框架铁律）**：路由意图（`At`/`ByHash`/`Broadcast`/`Timeout`）是单跳属性，只在当前 Stub 调用链上消费，不写入 `Ctx`；`Ctx` 只承载 deadline、trace、来源节点、client_meta、stale guard 等跨跳传播信息（见 §7.1.1）。

调用示例（client 入口为 `rpc` 包的包级变量，见 §7.6）：

```go
// 默认 ANY
rpc.Lobby.Login(ctx, req, s.onLoginRsp)

// DIRECT（已绑节点，可跨 world）
rpc.Room.At(game.RoomNodeID).FrameSync(ctx, ntf)

// CONSISTENT_HASH + 覆盖超时
rpc.Room.ByHash(req.GameId).Timeout(3 * time.Second).OpenGame(ctx, req, s.onOpenGameRsp)
```

**BroadcastSent / waiter 语义边界**：

- `waiter_id` 是 Broadcast 调用方在 `RpcNotify` header 里附带的局部 ID；不携带时广播静默发出，RA 不回 BroadcastSent。
- RA 以 `waiter_id` 归拢所有目标节点的投递结果（all-or-timeout），回 `BroadcastSent{ waiter_id, node_ids }` 给发起方，发起方做 scatter-gather 聚合。
- waiter 机制**只服务于广播收集**，不参与普通 Request/Response；超时后允许返回部分节点结果，发起方自行判断充分性。
- 超时后 RA 丢弃该 waiter 所有状态，迟到的回包用 waiter_id 查不到记录时静默丢弃并计数。

### 6.6 peerMgr 懒建连 + 防双向重复建连

**懒建连**：RA 之间不预连全网。peerMgr 维护内存表 `map[peerAddr]→PeerState`，仅当 RA 首次需要转发到某个 peer 时才发起 TCP 连接（peerAddr 来自 `nodes/{nodeID}.ra_addr`，见 §6.8）。

**单向建连是常态**：TCP 全双工，**一条连接即可双向通信**。"只有 A 连了 B、B 没有反向连 A"是完全正常且高效的状态。**只有** A、B 在极窄时间窗内**同时**互相发起、产生两条连接时，才触发"防双向重复建连"去重。

**稳定标识**：用 RA 的**监听地址**（`ra_addr`，IP:port）作为 peer 的唯一标识。
- peerMgr 内存表的 key、字典序比较，**一律用握手包里对端自报的监听地址**；
- **不能用 socket 的 `RemoteAddr()`**——incoming 连接的源地址是对端的临时端口。

**防双向重复建连**：

1. TCP 连接建立后双方各自发送 `Handshake{ listen_addr }`（双向并发发送）
2. 各自收到对端 Handshake 后比较两端监听地址字典序，双方独立得出同一结论：字典序大的一端的 outgoing conn 胜出，另一条关闭
3. 胜出连接晋升 `PeerConnected` 后才 flush pendingBuffer，零丢消息

peerMgr 内部状态机：

```
PeerDisconnected → PeerConnecting（懒建连，要转发时触发）
PeerConnecting   → PeerHandshaking（TCP 建立）
PeerHandshaking  → PeerConnected（握手完成，flush pendingBuffer）
```

peer 连接断开后回到 `PeerDisconnected`，下次有转发需求时重新懒建连。

### 6.7 跨机 seq_id 追踪

RouterAgent A 转发时维护 `remoteSeq → { udsConn, originalSeq }` pending map（主循环内读写）。超时清理用 `time.AfterFunc`（回调 Post 到主循环），到期删除条目。

**失败语义（写死）**：

| 场景 | 处理 |
|------|------|
| remoteSeq 超时 | 到期删除条目，不回包；`remoteSeq_timeout` 计数 +1 |
| peer TCP 断开 | 清理该 peer 所有 pending 条目，不回包；`peer_disconnect_drop` 计数 +1 |
| 回包迟到（entry 已删） | 以 remoteSeq 查不到记录，静默丢弃；`late_response` 计数 +1 |
| 重复回包（同 seq 两次） | 第二次查不到已删 entry，静默丢弃；`late_response` 计数 +1 |
| 未知 remoteSeq | 静默丢弃；`unknown_seq` 计数 +1 |
| UDS 业务进程连接断开 | 清理该进程所有 pending 条目，不回包；进程重连后自然重建 |

### 6.8 服务注册与发现

**etcd key 设计**：

```
业务节点：     nodes/{nodeID}
               value: NodeInfo{ nodeID, ra_addr, startTime }
```

- `ra_addr`：该节点**所在物理机的 RA 监听地址**（IP:port）。业务进程上线时由其本机 RA 代写。
- **无 serverType 字段**：serverType 已编码在 nodeID 中间 8 位，用到时 `nodeid.Decode(nodeID)` 解出；member_table 建 `serverType→[]NodeInfo` 索引时由 RA 解码填充。
- **无业务节点自身地址字段**：业务进程之间从不直连，全部通过本机 RA 的 UDS 收发。
- **无 `routeragents/` 表**：RA 的身份就是它的监听地址 `ra_addr`，懒建连时直接用。

Lease TTL = 30s，RouterAgent 持续 KeepAlive。RA 在 `OnStop` 时主动 revoke lease，快速摘除其名下所有业务节点。

**RA 全量 watch + 一跳解析**：每个 RA 启动时全量拉取并 watch `nodes/`，构成全局成员表。路由解析**一跳到位**：

```
NodeID → 查 member_table[nodeID].ra_addr → peerMgr 懒连该 ra_addr → 转发
（target ra_addr == 本机 RA 时本地短路，直接走 UDS 投递给目标业务进程）
```

跨 world DIRECT 因此天然可达，无需特殊逻辑。
> **规模边界**：RA 之间按需单向建连，TCP 连接数最坏为 O(n²)（双方同时有跨机需求），实际为 O(traffic edges)，中小规模集群（数十台机器）代价可接受。若集群规模显著增大，再演化为分层或按需连边结构，属后续工作。全量 watch etcd 在节点数较少时可接受；节点数增大时按需 watch 优化再做。

**业务进程上线**：

```
① 业务进程启动（RouterAgent 须先启动）
② connect /run/routeragent/ra.sock
③ 发送 Handshake{ node_id }（serverType 已在 nodeID 内，RA 解码即得）
④ 等待 HandshakeAck
   ok=false → 打日志退出（NodeID 重复，配置错误）
   ok=true  → 注册完成，开始正常收发
⑤ RouterAgent 代为向 etcd 写 nodes/{nodeID}（ra_addr 填本机 RA 监听地址），回 HandshakeAck
```

成员表刷新：RouterAgent 启动时全量拉取，后续靠 etcd Watch 增量更新，每 30s 全量对账防漏事件。

### 6.9 RA 可观测性指标（后续清单）

<!-- 指标清单：实现时按需选用，不强制在雏形阶段完成 -->
| 指标 | 类型 | 说明 |
|------|------|------|
| `ra_peer_count` | Gauge | 当前已连接 peer 数 |
| `ra_peer_connect_total` | Counter | 懒建连发起次数 |
| `ra_peer_connect_fail_total` | Counter | 建连失败/重连次数 |
| `ra_peer_disconnect_total` | Counter | peer 断开次数 |
| `ra_uds_conn_count` | Gauge | 当前 UDS 业务进程连接数 |
| `ra_forward_total` | Counter | 转发消息总数（按 routing_mode 分） |
| `ra_forward_latency_ms` | Histogram | 本地 UDS → 远端 RA 转发延迟（P95/P99） |
| `ra_remote_seq_pending` | Gauge | 当前 remoteSeq pending 数 |
| `ra_remote_seq_timeout_total` | Counter | remoteSeq 超时丢弃次数 |
| `ra_late_response_total` | Counter | 迟到/重复回包丢弃次数 |
| `ra_unknown_seq_total` | Counter | 未知 seq 丢弃次数 |
| `ra_peer_disconnect_drop_total` | Counter | peer 断开时清理 pending 数 |
| `ra_route_miss_total` | Counter | 路由查不到目标节点次数（ANY/HASH 时 member_table 为空） |
| `ra_waiter_count` | Gauge | 当前 Broadcast waiter 数 |
| `ra_waiter_timeout_total` | Counter | Broadcast waiter 超时次数 |
| `ra_member_table_size` | Gauge | 全局成员表节点数 |
| `ra_keepalive_timeout_total` | Counter | UDS keepalive 超时强制断开次数 |

---

## 七、RPC 设计

RPC 是本框架的核心。所有跨进程调用**异步、不阻塞主循环**，结果通过回调在主循环上执行。
service 端 handler 通过 `Reply` 句柄回包，可立刻回、也可等下游回来再回（异步，支持任意深度调用链）。

### 7.1 RPC 引擎（对业务隐藏）

引擎在 `internal/core/rpc` 包，类型为 `rpc.Core`，`seq / pending / timer` 三件套活在主循环 goroutine 上，业务永不接触：

```go
type inflight struct {
    onResult func(payload []byte, code ErrCode)  // 类型擦除，由生成的 stub 填入
    timer    *timewheel.Timer
    span     Span
}

// rpc.Core —— internal/core/rpc 包的异步 RPC 引擎
type Transport interface {
    Send(t Target, header RpcHeader, body []byte)
}

type Core struct {
    transport Transport
    seq       uint64                 // 主循环单线程自增，无需 atomic
    pending   map[uint64]*inflight   // seq -> 在途调用
}

// 发起请求（由生成的 stub 调用，在主循环上）
func (c *Core) Call(t Target, route string, body []byte, ctx Ctx, on func([]byte, ErrCode)) {
    seq := c.nextSeq()
    budget := ctx.Remaining()                         // deadline 传播：剩余预算
    c.pending[seq] = &inflight{
        onResult: on,
        span:     ctx.Span().Child(route),
        timer: timewheel.AfterFunc(budget, func() {   // 超时也回主循环，绝不直接执行回调
            if f := c.pending[seq]; f != nil {
                delete(c.pending, seq)
                f.onResult(nil, ERR_TIMEOUT)
            }
        }),
    }
    c.transport.Send(t, RpcHeader{Seq: seq, Route: route, DeadlineMs: budget, ...}, body)
}

// 发起单向通知（无回包）：不分配 seq、不登 pending、不挂超时
func (c *Core) Send(t Target, route string, body []byte, ctx Ctx) {
    c.transport.Send(t, RpcHeader{Route: route, ...}, body)   // RpcNotify, seq=0
}

// 收到响应（read goroutine Post 回主循环 → 主循环执行）
func (c *Core) OnResponse(seq uint64, payload []byte, code ErrCode) {
    f := c.pending[seq]
    if f == nil { return }                            // 迟到/已超时 → 丢弃
    delete(c.pending, seq)
    f.timer.Stop()
    f.span.Finish()
    f.onResult(payload, code)                         // 解码 + 业务回调，全在主循环
}
```

**三条铁律（零并发 bug 的根）**：

1. **回包与超时一律回到主循环执行**，read goroutine / 定时器**绝不直接调回调**。
2. 回调在主循环**串行执行**，因此碰业务状态**无锁安全**。
3. `pending` 找不到就**丢弃**（超时后迟到的回包）。

**`Call`（请求）vs `Send`（通知）是引擎层根本二分**：有 `_Rsp` → `Call`（配对 + 超时）；`_Ntf` → `Send`（零登记）。

#### 7.1.1 统一的 RPC 上下文（Ctx）

`Ctx` 只承载跨跳传播的信息，不承载路由意图：

| 字段 | 作用 |
|------|------|
| `deadline` | 上游剩余预算，向下游继续传播 |
| `trace/span` | 链路追踪与日志关联 |
| `from_node_id` | 来源节点，用于回包与审计 |
| `client_meta` | 客户端入口附带信息（uid / session_id / gate_node_id） |
| `stale` / `guard` | 实体生命周期判断，回包晚到时自动丢弃 |

> 路由意图（`At` / `ByHash` / `Broadcast` / `Timeout`）只放在调用链对象上，不进 `Ctx`；`Ctx` 只做传播。

#### 7.1.2 `rpc.Core` 的职责边界

`rpc.Core` 只负责：

- `Call / Send`
- `seq / pending / timer`
- `OnResponse`
- 回调回主循环
- deadline 兜底超时

`rpc.Core` 不负责：

- 业务路由表 / Handler 分发
- `ragent` 连接管理
- 业务 payload 解析
- `Session` / `Player` 生命周期
- 服务发现 / 节点选择

### 7.2 入站消息按 type 分流

业务进程收到的消息分两个方向，**收到后第一步按帧 type 分流**，分别交给 `rpc` 引擎和 `dispatcher`——二者不冲突，是 RPC 一来一回的两半：

| 入站 type | 含义 | 去向 | 为什么 |
|-----------|------|------|--------|
| `RpcRequest` / `RpcNotify` | **别人调我**（请求/通知） | **dispatcher** → Middleware 链 → 业务 Server 方法 | 是新请求，需要路由到 handler、过鉴权/panic 恢复 |
| `RpcResponse` | **我之前调别人的回包** | **rpc 引擎** `OnResponse` → 查 pending → 执行 cb | 不是新请求，不需要路由/Middleware，只需按 seq 配回原回调 |

```go
// read goroutine 收到消息 → taskqueue.Post → 主循环按 type 分流
switch frame.Type {
case RpcRequest, RpcNotify:
    dispatcher.Dispatch(...)      // 入站请求：分发到 handler（见 §8.6）
case RpcResponse:
    rpc.OnResponse(...)           // 回包：进 rpc 包的引擎查 pending（见 §7.1）
}
```

> 这里的 `rpc` 是 `internal/core/rpc` 包，引擎类型 `rpc.Core`，`OnResponse` 是它对外暴露的回包入口（§7.1）。分流本身也可由 `rpc` 包提供一个统一入口承接，对外只暴露"喂一帧进来"。

> **回包不走 dispatcher**：`RpcResponse` 是发起侧在途调用的结果，直接进 rpc 引擎按 seq 配对，不经路由表、不过 Middleware。dispatcher 只处理"别人发起、由我落地"的入站请求。

**发起侧 vs 落地侧，一次 RPC 的两端各用一个**：

```
A 发起:  rpc 引擎(出站，登 pending) ──网络──▶ B 的 dispatcher(入站，分发到 handler)
B 回包:  B 的 reply ───────────────网络──▶ A 的 rpc 引擎(入站回包，查 pending 执行 cb)
```

**Reply 回包复用 dispatcher 记下的来源**：dispatcher 把请求送到 handler 时，顺带把"回包地址"（原始 seq、来源 from_node_id）放进 ctx / reply 闭包；handler 调 `reply(rsp)` 时，框架 dispatch wrapper 用这些信息把 rsp 发回原调用方。这是 dispatcher（入站分发）与 reply（出站回包）的协作点。

### 7.3 proto service 分两类

**cmd_id / no_auth 挂 message option**（不挂 method）：cmd_id 编号的是"一条具体消息"，挂在被编号的 message 上语义精准，且天然覆盖 Notify 单向消息与无 method 的主动 Push 消息（§8.9）。req↔rsp 配对靠命名约定（`CS_Xxx_Req` ↔ `SC_Xxx_Rsp`）与 method 的 `returns` 声明恢复。

```protobuf
import "google/protobuf/empty.proto";

// —— 消息定义：cmd_id / no_auth 挂在 message 上 ——
message CS_ClaimReward_Req {
    option (framework.cmd_id) = 2050;
}
message SC_ClaimReward_Rsp {
    option (framework.cmd_id) = 2051;
}
message CS_SyncPos_Ntf {
    option (framework.cmd_id) = 2052;
    option (framework.no_auth) = true;
}

// Handler 类：客户端消息，经 gate 转发
// 生成：路由表条目 + Handler 接口
service LobbyHandler {
    option (framework.kind)        = FRONTEND;
    option (framework.server_type) = ST_LOBBYSVR;

    rpc ClaimReward(CS_ClaimReward_Req) returns (SC_ClaimReward_Rsp);
    rpc SyncPos(CS_SyncPos_Ntf) returns (google.protobuf.Empty);
}

// Service 类：服务间 RPC（走 route 字符串路由，不用 cmd_id）
// 生成：Service 接口 + Stub
service LobbyService {
    option (framework.kind)        = BACKEND;
    option (framework.server_type) = ST_LOBBYSVR;

    rpc Login(RPC_Login_Req) returns (RPC_Login_Rsp);
    rpc PlayerDisconnect(RPC_PlayerDisconnect_Ntf) returns (google.protobuf.Empty);
}
```

> **option 归属原则**：message option 描述"消息本身"（cmd_id、no_auth），method option 描述"调用行为"，service option 描述"服务整体"（kind、server_type）。

### 7.4 消息命名规范（codegen 强制 lint，违规编译失败）

| 后缀 | 含义 | 示例 |
|------|------|------|
| `_Req` | Request（双向，期待回包） | `CS_Login_Req`, `RPC_OpenGame_Req` |
| `_Rsp` | Response | `SC_Login_Rsp`, `RPC_OpenGame_Rsp` |
| `_Ntf` | Notify（单向，无回包） | `CS_SyncPos_Ntf`, `RPC_PlayerDisconnect_Ntf` |

前缀：`CS_`（Client→Server）、`SC_`（Server→Client）、`RPC_`（服务间）。
服务类型不进消息名（由 service 块和 route 表达）；节点流向不进消息名（由调用点决定）。

**命名约定取代显式 oneway 标记**。codegen 在生成前做 lint，**违规 → 编译失败（硬强制）**：

| 校验项 | 规则 |
|--------|------|
| 请求消息 | 必须 `_Req` 结尾 |
| 响应消息 | 必须 `_Rsp` 结尾 |
| 通知消息 | 必须 `_Ntf` 结尾 |
| 双向 `rpc X(A) returns (B)` | A 必须 `_Req`，B 必须 `_Rsp`，且 `X`/`A`/`B` 前缀一致 |
| 单向 `rpc X(A) returns (Empty)` | A 必须 `_Ntf`，B 必须是 `google.protobuf.Empty` |
| 反向 | `_Ntf` 消息的 rpc **必须** returns `Empty`；`_Req` 消息**必须**有非 Empty `_Rsp` |

**单/双向判定逻辑**：

```
if 请求 _Ntf 结尾  &&  返回 google.protobuf.Empty:  → Send 风格（client 无 cb，server 无 reply）
elif 请求 _Req 结尾 && 返回 _Rsp 结尾:               → Call 风格（client 带 cb，server 带 reply）
else:                                               → 编译报错
```

**边界：双向但响应为空** —— 用空字段的 `_Rsp` message（**不是** `Empty`），走 Call 风格，保留"确认收到 + 错误"的能力。硬判据：**是不是 `google.protobuf.Empty`** 决定单/双向。

### 7.5 路由 API：调用链对象

路由意图通过**带定向的 Stub 调用链对象**表达，调用本身回归纯粹，**cb 永远是最后一个参数**：

```go
// 默认：服务发现 + 负载均衡（ANY）
rpc.Room.RoomStart(ctx, req, cb)

// DIRECT：直发指定节点（stateful 主力：已绑定 nodeID）
rpc.Room.At(nodeID).RoomStart(ctx, req, cb)

// 一致性哈希
rpc.Room.ByHash(key).RoomStart(ctx, req, cb)

// 广播（见 §7.8）
rpc.Lobby.Broadcast().SystemNotice(ctx, ntf)

// 叠加超时（非路由选项，仍在链上）
rpc.Room.At(nodeID).Timeout(3 * time.Second).OpenGame(ctx, req, cb)
```


### 7.6 生成的 stub 与 rpc 包

```go
// Handler 类生成：接口 + 注册函数
type LobbyHandler interface {
    ClaimReward(ctx Ctx, req *CS_ClaimReward_Req, reply Reply[*SC_ClaimReward_Rsp])
    SyncPos(ctx Ctx, ntf *CS_SyncPos_Ntf)
}
func RegisterLobbyHandler(d *dispatcher.Dispatcher, srv LobbyHandler)

// Service 类生成：Service 接口 + 注册函数 + Stub 类型
type LobbyService interface {
    Login(ctx Ctx, req *RPC_Login_Req, reply Reply[*RPC_Login_Rsp])
    PlayerDisconnect(ctx Ctx, ntf *RPC_PlayerDisconnect_Ntf)
}
func RegisterLobbyService(d *dispatcher.Dispatcher, srv LobbyService)

// Stub 类型（无状态，仅持 rpc.Core + 当前路由 target）
type LobbyStub struct{ core *corerpc.Core; target Target }

func (c *LobbyStub) At(node uint32) *LobbyStub      // DIRECT
func (c *LobbyStub) ByHash(key uint64) *LobbyStub   // CONSISTENT_HASH
func (c *LobbyStub) Broadcast() *LobbyStub          // BROADCAST
func (c *LobbyStub) Timeout(d time.Duration) *LobbyStub

func (c *LobbyStub) Login(ctx Ctx, req *RPC_Login_Req,
    cb func(rsp *RPC_Login_Rsp, err error))
func (c *LobbyStub) PlayerDisconnect(ctx Ctx, ntf *RPC_PlayerDisconnect_Ntf)  // 无 cb
```

业务只要调用了 `RegisterLobbyService`，缺少任何方法实现都会编译失败。

**业务调用入口：`rpc` 包 + 包级变量**

```go
// protocol/gen/rpc.go（生成）
package rpc

var (
    Lobby  *LobbyStub
    Room   *RoomStub
    Match  *MatchStub
    Online *OnlineStub
    Gate   *GateStub
)

// 框架在 rpc.Core 初始化完成后调用一次，注入 Core，业务不感知
func Init(core *corerpc.Core) {
    Lobby = &LobbyStub{core: core}
    // ...
}
```

业务在**任意位置**直接调用，零字段、零注入样板：`rpc.Lobby.At(nodeID).Login(ctx, req, s.onLoginRsp)`。

- **命名**：包级变量用 `rpc.Lobby`（不是 `rpc.LobbyStub`）——`rpc.` 包名已表语境，避免 stutter。Stub 类型名叫 `LobbyStub`（类型与变量分离）。
- **无状态全局变量安全**：Stub 仅持 `rpc.Core` + 路由 target；`At/ByHash` 返回带新 target 的副本，不改原变量。`rpc.Init` 由框架保证启动时调用一次。
- **可测试**：单测可替换包级变量，或给 `rpc.Core` 注入假的 `Transport`。

**同一服务的客户端入口与服务间 RPC 实现拆为两个类型**：`LobbyHandlerImpl`（实现 `LobbyHandler`）与 `LobbyServiceImpl`（实现 `LobbyService`）。两者持有同一份 `*lobbyCore`（玩家表等共享状态），职责分离但状态不丢。

**谁实现 Handler 接口——看协议的 `server_type`**：gate 收到客户端消息后走 §8.6 统一分发：
- `server_type` 是某业务服（如 ST_LOBBYSVR）→ gate 转发给 lobby，由 lobby 的 `LobbyHandlerImpl` 实现；
- `server_type` 是 gate 自己（ST_GATESVR）→ gate 本地 `Handler` 实现处理。

即 gate 既是**转发器**（对非自己的客户端入口协议），也是**处理者**（对 server_type 是自己的客户端入口协议），共用同一套 Dispatch。

### 7.7 Reply + 链式 + 编排

#### 7.6.1 Reply：一次性、可延迟到任意深度的"回上游"句柄

service handler **永远不直接 return 结果**，而是拿一个 `Reply`，想什么时候回就什么时候回：

```go
type Reply[T proto.Message] func(T, error)
```

框架保证 Reply **恰好调用一次**：重复调用 → dev 环境 panic、prod 环境丢弃 + 告警；handler 走完未调用且无在途下游 → "丢失 reply"告警。

**自动 Guard（业务不写 Guard）**：回包晚到时归属实体可能已销毁（玩家下线/重连换对象）。框架在生成的 stub 里自动套 Guard，业务零感知：

```go
// 生成代码内部，业务看不到这层
func (c *RoomStub) RoomStart(ctx Ctx, req *RPC_RoomStart_Req, cb func(*RPC_RoomStart_Rsp, error)) {
    c.core.Call(c.target, "RoomService/RoomStart", mustMarshal(req), ctx, func(p []byte, code ErrCode) {
        if ctx.Stale() { return }                       // 自动 Guard：实体已失效 → 静默丢弃
        if code != OK  { cb(nil, errcode.From(code)); return }
        rsp := &RPC_RoomStart_Rsp{}
        _ = proto.Unmarshal(p, rsp)
        cb(rsp, nil)                                    // 解码好的强类型，回调在主循环执行
    })
}
```

#### 7.6.2 链式（chaining）：A→B→C

**A→B→C 不在 A 上编排**。A 只认识 B；B 内部自己调 C，把自己的 `reply` 捕获进 C 的回调，等 C 回来才 reply 回 A：

```go
// B 节点：Account.Query 要先问 C（Profile）才能回答 A
func (h *AccountService) Query(ctx Ctx, req *RPC_Query_Req, reply Reply[*RPC_Query_Rsp]) {
    rpc.Profile.Get(ctx, &RPC_Get_Req{Uid: req.Uid}, func(prof *RPC_Get_Rsp, err error) {
        if err != nil { reply(nil, err); return }              // C 失败 → 错误透传回 A
        reply(&RPC_Query_Rsp{Name: prof.Name}, nil)            // C 回来才回 A，框架按原 seq 发
    })
    // handler 到此 return，但 reply 尚未调用；B 主循环不阻塞，继续处理别的任务
}
```

链路多长都这么接力：**每个中间节点 = 发起下一跳 + 把自己的 reply 揣进回调**，最末端调用，逐层回传。

#### 7.6.3 编排（orchestration）：一个节点主动调多个下游

**判据**：链路线性传递（A 要的东西 B 得问 C）→ B 内部链式调 C；一个节点要并行/分别问多个下游 → 用编排原语 `Join / Each / Seq`。

```go
// 并行汇合（异构、定长、类型安全）
rpc.Join2(ctx,
    func(cb func(*RPC_Account_Rsp, error)) { rpc.Account.Query(ctx, r1, cb) },
    func(cb func(*RPC_Bag_Rsp, error))     { rpc.Bag.Query(ctx, r2, cb) },
    func(acc *RPC_Account_Rsp, bag *RPC_Bag_Rsp, err error) {   // 两者都回来后，主循环上触发一次
        if err != nil { reply(nil, err); return }
        reply(&RPC_Enter_Rsp{Name: acc.Name, Items: bag.Items}, nil)
    })

// 同构扇出（N 个同类目标，带 timeout）
rpc.Each(ctx, roomIDs,
    func(id uint64, cb func(*RPC_Room_Rsp, error)) { rpc.Room.At(nodeOf(id)).Get(ctx, mk(id), cb) },
    func(rs []*RPC_Room_Rsp, err error) { ... })

// 顺序水流（把回调金字塔拉平 + 统一错误出口）
rpc.Seq(ctx).
    Step(func(next rpc.Next) { rpc.Account.Query(ctx, r,  func(a, e error){ acc = a;  next(e) }) }).
    Step(func(next rpc.Next) { rpc.Role.Query(ctx, r2,    func(ro, e error){ role = ro; next(e) }) }).
    Done(func(err error) {                          // 全程任一步出错都到这
        if err != nil { reply(nil, err); return }
        reply(&RPC_Enter_Rsp{...}, nil)
    })
```

`Join2/Join3/Join4` 由 codegen 生成；计数与汇总全在主循环上，天然无锁。

### 7.8 广播（Broadcast）

广播按消息类型自动决定回包行为：

**场景 1 — 广播通知（fire-and-forget，主力）**

```go
rpc.Lobby.Broadcast().SystemNotice(ctx, ntf)   // _Ntf：无 cb，对 N 个节点各 Send 一次，不等回包
```

**场景 2 — 广播收集（scatter-gather）**

```go
rpc.Lobby.Broadcast().CountOnline(ctx, req, func(results []*RPC_CountOnline_Rsp, err error) {
    total := sum(results)   // 所有 lobby 节点在线数汇总
})
```

- `_Req`/`_Rsp`：cb 签名是 `func([]*Rsp, error)`（切片）。
- **内建 timeout + all-or-timeout**：死节点回不来时，deadline 一到用已收到的部分触发 cb。
- 实现 = §7.7.3 `Each` 作用在"所有同类型节点"上：对 N 个节点各发异步请求 + 计数，全到齐（或超时）后触发一次 cb。

**框架机制（waiter）**：waiter_id 由 transport 层内部自增分配（主循环内，无锁），以 waiter_id 为 key 注册 waiter map。全链路异步，每一步通过 taskqueue 在主循环内串行执行：

```
业务发送带 waiter_id 的 RpcNotify/RpcRequest（Broadcast）
  → RA fanout 到所有目标节点，原样透传 waiter_id
  → BroadcastSent 回调 → 更新 waiter 预期节点集合（node_ids 列表）
  → 每个回包       → transport 从 header 取 waiter_id → 找到 waiter → 收集（记录 from_node_id）
  → 全收齐或超时   → cb 回调（主循环内），timedOut = 期望集合 − 已回包集合
```

waiter_id 三段透传（业务→RA header / RA→目标节点原样透传 / 目标节点回包时由框架自动填回），业务只写业务字段。

### 7.9 DB 落地与异步统一

主循环内**阻塞驱动（如 mongo）绝不能同步调**。DB 模块 = 一个 **worker goroutine 池**：

```
主循环:  rpc.DB.Load(ctx, id, cb)        // 发请求 + 登回调，立刻返回
            ↓ 投给 DB worker 池
worker:   row, err := mongo.FindOne(...)  // 阻塞 IO，在 worker goroutine
            ↓ 结果 taskqueue.Post 回主循环
主循环:   cb(row, err)                    // 在主循环执行，无锁访问业务状态
```

**这与 §7.1 RPC 引擎是完全相同的机制**（发请求 + 登回调 + 结果回投主循环）。业务看到的也是同一种异步形态 `rpc.DB.Load(ctx, id, cb)`。异步 RPC 与异步 DB 是同一套抽象。

> DB 是并发安全的共享基础设施，不串行化；worker 池做阻塞 IO，主循环只负责发起与回调。

### 7.10 deadline 传播 + 取消

- `RpcHeader` 带 `deadline_ms`（上游剩余预算），`Ctx` 里的 deadline 以 `now + min(上游剩余, 本地默认)` 计算。
- B 调 C 时继续下发剩余预算，**C 绝不超过 A 的耐心**。
- A 的 deadline 一到，A 的 pending 触发 timeout 回调；整链各节点的 timer 各自兜底。
- **取消（v1）**：靠 deadline + 自动 Guard——A 放弃 → 实体失效 → B 回 A 的 reply 被 Guard 丢弃。显式 cancel 传播列入后续。

### 7.11 RPC 帧格式

UDS/TCP 帧在 type 之后拆为 header + body 两段，RA 只解析 header 做路由，body 零拷贝透传：

```
| length(4B) | type(1B) | header_len(2B) | header(protobuf) | body(raw bytes) |
```

```protobuf
enum RoutingMode {
    ANY             = 0;  // 默认
    CONSISTENT_HASH = 1;
    DIRECT          = 2;
    BROADCAST       = 3;
}

// RpcRequest（type=0x03）和 RpcNotify（type=0x05）共用同一 header；Notify 时 seq_id=0
message RpcHeader {
    uint64      seq_id        = 1;
    int32       server_type   = 2;  // UDS 腿：目标服务类型；TCP 腿：0
    RoutingMode routing_mode  = 3;  // UDS 腿：业务指定；TCP 腿：固定 DIRECT
    string      routing_key   = 4;  // CONSISTENT_HASH：业务 key；DIRECT：目标 node_id 字符串；TCP 腿：解析后目标 node_id
    int64       deadline_ms   = 5;  // UDS 腿：业务填，Unix 毫秒，0=不限；TCP 腿：0
    uint64      waiter_id     = 6;  // 广播收集，原样透传
    uint32      from_node_id  = 7;  // RA1 填入来源 node_id，透传
}

// RpcResponse（type=0x04）header
message RspHeader {
    uint64 seq_id        = 1;
    ErrCode err_code     = 2;  // 框架级/业务级错误码，OK=0（见 §7.12）
    uint32 from_node_id  = 3;
}

// RpcRequest/RpcNotify 的 body（RA 完全不解析，零拷贝透传）
message RpcPayload {
    string       route         = 1;  // "LobbyService/Login"
    bytes        data          = 2;  // 业务 proto 序列化
    ClientMeta client_meta = 3;  // gate 转发客户端请求时填，Service 直调时为空
}

// RpcResponse 的 body：裸 Rsp proto 序列化；失败时（err_code != OK）body 可为空

message BroadcastSent {
    uint64          waiter_id = 1;
    repeated uint32 node_ids  = 2;  // 实际发出的节点 ID 列表（期望集合）
}

message ClientMeta {
    int64  uid           = 1;
    string session_id    = 2;
    uint32 gate_node_id  = 3;
}
```

> **NodeID 在 wire 上的表示**：`from_node_id` / `gate_node_id` 等结构化字段用 `uint32`（NodeID 原值）。`routing_key` 因与哈希 key 共用 string 字段，DIRECT 时存 nodeID 十进制字符串。

**RA1 转发到 RA2 时对 RpcHeader 的改写**：

| 字段 | UDS 腿（业务 → RA1） | TCP 腿（RA1 → RA2） |
|------|---------------------|---------------------|
| seq_id | 业务原始 seq_id | remoteSeq（RA1 重新分配，pending map 记录映射） |
| server_type | 目标服务类型 | 0 |
| routing_mode | 业务指定 | DIRECT |
| routing_key | 业务 key 或 node_id 字符串 | 已解析的目标 node_id 字符串 |
| deadline_ms | 业务填 | 0 |
| waiter_id | 业务填 | 原样透传 |
| from_node_id | 0 | RA1 填入来源 node_id |

业务服 handler 从 ctx 取上下文：

```go
uid            := session.UIDFromCtx(ctx)
gateNodeID := session.GateNodeIDFromCtx(ctx)
```

### 7.12 错误码体系（errcode）

错误码全链路统一词根 `errcode` / `ErrCode`，集中定义、号段划分、跨语言共享：

```protobuf
// protocol/common/errcode.proto
enum ErrCode {
    OK = 0;

    // —— 框架级 1~999（框架保留）——
    ERR_INTERNAL  = 1;   // 未分类内部错误（业务返裸 error / panic 兜底）
    ERR_TIMEOUT   = 2;   // RPC 超时
    ERR_NO_ROUTE  = 3;   // 找不到目标节点
    ERR_UNAUTHED  = 4;   // 未认证
    ERR_UNMARSHAL = 5;   // 编解码失败

    // —— 业务级 1000+（业务自行往后加）——
    ERR_TOKEN_INVALID    = 1000;
    ERR_PLAYER_NOT_FOUND = 1001;
}
```

`cb` 的 `err` 是携带 ErrCode 的 `errcode.Error`：

```go
package errcode   // internal/core/errcode（依赖 protocol/common 的 ErrCode，故归框架核心而非 pkg）

type Error interface {
    error
    Code() ErrCode
}
func New(code ErrCode, msg string) Error   // 业务构造带码错误
func From(code ErrCode) Error              // 框架从 wire code 构造
```

- 业务 `reply(nil, errcode.New(ERR_TOKEN_INVALID, "bad token"))`，框架取 `Code()` 填入 `RspHeader.err_code`。
- 业务返回普通 `errors.New(...)` 或 handler panic（被 RecoverMiddleware 捕获）→ 映射为 `ERR_INTERNAL`。
- msg 仅供框架 log。
- **全链路一致**：同一 `ErrCode` 值在业务服↔业务服的 `RspHeader.err_code`、gate→client 内层帧 `err_code(4B)` 中不做转换，直接透传。

---

## 八、客户端接入（gatesvr）

### 8.1 组件职责

| 模块 | 职责 |
|------|------|
| Acceptor | 接收连接 |
| Connection | 异步读写 socket，维护 `lastRecv` |
| Codec | Packet / Message 编解码 |
| SessionManager | 连接态、认证态、绑定态 |
| Dispatcher | 按 `CmdID` 路由，本地处理或转发 |
| pending | 客户端请求转发后的回写表 |
| Heartbeat | 连接存活检测 |
| GateService | `SendToClient` / `BindSession` / `SetBound` |

```
Acceptor → Connection → Codec → dispatcher.Dispatcher
                                      │
                    ┌─────────────────┴──────────────────┐
                    │ 本地消息                             │ 转发消息
                    ▼                                     ▼
             Middleware 链                             ragent
             （panic 恢复 / 认证 / 限流）
                    │
                    ▼
             Handler（业务实现）
```

### 8.2 Acceptor

```go
// internal/core/acceptor/acceptor.go
type Acceptor interface {
    Listen() error
    Accept() <-chan conn.Connection
    Close() error
}
// tcp_acceptor.go → TCPAcceptor；ws_acceptor.go → WSAcceptor
```

### 8.3 Connection

```go
// package conn
type Connection interface {
    Send(data []byte)            // 异步写入 writeChannel，失败框架内部 log，业务无感
    Close() error
    RemoteAddr() string
    Done() <-chan struct{}
    LastRecvUnixNano() int64     // 原子读最近收包时间戳（纳秒）
    TouchRecv()                  // 原子写最近收包时间戳为当前时间
}
```

**最近收包时间戳 `lastRecv`**：Connection 内部 `atomic.Int64`（存 `UnixNano`）。它是唯一跨 goroutine 共享的连接状态，必须原子操作，**禁止裸 `time.Time`**。

每个 Connection 有 2 个 goroutine：

```
read goroutine  → 读到完整 Packet → 投递 taskqueue → 主循环处理
主循环 Send()   → 写入 writeChannel → write goroutine → 实际写网络
```

writeChannel 容量默认 256（可配置），写满时：发 Kick 包通知客户端 → 关闭连接。

**连接关闭与 goroutine 收尾**：
- **`conn.Close()`** 关底层 socket（幂等）。socket 一关，read goroutine 阻塞中的 `Read()` 立即返回 error → return。
- **`conn.Done()`** 是关闭广播 channel。write goroutine `select` 监听，收到即排空 writeChannel 后 return。
- **逻辑清理**（删 SessionManager 双索引、Player 等）一律在主循环内执行。

两条触发路径：

```
① 超时路径（主循环 timer 回调）：
   主循环内先做逻辑清理 → conn.Close() → read goroutine Read() 返回 err → return → write goroutine 收到 Done() → return

② 网络错误 / 对端断开（read goroutine 触发）：
   read goroutine Read() 返回 err → 不碰共享状态，taskqueue.Post(sessionMgr.OnDisconnect) → 自己 return
   主循环收到 OnDisconnect：逻辑清理 + conn.Close()（幂等）→ write goroutine 收到 Done() → return
```

- read goroutine **从不直接清理共享状态**，只投递事件给主循环。
- `conn.Close()` 幂等，两条路径都可能调用。

### 8.4 Codec 两层

```
internal/core/codec/
├── packet.go   # 外层帧，解决 TCP 粘包（type 1B + length 3B + body）
└── message.go  # 内层消息，解析 type/SeqID/CmdID，Data 保持原始 []byte
```

外层 Packet：`| type(1B) | length(3B, big-endian) | body |`

| 值 | 名称 | 方向 |
|----|------|------|
| 0x01 | Handshake | 客→服 |
| 0x02 | HandshakeAck | 服→客 |
| 0x03 | Heartbeat | 双向 |
| 0x04 | Data | 双向（业务数据，承载 Req / Rsp / Ntf） |
| 0x05 | Kick | 服→客 |

内层 Message（仅 Data 包的 body）：

```
Request:  | type(1B) | SeqID(2B) | CmdID(4B) | body |
Response: | type(1B) | SeqID(2B) | CmdID(4B) | err_code(4B) | body |
Notify:   | type(1B) | CmdID(4B) | body |
```

- `SeqID` 为 uint16（单连接在途上限 65536）。
- `err_code` 为 §7.12 的 `ErrCode` int32 值，全链路透传，不转换。

#### Heartbeat

- read goroutine 收到 `Heartbeat` 直接回 pong，并调用 `conn.TouchRecv()`；**不进 taskqueue**。
- 主循环用 `timewheel` 定时检查 `idle := now - lastRecv`，超过 `heartbeatTimeout` 就 `sessionMgr.OnTimeout(connID)`。

### 8.5 Session

```go
type Session struct {
    ID         string
    UID        int64
    ConnID     string
    Conn       conn.Connection
    Authed     bool                   // BindSession 后置 true，AuthMiddleware 据此放行
    BoundNodes map[ServerType]uint32  // serverType → nodeID，DIRECT 路由用
}
```

雏形阶段 Session 和 Connection 生命周期一致；`Session` 仅表达连接态、认证态、绑定态，后续若做断线重连，再把状态机显式拆开。

**玩家上下文分布式存在**：
- gate 上是 `Session`（持 BoundNodes，映射玩家在各业务服的亲和节点）；
- 每个参与的业务服（lobby/room/...）上是该玩家的 `Player` 对象（持业务状态 + 自己的 BoundNodes + 冗余存 `gateNodeID`，供主动 Push 时本地查）；
- onlinesvr 维护玩家全局位置（gate/lobby/room）。

SessionManager 维护双索引，所有操作在主循环内执行；`OnConnect` / `OnDisconnect` / `OnTimeout` 只做状态迁移，不碰业务逻辑：

```go
type SessionManager struct {
    byConnID map[string]*Session
    byUID    map[int64]*Session   // 登录后由 BindSession 建立
}
// acceptor.Accept() → taskqueue.Post(sessionMgr.OnConnect)
```


### 8.6 dispatcher.Dispatcher

```go
func (d *Dispatcher) Dispatch(session *Session, msg *codec.Message) {
    entry, ok := RouteTable[msg.CmdID]
    if !ok { return }   // 未知 CmdID，log + 丢弃
    if entry.ServerType == selfServerType {
        d.dispatchLocal(session, entry, msg)     // 本地 → Middleware 链 → Handler
    } else {
        d.forwardToBackend(session, entry, msg)   // 转发到业务服 → ragent（Data 原始 bytes 透传）
    }
}
```

> `forwardToBackend` 是框架内部方法（dispatcher 据路由表自动调用，业务不直接调），与业务服主动下行的 `SendToClient`（§8.9）构成上下行一对。

**上行转发完整链路**（客户端消息 → gate → 业务服 → 回客户端）：

```
客户端 ─Data帧─► gate read goroutine ─► taskqueue ─► 主循环 Dispatcher.Dispatch
                                                          │
                                          RouteTable[CmdID] → 目标 serverType
                                                          │
                         ┌────────────────────────────────┴─────────────────────────┐
                  server_type == gate                                       server_type == 业务服
                         │                                                          │
                  dispatchLocal                                            forwardToBackend
                  (Middleware→Handler)                                       │
                                                ┌──────────────────────────────────┤
                                         ① 选节点：BoundNodes 命中→DIRECT；否则 Hash(connID)
                                         ② 存 gate pending：pendingSeq → {conn, rspCmdID, timer}（§8.8）
                                         ③ ragent 发 RpcRequest（body 原始 bytes 零拷贝；header 带 ClientMeta）
                                                          │
                                            ragent ─UDS─► RouterAgent ─► 目标业务服
                                                          │
                            业务服 dispatcher（§7.2 入站分流）→ Handler handler → Reply 回包
                                                          │
                            原路 RA ─► gate：rpc.Core callback → 查 pendingMap[pendingSeq] → conn.Send(rsp)（§8.8）─► 客户端
```

| 步骤 | 关键点 |
|------|--------|
| 查路由 | gate **不解析 body**，只看 CmdID 查 `RouteTable` 拿目标 serverType（路由表由 codegen 生成，§9.2） |
| 选节点 | 已绑定 → DIRECT 到固定 nodeID；未绑定（如登录前取不到 uid）→ CONSISTENT_HASH，key=connID，保证同连接稳定落点 |
| 存 pending | gate 记 `pendingSeq → conn`，是 RPC callback 能找回原连接的根据（§8.8） |
| 转发 | body 原始 bytes **零拷贝透传**，gate 无需知道业务结构；header 带 ClientMeta(uid/session_id/gate_node_id) |

**转发路由策略**（gate 转发客户端入口请求到业务服时选节点）：

```
若 session.BoundNodes[entry.ServerType] 已设置：
    → DIRECT 到该 nodeID
否则（未绑定）：
    → CONSISTENT_HASH，routing_key = session.ConnID
      （gate 不解析 body，无法取 player_id，故用 connID 做 key；
       登录成功后由 BindSession 写入 BoundNodes，后续转 DIRECT）
```

> room 这类 match 动态分配的节点无法用哈希命中，必须由业务服显式调 `GateService.SetBound` 写入 BoundNodes（见 §8.9）。

**gate 转发用 Send（fire-and-forget），不阻塞**。业务服 Handler 处理完若需回客户端，通过 `Reply` 回包，框架按 §8.8 的 pending 映射发回原 gate → 原连接。

### 8.7 Middleware

```go
type HandlerFunc func(ctx Ctx, session *Session, msg *Message) error
type Middleware  func(next HandlerFunc) HandlerFunc

// 框架内置
RecoverMiddleware()   // panic 恢复（panic → ERR_INTERNAL）
AuthMiddleware()      // 认证：检查 session.Authed；白名单来自 gen_routes 生成的 AuthWhitelist
```

### 8.8 gate pending + 转发

`gate pending` 是客户端请求转发后的回写表，和 `rpc.Core.pending` 分层独立：前者负责 `pendingSeq → conn/rspCmdID/timer`，后者负责服务间 RPC 的 `seq → callback/timer`。`pendingSeq` 是 gate 内部 `uint64`，和客户端内层 `Message.SeqID(uint16)` 不同，也不写入业务 payload。

```go
type pendingReq struct {
    conn       conn.Connection
    pendingSeq uint64
    rspCmdID   uint32
    timer      *timewheel.Timer
}

// _Req：转发到业务服，回包后写回原连接
pendingSeq := nextPendingSeq()
pendingMap[pendingSeq] = &pendingReq{conn: conn, pendingSeq: pendingSeq, rspCmdID: rspCmdID, timer: timer}

rpcCore.Call(target, route, body, ctx, func(payload []byte, code ErrCode) {
    req := pendingMap[pendingSeq]
    if req == nil { return } // 超时 / 断线 / 已回写
    delete(pendingMap, pendingSeq)
    req.timer.Stop()
    req.conn.Send(encodeFrame(req.rspCmdID, payload))
})

// _Ntf：单向转发，无 pending
rpcCore.Send(target, route, body, ctx)
```

所有 `pendingMap` 操作均在主循环内完成；超时、迟到回包、断线后的回包统一按 `req == nil` 丢弃。

### 8.9 GateService：SendToClient / BindSession / SetBound

`SendToClient` 是业务服主动下行到网关的 one-way RPC，和上行的 `forwardToBackend`（§8.6）构成一对；RPC 应答仍由 §8.8 的 gate pending 自动回原连接。

```protobuf
service GateService {
    option (framework.kind)        = BACKEND;
    option (framework.server_type) = ST_GATESVR;

    rpc SendToClient(RPC_SendToClient_Ntf) returns (google.protobuf.Empty);   // 主动推消息给指定 uid 的客户端
    rpc BindSession(RPC_BindSession_Ntf) returns (google.protobuf.Empty); // 登录成功后写 UID + 建索引 + Authed + BoundNodes
    rpc SetBound(RPC_SetBound_Ntf) returns (google.protobuf.Empty);    // 设置/更新单个 serverType 的亲和节点
}

message RPC_SendToClient_Ntf {
    int64  uid        = 1;
    uint64 session_id = 2;
    uint32 cmd_id     = 3;
    bytes  payload    = 4;
}
message RPC_BindSession_Ntf {
    string session_id = 1;
    int64  uid        = 2;
    map<uint32, uint32> bound_nodes = 3;  // serverType → nodeID（至少含 lobby）
}
message RPC_SetBound_Ntf {
    int64  uid         = 1;
    uint32 server_type = 2;
    uint32 node_id     = 3;
}
```

- **SendToClient**：业务服只调 `player.SendToClient(ntf)`；框架内部自动组装 `RPC_SendToClient_Ntf(uid/session_id/cmd_id/payload)` 并调用 `rpc.Gate.At(gateNodeID).SendToClient(...)`。gate 只看信封、不解码 payload，按 `uid/session_id` 找连接后直接发 `cmd_id + payload`，实现 **payload 零解析、零重组**。payload 既可以是客户端请求的 `_Rsp`，也可以是服务器主动下发的 `_Ntf`。

```go
// 业务层只传 `ntf`，框架内部自动组装 RPC_SendToClient_Ntf 后投递到 gate。
```

> `cmd_id` 由 codegen 生成并与客户端协议表一致；业务服只 marshal 一次，gate 不做二次 unmarshal / re-marshal。
>
> **失败语义**：`uid` 找不到 session、`session_id` 不匹配、`cmd_id` 非法、连接已关闭时直接丢弃并计数；`conn.Send` 写队列满时按连接异常关闭处理。

- **BindSession**：lobby 登录成功后调用（`At` 玩家所在 gate）。gate 据此写 `session.UID`、建 `byUID` 索引、置 `Authed=true`、写 `BoundNodes`（至少含 lobby）。框架不做"响应自动回填"，亲和一律显式设置。
- **SetBound**：match 分配 room 后，由 lobby/match 调用写 `session.BoundNodes[room]=roomNodeID`，使后续 room 请求走 DIRECT。

### 8.10 握手流程（雏形简化，两次）

```
Client → Server : Handshake    (0x01) { version, platform }
Server → Client : HandshakeAck (0x02) { err_code:OK, heartbeat_interval:10 }
── 握手完成，建立 Session，进入正式收发 ──
```

握手失败回 `err_code != OK` 并关闭连接。token 校验是 lobbysvr Login 的职责，gate 握手不做业务校验。
> TCP 已保证可靠，服务端发出 HandshakeAck 后即可进入收发，无需第三次确认。

---

## 九、代码生成工具

### 9.1 options.proto

`ServerType` 是全局基础类型，同时被生成器、业务代码和 NodeID 编解码（§五）使用，**单独定义在 `protocol/common/node.proto`**；`options.proto` import 引用，不重复定义。

```protobuf
// protocol/common/node.proto
enum ServerType {
    ST_UNKNOWN     = 0;  // 保留，不可用于业务节点
    ST_GATESVR     = 1;
    ST_LOBBYSVR    = 2;
    ST_ROOMSVR     = 3;
    ST_MATCHSVR    = 4;
    ST_ONLINESVR   = 5;
    ST_ROUTERAGENT = 6;
}
```

```protobuf
// protocol/common/options.proto
import "protocol/common/node.proto";

enum ServiceKind {
    FRONTEND = 0;  // 客户端入口协议：生成 Handler 路由表/鉴权白名单
    BACKEND = 1;   // 服务间 RPC 协议：生成 Service/Stub 及 rpc 包级变量
}

// service 级 option（描述服务整体）
extend google.protobuf.ServiceOptions {
    ServiceKind kind        = 50001;
    ServerType  server_type = 50002;
}

// message 级 option（描述消息本身）
extend google.protobuf.MessageOptions {
    uint32 cmd_id  = 50003;  // 该消息的客户端帧 CmdID(4B)；cmd_id=0 保留，生成器 lint 拒绝
    bool   no_auth = 50004;  // 该上行消息免鉴权（默认需鉴权）
}
```

> **cmd_id=0 保留**：0 表示"无响应"（`RspCmdID=0`），不得在任何 message option 里显式使用；gen_routes 和 protoc-gen-svcstub 均 lint 拒绝 `cmd_id=0`，违规即报错退出。
>
> cmd_id 不分 req/rsp——每条消息（req、rsp、ntf）各自在自己的 message option 里声明一个 cmd_id。生成器按命名约定（`_Req`↔`_Rsp`）和 service 的 rpc `returns` 声明，把 req 的 cmd_id 与其 rsp 的 cmd_id 关联进路由表。
>
> 单/双向不用 method option 标记，由 §7.4 的命名约定（`_Ntf` + `returns Empty`）强制 lint 判定。

### 9.2 gen_routes

扫描所有客户端入口的 `FRONTEND` kind service 及其引用的 message，生成 Handler 路由表与鉴权白名单：从 service 取 `serverType + route`，从 input message 取 req `cmd_id`，从 output message 取 rsp `cmd_id`（`returns Empty` 时 `RspCmdID=0`），从 input message 的 `no_auth` 决定是否进白名单。

```go
// protocol/gen/routes.go（自动生成）
var RouteTable = map[uint32]RouteEntry{
    2050: {ServerType: ST_LOBBYSVR, Route: "LobbyHandler/ClaimReward", RspCmdID: 2051},
    2052: {ServerType: ST_LOBBYSVR, Route: "LobbyHandler/SyncPos",     RspCmdID: 0},
}

var AuthWhitelist = map[uint32]bool{
    2052: true,  // CS_SyncPos_Ntf（message option no_auth=true）
}
```

> gen_routes 同时校验 cmd_id 全局唯一且非零（撞号或 cmd_id=0 均报错退出）。

### 9.3 protoc-gen-svcstub

自研 protoc 插件，根据 `framework.kind` 生成不同产物，**生成前先跑命名 lint，违规即报错退出**。

**命名 lint 规则（摘要）**：

| 规则 | 违规示例 | 处理 |
|------|---------|------|
| FRONTEND service 名必须以 `Handler` 结尾 | `service Lobby` | 报错退出 |
| BACKEND service 名必须以 `Service` 结尾 | `service LobbyRPC` | 报错退出 |
| 有 `returns(non-Empty)` 的 method，input 必须 `_Req` 结尾，output 必须 `_Rsp` 结尾 | `rpc Login(LoginReq)` | 报错退出 |
| `returns(Empty)` 的 method，input 必须 `_Ntf` 结尾 | `rpc SyncPos(CS_SyncPos_Req) returns (Empty)` | 报错退出 |
| `_Rsp` 消息不得出现在 FRONTEND service input 中 | `rpc Foo(SC_Foo_Rsp)` | 报错退出 |
| cmd_id=0 不得出现在任何 message option | `option (framework.cmd_id) = 0` | 报错退出 |

（完整规则见 §7.4）

| kind | 生成产物 |
|------|---------|
| FRONTEND | 生成 `gen/handler/xxx_handler.go`：Handler 接口（含 Reply）+ RegisterXxxHandler 函数（客户端入口） |
| BACKEND | 生成 `gen/service/xxx_service.go`：Service 接口（含 Reply）+ RegisterXxxService 函数 + Stub 类型（含 At/ByHash/Broadcast/Timeout 链 + 自动 Guard）+ `rpc` 包级变量（服务间 RPC） |

此外汇总所有 `BACKEND` kind 服务，生成 `protocol/gen/rpc.go`：各服务包级 Stub 变量 + `rpc.Init(core)`，以及编排原语 `Join2/3/4`。

> **性能**：用 **vtprotobuf 风格生成（无反射 Marshal）**，规避 protobuf 反射序列化。CPU 大头是序列化（架构无关）；GC 来源是 inflight/闭包分配，池化列入后续。生成代码本身无瓶颈，全局吞吐上限 = 单核串行（靠多进程扩展）。

生成文件放 `protocol/gen/`，不手改：`gen/handler/*.go` 放客户端入口 Handler，`gen/service/*.go` 放服务间 RPC 的 Service/Stub，`gen/rpc.go` 放包级 Stub 变量与 `rpc.Init(core)`。

**生成器质量约束（写死）**：

- **幂等**：同一份 proto 多次执行生成，输出 bit-for-bit 一致（含注释头的时间戳禁用）；CI 用 `git diff --exit-code protocol/gen/` 校验，有 diff 则 CI 失败。
- **gofmt**：生成的 `.go` 文件在写出前过 `gofmt`，提交后直接可读，不需要手动格式化。

### 9.4 gen_proto.sh

```bash
#!/bin/bash
# 1. 生成 .pb.go
protoc --go_out=. --go_opt=paths=source_relative \
    protocol/common/node.proto \
    protocol/common/options.proto \
    protocol/common/errcode.proto \
    protocol/ra/ra.proto \
    protocol/cs/*.proto \
    protocol/ss/*.proto \
    protocol/handler/*.proto \
    protocol/service/*.proto

# 2. 生成 service stub（含命名 lint）
protoc --svcstub_out=protocol/gen \
    protocol/handler/*.proto \
    protocol/service/*.proto

# 3. 生成路由表
go run tools/gen_routes/main.go \
    --proto protocol/cs \
    --out protocol/gen/routes.go
```

---

## 十、启动与退出

> 配置系统已独立成文，完整设计见 `docs/superpowers/specs/config-system-reference.md`。
> 本节仅保留与框架生命周期直接相关的接入点。

### 10.1 App 启动生命周期

启动总流程与 §4.1 完全一致：`Init → OnAfterInit → WaitReady → Run`。

- `bootstrap` 负责把 `config / log / ragent` 这三层装好，业务 `main.go` 只注册自己的模块。
- 配置、日志、ragent 的访问方式分别见 §4.1 的包级访问表。
- 业务模块之间的依赖获取规则也与 §4.1 完全一致：**构造函数注入优先，`GetModule[T]` 只作 escape hatch**。

这一节只保留总览，不再重复展开实现细节。

### 10.3 模块就绪机制（WaitReady）

`WaitReady` 的定位、串行顺序和 `Ready` 原语都已经在 §4.1 定义完毕；这里仅保留一个结论：

- 需要异步就绪的模块实现 `ReadyWaiter`。
- `WaitReady` 按注册顺序串行等待。
- 不实现则跳过，零侵入。

> 选择串行而非并发，是因为启动期的真实依赖关系就是注册顺序，模块数又很少；并发带来的隐性依赖和排障成本大于收益。

### 10.4 main.go 结构（各服务近乎一致）

各服务入口结构统一：cobra 负责 flag 解析和配置校验，`RunE` 里一行 `bootstrap.New()` 完成基础设施组装，再注册业务模块。

```go
// cmd/gatesvr/main.go
func main() {
    var (
        configFiles  []string
        serverIndex  int32
        validateOnly bool
    )

    root := &cobra.Command{
        Use:          "gatesvr",
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, _ []string) error {
            // ① --validate-config：校验配置文件，通过即退出（CI 用）
            if validateOnly {
                if err := bootstrap.Validate(configFiles); err != nil {
                    return err
                }
                fmt.Printf("config OK: %v\n", configFiles)
                return nil
            }

            // ② bootstrap.New 组装地基：config + log + ragent
            a := bootstrap.New(configFiles)

            // ③ 注册业务模块
            a.Register(acceptor.NewModule(tcp.NewAcceptor(), ws.NewAcceptor()))
            a.Register(gatesvr.NewModule())
            return a.Run()
        },
    }

    f := root.Flags()
    f.StringArrayVarP(&configFiles, "config", "c",
        []string{"run/common/conf/", "run/gatesvr/conf/"},
        "config dirs or files (dirs auto-glob *.yaml)")
    f.Int32Var(&serverIndex,  "server-index",    -1,    "override node.server_index")
    f.BoolVar( &validateOnly, "validate-config", false, "validate config then exit (for CI)")

    addVersionCmd(root)
    if err := root.Execute(); err != nil {
        os.Exit(1)
    }
}
```

lobbysvr 完全一致，`a.Run(app.WithTick(100*time.Millisecond))` 加 tick；RouterAgent 无 tick，`a.Run()`。

**`bootstrap` 包接口**：

```go
// core/bootstrap/bootstrap.go
func New[T any](commonFiles, configFiles []string, opts ...Option) *app.App {
    a := app.New(
        config.NewModule(commonFiles, configFiles),
        log.NewModule(),
    )
    a.RegisterInfra(ragent.NewModule())
    for _, o := range opts { o(a) }
    return a
}

// Validate 只做配置校验，不启动 App（供 --validate-config 和 CI 使用）
func Validate[T any](configFiles ...string) error {
    _, err := configgen.Load[T](configFiles...)
    return err
}

// WithServerIndex：--server-index flag 覆盖 node.server_index
func WithServerIndex(cmd *cobra.Command, idx int32) Option {
    return func(a *app.App) {
        if cmd.Flags().Changed("server-index") {
            config.Startup[*gen.GatesvrConfig]().Node.ServerIndex = uint32(idx)
        }
    }
}
```

> **后续计划**：客户端 SDK 生成模型（基于 `protocol/handler/*.proto` 生成 `stub.Lobby.*` 调用代理与 push handler 注册）先不展开，作为独立后续任务处理。

### 10.5 标准 CLI Flags（全服务共用，写死）

每个服务二进制的 cobra root command 必须注册以下 flag，不允许各服务自行增删：

| Flag | 短名 | 类型 | 默认值 | 说明 |
|------|------|------|--------|------|
| `--config` | `-c` | string[] | `run/<svr>/conf/<svr>*.yaml` | 配置文件路径，可重复指定多个文件（多文件合并加载） |
| `--server-index` | — | int32 | `-1`（不覆盖） | 覆盖 `node.server_index`，横向扩展多副本时使用 |
| `--validate-config` | — | bool | false | 校验配置通过后退出（exit 0），供 CI 使用 |

子命令（所有服务统一）：

| 子命令 | 说明 |
|--------|------|
| `<svr> version` | 打印版本（二进制编译时注入 `git describe --tags`）并退出 |

**`--validate-config` 语义（写死）**：

```
① configgen.Load[T]（ExpandUpperEnv → yaml.Unmarshal → RequiredFields/EnvFields 校验）
② cobra flag 覆盖（Changed 判断，仅 --server-index）
③ 打印 "config OK: <files>"，exit 0
```

> 静态字段保护（ReloadableFields 新旧快照对比）是热更专用，首次加载无旧快照，`--validate-config` 不涉及。

CI 阶段用法：

```bash
./lobbysvr --config run/lobbysvr/conf/lobbysvr.yaml --validate-config
```

`addVersionCmd` 实现（各服务共享，放 `pkg/app/cobra.go`）：

```go
var (
    Version   = "dev"           // ldflags: -X pkg/app/cobra.go.Version=$(git describe --tags)
    BuildTime = "unknown"
)

func addVersionCmd(root *cobra.Command) {
    root.AddCommand(&cobra.Command{
        Use:   "version",
        Short: "print version and exit",
        Run: func(*cobra.Command, []string) {
            fmt.Printf("%s version %s (built %s)\n", root.Use, Version, BuildTime)
        },
    })
}
```

**编译时注入版本**：

```makefile
LDFLAGS := -X 'game-server-pro/pkg/app.Version=$(shell git describe --tags --always)' \
           -X 'game-server-pro/pkg/app.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)'

build:
    go build $(LDFLAGS) -o bin/lobbysvr ./cmd/lobbysvr
```

### 10.5.1 实现约束总表（写死）

| 主题 | 结论 |
|------|------|
| 启动参数 | `configFiles []string` 由 cobra `--config` flag（可重复）传入；在配置加载前，入口层只知道路径 |
| 配置模块 | `core/config.Module` 自己负责首次加载、SIGHUP 热更和快照持有；`bootstrap` 只负责把路径传进去 |
| 快照存储 | 使用 `atomic.Pointer[T]` 持有类型化 struct 快照；`Startup[T]()` 直接返回，不做类型断言 |
| 启动失败语义 | `Init()` / `OnAfterInit()` / `WaitReady()` 失败直接宕机；热更失败保留旧快照并告警；`OnStop()` 失败只记日志但继续停 |
| 业务依赖 | 业务模块之间默认构造函数注入；`GetModule[T]` 仅作为 escape hatch |
| 路由分发 | `dispatcher` 走 codegen 生成注册代码，不使用手写 msgID map 作为默认方案 |
| 队列模型 | `taskqueue` 先单队列，只保留 `Post(fn)` |
| 可观测面 | 健康检查 / ready / debug 面后续再补；当前不作为雏形实现范围 |
| 测试支架 | 后续再加，不阻塞当前实现 |

### 10.6 优雅退出

**信号处理**：App 在主循环外监听 `SIGTERM` / `SIGINT`，收到后进入退出序列。整个退出过程有总超时兜底（默认 15s），超时后强制 `os.Exit(1)`。

**退出序列（写死）**：

```
收到 SIGTERM / SIGINT
        │
        ▼
① [gatesvr] Acceptor.Close()           停止接受新连接
        │
        ▼
② [gatesvr] 等 pending drain           最多 drainTimeout（默认 5s，见 gate config）
            pending 全部写回或超时后继续
        │
        ▼
③ App.Stop() → 各模块 OnStop 倒序执行
        │
        ├─ 业务模块 OnStop：停止 tick，不再处理新消息
        ├─ ragent  OnStop：revoke etcd lease → RA 摘除其下所有节点（快速下线）
        └─ logger  OnStop：flush 日志缓冲
        │
        ▼
④ 进程退出（exit 0）
```

**pending drain 实现**：

```go
// gatesvr 优雅退出时
func (g *GateSvr) drain(timeout time.Duration) {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        if len(g.pendingMap) == 0 {
            return
        }
        time.Sleep(10 * time.Millisecond)
    }
    // 超时：强制清空 pending，剩余客户端请求丢弃并计数
    log.Warnf("drain timeout, %d pending requests dropped", len(g.pendingMap))
}
```

**各进程退出行为（写死）**：

| 进程 | 收到 SIGTERM 后的关键动作 |
|------|--------------------------|
| gatesvr | 停止 Acceptor → drain pending → OnStop |
| lobbysvr / roomsvr | 直接 OnStop（无 pending drain，ragent revoke lease 摘节点） |
| routeragent | revoke 所有代管业务节点 lease → 关闭所有 UDS/TCP 连接 → OnStop |

> drain 超时后强制退出，不无限等待；总超时（15s）兜底，确保滚动发布时旧进程不卡住。
## 十一、通用库（pkg/）

| 包 | 职责 |
|----|------|
| `pkg/logger` | 日志封装（zap） |
| `pkg/configgen` | 配置加载机制：`Load[T]`、`CheckReloadable`、`ExpandUpperEnv`；config struct 由 gen_config 从 proto FileDescriptorSet 生成，详见配置系统文档 |
| `pkg/serialize` | 仅保留 protobuf 序列化封装 |
| `pkg/taskqueue` | channel-based MPSC 投递队列，跨 goroutine 回调投递，主循环单消费 |
| `pkg/event` | 进程内同步事件总线（泛型 `Subscribe[T]/Publish[T]`），主循环内同步调用，用于模块间解耦 |
| `pkg/timewheel` | 多层哈希时间轮，slot 粒度 100ms，**仅供业务服由主循环 `Update()` 驱动**（`Advance(dt)`），O(1) 插入/删除，供心跳超时、pending map 超时复用。**整个进程只有一个时间轮实例**，每个连接/每条 pending 在其中注册一个 timer 条目。**无锁：契约要求仅单 goroutine（主循环）调用**（RA 事件驱动不用 timewheel，改用 `time.AfterFunc`） |

> 错误码构造 `New/From` + `Error` 接口在 **`internal/core/errcode`**（依赖项目专属 `ErrCode`，绑定本项目，不满足 pkg「任何项目可复用」契约，故归框架核心，见 §7.12），不在 `pkg/`。

**pkg/event API 草案**：

```go
package event

type Bus struct{ ... }
func NewBus() *Bus
func Subscribe[T any](b *Bus, fn func(T))
func Publish[T any](b *Bus, evt T)   // 同步调用所有订阅者（主循环内）
```

---

## 十二、后续计划（雏形之外）

| 能力 | 说明 |
|------|------|
| KCP 支持 | `internal/core/acceptor/kcp_acceptor.go` |
| 断线重连 | Session 比 Connection 活得更长 |
| 配置热更 | SIGHUP 触发，ReloadableFields 表保护静态字段，详见配置系统文档 |
| taskqueue 无锁化 | 替换为真正的无锁 MPSC（接口不变） |
| RA 按需 watch | world/节点规模增大后，从全量 watch 优化为按需 watch |
| bootstrap CLI | `tools/bootstrap` 一键生成服务骨架 |
| RPC 自动重试 | 仅幂等 RPC（需幂等标记） |
| 显式 cancel 传播 | v1 靠 deadline + Guard，后续支持 cancel(seq) 向下游传播 |
| 过载保护 | 雏形已做 taskqueue 满载阻塞背压 + 限频错误日志（见 §4.5）；生产阶段升级：任务分级双队列（回包不可丢/上行可丢）、每帧任务配额 maxPerTick 保 Update、入口限流、`len(pending)` 超阈值拒绝新请求 |
| taskqueue 可观测 | 队列深度 / 每周期峰值(highWater，Swap 清零) / 丢弃计数 接 Prometheus；配 `high_water/capacity` 饱和度 + dropped 告警；补帧间隔抖动直方图反映主循环卡顿 |
| inflight/闭包池化 | sync.Pool 复用，降 GC |
| Broadcast quorum | scatter-gather 只等多数 |
| Probe 告警 | UDS 连接断开时的监控告警 |
| 跨 world 多 world 治理 | 框架已支持跨 world DIRECT 寻址；运营层面的 world 编排后续完善 |
