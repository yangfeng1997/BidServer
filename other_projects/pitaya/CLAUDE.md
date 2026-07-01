# CLAUDE.md

本文件为 Claude Code 在 `other_projects/pitaya` 中工作时的总索引。目标是让你最快从项目根定位到 builder、app、service、session、cluster 和 docs 热点。

> **维护约定**：凡是新增、删除、移动目录或包，修改启动流程、Builder 依赖、组件 / 模块生命周期、RPC / service discovery、session、acceptor、协议、配置项、构建命令或生成链路，都要同步更新本文件以及对应文档。

## 最快路径

| 需求 | 直接看 |
|---|---|
| 应用装配 | `builder.go`、`app.go` |
| 组件 / 模块 | `component.go`、`module.go` |
| 业务 handler / remote | `service/handler.go`、`service/remote.go`、`service/handler_pool.go` |
| Session | `session/` |
| 集群 RPC | `cluster/` |
| 路由 / pipeline | `route/`、`router/`、`pipeline/` |
| 网络接入 | `acceptor/`、`agent/`、`conn/` |
| 配置 | `config/`、`docs/configuration.rst` |
| 协议 / 文档 | `docs/API.md`、`docs/communication.md`、`docs/builder.md` |
| 发现 / group / worker | `groups/`、`worker/`、`metrics/`、`tracing/` |

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| `CLAUDE.md` | 文档索引、工程纪律、上下文加载规则、项目事实 | 始终加载 |
| [`README.md`](README.md) | 项目总览、安装、示例运行、测试入口 | 先做整体了解时 |
| [`docs/overview.md`](docs/overview.md) | 功能与架构概览 | 理解框架边界时 |
| [`docs/API.md`](docs/API.md) | Handler / Remote API、签名、路由、生命周期 | 写业务 handler、remote、路由前 |
| [`docs/builder.md`](docs/builder.md) | Builder 与 post-build hook | 改 Builder 或装配方式前 |
| [`docs/communication.md`](docs/communication.md) | 客户端连接、握手、转发、remote service、pipeline | 改网络、消息处理、转发链路前 |
| [`docs/configuration.rst`](docs/configuration.rst) | Viper 配置项参考 | 改配置结构或默认值前 |
| [`docs/handshake-validators.md`](docs/handshake-validators.md) | 握手校验器 | 改 handshake validation 前 |
| [`docs/cli.md`](docs/cli.md) | pitaya-cli REPL 客户端 | 调试客户端交互或文档路由前 |
| [`docs/tracing.md`](docs/tracing.md) | tracing 说明 | 改 tracing 上下文传播前 |

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 修改既有英文文档或英文注释时，保持原文件语言风格。
- 标识符命名遵循 Go 习惯，不为了中文语义破坏可读性。

## 项目概述

Pitaya 是一个 Go 分布式游戏服务器框架，支持客户端连接、session、handler / remote、集群 RPC、service discovery、metrics、tracing、pipeline、worker 和 group。

当前仓库事实：

- 模块路径：`github.com/topfreegames/pitaya/v2`
- Go 版本：`1.25.0`
- 主要入口：`builder.go`、`app.go`
- 配置：使用 Viper，配置结构在 `config/config.go`
- 集群发现：默认 etcd service discovery
- 集群 RPC：NATS 与 gRPC 相关实现并存
- 客户端协议：Pomelo packet / message 编解码，TCP 与 WebSocket acceptor

## 快速读法

- 先看“最快路径”表，再打开对应目录的 `CLAUDE.md`。
- 改应用装配先看 `builder.go`、`app.go`。
- 改网络 / 连接先看 `acceptor/`、`agent/`、`conn/`、`service/handler.go`。
- 改集群 / 发现先看 `cluster/`、`groups/`、`worker/`。
- 改配置先看 `config/`、`docs/configuration.rst`。

## 代码组织速览

| 目录 / 文件 | 职责 |
|---|---|
| `builder.go` | 创建默认依赖：Server、Router、RPC client/server、ServiceDiscovery、Groups、Worker、Serializer、SessionPool、hooks |
| `app.go` | `Pitaya` 接口与 `App` 实现，启动、停止、RPC、group、push、kick、module、component 等总入口 |
| `component.go` | handler component / remote component 的注册、启动、停止 |
| `module.go` | framework module 注册、启动、session draining、停止 |
| `acceptor/` | TCP / WebSocket acceptor 与低层连接入口 |
| `acceptorwrapper/` | acceptor 包装器，如 rate limiting |
| `agent/` | 客户端连接 agent，与 session、编码、写队列相关 |
| `cluster/` | RPC client/server、service discovery、server metadata、NATS / gRPC 实现 |
| `component/` | 组件抽象、handler / remote 方法提取、service 元数据 |
| `config/` | Viper config、默认配置、PitayaConfig |
| `conn/` | packet / message 编解码与协议结构 |
| `context/` | RPC 上下文传播 |
| `groups/` | group service，内存和 etcd 实现 |
| `metrics/` | Prometheus / StatsD reporter 与指标上报 |
| `modules/` | 内置模块，如 binary、unique session、API docs |
| `pipeline/` | handler / remote before/after hooks |
| `route/`、`router/` | route 解析与按 server type 的路由选择 |
| `service/` | HandlerService、RemoteService、handler pool、RPC 分发主干 |
| `session/` | Session / SessionPool、bind、push、kick、handshake validators |
| `timer/` | 全局 ticker、timer / cron |
| `worker/` | reliable RPC job 与重试 worker |
| `pitaya-cli/` | REPL 客户端 |
| `xk6-pitaya/` | k6 扩展 |
| `docs/` | 用户文档 |

## 常用命令

```bash
make setup
make init-submodules
make unit-test-coverage
make test
make e2e-test
make e2e-test-nats
make e2e-test-grpc
make build-cli
make build-k6-extension
make protos-compile
make protos-compile-demo
make mocks
```

```bash
go test ./...
go test ./cluster -run TestName -count=1
go test ./service ./session ./agent -count=1
```

注意：`make test` 会依赖 Docker Compose 启动 `examples/testing` 下的 etcd / NATS，并会跑 e2e；只改局部包时优先跑对应包测试。

## 核心启动链路

典型装配链路：

1. `NewBuilderWithConfigs(...)` 或 `NewDefaultBuilder(...)` 创建 `Builder`
2. `Builder` 根据 `PitayaConfig` 创建默认依赖
3. `AddAcceptor(...)` 给 frontend server 添加 TCP / WebSocket acceptor
4. `Build()` 创建 `HandlerService`、`RemoteService`、`AgentFactory` 和 `App`
5. 业务用 `Register(...)` 注册 handler component
6. 业务用 `RegisterRemote(...)` 注册 remote component
7. 可选用 `RegisterModule(...)` 注册 framework module
8. `Start()` 启动 acceptor、handler dispatch goroutine、cluster modules、components、metrics 和信号监听

`Standalone` 模式不能有 RPC / service discovery 实例。`Cluster` 模式必须具备 `ServiceDiscovery`、`RPCClient`、`RPCServer`。

## 生命周期约定

### Component 生命周期

Handler / Remote 组件注册后由 `startupComponents()` 管理：

1. handler components：`Init → AfterInit`
2. remote components：`Init → AfterInit`
3. 注册到 `HandlerService` / `RemoteService`
4. 停止时按逆序执行 `BeforeShutdown → Shutdown`

Handler / Remote 的方法签名和路由规则见 `docs/API.md`。

### Module 生命周期

Module 接口定义在 `interfaces/interfaces.go`：

```go
Init() error
AfterInit()
BeforeShutdown()
Shutdown() error
```

`RegisterModuleBefore` / `RegisterModuleAfter` 决定模块顺序。停止时逆序执行。实现 `SessionModule` 的模块会参与 session draining。

### App 停止语义

`Start()` 监听 `SIGINT`、`SIGQUIT`、`SIGTERM`：

- `SIGTERM` 且 `pitaya.session.drain.enabled=true` 时进入 session draining
- draining 达到超时、收到 `SIGINT` 或 session 归零后继续关闭
- 关闭顺序包括 `sessionPool.CloseAll()`、`shutdownModules()`、`shutdownComponents()`
- `Shutdown()` 通过关闭 `dieChan` 触发停服，已做重复 close 保护

## 通信链路摘要

客户端请求在 cluster 模式下的大致路径：

1. 客户端连接 acceptor
2. `HandlerService.Handle(conn)` 创建 agent 并启动 agent goroutine
3. 首包必须是 handshake，服务端返回 serializer、dictionary、heartbeat
4. 客户端发送 handshake ack 后连接进入工作状态
5. Data 消息经 decoder 得到 packet / message
6. `HandlerService` 根据 route 判断本地 handler 或 remote 转发
7. remote 请求经 `RemoteService` 选择目标 server 并通过 RPC client 发出
8. 后端 remote server 收到 `_Sys_` RPC 后创建短生命周期 remote agent
9. 目标 handler 执行，response 回到 frontend
10. frontend agent 按原 MID 回包给客户端

重要边界：后端修改 session 不会自动同步到 frontend session，必须显式 push / commit 到 frontend。

## 配置与生成链路

### 配置

Pitaya 使用 Viper：

- 默认配置结构在 `config/config.go`
- 配置项说明在 `docs/configuration.rst`
- `NewPitayaConfig(config *Config)` 从 `pitaya` key 读取配置
- 修改配置字段时，需要同步默认值、文档、相关测试

### Protobuf / 生成

- `protos/*.pb.go` 是生成代码，通常不要手改
- `pitaya-protos/` 是 submodule 输入源之一
- `make protos-compile` 会生成 `protos/` 相关代码
- `make mocks` 通过 `mockgen` 生成 mocks

## 工程纪律

- 所有外部输入都要校验：客户端 handshake、消息 payload、RPC 参数、配置、metadata、session data。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理重复请求、断线、重连、并发、超时、路由目标缺失、RPC 失败、service discovery 暂不可用。
- 关键失败路径要返回明确错误并打有用日志。
- 更改网络、RPC、session、worker、timer、metrics 时要考虑 goroutine 泄漏、channel 阻塞、重复 close、竞态和测试清理。
- 修改 public API 时，同步 `docs/API.md` 和相关示例。

## 规则优先级

1. 任务明确要求优先。
2. 更具体的文档优先于更宽泛的文档。
3. 本文件管工程纪律、工作流和上下文加载。
4. 设计与协议事实以源码为准。
5. 不明确时先问，不要默默猜。
