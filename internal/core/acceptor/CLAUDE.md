# CLAUDE.md

本文件是 `internal/core/acceptor/` 的局部索引。进入连接接入器目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`acceptor`。
- TCP / WebSocket 连接接入器，输出统一的 `conn.Connection`。

## 主要文件

- [`acceptor.go`](acceptor.go)
- [`ws.go`](ws.go)
- [`ws_test.go`](ws_test.go)

## 快速读法

- 查 TCP 接入看 `acceptor.go`。
- 查 WebSocket 接入看 `ws.go`。

## 工作规则

- 接入器只负责监听和包装连接，不放业务分发逻辑。
- 改连接协议时要同步 `internal/core/conn/` 和 `internal/core/codec/`。
