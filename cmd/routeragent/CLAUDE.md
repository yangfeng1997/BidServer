# CLAUDE.md

本文件是 `cmd/routeragent/` 的局部索引。进入本目录工作时，先读本文件，再看入口 main.go。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- `routeragent` 服务入口目录。
- 这里只放 flags 解析、Builder 组装和启动流程，不放业务逻辑。

## 主要文件

- [`main.go`](main.go)

## 快速读法

- 先看 `main.go` ，再看对应的 `internal/server/routeragent/`。
- 改启动参数时，要同步 `internal/server/routeragent/` 的配置和 options。
