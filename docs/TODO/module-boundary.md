# Module 边界标准

判别一个组件该做成 Module 还是资源/hook,用一条标准:**Module 是有完整 `Init/AfterInit/BeforeShutdown/Shutdown` 业务生命周期的能力单元。** 凡是「被 module 依赖的基础设施」「app 自身的协调机制」「进程级 OS 状态」「纯副作用 hook」都不该做成 Module。

---

## 判别规则

四类不适合做成 Module:

### 1. 进程级 OS 资源(app 存在之前就要建立)

生命周期绑二进制进程,先于 app 存在,没有对应的 Init 语义。

- **PID 文件**(`internal/core/process/pidfile.go` 的 `WritePIDFile`/`RemovePIDFile`):`main.go` 在 `app.Startup()` 前后写/删。
- **守护进程化**(`internal/core/process/daemon.go` 的 `StartDaemon`):app 构造前 fork,不在 app 生命周期内。

### 2. App 自身的协调机制(管理 module 的东西,不能反过来被 module 管理)

做成 module 是循环依赖——谁来驱动它的 Init?

- **信号循环** `runLoop` / `sigChan` / `dieChan` / `dieNotifyChan`(`internal/core/app/app.go`):驱动所有 module 生命周期的引擎本身。
- **WaitGroup**(`AddRoutine`/`DoneRoutine`/`waitRoutines`):停服等待的协调原语,属 app 容器。
- **模块注册表本身**(`modulesMap`/`modulesArray`/`RegisterModule`)。

> 这类也呼应「App 接口偏胖」的待办:`AddRoutine`/`DoneRoutine`/`DieChan` 等应从 `App` 接口收回,而非暴露给业务。

### 3. 纯副作用 hook(只有清理/重载,没有 Init 语义)

只有 `Close()` 或只有 `Reload()` 的逻辑,对应 `AddShutdownHook`/`AddReloadHook`。`Module` 接口里**没有 Reload 方法**,热更走 app 的 `reloadHooks`——纯重载逻辑做成 hook 比 module 自然,塞 module 要用空 `Init/AfterInit` 占位。

- **pprof server**:作者已按资源处理(`BaseApp.startPprof` 启动 + shutdown hook),需"启动即就绪"(模块 Init 阶段可能就要抓 profile),只有 `Close()` 清理。
- **任何纯清理/纯重载逻辑**:未来的限流参数刷新、指标重采样等。

### 4. 横切基础设施(将来若引入,与 logger/config 同类)

所有 module 热路径都要用,必须先就绪,横切关注点。

- **metrics / trace sink**:与 logger 完全同构,资源 + 注入。
- **全局调度器 / ticker 服务**:prerequisite,先于业务 module 就绪。

> logger 和 config 也属此类,详见 `framework-issues.md` 的「Logger / Config 改造方案」。

---

## 反向边界:什么适合做成 Module

有真实连接/断开语义的外部依赖,有完整生命周期:

- **Redis client / etcd client**:`Init` 连接、`Shutdown` 关闭、`AfterInit` 注册就绪。
- **TCP/WebSocket acceptor**:`Init` 监听、`Shutdown` 停接、`BeforeShutdown` 停止接入 drain。

共享客户端靠注册顺序保证先于消费者 init,消费者用 `app.GetModule("xxx")` 取。

---

## 一句话记忆

> 「app 用来管 module 的东西」和「module 还没 init 就要用的东西」,都不能是 module。

前者是第 2 类,后者是第 1/4 类,纯清理重载是第 3 类。
