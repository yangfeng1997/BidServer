# 目标架构设计：房间/匹配制对战游戏服务端

> 状态：**设计稿（待评审）** · 日期：2026-06-01 · 适用：`project`（GameServer）
>
> 本文描述**目标架构**及其与**现状代码**的差异与迁移路径。实现落地后，再把定稿内容同步回根目录的 `architecture.md` / `network.md` / `cluster.md` 体系（遵循 `CLAUDE.md` 的文档维护约定）。

---

## 1. 背景与目标

### 1.1 为什么重构

现有代码框架层（network / agent / session / handler / cluster / 通用库）完成度约 80~90%，但：

- **拓扑不匹配业务**：proto 示例与文档以 MMO 大世界（`worldsvr` / `scenesvr`）为模板，gate **直连**后端；而真实业务是**房间/匹配制对战游戏**（lobby / match / room）。
- **业务层几乎为空**：仅 `gatesvr` 有带 TODO 的样板，`lobby/match/room/login/db` 均为 `.gitkeep` 空壳——**几乎无沉没成本，正是重定架构的好时机**。

### 1.2 目标

确立一套**以 lobby 为玩家枢纽、经 router 接入一组有状态微服务**的分布式架构，统一用 **NATS** 做集群总线、**etcd** 做服务发现、**MongoDB** 做持久化，并最大化复用现有框架层。

### 1.3 设计原则

- **简单优先**：单一传输（NATS）、单一持久化（MongoDB 直连）、无多余代理进程。
- **异步事件为主**：跨进程通信以异步消息/事件为主，请求-响应也走异步回调（actor 风格）。
- **状态边界清晰**：连接级状态、权威业务状态、无状态路由三者分层。
- **统一微服务框架**：所有后端服务（lobby / online / match / room …）**共用同一套通用框架**（`src/framework` + `src/common`）——生命周期、Handler 分发、cluster RPC、帧驱动、MongoDB/router 接入全部复用，服务只写业务（详见 §3.0）。尽量不推翻现有框架。

---

## 2. 总体架构

```
                              etcd（服务发现，全局）
                               ▲      ▲      ▲      ▲
   client ──TCP/WS──► gateway ─NATS─► lobby ─NATS─► router ═NATS═► { online, match, room, ... }
                      连接级           权威玩家       无状态          有状态微服务
                      session          状态(工作副本)  转发+横切中枢    │
                      (仅转发)            │                          │
                                         └──────► MongoDB ◄──────────┘
                                          (lobby / 微服务 直连，无 dbsvr 代理)

   注：登录在 lobby 处理（校验 token + 加载玩家 + 经 router 向 onlinesvr 注册在线）；gateway 仅选 lobby 并转发。
```

### 2.1 组件职责总览

| 组件 | 状态 | 核心职责 | 驱动模型 | 来源 |
|---|---|---|---|---|
| **gateway** | 连接级 session | 建连/握手/心跳/收发；登录后记 `uid→lobby` 绑定；转发玩家消息到其所属 lobby | 纯 IO（read/write 双协程） | 复用现 `gatesvr`+`agent`+`session` |
| **lobby** | 权威玩家状态（内存工作副本，MongoDB 托底） | 玩家集群分身、大厅逻辑、发起匹配、持 `玩家↔room` 亲和绑定、玩家状态加载/落库 | 事件驱动（`taskqueue.Flush`） | 复用 `cluster.Call`+`taskqueue`+`BindNode` |
| **router** | 无状态 | `逻辑目标→具体实例` 转发 + 负载均衡；限流/监控/灰度/链路追踪 | 无状态，NATS queue-group（N 实例） | 基于现有 `cluster`/NATS 抽出 |
| **match** | 有状态 | 匹配队列、凑桌、分配 room 实例、回告 lobby | 队列/事件驱动 | 新建 |
| **room** | 有状态 | 对局实例、战斗逻辑、帧广播、对局结算 | **帧驱动**（`timewheel.Advance` + tick） | 新建 |
| **onlinesvr** | 有状态（在线态，易失） | 全局在线目录：在线状态 + 玩家位置（gateway/lobby/room）；跨 gateway 重复登录踢号、定位玩家、presence、重连恢复 | 请求/事件驱动 | 新建 |

> **无 dbsvr**：持久化由 lobby / 各微服务**直连 MongoDB**，不设数据库代理进程。
> **无 loginsvr**：登录逻辑（token 校验 + 玩家加载 + 在线注册）并入 **lobbysvr**；gateway 仅做 lobby 选择与转发，不设独立登录进程（见 §3.6）。

---

## 3. 组件详解

### 3.0 微服务通用框架（所有后端服务的统一底座）

**原则**：lobby / online / match / room 等所有后端服务**共用同一套通用框架**（现有 `src/framework` + `src/common`），不为每个服务另造轮子。框架提供横向能力，服务只实现自己的业务。

**框架提供（横向，所有服务一致）**：

- 生命周期：`application`（Builder、Start/Run/Stop）+ `module`（**服务模块**四步：Init/OnAfterInit/OnBeforeStop/OnStop）。
- 消息分发：`handler` 反射注册 + `pipeline` 中间件（全局 / per-route Before/After）。
- 集群接入：`cluster`（NATS transport + etcd discovery + NodeID 寻址 + 泛型 Call/Cast），统一经 router 收发。
- 帧驱动设施：`taskqueue`（跨 goroutine 回调投递）+ `timewheel`（定时）；有状态服务在主循环 Flush/Advance。
- 通用库：`config` / `logger` / `serialize` / `syncmap` / `event`。
- **新增统一能力**：MongoDB 接入层、router 客户端、（可选）统一 bootstrap 助手 `service.Run(cfg, modules...)`，让各服务 `main.go` 近乎一致。

**服务只实现（纵向，各服务不同）**：自己的 proto + 业务 Module（资源初始化、主循环 tick）+ 业务 Handler（CS/RPC 方法）+ 业务模型（如 lobby 的 Entity-Component，见 §3.2）。

> **特例**：gateway（多一套 `network`/`agent`/`acceptor` 连接层）与 router（无业务 Handler 的无状态转发）仍构建在同一框架底座（Application/Module 生命周期）之上，只是横向能力侧重不同。

### 3.1 gateway（无状态业务，持连接级 session）

- 终结客户端 TCP/WS 连接，负责握手、心跳、两层帧（Packet/Message）编解码——**完全复用现有 `network` + `agent`**。
- 持有**连接级 session**（活动连接、Agent、session id、握手状态、心跳计时、登录后的 uid 绑定）。这部分状态**断线即弃、重连重建**，不是权威游戏数据，因此 gateway **可水平扩展**（每条连接只落在一个 gateway）。
- **不承担登录逻辑**（见 §3.6）：gateway 不校验 token；收到登录请求后经 etcd 选一个 lobby 转发，由 lobby 处理登录。
- 据 lobby 登录响应，在连接 session 上记录 `uid` 与 `lobby→nodeID`（复用现有 `session.BindNode`），之后该连接的玩家消息统一转发到这个 lobby 实例。
- 向 lobby 转发时携带 `ClusterSession{id, uid, ip, frontend_id}`——**现有 pb 消息已具备**。

### 3.2 lobby（玩家集群枢纽，权威业务状态）

- 是玩家在集群侧的"分身"：承载大厅逻辑（好友、商城、开始匹配等入口）。
- **处理登录**（无独立 loginsvr）：校验 token（无状态）+ 加载玩家数据（MongoDB）+ 经 router 向 onlinesvr 注册在线、处理跨 gateway 重复登录（见 §6.1）。
- **状态模型**：lobby 的玩家状态是**内存工作副本 + MongoDB 托底**。被分配到某玩家时从 MongoDB 加载其状态；在关键节点 / 周期 flush 回 MongoDB。"权威"指"内存工作集 + 持久化落地"，而非唯一真相源——这是 lobby 可被重分配/宕机恢复的前提。
- 持有 `玩家↔当前 room` 亲和绑定（`BindNode("room", X)`），并经 router 把该绑定**同步到 onlinesvr**（用于重连恢复与玩家定位，见 §6.5）。
- 经 **router**（而非直连）与 online/match/room 等微服务通信，从而**不感知微服务拓扑**。

**业务模型：Entity-Component（实体-组件）**

- lobby 的玩家业务用 **Entity-Component 框架**（**仅 lobby 服使用**，不提取为通用库）组织：`Player` 是实体，**只存 `player_id` + 其拥有的组件集合**；玩家各项功能/数据拆成独立**实体组件**（如背包、好友、邮件、货币…），各组件自管状态与加载/落库。
- **手写注册**：组件通过**显式代码**注册到 `Player`（如 `player.AddComponent(NewBagComponent())`），**不走反射 / 配置扫描**——编译期可检、依赖与初始化顺序清晰、无反射魔法（契合 Go 风格）。
- **组件间通信：同步 event**：不同实体组件之间通过**进程内同步事件总线**（复用 `src/common/event`）通信——发布即同步串行回调（lobby 单 goroutine 帧驱动，零锁）；组件间**不直接相互调用**，以事件解耦。
- **与消息分发衔接**：lobby 框架级 Handler 收到客户端消息后路由到对应玩家的组件方法（如 `CS_AddFriend_Req → player.Friend().Add(...)`）。
- **初始组件集**：背包(Bag)、好友(Friend)、邮件(Mail)、货币(Currency) 等，后续按业务增量添加。
- **命名消歧** ⚠️：此处"组件"是**实体组件（玩家功能单元）**，与框架的**服务模块**（`src/framework/module` 的生命周期单元）是**两个不同层级**的概念。为彻底避免混淆，框架层生命周期单元已从 `Component` 更名为 `Module`（见 `src/framework/module`），故 `Component` 一词此后**专指 lobby 的实体组件**（如 `entity.Component` / `PlayerComponent`）。

### 3.3 router（无状态转发 + 横切中枢）

- **不持业务状态**，只做 `逻辑目标（服务类型/路由 key）→ 具体实例` 的转发与负载均衡；对已知目标 nodeID 的消息做透传，对"任意某类型"的消息做实例选择（复用现有 `CallAny` / etcd 发现）。
- **横切能力集中地**：限流、监控/指标采集、灰度发布、链路追踪（`traceID` 现有代码已支持端到端传播）。
- **部署形态**：无状态 → 以 **NATS queue-group** 跑 N 实例，lobby 发到 router 的逻辑 subject，NATS 自动分发到任一 router 实例，**无单点、随意扩缩容**。
- **取舍**：lobby 始终经 router（即使已知目标 room nodeID），换取"lobby 不感知微服务拓扑"的解耦，代价是亚毫秒级一跳——可接受。

### 3.4 match（匹配微服务，有状态）

- 维护匹配队列，按 **MMR（匹配评分）** 凑相近水平的玩家成桌；凑齐后在某 **room 实例上开一局**（`gameId`），回告相关玩家所在 lobby「对局在 room#X / game G」。
- 队列为内存态；匹配请求建议走 **JetStream**（不能丢玩家的匹配请求，见 §5.3）。MMR 的分桶/扩容搜索策略见 §12。

### 3.5 room（对局微服务，有状态，帧驱动）

- **每个 room 实例承载多局**：进程内以 `gameId` 区分并存的多个对局；玩家消息携带 `gameId`，room 实例分发到对应对局。各对局独立成员与战斗状态，共享同一帧循环遍历推进。单实例承载局数上限/开局调度见 §12。
- **帧驱动**：用 `timewheel.Advance` + tick 在主循环串行执行逻辑与定时（零锁，与 `taskqueue` 一致）。
- 对局帧广播走 **core NATS**（丢帧靠客户端插值/快照，不上 JetStream，见 §5.3）。
- 对局过程内存态；关键节点（开局/结算）落 MongoDB。

### 3.6 登录与鉴权（并入 lobbysvr，无独立 loginsvr）

- **不设独立 loginsvr**。登录逻辑（校验 token + 玩家数据加载 + 在线注册 + 重复登录处理）在 **lobby** 完成。
- **gateway 只做 lobby 选择与转发**：gateway 不含 token 逻辑；收到登录请求后经 etcd 选一个 lobby（负载最低/就近）转发，**被选中的 lobby 即成为玩家归属 lobby**；gateway 据响应记 `uid` + `BindNode("lobby", nodeID)`。
- **token 校验（默认无状态）**：*假设* token 由本集群之外的账号/登录系统（如 Web 账号服务）签发，lobby 校验签名/有效期；lobby 本就直连 MongoDB，可按需做账号落地查询。
- 对接 gatesvr 现有 `verifyToken` / `assignBackendNodes` 的 TODO 调用点（token 校验移到 lobby；gateway 侧保留"选 lobby + 记绑定"）。

### 3.7 onlinesvr（在线状态微服务，有状态）

- **全局在线目录 / Presence**：存储 `玩家 → {在线状态, 所在 gateway/lobby/room nodeID, 登录时间}`，是"玩家是否在线、在哪"的权威。
- **职责**：
  - **跨 gateway 重复登录检测与踢号**：同一玩家在 G2 再次登录时，onlinesvr 发现其已在 G1/L1 在线 → 通知踢旧会话再登记新会话。现有 `session.Manager` 仅能踢**单 gateway 内**旧连接，跨 gateway 需 onlinesvr 全局协调。
  - **定位玩家**：跨服消息（好友私聊、邀请、系统推送）需知道玩家落在哪个 gateway/lobby → 查 onlinesvr。
  - **Presence**：好友在线状态、全服在线数。
  - **重连恢复**：提供玩家当前 room 等绑定，供重连接回（见 §6.5）。
- **状态模型**：在线态为**纯内存态（易失，不持久化到 MongoDB）**；onlinesvr 重启后由 gateway 心跳 / 玩家重连重建。
- **离线判定与重连宽限**：超过 **5 分钟**无活跃刷新判玩家离线；这 5 分钟同时是**重连宽限窗口**——窗口内重连可凭 onlinesvr 残留的绑定（lobby/room）无缝接回（见 §6.5），超窗则按全新登录。
- **接入**：作为微服务经 **router** 被 lobby 调用；登录期由 lobby 注册在线（见 §6.1）。
- **部署**：按 uid **一致性哈希分片**（多实例）；调用方（lobby）按 uid 一致性哈希定位目标分片，扩缩容时仅少量 uid 迁移。

---

## 4. 传输、寻址与服务发现

- **统一总线 NATS**：gateway↔lobby、lobby↔router↔微服务，**全部走 NATS**，一种传输、一套运维。
- **寻址**：复用现有 `NodeID = | 16位 worldID | 8位 serverTypeID | 8位 serverIndex |`，点分格式（如 `1.3.1`）同时作 NATS subject 与 etcd key 的一部分。每进程订阅自己 NodeID 的 subject，**同目标消息有序**，target==self 时**本地短路**。
- **服务发现 etcd**：key=`nodes/world-{worldID}/{serverTypeName}/{nodeID}`，value=protobuf `NodeInfo`；Lease+KeepAlive、Watch+30s 全量对账、失败超限写 `dieCh` 退出——**现有 `discovery` 子系统已实现**。
- **单 world / 不做跨服**：`worldID` 预留但不启用；跨服通信、跨服广播均不考虑（业务确认无需跨服）。

---

## 5. 通信范式

### 5.1 异步为主

跨进程通信以**异步事件/消息**为主：解耦、削峰、可回放。

### 5.2 请求-响应（异步回调）

需要结果的调用（匹配、进房间等）走**异步请求-响应**：请求方生成 correlation-id + reply-subject（NATS inbox），响应到达时通过回调处理。**现有 `cluster.Call(ctx, node, route, req, done)` 已实现这套**：NATS request-reply + deadline 传播 + `done` 回调经 `taskqueue.Flush` **投递回帧循环串行执行**。纯单向通知走 `Cast` / `Broadcast`。

### 5.3 投递语义（NATS core vs JetStream）

| 类别 | 通道 | 语义 | 理由 |
|---|---|---|---|
| 对局帧广播、实时状态 | core NATS | at-most-once | 最低延迟；丢帧靠客户端插值/快照/重传 |
| 匹配请求 | JetStream | at-least-once | 不能丢玩家的匹配请求 |
| 结算/发奖/关键业务事件 | JetStream | at-least-once + 幂等 | 必达 + 可回放；发奖须幂等（见 §7） |
| 玩家上下线 / presence 通知 | JetStream（或 core，按需） | at-least-once | 在线态一致性（onlinesvr） |

---

## 6. 关键数据流

### 6.1 登录

```
client → gateway: 建连 + 握手 + 提交 token
gateway: 经 etcd 选负载最低的 lobby → 转发登录请求（不校验 token）
lobby: 校验 token（无状态签名校验，token 来自外部账号系统）+ 加载玩家数据(MongoDB)
lobby →(router)→ onlinesvr: 注册在线（玩家→gateway/lobby 位置）
  └ onlinesvr 若发现重复登录 → 通知踢旧会话（旧 gateway+lobby），再登记新会话
lobby → gateway: 登录响应(uid, ok)
gateway: 记 uid + BindNode("lobby", 该 lobby nodeID) → 回客户端
```

### 6.2 大厅

```
client → gateway →(NATS)→ lobby[玩家所属]
lobby: 从 MongoDB 加载/已加载玩家工作副本，处理大厅逻辑
```

### 6.3 匹配

```
client(开始匹配) → gateway → lobby
lobby →(NATS)→ router →(NATS/JetStream)→ match: 加入匹配队列
match: 凑齐一桌（MMR 相近）→ 在 room 实例 X 上开一局 gameId=G → 回告各玩家 lobby「room#X / game G」
```

### 6.4 进房间 / 对局

```
lobby: 绑定 room#X + gameId=G（BindNode）+ 经 router 更新 onlinesvr(玩家当前 room=X/game G)
client(对局操作) → gateway → lobby →(NATS)→ router →(NATS)→ room#X（携带 gameId=G）
room#X: 帧驱动分发到对局 G → 广播帧 →(NATS)→ 各玩家 lobby →(NATS)→ gateway → client
```

### 6.5 重连与状态恢复（核心）

登录期重分配意味着重连可能落到**另一个 lobby**，故亲和与状态**不能只活在 lobby 内存**：

```
client 掉线 → 重连到任一 gateway → gateway 选 lobby L2 并转发重登录 → L2 校验 token
L2: 从 MongoDB 加载玩家工作副本 + 经 router 向 onlinesvr 查「当前 room 绑定/在线态」
  - 若玩家在对局中(room#X 仍存活) → 重新 BindNode("room", X)，接回原对局
  - 若 room#X 已结束/宕机 → 走对局异常恢复（见 §8）
L2 → onlinesvr: 更新玩家位置（新 gateway/lobby）
```

> **重连宽限**：onlinesvr 在 **5 分钟**无活跃刷新前保留玩家在线条目与绑定；窗口内重连可无缝接回，超窗后按全新登录处理。

---

## 7. 状态与持久化

- **持久化 = MongoDB 直连**：lobby 与各微服务**直接连接 MongoDB**，无 dbsvr 代理进程。
- **lobby**：内存工作副本，分配时加载、关键节点/周期 flush。
- **玩家在线态与当前位置（gateway/lobby/room 绑定）**：由 **onlinesvr** 持有（**纯内存，不落 MongoDB**），重连恢复查 onlinesvr（§6.5）。*不引入 Redis*（✓ 已确认，onlinesvr 已承担在线集/定位职责）。
- **room**：对局过程内存态，开局/结算落库。
- **幂等要求**（沿用 `CLAUDE.md` 工程纪律）：发奖、扣资源、结算、落库必须**幂等**，绝不静默重复；保持内存 / MongoDB / 异步回调一致。

---

## 8. 故障容灾（默认策略，待评审）

| 失效点 | 影响 | 处理 |
|---|---|---|
| gateway 宕机 | 该机上连接断开 | 客户端重连到另一 gateway（连接级 session 重建） |
| lobby 宕机 | 内存工作副本丢失 | 玩家请求超时 → 重连 → 重分配 lobby → 从 MongoDB 恢复（§6.5）；依赖 etcd 心跳剔除 + 重分配 |
| router 宕机 | 无（无状态） | queue-group 其他实例接管；in-flight 消息靠重试 / JetStream 兜底 |
| match 宕机 | 匹配队列丢失 | 队列周期落库或匹配请求走 JetStream 重放；玩家重新匹配 |
| onlinesvr 分片宕机 | 该分片 uid 段在线态丢失 | 一致性哈希仅影响该分片；在线态纯内存不持久，由 gateway 心跳 / 玩家重连重建（5min 宽限内无感）；环变动时少量 uid 迁移 |
| **room 宕机** | **对局中断（最严重）** | **MVP：对局作废 + 补偿**（不做对局级恢复）；后续迭代再做 room 周期 checkpoint 到 MongoDB + 重建恢复。**此权衡需重点评审** |

---

## 9. 与现有代码的映射与迁移

### 9.1 直接复用

- `src/framework/network/*`（packet/message/handshake/acceptor）——gateway 接入层。
- `src/framework/agent`、`src/framework/session`——连接与会话（含 `BindNode`）。
- `src/framework/handler`、`pipeline`、`module`、`application`——分发主干与生命周期。
- `src/framework/cluster/*`（NodeID / discovery / transport / rpc_helpers / ctx）——NATS+etcd 集群层，router 在其上抽象。
- `src/common/*`（config / logger / serialize / syncmap / event / timewheel / taskqueue）。

### 9.2 新建

- `src/servers/lobbysvr`、`matchsvr`、`roomsvr`（填充现有空壳目录）；`src/servers/onlinesvr`（新建目录）。
- `router` 服（无状态转发 + 横切；NATS queue-group）。
- 对应 proto：`lobby.proto` / `match.proto` / `room.proto` / `online.proto`（CS/SC/RPC + msg_id/server_type/handler_method option），重跑 `gen_routes`。
- MongoDB 接入层（直连封装，供 lobby/微服务复用）。
- lobby 的 **Entity-Component 业务框架**（`Player` 实体 + 手写注册的实体组件 + 组件间同步 event 通信；**仅 lobby 用**；与框架 `module` 分属不同层级）。

### 9.3 淘汰 / 清理

- `protocal/world.proto`、`gen/world`、`gen/routes` 中的 world/scene 示例 → 替换为 lobby/match/room 体系。
- `architecture.md` / `network.md` / `cluster.md` 中 worldsvr/scenesvr 示例 → 改写为目标拓扑。
- `src/servers/dbsvr/`、`src/servers/loginsvr/`（占位）→ 删除（无 dbsvr、无独立 loginsvr）。

### 9.4 一并修复的框架现存风险（重构期 backlog）

实读发现、建议借重构窗口修复（详见评审附录，可单列 issue）：

- 优雅停机 **draining 不完整**：`agent.requestsDone` 写入但从不读取，`Stop()` 仅 `agentWg.Wait()`，不等在途请求。
- `math/rand` **并发不安全**：`CallAny` 选节点用全局 `rand.Intn`，建议 `math/rand/v2`。
- `taskqueue` 满 **静默丢任务**：RPC 回调可能丢失致业务卡死，需监控/扩容/降级策略。
- `nats_rpc` `handler` **nil 无防护**：未注入 handler 时运行时 panic。
- `discovery` `dieCh` 信号可能**丢失**（`select default`），故障态不退出。
- TCP Acceptor `listen` 失败**直接 panic**，与 WS Acceptor 返回 error 不一致。
- 每条消息新建 `context.Background()`，连接断开后在途 handler 的 ctx **不感知断连**（无 cancel 传播）。

---

## 10. 待定项 / 评审假设清单

以下是本稿中我采用的默认值（"简单优先"导向），请评审确认或推翻：

1. **不引入 Redis**（✓ 已确认）：在线态/位置由 onlinesvr 内存持有、重连查 onlinesvr，持久数据走 MongoDB。
2. **登录并入 lobbysvr（无独立 loginsvr）**（✓ 已确认）：gateway 仅"选 lobby + 转发 + 记绑定"、不含 token 逻辑；lobby 校验 token（无状态，外部账号系统签发）并加载玩家数据。
3. **单 world / 不做跨服**（✓ 已确认）：worldID 预留不启用，无跨服通信/广播需求。
4. **room 宕机 MVP 不可恢复**（对局作废+补偿），后续再做 checkpoint 恢复。
5. **JetStream 范围**：仅匹配/结算/发奖/跨服通知；实时帧走 core NATS。
6. **router 始终在 lobby→微服务热路径上**（解耦优先，接受一跳）。

---

## 11. 测试策略

- **单元**：各 handler、router 转发与 LB、session 绑定/恢复、NodeID 编解码、幂等逻辑。
- **集成**：以容器起 NATS + etcd + MongoDB，跑端到端 **登录→匹配→进房→对局→重连** 全链路。
- **测试纪律**（遵循 `CLAUDE.md`）：从设计意图/本文档推导用例，**不从实现反推**；约定与实现冲突时先暂停报告。
- 框架现存风险点（§9.4）补回归测试。

---

## 12. 开放问题（评审待答）

**已敲定**（均移入对应章节）：room 一实例承载多局（§3.5）、匹配用 MMR（§3.4）、不做跨服（§4）、onlinesvr 一致性哈希分片 + 纯内存不落库 + 5min 判离线/重连宽限（§3.7）、EC 仅 lobby 用、实体组件间同步 event 通信、初始组件集=背包/好友/邮件/货币（§3.2）、客户端协议**不需要**版本字段。

**暂缓**（不影响骨架，进入实现后再定）：

- room 单实例承载局数上限与开局调度/扩缩容策略（容量规划）。
- MMR 匹配的具体策略（队列分桶、扩容搜索范围、是否支持组队/段位）对 match 状态模型的影响。
