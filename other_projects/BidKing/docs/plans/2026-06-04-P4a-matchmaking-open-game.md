# P4a 设计 Spec：匹配凑桌 → 开局

> 状态：**设计 Spec（brainstorm 已定稿，待出任务计划）** · 日期：2026-06-04 · 适用：`project`（GameServer）
>
> 承接 [`2026-06-04-P4-match-room-auction.md`](2026-06-04-P4-match-room-auction.md)（P4 umbrella 总设计，§7 即本阶段）与 [`2026-06-04-pre-P4-proto-convergence-impl-plan.md`](2026-06-04-pre-P4-proto-convergence-impl-plan.md)（pre-P4 ⑤⑥，PR #19 已合入 main）。沿用 [`2026-06-01-target-architecture-design.md`](../design/2026-06-01-target-architecture-design.md) §5.3/§6.4 与 P3a/P3b 既有设施。
>
> **本阶段切片**：打通 `登录→发起匹配→匹配凑桌→开局→拿到 room 绑定` 的前半弧。**不含**出价/竞拍/结算（P4b）与重连接回/补偿（P4c）。出局后 roomsvr 仅建骨架。本 Spec 评审通过后走 `writing-plans` 产出 `…-P4a-…-impl-plan.md`。

---

## 1. 目标 / 范围

### 1.1 范围内

- **matchsvr（新建，有状态，单 goroutine 主循环）**：内存 MMR 匹配队列；从 JetStream 消费匹配请求；凑齐 N 个 MMR 相近的 bidder → 生成 `gameId` → 经 router 一致性哈希(key=gameId) 选 room 开局 → 经 router 回告各玩家所属 lobby `{room_node_id, game_id}`。
- **roomsvr（新建，有状态，帧驱动，P4a 仅骨架+开局）**：接受开局 RPC，按 `gameId` 建拍卖局对象（participants / 拍品占位 / 初始倒计时），tick 主循环骨架（**不含出价/结算**，P4b 填）。
- **lobby（扩 `Runtime`/`LobbyHandler`）**：`CS_StartMatch` 发起入口（校验可匹配 + 读 rating）→ 经 router 把匹配请求交 router 发布到 JetStream；开局回告 `GameStarted` 处理（置内存 room 亲和 + 经 router `BindRoom` 同步 online + 推 `SC_MatchFound`）。新增最小持久 **Rating 组件**。
- **router（扩）**：新增 `RouterHandler.publishmatch`——router 持 JetStream **publisher**，把匹配请求发布到 `MATCH` stream（JetStream 摄入点）。`ROUTING_CONSISTENT_HASH`/`ROUTING_DIRECT` resolve 复用现状。
- **online（扩，跨切 §5.1）**：`OnlineEntry` 加 `room_node_id`+`game_id`；新增 `RPC_BindRoom`/`RPC_UnbindRoom`（`OnlineHandler.bindroom`/`unbindroom`，绝对覆盖写）。P4a 接 `bindroom`；`unbindroom` handler 一并建好，wiring 留 P4b 结算清亲和用。
- **JetStream facility（跨切 §5.3，首次引入）**：框架级接入设施（`MatchQueue` 接口 + 真实 `nats.go/jetstream` 适配器 + 内存 fake）。router 用其 publisher、matchsvr 用其 durable consumer。
- **proto**：新建 `match.proto`、`room.proto`(最小 OpenGame)；扩 `lobby.proto`(StartMatch/MatchFound/gamestarted)、`online.proto`(room 绑定)；重跑 `gen_routes`。

### 1.2 范围外（留 P4b/P4c）

出价/竞拍逻辑、结算扣币发物、持久幂等发放设施（④a+①）、`UnbindRoom` wiring、重连接回、room 死/作废补偿、MMR 窗口放宽/超时、负载感知选址、赛后改分（Rating 本阶段只读）。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| **P4a-1** | JetStream seam（无 Docker） | **接口 `MatchQueue` + 真实 nats.go/jetstream 适配器（编译验证）+ 内存 fake（单测）** | 沙箱无 Docker 跑不起带 JetStream 的 NATS（同 P2/P3 集成测试只编译验证）。唯一真实现走 JetStream，不引入会误导的降级路径；lobby/match 全程对接口编程。真实适配器 `go vet` 编译验证，留待 JetStream 环境实跑（§9）。 |
| **P4a-2** | 跨服寻址 | **全部经 routersvr 转发**（统一横切中枢） | lobby→match 经 router 发布（router 持 JetStream publisher）；match→room 经 router `CONSISTENT_HASH`(key=gameId)；match→lobby 经 router `DIRECT`(key=lobbyNode)；lobby→online 经 router `CONSISTENT_HASH`(key=uid)。最大复用现成 resolve（`CONSISTENT_HASH`/`DIRECT` 已实现），**不需实现 `ROUTING_ANY`**，jumphash 留在 router。 |
| **P4a-3** | room 选址 | **一致性哈希 by gameId**（router `CONSISTENT_HASH` resolve 复用 jumphash） | match 给每局生成 `gameId`，经 router 按 `gameId` 一致性哈希落 room；同 `gameId` 稳定落同一 room（开局重投幂等友好）。room 在 `OpenGame_Rsp` **回带自己的 NodeID**，match 据此回告 lobby。 |
| **P4a-4** | MMR 来源 | **最小持久 Rating 组件**（`mmr`，默认 1000，本阶段只读） | P3 无 rating 组件。加最小 Component（与 EC 模式一致），`buildPlayer` 缺省 seed 1000，lobby 发起匹配时读取填入请求。为赛后改分留扩展点，本阶段**不写改分逻辑**。 |
| **P4a-5** | room 亲和存储 | **Player 内存运行态字段（不持久到 PlayerDoc）；权威重连源是 online** | §9 重连读 online 拿 room 绑定、doc 另行加载。持久到 doc 会与 online TTL/结算清亲和产生双源不一致与陈旧脏数据（结算后崩溃→doc 说在局、online 已清）；online 有 5min TTL 自愈，doc 不会。亲和只活在当前会话内存 + 同步 online。 |
| **P4a-6** | matchsvr 并发 | **单 goroutine 主循环（仿 lobby `Runtime`）** | 共享态=MMR 队列，触发点=JetStream 消费回调 + 凑桌 + 后续超时 tick 多点；单循环串行化最干净（仿 lobby）。off-loop 编排（CallVia router 调 room/lobby）回调经 `Submit` 回环（仿 lobby `registerOnline`/`inflight`）。 |

> 沿用 umbrella/全局既定：统一 NATS + etcd + MongoDB；集群 RPC 发送端硬编码 `proto.Marshal`，后端服务序列化器用 protobuf；存储用 BSON；gate 推送方向 body 用 client 序列化器。matchsvr/roomsvr 均为 backend 节点（不 `Frontend()`、不接客户端连接），仿 onlinesvr 装配。

---

## 3. 架构与寻址

```
client ──CS_StartMatch──► gate ──转发(已绑 lobbysvr)──► lobby(L)
                                                          │ ① 校验 roomAffinity==nil + 读 mmr
                                                          │ ② CallAnySync(routersvr,"RouterHandler.publishmatch",MatchRequest)
                                                          ▼
                                                      router ── JetStream publisher ──► [MATCH stream]
                                                          ▲                                   │ durable consumer "matchsvr"(queue group)
                                          ③ SC_StartMatch{已入队}                            ▼
                                                                                     matchsvr(M, 单 goroutine 主循环)
                                                                                          │ ④ dedup(uid,reqId)→入队→ack
                                                                                          │ ⑤ 凑齐 N 个窗口内 → gameId=G
                                          off-loop 编排 goroutine ◄───────────────────────┘
                                                  │
            ⑥ CallViaSync(CONSISTENT_HASH,key=G,"RoomHandler.opengame") ──► router ──► roomsvr(R) 建局; Rsp 回带 room_node_id=R
                                                  │
            ⑦ 对每 participant: CallViaSync(DIRECT,key=lobbyNode,"LobbyHandler.gamestarted"{uid,G,R}) ──► router ──► lobby
                                                                                          │ ⑧ roomAffinity={R,G}(内存)
                                                                                          │ ⑨ CallViaSync(CONSISTENT_HASH,key=uid,"OnlineHandler.bindroom")→online
                                                                                          │ ⑩ PushToClient SC_MatchFound{R,G}→gate→client
```

**寻址表（全部经 router）**：

| 跨服 hop | 经 router 方式 | route | 复用现状 |
|---|---|---|---|
| lobby → match 请求 | `cls.CallAnySync(routersvr, …, MatchRequest)` | `RouterHandler.publishmatch` | **新增**（router JetStream publisher） |
| match → room 开局 | `routerclient.CallViaSync(CONSISTENT_HASH, gameId, …)` | `RoomHandler.opengame` | 复用 `CONSISTENT_HASH` resolve |
| match → lobby 回告 | `routerclient.CallViaSync(DIRECT, lobbyNode, …)` | `LobbyHandler.gamestarted` | 复用 `DIRECT` resolve |
| lobby → online 绑定 | `routerclient.CallViaSync(CONSISTENT_HASH, uid, …)` | `OnlineHandler.bindroom` | online 既有模式 |

- **JetStream 摄入点在 router**：lobby 不持 JetStream 句柄；router（任一无状态实例）收 `publishmatch` → 经 `MatchQueue.Publish` 写 `MATCH` stream → 成功返回 ack。durability 边界 = router 成功发布到 stream。
- **match→lobby 改同步 RPC（经 router）**：相比 fire-and-forget，match 拿到每个 lobby 的 ack，失败可重试/记录——消除回告丢失隐患。

---

## 4. 组件与职责

### 4.1 matchsvr（新建）

仿 onlinesvr 的 backend 装配（main/module/internal）+ 仿 lobby 的单 goroutine `Runtime`：

- **`Runtime`（单 goroutine 主循环，零锁）**：`Submit(fn)` 进环；`queue` 持 MMR 队列状态；`tw` timewheel（P4a 仅骨架，超时/放宽留后续）。
- **JetStream 消费 goroutine**：`MatchQueue.Consume(durable="matchsvr", handler)`；handler 收 `MatchRequest` → `Submit{ dedup by (uid,reqId)；非重复则入队；扫描凑桌 }` → **入队成功后 ack**（遵循 umbrella §5.3）。
- **凑桌（主循环内）**：单队列，每次入队扫描「N 个落在 MMR 窗口 W 内」（N、W 为常量，N 默认 2）；够则成桌、生成全局唯一 `gameId`、把 `{participants, gameId}` 交 off-loop 编排；不够则挂起。**不做窗口放宽/超时**（标注延后）。
- **off-loop 编排 goroutine**（仿 lobby `registerOnline`）：⑥ `CallViaSync(CONSISTENT_HASH, gameId, RoomHandler.opengame)` 拿 `room_node_id=R`；⑦ 对每 participant `CallViaSync(DIRECT, lobbyNode, LobbyHandler.gamestarted)`。失败处理见 §6。
- **`MatchHandler`**：本阶段 matchsvr 不直接收集群 RPC（请求走 JetStream 消费）；如需健康/管理 RPC 后续加。

### 4.2 roomsvr（新建，仅骨架+开局）

仿 onlinesvr backend 装配 + 帧驱动单 goroutine tick 主循环：

- **`Game`（拍卖局对象）**：`{gameId, participants[]{uid, lobbyNode}, item(占位), countdownSec, highestBid/highestBidder(P4b 用，先零值)}`。多 `gameId` 并存隔离（map[gameId]*Game）。
- **`RoomHandler.opengame(ctx, *RPC_OpenGame_Req) (*RPC_OpenGame_Rsp, error)`**：按 `gameId` 建局（已存在则幂等返回，§6）；记 participants/拍品/倒计时；`Rsp{code, room_node_id=self}`。
- **tick 主循环骨架**：timewheel 推进各局倒计时；**P4a 不结算**（到点仅日志/留空，P4b 填定赢家+回告）。

### 4.3 lobby（扩展）

- **`CS_StartMatch` handler**（薄壳 + `Submit` + `ErrDeferredReply`，仿 `Purchase`）：主循环里 `p := rt.Player(uid)`；校验已加载、`p.roomAffinity==nil`（未在局中）；读 `p.Rating().MMR()`；off-loop `CallAnySync(routersvr, RouterHandler.publishmatch, MatchRequest{uid, reqId, mmr, lobbyNode})`；回 `SC_StartMatch{code}`（0=已入队 / 1=已在局中 / 负=失败）。`reqId` 由 lobby 生成（幂等键，§6）。
- **`GameStarted` handler**（match→lobby 回告，经 router DIRECT 到本节点；同步 RPC，返回 ack）：主循环里 `p.setRoomAffinity({R,G})`（内存）；off-loop `CallViaSync(CONSISTENT_HASH, uid, OnlineHandler.bindroom, {uid,R,G})` 同步 online；`PushToClient(uid, SC_MatchFound{R,G})`（复用 P3b `presence.Push`→`GateHandler.pushtoclient`）。返回 `Rsp{code}` 让 match 知道已落地。
- **Rating 组件**（最小 Component，仿 Currency 的结构但更薄）：`Name="rating"`/`Field="rating"`；state BSON `{mmr int64}`；实现 `Component` 接口；`buildPlayer` 缺省 seed `mmr=1000`；本阶段只读（无 mutator，赛后改分留扩展点）。加入 `PlayerDoc.Rating`，`buildPlayer` 注册。
- **`Player.roomAffinity *roomBinding`**（内存运行态字段，`{roomNodeID, gameId}`，不持久）：登录加载置 nil；`GameStarted` 置值；P4b 结算清空。`CS_StartMatch` 据此判可匹配。

### 4.4 router（扩展）

- **`RouterHandler.publishmatch(ctx, *MatchRequest) (*RPC_PublishMatch_Rsp, error)`**：经框架 `MatchQueue.Publish(subject="match.request", MatchRequest)` 发布到 `MATCH` stream；成功 `Rsp{code:0}`。router 持 `MatchQueue` publisher（module 装配时建）。
- `Resolve`（`CONSISTENT_HASH`/`DIRECT`）不动；`ROUTING_ANY` 仍留桩（本阶段不需要）。

### 4.5 online（扩展，跨切 §5.1）

- `OnlineEntry` 加 `room_node_id`(string)+`game_id`(string)。
- `RPC_BindRoom_Req{uid, room_node_id, game_id}`/`Rsp{code}`（`OnlineHandler.bindroom`）：在线条目上**绝对覆盖写** room 绑定字段（条目不在线则返回 code≠0/记录）；与 5min TTL 协同（条目过期则绑定一并失效）。
- `RPC_UnbindRoom_Req{uid}`/`Rsp{code}`（`OnlineHandler.unbindroom`）：清 room 绑定字段。**handler 本阶段建好，wiring 留 P4b**。
- onlinesvr 仍纯内存目录，不落库、不订阅业务。

### 4.6 JetStream facility（框架级，跨切 §5.3）

- **接口 `MatchQueue`**（或并列 `jetstream` 子包，沿 §5.3）：`Publish(ctx, subject, msg proto.Message) error` / `Consume(ctx, durable string, handler func(ctx, data []byte) error) error`（handler 返回 nil 即 ack，err 不 ack 留重投）。
- **真实适配器**：`nats.go/jetstream`（v1.52.0，module cache 已有）——建/确保 `MATCH` stream（subject `match.request`）、durable consumer、`Consume`/`Ack`。**编译验证（`go vet`），沙箱不实跑**（无 Docker，同 P2/P3 集成测试欠账）。
- **内存 fake**：单测用——`Publish` 入内存 slice，`Consume` 投递给 handler，可注入重复投递验幂等。
- 用途边界（§5.3）：仅**匹配请求**走 JetStream（P4a）；拍卖广播/实时态走 core NATS（P4b）。

---

## 5. 端到端数据流（P4a happy path）

```
登录(已有)  client→gate→lobby L: 校验token+加载玩家(含 rating seed 1000)+注册 online; gate BindNode("lobbysvr",L)
匹配发起    CS_StartMatch→gate→lobby L: 校验 roomAffinity==nil + 读 mmr
            → off-loop CallAnySync(routersvr,"RouterHandler.publishmatch",{uid,reqId,mmr,lobbyNode=L})
            → router 经 MatchQueue.Publish 写 MATCH stream → 回 ack → lobby 回 SC_StartMatch{0}
凑桌        matchsvr durable 消费 MatchRequest → Submit{dedup(uid,reqId)→入队→扫描} → ack
            凑齐 N 个 MMR 窗口内 → gameId=G → 交 off-loop 编排
开局选址    off-loop: CallViaSync(CONSISTENT_HASH,key=G,"RoomHandler.opengame",{G,participants,item,countdown})
            → router jumphash(rooms,G)=R → 转 R → R 建局 → Rsp{code:0, room_node_id:R}
回告        off-loop: 对每 participant CallViaSync(DIRECT,key=lobbyNode,"LobbyHandler.gamestarted",{uid,G,R})
绑定        各 lobby: roomAffinity={R,G}(内存)
            + off-loop CallViaSync(CONSISTENT_HASH,key=uid,"OnlineHandler.bindroom",{uid,R,G}) → online 写 room 绑定
            + PushToClient SC_MatchFound{R,G} → gate → client
```

---

## 6. 错误处理 / 边界 / 幂等（CLAUDE.md 工程纪律）

### 6.1 校验

- `CS_StartMatch`：Player 已加载（否则 code<0）、未在局中（`roomAffinity!=nil`→code:1）、mmr 合法。
- matchsvr 消费：`MatchRequest` 字段非空、`reqId` 非空；room 列表空（router `CONSISTENT_HASH` resolve 无成员）→ 开局 CallVia 失败 → 重排重试（见 6.3）。
- `RoomHandler.opengame`：participants 非空、`gameId` 非空、未越界。

### 6.2 幂等

| 路径 | 幂等键 | 机制 |
|---|---|---|
| 匹配请求（JetStream 重投） | `(uid, reqId)` | matchsvr 消费侧去重 set；重复不重复入队，仍 ack |
| 开局（match 重排重发 / 同 gameId） | `gameId` | room 建局检测已存在则幂等返回同 `room_node_id` |
| BindRoom 同步 online | `uid`（绝对写） | 覆盖式写 room 绑定，重复同值安全 |
| GameStarted 置亲和 | `uid`+`{R,G}`（绝对写） | 置内存亲和为绝对值，重复幂等 |

> `(uid,reqId)` 去重为 matchsvr **会话内内存态**（matchsvr 纯内存，无持久）——足够覆盖 JetStream 重投；跨 matchsvr 重启的持久去重不在 P4a（结算侧 ④a 的持久幂等是 P4b）。

### 6.3 失败路径

- **开局 CallVia(room) 失败**（room 不可达 / resolve 空）：participants 已 ack，JetStream 不再投 → **放回内存队列下轮重试**（或按策略丢弃并记录）；显式日志（强类型字段：gameId/参与者数/err）。
- **GameStarted CallVia(lobby) 失败**（某 lobby 不可达）：room 已建局、该玩家 lobby 未绑亲和/未写 online。本阶段**记录 + 继续**（room 仍持该 participant）；缺失绑定由 P4c 重连兜底。标注为已知边界，P4a 不加重投闭环。
- **ack 边界**：入队即 ack ⇒ matchsvr 崩溃丢「已入队未成桌」请求，靠客户端重发 + `(uid,reqId)` 去重兜底——MVP 取舍（遵循 §5.3），显式标注。

### 6.4 并发

matchsvr/roomsvr 共享态全部经各自单 goroutine 主循环串行，零锁；off-loop 网络 IO 回调经 `Submit` 回环（仿 lobby `inflight` 计数，停机 drain 沿用既有模式）。

---

## 7. proto / gen_routes 变更

- **`match.proto`（新建）**：
  - `MatchRequest{ int64 uid; string req_id; int64 mmr; string lobby_node_id }`（JetStream payload，无 msg_id）。
  - `RPC_PublishMatch_Rsp{ int32 code }`（`publishmatch` 返回；req 直接用 `MatchRequest`，route=`RouterHandler.publishmatch`，无 msg_id）。
  - `RPC_GameStarted_Notify`/`_Rsp{ int64 uid; string game_id; string room_node_id } / { int32 code }`（route=`LobbyHandler.gamestarted`，server-server，无 msg_id）。
- **`room.proto`（新建，最小）**：
  - `Participant{ int64 uid; string lobby_node_id }`。
  - `RPC_OpenGame_Req{ string game_id; int32 item_id; int32 countdown_sec; repeated Participant participants } / _Rsp{ int32 code; string room_node_id }`（route=`RoomHandler.opengame`，无 msg_id）。
  - （P4b 扩 `CS_Bid`/`SC_AuctionState`/`SC_AuctionResult`。）
- **`lobby.proto`（扩）**：
  - `CS_StartMatch{}`（msg_id 2034，server_type=`lobbysvr`，handler=`LobbyHandler.startmatch`）。
  - `SC_StartMatch{ int32 code }`（msg_id 2035）。
  - `SC_MatchFound{ string room_node_id; string game_id }`（msg_id 2036，**仅 msg_id**，push 方向）。
- **`online.proto`（扩）**：`OnlineEntry` 加 `room_node_id`/`game_id`；`RPC_BindRoom_Req/Rsp`、`RPC_UnbindRoom_Req/Rsp`（`OnlineHandler.bindroom`/`unbindroom`，无 msg_id）。
- 重跑 `gen_routes`。**msg_id 2034-2036 续 lobby 2000 段**；server-server RPC 走字符串 route 无 msg_id。**matchsvr/roomsvr 的 NodeID serverTypeID + conf/*.yaml 在 plan 核定**（roomsvr=7 已见测试 `1.7.3`；matchsvr 待核既有分配，新增 `conf/match.yaml`/`conf/room.yaml` 仿 `conf/online.yaml`）。

---

## 8. 测试策略（TDD，从本 Spec/设计意图推导）

- **单元**：
  - **matchsvr**：MMR 凑桌（窗口内成桌 / 不足挂起 / 跨窗口不成桌）、`(uid,reqId)` 去重、gameId 唯一性、off-loop 编排（fake cluster 验 CallVia room CONSISTENT_HASH + 各 lobby DIRECT 扇出、room_node_id 回传链路）。
  - **roomsvr**：`opengame` 建局、同 gameId 幂等、多 gameId 隔离、participants 记录、tick 骨架推进倒计时（不结算）。
  - **lobby**：`CS_StartMatch` 校验（未加载 / 已在局中 / 正常入队发布）、`GameStarted` 置亲和+BindRoom 同步+SC_MatchFound 推送、Rating seed/读取。
  - **online**：`OnlineEntry` room 绑定读写、`bindroom`/`unbindroom` 绝对写幂等。
  - **JetStream facility**：内存 fake 的 publish-consume-ack + 重投幂等；真实适配器 `go vet` 编译验证。
  - **router**：`publishmatch` 经 `MatchQueue.Publish`（fake）发布、resolve 复用回归。
- **集成（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+JetStream+etcd+MongoDB）**：凑桌→开局→lobby 拿 room 绑定→online `Query` 可见 room 绑定。
- **无 Docker（umbrella D10）**：集成测试只编译验证；JetStream 真实语义实跑留 JetStream 环境（§9）。
- **测试纪律**：约定/文档与实现冲突先暂停报告；后端服务集成测试放各服务自身包内（Go internal 可见性）。

---

## 9. 风险 / 开放问题

- **JetStream 首引、沙箱不可实跑**：真实适配器只能编译验证；durable consumer / ack / 重复投递 / stream 建立语义需在 JetStream 环境专门验证（同 umbrella §13）。MVP 只用核心 publish-consume-ack，不上保留/重放策略。
- **ack-after-enqueue 的 durability 边界**：matchsvr 纯内存，崩溃丢「已入队未成桌」请求（§6.3）；产品上靠客户端重发。
- **GameStarted 回告失败 → 缺失绑定**：P4a 仅记录，靠 P4c 重连兜底；若 P4c 前需更强保证，可让 room 周期性 re-notify 或由 match 写 online（本阶段不做）。
- **gameId 全局唯一**：多 matchsvr 实例共享 durable consumer，各自成桌——`gameId` 须跨实例唯一（plan 定生成方案：matchNodeID+单调计数 或 uuid）。
- **room 选址成员集变动**：jumphash(rooms,gameId) 在 room 扩缩容期映射会变；P4a 单局开局一次、结果由 online 存权威，重连读 online 不重算，故无影响（umbrella §5.1）。
- **proto/gen_routes 体量 + rebase**：新增 match/room + 扩 lobby/online，注意与并行落地 rebase（P1~P3 经验）。

---

## 10. 交付物速查

| 模块 | 新建/扩充 | 关键验收 |
|---|---|---|
| **matchsvr** | 新建：main/module/`Runtime`(单 goroutine)/凑桌/off-loop 编排/JetStream 消费 | ≥N 客户端发起匹配→凑齐一桌→选 room 开局→各 lobby 拿 `{R,G}` |
| **roomsvr** | 新建：main/module/`Game`/`opengame`/tick 骨架 | `opengame` 建局、多 gameId 隔离、Rsp 回带 room_node_id |
| **lobby** | 扩：`CS_StartMatch`/`GameStarted` handler、Rating 组件、`roomAffinity` 内存字段 | 校验可匹配→发布；回告置亲和+BindRoom+SC_MatchFound |
| **router** | 扩：`publishmatch` + JetStream publisher | lobby→match 请求经 router 发布到 MATCH stream 不丢 |
| **online** | 扩：`OnlineEntry` room 字段 + `bindroom`/`unbindroom` | `bindroom` 绝对写、`Query` 可见 room 绑定 |
| **JetStream facility** | 新建：`MatchQueue` 接口 + 真实适配器 + 内存 fake | fake publish-consume-ack 幂等；真实适配器编译验证 |
| **proto** | `match.proto`/`room.proto`(min)；扩 `lobby`/`online`；gen_routes | 路由表生成、msg_id 2034-2036 |

---

## 附录：工作流约定

- 本 Spec 评审通过后走 `writing-plans` 产出 `docs/plans/2026-06-04-P4a-…-impl-plan.md`（每步含失败测试+实现+验证），再用 `subagent-driven-development` 逐任务 TDD。
- 核心任务 spec+质量双评审 + 整支 `-race` 终审（P2/P3a/P3b 反复证明 verbatim 计划代码会偏离/不全，终审 + `-race` 必做）。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；合入前先 rebase 最新 origin/main。
- 文档维护：对 `architecture.md`/`cluster.md`/`development.md` 有影响的改动按 CLAUDE.md 同步（matchsvr/roomsvr 目录、JetStream 设施、新 conf）。
