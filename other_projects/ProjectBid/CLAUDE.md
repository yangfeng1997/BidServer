# CLAUDE.md

本文件为 Claude Code 在 `other_projects/ProjectBid` 中工作时的索引与工程约定。它只保留**必须先读的入口**、**当前仓库事实**、**工作流**和**修改规则**；更细节的行为以源码和测试为准。

> **维护约定**：凡是新增、删除、移动目录或包，修改启动流程、Builder 配置、组件生命周期、网络协议、RPC / service discovery、session、配置项、示例或生成链路，都要同步更新本文件，避免文档和代码脱节。

---

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

---

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 标识符命名遵循 Go 习惯，不为了中文语义破坏可读性。
- 修改既有英文或第三方风格文件时，保持原文件语言风格。

---

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

---

## 常用命令

```bash
# 编译全部包
go build ./...

# 运行全部测试
go test ./...

# 只跑核心包测试
go test ./application ./service ./session ./conn/... ./route ./pipeline ./timer ./ratelimit ./health

# 运行示例
go run ./examples/simple
```

当前仓库没有顶层 `Makefile`，优先使用 Go 原生命令。

---

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

---

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

`shutdown()` 应按反向顺序停止网络、集群、组件和时间轮。修改这里时要特别注意重复关闭 channel、goroutine 泄漏和组件顺序。

### Service / Module 排序

`Builder` 对 Service 与 Module 分别做拓扑排序：

- Service 使用 `WithServiceBefore(name)` / `WithServiceAfter(name)`
- Module 使用 `WithModuleDependsOn(names...)`
- 重名或循环依赖会在 `Build()` 阶段返回错误

---

## 网络与消息链路

前端消息处理主干在 `service/handler_service.go`：

1. acceptor 收到连接
2. `HandlerService.Handle(conn)` 创建 agent
3. 启动 agent 写循环
4. 读取底层消息并由 packet decoder 解包
5. 首包执行 handshake，失败返回 handshake error 并关闭连接
6. 收到 `HandshakeAck` 后设置为 working
7. Data packet 解出 `message.Message`
8. route 解析后，目标类型等于本服务则投递到本地处理队列
9. 目标类型不同且有 NATS client，则走远程 RPC
10. 否则记录 warning

重要约束：未完成握手前收到 Data 会关闭会话。

---

## Handler / Remote 约定

### 本地 Handler

使用 `app.Register(comp, opts...)` 注册。组件必须实现 `component.Component`，公开方法由反射提取为 handler。

典型签名：

```go
func (h *Handler) SayHello(ctx context.Context, req *pb.Request) (*pb.Response, error)
func (h *Handler) Ping(ctx context.Context) error
```

### Remote Handler

使用 `app.RegisterRemote(comp, opts...)` 注册。远程 RPC 请求由 `service.RemoteService` 接收，经 `HandlerPool.ProcessHandlerMessage` 分发。

远程请求会根据 `cluster.Request.Session` 创建临时 remote session，并把 session 注入 context。

### Pipeline

- 本地 handler hooks 由 `AddBeforeHandlerHook` / `AddAfterHandlerHook` 注册
- 远程 hooks 由 `AddBeforeRemoteHook` / `AddAfterRemoteHook` 注册
- hook 能改 context / 入参 / 出参，也能返回错误中断处理

---

## 配置约定

配置结构在 `config/config.go`，默认值由 `config.Default()` 给出。

主要配置组：

- `Name` / `DisplayName` / `Version`
- `GracefulTimeout`
- `Signals`
- `Heartbeat`
- `Buffer`
- `Acceptor`
- `Cluster`
- `Timer`

配置方式：

- 函数式选项：`config.WithName(...)`、`config.WithVersion(...)`、`config.WithNatsURL(...)` 等
- YAML / Viper：`config.NewConfigFromFile(...)`

修改配置字段时，需同步：

- `Config` struct
- `Default()` 默认值
- `options.go` 中的函数式选项
- `viper_config.go` 的加载行为
- `examples/simple/main.go` 中的示例注释（如相关）

---

## Protobuf 与生成产物

- `protos/cluster.proto` 是 cluster RPC 协议源文件
- `protos/cluster.pb.go` 是生成产物，通常不要手改
- `protoc_install/`、`protoc.zip`、`simple.exe` 是当前目录中的工具 / 二进制残留，修改代码时不要依赖它们作为源码事实

如果改 `.proto`，应重新生成对应 `.pb.go` 并说明所用命令或环境。

---

## 工程纪律

### 实现

- 所有外部输入都要校验：handshake、消息 payload、route、RPC request、配置、session data。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理重复请求、断线、重连、并发、超时、路由目标缺失、NATS 失败、etcd 不可用。
- 关键失败路径要返回明确错误并打有用日志。
- 修改网络、RPC、session、timer、pipeline 时要特别注意 goroutine 泄漏、channel 阻塞、重复 close 和竞态。
- 保持代码风格与周边一致：简体中文注释、显式错误、少量但有用的日志。

### 清理

- 清掉自己改动产生的孤儿：未用 import、变量、函数、临时测试代码和生成残留。
- 既有死代码不主动删除；发现明显陈旧代码，先在结果里指出。

### 测试

- 测试从设计意图和文档约定推导，不要为了迁就实现去改测试。
- 当文档与实现冲突导致测试失败时，先报告冲突点、源码位置和文档依据。
- 外部依赖不便本地运行时，优先跑局部包测试和 `go test ./...` 的纯单测。

---

## 上下文加载规则

- 改启动 / 装配时，先读 `application/build.go`、`application/app.go`、`examples/simple/main.go`。
- 改组件生命周期时，先读 `component/`、`application/app.go`。
- 改 handler / remote 时，先读 `service/`、`pipeline/`、`route/`。
- 改网络协议时，先读 `acceptor/`、`agent/`、`conn/packet`、`conn/message`、`conn/codec`。
- 改 session / handshake 时，先读 `session/`、`agent/`、`service/handler_service.go`。
- 改 NATS / etcd 时，先读 `cluster/`、`discovery/`、`service/remote_service.go`。
- 改配置时，先读 `config/`。
- 改 timer / group / rate limit / health 时，先读对应目录和测试。

---

## 规则优先级

规则冲突时：

1. 任务明确要求优先。
2. 源码和测试优先于本文件。
3. 本文件管工程纪律、工作流和上下文加载。
4. 更具体的局部约定优先于宽泛约定。
5. 不明确时先问，不要默默猜。
