# CLAUDE.md

本文件是 `internal/server/routeragent/` 的局部索引。进入本目录工作时，先读本文件，再按需进入 `internal/core/ragent/`。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../../core/ragent/CLAUDE.md](../../core/ragent/CLAUDE.md)

## 目录定位

- 本目录是 RouterAgent 服务的**进程外壳**。
- 由服务骨架/配置生成链路关心的 `builder.go`、`config.go`、`options.go` 留在这里。
- RouterAgent 数据面核心在 `internal/core/ragent/agent/`。
- 业务服 SDK 在 `internal/core/ragent/sdk/`。
- 公共 wire 协议在 `internal/core/ragent/wire/`。

## 当前文件

- [`module.go`](module.go) — App 生命周期适配器，持有并委托 `agent.Runtime`。
- [`builder.go`](builder.go) — 服务 builder：加载配置、初始化 logger、装配 `agent.Runtime` 和 etcd registry。
- [`config.go`](config.go) — RouterAgent 服务配置入口和 reload hook。
- [`options.go`](options.go) — CLI options。
- [`CLAUDE.md`](CLAUDE.md) — 本索引。

## 主要实现位置

| 需求 | 入口 |
|---|---|
| RouterAgent 服务启动装配 | `internal/server/routeragent/builder.go` |
| RouterAgent 服务配置 / reload | `internal/server/routeragent/config.go` |
| RouterAgent App Module 适配 | `internal/server/routeragent/module.go` |
| RouterAgent Runtime / 服务端核心 | `internal/core/ragent/agent/runtime.go` |
| RouterAgent 路由决策 | `internal/core/ragent/agent/router.go` |
| 跨 RA peer 转发 | `internal/core/ragent/agent/peer_forward.go` |
| UDS / TCP / peer conn | `internal/core/ragent/agent/uds_conn.go`, `tcp_server.go`, `peer_conn.go` |
| 服务发现抽象 | `internal/core/ragent/agent/discovery/registry.go` |
| etcd 服务发现实现 | `internal/core/ragent/agent/discovery/etcd/registry.go` |
| gate/lobby 客户端 SDK | `internal/core/ragent/sdk/client.go` |
| wire 协议 | `internal/core/ragent/wire/frame.go`, `rpc_wire.go` |

## 工作规则

- 服务生成、配置加载、CLI options、logger/app 装配相关改本目录。
- 新增 RouterAgent 数据面能力时，优先修改 `internal/core/ragent/agent/`。
- 新增业务服接入 SDK 能力时，修改 `internal/core/ragent/sdk/`。
- 新增/修改 wire 协议时，修改 `internal/core/ragent/wire/`，并检查 SDK 与 agent。
- 本目录不新增数据面核心逻辑；只保留服务外壳和配置装配。
