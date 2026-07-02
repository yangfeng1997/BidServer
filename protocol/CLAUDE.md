# CLAUDE.md

本文件是 `protocol/` 的局部索引。进入协议目录工作时，先读本文件，再进入具体协议分组或生成目录。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 协议 proto 源文件与生成代码。
- `common/` 放协议注解、错误码和节点类型；`cs/` 放客户端协议；`handler/` 放前端 handler 服务；`remote/` 放后端 RPC remote 服务；`ra/` 放 routeragent 协议；`gen/` 放生成的路由、handler、remote 和 RPC stub。

## 子目录

- [`common/`](common/)
- [`cs/`](cs/)
- [`handler/`](handler/)
- [`ra/`](ra/)
- [`remote/`](remote/)
- [`ss/`](ss/)
- [`gen/`](gen/)

## 快速读法

- 查协议扩展选项先看 `common/options.proto`。
- 查客户端入口命令先看 `handler/` 和 `gen/routes.go`。
- 查后端 RPC 先看 `remote/` 和 `gen/rpc.go`。
- 查生成链路先看 `tools/gen_routes/` 与 `tools/protoc-gen-svcstub/`。

## 工作规则

- `.proto` 是事实源；`*.pb.go` 和 `protocol/gen/` 是生成产物。
- 改协议后要重新生成并跑测试。
- 生成产物不要手工改业务语义。
