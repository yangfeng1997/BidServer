# CLAUDE.md

本文件是 `pkg/timewheel/` 的局部索引。进入时间轮目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`timewheel`。
- 多层哈希时间轮，支持一次性任务、周期任务、手动 `Advance` 和自驱动 `Start`。

## 主要文件

- [`timewheel.go`](timewheel.go)
- [`timewheel_test.go`](timewheel_test.go)

## 快速读法

- 查构造和参数默认值看 `New` / `NewWithLevelCount`。
- 查一次性和周期任务看 `AfterFunc` / `Tick`。
- 查主循环驱动看 `Advance`。
- 查自驱动模式看 `Start` / `Close`。

## 工作规则

- 定时任务回调在调用 `Advance` 的 goroutine 或内部自驱动 goroutine 中执行。
- 修改级联、重排或取消语义时必须同步测试。
