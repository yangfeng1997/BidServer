# P3b 设计 Spec：好友/邮件/货币组件 + 心跳→Touch/presence 接线 + flush 加固

> 状态：**设计 Spec（已评审通过，待出任务计划）** · 日期：2026-06-03 · 适用：`project`（GameServer）
>
> 承接 [`docs/plans/2026-06-03-P3a-lobby-ec-mongodb.md`](2026-06-03-P3a-lobby-ec-mongodb.md)（P3a 已实现并合入 main，PR #14）与 [`docs/plans/2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md)（P3 段）。P3a 把 lobby 推进为「权威玩家枢纽」地基 + 背包(Bag) 单组件竖切；**本 Spec 是 P3 的后半段 P3b**：在 P3a 的 EC 框架与单主循环之上铺第二批组件（货币/好友/邮件）、接线 P2 已实现未接的 `Touch`、落地 presence，并把 P3a 的 flush 雏形加固为生产级。
>
> **本阶段为单一 P3b（不再切分）**，一个设计 Spec + 一个 PR 覆盖全部范围（与用户确认，D1）。impl-plan 内部仍按地基→组件→跨玩家分阶段 TDD。

---

## 1. 目标 / 里程碑

让 lobby 从「单组件竖切」长成「多组件玩家枢纽」，并补齐在线活跃与跨玩家交互这两块新territory：

- **第二批组件落地**：货币(Currency)、好友(Friend) 作内嵌 flush 组件；邮件(Mail) 作 mailbox 集合后端组件。
- **跨玩家交互首次出现**：好友请求/接受、邮件投递（含投递给**离线**玩家），全部经 `mailbox` 集合（insert-only）流转，零分布式锁。
- **心跳→Touch 接线**：把 P2 已实现未接的 `onlinesvr.Touch` 接回——心跳经 lobby 中转、节流刷新在线活跃，5min TTL 真正生效。
- **presence（Hybrid）**：打开好友列表 pull 在线快照 + 在线期间 push 上/下线 delta。
- **跨组件编排范式定型**：「买道具扣币」走主循环显式编排（原子、可预检/回滚）；事件总线只承载不会失败的副作用（`CurrencyChanged`）。
- **flush 加固**：键点 flush + 优雅停机 drain + 多组件 flush 健壮性，消化 P3a 欠账。

这是新架构第一次出现「跨玩家写」「在线活跃刷新」「presence 推送」「跨组件编排」四个设施。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| D1 | P3b 切分 | **保持单一 P3b**（一个 Spec / 一个 PR） | 用户选择；impl-plan 内部按地基→组件→跨玩家分阶段 TDD，PR 体量大但一次评审定型 EC 的跨玩家边界。 |
| D2 | 心跳→Touch 路径 | **经 lobby 中转、节流** | gate.Heartbeat → Cast `RPC_Touch_Notify{uid}` → 绑定 lobby 主循环 →（节流：距上次 Touch < ~2min 跳过）→ off-loop `onlinesvr.Touch`。把 onlinesvr 写入全集中在 lobby（与 register/unregister 一致，单写者心智）；节流点在内存 `Player.lastTouchAt`，每跳仅一次廉价 enqueue，真正 RPC 每 ~2min 一次，5min TTL 余量充足。**必须心跳驱动**（非 lobby 自 tick），否则静默死连无法过期。 |
| D3 | presence 机制 | **Hybrid：pull 快照 + push delta，lobby 驱动 fan-out** | 开列表 `CS_FriendList` 遍历好友 `onlinesvr.Query` 取快照；登录/断连时 lobby 遍历好友图、对在线好友 Cast 推送上/下线。好友图是 lobby/player 数据，onlinesvr 保持**纯在线目录**（不订阅好友关系）。互为好友使每次登录 fan-out 天然对称，无需独立 subscribe。 |
| D4 | 跨玩家投递存储 | **独立 `mailbox` 集合（insert-only）** | 邮件、好友请求(`friend_req`)、好友接受(`friend_accept`) 统一作 mailbox 文档（按 `type` 区分），按 `to=recipientUid` 索引。投递=直接 insert（在/离线一致，无需加载收件人）；claim/read=按 `_id` update。绕开单文档 16MB、绕开「改未加载玩家」、不与组件「绝对 $set」flush 冲突。 |
| D5 | 跨组件交互 | **购买显式编排；event 只管不会失败的副作用** | 购买=主循环内 `CanAfford→Spend→Add`（单线程原子，可预检可回滚）。**反转 roadmap 原述**（其述 event-driven purchase）：事件无法预检 affordability、无法回滚，不适合必成功-或-回滚的流程。event 总线保留给解耦、不会失败的通知（`CurrencyChanged`→统计/审计）。 |
| D6 | 单写者不变式（基石） | **players 文档单写者；跨玩家一律经 mailbox** | 一个玩家文档**只有其归属 lobby 写**（按脏组件绝对 `$set`）。一切跨玩家交互经 `mailbox`（insert-only，任意方可写）。好友双向经 mailbox 握手建立，**最终一致**。此不变式让全程无分布式锁，且 P3a 的绝对-`$set` flush 不变式对内嵌组件原样成立。 |
| D7 | friend_accept 处理时机 | **登录扫描 + 在线 push 触发** | A 登录扫 `mailbox{to:A,type:friend_accept,claimed:false}` 逐条 `Friend.add(from)+claim`；A 在线时 B 接受触发「新邮件」push → A 即时处理。复用同一推送机制，无额外路径。 |
| D8 | 组件二分 | **内嵌 flush 型 vs mailbox 型** | 内嵌型（实现 P3a `Component` 接口、入绝对-`$set` flush）：Bag(已有)/Currency/Friend——有界、自有。mailbox 型（自管对 `mailbox` 的异步 I/O、`Dirty()` 恒 false、不入 flush 循环）：Mail。 |
| D9 | 幂等机制 | **op-id 去重抽共享 helper；Mail claim 用 per-mail 标志** | Bag 的 `recentOps` 环抽成可嵌入 `opDedup`，Bag/Currency 复用（消化 P3a 欠账：3 组件都需要）。Mail 领取幂等用 DB 原子 `update {_id,claimed:false}→claimed:true`：匹配 1 才发附件（附件以 `opID=mailID` 入 Bag/Currency，再防双发）；匹配 0 即已领，零副作用。 |
| D10 | flush 加固 | **键点 flush + 优雅停机 drain + 多组件健壮性** | 资源敏感操作（购买/Spend/Gain/领附件）后即时 flush（标 `flushSoon` 下 tick 合并，降宕机丢档窗口）；`Stop` 先 flushAllDirty 再等 in-flight Mongo 回调（补 P3a 裸 `context.Background()` 欠账）；验证 3+ 组件并发 flush 下 fire-once 守护与失败重标脏正确。 |

> 沿用全局既定决策（设计稿 §10 / roadmap §0.2 / P3a §2）：不引入 Redis；登录并入 lobby；单 world 不跨服；后端服务序列化器用 protobuf（集群 wire），存储用 BSON（与 wire 分离）；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 范围

### 3.1 范围内（P3b）

- **基石与存储**：确立「players 单写者 + mailbox 跨玩家」不变式（D6）；新增 `mailbox` 集合（D4）。
- **组件**：
  - Currency（内嵌）：多币种 `map[kind]int64`、`CanAfford/Gain/Spend`、op-id 去重、`CurrencyChanged` 事件。
  - Friend（内嵌）：好友 uid 集、`Add/Remove/Has/List`（全本地写）。
  - Mail（mailbox 型）：`List/Send/Claim`，对 `mailbox` 集合异步 I/O，不入 flush 循环。
- **跨玩家流程**：好友请求/接受/拒绝（mailbox 握手）、邮件投递（含离线）、领取附件（幂等）。
- **Touch 接线**（D2）：`RPC_Touch_Notify` + gate.Heartbeat Cast + lobby 节流 Touch。
- **presence**（D3）：`CS_FriendList` 快照（含每好友在线位）+ 登录/断连 push delta（`SC_FriendPresence`）。
- **框架新增**：通用 gate 推送路由 `GateHandler.PushToClient(uid, msgID, body)`（泛化 `KickSession`），复用于 presence delta 与「新邮件」notify；明确 push 方向 client↔gate 协议（gate 转发**已按 client 序列化器(json) marshal 的不透明 body** by uid）。
- **跨组件编排**（D5）：`CS_Purchase` 显式编排 Currency+Bag；事件总线落一个 `CurrencyChanged` 安全订阅者作示范。
- **flush 加固**（D10）：键点 flush、优雅停机 drain、多组件健壮性。
- **op-id 去重抽取**（D9）：`opDedup` 共享 helper。
- **proto**：扩 `lobby.proto`（货币/好友/邮件/购买 CS/SC + Touch Notify + 推送 SC）+ gate 推送路由；重跑 `gen_routes`。

### 3.2 范围外（留更后 / P4 / P5）

- 真实 token 校验、玩家数据版本化/迁移、分库分表、懒加载（沿 P3a）。
- `mailbox` 归档/清理/分页冷热分离（MVP 仅 `List` 限 N 条 + backlog）。
- 全服在线数统计、好友推荐、群组/公会、私聊频道。
- presence fan-out 的批量优化（MVP O(好友数) 逐个 Query/Cast）。
- 重连接回（依赖 room，P4 §6.5）；跨重登 op-id 去重（依赖发奖幂等键，P4）。
- 框架风险 backlog 其余项（draining 全量、`math/rand`、discovery dieCh 等，P5 收口；本阶段仅落 lobby 停机 drain 与 Mongo ctx）。

---

## 4. 基石：单写者不变式 + 存储模型（D6/D4/D8）

### 4.1 不变式

> **一个 players 文档只有其归属 lobby 写（按脏组件绝对 `$set`）；一切跨玩家交互经 `mailbox` 集合（insert-only，任意方可写）。**

这是 P3b 全程无分布式锁的根因，也让 P3a 的绝对-`$set` flush 不变式对内嵌组件原样成立。

### 4.2 两个集合

| 集合 | 键 | 写者 | 语义 | 内容 |
|---|---|---|---|---|
| `players` | `_id = uid` | **仅 owner 的 lobby** | 按脏组件绝对 `$set` | 内嵌组件：`bag` / `currency` / `friend` |
| `mailbox` | `_id` 自动；`to = recipientUid`（索引） | **任意**（跨玩家） | insert-only；claim/read = 按 `_id` update | 邮件 + 好友请求 + 好友接受（按 `type` 区分） |

`mailbox` 文档结构（建议）：
```
{ _id, to, from, type, attachments:[{kind,id,count}], body, ts, read, claimed }
type ∈ { normal, friend_req, friend_accept }
```

### 4.3 组件二分

- **内嵌、入 flush（实现 `Component` 接口）**：`Bag`(已有) / `Currency` / `Friend`。`buildPlayer` 手写装配，`PlayerDoc` 增 `currency`/`friend` 子字段。
- **mailbox 型、不入 flush**：`Mail`——是 dispatch 句柄（`p.Mail().List()`），自管对 `mailbox` 的异步 I/O，不嵌入 players 文档、`Dirty()` 恒 false、不进 `Components()` 脏遍历。

---

## 5. 组件详解

### 5.1 Currency（内嵌，flush 型）

- 存储态：`CurrencyState{ Balances map[string]int64 bson:"balances" }`（内嵌 `currency` 子文档）。多币种（`gold`/`diamond`/…）避免「只有金币」返工。
- 内存态 + 操作（主循环同步）：
  - `CanAfford(kind, amt) bool`
  - `Gain(opID, kind, amt)` / `Spend(opID, kind, amt) bool`——op-id 去重（共享 `opDedup`）+ 标脏；`Spend` 余额不足返回 false 零副作用。
- 成功变更后发布 `CurrencyChanged{uid, kind, delta}`（事件总线，§7）。

### 5.2 Friend（内嵌，flush 型）

- 存储态：`FriendState{ Friends []int64 bson:"friends" }`（内嵌 `friend` 子文档）。**有界、自有**，故为普通 `$set` 组件。
- 操作：`Add/Remove/Has/List`——**全本地写**（绝不跨玩家写，§4.1）；`Add` 集合幂等。
- 双向好友经 mailbox 握手建立（§6.1），不在此直接写对端。

### 5.3 Mail（mailbox 型，不入 flush）

- 不持权威内存列表；对 `mailbox` 集合异步 I/O：
  - `List(uid)`：异步 `find {to:uid}`（限 N 条、按 ts 倒序）→ reply。
  - `Send(to, type, payload)`：insert mailbox 文档。
  - `Claim(mailID)`：DB 原子 `update {_id:mailID, to:uid, claimed:false} → {claimed:true}`；匹配 1 → 按附件 `opID=mailID` 编排进 Bag/Currency（再防双发）→ reply 已领取的附件；匹配 0 → reply「已领取」零副作用（D9 幂等）。
- 在线收件人不持脏内存态：投递时若收件人在线，push「新邮件」notify（§6.3）触发其重拉 `List`，无 reconcile。

---

## 6. 跨玩家流程

### 6.1 好友请求 / 接受 / 拒绝（mailbox 握手，零 player-doc 跨写）

```
CS_FriendAdd(target)        → A.lobby: 校验(非自己/未是好友/无重复 pending) → insert mailbox{to:target, type:friend_req, from:A}
CS_FriendRespond(mailID, accept):
   accept → B.lobby: B.Friend.add(from)[本地脏] + insert mailbox{to:from, type:friend_accept, from:B} + claim(req)
   reject → B.lobby: claim(req)（不加好友）
A 处理 friend_accept（D7）   → A.lobby: 登录扫描 mailbox{to:A,type:friend_accept,claimed:false} 逐条 A.Friend.add(from)+claim；
                              A 在线时由「新邮件」push 触发即时处理
```

好友关系**最终一致**：B 接受即见 A；A 见 B 须等 A 处理 accept-mail（下次登录或在线被 push）。换来单写者不变式与零跨玩家锁——对游戏好友系统可接受。

### 6.2 邮件投递（含离线收件人）

```
发件（系统/玩家）→ insert mailbox{to:recipient, type:normal, attachments, ...}   // 在/离线一致，不加载收件人
收件人在线？      → onlinesvr.Query(recipient).online 则 push「新邮件」notify（§6.3）
收取             → CS_MailList → Mail.List(uid)
领取            → CS_MailClaim(mailID) → Mail.Claim（幂等，§5.3）
```

### 6.3 presence（Hybrid，lobby 驱动 fan-out）

```
Pull 快照:  CS_FriendList → for f in A.Friend: onlinesvr.Query(f) → SC_FriendList{每好友 uid + 在线位}
Push delta: A 登录（先处理 accept-mail 补全好友图，再 fan-out）/断连 → 遍历 A.Friend:
              onlinesvr.Query(f) → 若在线(拿到 f.GatewayNodeID):
                Cast RPC_PushToClient{uid:f, msg_id:SC_FriendPresence, body} → f 所在 gate → 推 client
新邮件 notify: 同机制，Cast RPC_PushToClient{uid:recipient, msg_id:SC_MailNew, body}
```

- 互为好友 → 每次登录 fan-out 对称（f 后登录则 f 的 fan-out 通知 A）。
- **断连必须在剔除 Player 前捕获好友列表**（先 fan-out 离线，再 flush+剔除）。
- 成本：O(好友数) Query+Cast/次登录登出，MVP 可接受，批量优化留后续。

### 6.4 通用 gate 推送（框架新增）

- 新增集群路由 `GateHandler.PushToClient(ctx, raw)`：gate `Sessions().ByUID(uid)` → `agent.Push(msgID, body)`，泛化既有 `KickSession`。
- `body` 是**已按 client 序列化器(json) marshal 的不透明字节**，gate 仅按 uid 透传——定下 push 方向的 client↔gate 协议（补 P3a 遗留的一半：推送方向）。集群信封 `RPC_PushToClient{uid, msg_id, body}` 仍 proto（集群 wire），body 内层 json。
- 复用于：presence delta（`SC_FriendPresence`）、新邮件 notify（`SC_MailNew`）。

---

## 7. 跨组件编排 + 事件总线（D5）

### 7.1 购买 = 主循环内显式编排（原子）

```
CS_Purchase(opID, kind, price, itemID) → lobby 主循环:
  p := Player(uid)
  if !p.Currency().CanAfford(kind, price):   reply SC_Purchase{code:余额不足}
  p.Currency().Spend(opID, kind, price)      // op-id 去重 + 标脏
  p.Bag().Add(opID, itemID, 1)               // 同 opID 去重 + 标脏
  reply SC_Purchase{ok, 新余额, 新数量}
  → 触发键点 flush（§8）
```

- **幂等**：Spend 与 Add 共用购买请求的 `opID`，重复请求两边各自去重，绝不双扣双发。
- **失败语义**：`CanAfford` 预检在扣减前，不足即拒、零副作用；单线程内 Spend→Add 不被打断，无需回滚。

### 7.2 事件总线 = 不会失败的副作用

- `Currency.Spend/Gain` 成功后发布 `CurrencyChanged{uid, kind, delta}`；P3b 落一个**安全订阅者**作示范（如「货币变动写审计日志」），不影响主流程、不会失败。
- 即 P3a 已铺的 `Events`（`PlayerLoaded`）增补 `CurrencyChanged`；事件仅主循环内同步分发，天然零锁。
- 反衬边界：必成功-或-回滚的流程（购买）走编排，不走事件。

---

## 8. flush 加固（D10）

P3a 仅「周期 + 断连 flush」雏形，且 P3a 复盘预警「P3b 加组件即爆」。三处加固：

1. **键点 flush**：资源敏感操作（购买/Spend/Gain/领附件）后即时 flush 该玩家——实现为标 `flushSoon` 下一 tick 合并（而非每次都写，缓解写放大），降低宕机丢档窗口。普通操作（背包整理/好友增删）仍随周期 flush。
2. **优雅停机 drain**（补 P3a 欠账）：`Runtime.Stop` 改为**先 flushAllDirty，再等所有在途 Mongo 回调完成**才退出。引入在途计数（WaitGroup/pending）+ Stop 等待；Mongo 异步 op 改用「可取消但停机期等 in-flight 完成」的 ctx（不再裸 `context.Background()`）。与现有 10s Agent 退出超时的协同在 impl-plan 对齐。
3. **多组件 flush 健壮性**：验证 3+ 组件并发 flush（部分成功/失败、写期间再变脏）下 P3a 的 `fireAfter` fire-once 守护与失败重标脏正确；Mail 不入 flush 循环。

---

## 9. 关键数据流（购买 + 好友 presence）

```
[购买]
client CS_Purchase(opID,kind,price,itemID)
  → gate forwardTable→lobby（绑定）CallRaw
    → LobbyHandler.purchase: Submit 进主循环 + 返回延迟回包哨兵
      → 主循环: CanAfford→Spend(opID)→Bag.Add(opID) → Replier 回 SC_Purchase
              → CurrencyChanged 事件（审计订阅者） + 标 flushSoon
    → gate done: agent.Response(SC_Purchase)
（下 tick）键点 flush: $set players._id=uid {currency:.., bag:..}

[好友 presence — A 登录]
lobby Login(A) 加载 Player + A.Friend
  → 先扫描 mailbox{to:A,type:friend_accept} 逐条 A.Friend.add+claim   // 补全好友图
  → 再 for f in A.Friend: onlinesvr.Query(f)
       若在线: Cast RPC_PushToClient{f, SC_FriendPresence(A,online)} → f.gate → f.client
```

---

## 10. 测试策略

- **单元**：Currency `CanAfford/Gain/Spend`+op-id 去重；Friend `Add/Remove/Has/List`；`opDedup` helper；Mail `List/Send/Claim` 幂等（fake mailbox）；购买编排（足/不足/重复 opID）；`CurrencyChanged` 事件分发；presence fan-out（fake Query/Cast 计数）；Touch 节流（`lastTouchAt` 阈值）；flush 加固（多组件部分失败重标脏、停机 drain 等 in-flight、fireAfter once）。
- **集成**（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+etcd+MongoDB）：
  - 购买改状态落库 → 重登读回；重复 opID 不双扣双发。
  - 好友请求→接受→双方各自登录后好友列表互含（最终一致）。
  - 邮件投递给离线玩家 → 该玩家登录后 `List` 可见 → `Claim` 发附件且重复 claim 不双发。
  - 心跳经 lobby Touch → onlinesvr `Query` 活跃刷新、不误过期。
- TDD：从本 Spec/设计意图推导用例、先写失败测试；后端服务集成测试放各服务自身包内（Go internal 可见性）。约定与实现冲突先暂停报告。

---

## 11. 风险 / 开放问题

- **mailbox 无界增长**：MVP 不做归档/清理，`List` 限 N 条 + 留 P5/后续 backlog；附件领取后留存（标 `claimed` 不删）。
- **presence fan-out 成本**：O(好友数) Query+Cast/次登录登出；MVP 单 world、好友数有限可接受，批量 Query/扇出优化留后续。
- **键点 flush 写放大**：高频资源操作 → 用 `flushSoon` 下 tick 合并缓解；监控 Mongo 写频。
- **好友最终一致窗口**：A 见 B 延迟到 A 处理 accept-mail；产品上需明确「对方已接受、稍后同步」的 UX，不视为 bug。
- **优雅停机等 in-flight 与 Agent 10s 超时协同**：impl-plan 对齐停机顺序（停 Acceptor→断连 fan-out/flush→等 Mongo in-flight→退出）。
- **gate 推送 by uid 的连接竞态**：`ByUID` 命中时连接可能正在关闭（push 失败 best-effort 忽略，与 KickSession 一致）。
- **跨重登 op-id 去重**：op-dedup 仅会话内内存态；跨重登去重靠发奖幂等键，留 P4（沿 P3a 遗留）。

---

## 12. P3b 交付物速查

| 类别 | 交付物 |
|---|---|
| 基石 | 单写者不变式（players 单写 + mailbox insert-only）；`mailbox` 集合 |
| 组件 | Currency（多币种/CanAfford/Gain/Spend/op-dedup/CurrencyChanged）；Friend（uid 集/Add/Remove/Has/List）；Mail（mailbox 型 List/Send/Claim 幂等） |
| 跨玩家 | 好友请求/接受/拒绝（mailbox 握手，最终一致）；邮件投递（含离线）；领附件幂等 |
| Touch | `RPC_Touch_Notify` + gate.Heartbeat Cast + lobby 节流（`Player.lastTouchAt`）→ onlinesvr.Touch |
| presence | `CS_FriendList` 快照（含在线位）+ 登录/断连 push delta（`SC_FriendPresence`） |
| 框架 | 通用 gate 推送 `GateHandler.PushToClient(uid,msgID,body)`（泛化 KickSession）；push 方向 client↔gate 协议（json 不透明 body by uid） |
| 编排/事件 | `CS_Purchase` 显式编排 Currency+Bag；`CurrencyChanged` 事件 + 安全订阅者示范 |
| flush 加固 | 键点 flush（flushSoon 合并）；优雅停机 drain（flushAllDirty + 等 in-flight Mongo）；多组件健壮性验证 |
| 复用/清理 | `opDedup` 共享 helper（Bag/Currency 复用，抽自 P3a Bag.recentOps） |
| proto | `lobby.proto` 扩货币/好友/邮件/购买 CS/SC + `RPC_Touch_Notify` + 推送 SC（`SC_FriendPresence`/`SC_MailNew`）+ gate `RPC_PushToClient`；重跑 `gen_routes` |
| 测试 | 上列单测 + 购买落库回读/好友握手/离线邮件/Touch 活跃 集成测试（编译验证） |

---

## 附录：工作流约定

- 本 Spec 评审通过后，走 `writing-plans` 产出任务级实现计划 `docs/plans/2026-06-03-P3b-…-impl-plan.md`（每步含失败测试 + 实现 + 验证）；impl-plan 内部按 地基(mailbox/不变式/opDedup/Touch)→组件(Currency/Friend/Mail)→跨玩家(握手/投递/presence)→编排/事件→flush 加固 分阶段 TDD。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；合入前先 rebase 最新 origin/main。
- 集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf；MongoDB 存储用 BSON（与 wire 分离）；gate 推送方向 body 用 client 序列化器(json)。
</content>
</invoke>
