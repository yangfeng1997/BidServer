# CLAUDE.md

本文件是 `protocol/cs/` 的局部索引。进入客户端协议目录工作时，先读本文件，再看 proto 源。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 客户端到服务器的通用协议定义。

## 主要文件

- [`lobby_cs.proto`](lobby_cs.proto)

## 工作规则

- `cmd_id` 是客户端入口协议号，不能随意复用。
- `.pb.go` 为生成产物，优先修改 `.proto`。
