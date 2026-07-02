# CLAUDE.md

本文件是 `protocol/common/` 的局部索引。进入协议公共目录工作时，先读本文件，再看 proto 源和生成代码。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 协议公共定义：错误码、节点类型和 protobuf 扩展选项。

## 主要文件

- [`errcode.proto`](errcode.proto)
- [`node.proto`](node.proto)
- [`options.proto`](options.proto)

## 工作规则

- 扩展选项变更会影响 `tools/gen_routes` 和 `tools/protoc-gen-svcstub`。
- `.pb.go` 为生成产物，优先修改 `.proto`。
