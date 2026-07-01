# CLAUDE.md

本文件是 `pkg/serialize/protobuf/` 的局部索引。进入 protobuf 序列化目录工作时，先读本文件，再看源码。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`protobuf`。
- `serialize.Serializer` 的 protobuf 实现。

## 主要文件

- [`protobuf.go`](protobuf.go)

## 快速读法

- 查构造入口看 `NewSerializer`。
- 查类型校验和编解码看 `Marshal` / `Unmarshal`。

## 工作规则

- `Marshal` / `Unmarshal` 只接受 `google.golang.org/protobuf/proto.Message`。
- 非 protobuf 消息应返回 `ErrWrongValueType`。
