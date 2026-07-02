# CLAUDE.md

本文件是 `protocol/remote/` 的局部索引。进入服务间 RPC remote 协议目录工作时，先读本文件，再看 proto 源和生成代码。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 后端服务之间的 RPC remote 定义。
- 当前仅保留已有服务相关协议：gate、lobby。

## 主要文件

- [`gate_remote.proto`](gate_remote.proto)
- [`lobby_remote.proto`](lobby_remote.proto)

## 工作规则

- 修改 remote 后要重新生成 `protocol/gen/remote/` 和 `protocol/gen/rpc.go`。
- 没有对应服务实现的 remote 协议不要引入当前主链路。
