# CLAUDE.md

本文件是 `internal/core/errcode/` 的局部索引。进入错误码目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`errcode`。
- 框架统一错误码和带错误码的 error 封装。

## 主要文件

- [`errcode.go`](errcode.go)
- [`errcode_test.go`](errcode_test.go)

## 快速读法

- 查错误码定义看 `ErrCode` 常量。
- 查 error 到错误码映射看 `CodeOf`。

## 工作规则

- 错误码数值是跨协议约定，新增或修改要同步协议层和调用方。
