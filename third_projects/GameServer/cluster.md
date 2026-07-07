# 集群与跨进程

本文件是 `CLAUDE.md` / [`architecture.md`](architecture.md) 的子文档，描述集群 RPC 体系（包结构、NodeID 寻址、etcd 服务发现、NATS 传输、ICluster 接口、泛型辅助、ctx 工具）以及与集群 Call 回调强相关的 TaskQueue。写集群 / RPC / 跨进程相关代码前先读本文件。变更集群接口、寻址规则、服务发现 / 传输实现时，需同步更新本文件并回看 `architecture.md` 与 `CLAUDE.md` 索引。

> gate 如何把客户端消息转发到集群节点，见 [`architecture.md`](architecture.md) 的「gate 转发完整流程」；客户端连接与 Session 见 [`network.md`](network.md)。

---

## 集群（`src/framework/cluster/`）

**包结构（顶层=抽象，子包=实现，依赖单向向上）**：

- 顶层 `cluster/`：`Cluster` 接口、`NodeID` 寻址、ctx 工具（`context.go`）、泛型 `Call[R]`（`rpc_helpers.go`）、`noopCluster` 默认实现。**不 import 任何子包。**
- `cluster/discovery/`：etcd 服务发现（`Discovery`/`SDListener`/`NodeInfo`）。import 顶层拿 `NodeID`。
- `cluster/transport/`：NATS 传输 + 组装层（`NatsRPC` + `NatsCluster`）。import 顶层（接口/NodeID/ctx）和 `discovery` 子包。生产集群用 `transport.NewNatsCluster(self, cfg)` 构造。
- `cluster/routerclient/`：「经 routersvr 转发调微服务」的统一客户端，泛型 `CallViaSync[R]`。import 顶层（`Cluster` 接口）与 `protocal/gen/router`。详见下文「routersvr 路由模式」。
- `cluster/pb/`：protobuf 生成代码。

**NodeID**：`| 16位 worldID | 8位 serverTypeID | 8位 serverIndex |`，worldID=0 保留给全局服务（当前单 world MVP 未使用，所有服务在 world 1）。
点分格式 `"1.2.1"` 同时作为 NATS subject 和 etcd key 的一部分。`ParseNodeID("1.2.1")` 解析。

**etcd 服务发现**（`cluster/discovery/`）：

- key：`nodes/world-{worldID}/{serverTypeName}/{nodeID}`
- value：protobuf 序列化的 `NodeInfo`（nodeId/serverTypeName/subject/addr/startTime）
- Lease+KeepAlive；Stop 时 Revoke + 300ms shutdownDelay（让其他节点感知）
- Watch + 30s 全量对账；失败超 10 次通知进程退出（写 dieCh）
- `Discovery().Dump()` / `DumpNode(id)` 可读 JSON 运维接口（protojson）

**NATS RPC**（`cluster/transport/`）：

- 每进程订阅一个 subject，同目标消息有序
- 本地短路：target==self 直接调本地 handler
- 超时传播：ctx deadline → ClusterMessage.Deadline（Unix 纳秒）
- 链路追踪：`cluster.WithTraceID(ctx, id)` → ClusterMessage.TraceId

**ICluster 接口**（`cluster.go`）：

```go
// 有返回（异步，适合帧驱动）
Call(ctx, nodeID, route, req, done func([]byte,error))
CallRaw(ctx, nodeID, route, data, done)        // 转发场景，已序列化
CallAny(ctx, typeName, route, req, done)       // 随机节点
CallAnyRaw(ctx, typeName, route, data, done)
CallSync(ctx, nodeID, route, req) ([]byte, error) // 同步阻塞，适合无状态服务
CallAnySync(ctx, typeName, route, req) ([]byte, error)

// 无返回
Cast(ctx, nodeID, route, msg)
CastRaw(ctx, nodeID, route, data)
CastAny(ctx, typeName, route, msg)
CastAnyRaw(ctx, typeName, route, data)
Broadcast(ctx, typeName, route, msg)           // 广播，失败只打日志
```

**泛型辅助函数**（`rpc_helpers.go`）：

```go
// done 回调直接携带具体类型，自动 Unmarshal
cluster.Call[*pb.Rsp](ctx, nodeID, route, req, func(resp *pb.Rsp, err error){...})
cluster.CallSync[*pb.Rsp](ctx, nodeID, route, req) (*pb.Rsp, error)
```

**ctx 工具函数**：

```go
cluster.WithCluster(ctx, c)           // 注入 cluster 实例
cluster.WithDispatch(ctx, tq)         // 注入 Dispatcher（帧驱动服务）
cluster.WithSession(ctx, sess)        // 注入 ClusterSession
cluster.WithTraceID(ctx, id)          // 注入 traceID
cluster.SessionFromCtx(ctx)           // 取 ClusterSession（再 .Uid/.Id 取字段）
cluster.TraceIDFromCtx(ctx)           // 取 traceID
```

集群路径取 UID 用 `cluster.SessionFromCtx(ctx).Uid`；客户端路径用 `handler.UIDFromCtx(ctx)`。
两套数据源不同（前者 pb.ClusterSession，后者客户端 Session），不要混用。

> 两条路径的 session 注入差异，见 [`architecture.md`](architecture.md) 的「Handler」。

### 主循环延迟回包（Deferred Reply）

适用于帧驱动服务（如 lobbysvr）：handler 无法在当前调用栈同步回包，需将回包动作 dispatch 到主循环 continuation 中执行。

**接口与哨兵**（顶层 `cluster/`）：

```go
// Replier 主循环发起的异步回包句柄，由传输层注入 ctx
type Replier interface {
    Reply(data []byte, err error)
}

// ErrDeferredReply 由 handler 返回，表示将经 Replier 异步回包；
// 传输层 handleMessage 据 errors.Is(err, ErrDeferredReply) 跳过自动回包
var ErrDeferredReply = errors.New("cluster: reply deferred by handler")
```

**ctx 工具**：

```go
cluster.WithReplier(ctx, r)    // 传输层注入 natsReplier（持 reply 主题）
cluster.ReplierFromCtx(ctx)    // 主循环 continuation 取出 Replier，调用 Reply 发包
```

**约束**：仅适用于 NATS 请求-应答路径；本地短路（target==self）不注入 Replier。

---

### handler 注册查询

```go
registry.HasRoute(route string) bool  // gate 据此决定本地分发还是转发（见 architecture.md §gate 转发）
```

---

---

## JetStream 匹配队列（`src/common/matchqueue/`）

匹配请求的可靠投递设施：router 侧发布、matchsvr 侧 durable 消费。唯一真实实现走 JetStream（at-least-once）；单测用内存 fake。

**包级常量**（发布端与消费端共用同一真相源）：

```go
const (
    StreamMatch         = "MATCH"         // JetStream stream 名
    SubjectMatchRequest = "match.request" // 匹配请求 subject
    DurableMatchsvr     = "matchsvr"      // durable consumer 名
)
```

**MatchQueue 接口**：

```go
type MatchQueue interface {
    Publish(ctx context.Context, subject string, msg proto.Message) error
    // handler 返回 nil → ack；非 nil → 不 ack 留重投
    Consume(ctx context.Context, durable string, handler func(ctx context.Context, data []byte) error) error
    Close() error
}
```

**实现**：

| 类型 | 用途 |
|---|---|
| `JetStreamQueue` | 生产实现，`NewJetStreamQueue(urls)` 构造；幂等建 MATCH stream（WorkQueue retention，消费后删） |
| `MemoryQueue` | 单测 fake，`Publish` 入内存并立即投递；`Redeliver(i)` 手动重投验幂等 |

**"入队即 ack" 边界**：matchsvr `StartConsumer` 把消息 `Submit` 进主循环后**同步等待入队完成**再返回 nil（触发 JetStream ack）。因此 ack 时消息仅在内存队列中，matchsvr 进程崩溃会丢失该条。由客户端超时重发 + `(uid, reqId)` 去重兜底（重投消息会被主循环幂等处理后直接跳过）。

**P4b 结算不走 JetStream**：以 direct-RPC-retry（投递）+ 持久 ops（效果幂等）+ `offline_messages`（离线 durable）等价「不丢不重」（设计 §9.4 有据偏离）。

**routersvr 的 publishmatch**：`RouterHandler.publishmatch` 是 routersvr 的原生 route（非 forward），直接调 `matchqueue.Publish`；参见 [`architecture.md`](architecture.md) 匹配链路寻址表。

---

## routersvr 路由模式（`protocal/router.proto`）

**转发信封 + 统一客户端**：需回包的服务间调用统一经 `routerclient.CallViaSync[R]`（`src/framework/cluster/routerclient/`）发起——它把 `{目标类型, 路由模式, 路由 key, 真实 route, 已序列化的真实请求}` 封进 `RPC_RouterForward_Req`，`CallAnySync` 发到任一 routersvr 的唯一转发 route `RouterHandler.forward`；router 侧 `Resolve` 解出目标 NodeID 后 `CallRawSync` 透传，把内层响应原样塞回 `RPC_RouterForward_Rsp.inner_data`，routerclient 再 `Unmarshal` 成具体类型 `R` 返回。`routersvr` 开启 `asyncDispatch`，每次 forward 在独立 goroutine 跑。

```go
// lobby/room/match 侧调微服务的统一姿势
rsp, err := routerclient.CallViaSync[*onlinepb.RPC_Register_Rsp](
    ctx, cls, "onlinesvr", routerpb.RoutingMode_ROUTING_CONSISTENT_HASH,
    uidStr, "OnlineHandler.register", req)
```

**Resolve 把 `{targetType, mode, key}` 解成 NodeID**（`routersvr` 无状态、无主循环，成员实时取自 discovery）：

| 模式 | 值 | Resolve 行为 |
|---|---|---|
| `ROUTING_CONSISTENT_HASH` | 0 | `jumphash.Pick(disc.ByType(targetType), key)` 选分片（uid 定位 onlinesvr；gameId 定位 roomsvr）。成员升序去重后选，保证各 router 实例对同一 key 选出同一节点 |
| `ROUTING_ANY` | 1 | **未实现**（Resolve 走 default 返回 false） |
| `ROUTING_DIRECT` | 2 | `key` 即目标 NodeID 串，直接 `ParseNodeID` |

> **fire-and-forget 不经 router**：无需回包的单向通知（`LobbyHandler.auctionstate` room→lobby、`LobbyHandler.matchtimeout` match→lobby）已知目标 NodeID，直接 `cls.Cast(ctx, node, route, msg)` 发到该节点，**不走 `RouterHandler.forward`**（router 主要为 CONSISTENT_HASH 需要 discovery 成员表、以及 sync 回包透传服务）。

**服务间路由汇总**（内层 route 经目标进程反射注册表按 route 串分发，非 gen_routes 生成）：

| 发起方 | 目标 | Route | RoutingMode | 说明 |
|---|---|---|---|---|
| lobbysvr | roomsvr | `RoomHandler.bid` | `DIRECT` | lobby→room 出价转发 |
| roomsvr | lobbysvr | `LobbyHandler.auctionstate` | `DIRECT` Cast | room→lobby 竞拍态广播 |
| roomsvr | lobbysvr | `LobbyHandler.settle` | `DIRECT` CallVia | room→lobby 结算回告（at-least-once 重试） |
| matchsvr | lobbysvr | `LobbyHandler.matchtimeout` | `DIRECT` Cast | match→lobby 匹配超时回告（best-effort） |
| lobbysvr | onlinesvr | `OnlineHandler.unbindroom` | `CONSISTENT_HASH` | lobby→online 清 room 绑定（P4a 建本期接线） |
| lobbysvr | roomsvr | `RoomHandler.rejoin` | `DIRECT` | lobby→room 重连改投+取竞拍快照（routing_key=room_node_id） |
| lobbysvr | roomsvr | `RoomHandler.querygame` | `DIRECT` | lobby→room 惰性判活（routing_key=room_node_id） |
| lobbysvr | onlinesvr | `OnlineHandler.query`（重连读绑定） | `CONSISTENT_HASH` | lobby→online 读保留 room 绑定（routing_key=uid） |

**P4c onlinesvr `Directory.Register` 语义**：重注册（重连）时保留已有条目的 `RoomNodeID`/`GameID`，不清空——作为重连宽限窗口的前提。TTL 续期与顶号（多设备）逻辑不变。

**P4c lobbysvr `Runtime.Disconnect` 语义**：对局期玩家（`RoomAffinity()!=nil`）跳过 online 注销，在线条目（含 room 绑定）保留至 5 分钟 TTL，即重连宽限窗口；非对局期玩家仍立即注销。

---

## TaskQueue（`src/common/taskqueue/`）

帧驱动服务的跨 goroutine 任务投递：`Dispatcher` 接口 + `Queue` 实现：

```go
tq := taskqueue.New(256)
ctx = cluster.WithDispatch(ctx, tq)   // 初始化时注入一次

func (rt *Runtime) tick() { tq.Flush() } // 每帧消费（如 roomsvr 主循环）

// cluster.Call done 自动投递到主循环
cluster.Call[*pb.Rsp](ctx, nodeID, route, req, func(resp, err){...})
```
