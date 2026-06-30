# 实现路线图：P2–P5 分阶段 Spec

> 状态：**路线 Spec（待评审）** · 日期：2026-06-02 · 适用：`project`（GameServer）
>
> 本文承接 [`docs/design/2026-06-01-target-architecture-design.md`](../design/2026-06-01-target-architecture-design.md)（目标架构设计稿）与 [`docs/plans/2026-06-01-P1-gateway-lobby-login.md`](2026-06-01-P1-gateway-lobby-login.md)（P1 已实现并合入 main，PR #9）。
>
> **本文只回答"每个阶段做什么、到哪算完成"**，不展开到任务级步骤。每个阶段进入实现前，单独走 `brainstorming → 该阶段 design spec → writing-plans → 执行` 的完整流程，产出各自的 `docs/plans/<date>-P{n}-*.md` 任务计划。

---

## 0. 总览

### 0.1 目标拓扑（回顾）

```
client ─TCP/WS─► gateway ─NATS─► lobby ─NATS─► router ═NATS═► { online, match, room }
        连接级 session       权威玩家状态        无状态转发        有状态微服务
                            (内存工作副本)       +横切中枢
                                  └──────────► MongoDB ◄────────┘（直连，无 dbsvr）
```

统一 **NATS** 总线 + **etcd** 发现 + **MongoDB** 直连；登录并入 lobby；无 dbsvr / 无 loginsvr / 无 Redis；单 world 不跨服。

### 0.2 已确认的全局决策（不再重议）

源自设计稿 §10 / §12，均已与用户确认：

- 不引入 Redis；登录并入 lobbysvr（gateway 仅"选 lobby + 转发 + 记绑定"）；单 world / 不做跨服。
- onlinesvr：一致性哈希分片 + 纯内存不落库 + 5min 判离线（兼作重连宽限窗口）。
- lobby 业务用 Entity-Component（**仅 lobby 用**，手写注册不走反射，组件间同步 event 通信；初始组件 = 背包/好友/邮件/货币）。
- router 始终在 lobby→微服务热路径上（解耦优先，接受一跳）。
- room：一实例多局、帧驱动；匹配用 MMR。
- 客户端协议无版本字段。
- 框架层生命周期单元已更名 `Component → Module`（已在 main）；"Component" 一词此后专指 lobby 的实体组件。
- 集群 RPC 发送端硬编码 `proto.Marshal`，故所有后端服务序列化器必须用 **protobuf**（gateway 客户端侧用 json）。

### 0.3 P1 基线（已交付）

- `lobbysvr`：`LobbyHandler.Login`（token 校验 stub，返回固定 uid + 节点 ID）、`LobbyModule` 生命周期占位、protobuf + NatsCluster 入口。
- `gatesvr`：登录改为经 `cluster.CallAnySync` 转发任一 lobbysvr，成功后 `Bind(uid)` + `BindNode("lobbysvr", nodeID)`；接入 NatsCluster。
- `protocal/lobby.proto`：`RPC_Login_Req/Rsp`（2001/2002）。
- framework：`handler.WithSessionID`。
- 集成测试（`//go:build integration`）：NATS+etcd 下 gateway→lobby 登录 RPC 端到端。

### 0.4 阶段依赖与顺序

```
P1(done) ──► P2(router+online) ──► P3(lobby EC + MongoDB) ──► P4(match+room 对局全链路) ──► P5(清理+框架backlog+文档)
                  │                      │                          │
                  │                      └─ 依赖 P2 的 online（登录加载玩家 + 在线注册落地）
                  └─ 接回 P1 登录流程（lobby 登录时经 router 注册 online）
```

P5 的"框架风险 backlog"与"文档同步"可在各阶段顺手部分完成，但统一在 P5 收口。

### 0.5 各阶段统一验收口径

每阶段至少满足：

1. `go build ./...`、`go vet ./...`、`go test ./src/... -count=1` 全绿（不依赖外部基础设施）。
2. 该阶段新增逻辑有单元测试（TDD：先写失败测试）。
3. 该阶段的端到端能力有 `//go:build integration` 集成测试（容器起 NATS+etcd[+MongoDB]）。
4. 涉及发奖/扣资源/结算/落库的路径**幂等**。
5. 对 `architecture.md`/`network.md`/`cluster.md`/`development.md` 有影响的改动，按 CLAUDE.md 约定同步（或在 P5 统一收口，但当阶段需在 PR 描述中记录欠账）。

---

## P2 — router + onlinesvr（无状态转发中枢 + 全局在线目录）

### 目标 / 里程碑

打通 **lobby ─► router ─► onlinesvr** 这条"经 router 调用有状态微服务"的纵切，并接回 P1 登录流程：玩家登录时，lobby 经 router 向 onlinesvr 注册在线；跨 gateway 重复登录可被检测并踢旧会话；可经 router 向 online 查询玩家位置。这是新架构第一次出现"router 转发"和"在线目录"两个核心设施。

### 范围内

- **router 服（新建，无状态，NATS queue-group N 实例）**
  - 在现有 `cluster` 之上做 `逻辑目标 → 具体实例` 的转发：lobby 把"调用某类微服务"的请求发到 router，router 据目标类型/路由 key 选实例（复用 `CallAny`/etcd 发现）转发并回传响应。
  - 横切能力**起步只做链路追踪透传 + 基础指标**（`traceID` 端到端已支持）；限流/灰度留到后续。
  - 部署形态：queue-group，无单点，可扩缩容。
- **onlinesvr 服（新建，有状态，纯内存）**
  - 全局在线目录：`uid → {在线状态, gateway/lobby/room nodeID, 登录时间, 最后活跃时间}`。
  - 一致性哈希分片：多实例，**调用方（lobby）按 uid 一致性哈希定位目标分片**。
  - RPC（起步集）：`Register`（登录注册/顶号）、`Query`（定位玩家）、`Unregister`（登出）、`Heartbeat`/`Touch`（刷新活跃）。
  - 5min 无活跃刷新判离线（`timewheel` 驱动过期清理）；本阶段先实现"过期清理 + 顶号"，完整"重连宽限接回"留到 P4（依赖 room 绑定）。
- **框架新增**
  - **router 客户端抽象**：让 lobby 以"经 router 调用微服务"的统一姿势发起 RPC（封装 subject/route 约定），后端服务侧无感。
  - **一致性哈希工具**（`src/common` 下新增，供 online 客户端定位分片；通用、可测）。
- **接回 P1 登录**：`LobbyHandler.Login` 成功后，经 router 调 onlinesvr `Register`；onlinesvr 检测到该 uid 已在线则触发踢旧（通知旧 gateway/lobby）。

### 范围外（延后）

- router 的限流 / 灰度发布 / 完整指标看板（后续迭代）。
- onlinesvr 的 presence 推送（好友在线变更广播）、全服在线数统计（P3 好友组件相关时再做）。
- 完整重连恢复接回（依赖 room，放 P4 §6.5）。
- JetStream（在线通知本阶段先走 core NATS，必要时 P4 引入 JetStream 时统一评估）。

### 关键设计决策（P2 brainstorm/plan 时敲定）

1. **router 转发的具体线机制**：lobby 发往 router 的 subject/route 如何编码"真实目标类型 + 业务 route"；router 如何解出目标并 `CallAny` 转发、如何回传响应（reply-subject 透传 vs 二次回调）；与现有 `forwardTable`/`CallAnyRaw` 的关系。
2. **一致性哈希细节**：哈希环虚拟节点数、分片成员来源（etcd discovery 的 onlinesvr 实例列表）、成员变动时的再平衡与一致性窗口。
3. **顶号协议**：onlinesvr 如何通知旧 gateway 踢连接（经 router 反向下发 vs 直达 gateway）；与现有 `session.Manager` 单 gateway 踢号如何衔接。
4. **online 在线条目的活跃刷新来源**：gateway 心跳如何传导到 onlinesvr（是否每跳都刷、还是周期批量）。

### 验收标准

- onlinesvr 多实例下，按 uid 一致性哈希路由稳定；某实例宕机仅影响其分片。
- 集成测试：登录 → lobby 经 router 向 online `Register` → `Query` 能定位玩家；同一 uid 第二次登录触发踢旧会话；登出/过期后 `Query` 不再命中。
- router 以 queue-group 起 ≥2 实例，请求被任一实例处理且响应正确回传。

### 风险 / 开放问题

- router 引入一跳延迟（设计已接受）；需确认 reply-subject 在"lobby→router→online→router→lobby"链路上的正确透传，避免回包丢失。
- 一致性哈希成员变动期的瞬时不一致（少量 uid 落到旧分片）——在线态纯内存，可接受短暂重建。

---

## P3 — lobby Entity-Component + MongoDB + 初始组件

### 目标 / 里程碑

让 lobby 成为真正的"权威玩家枢纽"：登录时从 MongoDB 加载玩家工作副本（替换 P1 的固定 uid stub），用 Entity-Component 框架组织玩家业务，至少一个组件（建议**背包**）走通"客户端 CS 请求 → lobby → 组件方法 → 改状态 → 落库 → 回包"的端到端闭环。

### 范围内

- **MongoDB 接入层（新建，通用，供 lobby/微服务复用）**：直连封装（连接管理、集合访问、序列化约定、超时/重试）；放 `src/common` 或 `src/framework` 下，单元可测（可用容器 Mongo 集成测试）。
- **lobby Entity-Component 框架（仅 lobby 用，新建于 lobbysvr 内）**
  - `Player` 实体 = `player_id` + 拥有的组件集合；**手写显式注册**（`player.AddComponent(NewBagComponent())`），不走反射。
  - 组件间通信走**进程内同步 event**（复用 `src/common/event`）；组件不直接互调。
  - lobby 框架级 Handler 收到 CS 消息后路由到对应玩家的组件方法（如 `CS_AddItem → player.Bag().Add(...)`）。
  - 玩家工作副本加载（登录/被分配时）、关键节点/周期 flush 回 MongoDB。
- **初始组件集**：背包(Bag) 先落地端到端；好友(Friend)/邮件(Mail)/货币(Currency) 起骨架（注册 + 加载/落库占位 + 各自 proto），逐个补业务。
- **proto**：扩充 `lobby.proto`（玩家 CS/SC 业务消息 + 组件相关 RPC）；重跑 `gen_routes`。
- **登录改造**：`LobbyHandler.Login` 用真实 token 校验占位 + MongoDB 加载玩家（uid 不再写死）；与 P2 的 online 注册衔接。

### 范围外（延后）

- 全部组件的完整业务（先背包闭环，其余按需增量）。
- 玩家数据的复杂迁移/版本化、分库分表（单 world，简单优先）。
- 跨玩家交互（好友私聊/邮件投递）依赖 online 定位，可在好友/邮件组件落地时再接 P2 的 `Query`。

### 关键设计决策（P3 brainstorm/plan 时敲定）

1. **EC 与 framework handler 的衔接点**：消息如何从 lobby Handler 路由到"具体玩家的具体组件方法"（按 uid 找 Player → 按 msgID/route 找组件方法）。
2. **玩家工作副本的内存管理**：Player 在 lobby 内存的生命周期（登录加载、空闲卸载、宕机恢复）、与 online 在线条目的关系。
3. **MongoDB 文档模型**：Player 及各组件的集合/文档结构、加载粒度（整玩家 vs 按组件懒加载）、flush 策略与幂等。
4. **同步 event 总线的组件解耦边界**：哪些跨组件交互走 event（如"买道具扣货币"= Bag 发事件、Currency 订阅）。

### 验收标准

- 集成测试（容器 NATS+etcd+MongoDB）：登录加载玩家工作副本；背包组件 CS 操作改状态并落库；重新登录能读回。
- 落库路径幂等（重复请求/重试不产生重复道具）。
- 至少背包组件有完整单测；EC 注册/event 分发有单测。

### 风险 / 开放问题

- EC 框架是 lobby 专用新设施，边界设计影响后续所有玩家功能；需在 P3 brainstorm 充分定型避免返工。
- MongoDB 加载/落库与 lobby 单 goroutine 帧驱动的衔接（异步 IO 回调经 `taskqueue` 投递回帧循环）。

---

## P4 — match + room + 对局全链路（含重连恢复）

### 目标 / 里程碑

打通**登录 → 匹配 → 进房 → 对局帧 → 重连接回**的对局全链路，这是整套架构的业务高峰。

### 范围内

- **matchsvr（新建，有状态）**：MMR 匹配队列，凑相近水平玩家成桌，凑齐后在某 room 实例开一局（`gameId`），回告各玩家所属 lobby「room#X / game G」。匹配请求走 **JetStream**（at-least-once，不能丢）。
- **roomsvr（新建，有状态，帧驱动）**：一实例多局（进程内 `gameId` 区分），`timewheel.Advance` + tick 主循环串行推进；对局帧广播走 **core NATS**；开局/结算落 MongoDB（幂等）。
- **lobby 侧**：发起匹配入口；持 `玩家↔room` 亲和绑定（`BindNode("room", X)`）并经 router 同步到 onlinesvr；对局消息经 router 转发 room（携带 `gameId`）。
- **重连恢复（设计稿 §6.5）**：重连可能落到另一 lobby；L2 从 MongoDB 加载工作副本 + 经 router 向 online 查"当前 room 绑定/在线态"，room 仍存活则重新绑定接回，否则走对局异常恢复。**补齐 P2 延后的 5min 重连宽限接回。**
- **JetStream 接入**：引入 JetStream 设施（匹配请求 + 结算/发奖/关键事件 at-least-once + 幂等）；明确 core vs JetStream 的边界（设计稿 §5.3）。
- **proto**：新建 `match.proto` / `room.proto`，扩充 online/lobby 相关消息；重跑 `gen_routes`。

### 范围外（延后 / MVP 取舍）

- **room 宕机对局级恢复**：MVP 为**对局作废 + 补偿**（不做对局 checkpoint 重建），设计稿 §8 标注需重点评审。
- 组队 / 段位匹配、复杂分桶扩容搜索策略（设计稿 §12 暂缓项，先单人 MMR 基础匹配）。
- room 单实例承载局数上限与开局调度/扩缩容（容量规划，后续迭代）。

### 关键设计决策（P4 brainstorm/plan 时敲定）

1. **匹配协议与 JetStream 用法**：匹配请求的投递/确认/重放、凑桌算法的 MVP 形态、回告 lobby 的可靠性。
2. **room 帧驱动与多局并存**：tick 频率、单实例多 `gameId` 的循环推进、帧广播扇出路径（room → 各玩家 lobby → gateway → client）。
3. **对局结算幂等**：发奖/结算的幂等键与一致性（内存/MongoDB/异步回调三者）。
4. **重连接回的判定与竞态**：window 内重连、room 存活判定、亲和绑定的权威来源（online vs lobby）。

### 验收标准

- 集成测试（容器 NATS+JetStream+etcd+MongoDB）：两个/多个客户端登录 → 匹配凑桌 → 进同一 room 的一局 → 互发对局操作 → 收到帧广播 → 一方掉线 5min 内重连接回原局。
- 结算落库幂等；匹配请求在 room/match 重启后不丢（JetStream 重放）。
- room 一实例并存多局互不串扰（按 `gameId` 隔离）的单测/集成验证。

### 风险 / 开放问题

- 全链路最复杂，建议拆成 P4a（匹配凑桌→开局）/ P4b（对局帧→结算）/ P4c（重连接回）多个子计划分别落地。
- JetStream 运维与语义（消费组、ack、重复投递）首次引入，需要专门验证。
- room 宕机 MVP 取舍需在 P4 启动前再次确认（影响补偿逻辑设计）。

---

## P5 — 清理 + 框架风险 backlog + 文档同步

### 目标 / 里程碑

收口技术债：删除被新架构淘汰的占位与示例，修复重构期识别出的框架现存风险，并把设计定稿同步回根目录文档体系，使代码库与文档一致、可长期维护。

### 范围内

- **淘汰 / 清理（设计稿 §9.3）**
  - 删除 `src/servers/dbsvr/`、`src/servers/loginsvr/`（无 dbsvr、无独立 loginsvr 的占位目录）。
  - 淘汰 `protocal/world.proto`、`gen/world` 及 routes 中的 world/scene 示例 → 已被 lobby/match/room/online 体系取代。
  - 清掉重构过程产生的孤儿（未用 import/变量/函数）。
- **框架现存风险 backlog（设计稿 §9.4，逐项修复 + 回归测试）**
  - 优雅停机 draining 不完整（`agent.requestsDone` 写入从不读取，`Stop()` 不等在途请求）。
  - `math/rand` 并发不安全（`CallAny` 选节点 → `math/rand/v2`）。
  - `taskqueue` 满静默丢任务（监控/扩容/降级策略）。
  - `nats_rpc` handler nil 无防护（运行时 panic）。
  - `discovery` `dieCh` 信号可能丢失（`select default`），故障态不退出。
  - TCP Acceptor `listen` 失败直接 panic（与 WS Acceptor 返回 error 不一致）。
  - 每条消息新建 `context.Background()`，断连后在途 handler ctx 不感知断连（无 cancel 传播）。
- **文档同步（CLAUDE.md 维护约定）**
  - 把设计定稿同步回 `architecture.md` / `network.md` / `cluster.md`：worldsvr/scenesvr 示例改写为 gateway/lobby/router/online/match/room 目标拓扑。
  - `development.md`：补 lobbysvr/onlinesvr/matchsvr/roomsvr/router 的构建测试命令、新增 proto/配置步骤、MongoDB/JetStream 依赖。
  - 更新文档索引与"基础事实"。
- **评审项收口**：把设计稿 §10 待定项 / §12 开放问题中已落地的标注为定稿，未落地的转为独立 issue/backlog。

### 范围外

- 新业务功能（P5 只做清理/加固/文档，不加功能）。
- 大规模性能优化 / 压测（可另立专项）。

### 关键设计决策（P5 plan 时敲定）

1. 框架风险逐项修复的优先级与是否需要接口/行为变更（如 draining 改动可能影响 `Stop()` 语义）。
2. 文档改写的粒度（一次性重写 vs 增量）。

### 验收标准

- 删除占位后 `go build ./...` / `go test ./...` 全绿；无悬挂引用。
- 每个框架风险项有对应修复 + 回归测试，并在 PR 描述中逐项对应 §9.4。
- 文档与代码库实际状态一致（目录、命令、拓扑、proto/配置步骤）。

### 风险 / 开放问题

- 框架风险修复涉及核心生命周期/并发路径，需小步提交 + 充分测试，避免回归。
- 文档同步量大，建议按 architecture/network/cluster/development 分 PR。

---

## 附录 A：各阶段产物速查

| 阶段 | 新建服务 | 新建/扩充 proto | 框架/通用新增 | 关键集成测试 |
|---|---|---|---|---|
| P2 | router、onlinesvr | online.proto；扩充 lobby.proto | router 客户端抽象、一致性哈希工具 | 登录→router→online 注册/查询/顶号 |
| P3 | （无新服务） | 扩充 lobby.proto（玩家/组件） | MongoDB 接入层、lobby EC 框架 | 登录加载玩家 + 背包组件落库回读 |
| P4 | matchsvr、roomsvr | match.proto、room.proto | JetStream 接入 | 登录→匹配→进房→对局帧→重连接回 |
| P5 | （删除 dbsvr/loginsvr 占位） | 淘汰 world.proto | 框架风险 backlog 修复 | 框架风险回归测试 |

## 附录 B：工作流约定

- 每阶段进入实现前，独立完成 `brainstorming → docs/design 或 docs/plans 下的该阶段 spec → writing-plans 任务计划 → 执行`，并经用户评审。
- 分支纪律：feature 分支 + PR 合入 main，禁止直接 push main（P1 经验：合入前先 rebase 最新 main，注意并行落地的重命名/重构）。
- 测试纪律：从设计意图/本文档与设计稿推导用例，不从实现反推；约定与实现冲突先暂停报告。
