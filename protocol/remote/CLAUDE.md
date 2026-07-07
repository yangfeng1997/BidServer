# CLAUDE.md

本文件是 `protocol/remote/` 的局部索引。进入服务间 RPC remote 协议目录工作时，先读本文件，再看 proto 源和生成代码。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 后端服务之间的 RPC remote 定义。
- 当前只保留 `lobby_remote.proto` 下的 `RPC_Test_Req`、`RPC_Test_Rsp`、`RPC_Test_Ntf` 样例。

## 主要文件

- [`lobby_remote.proto`](lobby_remote.proto)

## 工作规则

- 修改 remote 后要重新生成 `protocol/gen/remote/` 和 `protocol/gen/rpc.go`。
- 没有对应服务实现的 remote 协议不要引入当前主链路。
- 当前只生成 `protocol/gen/remote/lobby_remote.go` 和 `protocol/gen/rpc.go` 中的 `Lobby.Test` / `Lobby.TestNtf` 样例 stub。
