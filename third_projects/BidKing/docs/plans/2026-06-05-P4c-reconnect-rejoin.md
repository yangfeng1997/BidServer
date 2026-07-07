# P4c 设计 Spec：重连接回 + room participant 改投 + robust room-death 探测

> 状态：**设计 Spec（brainstorm 已定稿，待出任务计划）** · 日期：2026-06-05 · 适用：`project`（GameServer）
>
> 承接 [`2026-06-04-P4-match-room-auction.md`](2026-06-04-P4-match-room-auction.md)（P4 umbrella 总设计，§9 即本阶段）、[`2026-06-04-P4a-matchmaking-open-game.md`](2026-06-04-P4a-matchmaking-open-game.md)（匹配凑桌→开局，PR #20/#21）与 [`2026-06-04-P4b-auction-settlement.md`](2026-06-04-P4b-auction-settlement.md)（对局竞拍+结算，PR #24，已合入 main）。沿用 P3a/P3b/P4a/P4b 既有设施（EC 组件 / 持久 opDedup / mailbox / offline_messages / flush 加固 / 帧驱动 roomsvr / online 目录）。
>
> **本阶段切片**：P4 的最后一块。在 P4a/P4b 打通的 `发起匹配→凑桌→开局→出价→结算` 之上，把对局弧从「玩家全程在线在同一 lobby」的理想直线，加固成**能容忍掉线 / 重连 / 换 lobby / room 崩溃**的健壮闭环。补齐 P2 延后的 5min 重连宽限接回。本 Spec 评审通过后走 `writing-plans` 产出 `…-P4c-…-impl-plan.md`。

---

## 1. 目标 / 范围

### 1.1 范围内

- **5min 重连接回**（补齐 P2/D8 延后）：掉线 5min 内重连任一 gate、选任一 lobby L2，接回原拍卖局继续出价、收后续广播与结算。权威重连源 = onlinesvr 在线目录（非 PlayerDoc）。
- **room participant `LobbyNodeID` 改投**：重连换 lobby ⇒ 经 router 通知 room 把该 uid 的 participant `LobbyNodeID` 改为 L2，否则广播 / 结算回告仍发往已死旧 lobby（umbrella §13 / P4b §1.2 点名的关键竞态点）。
- **robust room-death 惰性探测**：超越 P4b「仅 bid-failure 触发」的轻量作废——重连时 + 大厅消费被禁购拦截时**主动反查 room 存活**，room 死则作废清亲和，让玩家从「对局期禁购」状态解套回大厅。
- **宽限窗机制**：让 in-game 玩家的 room 绑定熬过「掉线→重连」间隙（改 `Directory.Register` / `Runtime.Disconnect` 语义）。
- **②环淘汰边界仅文档标注**（见 §9）。

### 1.2 范围外（延后 / 后续）

- **① 多组件部分 flush 跨字段原子性**（P4b 终审记录的已知边界）：架构级改动（`src/common/mongo` 无事务，须 Currency+Bag 折一个 `$set` 或 done 门控全组件成功 才 ack），触及核心 flush 主路、与 P4c 重连主线正交，**延后 P5** 独立处理。
- **room 对局级 checkpoint 恢复**：MVP 为作废（D4），不做对局帧级重建。
- **escrow 冻结子态**（D6：no-escrow 论证仍成立，见 §8）。
- **后台 room 心跳 / etcd discovery watch 的主动 room-death 探测**（本期用惰性探测，见 §2 P4c-3）。
- **JetStream**：umbrella §5.3 设想已在 P4a（匹配请求）/ P4b §9.4（结算改 RPC-retry + 持久 ops + offline_messages）全部讨清，P4c 不引入新 JetStream。
- anti-snipe / 多拍品 / 暗拍 / 段位匹配 / MMR 窗口放宽（延续 P4a/P4b 范围外）。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| **P4c-1** | P4c 范围 | **核心三件套**（重连接回 + 改投 + robust room-death）+ **②文档化**；**①延后 P5** | 保持 P4c 聚焦重连弧。① 跨字段原子性是架构级、风险高、与重连主线正交；② 极迟重投理论重发在 room 3 重试/~600ms 窗口下不可达，文档标注即可。 |
| **P4c-2** | 宽限窗机制 | **`Disconnect` 保留 in-game 在线条目靠 5min TTL 自然过期**；**`Register` 保留已存在条目的 room 绑定字段** | online 是 5min 宽限窗本体（P2/D8）；当前 `Disconnect` 立即 `Unregister`、`Register` 重建空 Entry 抹掉绑定，宽限窗对干净掉线未生效。room 绑定与 gate/lobby 位置正交，只由 `UnbindRoom`/结算/`Unregister`/TTL 清。保「online 单源权威」，不引第二份状态。 |
| **P4c-3** | room-death 探测 | **惰性探测**：重连 rejoin 不可达 + 大厅消费被禁购拦截时反查 room | 重连接回本就要联系 room（改投+取快照），room 不可达天然是「重连场景死探测」。在线不操作的玩家亲和滞留无正确性影响（不影响别人，opID 防双发），不值后台心跳/discovery watch 的流量与状态复杂度（与低频拍卖性质不符）。 |
| **P4c-4** | rejoin 形态 | **合并为单个 `RoomHandler.rejoin` RPC**：原子改投 + 回当前拍卖快照（含 currency）+ 判活 | 一次往返同时完成改投 / 取快照 / 取 currency / 判活；改投与 settle 在 room 帧驱动主循环串行，竞态最干净。currency 取自 room 权威的 `Game.Currency`，免在 OnlineEntry 加字段 + 改 BindRoom。 |
| **P4c-5** | 补偿语义 | **no-escrow ⇒ 无退款**；作废 = 清亲和 + （若离线期已结算）offline replay 兜 | 竞价期零扣减，「退还出价」是空操作；离线期正常结算已由 P4b `offline_messages` 登录重放兜住（opID=gameId 恰一次）。P4c「补偿」实质退化为清亲和让玩家解套，非发退款邮件。 |

> 沿用全局既定：统一 NATS + etcd + MongoDB；集群 RPC 发送端硬编码 `proto.Marshal`，后端服务序列化器用 protobuf；存储用 BSON；客户端全链路 proto（pre-P4 决策 C）；gate 推送方向 body 用 client 序列化器；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 验证的并发事实 / 现状缺口（动手前已读码核实）

- **OnlineEntry 已含 room 绑定**：`directory.go:11-19` `Entry{Uid, GatewayNodeID, LobbyNodeID, LoginTime, LastActive, RoomNodeID, GameID}`；P4a 建 `BindRoom`/`UnbindRoom`（`directory.go:136-159`，绝对覆盖写，幂等）+ `OnlineHandler.Bindroom/Unbindroom`（`online_handler.go:66-79`）+ `Query` 回带 room 字段。**这是重连查询源。**
- **缺口① `Register` 抹掉 room 绑定**：`directory.go:58-61` `d.entries[uid] = &Entry{Uid, GatewayNodeID, LobbyNodeID, LoginTime, LastActive}` 建**全新条目**，`RoomNodeID`/`GameID` 归零。重连重注册即丢绑定。→ P4c-2 改为保留。
- **缺口② `Disconnect` 立即注销整条目**：`runtime.go:206-218` 无条件 `onlineUnregister(uid)` → `Directory.Unregister` 直接 `delete`。干净掉线（PlayerDisconnect notify）瞬间删条目，5min 宽限窗仅对不干净掉线（靠 TTL 过期）偶然成立。→ P4c-2 改为 in-game 跳过注销。
- **缺口③ `Login` 不查 room 绑定**：`runtime.go:174-203` 重连只 `onlineRegister`+回包，不读 `RoomNodeID`/`GameID`、不重建 `roomAffinity`。→ P4c 加重连分支。
- **5min TTL + 代次防竞态**：`online_module.go:12` TTL=`5*time.Minute`；`directory.go:112-133` timewheel `AfterFunc` + 全局单调代次 `genOf`/`nextGen`，迟到回调凭代次自证（P2 防 Touch/到期竞态）。**保留在 in-game 条目上的绑定靠此 TTL 在 5min 后随条目一并过期清理。**
- **roomsvr 帧驱动、timewheel 回调在主循环 goroutine 串行**：`runtime.go:87-102` `loop` 在 `case <-ticker.C:` 调 `tw.Advance()`；`TimeWheel.Advance()` 释放锁后在调用方 goroutine 内联 `t.fn()`。故 `OpenGame` 注册的 `settle`（`runtime.go:120-130`、`194-222`）与新增 `rejoin` 处理（经 Submit）**同主循环串行**，可零锁直接改 `Game` 态。off-loop 网络 IO 走 `go`+`Submit`+`inflight`/`drain`。
- **room participant 持 `{UID, LobbyNodeID}`**：`game.go:5-9` `Participant{UID, LobbyNodeID}`；广播 `runtime.go:150-186` 取 `p.LobbyNodeID` DIRECT Cast、结算 `runtime.go:224-229` `settleViaRouter(lobbyNode,…)` DIRECT CallViaSync。**改投即更新此 `LobbyNodeID`。**
- **lobby roomAffinity 内存态**：`player.go:56-74` `roomBinding{roomNodeID, gameID, currency}`，`SetRoomAffinity`（`lobby_handler.go:376` Gamestarted 置）/`RoomAffinity()`（bid `lobby_handler.go:420` 读）/`ClearRoomAffinity`（settle `runtime.go:621` 清）。**重连重建即据 online 绑定 + rejoin 回的 currency 调 `SetRoomAffinity`。**
- **顶号链既有**：`Register` 返回 `old, replaced=true`（`directory.go:49-53`）→ `OnlineHandler` Cast `RPC_KickSession_Notify` 给旧 gateway → gate OnClose → `PlayerDisconnect` → 旧 lobby `Disconnect` flush+剔除。**P4c 重连换 lobby 时靠此链收敛旧 lobby 残副本（见 §6.6 双载分析）。**
- **`src/common/mongo` 无事务**：仅单文档原子。重连不新增持久写设施；沿用 P4b 持久 ops（`opID=gameId`）作幂等真边界。
- **lobby msg_id 已用到 2041**（`lobby.proto`，P4b `SC_MatchTimeout`）；本期 `SC_ReconnectAuction` 续 **2042**。room/online server-server RPC 走字符串 route 无 msg_id。

---

## 4. 架构与寻址

### 4.1 重连接回（5min 宽限）

```
client 掉线 → 重连任一 gate → CallAnySync 选 lobby L2 → L2 校验 token(stub) + Mongo 加载玩家
L2 Runtime.Login:
  store.Load → buildPlayer → attachMail
  → replayOffline                    // 兜“离线期已结算”（offline_messages，P4b §6.3）
  → onlineRegister(L2)               // Register 保留 room 绑定（更新 gate/lobby=L2）
  → Query online{uid} → preserved {room=X, game=G}?   // RPC_Query_Rsp 已回带 room 字段，online.proto 无改
  → 有 room 绑定 ? 重连接回分支(新增):
       off-loop CallViaSync(DIRECT, X, "RoomHandler.rejoin"{uid, game_id=G, new_lobby_node=L2}):
         ├ alive  → 回 snapshot{code=0, highest_bid/bidder, countdown_remaining, item_id, currency}
         │          → Submit: SetRoomAffinity(X, G, currency) + push SC_ReconnectAuction(snapshot, status=active)
         └ dead   → code=closed | room 不可达
                    → Submit: unbindRoom(X 已清/或经 router 清 online) + 不置亲和
                      + push SC_ReconnectAuction(status=voided)
                      (经济效果由 replayOffline 或下次登录 replay 兜, opID=gameId 恰一次)
  → reply SC_Login → scanFriendAccepts → fanoutPresence
```

### 4.2 改投（room 主循环）

```
RoomHandler.rejoin{uid, game_id, new_lobby_node}  (薄壳 + Submit + ErrDeferredReply):
  主循环:
    g := games[game_id]
    ├ g==nil || g.closed → 回 RPC_Rejoin_Rsp{code=closed}
    └ 否则 → 改投: 对 g.participants 中 UID==uid 者 LobbyNodeID = new_lobby_node
             → 回 RPC_Rejoin_Rsp{code=0, highest_bid, highest_bidder, countdown_remaining, item_id, currency}
```

### 4.3 惰性 room-death 探测（禁购触发点）

```
LobbyHandler.purchase（及未来消费）:
  主循环: p.RoomAffinity()!=nil ?
    ├ 否 → 正常 Purchase
    └ 是 → off-loop CallViaSync(DIRECT, aff.roomNodeID, "RoomHandler.querygame"{game_id=aff.gameID}):
            ├ exists && !closed → 回 SC_Purchase{code=禁购}（维持禁购）
            └ 不可达 | !exists | closed
                → Submit: ClearRoomAffinity + unbindRoom（经 router 清 online）
                  → 回 SC_Purchase{code=请重试}（客户端重发，此时亲和已清 → 正常 Purchase，无 mid-flow 重入）
```

### 4.4 寻址表（全部经 router，复用 P4a/P4b resolve）

| 跨服 hop | 经 router 方式 | route | 复用现状 |
|---|---|---|---|
| lobby → room 重连改投/取快照 | `CallViaSync(DIRECT, roomNodeID, …)` | `RoomHandler.rejoin` | **新增 handler**，复用 DIRECT resolve |
| lobby → room 探活 | `CallViaSync(DIRECT, roomNodeID, …)` | `RoomHandler.querygame` | **新增 handler** |
| lobby → online 查绑定 | `CallViaSync(CONSISTENT_HASH, uid, …)` | `OnlineHandler.query` | 复用 P2/P4a（重连读 room 绑定） |
| lobby → online 清绑定 | `CallViaSync(CONSISTENT_HASH, uid, …)` | `OnlineHandler.unbindroom` | 复用 P4b（作废清绑定） |
| lobby → client 推送 | `presence.Push`→`GateHandler.pushtoclient` | （msg_id 2042） | 复用 P3b/P4a/P4b 推送 |

---

## 5. 组件与职责

### 5.1 onlinesvr

- **`Directory.Register` 保留 room 绑定**：重注册时若已存在条目，新 Entry 沿用其 `RoomNodeID`/`GameID`（仅更新 gate/lobby/时间戳）。顶号路径（`old, replaced`）不变。
- 其余不变：`BindRoom`/`UnbindRoom`/`Query`/`Touch`/`expire` 代次防竞态全保留。

### 5.2 lobbysvr

- **`Runtime.Login` 重连分支**：`onlineRegister`（Register 保留绑定）后紧接 `Query online`（`RPC_Query_Rsp` 已回带 room 字段，**无需扩 `RPC_Register_Rsp`，保 online.proto 无 schema 改**）→ 有绑定则 off-loop `rejoin` → 据返回重建亲和 + push `SC_ReconnectAuction`（alive）或作废（dead）。reply `SC_Login` 不阻塞于 rejoin（rejoin 异步，亲和重建/推送在其回调主循环内）。
- **`Runtime.Disconnect` 改 in-game 语义**：`p.RoomAffinity()!=nil` → 仅 flush 剔除内存副本 + presence offline，**跳过 `onlineUnregister`**（条目靠 TTL 过期）；否则维持现状立即注销。
- **`LobbyHandler.rejoin` 编排 / `Runtime` 加 rejoin hook**：off-loop `CallViaSync(DIRECT, roomNodeID, RoomHandler.rejoin)`（仿 P4b `forwardBid`/`Settle` 的 off-loop 模式，可替换 fake 供单测）。
- **`LobbyHandler.purchase` 加惰性探活**：`RoomAffinity()!=nil` 时 off-loop `querygame`，据结果维持禁购或清亲和放行（替代 P4b 的同步直接拒）。
- **`SC_ReconnectAuction` 推送**：复用 `PushToClient`。

### 5.3 roomsvr

- **`RoomHandler.rejoin`**（薄壳 + Submit + ErrDeferredReply）：主循环改投 participant `LobbyNodeID` + 回快照；game 不存在/closed 回 `code=closed`（§4.2）。
- **`RoomHandler.querygame`**（薄壳 + Submit）：只读回 `{exists, closed}`。
- **`Game` 无新字段**（快照字段 `HighestBid/HighestBidder/CountdownSec/ItemID/Currency/closed` P4b 已有）；改投仅改 `Participant.LobbyNodeID`。countdown_remaining 由 `deadline` 在主循环算（`runtime.go` 既有 broadcastState 同款）。

### 5.4 proto / gen_routes

见 §7。

---

## 6. 关键算法 / 流程

### 6.1 Login 重连分支顺序（replay 先于 rejoin 先于放行新操作）

```
Login(uid, gw, reply):
  已有内存副本(p!=nil)? → onlineRegister + reply（热路径，无掉线，不重建）  // runtime.go:175-178 现状
  否则 store.Load:
    buildPlayer + attachMail + PlayerLoaded
    → replayOffline(uid, p, after):                                 // P4b §6.3, 兜离线已结算
         onlineRegister(uid, gw)                                    // Register 保留绑定（更新 gate/lobby=L2）
         Query online{uid} → preserved {room,game}?                 // RPC_Query_Rsp 回带 room 字段
           preserved 有? → rejoin(uid, game, L2) off-loop:
             alive → Submit{ SetRoomAffinity(room,game,currency); push SC_ReconnectAuction(active) }
             dead  → Submit{ unbindRoom; push SC_ReconnectAuction(voided) }
         reply SC_Login                                             // 放行；亲和重建可能略晚于 reply（rejoin 异步）
         scanFriendAccepts → fanoutPresence
```
- **replay 与 rejoin 互斥安全**：一局 game 要么已结算（settlement 在 offline_messages，replay 兜）、要么仍 active（rejoin 接回）；两路 funnel 同一 `opID=gameId` 持久 ops，绝不双发。
- **亲和重建晚于 reply 的窗口**：reply 后、亲和置入前若客户端立刻 `CS_Bid`，bid 校验 `RoomAffinity()==nil` → 拒（零副作用），客户端据 `SC_ReconnectAuction` 收到快照后重发即可。可接受。

### 6.2 改投与 settle 的主循环串行（核心竞态，§4.2 在 room 主循环）

room 帧驱动单主循环（§3），rejoin（Submit）与 settle（timewheel 回调 inline）串行：
- **rejoin 先到**：改投 `LobbyNodeID=L2` → 后续 settle 回告按 L2 发 → 在线赢家 L2 直接 `Spend+Add`。✓
- **settle 先到（已 closed）**：settle 回告按当时 `LobbyNodeID`（旧 L1）发 → L1 已剔除副本（Disconnect flush）→ 走离线 `$push offline_messages`；rejoin 见 closed → 回 `code=closed` → L2 作废。结算延迟到**下次登录** replay（opID=gameId 恰一次，不丢不双发）。MVP 接受（§9）。
- **in-flight 广播发往旧 lobby**：改投落定前在途的 `auctionstate` Cast 发往 L1 丢失（L1 无副本）→ rejoin 快照兜底（Cast 本约定可丢，客户端下次刷新）。✓

### 6.3 惰性 room-death 探测（三触发点，统一“联系 room 失败 = 死”）

1. **重连**：rejoin 不可达 / `code=closed` → 作废清绑定（§6.1）。
2. **禁购**：`purchase` 命中亲和 → `querygame` 不可达 / `!exists` / `closed` → 清亲和 + `unbindRoom` → 放行（§4.3）。
3. **出价**（P4b 既有）：bid 转发 room 失败 → 清亲和。

纯在线不操作玩家：亲和滞留至下次操作 / 断连，无正确性影响（不影响别人；结算 opID 防双发）。

### 6.4 「持久落库后才 ack/清理」不变式延续（P4b §6.6）

P4c 新增路径继续遵守：
- **重连接回**：rejoin 是查询/改投，无持久副作用 → 无 ack-before-persist 风险。亲和是内存态。
- **作废 unbindRoom**：online 绝对写，幂等，best-effort（失败下次重连/TTL 自愈）。
- **结算路径不变**：在线/离线赢家结算仍按 P4b §6.6（flush 后才 ack / `$pull`）。重连不改结算落库时序。

### 6.5 Register 保留绑定的幂等性

- 重注册保留 `{RoomNodeID, GameID}`：重复同值安全（绝对覆盖语义）。
- 结算 `UnbindRoom` 后再重注册：绑定已空，保留空仍空。✓
- TTL 过期：条目随绑定一并清（5min 无 Touch）。重连若已过 5min → `Query` 无条目 → Login 正常分支（无接回，玩家回大厅；若期间已结算则 offline replay 兜）。✓

### 6.6 不干净掉线双载分析（已知窄窗，§9）

- **干净掉线**：gate FIN → PlayerDisconnect → L1 Disconnect flush+剔除 + 保留 online 条目（in-game）。重连 L2 加载 → 单写者（仅 L2）。✓
- **不干净掉线**：L1 副本未剔除 + L2 重连加载 → 双 lobby 持同一 player。靠顶号链收敛：L2 `Register` 见旧 gateway≠新 → `replaced` → Cast `KickSession` 旧 gate → gate OnClose → `PlayerDisconnect` → L1 flush+剔除。
  - **收敛前窄窗**：L1/L2 各自 absolute-`$set` flush 有覆盖风险。结算 `opID=gameId` 持久 ops 令结算幂等收敛（任一侧已扣，另一侧命中跳过）；非结算的并发改动（如禁购被绕过的极端）属窄窗已知边界。干净修需文档 lease（无 Mongo 事务），超范围，记 §9。

---

## 7. proto / gen_routes 变更

**`room.proto`（server-server，无 msg_id；“发送方持有消息”约定）**：
- `RPC_Rejoin_Req{string game_id; int64 uid; string new_lobby_node}` / `RPC_Rejoin_Rsp{int32 code; int64 highest_bid; int64 highest_bidder; int32 countdown_remaining; int32 item_id; string currency}`（route `RoomHandler.rejoin`，CallViaSync；`code`：0=alive 接回，非 0=closed/不存在 作废）
- `RPC_QueryGame_Req{string game_id}` / `RPC_QueryGame_Rsp{bool exists; bool closed}`（route `RoomHandler.querygame`，CallViaSync）

**`lobby.proto`（客户端面，续 msg_id 2042）**：
- `SC_ReconnectAuction{string game_id; int64 highest_bid; int64 highest_bidder; int32 countdown_remaining; int32 item_id; string currency; int32 status}`（2042，push，仅 msg_id；`status`：0=active 接回快照，1=voided 作废）

**`online.proto`**：无 schema 改（Register 保留绑定是 Directory 内部行为）。

**重跑 `gen_routes`**（msg_id 2042 续 lobby 2000 段；room server-server RPC 走字符串 route 无 msg_id）。

---

## 8. 错误处理 / 边界 / 幂等

### 8.1 校验（CLAUDE.md 工程纪律）

- **重连 rejoin**：`game_id`/`new_lobby_node` 非空；room 侧 game 不存在/closed → `code=closed`（非错误，作废）；uid∉participants → `code=closed`（防脏）。
- **querygame**：`game_id` 非空；不存在 → `exists=false`。
- **Login 重连分支**：online `Query` 失败（onlinesvr 不可达）→ 降级为正常登录（不接回，记日志）；rejoin off-loop 失败 → 作废（room 死）。
- **Disconnect in-game 判定**：取 `p.RoomAffinity()` 须在 flush+剔除**之前**（内存态在 Player 上）。

### 8.2 幂等汇总

| 路径 | 幂等键 | 机制 |
|---|---|---|
| 重连接回（重复重连） | online 绝对写 + 亲和绝对绑定 | 重复同值安全；Register 保留绑定 |
| 改投（重复 rejoin） | `uid`（participant 绝对写 LobbyNodeID） | 覆盖式，重复同值安全 |
| 作废 unbindRoom | `uid`（绝对写） | 覆盖式幂等 |
| 结算扣币发物（在线/离线/重投/重连重放） | `opID=gameId` | `Spend`/`Add` + 持久 ops（P4b，不变） |
| 离线 replay | `op_id` + `$pull` 已处理 | ops 去重 + 非盲删（P4b，不变） |

### 8.3 货币安全（D6 / P4b-6 复核，no-escrow 仍成立）

- 单一对局亲和 + 对局期禁大厅消费 ⇒ 余额不被侵蚀 ⇒ `CanAfford(出价)` ⇒ 结算 `Spend(最高价)` 必成。
- **P4c 不破此前提**：重连接回恢复同一对局亲和（仍单一对局）；惰性探测作废只在 room 死时放行禁购（此时无结算将发生）。离线 replay 在登录早于玩家操作（P4b §6.3），余额仍 ≥ 当时出价。

### 8.4 失败路径

- **rejoin 期 room 崩溃**：CallViaSync 报错 → 作废（§6.3）。
- **改投落定前 settle**：§6.2，结算延迟到下次登录 replay。
- **online 不可达**：重连降级正常登录（§8.1）；unbindRoom 失败 best-effort（下次重连/TTL 自愈）。
- **超 5min 宽限**：online 条目已过期 → 无接回 → 正常回大厅（已结算则 offline replay 兜）。

---

## 9. 风险 / 开放问题

- **改投竞态（核心）**：settle 抢在 rejoin 前 → 结算回告发旧 lobby → 离线 `$push` → 延迟到下次登录 replay（opID=gameId 恰一次，不丢不双发）。MVP 接受；产品上重连期短暂「结算稍后到账」可接受。须重点单测（§10）。
- **不干净掉线双载窄窗（§6.6）**：靠顶号链收敛，结算 opID 幂等兜底；干净修需 lease（无 Mongo 事务），延后。
- **②持久 ops 环 128 有界**（P4b 既记）：极迟重投理论重发，room 3 重试/~600ms 窗口下不可达，**仅文档标注**，本期不动。
- **in-game 断连在线条目滞留 presence 不一致**：FriendList 在线快照可能 5min 内显示「在线」（push 已 offline）。轻微 UI 瑕疵，MVP 接受。可选改进：保留条目但标 disconnected 子态供 presence 排除（延后）。
- **惰性探测的滞留**：纯在线不操作玩家亲和滞留至下次操作/断连，无正确性影响（§6.3）。
- **亲和重建晚于 reply 窗口**（§6.1）：首个 bid 可能被拒，客户端据快照重发。可接受。
- **proto / gen_routes 体量 + rebase**：新增 room rejoin/querygame + 扩 lobby，注意与并行落地 rebase（P1~P4b 经验，执行期 main 会前进）。

---

## 10. 测试策略（TDD，从本 Spec / 设计意图推导）

- **单元**：
  - **onlinesvr**：`Register` 保留已存在条目 room 绑定（重注册不抹）、顶号路径仍返回 old/replaced、TTL 过期随条目清绑定。
  - **lobbysvr**：
    - 重连分支：有绑定 → rejoin alive → 重建亲和 + push `SC_ReconnectAuction(active)`；rejoin dead/不可达 → 作废 + push `voided` + unbindRoom。
    - `Disconnect` in-game（`RoomAffinity!=nil`）保留在线条目（不 Unregister）+ flush 剔除；非 in-game 立即注销。
    - 惰性探活：`purchase` 命中亲和 → querygame alive → 维持禁购；dead → 清亲和 + unbindRoom + 放行。
    - replay 与 rejoin 互斥幂等（已结算 → replay 兜不双发；active → rejoin 接回）。
  - **roomsvr**：`rejoin` 改投 `LobbyNodeID` + 回快照（含 currency/countdown_remaining）、game 不存在/closed → `code=closed`、`querygame` 只读、**rejoin 与 settle 主循环串行序**（rejoin 先→改投后 settle 发新 lobby；settle 先→rejoin 回 closed）。
  - **竞态**：settle-先于-rejoin → 离线 `$push` + 下次 replay 恰一次（核心正确性用例）；in-flight 广播丢失 + 快照兜底。
- **集成（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+etcd+MongoDB）**：掉线 5min 内重连接回原拍卖局，继续出价 + 收后续广播 + 收结算；超窗 / room 死 → 作废可见；重连换 lobby 后结算落点正确（改投生效）。
- **无 Docker（umbrella D10）**：集成测试只编译验证；实跑留 Docker host。
- **测试纪律**：约定/文档与实现冲突先暂停报告（CLAUDE.md）；后端服务集成测试放各服务自身包内（Go internal 可见性）。

---

## 11. 交付物速查

| 模块 | 新建/扩充 | 关键验收 |
|---|---|---|
| **onlinesvr** | 改：`Register` 保留 room 绑定 | 重注册不抹绑定；顶号路径不变；TTL 随条目清 |
| **lobbysvr** | 扩：Login 重连分支、`Disconnect` in-game 保留条目、`rejoin` 编排、`purchase` 惰性探活、`SC_ReconnectAuction` 推送 | 5min 重连接回原局；改投后结算落点正确；禁购探活作废 |
| **roomsvr** | 扩：`RoomHandler.rejoin`（改投+快照）、`RoomHandler.querygame`（只读判活） | rejoin 改投 LobbyNodeID + 回快照；game 死回 closed；主循环串行 |
| **proto** | `room`(Rejoin/QueryGame)、`lobby`(ReconnectAuction 2042)；gen_routes | 路由表生成 |

---

## 附录：工作流约定

- 本 Spec 评审通过后走 `writing-plans` 产出 `docs/plans/2026-06-05-P4c-…-impl-plan.md`（每步含失败测试 + 实现 + 验证），再用 `subagent-driven-development` 逐任务 TDD。
- 核心/并发任务（重连分支、rejoin 改投、改投×settle 竞态、Disconnect 语义改）spec+质量双评审 + 整支 `-race` 终审（P2/P3a/P3b/P4a/P4b 反复证明 verbatim 计划代码会偏离/不全，终审 + `-race` 必做）。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；**合入前先 rebase 最新 origin/main**（执行期远端 main 会前进）。spec+impl-plan 合一个 docs PR，code 另开 PR。
- 文档维护：对 `architecture.md`/`cluster.md`/`development.md` 有影响的改动按 CLAUDE.md 同步（重连接回流程、改投、新 proto/路由）。
- 集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf；存储用 BSON；客户端全链路 proto；JetStream 仅用于匹配请求（P4c 不引入新 JetStream）。
