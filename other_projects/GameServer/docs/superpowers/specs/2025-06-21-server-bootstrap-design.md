# 服务启动架构重构设计

> 日期：2025-06-21
> 目标：Module → Server，bootstrap 融入 app，Init → Run → Fini 三段式生命周期

---

## 一、动机

当前 `internal/xxxsvr/module.go` 以 Module 作为服务的顶层抽象，`cmd/xxxsvr/main.go` 里 cobra + bootstrap 代码重复严重（6 个服务几乎一模一样）。重构目标：

1. **Module → Server**：Server 嵌入 App，成为服务的顶层入口
2. **bootstrap 包名消失**：App 保持纯生命周期容器；新增 `internal/core/runtime` 作为上层工厂，避免 app↔config/log/ragent import cycle
3. **Init → Run → Fini 三段式**：和行业游戏服（Leaf、Nano、Pitaya）及公司 C++ 框架一致
4. **命令行参数**：cobra 只存在于 `internal/core/cmd` 和 `cmd/xxxsvr/main.go`；业务层只接收纯 Go 参数结构
5. **main.go 模板化**：所有服务统一使用 `corecmd.Command[T]`，显式传入 `NewOptions` 工厂；有额外 flag 的服务只补一个类型安全的 `addFlags` 函数

---

## 二、整体架构

```
cmd/gatesvr/main.go
  └── corecmd.Command(meta, gatesvr.NewOptions, addFlags, gatesvr.Main).Execute()

cmd/lobbysvr/main.go
  └── corecmd.Command(meta, lobbysvr.NewOptions, nil, lobbysvr.Main).Execute()

internal/core/app/
  ├── app.go         App 结构体 + Init/Run/Fini（纯生命周期容器）
  ├── module.go      Module 接口 + BaseModule
  └── options.go     BaseOptions / Options 接口 / CommandMeta

internal/core/runtime/
  └── app.go         NewApp(opt) 组装 app + config/log/ragent，替代旧 bootstrap 包名

internal/core/cmd/
  └── command.go     泛型 Command[T] / BindCommonFlags / AddVersionCmd（cobra 胶水）

internal/gatesvr/
  ├── gateserver.go  GateServer（嵌入 *app.App + 业务字段）
  └── options.go     Options（嵌入 app.BaseOptions + gate 独有字段）

internal/lobbysvr/
  ├── lobbyserver.go LobbyServer（嵌入 *app.App）
  └── options.go     Options（只嵌入 app.BaseOptions）
```

---

## 三、生命周期

```
corecmd.Command[T]
    │
    ├── 调用服务传入的 NewOptions() 创建默认参数
    ├── 绑定公共 flag 到 opt.Base()
    ├── 绑定服务独有 flag（可选 addFlags）
    └── cmd.Execute() 解析 argv 后调用 server.Main(opt)

server.Main(opt)
    │
    ├── ValidateConfig / OverrideServerIndex
    ├── Build/NewServer(opt)
    ├── server.Init()
    └── server.Run()    ← defer App.Fini()
```

服务运行期：

```
NewGateServer(opt)     ← 构造，注册业务 Module
    │
server.Init()          ← 前期：驱动 Module.Init → AfterInit → WaitReady
    │                     Server 如需额外 hook，则 override Init 并调用 App.Init
    │
server.Run()           ← 运行期：主循环 + 信号监听（阻塞）
    │                     defer App.Fini() 保证清理
    │
App.Fini()             ← 停机：逆序 Module.BeforeStop → Fini
```

### 与行业对比

| | C++ HAppPlatform | Leaf | Nano | go-micro | 本设计 |
|---|---|---|---|---|---|
| **参数入口** | `HAppCtx` | 少量全局/模块配置 | `Options`/函数式 option | `Options` | `app.Options` 接口 + `BaseOptions` |
| **前期** | `HAppInit` | `leaf.Run` 内部 `OnInit` | `nano.Listen` 内部 `Init→AfterInit` | `New+Init` | `Init()` |
| **运行** | `HAppMainLoop` | `leaf.Run` 内部各模块 goroutine | 内部主循环 | `Run()` | `Run()` |
| **停机** | `HAppFini` | `OnDestroy` | `BeforeShutdown→Shutdown` | `Stop()` | `Fini()` (defer 保护) |

---

## 四、核心接口

### 4.1 Module 接口

```go
// internal/core/app/module.go
type Module interface {
    Init(*App) error
    AfterInit() error
    BeforeStop()
    Fini()
}

type ReadyWaiter interface { WaitReady(ctx context.Context) error }
type Updater     interface { Update(dt time.Duration) }

type BaseModule struct{}
func (BaseModule) Init(*App) error  { return nil }
func (BaseModule) AfterInit() error { return nil }
func (BaseModule) BeforeStop()      {}
func (BaseModule) Fini()            {}
```

变更：`OnAfterInit` → `AfterInit`，`OnBeforeStop` → `BeforeStop`，`OnStop` → `Fini`，去掉 `On` 前缀并与 Server 命名统一。

### 4.2 Options 接口

```go
// internal/core/app/options.go
type Options interface {
    Base() *BaseOptions
    Defaults()
}

type BaseOptions struct {
    ConfigFiles  []string
    ServerIndex  int32
    ValidateOnly bool

    // 框架调优，统一走参数结构，不再走 Option 模式
    Tick         time.Duration
    ReadyTimeout time.Duration
    DrainTimeout time.Duration
}

func (o *BaseOptions) Base() *BaseOptions { return o }
func (o *BaseOptions) Defaults() {
    o.ReadyTimeout = 10 * time.Second
}

type CommandMeta struct {
    Use   string
    Short string
    Confs []string
}
```

服务自己的 Options 嵌入 `BaseOptions`，并提供 `NewOptions()` 工厂：

```go
// internal/gatesvr/options.go
type Options struct {
    app.BaseOptions
    ListenAddr string
}

func NewOptions() *Options {
    opt := &Options{}
    opt.Defaults()
    return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() {
    o.BaseOptions.Defaults()
    o.ListenAddr = "0.0.0.0:7001"
}
```

无额外参数的服务也有自己的 Options 和 NewOptions，便于每个服务拥有统一 `Main(opt)` 签名：

```go
// internal/lobbysvr/options.go
type Options struct { app.BaseOptions }

func NewOptions() *Options {
    opt := &Options{}
    opt.Defaults()
    return opt
}

func (o *Options) Base() *app.BaseOptions { return &o.BaseOptions }
func (o *Options) Defaults() { o.BaseOptions.Defaults() }
```

### 4.3 App 结构体

```go
type App struct {
    infraModules []Module    // config, log, ragent（New 时注入）
    modules      []Module    // 业务 Module（Register 加入）

    tick         time.Duration
    readyTimeout time.Duration
    drainTimeout time.Duration
    q            *taskqueue.Queue
    quit         chan struct{}
    stopped      chan struct{}
    stopOnce     sync.Once
}
```

变更：`infraModules` + `frameworkModules` → `infraModules`，只保留两个列表。`tick` / `readyTimeout` / `drainTimeout` 从 `BaseOptions` 统一入口进入。

### 4.4 Server = 嵌入 App

没有 `Server` 接口。Go 嵌入即继承——服务 struct 嵌 `*app.App` 自动获得 `Init()` / `Run()` / `Fini()`。需要额外逻辑就 override 对应方法，内部调 App 版本：

```go
// 不需要额外逻辑 — 不 override
type LobbyServer struct{ *app.App }

// 需要额外逻辑 — override
func (gs *GateServer) Init() error {
    // 自定义前置逻辑
    if err := gs.App.Init(); err != nil { return err } // 调基类
    // 自定义后置逻辑
    return nil
}
```

---

## 五、App 生命周期实现

### 5.1 New 与 runtime.NewApp（统一入口）

`app.New` 是纯生命周期容器构造函数，不 import `config/log/ragent`，避免 import cycle：

```go
// internal/core/app/app.go
func New(opt *BaseOptions) *App {
    return &App{
        tick:         opt.Tick,
        readyTimeout: opt.ReadyTimeout,
        drainTimeout: opt.DrainTimeout,
        q:            taskqueue.New(0),
        quit:         make(chan struct{}),
        stopped:      make(chan struct{}),
    }
}
```

基础设施组装放在上层 runtime 包：

```go
// internal/core/runtime/app.go
func NewApp(opt *app.BaseOptions) *app.App {
    a := app.New(opt)
    a.RegisterInfra(config.NewModule(opt.ConfigFiles))
    a.RegisterInfra(corelog.NewModule())
    a.RegisterInfra(ragent.NewModule())
    return a
}
```

Server 构造时传入：

```go
// internal/gatesvr/gateserver.go
func NewGateServer(opt *Options) *GateServer {
    gs := &GateServer{listenAddr: opt.ListenAddr}
    gs.App = runtime.NewApp(&opt.BaseOptions)
    gs.Register(gs.newAcceptModule())
    gs.Register(gs.newDispatcherModule())
    return gs
}
```

### 5.2 Init() — 驱动 Module 层

App 只驱动 Module 的生命周期。Server 自身的前后 hook 由 Server 在自己的 `Init()` override 里显式调用。

```go
func (a *App) Init() error {
    all := a.allModules()

    for _, m := range all {
        if err := m.Init(a); err != nil {
            return fmt.Errorf("module %T Init: %w", m, err)
        }
    }
    for _, m := range all {
        if err := m.AfterInit(); err != nil {
            return fmt.Errorf("module %T AfterInit: %w", m, err)
        }
    }

    ctx, cancel := context.WithTimeout(context.Background(), a.readyTimeout)
    defer cancel()
    for _, m := range all {
        if w, ok := m.(ReadyWaiter); ok {
            if err := w.WaitReady(ctx); err != nil {
                return fmt.Errorf("module %T WaitReady: %w", m, err)
            }
        }
    }
    return nil
}
```

### 5.3 Run() — 主循环 + defer Fini + 信号

```go
func (a *App) Run() error {
    defer a.Fini()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        if a.drainTimeout > 0 {
            time.Sleep(a.drainTimeout)
        }
        close(a.quit)
    }()

    a.runLoop()
    return nil
}
```

### 5.4 Fini() — 停机清理

```go
func (a *App) Fini() {
    select {
    case <-a.quit:
    default:
        close(a.quit)
    }
    a.stopModules()
}

func (a *App) stopModules() {
    a.stopOnce.Do(func() {
        all := a.allModules()
        for i := len(all) - 1; i >= 0; i-- {
            all[i].BeforeStop()
        }
        for i := len(all) - 1; i >= 0; i-- {
            all[i].Fini()
        }
        close(a.stopped)
    })
}
```

---

## 六、泛型 Command 方案

### 6.1 corecmd.Command[T]

`internal/core/cmd` 封装 cobra，`internal/core/app` 保持零 CLI 依赖。Options 由服务显式提供 `NewOptions` 工厂，避免反射。

```go
// internal/core/cmd/command.go
func Command[T app.Options](
    meta app.CommandMeta,
    newOptions func() T,
    addFlags func(cmd *cobra.Command, opt T),
    run func(opt T) error,
) *cobra.Command {
    opt := newOptions()
    opt.Base().ConfigFiles = meta.Confs

    cmd := &cobra.Command{
        Use:          meta.Use,
        Short:        meta.Short,
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, _ []string) error {
            if opt.Base().ValidateOnly {
                return app.ValidateConfig(opt.Base().ConfigFiles)
            }
            if cmd.Flags().Changed("server-index") && opt.Base().ServerIndex >= 0 {
                config.OverrideServerIndex(uint32(opt.Base().ServerIndex))
            }
            return run(opt)
        },
    }

    BindCommonFlags(cmd, opt.Base(), meta.Confs)
    if addFlags != nil {
        addFlags(cmd, opt)
    }
    AddVersionCmd(cmd)
    return cmd
}
```

`corecmd.Command` 不创建具体 Options，只调用服务传入的 `NewOptions`。这样没有反射，启动路径完全显式。

### 6.2 公共 flag 绑定

```go
func BindCommonFlags(cmd *cobra.Command, opt *app.BaseOptions, defaultConfs []string) {
    f := cmd.Flags()
    f.StringArrayVarP(&opt.ConfigFiles, "config", "c", defaultConfs, "配置文件路径")
    f.Int32Var(&opt.ServerIndex, "server-index", -1, "覆盖 server_index")
    f.BoolVar(&opt.ValidateOnly, "validate-config", false, "校验配置后退出")
}
```

`cmd.Execute()` 内部解析 argv 后，pflag 会直接把值写入 `opt` 的字段指针。

### 6.3 无额外 flag 的服务 main.go

```go
// cmd/lobbysvr/main.go (codegen 生成)
package main

import (
    "os"

    "project/internal/core/app"
    corecmd "project/internal/core/cmd"
    "project/internal/lobbysvr"
)

func main() {
    cmd := corecmd.Command[*lobbysvr.Options](
        app.CommandMeta{
            Use:   "lobbysvr",
            Short: "大厅服务",
            Confs: []string{"run/lobbysvr/conf/"},
        },
        lobbysvr.NewOptions,
        nil,
        lobbysvr.Main,
    )
    if err := cmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

### 6.4 有额外 flag 的服务 main.go

```go
// cmd/gatesvr/main.go (codegen 生成)
package main

import (
    "os"

    "github.com/spf13/cobra"

    "project/internal/core/app"
    corecmd "project/internal/core/cmd"
    "project/internal/gatesvr"
)

func main() {
    cmd := corecmd.Command[*gatesvr.Options](
        app.CommandMeta{
            Use:   "gatesvr",
            Short: "游戏网关服务",
            Confs: []string{"run/common/conf/", "run/gatesvr/conf/"},
        },
        gatesvr.NewOptions,
        func(cmd *cobra.Command, opt *gatesvr.Options) {
            cmd.Flags().StringVar(&opt.ListenAddr, "listen-addr", opt.ListenAddr, "监听地址")
        },
        gatesvr.Main,
    )
    if err := cmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

---

## 七、每个服务的 Main(opt)

每个服务都提供自己的 `Main(opt)`。`main.go` 不直接 new server，只负责把解析后的纯参数传入服务入口。

### 7.1 无额外参数服务

```go
// internal/lobbysvr/lobbyserver.go
func Main(opt *Options) error {
    s := NewLobbyServer(opt)
    if err := s.Init(); err != nil {
        return err
    }
    return s.Run()
}

func NewLobbyServer(opt *Options) *LobbyServer {
    s := &LobbyServer{}
    s.App = app.New(&opt.BaseOptions)
    s.Register(NewLobbyModule())
    return s
}
```

### 7.2 有额外参数服务

```go
// internal/gatesvr/gateserver.go
func Main(opt *Options) error {
    gs := NewGateServer(opt)
    if err := gs.Init(); err != nil {
        return err
    }
    return gs.Run()
}

func NewGateServer(opt *Options) *GateServer {
    gs := &GateServer{listenAddr: opt.ListenAddr}
    gs.App = app.New(&opt.BaseOptions)
    gs.Register(gs.newAcceptModule())
    gs.Register(gs.newDispatcherModule())
    return gs
}
```

`ValidateOnly` 和 `ServerIndex` 是公共行为，统一在 `corecmd.Command[T]` 中处理。服务 `Main(opt)` 只做服务构建与生命周期启动。

---

## 八、模块注册

Server 在构造时注册业务 Module：

```go
func NewGateServer(opt *Options) *GateServer {
    gs := &GateServer{listenAddr: opt.ListenAddr}
    gs.App = app.New(&opt.BaseOptions)
    gs.Register(gs.newAcceptModule())
    gs.Register(gs.newDispatchModule())
    return gs
}
```

```go
func (a *App) Register(m Module) { a.modules = append(a.modules, m) }
```

---

## 九、迁移影响

### 文件变更清单

| 文件 | 变更 |
|------|------|
| `internal/core/app/app.go` | App 改为两列表；Init/Run/Fini 方法；New(opt *BaseOptions) |
| `internal/core/app/module.go` | OnAfterInit→AfterInit，OnBeforeStop→BeforeStop，OnStop→Fini |
| `internal/core/app/options.go` | 新增：BaseOptions / Options 接口 / CommandMeta |
| `internal/core/cmd/command.go` | 新增：泛型 Command[T] / BindCommonFlags / AddVersionCmd |
| `internal/core/app/option.go` | 删除（WithTick 等并入 BaseOptions） |
| `internal/core/bootstrap/*` | 删除 |
| `internal/xxxsvr/module.go` → `xxxserver.go` | Module → Server，嵌入 App |
| `internal/xxxsvr/options.go` | 新增每个服务的 Options 类型 |
| `cmd/xxxsvr/main.go` | 手写 boilerplate → 泛型 Command 模板 |

### 6 个服务变更对比

| 服务 | 新 main.go | NewOptions | addFlags | Options |
|---|---|---|---|---|
| gatesvr | `corecmd.Command[*gatesvr.Options]` | `gatesvr.NewOptions` | ✅ `--listen-addr` | `BaseOptions + ListenAddr` |
| lobbysvr | `corecmd.Command[*lobbysvr.Options]` | `lobbysvr.NewOptions` | ❌ nil | `BaseOptions` |
| onlinesvr | `corecmd.Command[*onlinesvr.Options]` | `onlinesvr.NewOptions` | ❌ nil | `BaseOptions` |
| matchsvr | `corecmd.Command[*matchsvr.Options]` | `matchsvr.NewOptions` | ❌ nil | `BaseOptions` |
| roomsvr | `corecmd.Command[*roomsvr.Options]` | `roomsvr.NewOptions` | ❌ nil | `BaseOptions` |
| routeragent | `corecmd.Command[*routeragent.Options]` | `routeragent.NewOptions` | ❌ nil | `BaseOptions` |

---

## 十、设计原则总结

1. **三段式生命周期**：Init → Run → Fini，和 C++ 框架及行业游戏服一致
2. **defer Fini = 安全**：比 go-micro 裸 `return Stop()` 更可靠，不管怎么退出 Fini 都会执行
3. **参数边界清晰**：cobra 只负责填充 Options，Server 只接收纯参数 struct
4. **app 包零 CLI 依赖**：cobra/pflag 封装在 `internal/core/cmd/`，app 包不引用任何 CLI 库
5. **main.go 模板统一**：所有服务都使用 `corecmd.Command[T]`，显式传 `NewOptions`；有额外 flag 只补 `addFlags`
6. **每服一个 Main(opt)**：每个服务都有明确启动入口，参数肯定显式传入
7. **全路径无反射**：Options 由服务自己的 `NewOptions` 创建，Server 构造和请求处理路径均显式无反射
