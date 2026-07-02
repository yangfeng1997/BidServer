# CLAUDE.md

本文件是 `internal/core/session/` 的局部索引。进入会话目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`session`。
- 网关连接会话、UID 绑定和 serverType 到 nodeID 的亲和绑定。

## 主要文件

- [`session.go`](session.go)
- [`session_test.go`](session_test.go)

## 快速读法

- 查会话数据结构看 `Session`。
- 查连接 / 断开 / UID 绑定看 `SessionManager`。

## 工作规则

- byConn 和 byUID 索引必须保持一致。
- 同一 UID 绑定新 session 时要清理旧 session 的认证状态和索引。
