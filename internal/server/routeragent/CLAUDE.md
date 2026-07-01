# CLAUDE.md

本文件是 `internal/server/routeragent/` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码或上级文档。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- `routeragent` 服务实现目录。
- 这里放服务 builder、config、options、UDS/TCP 路由代理、peer 管理、路由选择和广播逻辑。

## 主要文件

- [`builder.go`](builder.go)
- [`config.go`](config.go)
- [`options.go`](options.go)
- [`module.go`](module.go)
- [`etcd_registry.go`](etcd_registry.go)
- [`frame.go`](frame.go)
- [`rpc_wire.go`](rpc_wire.go)
- [`uds_conn.go`](uds_conn.go)
- [`uds_server.go`](uds_server.go)
- [`tcp_server.go`](tcp_server.go)
- [`peer_conn.go`](peer_conn.go)
- [`peer_mgr.go`](peer_mgr.go)
- [`member_table.go`](member_table.go)
- [`resolver.go`](resolver.go)
- [`remote_seq.go`](remote_seq.go)
- [`broadcast.go`](broadcast.go)
- [`keepalive.go`](keepalive.go)
- [`metrics.go`](metrics.go)

## 测试文件

- [`frame_test.go`](frame_test.go)
- [`module_test.go`](module_test.go)
- [`broadcast_test.go`](broadcast_test.go)
- [`peer_mgr_test.go`](peer_mgr_test.go)

## 快速读法

- 查启动装配先看 `builder.go`，它会注册 `NewModule()`。
- 查服务配置入口先看 `config.go` 和 `module.go` 的 `ApplyConfig`。
- 查 UDS 本地进程接入先看 `uds_server.go`、`uds_conn.go` 和 `module.go` 的 `handleFrame`。
- 查跨 routeragent 转发先看 `peer_mgr.go`、`peer_conn.go` 和 `tcp_server.go`。
- 查 etcd 注册 / 发现先看 `etcd_registry.go`。
- 查路由协议先看 `frame.go`、`rpc_wire.go`、`resolver.go`。
