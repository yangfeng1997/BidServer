# P4b 设计 Spec：对局（竞拍）+ 结算

> 状态：**设计 Spec（brainstorm 已定稿，待出任务计划）** · 日期：2026-06-04 · 适用：`project`（GameServer）
>
> 承接 [`2026-06-04-P4-match-room-auction.md`](2026-06-04-P4-match-room-auction.md)（P4 umbrella 总设计，§8 即本阶段）与 [`2026-06-04-P4a-matchmaking-open-game.md`](2026-06-04-P4a-matchmaking-open-game.md)（P4a 匹配凑桌→开局，PR #20/#21 已合入 main）。沿用 P3a/P3b 既有设施（EC 组件 / op-dedup / mailbox / flush 加固）。
>
> **本阶段切片**：在 P4a 打通的 `发起匹配→凑桌→开局→room 绑定` 之上，实现 **对局竞拍业务 + 结算闭环**。**不含** 5min 重连接回 / room participant 改投（P4c）。本 Spec 评审通过后走 `writing-plans` 产出 `…-P4b-…-impl-plan.md`。

---

## 1. 目标 / 范围

### 1.1 范围内

- **roomsvr 竞拍状态机 + tick 结算**：在 P4a `Game`/tick 骨架上加 `HighestBid/HighestBidder/Currency/closed`；timewheel 倒计时到点 settle 定赢家；多 `gameId` 并存隔离。
- **出价路径 A**（umbrella D3）：`CS_Bid → gate → lobby（亲和+CanAfford 校验）→ router DIRECT → room（记价/更新最高价/广播）`。
- **结算闭环**：room 定赢家 → 经 router 回告各 participant lobby → 赢家 `Currency.Spend + Bag.Add` → 清亲和 + **`UnbindRoom` wiring**（P4a 留的接线）→ 推 `SC_AuctionResult`。
- **持久幂等发放设施**：把 P3b 仅会话内的 `opDedup` 环**持久化进各组件 BSON 子文档**（Currency/Bag），随既有 absolute-`$set` `FlushField` 原子落库。结算 `opID=gameId`、mail-claim ① `opID=mailID` 共用同一设施。**① mail-claim 重排** `grant→persist→mark-claimed`。
- **离线消息机制**（新 collection）：玩家离线时收到的状态变更消息（本期=结算扣币发物）落 `offline_messages`，登录加载后重放。
- **D6 大厅消费禁令**：对局期 lobby 拒绝大厅消费（`Purchase`）。
- **matchsvr 最小封堵**：① 匹配等待超时 reap；② `pendingUids` 封"成桌→回告未落定"的残余双发窗口。
- **proto 扩 + gen_routes**；room→lobby 广播/结算的 server-server RPC。

### 1.2 范围外（留 P4c / 后续）

5min 重连接回 / room participant 的 lobbyNode 改投、room 宕机对局级 checkpoint 恢复、escrow 冻结子态、JetStream 结算事件 durable 消费（见 §9.4）、anti-snipe 续时 / 多拍品 / 暗拍 / 段位匹配、MMR 窗口渐进放宽（"重排"，本期超时仅 cancel 语义）。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| **P4b-1** | P4a 残余项 | **纳入本期最小封堵**：① 匹配等待超时 reap、② `pendingUids` 封残余双发窗口 | 残余双发会致同 uid 进两桌/退化局，属 P4a 已知正确性缺口，与本期 room/结算同属对局弧，顺手闭合。 |
| **P4b-2** | room 死/对局异常补偿 | **P4b 轻量作废 + 离线投递**（重连接回仍 P4c） | 你的 brief 列为 P4b 决策；no-escrow 下竞价期零扣减，"退还出价"实为空操作，真实补偿=清亲和让玩家回大厅 + 赢家离线时结算改投离线消息。robust room-death 探测（心跳）留 P4c。 |
| **P4b-3** | 持久幂等发放设施 | **持久化组件 `opDedup` 环进 BSON 子文档**，随 absolute-`$set` 原子落库 | 无 Mongo 事务可用（`src/common/mongo` 仅单文档 `FindOneAndUpdate`）。把现有内存环持久化是最小改动：余额/道具 + opID 同一 `$set` 原子；分组件部分 flush 各自独立去重；贴 EC 模型，无新集合无事务。 |
| **P4b-4** | 离线赢家"扣币"难题 | **离线消息机制**（新 `offline_messages` collection，每玩家一 doc，登录重放） | mailbox 只能发物不能扣币；给未加载玩家走 DB 增量扣币会破 P3b「players 文档单写者」不变式。离线消息=多写者 append-only 独立集合，登录由 owner lobby（单写者）重放 `Spend+Add`，**单写者安全 + 离线投递 + 扣币齐全**。与持久 ops 组合消灭跨路双发。 |
| **P4b-5** | 对局消息路径 | **A：经 lobby 中转**（umbrella D3，不变） | 出价必经 lobby 校验/扣 Currency，对局无法与 lobby 解耦；广播 room→各 lobby→`PushToClient`。 |
| **P4b-6** | 货币安全 | **对局期禁大厅消费 + bid `CanAfford` + 结算 `Spend`，免 escrow**（umbrella D6，不变） | 单一对局亲和 + 禁大厅消费 ⇒ 余额不被侵蚀 ⇒ `CanAfford(我的出价)` ⇒ 结算 `Spend(最高价)` 必成。 |
| **P4b-7** | JetStream 结算事件 | **不用 JetStream**，以 direct-RPC-retry + 持久 ops + offline_messages 等价实现"不丢不重" | lobby 已定不持 JetStream（P4a）；无干净 per-uid 消费者映射；room 死本按 D4 作废，durable 跨 room 崩溃无意义。有据偏离 umbrella §5.3，见 §9.4。 |
| **P4b-8** | 竞拍厚度 | **极薄**：单拍品 / 倒计时 / 严格更高出价 / 到点定赢家 / 无 anti-snipe / N=2（umbrella D2，不变） | 价值在管线，不在玩法纵深。 |

> 沿用全局既定：统一 NATS + etcd + MongoDB；集群 RPC 发送端硬编码 `proto.Marshal`，后端服务序列化器用 protobuf；存储用 BSON；客户端全链路 proto（pre-P4 决策 C）；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 验证的并发事实（动手前已读码核实）

- **timewheel 回调在 roomsvr 主循环 goroutine 执行**：roomsvr 是**帧驱动**（`Runtime.loop` 在 `case <-ticker.C:` 调 `tw.Advance()`），而 `TimeWheel.Advance()` 先释放锁、再在**调用方 goroutine** 内联 `t.fn()`（`timewheel.go:164-166`）。故 `OpenGame` 注册的 settle 回调**在主循环串行执行**，可零锁直接改 `Game` 态。**与 onlinesvr 不同**——onlinesvr 用自驱动 `tw.Start()`，回调在内部 goroutine（P2 代次竞态根源）；roomsvr 无此问题。off-loop 网络 IO 仍走 `go`+`Submit`+`inflight`/`drain`（仿 matchsvr）。
- **roomsvr `Runtime` 当前无 `cls`**：`main.go` 建了 `cls` 但只传 `NodeID`。本期 `RuntimeConfig` 加 `Cluster cluster.Cluster`（仿 matchsvr），供 room→lobby 广播/结算。
- **`src/common/mongo` 无事务**：仅 `FindByID`/`UpsertSetByID`/`InsertOne`/`Find`/`FindOneAndUpdate`（单文档原子）。故持久幂等走"子文档原子 `$set`"，离线消息走"原子 `$push`/`$pull`"，均无需事务。
- **mailbox 既有原子 claim**（`FindOneAndUpdate{_id,to,claimed:false}→$set claimed:true`）；单写者模型下（mail.to=uid 仅其 owner lobby 领取）跨写者并发 claim 不会发生，故 ① 重排为"读→grant→persist→mark"安全。

---

## 4. 架构与寻址

```
出价  CS_Bid{game_id,amount} ─► gate(已绑lobby)转发 ─► LobbyHandler.bid:
        Submit→ p.RoomAffinity!=nil && affinity.gameID==game_id && CanAfford(affinity.currency, amount)
          ├ 不满足 → 回 SC_Bid{code}（零副作用，不触 room）
          └ 满足   → off-loop CallViaSync(DIRECT, affinity.roomNodeID, "RoomHandler.bid")
                     → room 记价+更新最高价+广播 → 回 {code,highest} → lobby 回 SC_Bid{code,highest}

广播  room ─► 每 participant: Cast(DIRECT, lobbyNode, "LobbyHandler.auctionstate"{uid,state})
        ─► lobby PushToClient(uid, SC_AuctionState)  [Cast 可丢，客户端下次刷新兜底]

结算  room settle(主循环, countdown 到点): closed=true; winner=HighestBidder(0=流拍)
        ─► off-loop CallViaSync(DIRECT, lobbyNode, "LobbyHandler.settle"{uid,gameId,winner,price,currency,itemId}) 给每 participant
           (inflight/drain + 失败重试 at-least-once；据 lobby ack 停重试)
        LobbyHandler.settle(Submit):
          · 清亲和 + off-loop CallViaSync(CONSISTENT_HASH,uid,"OnlineHandler.unbindroom") + push SC_AuctionResult
          · uid==winner:
              p := Player(uid)
              ├ 在线 → Spend(ops=gameId,currency,price) + Bag.Add(ops=gameId,itemId) → flush(after→ack)
              └ 离线 → $push offline_messages[uid]: {type:settle, opID=gameId, price,currency,itemId} → (push 落定→ack)
          · 回 ack（**持久落库后才 ack**，见 §6.6）
```

**寻址表（全部经 router，复用 P4a resolve）**：

| 跨服 hop | 经 router 方式 | route | 复用现状 |
|---|---|---|---|
| lobby → room 出价 | `CallViaSync(DIRECT, roomNodeID, …)` | `RoomHandler.bid` | **新增 handler**，复用 DIRECT resolve |
| room → lobby 广播 | `Cast(DIRECT, lobbyNode, …)` 或 `CallVia` | `LobbyHandler.auctionstate` | **新增 handler** |
| room → lobby 结算回告 | `CallViaSync(DIRECT, lobbyNode, …)` | `LobbyHandler.settle` | **新增 handler** |
| lobby → online 清绑定 | `CallViaSync(CONSISTENT_HASH, uid, …)` | `OnlineHandler.unbindroom` | P4a 已建 handler，**本期接线** |
| lobby → client 推送 | `presence.Push`→`GateHandler.pushtoclient` | （msg_id） | 复用 P3b/P4a 推送 |
| matchsvr → lobby 超时回告 | `Cast(DIRECT, lobbyNode, …)` | `LobbyHandler.matchtimeout` | **新增 handler** |

---

## 5. 组件与职责

### 5.1 roomsvr（竞拍状态机）

- **`Game` 扩字段**：`HighestBid int64` / `HighestBidder int64`(uid，0=无) / `Currency string` / `closed bool`。
- **`RPC_OpenGame_Req` 加 `currency`**：matchsvr 在 `openGameViaRouter` 设（MVP 默认常量如 `"gold"`），room 存入 `Game.Currency`。
- **生命周期**（全主循环串行）：`OpenGame(Bidding, AfterFunc(countdown, settle))` → `Bid(!closed && uid∈participants && amount>HighestBid 严格)` 更新最高价+广播 → `settle(closed=true, winner=HighestBidder)` off-loop 回告。首价任意正数（起拍 0），无 anti-snipe。
- **`RoomHandler.bid`**（薄壳 + Submit + ErrDeferredReply）：校验 → 更新 → off-loop 广播 → 回 `{code, highest_bid, highest_bidder}`。
- **`Runtime` 扩**：加 `cls` + off-loop `inflight`/`drain`（仿 matchsvr）+ 广播/结算 hook（默认接真实 router，测试可替换 fake）。

### 5.2 lobby（出价编排 + 结算落地 + 持久 ops + 离线消费）

- **`LobbyHandler.bid`**（薄壳，仿 `Purchase`）：主循环校验亲和（非空 + gameID 匹配）+ `CanAfford(affinity.currency, amount)` → off-loop `CallViaSync(DIRECT, roomNodeID, RoomHandler.bid)` → 回 `SC_Bid{code, highest_bid}`。
- **`LobbyHandler.auctionstate`**（room→lobby Cast）：主循环 `PushToClient(uid, SC_AuctionState)`。
- **`LobbyHandler.settle`**（room→lobby CallViaSync）：见 §4 / §6.2。
- **`LobbyHandler.matchtimeout`**（matchsvr→lobby Cast）：push `SC_MatchTimeout`。
- **`roomBinding` 加 `currency`**：`GameStarted` 回告（`RPC_GameStarted_Req` 加 `currency`）置入，出价 `CanAfford` 用，避免在 lobby 硬编码币种。
- **持久 ops**：`CurrencyState`/`BagState` 加 `Ops []string`；`opDedup` 加 `snapshot()`/`loadFrom()`；`Load`/`Snapshot` 带 ops。
- **D6 大厅消费禁令**：`Purchase`（及未来消费路径）首检 `RoomAffinity()!=nil → 拒`。
- **离线消费**：`Runtime.Login` 在加载 player 后、回包+注册 online 之前串入 inbox 重放（§6.3）。
- **mail-claim ① 重排**：`grant→persist→mark-claimed`，`grantAttachments` 由 `opID=""` 改 `opID=mailID`（§6.4）。

### 5.3 matchsvr（最小封堵）

- **`matchQueue` 加 `pendingUids`**：`Enqueue` 拒 `pendingUids[uid]`；`FormTable.removeAll` 出 `waitingUids` 同时入 `pendingUids`；`orchestrate` 完成清/`Requeue` 移回（§6.1）。
- **超时 reap**：waiting 加入队 tick 序号；`tw.Tick` 周期扫描超 `maxWait` 者出队（保留 `seen`）+ best-effort `LobbyHandler.matchtimeout` 回告。

### 5.4 common/mongo（离线消息原语）

- 加 `$push`（append + upsert）/ `$pull`（按字段值移除）原语，或泛化 `UpdateByID(coll,id,update bson.M,upsert,done)`。仅离线消息用。

### 5.5 离线消息 store（新）

- collection `offline_messages`，doc `{_id:uid, messages:[{type, op_id, ...payload}]}`，多写者 append-only。
- `OfflineStore` 抽象（便于 fake）：`Push(d, uid, msg, done)` / `Load(d, uid, done([]OfflineMsg,err))` / `Ack(d, uid, opIDs, done)`（`$pull` 已处理）。
- 信封带 `type`，本期只 `settle` 一型；payload BSON `{price, currency, item_id}`。

---

## 6. 关键算法 / 流程

### 6.1 双发封堵（matchsvr，全主循环态）

```
Enqueue 拒绝: seen[uid:reqId] || waitingUids[uid] || pendingUids[uid]
FormTable.removeAll: 出 waitingUids → 入 pendingUids
orchestrate 完成(defer 经 Submit 回主循环):
  成功 → delete pendingUids[uid]   // GameStarted 已 ack ⇒ lobby roomAffinity 已置 ⇒ 后续 lobby 侧即拒
  失败 → Requeue: pendingUids[uid] 移回 waitingUids[uid]
```
封住 P4a 残余窗口（成桌→回告未落定）。残余"某 lobby 回告失败→缺绑定"仍 P4c 兜底（已知边界）。

### 6.2 结算回告幂等（在线 / 离线两路共用持久 ops）

- room→lobby `settle` 是 at-least-once（失败重试）。在线赢家 `Spend+Add(ops=gameId)`；离线赢家 `$push` inbox。
- **跨路双发防护**：online 已扣但 ack 丢 → room 重投 → 此刻玩家已离线 → inbox replay 时 `Spend+Add(ops=gameId)`，因 `ops=gameId` **已随 online 扣减持久落库**而命中跳过 ⇒ 无双发。在线/离线/重投/重连重放全funnel 同一持久 `ops=gameId`。
- 输家无经济副作用 ⇒ 仅清亲和；inbox 只承载**赢家离线**一种。

### 6.3 离线消息重放（登录链，replay 先于玩家可操作）

```
Login: store.Load(player) → buildPlayer → attachMail
  → offlineStore.Load(uid, msgs):
       逐条 replay(主循环): settle → Currency.Spend(ops=op_id) + Bag.Add(ops=op_id)
       → flush(uid, after→ offlineStore.Ack(uid, processedOpIDs))   // 落库后才 $pull，非盲删，护并发新投递
  → reply SC_Login + 注册 online + scanFriendAccepts   // replay 完成后才放行玩家操作
```
- **崩溃安全**：replay-flush 未落 → inbox 仍在 → 重登 replay（ops 去重，net-once）；flush 落但 `$pull` 未落 → 重登 replay → ops 命中跳过。**ops 是真边界，`$pull` best-effort**。
- **`CanAfford⇒Spend必成`**：离线期不能消费（未加载）；登录先 replay 再放行新操作 ⇒ 余额仍 ≥ 当时出价 ⇒ Spend 成。防御：极端 Spend 不足 → 日志 + 仍发物（charge 尽力，记审计）。

### 6.4 mail-claim ① 重排（持久 ops 为边界）

```
现状: Claim(原子标 claimed) → grantAttachments(opID="") → FlushSoon   // 崩溃窗口→永久丢失
改为: 读 mail(claimed:false) → 已 ops.seen(mailID)? → 跳过 grant
      否则 grant(opID=mailID) → flush(after→ mark-claimed)   // 落库后才标 claimed（§6.6）
```
- claimed 标志降级为"UI/清理提示"，**持久 ops(mailID) 才是幂等边界**。崩溃重登重放：grant-flush 未落 → mail 仍可领 → 重领 ops 未命中 → 重 grant（net-once）；grant-flush 落但 mark 未落 → 重领 ops 命中 → 跳过 grant，补 mark。单写者（mail.to=uid 仅 owner lobby 领）保无并发双领。

### 6.5 竞拍状态机（roomsvr，主循环串行）

```
OpenGame  → Bidding；AfterFunc(countdown, settle)
Bid       → !closed && uid∈participants && amount>HighestBid(严格) → set Highest{Bid,Bidder} → 广播；否则拒
settle    → closed=true；winner=HighestBidder（0=无人出价→流拍，无扣无发）→ off-loop 回告各 participant
```

### 6.6 「持久落库后才 ack/清理」不变式（结算/重放崩溃安全的关键）

at-least-once 投递（room→lobby settle 重试、offline replay）要求**消费副作用持久化落库之后**才回 ack / 清理来源，否则"ack 已发但 flush 未落"的崩溃会令上游不再重投、副作用永久丢失：

- **结算在线赢家**：`Spend+Add` 后用 `flushPlayer(uid, p, after=回 ack)` 立即落库（**非 `FlushSoon` 延迟合并**），ack 在 flush 完成回调内。崩溃在 flush 前 → 无 ack → room 重投 → 内存态亦已丢 → 重放 net-once；崩溃在 flush 后 ack 前 → 无 ack → room 重投 → 持久 ops 命中跳过 → 补 ack。
- **结算离线赢家**：inbox `$push` 成功回调内才 ack；`$push` 失败不 ack → room 重投。
- **离线 replay**：`Spend+Add` → `flush(after→ $pull 已处理 op_id)`，**落库后才 `$pull`**；崩溃在 flush 前 → inbox 仍在 → 重登重放（ops 去重）；flush 后 `$pull` 前崩溃 → 重登重放 → ops 命中跳过。
- **mail-claim ①**：grant → `flush(after→ mark-claimed)`，落库后才标 claimed（§6.4）。

统一原则：**持久 ops 是幂等真边界，ack/清理/标记一律 best-effort 且置于 flush 完成回调之后**。

---

## 7. proto / gen_routes 变更

**`lobby.proto`（客户端面，续 msg_id 2037+）**：
- `CS_Bid{string game_id; int64 amount}`（2037，server_type=lobbysvr，handler=`LobbyHandler.bid`）
- `SC_Bid{int32 code; int64 highest_bid}`（2038）
- `SC_AuctionState{string game_id; int64 highest_bid; int64 highest_bidder; int32 countdown_remaining}`（2039，push，仅 msg_id）
- `SC_AuctionResult{string game_id; int64 winner; int64 price; int32 item_id; string currency}`（2040，push，仅 msg_id）
- `SC_MatchTimeout{}`（2041，push，仅 msg_id）

**`room.proto`（server-server，无 msg_id；"发送方持有消息"约定）**：
- `RPC_OpenGame_Req` **加 `string currency`**
- `RPC_Bid_Req{string game_id; int64 uid; int64 amount}` / `RPC_Bid_Rsp{int32 code; int64 highest_bid; int64 highest_bidder}`（`RoomHandler.bid`）
- `RPC_AuctionState_Notify{int64 uid; string game_id; int64 highest_bid; int64 highest_bidder; int32 countdown_remaining}`（`LobbyHandler.auctionstate`，Cast）
- `RPC_Settle_Req{int64 uid; string game_id; int64 winner; int64 price; int32 item_id; string currency}` / `RPC_Settle_Rsp{int32 code}`（`LobbyHandler.settle`，CallViaSync）

**`match.proto`**：
- `RPC_GameStarted_Req` **加 `string currency`**
- `RPC_MatchTimeout_Notify{int64 uid; string req_id}`（`LobbyHandler.matchtimeout`，Cast）

**`online.proto`**：无改（`RPC_UnbindRoom` P4a 已建，本期接线）。**重跑 `gen_routes`**（msg_id 2037-2041 续 lobby 2000 段；server-server RPC 走字符串 route 无 msg_id）。

---

## 8. 错误处理 / 边界 / 幂等

### 8.1 校验（CLAUDE.md 工程纪律）

- `CS_Bid`：Player 已加载、`roomAffinity!=nil` 且 `gameID` 匹配、`CanAfford`；否则 `SC_Bid{code}` 零副作用。
- `RoomHandler.bid`：game 存在、`!closed`、uid∈participants、`amount>HighestBid`。
- `LobbyHandler.settle`：幂等（重复回告经 ops 去重）；player 未加载 → inbox（非错误）。
- 离线消息：`type`/`op_id` 非空；未知 type → 记录跳过（防脏数据）。

### 8.2 幂等汇总

| 路径 | 幂等键 | 机制 |
|---|---|---|
| 出价 | 无（最高价 last-write-max） | room 取 max，重复/乱序无害 |
| 结算扣币发物（在线/离线/重投） | `opID=gameId` | `Spend`/`Add` + **持久 ops 子文档** |
| 离线投递 replay | `op_id` + `$pull` 已处理 | ops 去重 + 非盲删 |
| mail-claim ① | `opID=mailID` | grant→persist→mark-claimed；持久 ops |
| 匹配请求（JetStream 重投） | `(uid,reqId)` | matchsvr 会话内 dedup（P4a 不变） |
| 双发窗口 | `pendingUids` | matchsvr 会话守护 |
| UnbindRoom / 亲和清除 | `uid`（绝对写） | 覆盖式，重复同值安全 |

### 8.3 失败路径

- **bid 转发 room 失败**（room 不可达）：lobby 视 room 已逝 ⇒ 清亲和 + push 客户端"对局作废"（P4b 轻量作废触发点；robust room-death 探测留 P4c）。
- **settle 回告某 lobby 失败**：room 重试（at-least-once）；持久 ops 保不双扣。room 自身崩溃中途 → 对局作废（D4，无 checkpoint）。
- **离线 inbox `$push` 失败**：lobby 不 ack → room 重投（at-least-once）。
- **持久 ops 环淘汰**：128 有界，极老 opID 被淘汰后若极迟重投会重发——JetStream/重连窗口短，MVP 接受，spec 标注。

### 8.4 并发

roomsvr/lobby/matchsvr 共享态全经各自单 goroutine 主循环串行，零锁；timewheel 回调在主循环（§3）；off-loop 网络/Mongo IO 回调经 `Submit` 回环（`inflight` 计数，停机 drain 沿用既有模式）。离线消息跨写者经原子 `$push`/`$pull`，不碰 players 单写者。

---

## 9. 风险 / 开放问题

- **离线消息 + 持久 ops 是结算正确性基石**：两者组合消灭跨路（在线/离线/重投）双发，须重点单测（§10）。
- **持久 ops 环有界淘汰**（§8.3）：极迟重投的理论重发，MVP 接受。
- **room 轻量作废依赖 bid-failure 触发**（§8.3）：玩家不再出价则亲和滞留至断连/P4c；robust 探测留 P4c。
- **赢家离线"白嫖"防护**：离线消息重放在登录早于玩家操作，`CanAfford⇒Spend必成` 成立；极端不足走 §6.3 防御分支。
- **mail-claim ① mark-claimed 滞后**：grant 落但 mark 未落且客户端不再领 → 邮件"看似未领实已发"（仅 UI 瑕疵，经济无误）。MVP 接受。
- **JetStream 结算事件偏离**（§9.4）。
- **proto/gen_routes 体量 + rebase**：新增 room Bid/Settle + 扩 lobby/match，注意与并行落地 rebase（P1~P4a 经验，执行期 main 会前进）。

### 9.4 JetStream 结算事件的有据偏离

umbrella §5.3 设想"结算/发奖经 JetStream（at-least-once + 幂等）"。本设计**不引 JetStream-for-settlement**，理由：① P4a 已定 lobby 不持 JetStream 句柄，per-uid 结算无干净 durable consumer 映射；② room 死本按 D4 作废，跨 room 崩溃的 durable 重放无意义；③ direct-RPC-retry（at-least-once 投递）+ 持久 ops（exactly-once 效果）+ offline_messages（离线 durable 投递）已等价实现"不丢不重"。JetStream 仍用于 P4a 的匹配请求。此偏离 spec 显式记录。

---

## 10. 测试策略（TDD，从本 Spec/设计意图推导）

- **单元**：
  - **roomsvr**：bid 接受/拒（closed/非参与者/未更高）、最高价更新、settle 定赢家（含无人出价流拍）、多 gameId 隔离、countdown→settle 在主循环触发、广播扇出计数（fake cls）。
  - **lobby**：bid 编排（亲和缺失/不匹配/CanAfford 足不足/转发）、settle 在线（Spend+Add+清亲和+unbind+push）、settle 离线（inbox `$push`）、**大厅消费禁令**（对局期 Purchase 拒）、**持久 ops round-trip**（Load→变更→Snapshot 含 ops，重 Load 去重）、**mail-claim ① 重排**（崩溃前后重放经 ops 幂等）、**offline inbox replay**（登录链顺序、ops 去重、`$pull`、replay 先于操作）。
  - **matchsvr**：超时 reap（出队+保留 seen）、pendingUids 封双发、Requeue 恢复守护。
  - **mongo**：`$push`/`$pull`（或 `UpdateByID`）原语。
  - **持久 ops 跨路防护**：在线已扣 → 同 gameId inbox replay 跳过（核心正确性用例）。
- **集成（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+etcd+MongoDB）**：凑桌→开局→出价→广播→结算→重登读回；重复结算/重投不双扣双发；离线赢家→inbox→重登补发（扣币+得物各一次）。
- **无 Docker（umbrella D10）**：集成测试只编译验证，实跑留 Docker host。
- **测试纪律**：约定/文档与实现冲突先暂停报告；后端服务集成测试放各服务自身包内（Go internal 可见性）。

---

## 11. 交付物速查

| 模块 | 新建/扩充 | 关键验收 |
|---|---|---|
| **roomsvr** | 扩：`Game` 竞拍字段、`RoomHandler.bid`、settle、广播；`Runtime` 加 cls/inflight/drain | bid 改最高价+广播；倒计时定赢家（含流拍）；多 gameId 隔离 |
| **lobby** | 扩：`bid`/`auctionstate`/`settle`/`matchtimeout` handler、持久 ops、大厅禁购、离线重放、① 重排、`roomBinding.currency` | 出价校验+编排；结算在线扣发/离线投递；持久幂等；① 崩溃既不丢不双发 |
| **matchsvr** | 扩：`pendingUids` 封双发、超时 reap | 残余双发封堵；超时出队+回告 |
| **mongo** | 扩：`$push`/`$pull` 原语 | 离线消息 append/ack |
| **offline_messages** | 新建：collection + `OfflineStore` | 离线投递+登录重放幂等 |
| **online** | 接线：`UnbindRoom` wiring（handler P4a 已建） | 结算清 room 绑定 |
| **proto** | `lobby`(Bid/AuctionState/Result/MatchTimeout 2037-2041)、`room`(Bid+Settle+OpenGame.currency)、`match`(GameStarted.currency+MatchTimeout)；gen_routes | 路由表生成 |

---

## 附录：工作流约定

- 本 Spec 评审通过后走 `writing-plans` 产出 `docs/plans/2026-06-04-P4b-…-impl-plan.md`（每步含失败测试+实现+验证），再用 `subagent-driven-development` 逐任务 TDD。
- 核心/并发任务 spec+质量双评审 + 整支 `-race` 终审（P2/P3a/P3b/P4a 反复证明 verbatim 计划代码会偏离/不全，终审 + `-race` 必做）。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；**合入前先 rebase 最新 origin/main**（执行期远端 main 会前进，P4a 即撞配置体系重写需 rebase+适配）。新增服务/配置须用新泛型配置体系（`cfgloader.NewLoader[genconfig.XConfig]` + `conf/schema/config_<svc>.proto` + conf 模板/values）。
- 文档维护：对 `architecture.md`/`cluster.md`/`development.md` 有影响的改动按 CLAUDE.md 同步（roomsvr 竞拍、离线消息 collection、新 proto/路由）。
- 集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf；存储用 BSON；客户端全链路 proto；JetStream 仅用于匹配请求（结算走 RPC-retry + 持久 ops + offline_messages，§9.4）。
