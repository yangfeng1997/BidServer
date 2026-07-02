# CLAUDE.md

本文件是 `internal/core/conn/` 的局部索引。进入连接封装目录工作时，先读本文件，再看源码。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`conn`。
- 统一连接接口与 TCP 连接包装，负责异步发送、读包、关闭通知和收包时间戳。

## 主要文件

- [`conn.go`](conn.go)

## 快速读法

- 查统一接口看 `Connection`。
- 查 TCP 实现看 `TCPConn`、`readLoop`、`writeLoop`。

## 工作规则

- `Send` 不应阻塞主循环；发送队列满时当前策略是丢弃。
- 改 packet 读取格式时要同步 `internal/core/codec/`。
