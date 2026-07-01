# CLAUDE.md

本文件是 `internal/server/` 的局部索引。进入服务实现目录工作时，先读本文件，再进入具体服务目录。

## 上级入口

- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- 各具体服务的业务实现目录。
- 这里放服务 builder、config、options 和业务逻辑。

## 子目录

- [`gate/`](gate/)
- [`lobby/`](lobby/)
- [`routeragent/`](routeragent/)

## 快速读法

- 网关相关先看 `gate/`。
- 大厅相关先看 `lobby/`。
- 路由代理相关先看 `routeragent/`。
- 改服务启动时，要同步 `cmd/<svc>/`。
