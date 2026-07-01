# CLAUDE.md

本文件为 Claude Code 在 `other_projects/GameServer` 中工作时的总索引。目标是让你最快从项目根定位到服务入口、框架核心和生成链路。

> **维护约定**：凡是新增、删除、移动目录或包，修改框架设计、接口定义、服务入口、配置结构、构建命令、协议生成链路、日志 / 配置 / 路由 / 热更语义，都要同步更新本文件以及对应文档。

## 最快路径

| 需求 | 直接看 |
|---|---|
| 服务入口 | `cmd/<svc>/main.go` |
| 命令装配 | `internal/core/cmd/` |
| 应用生命周期 | `internal/core/app/`、`internal/core/runtime/` |
| 配置加载 / 热更 | `internal/core/config/`、`conf/schema/` |
| 日志 | `internal/core/log/`、`pkg/logger/` |
| 路由代理 / RPC | `internal/core/ragent/`、`internal/core/rpc/` |
| 网络 / 帧 / 协议 | `internal/core/acceptor/`、`internal/core/codec/`、`internal/core/conn/` |
| 会话 | `internal/core/session/` |
| 业务服务 | `internal/gatesvr/`、`internal/lobbysvr/`、`internal/roomsvr/`、`internal/matchsvr/`、`internal/onlinesvr/`、`internal/routeragent/` |
| 生成链路 | `protocol/`、`tools/` |
| 构建 / 烘焙 | `scripts/` |
| 设计文档 | `docs/superpowers/specs/` |

## 文档索引

| 文档 | 内容 | 何时读 |
|---|---|---|
| `CLAUDE.md` | 文档索引、工程纪律、上下文加载规则、项目事实 | 始终加载 |
| [`README.md`](README.md) | 项目简介、目录概览、快速入门 | 先做整体了解时 |
| [`BUILDING.md`](BUILDING.md) | 构建、生成、服务入口、配置布局 | 动手跑命令前 |
| [`docs/superpowers/specs/framework-design.md`](docs/superpowers/specs/framework-design.md) | 框架设计总文档，当前实现的主事实来源 | 写框架 / 核心代码前 |
| [`docs/superpowers/specs/config-system-reference.md`](docs/superpowers/specs/config-system-reference.md) | 配置系统参考 | 改配置链路前 |
| [`docs/superpowers/specs/2026-06-21-config-system-refactor.md`](docs/superpowers/specs/2026-06-21-config-system-refactor.md) | 配置重构方案与约束 | 改配置模块前 |
| [`FINAL_STATUS.md`](FINAL_STATUS.md) | 当前仓库完成态摘要 | 需要快速确认仓库状态时 |

## 输出语言

- 对话回复、生成文档、代码注释、日志消息、错误消息默认使用简体中文。
- 标识符命名遵循 Go 习惯，不为了中文语义破坏可读性。
- 文档里尽量直接指向真实路径和真实符号，不用空泛描述替代实现事实。

## 项目概述

GameServer 是一个 Go 游戏服务器框架骨架，模块路径为 `project`，Go 版本为 `1.26`。

当前仓库的核心入口与分层如下：

| 分层 | 目录 | 职责 |
|---|---|---|
| 服务入口 | `cmd/<svc>/main.go` | 各服务薄入口，只做命令装配与启动 |
| 框架核心 | `internal/core/` | App 生命周期、配置、日志、路由代理、连接、协议、会话、RPC、节点 ID、错误码 |
| 业务服务 | `internal/<svc>/` | gatesvr / lobbysvr / roomsvr / matchsvr / onlinesvr / routeragent 的业务实现 |
| 通用库 | `pkg/` | 可复用基础库：configgen、logger、serialize、taskqueue、timewheel、event |
| 协议定义 | `protocol/` | protobuf 源文件与生成代码 |
| 配置源 | `conf/` | 配置模板、schema、values、secrets |
| 开发工具 | `tools/`、`scripts/` | proto 生成、配置烘焙、构建编排 |
| 文档 | `docs/` | 设计文档、计划、参考说明 |

## 最快读法

- 查服务入口先看 `cmd/<svc>/main.go` 和对应 `internal/<svc>/`。
- 查框架能力先看 `internal/core/<module>/` 的总索引，再读最核心的 1~2 个源文件。
- 改配置链路先看 `internal/core/config/`、`pkg/configgen/` 和 `conf/schema/`。
- 改网络 / RPC 先看 `internal/core/acceptor/`、`internal/core/codec/`、`internal/core/conn/`、`internal/core/rpc/`。

## 常用命令

```bash
make build
make test
make quick
make ci
make vet
make fmt
make gen-config
make gen-routes
make gen-proto
make config ENV=dev
make clean
make run-clean
```

```bash
python3 scripts/config.py --env=dev
python3 scripts/build.py
bash tools/gen_proto.sh
```

`make build` 依赖 `run/ENV`，所以通常先执行 `make config ENV=dev`。

## 配置与生成链路

- `conf/schema/*.proto` 是配置 schema 的事实来源。
- `conf/common/common.yaml` 是公共配置模板。
- `conf/servers/<svc>/<svc>.yaml` 与 `<svc>_log.yaml` 是各服务配置模板。
- `conf/values/{dev,prod,test}.yaml` 提供环境值，`svr_list` 是服务列表事实源。
- `tools/gen_config` 生成 `conf/schema/gen/` 下的 Go 结构体、校验和 reload 相关代码。
- `tools/gen_routes` 扫描 `protocol/handler/*.proto` 生成路由表到 `protocol/gen/routes.go`。
- `tools/protoc-gen-svcstub` 生成 handler / service stub 到 `protocol/gen/`。
- `scripts/config.py` 按环境创建 `run/` 目录结构、烘焙配置，并写入 `run/ENV`。
- `scripts/build.py` 读取 `run/ENV`，编译 `cmd/<svc>`，再复制到 `run/<svc>/bin/`。

## 快速路径优先级

1. 先看本文件的“最快路径”。
2. 再看对应模块的子目录 `CLAUDE.md`。
3. 最后才读具体源文件和测试。

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
4. 设计与协议事实以 `docs/superpowers/specs/*` 和源码为准。
5. 不明确时先问，不要默默猜。
