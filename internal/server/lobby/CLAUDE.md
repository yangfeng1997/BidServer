# CLAUDE.md

本文件是 `internal/server/lobby/` 的局部索引。进入大厅服务实现目录时，先读本文件，再看 builder / config / module / internal。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 大厅服务实现。
- lobbysvr 是纯后端节点，不监听客户端端口；通过 RouterAgent UDS 连接接入集群，并通过生成的 `gen/handler` route 注册适配器分发 LobbyHandler 请求。
- 当前客户端协议只保留 Ping/Pong；`internal/` 下承载 lobby 领域逻辑设计，但不新增客户端协议入口。

## 主要文件

- [`builder.go`](builder.go)：配置加载、logger group、app builder 组装。
- [`config.go`](config.go)：配置 entry、ReloadConfig 与 hook。
- [`module.go`](module.go)：RouterAgent client、RPC dispatcher、Runtime 生命周期编排。
- [`handler.go`](handler.go)：当前唯一客户端 handler，Ping/Pong。
- [`options.go`](options.go)：启动参数。
- [`internal/`](internal/)：lobby 私有领域包。

## `internal/` 领域包

- `player.go`：Player、Component、room affinity。
- `component_bag.go` / `component_currency.go` / `component_friend.go` / `component_rating.go` / `component_mail.go`：玩家组件。
- `op_dedup.go`：op-id 去重环，用于道具/货币/离线重放幂等。
- `events.go`：进程内同步事件总线，复用 `pkg/event`。
- `store.go` / `mailbox_store.go` / `offline_store.go`：持久化接口、文档结构和 Mongo store 实现。
- `runtime.go`：单线程主循环、taskqueue、timewheel、dirty flush、登录/断线/Touch/离线消息框架。

## 当前边界

- 客户端协议相关逻辑不在本轮移植；Runtime 中对应 push 调用点只保留 TODO 注释。
- RPC 消息相关逻辑暂不支持；online、match、room、game 等跨服务调用点是可注入 hook，默认 no-op，并保留 TODO 注释。
- Store 已接入 `pkg/mongo`；Mongo URI/DB 从 `CommonConfig().Mongo` 读取。

## 快速读法

- 先看 `builder.go` 理解配置加载和 builder 组装。
- 再看 `module.go` 理解 lobby 连接 RouterAgent 的握手流程、route dispatcher 注册和 Runtime 生命周期。
- 看业务领域逻辑时，从 `internal/runtime.go` 开始，再按组件读取 `internal/component_*.go`。
- 再看 `config.go` 理解配置 entry 与 ReloadConfig。
- 再看 `options.go` 理解启动参数。

## 工作规则

- 配置先于 logger，再到 app builder。
- 改热更 / reload hook 时，要同步根文档。
- 新增客户端协议时必须同步 protocol、生成链路和本文件。
- 接入真实 RPC 或持久化实现时，先补协议/Store 边界设计，再替换 Runtime 的 TODO hook，不要直接在领域组件里依赖具体协议。
