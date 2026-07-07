# CLAUDE.md

本文件是 `internal/server/online/` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码或上级文档。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- `online` 服务实现目录。
- 这里放服务 builder、config、options 和业务逻辑。

## 主要文件

- [`builder.go`](builder.go)
- [`config.go`](config.go)
- [`options.go`](options.go)
- [`module.go`](module.go)
- [`internal/directory.go`](internal/directory.go)

## 快速读法

- 查启动装配先看 `builder.go` 和 `module.go`。
- 查配置入口先看 `config.go`。
- 查启动参数先看 `options.go`。
- 查在线目录、TTL 过期、顶号和 room 绑定逻辑看 `internal/directory.go`。

## 当前边界

- `module.go` 只建立 RouterAgent 连接和在线目录生命周期。
- 踢旧 gateway、online RPC handler、room 绑定 RPC 等协议接入尚未接入；对应调用点后续随服务协议补齐。
