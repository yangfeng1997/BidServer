# CLAUDE.md

本文件是 `internal/core/options/` 的局部索引。进入核心选项目录工作时，先读本文件。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 启动参数、pid-file、daemon、pprof 与公共选项结构。

## 主要文件

- [`options.go`](options.go)

## 快速读法

- 直接看 `BaseOptions` 的字段含义。
- 改命令行参数时，要回看 `cmd/` 和 `internal/server/*` 的入口代码。

## 工作规则

- 这里的字段会影响服务入口行为，变更后要同步入口文档。
