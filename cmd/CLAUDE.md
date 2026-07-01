# CLAUDE.md

本文件是 `cmd/` 的局部索引。进入 `cmd/` 目录工作时，先读本文件，再进入具体服务入口目录。

> **维护约定**：本文件只记录顶层命令入口与导航；当新增、删除、移动服务入口时，同步更新索引。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)

## 目录定位

- Go 服务入口目录。
- 这里的文件通常只负责解析 flags、组装 Builder、启动应用。

## 子目录

- [`gatesvr/`](gatesvr/)
- [`lobbysvr/`](lobbysvr/)
- [`routeragent/`](routeragent/)

## 主要文件

- [`gatesvr/main.go`](gatesvr/main.go)
- [`lobbysvr/main.go`](lobbysvr/main.go)
- [`routeragent/main.go`](routeragent/main.go)

## 快速读法

- 查网关启动先看 `cmd/gatesvr/main.go`，再看 `internal/server/gate/`。
- 查大厅启动先看 `cmd/lobbysvr/main.go`，再看 `internal/server/lobby/`。
- 查路由代理启动先看 `cmd/routeragent/main.go`，再看 `internal/server/routeragent/`。

## 工作规则

- 先读对应服务目录的 `CLAUDE.md`，再看入口 `main.go`。
- 命令参数、pid-file、daemon、pprof 语义改动时要同步服务文档。
- 不要在 `cmd/` 放业务逻辑。
