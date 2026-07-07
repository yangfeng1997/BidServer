# 架构与核心模块

本文件是 `CLAUDE.md` 的子文档，给出框架的**架构概览**、**消息分发主干**（配置、应用框架层、消息路由、Handler）与 **gate 转发完整流程**。动手写业务/框架代码前先读本文件。变更架构设计或接口定义时，需同步更新本文件并回看 `CLAUDE.md` 索引。

> 网络与连接层（消息帧 / 握手 / Agent / Session）的细节拆到 [`network.md`](network.md)；集群与跨进程（cluster RPC / 服务发现 / 传输 / TaskQueue）的细节拆到 [`cluster.md`](cluster.md)。本文件保留把它们串起来的主干与转发流程。

## 架构概览

```
src/
├── common/
│   ├── config/        # 运行时配置加载器（Loader[T]，节点级大写注入 + 严格模式 + atomic 热更）
│   ├── logger/        # zap 封装（三层：Logger/Backend/coreLogger）
│   ├── serialize/     # Serializer 接口（json / protobuf 实现）
│   ├── syncmap/       # 泛型 sync.Map 封装，消除类型断言
│   ├── event/         # 进程内同步事件总线（泛型，支持取消订阅）
│   ├── timewheel/     # 单层哈希时间轮（业务定时设施，帧驱动 Advance / 自驱动 Start）
│   ├── taskqueue/     # 跨 goroutine 任务队列（Dispatcher 接口 + Queue 实现，帧驱动 Flush）
│   ├── matchqueue/    # 匹配请求队列：MatchQueue 接口 + JetStreamQueue 真实适配器 + MemoryQueue 单测 fake
│   ├── jumphash/      # Jump 一致性哈希（无环 / 无虚拟节点 / 零额外内存），routersvr CONSISTENT_HASH 选分片用
│   ├── mongo/         # MongoDB 直连封装：连接管理 + 异步 CRUD（回调经 dispatcher 回主循环）
│   ├── pidfile/       # PID 文件工具（Write/Read/Remove/IsRunning）
│   └── daemon/        # 进程后台化（Daemonize/FilterArgs，重 exec 自身）
└── framework/
    ├── cli/           # 服务进程 CLI 入口封装（cobra Builder/start/stop/kill/reload/status）
    ├── application/   # 生命周期编排，Application 核心类型（New/Builder 构造）
    ├── agent/         # 客户端连接管理（Agent、ForwardContext）
    ├── session/       # 纯数据对象（Session、Manager）
    ├── handler/       # 反射 handler 注册与 Pipeline 分发
    ├── pipeline/      # Before/After 中间件链
    ├── module/        # 四步生命周期模块接口
    ├── errors/        # 框架错误码与错误类型（负数 Code，编码进帧层 Response.Code）
    ├── cluster/       # 集群 RPC：顶层=抽象（Cluster 接口/NodeID/ctx/泛型 Call/noop）
    │   ├── discovery/    # etcd 服务发现（Discovery/SDListener/NodeInfo）
    │   ├── transport/    # NATS 传输 + 组装层（NatsRPC + NatsCluster）
    │   ├── routerclient/ # 经 routersvr 转发调微服务的统一客户端（泛型 CallViaSync）
    │   └── pb/           # protobuf 生成代码（cluster.pb.go）
    └── network/       # 网络层
        ├── acceptor/   # TCP / WebSocket Acceptor
        ├── packet/     # 外层 Packet 编解码
        ├── message/    # 内层 Message 编解码（MsgID 数字路由）
        └── handshake/  # 握手协议数据结构
└── servers/
    ├── gatesvr/       # 网关，serverTypeID=1，前端节点
    ├── lobbysvr/      # 大厅（EC 实体 + MongoDB），serverTypeID=2
    ├── onlinesvr/     # 在线目录（纯内存），serverTypeID=5
    ├── routersvr/     # 路由代理（forward+publishmatch），serverTypeID=6
    ├── roomsvr/       # 对局房间（Game + 单主循环 Runtime），serverTypeID=7
    └── matchsvr/      # 匹配服（MMR 队列 + off-loop 编排 + JetStream 消费），serverTypeID=8

protocal/
├── options.proto      # msg_id / server_type / handler_method 自定义 option
├── cluster.proto      # 集群内部消息结构（NodeInfo/ClusterSession/ClusterMessage/ClusterResponse）
├── lobby.proto        # 大厅业务协议（CS_StartMatch 2034/2035、SC_MatchFound 2036 等）
├── match.proto        # 匹配协议（MatchRequest JetStream payload / RPC_PublishMatch / RPC_GameStarted / RPC_MatchTimeout）
├── room.proto         # 房间协议（RPC_OpenGame / RPC_Bid / RPC_AuctionState / RPC_Settle / RPC_Rejoin / RPC_QueryGame）
├── online.proto       # 在线目录协议（Register/Query/Unregister/Touch/BindRoom/UnbindRoom/KickSession）
├── router.proto       # 路由代理协议（RoutingMode / RPC_RouterForward）
├── gate.proto         # 网关客户端协议（CS_Login / SC_Login 等）
└── gen/
    ├── routes/        # gen_routes 生成的路由表（勿手动修改）
    └── ...            # protoc 生成的 pb.go（勿手动修改）

tools/
├── gen_routes/        # 扫描 proto 生成路由映射表（protocal/gen/routes/routes.go）
├── gen_config/        # 读 protoc descriptor 生成配置 struct + 三张字段表
├── config_build/      # 烘焙 conf/ 模板 + values → run/{svc}/conf/config.yaml
└── luban/             # 预留空占位（Luban 配表工具），暂未使用
```

### 模块详解索引

| 模块 | 文档 |
|---|---|
| 配置 / 应用框架层 / 消息路由 / Handler / gate 转发 | 本文件（下文） |
| 消息协议（两层帧）/ 握手 / Agent / Session | [`network.md`](network.md) |
| 集群（包结构 / NodeID / discovery / transport / ICluster / ctx 工具）/ TaskQueue | [`cluster.md`](cluster.md) |

---

## 核心模块说明（分发主干）

### 配置（`src/common/config/`）

```go
// 加载 common 配置（框架级共享：redis/etcd/nats/mongo/log）
commonCfg, err := cfgloader.LoadCommon("run/common/conf/config.yaml")

// 加载服务私有配置，T 是 gen_config 生成的服务 Config struct
svrLoader := cfgloader.NewLoader[conf.GateSvr]("run/gatesvr/conf/config.yaml")
svrLoader.MustLoad()
cfg := svrLoader.Current()
// cfg.NodeId / cfg.Addr / commonCfg.Etcd.Endpoints ...

// 热更监听（生产靠 SIGHUP；开发备选 mtime 轮询）
stop := svrLoader.Watch(30 * time.Second)
defer stop()
```

配置体系分四层：

- **Schema**：`conf/schema/options.proto`（field option 定义）+ `conf/schema/types.proto`（AppStartup）+ `conf/schema/common.proto`（Common）+ `conf/schema/<svc>.proto`（各服务私有，proto 为事实源，option 标记 reload/env/required）
- **Gen**：`go run ./tools/gen_config --pb=conf/schema/gen/config.pb.descriptor --out=conf/schema/gen`（生成带 yaml tag 的 struct + 三张字段表）
- **Build**：`go run ./tools/config_build --env=<env> --svc=<svc>`（common 渲染到 `run/common/conf/`，各服务渲染到 `run/<svc>/conf/`；build 期填小写 `${value}`，大写 `${VAR}` 保留到运行时）
- **Runtime**：`cfgloader.LoadCommon` + `cfgloader.NewLoader[T]` 双 loader（节点级大写注入 + yaml.v3 严格模式 + RequiredFields 必填校验 + atomic 快照热更）

详见 `docs/design/2026-06-03-config-system-design.md`。

### 应用框架层（`src/framework/application/`）

推荐用 **Builder API** 创建 Application，`Serializer` 为必填：

```go
app := application.NewBuilder().
    NodeID(cfg.Node.ID).
    NodeType(cfg.Node.ServerTypeName).
    Frontend(cfg.Node.Addr).          // 前端节点，自动创建 TCPAcceptor；后端不调
    Serializer("json", json.NewSerializer()).
    Routes(routes.Config()).           // gen_routes 自动生成，前端节点使用
    Cluster(natsCluster).              // 可选，默认 noopCluster
    Build()
```

`New(opts...)` 仍可用（向后兼容），Builder 内部复用它。

生命周期：

```
Start()  → 注入集群 handler → [前端]注入路由表+启动 Acceptor → 模块 Init/OnAfterInit
Run()    → 阻塞等待 SIGINT/SIGTERM → Stop()
Stop()   → 逆序 OnBeforeStop → 停止 Acceptor → 强关所有活动连接（遍历 AgentMap Close，使 TCP 与 WS 一致迅速退出读循环、并取消各连接 ctx）→ 等待 Agent 退出（10s）→ 逆序 OnStop
```

关键方法：

- `RegisterHandler(handler, nameFunc)` — 反射注册 handler，同时用于客户端消息和集群 RPC
- `SetForwardFunc(fn)` — 自定义 gate 转发逻辑（覆盖框架默认的 session 绑定路由）
- `Cluster().CallRaw / CastRaw` — 转发场景专用，避免二次序列化

### 消息路由（proto option 驱动）

业务 proto 文件用两个自定义 option 定义路由：

```protobuf
message CS_AddItem {
  option (options.msg_id)         = 2003;   // 数字 ID，走网络
  option (options.server_type)    = "lobbysvr"; // gate 转发目标
  option (options.handler_method) = "LobbyHandler.additem"; // handler 方法
  string op_id   = 1; // 幂等键
  int32  item_id = 2;
  int32  count   = 3;
}
```

运行 `go run ./tools/gen_routes` 生成 `protocal/gen/routes/routes.go`：

- `MsgRouteTable`：msgID → handler route（服务器本地 dispatch 用）
- `ForwardTable`：msgID → serverType（gate 转发用）
- `RespMsgIDTable`：请求 msgID → 响应 msgID

> `MsgID` 在网络帧中的位置见 [`network.md`](network.md) 的「消息协议」；这些表如何驱动连接侧路由见 [`network.md`](network.md) 的「Agent」。

**消息命名约定**（框架不强制，业务层遵守）：

- `CS_Xxx_Req` / `SC_Xxx_Rsp` — 客户端 ↔ 服务器协议
- `CS_Xxx_OneWay` — 客户端单向通知（无返回）
- `RPC_Xxx_Req` / `RPC_Xxx_Rsp` — 服务器间 RPC

### Handler（`src/framework/handler/`）

反射注册，注册时校验签名。合法签名：

```go
func(ctx, *Req) (*Resp, error)   // Request，有返回
func(ctx, *Req)                  // Notify/OneWay，无返回
func(ctx) (*Resp, error)         // 无参数
func(ctx, []byte) (*Resp, error) // raw bytes
```

`Dispatch` 执行顺序：全局 Before → per-route Before → handler → per-route After → 全局 After。
`DispatchCluster` 用于集群 RPC 路径，直接返回序列化 resp bytes。

**框架错误码**（`src/framework/errors`）：handler 返回的 `error` 经 `errors.CodeOf` 映射成负数框架 `Code`（`NotFound -404` / `Timeout -408` / `Internal -500`，无 handler / panic / 超时等），编码进客户端帧层 `Response.Code`（见 [`network.md`](network.md)）。**正数业务错误码不走此层**，由业务自定义、写进 resp body。

**泛型 ctx 工具函数**（消除类型断言）：

```go
handler.UIDFromCtx(ctx)        // int64，客户端路径（源自 Session）
handler.SessionIDFromCtx(ctx)  // int64
handler.IPFromCtx(ctx)         // string
handler.FrontendIDFromCtx(ctx) // string
```

**两条路径 session 注入**（数据源不同，取值工具也不同）：

- 客户端路径：`injectSession(ctx, agent)` 注入 SessionID/UID/IP/FrontendID（源自客户端 `Session`）。
  handler 用 `handler.UIDFromCtx(ctx)` 等取值。
- 集群路径：`onMessage` 注入 `ClusterSession`（含 client_mid/msg_type，源自 `pb.ClusterSession`）。
  handler 用 `cluster.SessionFromCtx(ctx).Uid` 等取值。
- 两套不要混用：`handler.UIDFromCtx` 只在客户端路径有数据，集群路径走 `cluster.SessionFromCtx`。

> 客户端 `Session` 见 [`network.md`](network.md)；`cluster.SessionFromCtx` 与集群 ctx 工具见 [`cluster.md`](cluster.md)。

---

## gate 转发完整流程

> 本流程把连接层（[`network.md`](network.md)：Agent / Session）、路由表（上文「消息路由」）与集群 RPC（[`cluster.md`](cluster.md)：CallRaw/CastRaw）串起来。

```
客户端 Request(MID=42, MsgID=2003)
  → gate agent.handleData
    → forwardTable[2003]="lobbysvr"
    → buildDefaultForwardFn:
        查 session.BoundNode("lobbysvr") = "1.2.1" (有绑定)
        填 ClusterSession{client_mid=42, frontend_id="1.1.1"}
        → cluster.CallRaw(ctx, NodeID("1.2.1"), route, data, done)  ← 发到绑定节点
      → NATS → lobbysvr DispatchCluster
        → LobbyHandler.additem(ctx, req) return resp
      → gate done 回调
        → agent.Response(MID=42, MsgID=2004, respData)
→ 客户端 Response(MID=42, MsgID=2004)
```

**无绑定时**（无状态服务，如 routersvr）：走 `CallAnyRaw` 随机选节点。

**节点绑定**（gate handler 里设置，之后自动路由）：
```go
sess.BindNode("lobbysvr", "1.2.1")  // 登录后绑定玩家所属 lobby
sess.UnbindNode("lobbysvr")          // 断连后解绑
```

OneWay 消息走 `CastRaw`/`CastAnyRaw`，不等返回，不回包。

**本地分发优先（D9 修复）**：gate agent 处理消息时，先用 `registry.HasRoute(route)` 检查本地是否注册了该 handler；有则本地分发，无则转发并填 `ClusterSession.Uid`。

---

## 业务服务补注

### lobbysvr（`src/servers/lobbysvr/`）

单 goroutine 主循环 `Runtime`（零锁），承载全部玩家 Entity-Component 逻辑。玩家登录时从 MongoDB 加载文档，周期 flush 脏组件回库；脏组件落库与剔除玩家通过主循环 continuation 串行执行，无需外部加锁。

**EC 组件**（实现 `Component` 接口、随 `PlayerDoc` flush 的可落库组件，见 `player.go` / `store.go`）：

| 组件 | 文件 | 持有 |
|---|---|---|
| `Currency` | `component_currency.go` | 多币种钱包 `map[string]int64` + 持久 op-dedup 环；`Gain`/`Spend` 幂等（`Spend` 余额不足不记 opID，留重试余地）、`CanAfford`、`ProtectOp`/`UnprotectOp` |
| `Bag` | `component_bag.go` | 背包 `map[int32]int32`（itemID→count）+ op-dedup 环；`Add` 幂等（负数扣减、≤0 移除） |
| `Friend` | `component_friend.go` | 好友 uid 集合；`Has`/`Add`/`Remove`/`List` |
| `Rating` | `component_rating.go` | 单值 MMR（默认 1000，当前只读，留对局后改分扩展点） |

`Mail`（`component_mail.go`）**不是 flushable 组件**——它是独立 `mailbox` collection 的异步 I/O 句柄（`List`/`Claim`/`Get`/`MarkClaimed`），登录后由 `Player.attachMail` 附挂。

**持久化分三个 store**（均异步、回调经 dispatcher 回主循环）：

- `store.go`（`DocStore`/`mongoStore`，collection `players`）：`Load` 整文档；`FlushFields` 把所有脏组件折成**单文档多字段原子 `$set`**（P5-① 跨字段 flush 原子性）。
- `mailbox_store.go`（`MailStore`，collection `mailbox`）：`Claim` 走原子 `FindOneAndUpdate {claimed:false}→true`，insert-only。
- `offline_store.go`（`OfflineStore`，collection `offline_messages`）：每玩家一 doc，多写者 `$push`、登录重放后 `$pull`（`Ack`）；`OpID` 为幂等键（结算 = gameId）。

`presence.go` 是 off-loop 的「在线查询 + 客户端推送」层（`presenceClient`：经 onlinesvr 查玩家所在 gate、经 `GateHandler.pushtoclient` Cast 推送），并集中持有所有服务端→客户端推送 msg_id 常量（2031–2042）。`op_dedup.go` 是 Bag/Currency 复用的有界 FIFO op-id 去重环（`seen`/`remember` 分离 + `protect`/`unprotect` 防离线重放期被淘汰）。

**P4b 新增**：出价编排 `LobbyHandler.bid`（亲和+`CanAfford` 校验→转发 room，失败轻量作废亲和）；结算落地 `LobbyHandler.settle`（在线 `Spend+Add` 持久幂等 / 离线投 `offline_messages` / 清亲和+`UnbindRoom`+推结果，**持久落库后才 ack**，§6.6）；持久化 op-dedup 环（`CurrencyState.Ops`/`BagState.Ops` 随 absolute-`$set` 落库，幂等键 `opID=gameId`/`mailID`）；离线消息机制（新 `offline_messages` collection，每玩家一 doc 多写者 `$push`、登录链重放后 `$pull`，重放先于放行）；对局期禁大厅消费（`Purchase` 查 `roomAffinity`）；mail-claim 重排（grant→persist→mark-claimed）。

**P4c 新增**：重连接回编排——`Runtime.Login` 重连分支：向 onlinesvr 查询保留的 room 绑定 → 若存在，off-loop `RoomHandler.rejoin`（改投参与者 lobbyNodeID + 取竞拍快照）→ on-loop 重建 roomAffinity 并推 `SC_ReconnectAuction`(2042)；room 已死/已关则作废（清绑定 + 推 voided）。`Runtime.Disconnect`：对局期玩家（`RoomAffinity()!=nil`）跳过 online 注销，在线条目连同 room 绑定保留至 5 分钟 TTL（即重连宽限窗口）；非对局期行为不变。`LobbyHandler.Purchase` 被亲和拦截时惰性探测 room 存活（`RoomHandler.querygame`）：room 已死则清亲和+unbind+返回 SC_Purchase code 3（客户端重试），存活则继续拦截（code 2）。

**P5-① 新增**：`flushPlayer` 原子折单——所有脏组件的字段合并为一次 `DocStore.FlushFields`（单文档多字段 `$set`），取代原逐组件单字段写。回调签名改为 `after(ok bool)`，引入 done 门控：仅 `ok==true`（落库成功）时才执行 ack / `$pull` / 剔除玩家 / MarkClaimed / 回包等后续动作；落库失败时：Settle 返回 `done(1)` 令 room 重投，replayOffline 跳过 `$pull` 但仍续登录，Disconnect 保留玩家不剔除，Mailclaim 回 `Code:1` 令客户端重领——配合 `opID` 幂等保证恰好一次，不重扣不丢发。

### roomsvr（`src/servers/roomsvr/`）

有状态后端（serverTypeID=7，conf `conf/roomsvr.yaml`，NodeID `1.7.1`，addr `0.0.0.0:8871`）。**帧驱动**单 goroutine 主循环 `Runtime`（`tick` 推进 timewheel，驱动各局倒计时）管理所有 `Game` 实例（按 gameId 幂等建局）。入站通道唯一：集群 RPC `RoomHandler.opengame`（`CONSISTENT_HASH` by gameId 保证同一局总落同一节点）。无前端监听（不调 `Frontend()`）、无入站 JetStream。四个 RPC route 统一走「捕获 Replier → `Submit` 进主循环 → 返回 `ErrDeferredReply`」的延迟回包模式（`reply.go` 的 `replyProto` 负责序列化回包）。

**P4b 新增**：竞拍状态机——`Game` 加最高价/赢家/币种/封盘；`RoomHandler.bid` 记价+广播；倒计时到点 `settle` 在主循环定赢家、off-loop 经 router `DIRECT` 回告各 lobby（at-least-once 重试）；`Runtime` 加 `cls`+inflight/drain。

**P4c 新增**：两条服务间 Route——`RoomHandler.rejoin`（改投参与者 LobbyNodeID + 返回竞拍快照含币种/倒计时；局不存在/已关返回 code）；`RoomHandler.querygame`（只读，返回 `{exists, closed}`，供 lobby 惰性判活）。

### matchsvr（`src/servers/matchsvr/`）

有状态后端（serverTypeID=8，conf `conf/matchsvr.yaml`，NodeID `1.8.1`，addr `0.0.0.0:8881`）。**无入站集群 RPC handler**——所有匹配请求通过 JetStream MATCH stream 到达。**帧驱动**单 goroutine 主循环 `Runtime`（`tick` 推进 timewheel，驱动 `MaxWait` 超时 reap）串行执行 MMR 队列凑桌；off-loop goroutine 执行开局/回告编排，结果经 `Submit` 回主循环。

**P4b 新增**：`pendingUids` 封「成桌→GameStarted 回告未落定」的残余双发窗口；等待超时 reap（`tw.Tick` 周期扫描超 `MaxWait`，保留 `seen`，best-effort `LobbyHandler.matchtimeout` 回告，cancel 语义）。

---

## 匹配凑桌→开局全链路寻址

```
① 客户端 CS_StartMatch(2034)
  → gate → lobbysvr LobbyHandler.startmatch
    ② lobby 经 routersvr RouterHandler.publishmatch
       → JetStream MATCH stream（WorkQueue，at-least-once）
    ③ matchsvr StartConsumer 消费（入队即 ack）
       → 主循环 MMR 队列凑桌
    ④ off-loop orchestrate：
       match → routersvr → roomsvr RoomHandler.opengame
          寻址模式：CONSISTENT_HASH, key=gameId
       roomsvr 建 Game，返回 room_node_id（自身 NodeID）
    ⑤ off-loop 对每个参与者：
       match → routersvr → lobbysvr LobbyHandler.gamestarted
          寻址模式：DIRECT, key=lobbyNodeId（回告原发起 lobby）
    ⑥ lobby gamestarted：置 roomAffinity → 向 online 绑 BindRoom
          lobby → routersvr → onlinesvr OnlineHandler.bindroom
          寻址模式：CONSISTENT_HASH, key=uid
    ⑦ lobby 推 SC_MatchFound(2036) 给客户端
```

**各跳寻址模式汇总**：

| 发起方 | 目标 | Route | RoutingMode | routing_key |
|---|---|---|---|---|
| lobby | routersvr→matchsvr | `RouterHandler.publishmatch` | —（JetStream，非 forward） | — |
| matchsvr | routersvr→roomsvr | `RoomHandler.opengame` | `CONSISTENT_HASH` | gameId |
| matchsvr | routersvr→lobbysvr | `LobbyHandler.gamestarted` | `DIRECT` | lobbyNodeId |
| lobby | routersvr→onlinesvr | `OnlineHandler.bindroom` | `CONSISTENT_HASH` | uid |
| lobby | routersvr→roomsvr | `RoomHandler.rejoin` | `DIRECT` | room_node_id |
| lobby | routersvr→roomsvr | `RoomHandler.querygame` | `DIRECT` | room_node_id |
| lobby | routersvr→onlinesvr | `OnlineHandler.query`（重连读绑定） | `CONSISTENT_HASH` | uid |
| lobby | routersvr→onlinesvr | `OnlineHandler.unbindroom`（void 作废） | `CONSISTENT_HASH` | uid |

> `publishmatch` 是 routersvr 的原生（非 forward）route，直接把 `MatchRequest` 写入 JetStream；其他**需回包**的跳均经 `routerclient.CallViaSync` 封进 `RouterHandler.forward` 信封（router 用 jumphash 解 CONSISTENT_HASH 分片、DIRECT 直取 NodeID，再 `CallRawSync` 透传）。**无需回包的 fire-and-forget**（`LobbyHandler.auctionstate` / `LobbyHandler.matchtimeout`）则直接 `cls.Cast` 到目标 NodeID，**不经 router**。详见 [`cluster.md`](cluster.md) 的「routersvr 路由模式」与 routerclient。

> **P4c 推送**：重连成功时 lobby 向客户端推 `SC_ReconnectAuction`(2042)，携带竞拍快照（push-only，无 CS 对端）。
