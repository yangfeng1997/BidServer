# CLAUDE.md

本文件是 `pkg/taskqueue/` 的局部索引。进入任务队列目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`taskqueue`。
- 可复用的跨 goroutine 任务队列，适合单主循环消费场景。
- 当前实现直接使用 `chan func()` 存储任务并暴露给主循环 `select` 消费。
- 队列满时阻塞生产者形成背压，并用限频日志记录满队列命中。

## 主要文件

- [`taskqueue.go`](taskqueue.go)
- [`taskqueue_test.go`](taskqueue_test.go)

## 快速读法

- 查生产者投递语义看 `Post` / `Enqueue`。
- 查主循环消费语义看 `C`。
- 查批量消费看 `Flush`。
- 查容量和积压指标看 `Len` / `Cap`。

## 工作规则

- `Post` 是跨 goroutine 入口，必须保持并发安全。
- `C` 暴露底层任务 channel，主循环可直接 `select` 出 `func()` 执行。
- `Flush` 在调用方 goroutine 中执行任务，不要在内部新开 goroutine。
- 队列满时当前策略是阻塞生产者形成背压；改变该策略要同步使用方。
