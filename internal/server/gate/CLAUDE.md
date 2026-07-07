# CLAUDE.md

本文件是 `internal/server/gate/` 的局部索引。进入网关服务实现目录时，先读本文件，再看 builder / config / options。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 网关服务实现。
- 这里负责加载网关和公共配置、初始化 logger group、装配 BaseBuilder。
- `module.go` 负责客户端 TCP / WebSocket 接入、Session 管理、客户端上行 ping/pong 转发到 RouterAgent。
- `ragent_client.go` 负责连接本机 RouterAgent UDS，并实现 `internal/core/rpc.Transport`。

## 主要文件

- [`builder.go`](builder.go)
- [`module.go`](module.go)
- [`ragent_client.go`](ragent_client.go)
- [`config.go`](config.go)
- [`options.go`](options.go)

## 快速读法

- 先看 `builder.go` 理解配置加载和 builder 组装。
- 查 gate 运行链路看 `module.go`：acceptor、dispatcher、Session 和 RouterAgent 转发都在这里装配。
- 查 gate 与 RouterAgent 的 UDS/RPC 帧适配看 `ragent_client.go`。
- 再看 `config.go` 理解配置 entry 与 ReloadConfig。
- 再看 `options.go` 理解启动参数。

## 工作规则

- 配置先于 logger，再到 app builder。
- 改热更 / reload hook 时，要同步根文档。
