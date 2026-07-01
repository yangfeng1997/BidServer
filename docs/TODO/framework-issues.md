# 框架待办与改造清单

记录当前框架已识别的问题与改造方向。按优先级排列,标注影响范围与改造落点。

---

## P0 — 包级全局状态(最大的架构债)

**现状**
- `internal/server/{gate,lobby}/config.go` 用包级 `*ConfigEntry` 指针 + `SetXxxConfigEntry` 注入,`CommonConfig()`/`GateConfig()`/`ReloadConfig()` 都依赖包级变量。
- `internal/core/logger/logger.go` 用包级 `Main`/`Res`/`Tracing` 变量 + `logger.SetGlobal`。
- `tools/configgen` 生成的 entry 本身是干净对象,问题只在最后一层包级包装。

**后果**
- 单进程无法跑两份同类型 server 实例,集成测试难隔离。
- 依赖靠"先 Set 再用"的隐式顺序,`Set` 漏调时 `Get` 返回 nil,运行时才崩。
- 并发测试/单测难做,跨包隐式初始化顺序无法静态分析。

**改造方向**:把"包级变量 + Set 注入"改成"Builder 持有实例,构造时传给 Module"。详见 `docs/refactor-logger-config.md`(待补)或下方「Logger / Config 改造方案」。

---

## Logger / Config 改造方案

### Logger:去掉包级 `Main/Res/Tracing`

`LoggerGroup` 现在是空 struct,真正 logger 都赋给了包级变量。改成有状态实例:

```go
// internal/core/logger/logger.go
type LoggerGroup struct {
    main    logger.Logger
    res     logger.Logger
    tracing logger.Logger
    closers []*logger.LogCloser
}

type Loggers interface {
    Main() logger.Logger
    Res() logger.Logger
    Tracing() logger.Logger
}

func (g *LoggerGroup) Main() logger.Logger    { return g.main }
func (g *LoggerGroup) Res() logger.Logger     { return g.res }
func (g *LoggerGroup) Tracing() logger.Logger { return g.tracing }
```

- `NewLoggerGroup` 删掉 `Main = mainLogger` 赋值,改 `g.main = mainLogger`。
- 删掉包级 `var Main/Res/Tracing` 和 `logger.SetGlobal(Main)`(若需全局 fallback 另议)。
- 业务 Module 构造时显式要:`NewGateModule(cfg, logs Loggers)`,热路径用 `m.logs.Main().Info(...)`。

### Config:包级 entry 包成 `ConfigManager`

```go
// internal/server/gate/config.go
type ConfigManager struct {
    common *CommonConfigEntry
    gate   *GateConfigEntry
    mu     sync.Mutex
    hooks  []ConfigChangeHook
}

func NewConfigManager(common *CommonConfigEntry, gate *GateConfigEntry) *ConfigManager {
    return &ConfigManager{common: common, gate: gate}
}
func (m *ConfigManager) Common() *configgen.CommonConfig { return m.common.Get() }
func (m *ConfigManager) Gate() *configgen.GateConfig     { return m.gate.Get() }
func (m *ConfigManager) AddChangeHook(h ConfigChangeHook) { /* 加锁 append */ }
func (m *ConfigManager) Reload() error { /* 原 ReloadConfig 逻辑 */ }
```

- 删掉包级 `commonConfigEntry`/`gateConfigEntry`/`configChangeHooks` 与 `Set*`/`*Config()`/`ReloadConfig`。
- lobby 包同理。

### Builder 集中装配

```go
func NewGateBuilder(opts Options) *Builder {
    common := mustLoadCommonConfig(opts.CommonConfigPath)
    gate := mustLoadGateConfig(opts.GateConfigPath)
    cfgMgr := gate.NewConfigManager(common, gate)
    logGrp := newLoggerGroup(opts.BaseOptions, gate.Get().LoggerGroup)

    b := app.NewBaseBuilder(nil)
    b.SetDaemon(opts.Daemon)
    b.SetPprof(opts.Pprof, opts.PprofAddr)
    b.AddShutdownHook(logGrp.Shutdown)
    b.AddReloadHook(cfgMgr.Reload)
    // b.AddModule(net.NewGateModule(cfgMgr, logGrp))
    return &Builder{BaseBuilder: b, cfg: cfgMgr, logs: logGrp}
}
```

### 为什么不让 config/logger 当 Module

它们是其他 Module 的前置依赖,必须在任何 `Init()` 前就绪。Builder 构造期创建天然保证顺序;注册成 Module 会把顺序交给注册顺序,埋雷。**它们是"资源"不是"模块"**,只挂 shutdown/reload hook。

### 顺带两点

1. `mustLoad...` 的 panic 改返回 error,走 `Build()` 正常 error 链,去掉 `newLoggerGroup` 里手写的 `_ = closer.Close()` 补丁。
2. 热更放宽到日志级别:`config/schema/types.proto` 里 `LogConfig` 的 `level`/`stderr_also` 加 `(config.reload) = true` → `make gen-config`。`LoggerGroup` 订阅 `ConfigChange` hook 原子替换 zap logger。

### 改造范围

两个 `internal/server/*/config.go`、两个 `builder.go`、一个 `internal/core/logger/logger.go`,加未来业务 Module 构造签名。不涉及 `internal/core/app` 和 `internal/core/config`,改动收敛。

### 收益

可测、可多实例、依赖显式、静态分析友好。

---

## P1 — 两阶段 `${...}` 占位符 footgun

**现状**:同一 `${...}` 语法两个替换源:
- 烘焙时(`tools/config.py`):从 `config/values/{env}.yaml` 替换。
- 运行时(`internal/core/config/loader.go` 的 `LoadYAML`):从进程环境变量替换。

**后果**:key 放错阶段要么静默成功,要么运行时报 "env vars not injected"。

**改造方向**:模板里用不同前缀区分,如 `${var:xxx}`(烘焙)vs `${env:xxx}`(运行时),或在文档强约束命名约定(values 用小写下划线、env 用大写)。

---

## P1 — 构造期大量 panic

**现状**:`mustLoadCommonConfig`/`mustLoadLobbyConfig`/`newLoggerGroup` 直接 `panic`。

**后果**:panic 跳过 defer 链清理,`newLoggerGroup` 已用手写 `_ = closer.Close()` 打补丁对抗副作用。

**改造方向**:全部改返回 error,经 `Build()` 透传,`main.go` 已有 `os.Exit(1)` 兜底。

---

## P1 — 热更可用面太窄

**现状**:只有 `heartbeat_sec` 标了 `(config.reload) = true`。`LogConfig` 字段全没标,改日志级别会被 `CheckXxxReload` 拒绝回滚。

**改造方向**:放开 `level`/`stderr_also` 等天然适合热更的字段;`LoggerGroup` 订阅 change hook 重建 logger。

---

## P2 — daemon 子进程丢 stdout/stderr 重定向

**现状**:`start.sh` 用 `exec ./{svr} --daemon 1>>stdout.log 2>>stderr.log` 接好日志文件,但进程内 `StartDaemon` fork 子进程时 `cmd.Stdout=nil`/`Stderr=nil`,子进程标准输出被丢到 /dev/null。

**后果**:daemon 子进程里 `fmt.Println`、第三方库 stderr、panic 栈全丢。业务日志走 zap 文件不受影响。

**改造方向**:子进程继承父进程 fd,或显式文档化"daemon 下只信文件日志"。

---

## P2 — `App` 接口偏胖

**现状**:`BaseApp` 混了生命周期 + 模块注册表 + WaitGroup 管理 + 信号循环,`App` 接口暴露 `AddRoutine/DoneRoutine/DieChan` 等内部机制给业务。

**改造方向**:模块注册和 routine 计数拆出去,接口收敛到业务真正需要的(配置/日志/停服触发)。

---

## P2 — 测试覆盖薄

**现状**:只有 `config/gen` 热更约束测试和 logger example 测试。

**缺口**:`internal/core/app` 生命周期时序、`internal/core/process` daemon/signal、`internal/core/config.LoadYAML` 环境变量展开均无单测——恰是易 bug 处(幂等 close、信号分支、env 缺失)。

**改造方向**:优先补这三块的表驱动单测。

---

## P3 — 工具间重复

**现状**:`DEFAULT_SERVICES` 在 `tools/config.py` 和 `tools/build.py` 各定义一份,真源是 `values/*.yaml` 的 `svr_list`,两份默认值有漂移风险。

**改造方向**:抽公共模块,或强制只信 `svr_list`、删除硬编码默认值。

---

## P3 — 业务/网络层尚未开始

**现状**:`gatesvr`/`lobbysvr` 只接了配置和日志,没有业务 Module 注册,无网络层/协议层。

**方向**:接一个真实网络层(如 TCP/WebSocket accept + 协议编解码)验证 Module 抽象是否够用;这是验证框架设计是否成立的下一步。

---

## 关联

- 总体评价见对话记录(2026-07-01 评审)。
- 配置系统与生命周期为"准生产级",本文件聚焦待还的债。
