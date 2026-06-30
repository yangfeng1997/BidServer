# P4 总设计 Spec：匹配 + 对局房间（竞拍）+ 结算 + 重连接回 + JetStream

> 状态：**设计 Spec（已评审通过，待出任务计划）** · 日期：2026-06-04 · 适用：`project`（GameServer）
>
> 承接 [`docs/plans/2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md)（P4 段）、[`docs/design/2026-06-01-target-architecture-design.md`](../design/2026-06-01-target-architecture-design.md)（§5.3/§6.4/§6.5/§8）与 P3b（[`2026-06-03-P3b-friend-mail-currency-touch-presence.md`](2026-06-03-P3b-friend-mail-currency-touch-presence.md)，PR #16，已合入 main）。
>
> **本文是 P4 的 umbrella 总设计**：统揽 `登录→匹配→进房→对局(竞拍)→结算→重连接回` 全弧 + 跨切设施（JetStream / OnlineEntry 扩 room 绑定 / lobby 驱动广播）+ 6 项 P3b backlog 的归属。**实现拆为 4 个独立 impl-plan + PR**：`pre-P4 加固` → `P4a 匹配凑桌→开局` → `P4b 对局(竞拍)+结算` → `P4c 重连接回+补偿`。每个子阶段进入实现前各自走 `writing-plans`，本 Spec 是其共同地基与边界契约。

---

## 1. 目标 / 里程碑

打通整套架构的业务高峰——**对局全链路**，并补齐 P2 延后的 5min 重连宽限接回：

- **matchsvr（新建，有状态）**：MMR 匹配队列，凑相近水平的 bidder 成桌，凑齐后在某 room 实例开一局（`gameId`），回告各玩家所属 lobby「room#X / game G」。匹配请求走 **JetStream**（at-least-once，不丢）。
- **roomsvr（新建，有状态，帧驱动）**：一实例多局（`gameId` 隔离），timewheel 倒计时 + tick 主循环串行推进**拍卖对局**；开局/结算落 MongoDB（幂等）。
- **lobby 侧**：发起匹配入口；持 `玩家↔room` 亲和绑定（`BindNode("roomsvr", X)`）并经 router 同步到 onlinesvr；对局期作为出价的货币校验/编排中介与结算回告的落地点。
- **重连恢复（设计稿 §6.5）**：重连可能落到另一 lobby；L2 从 MongoDB 加载工作副本 + 经 router 向 online 查「当前 room 绑定/在线态」，room 仍存活则接回原拍卖局，否则走作废+补偿。**补齐 P2 延后的 5min 重连宽限接回。**
- **JetStream 接入**：首次引入 JetStream 设施（匹配请求 + 结算/发奖关键事件 at-least-once + 幂等）；明确 core vs JetStream 边界（设计稿 §5.3）。

**业务载体：竞拍（auction）对局。** 设计稿原文只写抽象的"房间/匹配制对战游戏"；本 Spec 起，P4 的具体对局是**竞拍**——这决定了对局是**低频、timer 驱动、出价耦合玩家货币(Currency)与物品(Bag)**，而非实时帧战斗。这一性质是 §2 多个决策（尤其 D3 对局消息路径）的根因。

这是新架构第一次出现「匹配凑桌」「帧驱动对局」「JetStream 可靠投递」「跨进程对局结算幂等」「跨 lobby 重连接回」五个设施。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| **D1** | P4 拆分 | **umbrella 总 Spec + 4 个 impl-plan/PR**：`pre-P4 加固`→`P4a`→`P4b`→`P4c` | P4 体量最大（4 新子系统 + JetStream + 重连 + 6 backlog），roadmap §P4 风险即建议拆 P4a/b/c。一次 brainstorm 定型跨切边界，实现分 PR 可评审、可回滚、TDD 周期可控。 |
| **D2** | 对局业务载体 + 厚度 | **竞拍(auction)对局，极薄占位**：单拍品 / timewheel 倒计时 / 记最高价 / 到点定赢家。**桌大小常量 N（默认 2）** | 与 P3a Bag 竖切同思路——P4 价值在管线 `match→room→出价→结算→重连`，不在拍卖玩法复杂度。N 为常量为后续多人留口，但 spec 明确不做组队/段位。 |
| **D3** | 对局消息路径 | **A：经 lobby 中转（遵循设计稿 §6.4）** —— 上行 `出价→gate→lobby(Currency 校验)→router→room`；下行 `room→各 lobby→PushToClient→gate→client` | 竞拍下 bypass(gate↔room 直达) 的三条优势全部蒸发：①出价**必须校验/扣减玩家货币(Currency，lobby 权威态)**，对局本就无法与 lobby 解耦，故 bypass 的"故障隔离"不成立；②低频 timer 驱动，bypass 的延迟/单 goroutine 负载优势无意义；③货币/物品耦合让 lobby 成天然中介。A 还兼得设计稿一致 + 单枢纽心智 + 可集中校验。 |
| **D4** | room 宕机 / 对局异常 | **对局作废 + mailbox 补偿（无 checkpoint）** | roadmap §8 默认值，P4 启动前再确认。重连落到已死 room → 作废 → 经 mailbox（复用 P3b）退还出价/冻结（幂等）→ 玩家回 lobby 正常态。room 周期 checkpoint 重建留后续迭代。 |
| **D5** | 跨切设施 | **3 项**：OnlineEntry 扩 room 绑定 / lobby 驱动广播(复用 PushToClient) / JetStream 框架级接入。**不采用** bypass 方案的"gate room 绑定" | 选 A 后 gate 仍只绑 `lobbysvr`，对局消息天然走已绑 lobby，无需 gate 持第二种绑定——设计更贴设计稿、跨切设施减一项。 |
| **D6** | 出价的货币处理 | **对局期 lobby 拒绝大厅消费(基于亲和绑定) + bid 时 `CanAfford` 校验 + 结算时 `Spend(opID=gameId)`**；**不引入 escrow 子态** | 玩家同时只在一个对局（单 `玩家↔room` 亲和绑定）。对局期禁止大厅消费 ⇒ 货币只会被本局结算扣减 ⇒ bid 时 CanAfford 保证最高价 ≤ 余额 ⇒ 结算 Spend 必成功，无需冻结子态（P3b Currency 仅余额 map）。escrow 留作"未来支持对局期并发消费"再引入。 |
| **D7** | 结算幂等（含 ④a + ①） | **结算扣币/发物以持久键 `opID=gameId(:uid)` 落库去重；邮件领取(①)复用同一持久幂等设施** | 赢家 `Currency.Spend(opID=gameId)`+`Bag.Add(opID=gameId)`；落库以 `gameId` 为幂等键。P3b 的 op-dedup 仅会话内内存态，**本阶段补上"跨重登/跨重放"的持久幂等键**（= backlog ④a）。**①（mail-claim 附件发放）与 ④a 是同一问题的两个实例**，共用一套"持久幂等发放"设施（详见 §8）——这修复了 P3b claim-before-persist 崩溃窗口（原 pre-P4 简单反转修法因 op-dedup 仅会话内，崩溃重登时会 double-grant，plan 阶段发现，见 §6 注）。 |
| **D8** | 重连权威与宽限 | **online 持 room 绑定为重连查询源；lobby 持亲和并经 router 同步 online；5min 宽限** | lobby 持 `玩家↔room` 亲和（内存），经 router 写 onlinesvr（纯内存在线目录）；重连时新 lobby L2 查 online 拿绑定。补齐 P2 §2 延后的"5min 重连宽限接回"。 |
| **D9** | 6 项 backlog 归属 | 见 §3.3 表 | 纯地基/测试缺口(⑤⑥)先行 pre-P4；① claim-before-persist 与 ④a 同属"持久幂等发放"问题，**合并入 P4b**（plan 阶段发现原计划的简单反转修法因 op-dedup 仅会话内、崩溃重登时 double-grant，详见 §6 注）；④a 跨重登去重并入 P4b 结算；④b/③ 延后；② 环境受限。 |
| **D10** | 无 Docker 约束 | **集成测试沿 P3a/P3b 仅编译验证 + 文档标注；② 无法在本沙箱关闭** | 沙箱无 Docker，无法起 NATS/JetStream/etcd/MongoDB 实跑容器集成测试。`//go:build integration` 测试照写并编译验证，实跑需 Docker host。 |

> 沿用全局既定决策（设计稿 §10 / roadmap §0.2 / P3a/P3b §2）：统一 NATS + etcd + MongoDB 直连；登录并入 lobby；单 world 不跨服；集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf（集群 wire），存储用 BSON，gate 推送方向 body 用 client 序列化器(json)；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 范围

### 3.1 范围内（P4）

- **pre-P4 加固 pass**（独立 PR，先行）：backlog ⑤⑥（§6）。
- **P4a 匹配凑桌→开局**（§7）：matchsvr + `match.proto` + JetStream 匹配请求 + lobby 发起匹配/开局回告 + room 开局建拍卖局。
- **P4b 对局(竞拍)+结算**（§8）：roomsvr 拍卖帧驱动 + `room.proto` + 出价编排(经 lobby) + 倒计时结算 + 结算幂等(含 ④a) + **邮件领取持久幂等(①，与 ④a 共用设施)** + JetStream 结算事件。
- **P4c 重连接回+补偿**（§9）：5min 宽限接回 + room 死/异常作废 + mailbox 补偿。
- **跨切设施**（§5）：OnlineEntry 扩 room 绑定 / lobby 驱动广播 / JetStream 框架接入。
- **proto**：新建 `match.proto` / `room.proto`；扩 `online.proto`（room 绑定）/ `lobby.proto`（匹配入口、拍卖 CS/SC、结算回告）；重跑 `gen_routes`。

### 3.2 范围外（延后 / P5）

- **room 宕机对局级恢复**：MVP 为作废+补偿（D4），不做对局 checkpoint 重建。
- **组队 / 段位匹配、复杂分桶扩容搜索**（设计稿 §12 暂缓）：先单人 MMR 基础凑桌。
- **room 单实例承载局数上限 / 开局调度 / 扩缩容**（容量规划）。
- **escrow 冻结子态**（D6）：MVP 用"对局期禁大厅消费 + bid CanAfford + 结算 Spend"替代。
- **拍卖玩法纵深**：多拍品/连续拍/暗拍/加价规则等，留后续；MVP 单拍品+明价+倒计时+最高价赢。
- backlog **④b presence fan-out 批量优化**、**③ mailbox 归档/分页**（§3.3）。

### 3.3 6 项 P3b backlog 归属（D9）

| # | backlog 项 | 归属 | 处理 |
|---|---|---|---|
| ⑤ | 客户端↔gate 请求-响应方向 json/proto 收敛 | **pre-P4** | 对称补齐 P3b 只定的 push 方向，定 req/rsp 转码边界（§6.1）。 |
| ⑥ | 补 agent.handleData 层 D9 转发单测 | **pre-P4** | 补 gate 按绑定转发的单测（§6.2）。 |
| ① | claim-before-persist 崩溃窗口加固 | **P4b（与 ④a 合并）** | 需**持久幂等**（跨崩溃重登才正确）；与 ④a 同设施。plan 阶段发现原 pre-P4 简单反转修法不正确（op-dedup 仅会话内，崩溃重登 double-grant），见 §6 注 / §8。 |
| ④a | 跨重登 op-id 去重 | **P4b** | 结算扣币/发物持久幂等键 `opID=gameId(:uid)`；与 ① 共用"持久幂等发放"设施（D7 / §8）。 |
| ④b | presence fan-out 批量优化 | **延后** | 纯性能优化，单 world 好友数有限；顺手再做。 |
| ③ | mailbox 归档 / 分页 | **延后到 P5** | 功能项，不阻塞 P4。 |
| ② | 集成测试去 t.Skip 实跑（需 Docker） | **环境受限** | 沙箱无 Docker，无法在本沙箱关闭；集成测试保持编译验证 + 文档标注（D10）。 |

---

## 4. 拆分与顺序

```
pre-P4 加固(⑤⑥) ──► P4a(match 凑桌→开局) ──► P4b(对局竞拍+结算, 含 ④a+①) ──► P4c(重连接回+补偿)
       │                  │                          │                            │
  地基/测试缺口         接回登录流程              依赖 P4a 的开局/亲和           依赖 P4b 的 room 存活/结算
  (json/proto 边界)     (JetStream 首引)          + P3b Currency/Bag/mailbox      + online room 绑定
                                                  + 持久幂等设施(④a + ① mail-claim)
```

- **pre-P4 先行**：⑤⑥ 是已合入 P3b 代码的地基/测试缺口，且 ⑤(req/rsp json/proto 边界) 是 P4 要堆大量 match/room CS/SC 消息的地基——先修干净再堆。**①（claim-before-persist）因需持久幂等、与 ④a 同设施，已移 P4b**（见 §6 注）。
- **P4a→P4b→P4c 顺序**：P4a 产出 `玩家↔room` 绑定与开局，P4b 在其上做出价/结算，P4c 在 room 存活+online 绑定上做重连。每阶段满足 roadmap §0.5 统一验收口径。
- 跨切设施（§5）按需在最早用到的子阶段落地：online room 绑定 + JetStream 在 P4a；lobby 驱动广播在 P4b。

---

## 5. 跨切设施（umbrella 级）

### 5.1 OnlineEntry 扩 room 绑定（D8）

- `online.proto` 的 `OnlineEntry` 增 `room_node_id` + `game_id`；新增 RPC `RPC_BindRoom_Req{uid, room_node_id, game_id}` / `RPC_UnbindRoom_Req{uid}`（route=`OnlineHandler.bindroom`/`unbindroom`），由 lobby 经 router 在开局/结算/作废时同步。
- onlinesvr 仍是**纯内存在线目录**（不落库、不订阅业务）；room 绑定只是在线条目上的附加位置字段，与 gateway/lobby 位置同列。
- 重连（§9）时 L2 经 router `Query(uid)` 读回 `{room_node_id, game_id}` 判断是否在对局中。
- **幂等/一致性**：BindRoom/UnbindRoom 为绝对写（覆盖式），重复同值安全；与 5min TTL 过期清理协同（在线条目过期则绑定一并失效）。

### 5.2 room→client 广播（lobby 驱动，复用 P3b PushToClient）

- room 不直接面向 client。room 持每局 `participants[{uid, lobbyNode}]`（开局 payload 带入，重连刷新 lobbyNode，§9）。
- 广播拍卖状态：`room → 各参与者 lobby（Cast）→ lobby 复用 P3b RPC_PushToClient{uid, SC_AuctionState, body} → 其 gate → client`。
- `body` 是已按 client 序列化器(json) marshal 的不透明字节（沿 P3b push 方向约定）；集群信封仍 proto。
- 与 P3b 既有推送（presence/新邮件）同机制、同 `GateHandler.PushToClient` 路由——P4 不新增 gate 推送设施，只新增 SC 消息号。

### 5.3 JetStream 框架级接入（首次引入）

- 在 `src/framework/cluster`（或并列的 `jetstream` 子包）抽出 JetStream 接入设施，与现有 core NATS transport 并存；提供"发布到 stream + durable consumer 消费 + ack"的统一姿势，各服务无感复用（契合设计稿 §3.0"新增统一能力"）。
- **用途与边界**（设计稿 §5.3）：
  - **匹配请求** → JetStream：不能丢玩家的匹配请求。match 作 durable consumer，**凑桌入队成功后 ack**（崩溃未 ack 则重投，幂等去重见下）。
  - **结算/发奖关键事件** → JetStream：at-least-once + 幂等（`opID=gameId(:uid)`，D7）。
  - **拍卖状态广播 / 实时态** → core NATS（PushToClient 即 core；丢失靠客户端重拉当前态，§5.2）。
- **消费幂等**：匹配请求以 `(uid, matchReqId)` 去重（同一玩家重复入队幂等）；结算事件以 `gameId(:uid)` 去重。JetStream 重复投递不产生重复副作用。
- **首次引入风险**：消费组/ack/重复投递语义需专门验证（§13）；MVP 只用 JetStream 的核心发布-消费-ack，不上复杂的 stream 保留/重放策略。

---

## 6. pre-P4 加固 pass（⑤⑥）

> 独立 PR，先于 P4a。修已合入 P3b 代码的地基/测试缺口。

> **注：① claim-before-persist 为何移出 pre-P4（plan 阶段发现）。** 原计划 ① 在 pre-P4 用"反转顺序（入账→同步 flush→标 claimed）+ 重放凭 `opID=mailID` 去重"修复。读代码确认 `lobby_handler.go:215-226` 现状=`Claim(原子标 claimed)→grantAttachments(opID="")→FlushSoon`（崩溃窗口→永久丢失），但**反转修法不正确**：`op_dedup.go` 是纯内存、**仅会话内**结构（P3b spec §11 明载"op-dedup 仅会话内内存态；跨重登去重靠发奖幂等键，留 P4"），崩溃必然重登→新会话 `opDedup` 为空→反转后的失败窗口在重放时 `opID=mailID` 不命中→**double-grant**（把"永久丢失"换成"可重放刷物"，更糟）。正确修法需**持久幂等**，与 ④a「跨重登 op-id 去重」是同一问题；故 ① **合并入 P4b**，与 ④a 共用一套"持久幂等发放"设施（§8）。

### 6.1 ⑤ 客户端↔gate 请求-响应方向 json/proto 收敛

- **现状**：P3b 只定了**推送方向**（gate 透传已 json marshal 的不透明 body by uid）。**请求-响应方向**（client→gate→lobby 的 CS、lobby→gate→client 的 SC 回包）的 json/proto 转码边界尚未收口。
- **目标**：定死 req/rsp 方向 client(json)↔gate↔lobby(proto) 的转码契约——明确 gate 是透传不透明 client 字节由 lobby 用 client 序列化器解码，还是 gate 转码。P4 将堆大量 match/room 的 CS/SC，先把边界定死。
- **范围克制**：只收敛边界契约 + 必要重构，不扩散到无关改动。具体形态在 pre-P4 plan 读 `gate_handler.go` 转发路径 + `lobby_handler.go` 收包路径后定。

### 6.2 ⑥ 补 agent.handleData 层 D9 转发单测

- 补 gate 按 `BindNode(serverType)` 绑定转发玩家消息（`agent.handleData` → forward 到 `BoundNode`）的单测覆盖——P3b 遗留的转发地基测试空白。廉价、纯补测。

---

## 7. P4a — 匹配凑桌 → 开局

### 范围内

- **matchsvr（新建，有状态）**：内存 MMR 匹配队列；从 JetStream 消费匹配请求；凑齐 N 个 MMR 相近的 bidder → 选一个 room 实例（etcd discovery + 负载/轮询）开一局 `gameId=G` → 回告各玩家所属 lobby `{room_node_id=X, game_id=G, participants:[{uid, lobby_node_id}]}`。
- **lobby**：
  - 发起匹配入口 `CS_StartMatch`（校验玩家可匹配状态：未在对局中等）→ 经 router 把匹配请求**发布到 JetStream**。
  - 开局回告处理：`BindNode("roomsvr", X)` 亲和 + 经 router `BindRoom(uid, X, G)` 同步 online + 回 client `SC_MatchFound{room, game}`。
- **roomsvr（骨架 + 开局）**：接受开局 RPC，按 `gameId` 建拍卖局，记 `participants[{uid, lobbyNode}]` + 拍品 + 初始倒计时；tick 主循环骨架（P4b 填出价/结算）。
- **proto**：`match.proto`（`CS_StartMatch` 关联 lobby、匹配请求 JetStream payload、开局回告 RPC）；扩 `lobby.proto`（`SC_MatchFound`）；扩 `online.proto`（BindRoom，§5.1）；重跑 `gen_routes`。
- **JetStream 设施**（§5.3）落地：匹配请求发布 + match durable 消费 + ack。

### 验收

- 凑桌：≥N 个客户端登录后发起匹配 → match 凑齐一桌 → room 开局 → **各玩家 lobby 收到 `room=X/game=G` 绑定**且经 router 写入 online（`Query` 可见 room 绑定）。
- 匹配请求经 JetStream：match 重启后未 ack 的请求重投、不丢；同一玩家重复入队幂等。
- 单测：MMR 凑桌（相近水平成桌、不足 N 时挂起）；开局回告→lobby 亲和+online 同步；JetStream 消费幂等(fake)。

---

## 8. P4b — 对局（竞拍）+ 结算（含 ④a）

### 范围内

- **roomsvr 拍卖帧驱动**：timewheel 倒计时 + tick 主循环串行推进；多 `gameId` 并存隔离（各局独立拍品/出价/倒计时，共享帧循环）；记最高价与最高出价者。
- **出价路径（A，D3）**：`CS_Bid{game_id, amount}` → gate → lobby（`Currency.CanAfford(amount)` 校验 + 对局期亲和绑定确认）→ 经 router → room（记出价、更新最高价）→ 广播 `SC_AuctionState`（§5.2，room→各 lobby→PushToClient→gate→client）。
- **结算（倒计时到点）**：room 定赢家 → 经 router 回告各玩家 lobby `{game_id, winner, price, item}`：
  - 赢家 lobby：`Currency.Spend(opID=gameId, price)` + `Bag.Add(opID=gameId, item)`（同 `opID` 幂等，§D7）。
  - 输家 lobby：无扣减（无 escrow，D6）。
  - room 落库开局/结算（幂等键 `gameId`）→ 广播 `SC_AuctionResult` → 各 lobby `UnbindRoom` 清亲和 + 经 router 清 online room 绑定。
- **持久幂等发放设施（④a + ①）**：建一套"跨崩溃/重登/重放都成立"的持久幂等发放——把 P3b 仅会话内 `opDedup` 升级为**落库持久键**。两个消费者共用：
  - **④a 结算**：扣币/发物以 `opID=gameId(:uid)` 持久去重；JetStream 结算事件重投、重连重放、lobby 重分配均不双扣双发。
  - **① mail-claim**：邮件附件发放走同设施（持久键含 `mailID`），修复 P3b claim-before-persist 崩溃窗口——领取**持久幂等地**入账，崩溃重登重放既不丢也不双发（替代原计划在 pre-P4 的会话内 `opID=mailID` 反转修法，该修法不正确，见 §6 注）。`grantAttachments` 改走持久键（当前硬编码 `opID=""` 一并修正）。
  - 设施具体形态（players 文档 `granted` 账本条件原子写 vs 通用幂等 helper vs Mongo 多文档事务）在 **P4b plan** 读 `runtime.go`/`store.go`/`component_*.go`/`src/common/mongo` 后定。
- **D6 货币安全**：对局期 lobby 拒绝大厅消费（`CS_Purchase` 等检查玩家有无活跃 room 亲和绑定，有则拒）。
- **proto**：`room.proto`（`CS_Bid`/`SC_AuctionState`/`SC_AuctionResult` + 结算回告 RPC）；扩 `lobby.proto`（结算回告落地）；重跑 `gen_routes`。
- **JetStream 结算事件**（§5.3）：结算/发奖关键事件 at-least-once + 幂等。

### 验收

- 出价：`CS_Bid` 经 lobby Currency 校验（余额不足拒、零副作用）→ room 更新最高价 → 各参与者收到 `SC_AuctionState`。
- 结算：倒计时到点定赢家 → 赢家扣币得物、输家无扣 → 落库；**重复结算/JetStream 重投/重连重放不双扣双发**（持久 `opID=gameId` 幂等）。
- 多 `gameId` 并存：一实例多拍卖局互不串扰（出价/倒计时/结算按 gameId 隔离）。
- 单测：room 拍卖 tick/倒计时/出价/多局隔离；lobby 出价编排（足/不足/对局期禁购）；结算回告幂等（重复回告不双扣双发）；JetStream 结算消费幂等(fake)。

---

## 9. P4c — 重连接回 + 补偿

### 范围内

- **重连接回（5min 宽限，补齐 P2 延后）**：
  ```
  client 掉线 → 重连任一 gate → 选 lobby L2 → L2 校验 token + 从 MongoDB 加载工作副本
  L2 → 经 router 查 online{uid}: room_node_id=X, game_id=G ?
    ├ room X 存活: L2 BindNode("roomsvr",X) 亲和 + 经 router 更新 online(gate/lobby=新) 
    │              + 经 router 通知 room X「uid 新 lobby=L2」(room 改投广播/结算回告)
    │              + 回 client SC_ReconnectAuction(当前拍卖态快照) 或令 client 重拉
    └ room X 已死/已结束: 作废 → 经 mailbox 退还出价/冻结(幂等) → 玩家回 lobby 正常态
  ```
- **room 改投**：A 路径下 room 广播/结算回告发往各玩家**当前 lobby**；重连换 lobby ⇒ **必须经 router 通知 room 把该 uid 的 `lobbyNode` 改投 L2**（否则发往已死旧 lobby）。这是 P4c 的关键竞态点（room participant 的 lobbyNode 更新）。
- **room 宕机 / 对局异常（D4）**：对局作废 → 各参与者下次重连/查询发现 room 不可达 → 经 mailbox 退还（幂等，`opID` 复用 mail/补偿键）→ 回 lobby 正常态。
- **proto**：扩 `lobby.proto`/`online.proto`/`room.proto`（重连查询、room 改投通知、补偿）；重跑 `gen_routes`。

### 验收

- 集成（编译验证）：掉线 **5min 内**重连接回原拍卖局，能继续出价、收到后续广播与结算。
- 超 5min 宽限 / room 已死 → 作废 + mailbox 补偿可见（退还出价、不双退）。
- 单测：重连分支（room 存活→重绑+改投 / room 死→作废+补偿）；room participant lobbyNode 改投；补偿幂等。

---

## 10. 关键端到端数据流（竞拍 + A 路径）

```
登录   client→gate→CallAnySync lobby L1: 校验token+加载玩家+经router注册online(gate=G1,lobby=L1); gate BindNode("lobbysvr",L1)

匹配   CS_StartMatch→gate→lobby L1(校验可匹配)→router→JetStream 发匹配请求(不丢)
       match durable 消费→凑齐N个MMR近的bidder→选room X开拍卖局gameId=G→回告各玩家lobby{room=X,game=G,participants:[{uid,lobbyNode}]}→ack

开局   各玩家lobby: BindNode("roomsvr",X)亲和 + 经router BindRoom(uid,X,G) 同步online + 回client SC_MatchFound
       room X: 建拍卖局G(拍品/倒计时), 记participants[{uid,lobbyNode}]

出价   CS_Bid{game=G,amount}→gate→lobby(Currency.CanAfford + 对局期禁大厅消费)→router→room X(记价,更新最高价)
       room X 广播: 对每participant Cast→其lobby→PushToClient{uid,SC_AuctionState}→gate→client

结算   room X 倒计时到→定赢家→落库(幂等键gameId)+经router回告各玩家lobby{winner,price,item}
       赢家lobby: Currency.Spend(opID=gameId)+Bag.Add(opID=gameId) [幂等]; 输家lobby: 无扣
       room X 广播 SC_AuctionResult → 各lobby UnbindRoom + 经router清online room绑定

重连   掉线→重连任一gate G2→选lobby L2→L2校验+加载+经router查online{room=X,game=G?}
       ├ room X存活: L2 BindNode亲和+更新online(gate=G2,lobby=L2)+经router通知room X「uid新lobby=L2」→改投; client重拉拍卖态
       └ room X死/已结束: 作废→经mailbox退还(幂等)→玩家回lobby正常态

室宕机 对局作废→各参与者经mailbox退还出价/冻结(幂等)
```

---

## 11. 幂等汇总

| 路径 | 幂等键 | 机制 |
|---|---|---|
| 匹配请求（JetStream 重投） | `(uid, matchReqId)` | match 消费去重，凑桌入队后 ack |
| 结算扣币 / 发物（重投/重连/重放，**④a**） | `opID=gameId(:uid)` | `Currency.Spend`/`Bag.Add` + **落库持久去重键**（持久幂等发放设施） |
| 邮件领取附件（崩溃重登重放，**①**） | 含 `mailID` 的持久键 | 同上持久幂等发放设施；修复 claim-before-persist 崩溃窗口（既不丢也不双发） |
| room 开局/结算落库 | `gameId` | 绝对写 / upsert 幂等 |
| 重连接回（重复重连） | online 绝对写 + 亲和绝对绑定 | 重复同值安全 |
| 作废补偿（mailbox 退还） | `opID`（复用 mail/补偿键） | mailbox insert + claim 幂等（P3b D9） |

> **关键**：④a 把 P3b 仅会话内内存态的 op-dedup 升级为**跨重登/跨重放的持久键**（落库），是 P4 结算正确性的基石。

---

## 12. 测试策略

- **单元（TDD，从本 Spec/设计意图推导，先写失败测试）**：
  - match：MMR 凑桌（成桌/不足挂起）、开局回告、JetStream 消费幂等(fake)。
  - room：拍卖 tick/倒计时、出价记录/更新最高价、结算定赢家、多 `gameId` 隔离。
  - lobby：出价编排（Currency 足/不足/对局期禁购）、结算回告落地（扣币+发物幂等、重复回告不双扣双发）、重连分支（room 存活→重绑+改投 / room 死→作废+补偿）、补偿幂等、online room 绑定同步。
  - 跨切：JetStream 发布-消费-ack(fake)、PushToClient 广播扇出计数、OnlineEntry room 绑定读写。
  - pre-P4：⑤ req/rsp 转码边界、⑥ agent.handleData 转发。
  - P4b 持久幂等设施：① mail-claim 崩溃窗口重放幂等（入账已落库但 claim 前崩溃→重放不双发；claim 后崩溃→不重复）、④a 结算跨重登/重放去重——共用设施的单测。
- **集成（`//go:build integration`，沙箱仅编译验证；实跑需容器 NATS+JetStream+etcd+MongoDB）**：
  - 凑桌→开局→出价→广播→结算落库 → 重登读回；重复结算/JetStream 重投不双扣双发。
  - 掉线 5min 内重连接回原拍卖局，继续出价+收广播+收结算。
  - room 死 / 超窗 → 作废 + mailbox 补偿可见、不双退。
- **无 Docker（D10）**：集成测试只能编译验证；CI 实跑需 Docker host；backlog ② 无法在本沙箱关闭，spec 显式记为环境受限欠账。
- **测试纪律**：约定/文档与实现冲突先暂停报告（CLAUDE.md）；后端服务集成测试放各服务自身包内（Go internal 可见性）。

---

## 13. 风险 / 开放问题

- **全链路最复杂**：拆 pre-P4 + P4a/P4b/P4c 四个 PR 分别落地、各自 TDD + 评审；本 Spec 是共同地基。
- **JetStream 首次引入**：消费组/ack/重复投递语义需专门验证；MVP 只用核心发布-消费-ack，复杂保留/重放策略留后续。运维上 JetStream 需 NATS 开启 JS（部署/集成环境）。
- **room 宕机 MVP 取舍（D4）**：作废+补偿不做对局级恢复；产品上需明确"对局异常已退还"的 UX。
- **重连改投竞态（P4c）**：room 持 participant 的 lobbyNode，重连换 lobby 必须经 router 通知 room 改投，否则广播/结算回告发往已死旧 lobby——需在 P4c plan 仔细设计通知顺序与竞态（改投未达期间的帧/结算落点）。
- **D6 货币安全的前提**：依赖"玩家同时只在一个对局 + 对局期禁大厅消费"；若未来支持对局期并发消费或多局并行，需引入 escrow 冻结子态（已标范围外）。
- **结算回告可靠性**：赢家 lobby 在结算时可能宕机/重分配 → 结算事件走 JetStream at-least-once + 持久幂等键兜底（重投至某 lobby 消费）；需验证 lobby 重分配后结算不丢不重。
- **持久幂等发放设施形态未定（P4b 基石）**：④a 结算 + ① mail-claim 共用，是 P4b 正确性基石。形态候选——players 文档 `granted` 账本条件原子写（`{$ne:opID}→$push`，无需事务但改 grant 落库路径）/ 通用幂等 helper / Mongo 多文档事务（需 RS）——各有取舍，P4b plan 读 `src/common/mongo` 确认事务可用性后定。**这是 plan 阶段从 ① 暴露的关键设计点**（原 spec 低估为"简单反转"）。
- **MMR 凑桌 MVP**：单人基础 MMR，不做分桶扩容/组队/段位（范围外）；凑桌策略对 match 状态模型的影响留后续。
- **gen_routes / proto 体量**：P4 新增 match/room proto + 扩 online/lobby，注意与并行落地的重命名/重构 rebase（P1~P3 经验）。

---

## 14. 交付物速查

| 阶段 | 新建/扩充 | proto | 框架/通用 | 关键验收 |
|---|---|---|---|---|
| **pre-P4** | 加固 ⑤⑥ | — | gate req/rsp json/proto 边界 | req/rsp 边界定型；agent 转发单测 |
| **P4a** | matchsvr；roomsvr 开局骨架 | `match.proto`；扩 `online.proto`(BindRoom)/`lobby.proto`(MatchFound) | JetStream 接入；online room 绑定 | 凑桌→开局→lobby 拿 room 绑定；匹配请求不丢 |
| **P4b** | roomsvr 拍卖+结算；**① mail-claim 修复** | `room.proto`(Bid/AuctionState/Result+结算回告)；扩 `lobby.proto` | lobby 驱动广播(复用 PushToClient)；JetStream 结算事件；**持久幂等发放设施(④a+①)** | 出价改最高价+广播；结算赢家扣币得物幂等；① 崩溃窗口既不丢不双发；多局隔离 |
| **P4c** | 重连接回+补偿 | 扩 `lobby/online/room.proto`(重连查询/改投/补偿) | — | 5min 内重连接回原局；room 死→作废+补偿 |

---

## 附录：工作流约定

- 本 Spec 评审通过后，**每个子阶段**各自走 `writing-plans` 产出任务级 `docs/plans/2026-06-04-P4{a|b|c}-…-impl-plan.md`（或 `pre-P4-…`），每步含失败测试 + 实现 + 验证；再用 `subagent-driven-development` 逐任务 TDD 执行。
- 执行阶段：核心任务 spec+质量双评审 + 整支 `-race` 终审（P2/P3a/P3b 反复证明 verbatim 计划代码会偏离/不全，终审 + `-race` 必做）。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main；合入前先 rebase 最新 origin/main（注意并行落地的重命名/重构）。
- 文档维护：对 `architecture.md`/`network.md`/`cluster.md`/`development.md` 有影响的改动按 CLAUDE.md 约定同步（或在 P5 统一收口，但 PR 描述记欠账）。
- 集群 RPC 发送端硬编码 `proto.Marshal`，后端序列化器用 protobuf；存储用 BSON；gate 推送方向 body 用 client 序列化器(json)；JetStream 仅用于匹配请求 + 结算/发奖关键事件，实时广播走 core NATS。
