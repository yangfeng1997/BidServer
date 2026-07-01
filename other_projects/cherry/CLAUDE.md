# CLAUDE.md

本文件为 Claude Code 在 `other_projects/cherry` 中工作时的索引与工程约定。它只保留**必须先读的入口**、**项目事实**、**工作流**和**修改规则**；架构细节拆到子文档，按任务按需加载。

> **维护约定**：凡是新增、删除、移动目录或包，修改架构设计、接口定义、启动流程、协议格式、配置结构、构建命令、日志/配置/发现语义时，都要同步更新本文件以及对应子文档，避免文档和仓库状态脱节。

---

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| `CLAUDE.md` | 文档索引、工程纪律、上下文加载规则、当前项目事实 | 始终加载 |
| [`README.md`](README.md) | 中文项目总览、快速开始、核心特性、架构图入口 | 先做整体了解时 |
| [`README.en.md`](README.en.md) | 英文版项目总览 | 需要英文描述或对外说明时 |
| [`_docs/README.md`](_docs/README.md) | 文档导航 | 查找更细分说明时 |
| [`_docs/env-setup.md`](_docs/env-setup.md) | 本地环境安装与配置 | 搭环境、跑示例前 |
| [`_docs/pomelo.md`](_docs/pomelo.md) | Pomelo 协议与接入说明 | 处理协议兼容或前端接入时 |
| [`net/discovery/README.md`](net/discovery/README.md) | discovery 子系统说明 | 改服务发现时 |
| [`net/proto/README.md`](net/proto/README.md) | 集群包与协议层说明 | 改跨节点消息时 |

---

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 标识符命名遵循 Go 习惯，不为了中文语义破坏可读性。

---

## 项目概述

Cherry 是一个基于 Go + Actor Model 的分布式游戏服务器框架。

- 模块路径：`github.com/cherry-game/cherry`
- 当前 Go 版本要求：`Go 1.24+`
- 核心入口：`cherry.go` → `application.go`
- 典型使用方式：`Configure(...)` 创建 `AppBuilder`，链式注册组件、序列化器、Actor、集群/发现/网络解析器，然后 `Startup()`

### 当前仓库中的主要分层

| 分层 | 目录 | 职责 |
|---|---|---|
| 应用装配 | `cherry.go`、`application.go` | `AppBuilder`、应用生命周期、组件注册、启动/停止 |
| Actor 执行 | `net/actor/` | Actor goroutine、mailbox、远程调用、事件、定时器 |
| 集群通信 | `net/cluster/`、`net/nats/`、`net/discovery/` | 跨节点 RPC、NATS 传输、成员发现 |
| 前端接入 | `net/parser/`、`net/connector/` | 协议解析、连接器、Session、Agent Actor |
| 网络协议 | `net/proto/` | 集群包与协议层消息 |
| 基础设施 | `logger/`、`profile/`、`extend/`、`facade/` | 日志、配置、工具库、接口定义 |

---

## 常用命令

```bash
# 编译 / 测试
go build ./...
go test ./...
go test -v ./...

# 只跑某个包或测试
go test -run TestX ./net/actor/
go test -count=1 ./net/discovery/

# 生成 protobuf 代码（按仓库内 Makefile/脚本约定）
make protoc

# 安装 protoc 插件
make init

# 更新 tag / 版本信息
make tag

# 重新整理依赖
make modtidy
```

### 代码改动前后常用检查

```bash
go test ./...
go test ./net/discovery/ ./net/actor/ ./logger/
```

如果只改了文档，至少确认文档引用的路径和名称仍然存在。

---

## 核心工作流

### 1. 应用启动链路

典型启动顺序：

1. `Configure(profileFilePath, nodeID, isFrontend, mode)` 创建 `AppBuilder`
2. `AppBuilder.Startup()` 在集群模式下自动补默认 `nats` cluster 和 discovery
3. `Register(...)` 追加自定义组件
4. `Application.Startup()` 自动注册 actor system
5. 若配置了 `INetParser`，则在前端节点加载连接器与协议处理
6. 组件按注册顺序执行 `Set → Init → OnAfterInit`
7. 收到 `SIGINT` / `SIGQUIT` / `SIGTERM` 或 `Shutdown()` 后，按逆序执行 `OnBeforeStop → OnStop`

### 2. 组件/接口边界

`facade/` 是框架对外契约层，新增能力时优先先补接口，再补实现。

- `IComponent`：带生命周期的组件
- `IApplication`：应用容器与服务访问入口
- `IDiscovery` / `ICluster`：集群与成员发现
- `IConnector` / `INetParser`：前端接入层
- `ISerializer`、`IActorSystem`、`IActorHandler`：序列化和 Actor 运行时

判断标准：

- **适合做组件**：有明确 `Set / Init / OnAfterInit / OnBeforeStop / OnStop` 生命周期、需要挂到应用里统一装配的能力
- **适合做全局单例**：进程级配置、日志、profile、discover registry 这类横切基础设施
- **适合做普通包**：纯工具函数、协议编解码、数据结构、错误码

### 3. 典型前端接入链路

前端节点通常同时涉及：

- `net/connector/`：TCP / WebSocket / HTTP 连接器
- `net/parser/`：Pomelo / Simple 等协议解析器
- `net/actor/`：客户端连接对应的 agent actor
- `net/proto/`：消息包封装与跨节点传输

两类协议格式在 README 中已经约定：

- **Pomelo**：`type(1b) + length(3b) + data`
- **Simple**：`id(4b) + dataLen(4b) + data`

### 4. 配置与 profile

`profile/` 是 Cherry 的启动配置入口，当前实现是**包级全局状态**：

- `Init(filePath, nodeID)` 读取 profile JSON
- 支持 `include` 合并
- `GetConfig(...)` 从全局配置树读取子配置
- `Path()` / `Name()` / `Env()` / `Debug()` / `PrintLevel()` 都依赖初始化后的全局状态

这意味着：

- 单进程里不要指望并存多个独立 Application
- 初始化必须发生在业务代码读取 profile 之前
- 配置和节点信息会影响日志初始化

### 5. 日志

`logger/` 提供框架级默认日志单例：

- `DefaultLogger` 是包级默认 logger
- `SetNodeLogger(node)` 会根据 profile 里的引用日志配置切换到节点日志
- `Flush()` 负责最终刷盘/同步
- 所有 `Debug/Info/Warn/Error/...` 都是对默认 logger 的封装

日志初始化通常要早于组件启动。

---

## 工程纪律

### 实现

- 所有外部输入都要校验：profile、节点配置、客户端消息、RPC 参数、连接器回调、组件名。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理重复请求、重入、并发、乱序事件、重复注册、空指针、非法节点 ID。
- 关键失败路径要返回明确错误并打印有用日志，不要只写 happy path。
- Actor / 集群回调如果会回到主循环或共享状态，要明确同步边界，避免跨 goroutine 竞态。

### 清理

- 清掉自己改动产生的孤儿：未用 import、变量、函数、临时测试代码。
- 既有死代码不主动删除；如果发现明显陈旧代码，先在结果里指出。

### 测试

- 测试从设计意图和文档约定出发，不要为了迁就实现去改测试。
- 当文档与实现冲突导致测试失败时，先报告冲突点、源码位置和文档依据。
- 外部依赖（etcd / NATS / 网络连接）不便本地运行时，优先跑纯单测和 `go build ./...`。

---

## 代码组织速览

### 入口与装配

- `cherry.go`：`AppBuilder`，负责 `Register`、`SetSerializer`、`SetDiscovery`、`SetCluster`、`SetNetParser`、`AddActors`
- `application.go`：`Application` 核心生命周期、组件注册、启动、停止、服务访问器

### 接口层

- `facade/application.go`
- `facade/component.go`
- `facade/cluster.go`
- `facade/connector.go`
- `facade/net_parser.go`
- `facade/serializer.go`
- `facade/session.go`
- `facade/actor.go`
- `facade/message.go`

### 基础设施与实现

- `logger/`：zap 封装、默认 logger、构建与管理
- `profile/`：profile JSON 加载、include 合并、节点配置解析
- `extend/`：通用工具库（时间轮、集合、字符串、JSON、雪花、压缩、反射、限流等）
- `net/actor/`：Actor 核心
- `net/cluster/`：集群消息与 RPC
- `net/discovery/`：默认 / NATS / etcd 发现后端
- `net/connector/`：TCP / WebSocket / HTTP 连接器
- `net/parser/`：协议解析器（`pomelo/`、`simple/`）
- `net/proto/`：protobuf 与集群包
- `code/`、`const/`、`error/`：错误码、常量和错误类型

---

## 上下文加载规则

- 动手写应用、组件、Actor、连接器、协议时，先读 `README.md`、`application.go`、相关 `facade/*`。
- 改网络/协议/连接器时，再读 `net/parser/`、`net/connector/`、`net/proto/`。
- 改集群/发现时，再读 `net/cluster/`、`net/discovery/`、`net/nats/`。
- 改 profile 或日志时，再读 `profile/`、`logger/`。
- 改工具函数时，优先看同目录下的现有实现风格，再动手。

---

## 规则优先级

规则冲突时：

1. 任务明确要求优先。
2. 本文件管工程纪律、工作流和上下文加载。
3. 更具体的子文档优先于本文件。
4. 架构/协议/发现/组件定义以对应源码和子文档为准。
5. 不明确时先问，不要默默猜。
