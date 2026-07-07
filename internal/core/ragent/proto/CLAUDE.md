# CLAUDE.md

本文件是 `internal/core/ragent/proto/` 的局部索引。进入 RouterAgent protobuf 协议目录工作时，先读本文件，再看 proto 源。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../../CLAUDE.md](../../../CLAUDE.md)

## 目录定位

- RouterAgent 内部 protobuf 协议定义。
- 当前只保留 RouterAgent 握手样例消息，wire 帧编码仍以 `internal/core/ragent/wire/` 为主。

## 主要文件

- [`ra.proto`](ra.proto)

## 工作规则

- 改 proto 后运行 `scripts/gen_proto.sh` 重新生成 `*.pb.go`。
- 这里属于 `internal/core/ragent` 核心协议，不放客户端业务协议或服务间 RPC 协议。
