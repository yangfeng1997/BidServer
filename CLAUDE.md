# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 文档维护约定

本文件不是一次生成即固化的产物。当代码结构发生变化时，主动同步更新本文件，使其始终反映当前代码现状，避免过时内容误导后续会话：

- **需要同步的改动**：新增/重命名/删除服务（`cmd/`、`internal/server/`）、配置 schema（`config/schema/*.proto`）与生成代码、核心模块（`internal/core/`）、构建流程（`Makefile`、`tools/*.py`）、命令入口与 flags、日志结构、信号/进程语义等架构表面变化。
- **无需同步的改动**：函数内部实现逻辑、注释、纯重构等不改变架构表面的细节。
- `config/gen/*.go` 是生成产物，其变化应追溯到 `config/schema/*.proto` 的改动再决定是否更新文档。
- 完成会改动架构表面的任务后，对照「常用命令」「服务列表」「配置链路两阶段占位符」「proto→configgen 生成」「进程/信号语义」「日志结构」等章节核对，过时即用 Edit 更新，必要时新增章节。

## 参考项目目录（other_projects/）

`other_projects/` 下每个子目录是一个独立的参考项目（当前有 `BidKing`、`GameServer`、`ProjectBid`、`cherry`、`pitaya` 等），内容较多，**默认情况下不用查看和分析，可直接跳过该文件夹**。仅当用户主动指明「查看参考项目 X」（X 为项目名）时，才进入 `other_projects/X/` 查看对应代码。搜索、grep、文件遍历等操作应排除 `other_projects/`，避免把参考项目代码当作当前仓库代码处理。

## 项目概述

BidServer 是一个用 Go 编写的多服务游戏服务器框架。当前包含两个服务：`gatesvr`（网关）和 `lobbysvr`（大厅）。每个服务是一个独立的二进制，共享同一套核心框架（生命周期、配置、日志、进程管理）。

模块路径为 `project`（见 `go.mod`），Go 版本 1.26。

## 常用命令

构建与运行通过 Makefile 编排，底层调用 `tools/*.py`：

```sh
make config ENV=dev        # 烘焙运行时配置到 run/（生成 run/{svr}/conf/*.yaml、bin/{start,stop}.sh、startall.sh、stopall.sh、ENV）
make build                 # 编译 cmd/{svr} 到 build/，再拷贝到 run/{svr}/bin/
make test                  # go test ./...
make fmt                   # go fmt ./...
make gen-config            # 由 proto schema 重新生成 config/gen/*.go（修改 schema 后必须运行）
make all                   # config build test
make clean                 # 删除 build/ 和 run/
```

服务启动顺序（先启动的在前）：`run/startall.sh` → 各 `run/{svr}/bin/start.sh` 以 `--daemon` 方式拉起进程；`run/stopall.sh` 反向停止。改完代码后的完整流程：`make gen-config && make config ENV=dev && make build && run/startall.sh`。

测试单个包/用例：

```sh
go test ./config/gen/...
go test ./config/gen/ -run TestCheckGateReloadAllowsReloadFields -v
```

Python 工具依赖 `pyyaml`。验证脚本语法可用 `sh -n run/gatesvr/bin/start.sh`，Python 脚本可用 `python -m py_compile tools/config.py`。

## 架构

### 1. 服务启动与生命周期（internal/core/app）

每个服务入口 `cmd/{svr}/main.go` 遵循同一模式：用 `pflag` 绑定 flags → 校验必填配置路径 → 按 `--daemon` 决定是否 `process.StartDaemon()` → 构造 server `Builder` → `Build()` 出 `App` → 写 PID 文件 → `app.Startup()`。

`App` 接口（`app.go`）管理一组 `Module`，生命周期严格有序：
- `Startup()` 先注入 app 到各 module（`Set`），再依次调用 `Init` → `AfterInit`，然后进入信号循环 `runLoop`，退出时逆序调用 `BeforeShutdown` → `Shutdown`，最后 `close(dieNotifyChan)` 广播停服、`wg.Wait()` 等待业务 goroutine。
- 业务 goroutine 必须通过 `app.AddRoutine(1)` / `app.DoneRoutine()` 登记，否则停服时不会被等待。
- `Shutdown()` 用 `select{ default: close(dieChan) }` 保证幂等，**不要直接 `close(dieChan)`**。

新增服务：在 `cmd/` 下加 `main.go`，在 `internal/server/{name}/` 下实现 `Options`/`config.go`/`builder.go`（gate/lobby 是范本），并把服务名加入 `config/values/{env}.yaml` 的 `svr_list` 与 `tools/config.py` 的 `DEFAULT_SERVICES`/`SERVICE_CONFIG_FLAGS`。

### 2. 进程管理（internal/core/process）

- 守护进程化靠 `StartDaemon()`：父进程 `fork` 出子进程并 `Setsid` 脱离终端、释放，子进程通过 `GSP_DAEMON_CHILD=1` 环境变量识别自身，避免再次 fork。`tools/config.py` 生成的 `start.sh` 默认带 `--daemon`。
- 信号语义（`signal.go`）：`SIGINT`/`SIGQUIT` 终止，`SIGTERM` 优雅停服（drain），`SIGHUP` 热更。`stop.sh` 读 `{svr}.pid` 后发 `SIGTERM`。
- PID 文件由二进制自身在启动时写入、退出时清理（`pidfile.go`），`stop.sh` 只读取它来发信号。

### 3. 配置系统（核心，跨多文件理解）

配置链路分两阶段，**两阶段用同一种 `${...}` 占位符但语义不同**，容易混淆：

**阶段 A — 烘焙时（构建机器上，`tools/config.py` + `tools/config_bake.py`）：**
`config/{common,gate,lobby}.yaml` 是模板，其中的 `${name}` 由 `config/values/{env}.yaml` 的扁平化键替换（list 用逗号拼接）。结果写入 `run/{svr}/conf/*.yaml`。缺占位符会直接 `sys.exit` 报错。`config.py` 同时生成各服务的 `bin/`、`conf/`、`log/` 目录和启停脚本。

**阶段 B — 运行时（服务进程内，`internal/core/config/loader.go`）：**
`LoadYAML` 读 `run/{svr}/conf/*.yaml` 时再次扫描 `${...}`，这次从**进程环境变量**替换，缺失即报错。`config/secrets/dev.env.example` 列出需要注入的环境变量（本地 `source`，生产挂 K8s Secret）。proto 中标了 `(config.env) = true` 的字段就是走环境变量注入的。

**配置类型与校验由 proto 驱动、代码生成：**
- `config/schema/*.proto` 是唯一事实来源。`options.proto` 定义自定义 option：`root`（根配置，生成 Loader/Entry）、`server`/`common`（归属标记）、字段级 `required`/`reload`/`env`/`enum_values`。
- `tools/configgen/main.go`（`make gen-config`）解析 proto，生成 `config/gen/` 下 5 个文件：`config.go`（结构体）、`validate.go`（`Validate()`）、`loader.go`（`LoadXxx`+校验）、`entry.go`（`NewXxxConfigEntry`）、`reload.go`（`CheckXxxReload`）。**这些文件头部都是 `DO NOT EDIT`，改配置结构必须改 proto 后重新生成。**
- 热更约束在 `reload.go` 体现：**默认所有字段不可热更**，只有 proto 里标 `(config.reload) = true` 的字段（当前仅 `heartbeat_sec`）允许在 `SIGHUP` 时变化；其余字段变化会使 `Reload()` 报错回滚。`env` 与 `reload` 互斥（环境变量注入的字段必然不可热更）。
- 运行时配置以 `config.ConfigEntry[T]`（`internal/core/config/entry.go`）承载，内部用 `atomic.Pointer[T]` 做无锁读、`Reload()` 加锁重载。各 server 包（`internal/server/{gate,lobby}/config.go`）持有包级 `*ConfigEntry` 指针，提供 `XxxConfig()` 取值和 `ReloadConfig()`，并把 `ReloadConfig` 注册为 app 的 reload hook。

新增配置字段流程：改 `config/schema/*.proto` → `make gen-config` → 在 `config/values/*.yaml` 或模板里补值 → `make config ENV=dev` 重新烘焙。

### 4. 日志（pkg/logger + internal/core/logger）

`pkg/logger` 定义强类型 `Logger` 接口（`Field` 强类型，无 `interface{}` 可变参）和 `Backend` 适配接口，`zap_adapter.go` 是 zap 实现，`rotate.go` 支持按大小/按小时切割。`internal/core/logger` 依据 `LoggerGroupConfig`（main/res/tracing 三路）创建实例并赋给包级变量 `logger.Main`/`Res`/`Tracing`，注册为 shutdown hook。`StderrAlso` 在 `--daemon` 时强制关闭。业务代码应通过 `logger.Main` 等包级变量打日志，而非直接依赖 zap。

## 约定

- `config/gen/*.go` 是生成代码，**禁止手动编辑**；改动只能落在 `config/schema/*.proto` 后 `make gen-config`。
- `run/` 整体被 `.gitignore` 忽略（只保留目录骨架的 `.gitkeep`），是构建产物，不要提交其中的二进制/日志/烘焙配置。
- 配置占位符 `${...}` 在烘焙阶段（`config.py`）和运行时阶段（`LoadYAML`）都出现，修改模板时注意区分该占位符由谁替换。
- 服务列表以 `config/values/{env}.yaml` 的 `svr_list` 为准，`tools/config.py` 与 `tools/build.py` 都从中读取。
