# CLAUDE.md

本文件为 Claude Code 在 `other_projects/ProjectBid` 中工作时的总索引。目标是让你最快从项目根定位到应用、服务、会话、集群和配置热点。

> **维护约定**：凡是新增、删除、移动目录或包，修改启动流程、Builder 配置、组件生命周期、网络协议、RPC / service discovery、session、配置项、示例或生成链路，都要同步更新本文件，避免文档和代码脱节。

## 最快路径

| 需求 | 直接看 |
|---|---|
| 应用装配 / 启动 | `application/build.go`、`application/app.go` |
| 组件生命周期 | `component/component.go`、`component/base.go` |
| 业务 handler / remote | `service/handler_service.go`、`service/remote_service.go`、`service/handler_pool.go` |
| 会话 / handshake | `session/session.go`、`session/session_impl.go` |
| 网络接入 | `acceptor/`、`agent/`、`conn/codec/`、`conn/message/` |
| 集群 RPC | `cluster/cluster.go`、`cluster/nats_rpc_client.go`、`cluster/nats_rpc_server.go` |
| 服务发现 | `discovery/discovery.go`、`discovery/etcd_discovery.go` |
| 路由 / pipeline | `route/route.go`、`router/router.go`、`pipeline/pipeline.go` |
| 配置 | `config/config.go`、`config/options.go`、`config/viper_config.go` |
| 定时与辅助 | `timer/`、`group/`、`ratelimit/`、`health/` |
| 示例 | `examples/simple/main.go` |

## 项目概述

ProjectBid 是一个 Go 游戏服务器框架骨架，整体风格参考 Pitaya，但当前仓库已经有自己的包结构和简化实现。

当前仓库事实：

- 模块路径：`projectbid/server`
- Go 版本：`1.26.1`
- 主要入口：`application.NewBuilder(...)`、`Builder.Build()`、`Application.Start()`
- 配置：支持函数式选项与 Viper/YAML 加载
- 网络：支持 TCP / WebSocket / KCP acceptor
- 协议：Pomelo packet / message 编解码
- 序列化：JSON 与 Protobuf
- 集群：NATS RPC client/server + etcd discovery
- 示例：`examples/simple/main.go`

## 代码组织速览

| 目录 / 文件 | 职责 |
|---|---|
| `application/` | `Application` 生命周期、`Builder`、Service/Module 排序、RPC caller 代理、状态管理 |
| `component/` | `Component` 生命周期接口与 `Base` 空实现 |
| `service/` | Handler 反射注册、HandlerService、RemoteService、HandlerPool |
| `acceptor/` | TCP / WebSocket / KCP acceptor |
| `agent/` | 连接 agent、session 绑定、响应、推送、心跳相关逻辑 |
| `conn/packet/` | Pomelo 外层 packet 定义 |
| `conn/message/` | 内层 message 编解码与压缩 |
| `conn/codec/` | Pomelo packet encoder / decoder |
| `session/` | Session / SessionPool、bind、push、kick、handshake validation |
| `cluster/` | NATS RPC client/server、Server 信息、RPC 协议别名 |
| `discovery/` | ServiceDiscovery 接口与 etcd 实现 |
| `route/`、`router/` | route 解析、路由选择和路由错误 |
| `pipeline/` | handler / remote 前后置 hook |
| `group/` | group 与 group service |
| `timer/` | 时间轮、timer、cron、delay queue |
| `config/` | `Config`、函数式选项、Viper/YAML 加载 |
| `serialize/` | JSON / Protobuf serializer |
| `logger/` | zap 日志封装 |
| `metrics/`、`tracing/`、`health/`、`ratelimit/`、`worker/` | 横切能力 |
| `protos/` | cluster protobuf 与生成代码 |
| `examples/simple/` | 可运行的前端服务示例 |

## 常用命令

```bash
go build ./...
go test ./...
go run ./examples/simple
```

```bash
go test ./application ./service ./session ./conn/... ./route ./pipeline ./timer ./ratelimit ./health
```

当前仓库没有顶层 `Makefile`，优先使用 Go 原生命令。

## 快速读法

- 先看“最快路径”表，再打开对应模块的子目录 `CLAUDE.md`。
- 查服务逻辑优先看 `application/`、`service/`、`session/`。
- 查网络 / 协议优先看 `acceptor/`、`agent/`、`conn/`。
- 查集群 / 发现优先看 `cluster/`、`discovery/`。

## 核心启动链路

典型前端服务启动方式见 `examples/simple/main.go`：

1. 创建 packet decoder / encoder、message encoder、serializer
2. `application.NewBuilder(true, config.WithName(...), ...)`
3. `EnableAcceptor(...)` 启用客户端监听
4. 可选 `EnableTimeWheel(...)`
5. 注册 pipeline hooks
6. `AddService(...)` 注册基础设施 Service
7. `AddModule(...)` 注册业务 Module
8. 可选 `OnStartup(...)` / `OnShutdown(...)`
9. `Build()` 得到 `Application`
10. `app.Register(...)` 注册 Handler 组件
11. `app.Start()` 阻塞运行到关闭

后端服务需要配置集群：

- `EnableNats(url)`
- `EnableEtcd(discovery.EtcdConfig{...})`
- `RegisterRemote(...)` 注册远程 RPC handler

`Builder.Build()` 会校验前后端配置：

- 前端服务必须至少启用一个 acceptor
- 后端服务不允许启用 acceptor
- 后端服务必须配置 NATS 和 etcd

## 生命周期约定

### Component 生命周期

接口定义在 `component/component.go`：

```go
Name() string
Init(ctx context.Context) error
AfterInit(ctx context.Context) error
BeforeShutdown(ctx context.Context) error
Shutdown(ctx context.Context) error
```

`component.Base` 提供空实现，业务组件通常嵌入它，只覆写需要的阶段。

### Application 生命周期

`Application.Start()` 的主要阶段：

1. 设置启动时间并监听 `config.Signals`
2. `startup()`
3. 执行 `onStartup` 回调
4. 等待 `Shutdown()` 或系统信号
5. `shutdown()`
6. `logger.Sync()`

`startup()` 顺序：

1. 启动时间轮
2. Service `Init`
3. Service `AfterInit`
4. Module `Init`
5. Module `AfterInit`
6. 注册 etcd service discovery
7. 启动 NATS RPC server
8. 启动 acceptor、handler dispatch 和 accept loop
9. 设置状态为 `StateRunning`

### Service / Module 排序

`Builder` 对 Service 与 Module 分别做拓扑排序：

- Service 使用 `WithServiceBefore(name)` / `WithServiceAfter(name)`
- Module 使用 `WithModuleDependsOn(names...)`
- 重名或循环依赖会在 `Build()` 阶段返回错误

## 工程纪律

- 所有外部输入都要校验：配置、CLI 参数、协议消息、RPC 参数、连接状态。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理重复请求、重入、并发、乱序事件、空配置、脏数据、重连、超时。
- 主循环内不要做同步阻塞调用；跨 goroutine 的工作优先通过 `taskqueue.Post` 回投。
- 关键失败路径必须返回明确错误，并打印可定位问题的日志。
- 日志、错误信息、注释优先使用简体中文。

## 规则优先级

1. 任务明确要求优先。
2. 更具体的文档优先于更宽泛的文档。
3. 本文件管工程纪律、工作流和上下文加载。
4. 设计与协议事实以源码为准。
5. 不明确时先问，不要默默猜。
