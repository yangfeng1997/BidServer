# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 文档维护约定

本文件不是一次生成后固定不变的；当代码结构发生变化时，请主动同步更新，让它始终反映当前代码现状。

需要重点同步的变化：
- 新增、重命名、删除服务入口（`cmd/`、`internal/server/`）
- 配置 schema（`config/schema/*.proto`）和生成代码链路
- 核心生命周期/进程/日志行为（`internal/core/`、`pkg/logger/`）
- 构建与配置流程（`Makefile`、`tools/*.py`）
- 命令入口、flags、信号语义、热更语义

`config/gen/*.go` 是生成产物，任何变动都应回溯到 `config/schema/*.proto` 再决定是否需要更新文档。

## 默认跳过的目录

- `other_projects/`：这里是独立参考项目，默认不要读取、grep 或遍历；只有用户明确说“查看参考项目 X”时才进入对应目录。
- `docs/TODO/`：这里是临时方案/讨论文档，不是正式参考资料，默认跳过；只有用户明确要求时才打开。

## 回答准则

- 架构/设计问题要基于真实工程做法回答，优先使用行业里实际存在的方案名称，而不是抽象优劣。
- 不确定行业主流时就直说不确定，不要把训练记忆当事实。
- 评估方案时要落到本仓库的具体符号上，比如 `internal/core/app/app.go`、`internal/server/gate/config.go`、`config/schema/*.proto`。
- 当行业有事实标准时，直接给默认推荐，不要把罕见路径并列成同等选项。
- 如果发现旧抽象和新需求存在地基级冲突，要先把冲突点和可选方案说清楚，再改代码，不要硬叠补丁。

## 项目概述

BidServer 是一个用 Go 编写的多服务游戏服务器框架。当前有两个服务：`gatesvr`（网关）和 `lobbysvr`（大厅）。每个服务是独立二进制，共享一套核心框架：生命周期、配置、日志、进程管理。

模块路径是 `project`，Go 版本是 1.26。

## 常用命令

构建与运行通过 `Makefile` 编排，底层调用 `tools/*.py`：

```sh
make gen-config            # 由 proto schema 重新生成 config/gen/*.go
make config ENV=dev        # 烘焙运行时配置到 run/
make build                 # 编译 cmd/{svr} 到 build/，再拷贝到 run/{svr}/bin/
make test                  # go test ./...
make fmt                   # go fmt ./...
make all                   # config + build + test
make clean                 # 删除 build/ 和 run/
```

开发时的典型顺序：

```sh
make gen-config
make config ENV=dev
make build
run/startall.sh
```

服务启动顺序是 `run/startall.sh` → 各 `run/{svr}/bin/start.sh`（默认以 `--daemon` 拉起）→ `run/stopall.sh` 反向停止。

常用测试与检查：

```sh
go test ./config/gen/...
go test ./some/package -run 'TestName$' -v
sh -n run/gatesvr/bin/start.sh
python -m py_compile tools/config.py
```

`tools/build.py` 依赖 `run/ENV`，所以通常要先跑 `make config ENV=dev` 再 `make build`。

## 架构速览

### 1. 服务入口与 App 生命周期

每个服务入口（`cmd/{svr}/main.go`）都遵循同一模式：

1. 用 `pflag` 绑定 flags
2. 校验必填配置路径
3. 如果 `--daemon` 开启，先调用 `process.StartDaemon()`
4. 构造对应服务的 `Builder`
5. `Build()` 出 `App`
6. 写 PID 文件
7. 调用 `app.Startup()`

`internal/core/app` 是生命周期中心：

- `Startup()` 会先把 `App` 注入到各 module，再依次执行 `Init()`、`AfterInit()`
- 之后进入信号循环，处理 `dieChan` 和 `sigChan`
- 退出时逆序执行 `BeforeShutdown()`、`Shutdown()`，再广播 `dieNotifyChan`，最后等待登记过的业务 goroutine 结束
- 业务 goroutine 必须通过 `AddRoutine()` / `DoneRoutine()` 登记，否则停服时不会被等待
- `Shutdown()` 必须保持幂等，使用“先检查再 close”的方式，不能直接重复 close channel

### 2. Module / Hook / Resource 的边界

这里的判断标准很明确：**只有具备完整 `Init` / `AfterInit` / `BeforeShutdown` / `Shutdown` 生命周期的能力单元，才适合做 Module。**

不适合做 Module 的通常应做成资源或 hook：
- 进程级 OS 资源：PID 文件、daemon fork
- App 自身协调机制：信号循环、WaitGroup、模块注册表
- 纯副作用 hook：pprof、纯清理、纯重载逻辑
- 横切基础设施：logger、config、metrics/trace、全局调度器

适合做 Module 的，是那些有真实连接/断开语义的外部依赖，比如 Redis/etcd client、TCP/WebSocket acceptor。

### 3. 服务构建方式

`internal/server/gate/` 和 `internal/server/lobby/` 的 builder 是当前模板。

构建顺序一般是：

1. 先加载 common config 和各服务自己的 config
2. 把配置 entry 放到包级变量里，供运行期访问
3. 先根据配置初始化 logger group
4. 再创建 `app.BaseBuilder`
5. 注册 shutdown hook 和 reload hook

这意味着：
- 配置先于 logger 先就绪
- 服务自己的 `ReloadConfig()` 通过 app 的 reload hook 接入
- 各 server 包会保留包级 `XxxConfig()` / `ReloadConfig()` / `SetXxxConfigEntry()` 这类访问点

### 4. 进程管理

`internal/core/process` 管理 daemon、信号和 PID 文件：

- `StartDaemon()` 通过 fork + `Setsid` 把子进程脱离终端
- 子进程用 `GSP_DAEMON_CHILD=1` 识别自己，避免再次 fork
- `WatchedSignals()` 当前关注 `SIGINT`、`SIGQUIT`、`SIGTERM`、`SIGHUP`
- 语义上：`SIGINT` / `SIGQUIT` 直接终止，`SIGTERM` 走优雅停服，`SIGHUP` 预留给热更
- PID 文件由二进制自身写入和清理，`stop.sh` 只负责读取 PID 并发信号

### 5. 配置系统

配置链路分两阶段，而且两阶段都用 `${...}` 占位符，但语义不同：

**阶段 A：烘焙时**（`tools/config.py` + `tools/config_bake.py`）
- `config/{common,gate,lobby}.yaml` 是模板
- 占位符从 `config/values/{env}.yaml` 替换
- 结果写入 `run/{svr}/conf/*.yaml`
- 同时生成 `run/{svr}/bin/`、`run/{svr}/log/`、`run/startall.sh`、`run/stopall.sh`

**阶段 B：运行时**（`internal/core/config/loader.go`）
- `LoadYAML` 会再次扫描 `${...}`
- 这次从进程环境变量替换
- 缺失会直接报错
- `config/secrets/dev.env.example` 列出运行时要注入的环境变量

配置类型由 proto 驱动、代码生成：
- `config/schema/*.proto` 是唯一事实来源
- `tools/configgen/main.go` 生成 `config/gen/` 下的结构体、校验、加载、entry、reload 检查
- `config/gen/*.go` 是生成代码，不能手改
- 默认字段都不可热更，只有 proto 标记了 `(config.reload) = true` 的字段才允许在 `SIGHUP` 时变化
- `(config.env) = true` 和 `(config.reload) = true` 不能同时存在

运行时配置承载在 `config.ConfigEntry[T]` 上：
- 内部用 `atomic.Pointer[T]` 做无锁读
- `Reload()` 用互斥锁串行重载
- 各 server 包只保留包级 entry 指针和访问函数

### 6. 日志

`pkg/logger` 是业务侧依赖的日志抽象：

- `Logger` 接口是强类型的，没有 `interface{}` 可变参
- `Backend` 是三方日志库适配层
- `zap_adapter.go` 是 zap 实现
- `rotate.go` 负责按大小/按小时切割

`internal/core/logger` 负责把配置落成实际 logger：
- 创建 main / res / tracing 三路 logger
- 赋给包级变量 `logger.Main` / `logger.Res` / `logger.Tracing`
- 注册 shutdown hook
- `--daemon` 场景下会强制关闭 stderr 旁路输出

## 约定

- `config/gen/*.go` 是生成产物，禁止手动编辑
- `run/` 是构建产物目录，不要提交其中的二进制、日志和烘焙配置
- 修改配置模板时，要区分 `${...}` 是烘焙阶段替换还是运行时替换
- 服务列表以 `config/values/{env}.yaml` 里的 `svr_list` 为准，`tools/config.py` 和 `tools/build.py` 都从这里读取