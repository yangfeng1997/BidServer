# P5-① 设计 Spec：跨字段 flush 原子性（折单原子 `$set` + done 门控）

> 阶段：P5 第一切片。前置：P4（匹配+对局+结算+重连）已全部合入 main（P4c code PR #26 merge `021e790`）。
> 本切片修 P4b/P4c 反复点名「延后 P5」的已知边界：`flushPlayer` 无跨字段原子性，且 `after`/ack 在写**失败**路径仍触发，违背 §6.6「持久落库后才 ack/清理」。
> 沙箱可全单测验证，无 Docker 依赖。

---

## 1. 目标 / 范围

### 1.1 范围内
- **Part B 折单原子 `$set`**：`flushPlayer` 把同一次 flush 的**全部脏组件**折成**一个** `UpsertSetByID`（单文档多字段 `$set` 原子）。消除「players 文档内 currency `$set` 成、bag `$set` 败」的瞬时不一致。
- **Part A done 门控**：`flushPlayer` 的 `after` 由 `func()` 改为 **`func(ok bool)`**，写失败 `ok=false`、成功 `ok=true`。各跨 collection / 跨进程提交点（settle ack / offline `$pull` / player 剔除 / mail mark-claimed / client reply）**仅在 `ok=true` 时执行**，失败路退化为「自然重试 + opID 幂等」。补齐 §6.6 在失败路的违背。
- 附带：folding 后 `flushPlayer` 的 `pending` 计数与 `afterDone` fire-once guard **整体删除**（单回调天然 once），逻辑显著简化。
- 全程单元测试（故障注入 fake `DocStore`）+ 文档同步。

### 1.2 范围外（延后 / 后续）
- **跨 collection 真原子**：`offline_messages` 的 `$pull`、`mailbox` 的 mark-claimed 写到**别的 collection**，`src/common/mongo` 无事务 ⇒ 本就**只能**靠 done 门控 + at-least-once 重试 + opID 幂等，不在「真原子」范畴。
- roomsvr / matchsvr / onlinesvr：roomsvr 不持久玩家经济（纯 RPC），其余无 players 文档 flush。**blast radius 全在 `src/servers/lobbysvr/internal`**。
- 持久 ops 环 128 扩容/淘汰（P5 其他候选 ⑤）、mailbox 归档分页（③）、presence 批量（④）、集成测试去 `t.Skip`（②，需 Docker）。

---

## 2. 已敲定的设计决策（本次 brainstorm 确认）
1. **切片 = ①**（正确性修复，唯一纯单测可在本沙箱全验的候选）。
2. **修法 = done 门控 + 折单原子 `$set`**（两者**非二选一**：done 门控盖缺口 A 且为跨 collection 唯一手段；折单 `$set` 盖 players 文档内缺口 B）。
3. **接口改 `FlushField(field, state)` → `FlushFields(fields map[string]any)`**（单调用点，blast radius 小）；`after func() → func(ok bool)`。
4. **三处行为变更已拍板**：
   - Disconnect 写失败**不剔除**玩家（顺带修掉现有 data-loss 隐患：当前无条件 `delete` 在写失败时丢弃脏内存态）。
   - replayOffline 写失败**仍续登录**（grants 在内存、offline 消息留存下次登录重放 dedup，不因 mongo 瞬时抖动阻塞登录）。
   - Mailclaim 写失败 **reply `Code:1`**（客户端重领，opID=mailID 幂等）。

---

## 3. 验证的现状缺口（动手前已读码核实，main @ `021e790`）
- **`flushPlayer`**（`runtime.go:267`）：逐脏组件各发一次 `rt.store.FlushField(uid, comp.Field(), state, …)`；`pending` 计数到 0 即 `fireAfter()`，**失败路只 `comp.MarkDirty()` 后仍 `pending--`→`fireAfter`**。⇒ 缺口 A：§6.6 在失败路被违背。
- **`DocStore.FlushField`**（`store.go:29`）+ **`mongoStore.FlushField`**（`store.go:49-50`）：`UpsertSetByID(uid, bson.M{field: state})` —— **每组件一次单字段 `$set`**。⇒ 缺口 B：跨字段无原子性。`src/common/mongo` `UpsertSetByID`（`mongo.go:79`）无事务，仅单文档原子。
- **四个 `after` 站点**（均「落库后才…」注释，但失败路误触发）：
  - `runtime.go:750` Settle：`after = done(0)` ack 给 room（room 据此停止重投）。
  - `runtime.go:619` replayOffline：`after = Ack($pull) + 续登录 after()`。
  - `runtime.go:235` Disconnect：`after = delete(rt.players, uid)`。
  - `lobby_handler.go:250` Mailclaim：`after = MarkClaimed + reply 成功`。
- **Login 复用**（`runtime.go:193`）：`if _, ok := rt.players[uid]; ok { …reuse… }` ⇒ 断连未剔除的玩家在重连时**被复用**（携最新内存态）。**这是 Disconnect 失败不剔除安全的关键依据**：未剔除玩家被重连无害接管，或经 coalesce/drain 落库。
- **`Component` 接口**（`player.go:5`）：`Name()/Field()/Snapshot()/Dirty()/ClearDirty()/MarkDirty()` —— folding 所需方法齐备。
- **`PlayerDoc`**（`store.go:13`）：Bag/Currency/Friend/Rating 内嵌**同一** players 文档 ⇒ 折单 `$set` 对全部脏组件成立（非 currency+bag 专属）。
- **`inflight` 计数 + `drain`**（`runtime.go:177`）：停机排空等 `inflight==0`；folding 后一次批量写 = 一个 inflight，语义不变。

---

## 4. 架构与修法

### 4.1 Part B — 折单原子 `$set`
- **接口**：`DocStore.FlushField(d, uid, field string, state any, done)` → `FlushFields(d, uid, fields map[string]any, done func(error))`。
- **mongoStore**：`FlushFields` 把 `map[string]any` 转 `bson.M` 一次 `UpsertSetByID(uid, set, done)`。单文档多字段 `$set` 原子：全部字段同落或同不落。
- **持久 ops 同原子**：`CurrencyState.Ops` / `BagState.Ops` 是各自 `Snapshot()` 的一部分 ⇒ **opID 与余额/物品同一 `$set` 落库**。扣减未落则 ops 也未落（§6.6 文档内 airtight）。

### 4.2 Part A — done 门控 `after func(ok bool)`
- `flushPlayer(uid, p, after func(ok bool))`：收集全部脏组件 → 一次 `FlushFields` → 单回调内 `ok := err==nil`；失败 `MarkDirty` 全批 + `after(false)`，成功 `after(true)`。
- `pending`/`afterDone` 删除（单回调天然 once）。

### 4.3 逐站点失败语义表（核心）
| 站点 | `after(true)` 成功路 | `after(false)` 失败路 | 失败安全依据 |
|---|---|---|---|
| Settle (`runtime.go:750`) | `done(0)` ack | **`done(1)` → room 重投** | opID=gameId 持久 dedup，重投恰一次 |
| replayOffline (`runtime.go:619`) | `Ack`(`$pull`) + 续登录 | **跳过 `$pull`、仍续登录** | offline 消息留存，下次登录重放经 opID dedup |
| Disconnect (`runtime.go:235`) | `delete(rt.players)` | **不剔除** | Login 复用（`runtime.go:193`）/ coalesce / drain 落库 |
| Mailclaim (`lobby_handler.go:250`) | `MarkClaimed` + reply 成功 | **reply `Code:1`** | opID=mailID 持久 dedup，客户端重领恰一次 |
| coalesce / flushAllDirty | `nil`（无 after） | — | — |

---

## 5. 组件与职责
### 5.1 `store.go`
- `DocStore` 接口：`FlushField` → `FlushFields(d, uid, fields map[string]any, done func(error))`。
- `mongoStore.FlushFields`：`map[string]any` → `bson.M` 一次 `UpsertSetByID`。
- 编译期断言 `var _ DocStore = (*mongoStore)(nil)` 保留。

### 5.2 `runtime.go`
- `flushPlayer`：折单批量写 + `after func(ok bool)` 门控（见 §6.1 伪码）；删 `pending`/`afterDone`。
- `flushAllDirty` / `coalesceFlush`：`flushPlayer(uid, p, nil)` 不变（nil after 合法）。
- `Disconnect`：`after = func(ok){ if ok { delete(rt.players, uid) } }`。
- `replayOffline`：`after = func(ok){ if ok { Ack }; continueLogin() }`。
- `Settle`：在线赢家 `after = func(ok){ if ok { done(0) } else { done(1) } }`。

### 5.3 `lobby_handler.go`
- `Mailclaim`：`after = func(ok){ if !ok { reply Code:1; return }; MarkClaimed; reply 成功 }`。

### 5.4 测试 fake
- 单测 fake `DocStore` 实现 `FlushFields`，支持**故障注入**（按调用序号 / 按字段名 返错），用于断言原子性 + 门控 + 重试收敛。

---

## 6. 关键算法 / 流程 / 不变式

### 6.1 `flushPlayer` 新逻辑（伪码）
```go
func (rt *Runtime) flushPlayer(uid int64, p *Player, after func(ok bool)) {
    fields := make(map[string]any)
    var dirty []Component
    for _, c := range p.Components() {
        if !c.Dirty() { continue }
        fields[c.Field()] = c.Snapshot()
        c.ClearDirty()
        dirty = append(dirty, c)
    }
    if len(fields) == 0 {        // 无脏：视为成功（无需落库）
        if after != nil { after(true) }
        return
    }
    rt.inflight.Add(1)
    rt.store.FlushFields(rt.tq, uid, fields, func(err error) {
        defer rt.inflight.Add(-1)
        ok := err == nil
        if !ok {
            for _, c := range dirty { c.MarkDirty() }   // 重置脏，下次重试
            logger.Warn("lobby flush failed", logger.Int64("uid", uid), logger.Err(err))
        }
        if after != nil { after(ok) }
    })
}
```
要点：全部回调在主循环 goroutine 串行执行，零锁；snapshot 取值后若组件再变脏（另一主循环任务），失败 `MarkDirty` 与该变脏幂等叠加，成功路不触碰脏（新变更下次 flush），无丢写。

### 6.2 §6.6 延续：「持久落库后才 ack/清理」（失败路补齐）
P4b §6.6 要求 at-least-once 消费一律 `flush(after→ack/清理)` 立即落库。本切片把「落库后」做实到**失败路**：写失败 ⇒ `ok=false` ⇒ ack/`$pull`/剔除/mark-claimed **均不发生**。跨 collection 提交（`$pull` / MarkClaimed）仍是「players 文档原子落库**成功后**才发」，自身仍 at-least-once（无事务），但其触发**前置依赖**已落库 ⇒ 不变式闭合。

### 6.3 失败路自然重试 + opID 幂等（永不双扣 / 永不永久丢）
- **在线赢家 settle**：currency+bag 折单原子 `$set`。成功 → ack；失败 → 全批 `MarkDirty` + `done(1)` → room 重投。重投经 opID=gameId 持久 dedup：currency.ops/bag.ops 未落则余额/物品也未落 ⇒ 重投重新发放恰一次。**无文档内瞬时不一致**（原子 `$set` 消除缺口 B）。
- **离线赢家 / 登录重放**：apply 在内存（ops 记 opID）→ 折单写。成功 → `$pull` + 续登录；失败 → 不 `$pull`、续登录（grants 内存可见本会话）。崩溃则 offline 消息留存，下次登录重放，opID dedup 收敛恰一次。
- **mail-claim**：grant(opID=mailID)→折单写→成功 MarkClaimed+reply 成功；失败 reply Code:1。MarkClaimed 失败仅 UI 提示降级（P4b 决策③），重领经 opID dedup 不双发。

### 6.4 「续登录 / 不剔除」正确性论证
- **Disconnect 不剔除**：未剔除玩家 = 内存仍持脏。① 重连 → `Login` 复用同一 `*Player`（最新态，无丢）；② 不重连 → `coalesceFlush`/`flushAllDirty`（停机 drain）以 `nil` after 重试落库（数据安全，仅不再次剔除——非正确性问题，进程存活期内存占用，重连即收）。**对比当前**：当前失败仍 `delete` ⇒ 脏内存态被 map 移除后 coalesce 再也找不到 ⇒ **data-loss**。本切片修正之。
- **replay 失败续登录**：续登录 `reply(成功)`+`onlineRegister` 早于持久，但 offline 消息为权威源（未 `$pull`）⇒ 任何崩溃下次登录重放，opID dedup 保证不双发；不阻塞登录避免 mongo 瞬时抖动锁死玩家入口。

---

## 7. 接口变更
- `DocStore.FlushField(d, uid, field string, state any, done func(error))` → **`FlushFields(d, uid, fields map[string]any, done func(error))`**。
- `Runtime.flushPlayer(uid, p, after func())` → **`after func(ok bool)`**。
- 无 proto / gen_routes 变更（纯内部）。无新服务 / 新配置。

---

## 8. 错误处理 / 边界 / 幂等
- **校验**（CLAUDE.md 工程纪律）：`FlushFields` 空 map 早返（成功语义）；故障注入路径必有显式 `logger.Warn`（强类型字段 uid/err）。
- **幂等汇总**：settle=opID=gameId、mail=opID=mailID（per-attachment `mailID:index`，P4b D2 教训）、offline replay=各 OfflineMsg.OpID；持久 ops 与扣发同一原子 `$set`。
- **并发**：全部回调主循环串行，零锁；`inflight` 折单后一批一计数，drain 语义不变；`-race` 跑核心回调。
- **重入 / 乱序**：同一 player 多次 flushPlayer 重叠（settle in-flight + coalesce tick）——snapshot 时清脏、绝对 `$set` 幂等、后写只含新脏组件，无丢写（与当前行为一致）。

---

## 9. 风险 / 开放问题
- **done(1) 复用**：Settle 失败路复用 `done(1)`（当前语义=「离线 push 失败 → room 重投」）表达「在线 flush 失败 → room 重投」。需 impl-plan 动手前核实 roomsvr settle-RPC 端对**任意非零 code / 超时**均触发重投且重投幂等（记忆载 room 3 重试/~600ms）。若 room 仅区分特定 code，则改为不调 done 走超时重投，或新增 code。
- **持续 mongo 不可用**：done 门控下 settle 不 ack ⇒ room 重投耗尽后放弃 ⇒ 赢家「扣币未落 / 物品未发」全未落（原子 `$set` 保证不半落），属全服降级场景，非本切片回归。
- **持久 ops 环 128**：仍有界（P5 其他候选 ⑤），本切片不动；room 3 重试/~600ms 窗口下不可达。
- **未剔除玩家内存占用**：Disconnect 持续失败且永不重连的玩家滞留内存至停机 drain。MVP 接受（mongo 持续不可用才触发，且 drain 兜底落库）。

---

## 10. 测试策略（TDD，从本 Spec / 设计意图推导）
故障注入 fake `DocStore`（`FlushFields` 可按调用序 / 字段名返错），均沙箱可跑、无 Docker：
1. **原子性**：fake 令 `FlushFields` 返错 → 断言「currency 与 bag 任一未落则两者均未落」（折单 `$set` 不可能半落；fake 校验 set map 整体接收/整体拒绝）。
2. **done 门控（成功路）**：写成功 → ack/`$pull`/evict/MarkClaimed/reply 成功 **发生**。
3. **done 门控（失败路）**：写失败 → 上述 **均不发生**；Settle 得 `done(1)`、replay 不 `$pull` 但续登录、Disconnect 玩家**仍在** `rt.players`、Mailclaim reply `Code:1`。
4. **重试收敛恰一次**：写失败后再成功（fake 第二次放行）→ 余额/物品/ops 恰一次，无双扣无丢；重连复用未剔除玩家落库。
5. **flushPlayer 简化回归**：多脏组件一次批量写（断言 fake 收到单次 `FlushFields` 含全部脏字段，非多次 `FlushField`）；空脏早返 `after(true)`。
6. **`-race`**：核心并发回调（settle / replay / disconnect）`-race`，按既有 P4b/P4c 节奏 `-count` 压测。

测试从**设计意图**推导（CLAUDE.md）：若实现与本 Spec 冲突致测试失败，先暂停报告，不迁就实现改测试。

---

## 11. 交付物速查
- `store.go`：`DocStore.FlushFields` 接口 + `mongoStore.FlushFields`。
- `runtime.go`：`flushPlayer(after func(ok bool))` 折单批量写（删 pending/afterDone）；Disconnect / replayOffline / Settle 失败语义接线。
- `lobby_handler.go`：Mailclaim 失败语义接线。
- 测试：故障注入 fake `DocStore` + 原子性/门控/重试/简化 单测，`-race`。
- 文档：本 Spec + impl-plan（同 docs PR）；同步 architecture/development 若触及（预计仅注释级）。

---

## 附录：工作流约定
- 分支：docs 分支 `docs/p5-flush-atomicity`（spec+impl-plan）合一个 **docs PR**；实现走 `feat/p5-flush-atomicity` 单独 **code PR**（沿 P4a/P4b/P4c 节奏）。禁直接 push main；合 PR 前 `git fetch && git rebase origin/main`。
- 执行：`subagent-driven-development` 逐 Task TDD；核心/并发 Task（flushPlayer 折单+门控、Settle/replay/Disconnect/Mailclaim 失败语义）走 spec+质量双评审，机械 Task（接口改名、fake 扩展）轻评审；整支 opus 终审 + 全量 `go build`/`go vet`/`go vet -tags integration`/`go test ./... -race`。
- 环境：沙箱无 Docker（集成测试仅编译）；无 gh CLI（GitHub API 建/合 PR）；gofmt 用 `$(go env GOROOT)/bin/gofmt`；既有 pre-existing gofmt dirt 不动。
