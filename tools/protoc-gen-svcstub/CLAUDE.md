# CLAUDE.md

本文件是 `tools/protoc-gen-svcstub/` 的局部索引。进入 remote/handler stub 生成器目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`main`。
- protoc 插件，基于 `protocol/handler/*.proto` 和 `protocol/remote/*.proto` 生成 handler / remote 的 RouterAgent route 注册适配器，以及服务间 typed RPC stub 代码。

## 主要文件

- [`main.go`](main.go)
- [`main_test.go`](main_test.go)

## 快速读法

- 查 handler 生成看 `writeHandlerFile` / `writeServiceFile`。
- 查 remote 生成看 `writeRemoteFile` / `writeServiceFile`。
- 查统一 RPC stub 生成看 `writeRPCFile`。

## 工作规则

- 生成代码路径和 import 路径要与 `protocol/gen/` 目录结构一致。
- 改生成格式时必须同步测试和 `scripts/gen_proto.sh`。
