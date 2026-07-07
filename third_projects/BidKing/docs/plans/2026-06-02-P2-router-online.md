# P2 设计 Spec：router + onlinesvr（无状态转发中枢 + 全局在线目录）

> 状态：**设计 Spec（已评审通过，待出任务计划）** · 日期：2026-06-02 · 适用：`project`（GameServer）
>
> 承接 [`docs/design/2026-06-01-target-architecture-design.md`](../design/2026-06-01-target-architecture-design.md)（目标架构设计稿，§3.3 router / §3.7 onlinesvr / §6.1 登录）与 [`docs/plans/2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md)（P2 段）。P1（gateway→lobby 登录纵切）已合入 main（PR #9），本 Spec 在其上接回登录的"在线注册/顶号/定位"。

---

## 1. 目标 / 里程碑

打通 **lobby ─► router ─► onlinesvr** 这条"经 router 调用有状态微服务"的纵切，并接回 P1 登录流程：

- 玩家登录时，lobby 经 router 向 onlinesvr **注册在线**（uid → gateway/lobby 位置）。
- 跨 gateway 重复登录可被检测并**踢旧会话**（顶号）。
- 可经 router 向 online **查询玩家位置**（Query）。
- 断连 / 登出 **注销**（Unregister）；5min 无活跃刷新**过期清理**（兼作重连宽限窗口的雏形，完整重连接回留 P4）。

这是新架构第一次出现「router 转发」与「在线目录」两个核心设施。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）

| # | 决策点 | 结论 | 理由 |
|---|---|---|---|
| D1 | onlinesvr 一致性哈希由谁算 | **router 计算** | lobby 只传 `{target=onlinesvr, key=uid, route}`，router 持 online 成员表按 uid 选分片。统一了设计稿 §3.3「lobby 不感知微服务拓扑」与 §3.7（§3.7 的"调用方"实为 router 这个 online 客户端）。 |
| D2 | lobby→router 多实例负载均衡 | **复用 `CallAny`+etcd** | router 实例是普通 cluster 节点（各自 NodeID subject），lobby 用现有 `CallAny("routersvr", ...)` 随机选实例。无需在 transport 层加 NATS queue-group 订阅。满足"无单点、可扩缩容"。 |
| D3 | router 转发处理模型 | **异步、不串行** | router 入站消息每条独立 goroutine 处理（opt-in `asyncDispatch`，默认关），转发互不阻塞。详见 §4.2。 |
| D4 | router 转发并发上限 | **MVP 不封顶** | 每条转发起 1 个 goroutine（廉价），彻底不串行；并发封顶/背压列为后续加固项。 |
| D5 | 顶号踢旧会话下发路径 | **onlinesvr 直达旧 gateway** | online 是在线态权威，从在线条目取旧 gateway nodeID，直接 `Cast` 踢号通知；最短路径，不绕 router 反向寻址。 |
| D6 | 在线活跃刷新来源 | **注册/登出 + 活跃 touch；timewheel 过期兜底** | 登录 Register、优雅断连 Unregister、玩家有消息往来时 lobby 顺带 Touch；5min timewheel 过期仅作崩溃/非优雅断连兜底。消息量低、简单。 |
| D7 | 一致性哈希算法 | **Jump Consistent Hash** | 无环/无虚拟节点、零内存、计算快、分布均衡；成员按 NodeID 升序确定性编号。取舍：尾部增删最优，非尾部宕机迁移较多 uid（在线态纯内存可接受，见 §12）。 |

> 沿用全局既定决策（设计稿 §10）：不引入 Redis；登录并入 lobby；单 world 不跨服；后端服务序列化器用 protobuf；"Component" 专指 lobby 实体组件，框架生命周期单元叫 Module。

---

## 3. 组件与职责

### 3.1 routersvr（新建，无状态，≥2 实例）

- 唯一职责：`逻辑目标 → 具体实例` 的转发 + 一致性哈希选址 + 基础指标/trace 透传。
- **无业务 Handler**，只有一个转发 Handler `RouterHandler.forward`。
- 构建在统一框架底座上（Application/Module 生命周期 + cluster 接入），但不接客户端（无 `Frontend()`）、不接 MongoDB、无帧循环。
- 起 `asyncDispatch=true`：入站转发并发处理、不串行（§4.2）。
- 转发时按 `target_type` 从 discovery 实时读取实例列表，用无状态 `jumphash.Pick(members, key)` 选目标分片（无缓存 Picker / 无 SDListener——discovery 即成员真相，始终一致）；P2 只路由 `onlinesvr`。

### 3.2 onlinesvr（新建，有状态，纯内存，一致性哈希分片）

- 全局在线目录：`uid → OnlineEntry{ state, gatewayNodeID, lobbyNodeID, loginTime, lastActiveNano }`（`roomNodeID` 字段预留给 P4，本阶段不用）。
- RPC：`Register` / `Query` / `Unregister` / `Touch`。
- timewheel 驱动 5min（可配）无活跃过期清理。
- 检测重复登录并触发顶号（§6）。
- 纯内存，不落 MongoDB；重启后由后续登录/重连重建（设计稿 §3.7）。
- 多实例，每实例只持有「Jump Hash 选中它」的那部分 uid 的条目（实例无需知道自己的"区间"，只存收到的 Register）。

---

## 4. router 转发线机制

### 4.1 信封与双向协议（`protocal/router.proto`）

```proto
enum RoutingMode {
  ROUTING_CONSISTENT_HASH = 0; // 按 routing_key 一致性哈希选分片（P2 用：online）
  ROUTING_ANY             = 1; // 随机一个该类型实例（预留 P4：match）
  ROUTING_DIRECT          = 2; // routing_key 即目标 NodeID 串（预留 P4：room）
}

message RPC_RouterForward_Req {
  string      target_type  = 1; // 目标服务类型名，如 "onlinesvr"
  RoutingMode routing_mode = 2;
  string      routing_key  = 3; // CONSISTENT_HASH: uid 串；DIRECT: NodeID 串；ANY: 忽略
  string      inner_route  = 4; // 真实业务 route，如 "OnlineHandler.register"
  bytes       inner_data   = 5; // 已序列化的真实业务请求
}

message RPC_RouterForward_Rsp {
  int32 code       = 1; // 0=ok；非 0=router 侧错误（无目标/解析失败等）
  bytes inner_data = 2; // 真实业务响应原样透传
}
```

- 走**标准 typed Registry handler**，不碰传输内部；信封显式承载真实目标，router 不感知 inner_data 内容（payload-agnostic）。
- `routing_mode` 预留 ANY/DIRECT，避免把 "onlinesvr" 硬编进 router；P2 只实现 `CONSISTENT_HASH`（其余可留 stub + 明确 error）。

### 4.2 异步转发（传输层 opt-in `asyncDispatch`）

现状根因：`nats_rpc.go onMessage`（:150）**同步**调 `r.handler(...)` 再 `Publish` 回包，handler 一阻塞即卡住订阅 goroutine → 串行。

改造（最小、opt-in、默认关）：

```go
// NatsRPC 新增字段 asyncDispatch（经 NatsClusterConfig.AsyncDispatch 注入）
func (r *NatsRPC) onMessage(natsMsg *nats.Msg) {
    if r.asyncDispatch {
        go r.handleMessage(natsMsg) // 每条独立 goroutine，订阅 goroutine 立即取下一条
        return
    }
    r.handleMessage(natsMsg)
}
// handleMessage = 原 onMessage 函数体（解信封→重建 ctx→handler→回包），逻辑不动
```

- **默认关**：gate/lobby/onlinesvr 仍单 goroutine 顺序处理，保住"同目标有序"与帧驱动语义。**仅 routersvr 开**。
- `defer cancel()`（deadline）落在 `handleMessage` 内，随 goroutine 结束触发，**deadline 透传语义不变**。
- 资源：每个在途转发 ~1 个 goroutine（与 `CallAsync` 本就会起的同级），MVP 不封顶。

### 4.3 转发流程

```
lobby: routerclient.CallVia[*RPC_xxx_Rsp](ctx, cls, "onlinesvr", CONSISTENT_HASH, uidStr, "OnlineHandler.register", req)
  └ 内部：marshal req → RPC_RouterForward_Req{...} → cluster.CallAny(ctx, "routersvr", "RouterHandler.forward", 信封, done)
router(某实例, asyncDispatch goroutine): RouterHandler.forward(ctx, fwd)
  ├ CONSISTENT_HASH: picker["onlinesvr"].Get(fwd.routing_key) → online NodeID（空集 → 返回 code!=0 错误）
  ├ respData, err := cluster.CallRawSync(ctx, onlineNodeID, fwd.inner_route, fwd.inner_data)   // 同步拿 online 响应；deadline 透传
  └ return &RPC_RouterForward_Rsp{code, inner_data: respData}（或 err）
框架: 把 _Rsp 回包给 lobby → routerclient 解包 inner_data → unmarshal 成 *RPC_xxx_Rsp 还给业务
```

形成 `lobby→router→online→router→lobby` 回链（与路线图所画一致），且零传输层订阅改动（只加 `asyncDispatch` 开关 + `CallRawSync` 薄助手）。

### 4.4 框架新增（均为薄封装，无传输订阅改动）

- `NatsClusterConfig.AsyncDispatch bool` → 透传到 `NewNatsRPC` → `NatsRPC.asyncDispatch`；`onMessage` 拆出 `handleMessage`。
- `Cluster.CallRawSync(ctx, target NodeID, route string, data []byte) ([]byte, error)`（接口 + NatsCluster 实现，底层复用现有 `rpc.Call`，即 `RequestWithContext`）。
- `src/common/jumphash`（Jump Consistent Hash，见 §5）。
- `routerclient` 助手（lobby 侧统一发起姿势，封装信封 + `CallAny("routersvr", ...)` + 解包）。放 `src/framework/cluster`（与 `rpc_helpers.go` 同包，或子包 `routerclient`），因其封装 `Cluster`，故归属 cluster 包而非 `src/common`；通用可测。

---

## 5. 一致性哈希工具 `src/common/jumphash`（Jump Consistent Hash，通用、可测）

采用 **Jump Consistent Hash**（Lamping & Veach, 2014）：无环、无虚拟节点、零额外内存、计算极快（核心几行）、分布均衡。

```go
// Jump 纯算法：key → [0, numBuckets) 桶号（numBuckets<=0 返回 -1）
func Jump(key uint64, numBuckets int) int32

// Pick 把 members 去重升序后，用 Jump(fnv1a64(key)) 选一个成员；空集返回 ("", false)。
// 排序保证不同 router 实例对同一 key 选出同一成员（与传入顺序无关）。
// 无状态、免缓存：router 每次转发从 discovery 实时取 onlinesvr 成员传入。
func Pick(members []string, key string) (string, bool)
```

- `key → uint64`：对 `routing_key` 串做 FNV-1a 64（uid 串分布良好），确定性、无第三方依赖。
- router 用法：每次转发用 `disc.ByType("onlinesvr")` 取当前实例 NodeID 列表，调 `jumphash.Pick(members, uid)` 选分片。discovery 即成员真相（watch+周期对账维护），**无共享可变状态、免锁、免 SDListener**。
- ⚠️ Jump Hash 取舍（见 §12）：**尾部增删**（扩缩到 N±1 且变动的是排序最末实例）仅迁移 ~1/N 的 key、均衡最优；**非尾部实例宕机**会使其后实例桶号顺移、导致较多 `uid→实例` 映射变动。在线态纯内存、重建廉价，可接受；典型扩缩为尾部操作。

---

## 6. onlinesvr 内部

### 6.1 状态与并发

- `directory map[int64]*OnlineEntry` + `sync.Mutex`。
- Register 是"查旧→踢旧→覆盖"的复合临界区，需原子；KV 目录用 mutex 比单循环帧驱动更简单（帧驱动留给 room 的重 tick 逻辑）。
- timewheel 自驱动（`tw.Start()`），过期回调加同一把锁删除条目 → 与 RPC handler 串行，无数据竞争。

### 6.2 RPC（`protocal/online.proto`，handler = `OnlineHandler.*`）

| RPC | 入参 | 行为 | 幂等性 |
|---|---|---|---|
| `Register` | `uid, gatewayNodeID, lobbyNodeID` | 有同 uid 旧条目且 gateway/会话不同 → 触发顶号（§6.3）→ 覆盖为新位置；调度/重置该 uid 的过期定时器 | 同位置重复注册 = 幂等刷新 |
| `Query` | `uid` | 返回条目（state + gateway/lobby nodeID）或未命中 | 只读 |
| `Unregister` | `uid` | 删除条目 + 取消过期定时器 | 不存在 = no-op（幂等） |
| `Touch` | `uid` | 刷新 `lastActiveNano` + 重置过期定时器 | 幂等 |

### 6.3 过期（5min，可配）

- 配置项 `online.entry_ttl_sec`，默认 300；**测试用短 TTL** 免等 5 分钟。
- Register/Touch 时 `tw.AfterFunc(ttl, expire(uid))`（重置：先 `tw.Stop` 旧 timer 再建新）。
- 到期回调：加锁、校验"自上次活跃确已超 ttl"（防 Touch 与到期竞态）后删除条目。该 5min 同时是重连宽限窗口的雏形（完整接回 P4）。

---

## 7. 顶号流程（D5：onlinesvr 直达旧 gateway）

```
online.Register(uid, newGw, newLobby) 发现旧条目 old{oldGw, oldLobby} 且 oldGw/会话 != newGw:
  1. Cast RPC_KickSession_Notify{uid, reason=DUP_LOGIN} 直达 oldGw（online 知道 oldGw nodeID）
  2. Cast RPC_PlayerOffline_Notify{uid} 给 oldLobby（清理绑定；P2 lobby 态极少，先打通通道）
  3. 覆盖 directory[uid] = new{newGw, newLobby}，调度过期定时器，返回成功给新登录
```

**gateway 新增 `KickByUID` 处理**（`RPC_KickSession_Notify`，notify 无返回）：

```
gateway.KickSession(ctx, req):
  s, ok := Sessions().ByUID(req.uid)   // session.Manager 新增 ByUID 访问器
  若命中: 给客户端发 SC_Kicked_Notify（被顶下线，附原因）→ Sessions().Close(s)
  若未命中: 记日志返回（连接可能已在别处断开）
```

- `session.Manager` 现有 `Bind` 已做**单 gateway 内**同 uid 踢旧（`byUID.Swap` → `Close`）；跨 gateway 由 onlinesvr 全局协调，gateway 侧补 `ByUID` + `KickByUID` handler + 客户端 `SC_Kicked_Notify`。
- 竞态：新会话已在 online 登记为权威；对旧 gateway 的 kick 只是关停过期连接，客户端收到"已在别处登录"。

---

## 8. 关键数据流接回

### 8.1 登录（接 P1）

```
client → gateway: CS_Login_Req（P1）
gateway → lobby: CallAnySync("lobbysvr", "LobbyHandler.login", RPC_Login_Req)（P1，携带 ClusterSession{id, uid, ip, frontend_id}）
lobby.Login:
  token stub 出 uid（P1 不变）
  从 ctx 的 ClusterSession 取 frontend_id(=gateway NodeID) + sessionID
  routerclient → online.Register{uid, gatewayNodeID=frontend_id, lobbyNodeID=self}
    └ online 内含顶号（§7）
  返回 RPC_Login_Rsp{uid, lobbyNodeId=self}（P1 不变）
gateway: Sessions().Bind(uid) + s.BindNode("lobbysvr", lobbyNodeId)（P1 不变）→ 回客户端
```

### 8.2 断连注销（D6）

```
gateway session 关闭（OnClose 回调）: 若已绑定 uid + lobby:
  Cast RPC_PlayerDisconnect_Notify{uid} 给绑定的 lobby NodeID
lobby.OnPlayerDisconnect: routerclient → online.Unregister{uid}（+ 清理自身绑定）
崩溃路径（gateway 宕机无 OnClose）: 靠 online 5min timewheel 过期兜底
```

### 8.3 活跃刷新（D6）

- lobby 处理玩家消息时顺带 `routerclient → online.Touch{uid}`（P2 玩家业务消息尚少，主要由集成/单测直接驱动覆盖；P3 玩家功能上来后自然高频）。

---

## 9. proto / routes / 生成

- 新增 `protocal/router.proto`（§4.1 信封 + RoutingMode）。
- 新增 `protocal/online.proto`（4 RPC 的 Req/Rsp，带 `handler_method` option = `OnlineHandler.*`）。
- 新增通知消息：`RPC_KickSession_Notify`（online→gateway）、`RPC_PlayerOffline_Notify`（online→旧 lobby）、`RPC_PlayerDisconnect_Notify`（gateway→lobby）；客户端 `SC_Kicked_Notify`（gateway→client，走 gate 协议序列化器 json）。
- **路由说明**：online/router 的服务间 RPC 走 `lobby→router→online`，由信封路由，**不经 gate 的 `ForwardTable`**（ForwardTable 仅服务于 client→gate→msg_id 的转发）。这些 RPC 的 route 串由 `RegisterHandler` 反射生成（`OnlineHandler.register` 等）；`msg_id` option 仅用于唯一性/协议编号，给独立号段（如 online 3001+、router 3101+），不参与 gate 转发。
- 重跑 `go run ./tools/gen_routes`。protoc 借用 `/game/dev/silver-server/tools/server_excel_tool/protoc`，include 目录 `/game/dev/silver-server/3rd/protobuf/include`（见开发环境约定）。

---

## 10. 测试与验收

### 10.1 单元（不依赖外部基础设施，`go test ./src/...`）

- `jumphash`：`Jump` 桶号分布均衡；`Pick` 同 key 稳定映射、跨实例顺序一致（乱序成员同结果）、去重稳定；尾部增删迁移 ~1/N；空集返回 false。
- router 目标解析：CONSISTENT_HASH 命中/空环错误；ANY/DIRECT 的 stub 行为明确。
- onlinesvr：Register 首登/顶号/同位置幂等；Query 命中/未命中；Unregister 幂等；Touch 刷新；过期清理（短 TTL + 手动推进或注入时钟）；Touch 与过期的竞态。
- `session.Manager.ByUID` 命中/未命中；gateway `KickByUID` 查找+关停路径。

### 10.2 集成（`//go:build integration`，容器起 NATS+etcd）

放各服务自己包内（Go internal 可见性），`go vet -tags integration ./...` 编译验证（本沙箱无 Docker，实跑需有 Docker 的机器 `docker compose -f test/docker-compose.yaml up -d`）。

- 登录 → lobby 经 router → online.Register → Query 能定位玩家（gateway/lobby nodeID 正确）。
- 同一 uid 第二次登录（模拟另一 gateway）→ 旧 gateway 会话被踢（收到 `SC_Kicked_Notify` / 连接关闭）。
- 断连 → Unregister → Query 落空；或过期后 Query 落空。
- routersvr 以 ≥2 实例起，请求被任一实例处理且响应正确回传（验证 asyncDispatch 不串行 + CallAny 分摊）。

### 10.3 验收口径（路线图 §0.5）

- `go build ./...`、`go vet ./...`、`go test ./src/... -count=1` 全绿。
- 新增逻辑 TDD（先写失败测试）。
- 端到端能力有 integration 测试。
- 发奖/扣资源类幂等——本阶段无发奖，但 Register/Unregister/Touch 均幂等。
- 文档影响（`cluster.md` 新增 router/online、`development.md` 新增两服务构建测试命令）按 CLAUDE.md 约定同步，或在 PR 描述记欠账、P5 统一收口。

---

## 11. 范围外（按路线图延后）

- router 限流 / 灰度 / 完整指标看板。
- onlinesvr presence 推送（好友在线变更）、全服在线数（P3 好友组件相关时）。
- 完整重连宽限接回（P4，依赖 room 绑定）。
- JetStream（在线通知本阶段走 core NATS）。
- ANY/DIRECT 路由的实际使用（P4 match/room）；router 转发并发封顶/背压（后续加固）。
- onlinesvr 实例宕机时该分片在线态重建的主动策略（纯内存，靠后续登录/重连自然重建）。

---

## 12. 风险 / 开放问题

- **router 异步转发的 goroutine 不封顶**：极端高压下 goroutine 数随在途转发增长。MVP 接受（goroutine 廉价 + online 操作内存级），加固项=信号量背压（溢出回退同步，不丢消息）。
- **Jump Hash 成员变动**：尾部增删仅迁移 ~1/N uid（最优）；**非尾部实例宕机**会使其后实例桶号顺移、迁移较多 `uid→实例` 映射。叠加成员变动传播期（etcd watch 有先后），不同 router 实例可能短暂对同一 uid 选不同分片。两者均因在线态纯内存而可接受短暂重建（设计稿 §8 已接受）。
- **顶号与新登录的竞态**：新会话先在 online 登记为权威，再踢旧 gateway；需确保 Register 的"查旧→踢→覆盖"在 online 单锁内原子完成，避免两次并发登录互踢。
- **断连 Unregister 依赖 gateway OnClose 链路**：gateway→lobby→router→online 多跳；任一跳失败则靠 5min 过期兜底，不影响正确性（最终一致）。

---

## 13. 交付物速查

| 类别 | 新增/改动 |
|---|---|
| 新服务 | `src/servers/routersvr`、`src/servers/onlinesvr`（含 main.go + conf） |
| proto | `protocal/router.proto`、`protocal/online.proto` + kick/offline/disconnect/SC_Kicked 通知消息；重跑 gen_routes |
| 通用 | `src/common/jumphash`（Jump Consistent Hash） |
| 框架 | `NatsClusterConfig.AsyncDispatch` + `onMessage` 拆 `handleMessage`；`Cluster.CallRawSync`；`session.Manager.ByUID`；`routerclient` 助手 |
| 业务接回 | `lobbysvr`：登录后 Register、断连 Unregister、活跃 Touch；`gatesvr`：OnClose→通知 lobby、`KickByUID` handler + `SC_Kicked_Notify` |
| 测试 | jumphash/router/online/session 单测；登录→router→online 注册/查询/顶号/注销 集成测试（`//go:build integration`） |
