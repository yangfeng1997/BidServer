# CLAUDE.md

本文件是 `pkg/serialize/` 的局部索引。进入序列化目录工作时，先读本文件，再进入具体实现。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`serialize`。
- 定义通用序列化接口，具体实现放在子目录。

## 子目录

- [`protobuf/`](protobuf/)

## 主要文件

- [`serializer.go`](serializer.go)

## 快速读法

- 查抽象接口看 `serializer.go` 的 `Marshaler`、`Unmarshaler`、`Serializer`。
- 查 protobuf 实现看 `protobuf/`。

## 工作规则

- 序列化接口要保持小而稳定，避免把具体协议实现泄漏到接口层。
