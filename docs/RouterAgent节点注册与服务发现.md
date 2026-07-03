# RouterAgent 节点注册与服务发现

本文档记录 BidServer 中 RouterAgent、业务节点与 etcd 服务发现之间的职责划分。本文以当前 sidecar 架构为基础：业务进程只连接本机 RouterAgent，跨机器通信由 RouterAgent 之间转发。

## 结论

采用 **RouterAgent 代业务节点注册到 etcd** 的方案。当前实现先以 `world_id` 隔离服务发现；`cluster.name` 和 `cluster.env` 已进入公共配置，后续可直接扩展成环境 + world 双重隔离。

- RouterAgent 自己注册到 etcd。
- 业务进程不直接连接 etcd。
- 业务进程通过 UDS 与本机 RouterAgent 握手，握手 body 携带 4 字节 `uint32 nodeID`。
- RouterAgent 收到业务握手后，把业务节点注册到 etcd。
- etcd 中业务节点的 `ra_addr` 是所属 RouterAgent 的 TCP 地址，不是业务进程地址，也不是 UDS 地址。
- 所有 RouterAgent watch 同一个节点前缀，维护本地 `MemberTable`。

该方案让服务发现责任集中在 RouterAgent，业务进程只关心业务 RPC，不需要实现 etcd client、lease、watch、重连和注销逻辑。

## NodeID 表示

代码内部、握手和 RPC 传输使用 `uint32` NodeID。

编码规则见 `internal/core/nodeid/nodeid.go`：

```text
uint32 NodeID = worldID(16 bit) | serverType(8 bit) | serverIndex(8 bit)
```

可读格式只用于配置、脚本、日志和 etcd 可观测数据：

```text
world.serverType.index
```

例如：

```text
99.6.0  routeragent
99.1.0  gatesvr
99.2.0  lobbysvr
```

启动脚本传入可读格式：

```bash
./routeragent --nodeid 99.6.0 ...
./gatesvr     --nodeid 99.1.0 ...
./lobbysvr    --nodeid 99.2.0 ...
```

进程启动后，App 保存原始字符串；需要进入协议、握手、路由或 etcd 内存索引时，通过 `nodeid.Parse` 转成 `uint32`。

## Etcd Key 设计

节点注册统一使用一张节点表。当前按 world 隔离，路径结构为：

```text
/{cluster_name}/{cluster_env}/worlds/{world_id}/nodes/{node_id}
```

`{node_id}` 使用可读字符串格式。

示例：

```text
/bidserver/dev/worlds/99/nodes/99.6.0
/bidserver/dev/worlds/99/nodes/99.1.0
/bidserver/dev/worlds/99/nodes/99.2.0
```

当前测试期主要依赖 `world_id` 隔离；`cluster_env` 仍写入路径，方便后续同 world 多环境共用 etcd 时继续隔离。

不建议把 RouterAgent 和业务节点拆成两张互不关联的表。跨 RouterAgent 路由的核心查询是：

```text
目标业务 nodeID -> 所属 RouterAgent 的 ra_addr
```

统一节点表可以直接满足这个查询。

## Etcd Value 设计

### RouterAgent 节点

RouterAgent 启动成功后注册自己：

```json
{
  "node_id": "99.6.0",
  "server_type": 6,
  "ra_addr": "10.0.0.12:7100",
  "start_at": 1780000000
}
```

字段含义：

| 字段 | 含义 |
|---|---|
| `node_id` | 可读 NodeID，等价于 `nodeid.String(uint32NodeID)` |
| `server_type` | 从 NodeID 解码得到的服务类型，RouterAgent 为 `6` |
| `ra_addr` | 本 RouterAgent 的 TCP 监听地址，必须能被其他 RouterAgent 访问 |
| `start_at` | 注册时间，Unix 秒 |

### 业务节点

业务进程和本机 RouterAgent 握手成功后，由 RouterAgent 代注册业务节点：

```json
{
  "node_id": "99.1.0",
  "server_type": 1,
  "ra_addr": "10.0.0.12:7100",
  "start_at": 1780000001
}
```

业务节点的 `ra_addr` 仍然是所属 RouterAgent 的 TCP 地址。

这意味着远端 RouterAgent 查到 `99.1.0` 后，不会尝试连接业务进程，而是连接 `10.0.0.12:7100` 这个 RouterAgent，再由该 RouterAgent 投递到本机 UDS 业务连接。

## 注册流程

### RouterAgent 启动

1. 加载 common 和 routeragent 配置。
2. 从 `--nodeid` 得到自身 NodeID。
3. 启动 UDS listener。
4. 启动 TCP listener。
5. 创建 etcd client。
6. 创建 lease。
7. 注册自身节点 key。
8. 启动 keepalive。
9. 全量加载 `/routeragent/nodes/`。
10. watch `/routeragent/nodes/` 后续变化。

### 业务进程启动

1. 业务进程从 `--nodeid` 得到自身 NodeID。
2. 业务进程连接本机 RouterAgent 的 UDS socket。
3. 业务进程发送 Handshake frame，body 是 4 字节 big-endian `uint32 nodeID`。
4. RouterAgent 解码 NodeID，得到 `worldID/serverType/index`。
5. RouterAgent 注册本地 UDS 连接：`nodeID -> UDSConn`。
6. RouterAgent 用自己的 `ra_addr` 代业务节点写入 etcd。
7. RouterAgent 返回 HandshakeAck。

### 业务进程断开

1. RouterAgent 的 UDS 读循环退出。
2. RouterAgent 从本地连接表删除 `nodeID -> UDSConn`。
3. RouterAgent 从本地 `MemberTable` 删除该业务节点。
4. RouterAgent 主动删除 etcd 中该业务节点 key。

### RouterAgent 崩溃

RouterAgent 维护的 lease 过期后，etcd 自动删除该 lease 下的所有 key：

```text
/routeragent/nodes/99.6.0
/routeragent/nodes/99.1.0
/routeragent/nodes/99.2.0
```

这可以避免 RouterAgent 崩溃后残留它代管的业务节点。

## Lease 语义

推荐一个 RouterAgent 使用一个 lease，挂载自身和代管业务节点：

```text
RA lease
  ├── /routeragent/nodes/99.6.0
  ├── /routeragent/nodes/99.1.0
  └── /routeragent/nodes/99.2.0
```

这样有两个优点：

- RouterAgent 崩溃时，所有挂在它后面的业务节点随 lease 自动消失。
- 业务进程断开但 RouterAgent 仍存活时，RouterAgent 可以主动删除单个业务节点 key。

业务进程不应该单独维护 etcd lease。否则每个业务进程都要持有 etcd 客户端和 keepalive 逻辑，会把服务发现复杂度扩散到所有服务。

## MemberTable 语义

RouterAgent 本地维护 `MemberTable`，它是 etcd `/routeragent/nodes/` 的本地缓存，同时也保存本机握手节点的快速索引。

建议索引：

```text
byNodeID:
  uint32 nodeID -> NodeInfo

byServerType:
  serverType -> []NodeInfo
```

`NodeInfo` 至少包含：

```go
type NodeInfo struct {
    NodeID  uint32
    RAAddr  string
    StartAt int64
}
```

etcd 中 `node_id` 是可读字符串；加载到 `MemberTable` 时必须先用 `nodeid.Parse` 转成 `uint32`。

## 跨 RouterAgent 路由

假设：

```text
gatesvr(99.1.0) 在 RouterAgent A 后面
lobbysvr(99.2.0) 在 RouterAgent B 后面
```

etcd 中：

```text
/routeragent/nodes/99.1.0 -> ra_addr: 10.0.0.1:7100
/routeragent/nodes/99.2.0 -> ra_addr: 10.0.0.2:7100
```

调用流程：

1. `gatesvr(99.1.0)` 通过 UDS 把 RPC frame 发给 RouterAgent A。
2. RouterAgent A 根据 RPC header 选出目标 `99.2.0`。
3. RouterAgent A 查本地 `MemberTable`：`99.2.0 -> 10.0.0.2:7100`。
4. RouterAgent A 如果还没有到 `10.0.0.2:7100` 的 TCP peer，就懒连接 RouterAgent B。
5. RouterAgent A 把 frame 通过 TCP 发给 RouterAgent B。
6. RouterAgent B 查本地 UDS 连接表：`99.2.0 -> UDSConn`。
7. RouterAgent B 投递给本机 lobbysvr。
8. 如果是 request，回包沿 remote seq 映射返回调用方。

## 路由模式与节点表关系

### DIRECT

`RoutingKey` 是完整目标 NodeID。

```text
99.2.0 -> nodeid.Parse -> uint32 -> MemberTable.GetByNodeID
```

### ANY

从目标 `server_type` 的节点列表中选一个：

```text
MemberTable.ListByServerType(serverType)
```

### HASH

先取目标 `server_type` 节点列表，再用 routing key 选择一个稳定节点。

### BROADCAST

遍历目标 `server_type` 的所有节点。

## 校验规则

RouterAgent 处理业务握手时至少应校验：

- Handshake body 必须至少 4 字节。
- `nodeID != 0`。
- 解码出的 `serverType != 0`。
- 普通业务进程不应注册成 `ST_ROUTERAGENT`。
- 如果同一个 `nodeID` 已经存在本机连接，应关闭旧连接或拒绝新连接，不能静默双绑定。

测试期同类型只有一个节点，`index=0` 可以接受。后续多实例时，部署系统必须给每个同类型进程分配不同 `index`。

## ra_addr 要求

`ra_addr` 必须是其他 RouterAgent 能连接到的 TCP 地址。

单机测试可以使用：

```yaml
routeragent_listen_addr: "127.0.0.1:7100"
```

跨机器部署不能使用 `127.0.0.1`，否则远端 RouterAgent 会连到它自己的本机地址。跨机器部署应使用内网 IP 或可解析域名：

```yaml
routeragent_listen_addr: "10.0.0.12:7100"
```

## 与 GameServer 参考实现的关系

GameServer 的设计文档同样采用 sidecar 模型：

- 业务进程通过 UDS 连接本机 RouterAgent。
- 握手携带 4 字节 `uint32 nodeID`。
- RouterAgent 本地根据握手注册 UDS 连接。
- 预期由 RouterAgent 代业务节点注册到 etcd。
- etcd 节点 value 中包含 `node_id/server_type/ra_addr/start_at`。

GameServer 当前源码中服务发现骨架存在，但代业务节点注册到 etcd 尚未完整接入 RouterAgent module。BidServer 应按上述设计补完整链路。

## 当前实现状态

截至本文编写时，BidServer 已具备：

- `--nodeid world.serverType.index` 启动参数。
- App 保存 NodeID 字符串，并可转换为 `uint32`。
- 业务进程握手发送 4 字节 `uint32 nodeID`。
- RouterAgent 收握手后本地注册 UDS 连接。
- RouterAgent 自身注册到 etcd。
- Etcd 中 `node_id` 使用可读字符串。
- RouterAgent 可 watch etcd 并更新 `MemberTable`。

仍需继续收口或验证：

- 业务节点握手成功后由 RouterAgent 写入 etcd。
- 业务 UDS 断开时删除业务节点 key。
- `config/gen` 与 schema 中旧的服务私有 `node_id` 字段清理完成。
- 对重复 NodeID、非法 serverType 的握手校验。
- 跨机器部署时 `routeragent_listen_addr` 使用真实可达地址。
