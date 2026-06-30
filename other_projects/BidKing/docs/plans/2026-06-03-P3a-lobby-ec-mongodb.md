# P3a 设计 Spec：lobby 执行模型 + MongoDB 接入层 + EC 核心 + 背包竖切

> 状态：**设计 Spec（已评审通过，待出任务计划）** · 日期：2026-06-03 · 适用：`project`（GameServer）
>
> 承接 [`docs/design/2026-06-01-target-architecture-design.md`](../design/2026-06-01-target-architecture-design.md)（目标架构设计稿，§3.2 lobby / §3.6 登录 / §5.2 请求-响应 / §6.1 登录 / §6.2 大厅 / §7 状态持久化）与 [`docs/plans/2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md)（P3 段）。P2（router+onlinesvr 纵切）已合入 main（PR #12），本 Spec 在其上把 lobby 从「登录 stub」推进为「权威玩家枢纽」的地基 + 第一条玩家业务竖切。
>
> **本 Spec 是 P3 的前半段 P3a。** roadmap §P3 范围较大，本阶段先交付**地基 + 登录加载 + 背包(Bag) 端到端**；好友/邮件/货币组件、心跳→`Touch` 接线、presence、键点 flush 加固留 **P3b**（另起 brainstorm→spec→plan）。

---

## 1. 目标 / 里程碑

让 lobby 第一次具备「权威玩家枢纽」的地基，并跑通一条最薄的玩家业务竖切：

- lobby 建立**单主循环执行模型**（单 goroutine、零锁），所有玩家 EC 逻辑在主循环串行执行。
- 框架支持**主循环发起的异步回包**（room P4 复用）。
- 新增**通用 MongoDB 接入层**（异步 IO，回调投递回主循环）。
- 新增 **lobby Entity-Component 框架核心**（仅 lobby 用，手写注册，组件间同步 event）。
- 登录从 MongoDB **加载玩家工作副本**（uid 不再写死），并接回 P2 的 online 注册。
- **背包(Bag) 组件端到端**：CS 操作改内存状态 → 落库 → 重登读回，且落库幂等。

这是新架构第一次出现「lobby 主循环」「玩家工作副本」「实体组件」三个核心设施。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| D1 | P3 切分 | **拆 P3a + P3b** | P3a = 地基 + 登录加载 + 背包竖切；P3b = 好友/邮件/货币 + Touch/presence + flush 加固。沿用 P1/P2 薄竖切节奏，首个 PR 可评审、EC 边界先定型再铺量。 |
| D2 | lobby 执行/并发模型 | **单一全局主循环** | 一个 lobby 进程 = 一个主循环 goroutine 承载所有玩家 EC 逻辑；入站经 taskqueue 投递进主循环串行执行（零锁）；MongoDB IO 在 off-loop goroutine 异步跑、仅 done 回调经 taskqueue 回主循环。贴合设计稿 §3.2「单 goroutine 帧驱动零锁」、与 room 一致；`event.Bus` 非线程安全正好适配。 |
| D3 | 单主循环下的回包机制 | **给框架加主循环发起的异步回包** | 入站消息按到达序 enqueue 进主循环并**立即返回**（不阻塞入站 goroutine、无队头阻塞、保 NATS 同 subject 有序 → 保同一玩家消息有序）；Mongo/出站 RPC 用异步 done 回调回主循环（主循环永不阻塞 IO）；回包由主循环 continuation 经注入 ctx 的 `Replier` 异步发出。保留现有 `CallRaw + agent.Response` 请求-应答语义，统一处理 login 与业务请求-应答。 |
| D4 | MongoDB 文档模型 | **单文档整玩家、组件内嵌子文档** | `players` 集合，`_id = player_id`，各组件作内嵌 BSON 子文档。加载=一次读；落库用 `$set` 只写 dirty 组件子字段（不重写整文档），缓解整文档写放大。单写者主循环序列化，无需跨组件原子性。 |
| D5 | 存储序列化方式 | **原生 BSON 结构体**（非 proto-bytes blob） | 因 D4 选了「内嵌结构化子文档」：组件内存状态结构体带 bson tag，存储可读/可查、可按子字段 `$set`。proto 仍是**线上 wire schema**，bson 结构体是**存储 schema**，二者分离、各自演进。 |
| D6 | 消息→组件方法路由 | **手写显式注册**（不走反射） | 延续设计稿 §100「手写注册不走反射」。route/msgID → 组件方法 `func(p *Player, req) (rsp, error)` 显式注册到 lobby 分发表。 |
| D7 | Player 内存生命周期 | **登录加载、断连 flush 后剔除** | §6.5 重连可能落到另一 lobby、L2 从 Mongo 重载 → 在本 lobby 内存留副本对重连收益甚微，故断连即 flush+剔除最简且正确。online 条目（onlinesvr，纯内存 5min 宽限）与 lobby 工作副本解耦。P3a 无 room，重连=全新登录（接回留 P4）。 |
| D8 | 落库幂等 | **绝对状态 `$set` + 操作 op-id 去重** | ① flush 写绝对状态 → 重复 flush 不双加；② 变更操作携带客户端 op-id，组件保留有界「近期 op-id 集」去重 → gateway 重试/重复请求不双加。满足验收「重试不产生重复道具」。 |
| D9 | gate 客户端→后端转发被本地分发遮蔽 | **P3a 含最小修复** | 现状 `agent.handleData` 先查 `MsgRouteTable` 本地分发再查 `ForwardTable`，而 `gen_routes` 把带 `handler_method` 的转发消息也放进 `MsgRouteTable` → 携 `handler_method`+`server_type` 的客户端消息（如 `CS_AddItem`）被 gate 当本地处理（无该 handler）丢弃、从不转发（既有 `CS_ClaimReward_Req`/3001 即如此，client→后端转发从未端到端跑通）。修法：`agent.handleData` 仅当本地 registry 真有该 route 的 handler 才本地分发，否则落入转发（新增 `registry.HasRoute` + 一个条件），转发仍用 `MsgRouteTable[msgID]` 作集群 route。最小、语义正确，不动 `gen_routes`。 |

> 沿用全局既定决策（设计稿 §10 / roadmap §0.2）：不引入 Redis；登录并入 lobby；单 world 不跨服；后端服务序列化器用 protobuf（集群 wire）；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 范围

### 3.1 范围内（P3a）

- **框架**：① 主循环发起的异步回包能力（传输层 `Replier` + ctx helper + 延迟回包约定）；② gate 转发遮蔽最小修复（`agent.handleData` 本地无 handler 才转发，`registry.HasRoute`，D9）；③ gate 转发填 `ClusterSession.Uid`（lobby 据 uid 定位 Player）。
- **lobby 运行时**：单主循环（串行排空 taskqueue + `timewheel.Advance`）；自定义入站 cluster MessageHandler（enqueue + 循环内分发 + 异步回包）；出站 Mongo/RPC 异步 done 回调回循环。
- **MongoDB 接入层**（`src/common/mongo`，通用）：client/连接、collection 访问、异步 CRUD（回调经 dispatcher 投递回主循环）；config 扩 mongo URI。
- **EC 核心**（lobbysvr 内）：`Player` 实体、`Component` 接口、手写注册、消息→组件方法手写分发、`event.Bus` 机制铺设（`PlayerLoaded`）。
- **背包(Bag) 竖切**：proto（CS/SC + 路由 option）、组件实现（Add/List + op-id 去重）、内嵌 BSON 子文档加载/落库、handler 分发。
- **登录改造**：token stub 保留但 uid 来自 Mongo 加载（不再写死 10001）；接回 P2 online 注册；异步回包。

### 3.2 范围外（留 P3b / 更后）

- 好友/邮件/货币组件（注册/加载/落库/proto）。
- 心跳活跃 → onlinesvr `Touch` 接线、presence、全服在线数。
- 键点 flush / flush 加固、空闲卸载策略（P3a 仅断连 flush+剔除 + 周期 flush 雏形）。
- 跨组件业务 event（如「买道具扣货币」）。
- 真实 token 校验、玩家数据版本化/迁移、分库分表、懒加载。
- 重连接回（依赖 room，P4 §6.5）。

---

## 4. 框架改动：主循环发起的异步回包

### 4.1 现状

`cluster.MessageHandler = func(ctx, msg, route) ([]byte, error)`；传输层 `nats_rpc.handleMessage` 同步调 handler 后立即 `Publish(natsMsg.Reply, resp)`。handler 跑在 NATS 入站 goroutine 上。

### 4.2 改动

- `handleMessage` 调 handler 前，把一个 **`cluster.Replier`**（封装 `natsMsg.Reply` 主题 + `conn.Publish` + 序列化）注入 ctx。
- handler 返回**延迟回包哨兵**（如 `cluster.ErrDeferredReply`）时，`handleMessage` **不** Publish。
- 主循环 continuation 拿到结果后调 `replier.Reply(data, err)` → marshal `ClusterResponse` → `Publish` 到 reply 主题。
- 兼容：`natsMsg.Reply == ""`（OneWay）无回包；**正常返回的 handler 仍自动回包**——gate/online/router 行为不变。

### 4.3 约束

- 改动集中在 `transport/nats_rpc.go`（`handleMessage` + `natsReplier` + 提取 `publishReply`）与 cluster ctx helper（`Replier`/`ErrDeferredReply`/`WithReplier`）；反射 `handler.Registry` **不改**——lobby handler 方法返回 `ErrDeferredReply`，`pcall` 透传该 error，`handleMessage` 据 `errors.Is` 跳过自动回包。
- Replier 携带 reply 主题字符串即可跨 goroutine 使用（NATS publish 线程安全）；超时由请求方 deadline 兜底（迟到回包对端已超时丢弃，无害）。

### 4.4 gate 转发遮蔽最小修复（D9）

- `agent.handleData`：把"先本地分发"改为"**本地有 handler 才本地分发，否则落入转发**"——新增 `handler.Registry.HasRoute(route) bool`，条件改为 `if route, ok := msgRouteTable[msgID]; ok && registry.HasRoute(route) { 本地分发; return }`，否则进入 `forwardFn`。转发仍用 `msgRouteTable[msgID]` 作集群 route。不动 `gen_routes`。

### 4.5 gate 转发填 `ClusterSession.Uid`

- `application.buildDefaultForwardFn` 构造转发 session 时填 `sess.Uid = fctx.Agent.Session().UID()`（提取纯函数 `newForwardSession` 便于单测）。lobby 据 `SessionFromCtx(ctx).Uid` 定位 Player。

---

## 5. lobby 运行时

### 5.1 主循环

- **一个主循环 goroutine** 承载全部玩家 EC 逻辑（零锁）。
- 循环职责：串行排空 `taskqueue`（处理入站任务 + Mongo/RPC done 回调）+ 推进 `timewheel.Advance()`（周期 flush 等定时）。
- 复用现有 `src/common/taskqueue` + `src/common/timewheel`；tick 量级 vs 阻塞式取队列在 writing-plans 定（默认小 tick 帧驱动，低延迟优化留后续）。

### 5.2 入站

- lobby **复用框架反射 registry**（不另造自定义 handler）：每个集群 route 注册一个 `LobbyHandler` 方法（`Login`/`PlayerDisconnect`/`Additem`/`Baglist`），方法体统一为：
  1. 入站 goroutine 上捕获 `cluster.ReplierFromCtx` + `SessionFromCtx().Uid`，把工作按到达序 `Submit`（`Enqueue`）进主循环后**返回延迟回包哨兵 `cluster.ErrDeferredReply`**（registry 透传、`handleMessage` 跳过自动回包）→ 不阻塞入站 goroutine、保 NATS 同 subject 有序 → **保同一玩家消息有序**。`PlayerDisconnect` 是 Notify（无回包），只 `Submit` 不返回哨兵。
  2. 主循环内：查 `players[uid]` → **显式调用**组件方法（D6 手写，如 `p.Bag().Add(...)`）→ `Replier.Reply(marshal(rsp), err)`。
- 多数业务同步：玩家已加载时，组件方法在循环内同步改内存+标脏+返回 rsp → 立即经 Replier 回包；flush 异步、不挡回包。
- 异步回包主要用于**登录（需 Mongo 加载）**与需要新 IO 的操作。
- 约束：延迟回包仅适用于 NATS 请求-应答路径；`Call` 的**本地短路**（target==self）不注入 Replier，故 lobby 不得自调其延迟 handler（P3a 入站均来自 gate/router 跨节点，不触发）。

### 5.3 出站 / IO

- 循环内**绝不阻塞 IO**。
- Mongo 与出站 cluster RPC 走异步 `done` 回调，经 `cluster.WithDispatch` / 同款 dispatcher 投递回主循环 continuation。
- 顺带消化 P2 遗留：lobby 不再在入站 goroutine 上同步跑 `Login`/`PlayerDisconnect`。

---

## 6. MongoDB 接入层（`src/common/mongo`）

- 新增依赖 `go.mongodb.org/mongo-driver`；`conf/lobby.yaml` 扩 mongo URI + config 结构。
- 能力：client 连接（从 config，超时/重试）、collection 访问、**异步 CRUD**——driver 调用在 off-loop worker 执行，完成后把 `done(result, err)` 经 ctx 里的 dispatcher 投递回主循环（镜像 `cluster.Call` 回调投递语义）。
- 通用、单元可测；集成测试用容器 Mongo（沙箱仅编译验证，`//go:build integration`）。
- 供 lobby 与后续微服务（room 结算落库等）复用。

---

## 7. EC 框架核心（仅 lobbysvr 内）

- **`Player` 实体**：`player_id` + 组件集合 `map[ComponentName]Component`，**主循环独占、零锁**。
- **`Component` 接口**：`Name()` / `Load(sub bson)` / `MarshalState() (sub bson)` / 标脏（`Dirty()`/`ClearDirty()`）。
- **手写显式注册**：`player.AddComponent(NewBagComponent())`（§100，不走反射），依赖与初始化顺序编译期可检。
- **消息→组件方法路由**：手写注册（route/msgID → 组件方法），不走反射（D6）。
- **组件间通信**：进程内同步 `event.Bus`（复用 `src/common/event`，仅主循环用，天然零锁）。P3a 只有背包一个组件，先铺机制（如发布 `PlayerLoaded`），真正跨组件 event（买道具→扣货币）留 P3b。

> 命名消歧（设计稿 §104）：此处「组件 / Component」专指 lobby 实体组件，与框架 `module.Module` 是两层概念。

---

## 8. 背包(Bag) 端到端竖切

- **proto**：`lobby.proto`（或拆背包相关 message）加 `CS_AddItem`/`SC_AddItem`、`CS_BagList`/`SC_BagList`，带 `msg_id` + `server_type="lobbysvr"` + `handler_method` option；重跑 `gen_routes`（gate 据 msgID 转发到绑定 lobby）。
- **存储**：背包状态以**内嵌 BSON 子文档**存 `players` 文档 `bag` 字段；flush 用 `$set: {bag: ...}` 只写背包子字段。
- **操作**：`Bag.Add(opID, itemID, count)` 在主循环同步改内存 + 标脏；`Bag.List()` 读内存。
- **幂等**（D8）：① flush 写绝对状态 → 重复 flush 不双加 ✓；② `Add` 携带客户端 op-id，Bag 保留有界「近期 op-id 集」去重 → 重试/重复请求不双加 ✓。
- **flush 触发**（P3a 雏形）：周期 flush（timewheel 主循环任务，flush dirty 组件）+ 断连 flush；键点 flush 留 P3b。flush 时主循环对组件状态取**值快照**交 off-loop writer，写期间若又有变更则 dirty 保持、下次再写（绝对写保证安全）。

---

## 9. 登录改造 + Player 生命周期

### 9.1 登录

```
gate: CallAnySync 转发 RPC_Login_Req → 任一 lobby
lobby Login handler: Enqueue 进主循环 → 立即返回延迟回包哨兵
主循环:
  校验 token（stub：从 token 解析出 uid，不再写死常量 10001）
  据 uid 查 players[uid]；不在内存 → 异步 Mongo 加载（无文档则建新档）
  加载 done（主循环 continuation）:
    构建 Player + AddComponent（手写注册）
    经 router 注册 online（沿用 P2 现有异步 RPC）
    经 Replier 异步回包 RPC_Login_Rsp{uid, lobbyNodeID}
```

### 9.2 断连

```
gate: Cast RPC_PlayerDisconnect_Notify → 绑定 lobby
主循环:
  flush 该 Player（异步）
  flush done → 从 players[uid] 剔除
  经 router 注销 online（沿用 P2 现有）
```

### 9.3 与 online 的关系

online 条目（onlinesvr，纯内存、5min 宽限）是「在否/在哪」权威；lobby 工作副本易失，断连即 flush+剔除。二者解耦。P3a 无 room，重连=全新登录（§6.5 接回留 P4）。

---

## 10. 关键数据流（背包）

```
client CS_AddItem(opID,itemID,count)
  → gate: forwardTable[msgID]=lobbysvr，CallRaw 到绑定 lobby
    → lobby 自定义 handler: Enqueue 进主循环 + 返回延迟回包哨兵
      → 主循环: players[uid].Bag().Add(opID,...) 同步改内存+标脏（op-id 去重）
              → Replier 异步回包 SC_AddItem{ok}
    → gate done: agent.Response(MID, SC_AddItem)
  → client 收到 SC_AddItem
（异步）周期/断连 flush: $set players._id=uid {bag: 绝对状态}
重登: 主循环 Mongo 加载 players._id=uid → Bag.Load(bag 子文档) → 背包读回一致
```

---

## 11. 测试策略

- **单元**：EC 注册/分发、event 分发、`Bag.Add/List` + op-id 去重、MongoDB 层、主循环入站 enqueue/异步回包机制。
- **集成**（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+etcd+MongoDB）：登录加载 → 背包 CS 改状态落库 → 重登读回；重复 op-id 不双加。
- TDD：从设计意图/本 Spec 推导用例、先写失败测试（roadmap §0.5）；后端服务集成测试放各服务自身包内（Go internal 可见性）。

---

## 12. 风险 / 开放问题

- **单主循环吞吐**：单 goroutine 承载全进程玩家逻辑——MVP 单 world、lobby 逻辑轻量、IO 已 off-loop，不阻塞主循环，可接受；如成瓶颈，演进方向是 per-player 分片或多循环（非 P3a）。
- **单文档 16MB 上限**：mail/bag 增长可能逼近——MVP 单文档可接受，复杂迁移/分库分表 roadmap 已延后；P3a 仅背包，余量充足。
- **flush 期间并发变更**：主循环对组件取值快照交 writer + 绝对 `$set` 写，写期间新变更保持 dirty 下次再写——绝对写保证幂等安全。
- **延迟回包与请求方超时**：迟到回包对端已超时丢弃无害；需保证主循环不会"丢任务"（taskqueue 满丢任务是框架现存风险，§9.4 列入 P5 backlog，本阶段沿用 256/512 容量，监控 `Len`）。
- **新建玩家档**：首次登录无文档 → 建新档的并发/重复登录竞态由 online 顶号 + 单主循环序列化兜底；建档用 upsert 幂等。

---

## 13. P3a 交付物速查

| 类别 | 交付物 |
|---|---|
| 框架 | 传输层 `Replier` + 延迟回包哨兵 + ctx helper（主循环异步回包）；gate 转发遮蔽修复（`registry.HasRoute`，D9）；gate 转发填 `ClusterSession.Uid` |
| 通用 | `src/common/mongo` 接入层（异步 CRUD，回调回主循环）；config 扩 mongo |
| lobby 运行时 | 单主循环（taskqueue + timewheel）；自定义入站 handler（enqueue + 分发 + 异步回包）；出站异步 IO |
| EC 核心 | `Player` 实体、`Component` 接口、手写注册、消息→组件方法分发、`event.Bus` 机制（`PlayerLoaded`） |
| 业务 | 背包(Bag) 组件端到端（Add/List + op-id 去重 + 内嵌 BSON 加载/落库） |
| proto | `lobby.proto` 扩背包 CS/SC + 路由 option；重跑 `gen_routes` |
| 登录 | uid 由 token stub 解析（不写死 10001）→ 据 uid 从 Mongo 加载玩家数据；接回 P2 online 注册；异步回包；断连 flush+剔除 |
| 测试 | EC/Bag/Mongo/回包单测；登录加载+背包落库回读集成测试（编译验证） |

---

## 附录：工作流约定

- 本 Spec 评审通过后，走 `writing-plans` 产出任务级实现计划 `docs/plans/2026-06-03-P3a-lobby-ec-mongodb-impl-plan.md`（每步含失败测试 + 实现 + 验证）。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；合入前先 rebase 最新 origin/main。
- 集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf；MongoDB 存储用 BSON（与 wire 分离）。
