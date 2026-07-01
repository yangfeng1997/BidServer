# CLAUDE.md

本文件是 `internal/core/nodeid/` 的局部索引。进入节点 ID 目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 节点 ID 编解码工具。
- `uint32` 编码布局为 `world.serverType.index`，其中 world 16 bit、serverType 8 bit、index 8 bit。
- 点分格式仅用于日志、调试和文本解析。

## 主要文件

- [`nodeid.go`](nodeid.go)
- [`nodeid_test.go`](nodeid_test.go)

## 快速读法

- 查编码规则直接看 `nodeid.go` 的 `Encode` / `Decode`。
- 查文本格式直接看 `String` / `Parse`。

## 工作规则

- 编码布局会影响路由与服务发现语义，变更时要同步使用方和文档。
