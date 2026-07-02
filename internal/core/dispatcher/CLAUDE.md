# CLAUDE.md

本文件是 `internal/core/dispatcher/` 的局部索引。进入消息分发目录工作时，先读本文件，再看源码和测试。

## 上级入口

- [../../../CLAUDE.md](../../../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)
- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go package：`dispatcher`。
- 业务消息路由、handler 注册、中间件链和跨服务转发入口。

## 主要文件

- [`dispatcher.go`](dispatcher.go)
- [`gate.go`](gate.go)
- [`middleware.go`](middleware.go)
- [`dispatcher_test.go`](dispatcher_test.go)

## 快速读法

- 查路由表和 handler 分发看 `dispatcher.go`。
- 查网关侧转发辅助看 `gate.go`。
- 查中间件组合看 `middleware.go`。

## 工作规则

- 未命中路由、未注册 handler 和转发缺失必须返回明确错误码。
- 变更 `RouteEntry` 字段要同步协议生成工具和生成代码。
