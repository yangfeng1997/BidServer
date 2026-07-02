# CLAUDE.md

本文件是 `tools/protoc-gen-svcstub/` 的局部索引。进入 service/handler stub 生成器目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`main`。
- protoc 插件，基于 `protocol/handler/*.proto` 和 `protocol/service/*.proto` 生成 handler、service、RPC stub 代码。

## 主要文件

- [`main.go`](main.go)
- [`main_test.go`](main_test.go)

## 快速读法

- 查 handler 生成看 `genHandlerFile`。
- 查 service 生成看 `genServiceFile`。
- 查统一 RPC stub 生成看 `genRPCFile`。

## 工作规则

- 生成代码路径和 import 路径要与 `protocol/gen/` 目录结构一致。
- 改生成格式时必须同步测试和 `tools/gen_proto.sh`。
