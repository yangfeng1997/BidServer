# CLAUDE.md

本文件是 `internal/core/logger/` 的局部索引。进入日志核心目录工作时，先读本文件，再看具体实现文件。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 框架核心日志构建与全局日志实例管理。

## 主要文件

- [`field.go`](field.go)
- [`logger.go`](logger.go)

## 快速读法

- 先看 `logger.go` 了解全局 logger、LoggerGroup 和文件 logger 构建。
- 再看 `field.go` 了解字段封装和日志输出字段风格。

## 工作规则

- 日志级别、格式、daemon 场景行为变更时，要同步 `pkg/logger/`。
- 输出字段要保持稳定、可读。
