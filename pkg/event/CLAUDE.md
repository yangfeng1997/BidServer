# CLAUDE.md

本文件是 `pkg/event/` 的局部索引。进入事件总线目录工作时，先读本文件，再看源码。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`event`。
- 进程内同步事件总线，适合在单一 goroutine（如帧驱动主循环）内使用。

## 主要文件

- [`bus.go`](bus.go)

## 快速读法

- 查订阅 / 取消订阅看 `Subscribe` / `Unsubscribe`。
- 查同步发布和 panic 隔离看 `Publish`。

## 工作规则

- 当前事件总线非协程安全，不要跨 goroutine 并发订阅、取消订阅或发布。
- 发布流程会隔离单个 handler panic，并继续调用后续 handler。
