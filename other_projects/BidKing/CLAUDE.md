# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在 `other_projects/BidKing` 中工作时提供指引。它只保留入口索引、工程纪律、上下文加载规则和当前工程事实；架构细节按需读取根目录子文档。

> **维护约定**：本文件不是一次性文档。凡是新增、删除、移动目录或包，修改架构设计、接口定义、服务入口、配置 schema、构建脚本、运行命令、协议生成链路、CLI / signal / daemon 语义，都必须同步更新本文件以及对应子文档。

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| `CLAUDE.md` | 文档索引、工程纪律、上下文加载、项目事实 | 始终加载 |
| [`architecture.md`](architecture.md) | 架构概览、配置 / 应用框架层 / 消息路由 / Handler、gate 转发完整流程、业务服务补注 | 动手写框架或业务代码前 |
| [`network.md`](network.md) | Packet / Message 两层帧、握手协议、Agent、Session | 写网络、连接、协议帧、Session 相关代码前 |
| [`cluster.md`](cluster.md) | Cluster 接口、NodeID、etcd discovery、NATS transport、routerclient、TaskQueue、JetStream 匹配队列 | 写集群、RPC、跨进程、匹配队列相关代码前 |
| [`development.md`](development.md) | 常用命令、目录布局、代码放置规则、proto / config 生成步骤、serverTypeID 表 | 构建测试、新增代码、新增协议或配置时 |
| [`code_style.md`](code_style.md) | Go 命名规范，基于 Google Go Style Guide | 命名和编码风格不确定时 |

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 代码标识符遵循 Go 命名习惯，不为了中文语义牺牲可读性。

## 项目概述

BidKing 是 Go 编写的多服务游戏服务器框架，模块路径为 `project`，Go 版本为 `1.26`。

当前服务进程位于 `src/servers/`：

| 服务 | serverTypeID | 角色 |
|---|---:|---|
| `gatesvr` | 1 | 网关，前端节点，客户端 TCP / WebSocket 接入 |
| `lobbysvr` | 2 | 大厅，玩家 EC 实体、MongoDB 持久化 |
| `onlinesvr` | 5 | 在线目录，纯内存一致性哈希分片 |
| `routersvr` | 6 | 路由代理，forward + publishmatch |
| `roomsvr` | 7 | 对局房间，Game + 单主循环 Runtime |
| `matchsvr` | 8 | 匹配服，MMR 队列 + JetStream 消费 |

核心分层：

- `src/common/`：基础库，包括配置、日志、序列化、taskqueue、timewheel、mongo、matchqueue、pidfile、daemon 等。
- `src/framework/`：框架层，包括 application 生命周期、cli、agent/session、handler/pipeline、cluster、network、module、errors。
- `src/servers/`：各独立服务入口和服务内部逻辑。
- `protocal/`：业务与集群 protobuf 源文件；`protocal/gen/` 是生成产物，勿手改。
- `conf/`：配置模板、环境 values、配置 schema；`conf/schema/gen/` 是生成产物，勿手改。
- `tools/`：生成与配置烘焙工具，包括 `gen_routes`、`gen_config`、`config_build`。

## 常用命令

```bash
# 构建与测试
go build ./...
go test ./...
go test -v ./...

# Python 工具单测
python -m pytest tests

# 生成路由表，扫描 protocal/*.proto 写入 protocal/gen/routes/routes.go
go run ./tools/gen_routes

# 烘焙配置，conf/ + conf/envs/{env}.yaml -> run/{svc}/conf/config.yaml
go run ./tools/config_build --env=dev --all
go run ./tools/config_build --env=dev --svc=gatesvr

# 项目封装脚本
./config.py --env dev      # 创建 run 目录、渲染 common + svr_list 中所有服务配置、铺已有二进制
./build.py --env dev       # 按 svr_list 编译 src/servers/{svc} 到 bin/，再复制到 run/{svc}/bin/
./update_bin.py --env dev  # 仅复制已有 bin/{svc} 到 run/{svc}/bin/
```

`build.py` 依赖 `run/{svc}/` 目录已经存在，通常先执行 `./config.py --env dev`，再执行 `./build.py --env dev`。

## 配置与生成链路

配置链路分为四层：

1. Schema：`conf/schema/options.proto`、`types.proto`、`common.proto`、`<svc>.proto` 是配置类型事实源。
2. Gen：`tools/gen_config` 根据 descriptor 生成 `conf/schema/gen/` 下的 Go struct、字段表和 reload 表。
3. Build：`tools/config_build` 把 `conf/*.yaml` 模板与 `conf/envs/{env}.yaml` values 烘焙到 `run/`。
4. Runtime：`src/common/config.Loader[T]` 以严格 yaml、必填校验、运行时环境变量注入和 atomic 快照承载配置。

约定：

- `conf/schema/gen/*` 是生成代码，禁止手动编辑。
- `protocal/gen/*` 是生成代码，禁止手动编辑。
- `run/` 和 `bin/` 是构建 / 运行产物，不应提交。
- `conf/envs/{env}.yaml` 的 `svr_list` 是服务列表事实源，`config.py`、`build.py`、`update_bin.py` 都依赖它。
- 配置模板中小写 `${value}` 由 build 期 values 替换；大写 `${VAR}` 保留给运行时环境变量注入。

## CLI 与生命周期

每个服务入口通常遵循同一模式：

1. `src/framework/cli.New(...).DefaultConf(...).OnStart(runServer).Execute()` 创建 cobra 命令。
2. `start` 子命令处理 `--daemon`、`--pid-file`，再进入业务 `runServer`。
3. `runServer` 加载 `run/common/conf/config.yaml` 和本服务配置。
4. 初始化 logger、解析 `cluster.NodeID`、构造 NATS cluster。
5. 用 `application.NewBuilder()` 设置 NodeID、NodeType、Serializer、Cluster；前端服务额外设置 Frontend、Routes。
6. 注册 module 和 handler，调用 `app.Start()`。
7. 初始化 cluster、启动配置 Watch，最后 `app.Run()` 阻塞等待信号。

CLI 子命令语义：

- `start`：启动服务；`--daemon` 通过 `src/common/daemon` 重 exec 后台化。
- `stop`：读取 pid-file 发送 `SIGTERM`，走优雅停止。
- `kill`：读取 pid-file 发送 `SIGKILL`。
- `reload`：默认发送 `SIGHUP`；也可由服务注册 `OnReload` 覆盖。
- `status`：读取 pid-file 判断进程是否仍在运行。
- `version`：输出二进制名和编译期 `GitRevision`。

`Application.Start()` 非阻塞，负责注入 cluster handler、启动前端 acceptor、执行 module `Init -> OnAfterInit`。`Application.Run()` 阻塞等待退出信号。停止时按逆序执行 module 停止逻辑，关闭 acceptor 和活动连接，并等待 agent 退出。

## 工程纪律

### 实现

- 所有外部输入都要校验：客户端消息、RPC、配置、缓存 / DB 数据、命令行参数。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理非法参数、缺失配置、脏数据、重试、重复请求、重入、并发、乱序事件。
- 发奖、扣资源、结算、落库必须幂等；不要静默重复执行副作用。
- 跨 goroutine 回调进入帧驱动服务时，优先通过 `taskqueue.Dispatcher` 回到主循环，避免共享状态竞态。
- 关键失败路径必须返回显式错误并打有用日志，日志使用 `src/common/logger` 的强类型字段。

### 清理

- 清掉自己改动产生的孤儿：未用 import、变量、函数、临时文件和生成残留。
- 既有死代码不主动删除，除非任务明确要求；发现后在结果中指出即可。

### 测试

- 测试应从设计意图和文档约定推导，不要为了迁就当前实现去改测试。
- 当文档约定与实现冲突导致测试失败时，先报告冲突点、源码位置和文档依据。
- JetStream、etcd、NATS、MongoDB 等外部依赖在本地不可用时，优先跑无需外部服务的单测和 `go build ./...` 编译验证。

## 上下文加载规则

- 按任务阶段增量加载上下文，不要一上来通读整个仓库。
- 动手写框架 / 业务代码前先读 `architecture.md` 和 `code_style.md`。
- 写网络、握手、Agent、Session、协议帧时再读 `network.md`。
- 写 cluster、RPC、服务发现、NATS、routerclient、TaskQueue、matchqueue 时再读 `cluster.md`。
- 构建测试、新增服务、新增 proto、新增配置字段时再读 `development.md`。
- 新增代码前先读最近的同类包，对齐命名、错误处理、日志和测试风格。

## 规则优先级

规则冲突时：

1. 任务明确要求优先。
2. `code_style.md` 管命名；本文件管工程纪律、工作流和上下文加载；架构细节以 `architecture.md`、`network.md`、`cluster.md`、`development.md` 为准。
3. 更具体的规则优先于更宽泛的规则。
4. 仍不明确时，先问清楚，不要默默猜测。
