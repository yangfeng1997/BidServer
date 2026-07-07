# CLAUDE.md

本文件是 `protocol/` 的局部索引。进入协议目录工作时，先读本文件，再进入具体协议分组或生成目录。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 协议 proto 源文件与生成代码。
- `common/` 放协议注解、错误码和节点类型；`handler/` 放前端 handler 服务；`remote/` 放后端 RPC remote 服务；`gen/` 放生成的路由、handler、remote 和 RPC stub。
- RouterAgent 内部 protobuf 协议放在 `internal/core/ragent/proto/`。
- 当前客户端入口只保留 `LobbyHandler/Ping` -> `SC_Pong_Rsp`。
- 当前 RPC 只保留 `protocol/remote/lobby_remote.proto` 下的 `RPC_Test_Req`、`RPC_Test_Rsp`、`RPC_Test_Ntf` 样例。

## 子目录

- [`common/`](common/)
- [`cs/`](cs/)
- [`handler/`](handler/)
- [`remote/`](remote/)
- [`ss/`](ss/)
- [`gen/`](gen/)

## 快速读法

- 查协议扩展选项先看 `common/options.proto`。
- 查客户端入口命令先看 `handler/`、`gen/routes.go` 和 `gen/handler/`。
- 查后端 RPC 先看 `remote/lobby_remote.proto`、`gen/remote/lobby_remote.go` 和 `gen/rpc.go`。
- 查 RouterAgent route 自动分发先看 `internal/core/rpc.Dispatcher` 与 `gen/handler/`、`gen/remote/` 的 Register 函数。
- 查生成链路先看 `tools/gen_routes/` 与 `tools/protoc-gen-svcstub/`。

## 工作规则

- `.proto` 是事实源；`*.pb.go` 和 `protocol/gen/` 是生成产物。
- 改协议后要重新生成并跑测试。
- 生成产物不要手工改业务语义。
