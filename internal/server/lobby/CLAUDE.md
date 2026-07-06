# CLAUDE.md

本文件是 `internal/server/lobby/` 的局部索引。进入大厅服务实现目录时，先读本文件，再看 builder / config / options。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 大厅服务实现。
- lobbysvr 是纯后端节点，不监听客户端端口；通过 RouterAgent UDS 连接接入集群，并通过生成的 `gen/handler` route 注册适配器分发 LobbyHandler 请求。

## 主要文件

- [`builder.go`](builder.go)
- [`config.go`](config.go)
- [`module.go`](module.go)
- [`options.go`](options.go)

## 快速读法

- 先看 `builder.go` 理解配置加载和 builder 组装。
- 再看 `module.go` 理解 lobby 连接 RouterAgent 的握手流程和 route dispatcher 注册。
- 再看 `config.go` 理解配置 entry 与 ReloadConfig。
- 再看 `options.go` 理解启动参数。

## 工作规则

- 配置先于 logger，再到 app builder。
- 改热更 / reload hook 时，要同步根文档。
