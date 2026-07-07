# CLAUDE.md

本文件是 `internal/core/ragent/` 的局部索引。这里是 RouterAgent 相关的核心协议、客户端 SDK 与服务端核心实现。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- `wire/`：RouterAgent 共享 wire 协议，agent 和 sdk 共用。
- `proto/`：RouterAgent 内部 protobuf 协议定义。
- `sdk/`：业务服接入 RouterAgent 的客户端 SDK。
- `agent/`：RouterAgent 服务端核心运行时、路由、peer 转发、状态、发现和 transport。
- 根目录不保留 Go 代码；调用方直接 import `sdk/`、`wire/`、`proto/` 或 `agent/`。

## 子目录

| 目录 | 角色 | 最快入口 |
|---|---|---|
| `wire/` | RA frame / RPC wire header / routing mode | `wire/frame.go`, `wire/rpc_wire.go`, `wire/routing.go` |
| `proto/` | RouterAgent protobuf 协议 | `proto/ra.proto` |
| `sdk/` | gate/lobby 使用的 RouterAgent 客户端 | `sdk/client.go` |
| `agent/` | RouterAgent 服务端核心 | `agent/runtime.go`, `agent/router.go`, `agent/peer_forward.go` |
| `agent/discovery/` | 服务发现抽象、成员表和 resolver | `agent/discovery/registry.go` |
| `agent/discovery/etcd/` | etcd 注册发现实现 | `agent/discovery/etcd/registry.go` |

## 依赖方向

```text
sdk -> wire
agent -> wire
proto -> common generated runtime only
agent -> discovery
agent/discovery/etcd -> discovery
server/routeragent -> agent
```

规则：

- `wire` 不依赖 `sdk`、`agent` 或 `server/routeragent`。
- `sdk` 不依赖 `agent`。
- `agent` 不依赖 `sdk`。
- `agent` 核心只通过 `discovery.Registry` 抽象使用注册发现；etcd 实现放在 `agent/discovery/etcd`。
- `internal/server/routeragent` 是进程外壳，不放数据面逻辑。

## 快速读法

- 查业务服如何连接 RouterAgent：看 `sdk/client.go`。
- 查 RA wire 协议：看 `wire/frame.go` 与 `wire/rpc_wire.go`。
- 查 RA protobuf 协议：看 `proto/ra.proto`。
- 查 RouterAgent 服务端 runtime 生命周期：看 `agent/runtime.go`。
- 查路由决策：看 `agent/router.go`。
- 查跨 RA peer 转发：看 `agent/peer_forward.go`、`agent/peer_mgr.go`、`agent/peer_conn.go`。
- 查服务发现：看 `agent/discovery/registry.go` 和 `agent/discovery/etcd/registry.go`。

## 工作规则

- 新增 wire 字段或帧类型时，同时检查 `sdk/client.go` 与 `agent/router.go`。
- 新增业务服接入能力时优先放 `sdk/`，不要让 sdk import agent。
- 新增 RouterAgent 服务端能力时放 `agent/`，不要放到 `internal/server/routeragent/`。
- 新增注册中心实现时放 `agent/discovery/<impl>/`，并实现 `discovery.Registry`。
