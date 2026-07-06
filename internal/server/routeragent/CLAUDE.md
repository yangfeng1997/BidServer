# CLAUDE.md

本文件是 `internal/server/routeragent/` 的局部索引。进入本目录工作时，先读本文件，再按需读取相邻源码或上级文档。

## 上级入口

- [../CLAUDE.md](../CLAUDE.md)
- [../../CLAUDE.md](../../CLAUDE.md)

## 目录定位

- `routeragent` 服务实现目录。
- 按**分层 + 组件**组织：Module 负责生命周期编排，下属 transport / routing / discovery / state / observability 层组件。

## 分层与主要文件

### 生命周期与装配

- [`builder.go`](builder.go) — builder，注册 `NewModule()`。
- [`module.go`](module.go) — `Module` 类型：字段为各组件、`NewModule`、`Init`、`AfterInit`、`BeforeShutdown`、`ApplyConfig`、UDS 连接生命周期 `handleConn` / `handleFrame` / `handleBroadcastSent`。
- [`config.go`](config.go) — config entry。
- [`options.go`](options.go) — CLI options。
- [`listen_addr.go`](listen_addr.go) — listen addr helper。

### transport 层

- [`uds_server.go`](uds_server.go) — 本机业务进程 UDS 监听。
- [`uds_conn.go`](uds_conn.go) — UDS 连接。
- [`tcp_server.go`](tcp_server.go) — peer TCP 监听。
- [`peer_conn.go`](peer_conn.go) — `tcpPeerLink` 实现、`NewTCPPeerLink` 测试适配。

### routing 层

- [`router.go`](router.go) — `Router` 组件：`RouteFrame` / `forwardRPC` / `pickTargets` / `routeLegacyFrame` / `HandlePeerFrame` / `SendToNode`。
- [`resolver.go`](resolver.go) — 节点选择。
- [`member_table.go`](member_table.go) — nodeID / serverType 成员表。
- [`frame.go`](frame.go) — `Frame` / `FrameType`。
- [`rpc_wire.go`](rpc_wire.go) — `RPCWireHeader`。

### peer 转发层（跨 routing 和 transport）

- [`peer_forward.go`](peer_forward.go) — `PeerForwarder` 组件：`SendOrQueue` / `SendResponseViaPeer` / `HandleIncomingPeer` / `Dial` / `FlushPending` / `FailOutbound`。
- [`peer_mgr.go`](peer_mgr.go) — `PeerMgr`：peer 连接状态和 pending 队列。

### discovery 层

- [`registry.go`](registry.go) — `Registry` 接口。
- [`etcd_registry.go`](etcd_registry.go) — etcd 实现。
- [`keepalive.go`](keepalive.go) — lease 保活。

### state 层

- [`local_conn_set.go`](local_conn_set.go) — `LocalConnSet`：本机 UDS 连接注册表。
- [`remote_seq.go`](remote_seq.go) — `RemoteSeqMap`：跨 RA seq 映射。
- [`incoming_peer_seq.go`](incoming_peer_seq.go) — `IncomingPeerSeqStore`：入站 peer seq 跟踪。
- [`broadcast.go`](broadcast.go) — `BroadcastWaiter` + 广播编解码。

### observability 层

- [`metrics.go`](metrics.go) — `Metrics` 指标。
- [`queue_stats.go`](queue_stats.go) — `MetricsSnapshot` / queue stats 日志。

### 测试导出

- [`exports.go`](exports.go) — `NewModuleForTest` / `RegisterConn` / `RouteFrame` / `SetListenAddr` 等集成测试导出。

## 测试文件

- [`frame_test.go`](frame_test.go)
- [`module_test.go`](module_test.go)
- [`broadcast_test.go`](broadcast_test.go)
- [`peer_mgr_test.go`](peer_mgr_test.go)

## 快速读法

- 查启动装配先看 `builder.go`，再看 `module.go` 的 `Init` / `AfterInit`。
- 查 UDS 本地进程接入先看 `module.go` 的 `handleConn` / `handleFrame`，再看 `local_conn_set.go`。
- 查路由决策先看 `router.go` 的 `RouteFrame` / `forwardRPC` / `pickTargets`。
- 查跨 RA 转发先看 `peer_forward.go`、`peer_mgr.go`、`peer_conn.go`。
- 查 etcd 注册 / 发现先看 `registry.go` 接口和 `etcd_registry.go` 实现。
- 查路由协议先看 `frame.go`、`rpc_wire.go`、`resolver.go`。
- 查观测指标先看 `metrics.go`、`queue_stats.go`。

## 工作规则

- `Module` 只做生命周期编排和组件装配，路由/转发/状态逻辑下沉到对应组件。
- 新增 peer 连接能力改 `peer_forward.go` / `peer_mgr.go`，不改 `module.go`。
- 新增路由模式改 `router.go` 的 `pickTargets`，不改 `module.go`。
- `Registry` 是接口，etcd 是默认实现；替换注册中心时实现 `Registry` 即可。
