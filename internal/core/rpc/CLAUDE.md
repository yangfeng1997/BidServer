# CLAUDE.md

本文件是 `internal/core/rpc/` 的局部索引。进入 RPC 核心目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`rpc`。
- RPC 目标描述、请求 pending 管理、超时、回调投递、span 和默认全局 core。

## 主要文件

- [`types.go`](types.go)
- [`core.go`](core.go)
- [`context.go`](context.go)
- [`default.go`](default.go)
- [`compose.go`](compose.go)
- [`span.go`](span.go)
- [`core_test.go`](core_test.go)
- [`span_test.go`](span_test.go)

## 快速读法

- 查目标选择和 header 看 `types.go`。
- 查 Call / Send / OnResponse / timeout 看 `core.go`。
- 查调用上下文和 trace span 看 `context.go`、`span.go`。

## 工作规则

- pending map、timer 和回调投递的顺序会影响超时和重入，改动时必须跑测试。
- `Poster` 回调应回投主循环，不要在 timer goroutine 里直接执行业务回调。
