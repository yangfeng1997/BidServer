# CLAUDE.md

本文件是 `pkg/logger/` 的局部索引。进入公共日志包工作时，先读本文件，再看具体实现文件。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- 可复用的 zap 日志封装。
- 这里提供 logger 接口、字段封装、旋转、全局 logger 和适配器。

## 主要文件

- [`core.go`](core.go)
- [`field.go`](field.go)
- [`global.go`](global.go)
- [`logger.go`](logger.go)
- [`rotate.go`](rotate.go)
- [`sugared.go`](sugared.go)
- [`zap_adapter.go`](zap_adapter.go)
- [`example_test.go`](example_test.go)
- [`example_run_test.go`](example_run_test.go)

## 快速读法

- 先看 `logger.go` 和 `global.go` 理解对外 API。
- 再看 `core.go`、`zap_adapter.go` 理解底层实现。
- 日志字段与输出风格看 `field.go`。

## 工作规则

- 这里是公共库，接口改动会影响整个仓库。
- 日志字段、格式、旋转行为改动时要同步调用方。
