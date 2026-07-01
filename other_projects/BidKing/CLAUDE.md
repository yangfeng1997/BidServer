# CLAUDE.md

本文件为 Claude Code 在 `other_projects/BidKing` 中工作时的总索引。目标是让你**最快**从项目根定位到服务入口、框架热点和生成链路。

> **维护约定**：凡是新增、删除、移动目录或包，修改架构设计、接口定义、服务入口、配置 schema、构建脚本、运行命令、协议生成链路、CLI / signal / daemon 语义，都必须同步更新本文件以及对应子文档。

## 最快路径

| 需求 | 直接看 |
|---|---|
| 服务入口 / 启动 | `src/servers/<svc>/main.go`、`src/framework/cli/` |
| 应用生命周期 | `src/framework/application/` |
| 网络 / 连接 / 帧 | `src/framework/network/` |
| 集群 / RPC / 路由 | `src/framework/cluster/` |
| Session / 绑定 / 回包 | `src/framework/session/` |
| 配置加载 / 热更 | `src/common/config/`、`conf/schema/`、`tools/gen_config/` |
| 日志 | `src/common/logger/` |
| 生成路由 | `tools/gen_routes/`、`protocal/` |
| 配置烘焙 / 二进制铺放 | `config.py`、`build.py`、`update_bin.py` |
| 设计 / 讨论文档 | `docs/`、`tests/` |

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| `CLAUDE.md` | 项目事实、最快路径、工程纪律、上下文加载 | 始终加载 |
| [`architecture.md`](architecture.md) | 架构概览、配置 / 应用框架层 / 消息路由 / Handler、gate 转发完整流程、业务服务补注 | 动手写框架或业务代码前 |
| [`network.md`](network.md) | Packet / Message 两层帧、握手协议、Agent、Session | 写网络、连接、协议帧、Session 相关代码前 |
| [`cluster.md`](cluster.md) | Cluster 接口、NodeID、etcd discovery、NATS transport、routerclient、TaskQueue、JetStream 匹配队列 | 写集群、RPC、跨进程、匹配队列相关代码前 |
| [`development.md`](development.md) | 常用命令、目录布局、代码放置规则、proto / config 生成步骤、serverTypeID 表 | 构建测试、新增代码、新增协议或配置时 |
| [`code_style.md`](code_style.md) | Go 命名规范，基于 Google Go Style Guide | 命名和编码风格不确定时 |

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 代码标识符遵循 Go 命名习惯，不为了中文语义破坏可读性。

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

## 目录分层

- `src/common/`：基础库，包括配置、日志、序列化、taskqueue、timewheel、mongo、matchqueue、pidfile、daemon 等。
- `src/framework/`：框架层，包括 application 生命周期、cli、agent/session、handler/pipeline、cluster、network、module、errors。
- `src/servers/`：各独立服务入口和服务内部逻辑。
- `protocal/`：业务与集群 protobuf 源文件；`protocal/gen/` 是生成产物，勿手改。
- `conf/`：配置模板、环境 values、配置 schema；`conf/schema/gen/` 是生成产物，勿手改。
- `tools/`：生成与配置烘焙工具，包括 `gen_routes`、`gen_config`、`config_build`。

## 常用命令

```bash
go build ./...
go test ./...
go test -v ./...
python -m pytest tests
go run ./tools/gen_routes
go run ./tools/config_build --env=dev --all
./config.py --env dev
./build.py --env dev
./update_bin.py --env dev
```

`build.py` 依赖 `run/{svc}/` 目录已经存在，通常先执行 `./config.py --env dev`，再执行 `./build.py --env dev`。

## 快速读法

- 先看 `src/framework/CLAUDE.md`、`src/common/CLAUDE.md`、`src/servers/CLAUDE.md` 这三个总索引。
- 想看某个框架能力时，直接跳到对应子目录的 `CLAUDE.md`，再读该目录下最核心的 1~2 个源文件。
- 不要默认扫 `src/` 全树；模块问题优先走索引文件。

## 工程纪律

- 所有外部输入都要校验：客户端消息、RPC、配置、缓存 / DB 数据、命令行参数。
- 检查对象存在性、归属、生命周期、可操作状态与状态转移合法性。
- 处理非法参数、缺失配置、脏数据、重试、重复请求、重入、并发、乱序事件。
- 发奖、扣资源、结算、落库必须幂等；不要静默重复执行副作用。
- 跨 goroutine 回调进入帧驱动服务时，优先通过 `taskqueue.Dispatcher` 回到主循环，避免共享状态竞态。
- 关键失败路径必须返回显式错误并打有用日志，日志使用 `src/common/logger` 的强类型字段。

## 规则优先级

1. 任务明确要求优先。
2. `code_style.md` 管命名；本文件管工程纪律、工作流和上下文加载；架构细节以 `architecture.md`、`network.md`、`cluster.md`、`development.md` 为准。
3. 更具体的规则优先于更宽泛的规则。
4. 仍不明确时，先问清楚，不要默默猜测。
