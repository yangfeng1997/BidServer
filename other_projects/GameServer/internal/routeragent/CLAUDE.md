# CLAUDE.md

本文件是 `other_projects/GameServer/internal/routeragent/` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码、测试或上级文档。

> **维护约定**：本文件只记录本层目录边界与导航；当本目录新增、删除、移动关键文件或子目录时，同步更新索引。不要在这里复制上级 `CLAUDE.md` 的完整内容。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [GameServer/CLAUDE.md](../../CLAUDE.md)

## 目录定位

- Go package：`routeragent`。
- 本目录索引用于快速定位本 package 的文件与下级目录，具体行为以源码和测试为准。

## 主要文件

- [`broadcast.go`](broadcast.go)
- [`broadcast_test.go`](broadcast_test.go)
- [`etcd_registry.go`](etcd_registry.go)
- [`frame.go`](frame.go)
- [`frame_test.go`](frame_test.go)
- [`keepalive.go`](keepalive.go)
- [`member_table.go`](member_table.go)
- [`metrics.go`](metrics.go)
- [`module.go`](module.go)
- [`module_test.go`](module_test.go)
- [`options.go`](options.go)
- [`peer_conn.go`](peer_conn.go)
- [`peer_mgr.go`](peer_mgr.go)
- [`peer_mgr_test.go`](peer_mgr_test.go)
- [`remote_seq.go`](remote_seq.go)
- [`resolver.go`](resolver.go)
- [`routeragentserver.go`](routeragentserver.go)
- [`rpc_wire.go`](rpc_wire.go)
- [`tcp_server.go`](tcp_server.go)
- [`uds_conn.go`](uds_conn.go)
- [`uds_server.go`](uds_server.go)

## 工作规则

- 不要把本目录规则外推到其他参考项目或兄弟目录。
- 修改代码前先读同目录测试和相邻实现，保持命名、错误处理、日志和注释风格一致。
- 生成产物、二进制、临时文件不要作为设计事实源；如必须改生成产物，先找到对应源文件或生成命令。
- 如果本索引与源码冲突，以源码和测试为准，并同步修正文档。
