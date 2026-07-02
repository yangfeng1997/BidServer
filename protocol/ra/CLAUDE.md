# CLAUDE.md

本文件是 `protocol/ra/` 的局部索引。进入 routeragent 协议目录工作时，先读本文件，再看 proto 源。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- RouterAgent 相关 protobuf 定义。

## 主要文件

- [`ra.proto`](ra.proto)

## 工作规则

- `.pb.go` 为生成产物，优先修改 `.proto`。
- 改 RA wire 语义时要同步 `internal/server/routeragent/` 和 `internal/core/rpc/`。
