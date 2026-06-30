# P5 ⑤ 持久 op-dedup 环淘汰边界设计 Spec

> 关联：P4b/P4c/P5-① 留下的已知边界——「持久 ops 环有界 128，离线 ack-fail 后 >128 ops 致 opID 滑出环 → 再登重放 double-apply」。

## 1. 背景与动机

lobby 的持久 op-dedup 环（`opDedup`，`Currency.ops` / `Bag.ops`，各 bound=128）随玩家 BSON 子文档 `CurrencyState.Ops` / `BagState.Ops` 落库，是跨会话/重投/重放的幂等真边界（P4b §6.6）。`Spend(opID)` / `Bag.Add(opID)` 命中 `seen(opID)` 即 no-op，否则 `remember(opID)`；环满（>128）FIFO 淘汰最旧。

**double-apply 窄窗（确认真）**：离线赢家结算经 `offline_messages` collection，登录链 `replayOffline` 重放（`applyOfflineMsg` → `Spend/Add` with `opID=gameId`），**持久落库成功才 `Ack`（`$pull`）**（P4b §6.6）。P5-① 定：`$pull` 失败时跳过但**仍续登录，靠下次登录重放的 opID 去重收敛**。但若该 opID 在两次登录之间被 **>128 个新 ops FIFO 淘汰**出环，则：

```
会话2 登录：replay msg{gameId=G} → Spend/Add(opID=G) → flush 成功（G 入持久环）→ Ack($pull) 失败（msg G 滞留）
会话2 期间：玩家做 >128 次 currency/bag ops → G 被 FIFO 淘汰出环 → 持久 ops 不再含 G
会话3 登录：loadFrom(持久 ops，无 G) → replay msg G（仍滞留）→ seen(G)=false → 再次 Spend/Add(opID=G) → DOUBLE APPLY（双扣双发）
```

触发条件 = `$pull` 失败（Mongo 瞬时故障）+ 同一会话内 ≥128 次 currency/bag ops。窄但对活跃玩家真实，且沙箱可单测复现。

## 2. 设计决策：保护态淘汰（protected-set eviction）

**根因**：环 FIFO 淘汰不区分「opID 还有滞留重放源」与「opID 已彻底消费」。离线消息 `$pull` 成功后其 opID 永不再重放、可安全淘汰；`$pull` 失败滞留期间其 opID **必须留在环里**直到 `$pull` 成功。

**修法**：`opDedup` 增「保护集」`protected`，淘汰时**跳过受保护 opID，淘汰最旧的非保护项**；`replayOffline` 在重放时把每条离线消息的 opID 标为受保护，**`Ack`（`$pull`）成功才解除保护**。

**为何这是硬保证（而非仅缩小窗口）**：
- 受保护 opID 永不被淘汰 → 始终留在 `order` → `snapshot()` 落库 → 跨会话由 `loadFrom` 还原 → 再登 `seen(opID)=true` → 重放被正确去重，**无 double-apply**。
- opID 仅在 `Ack` 成功（消息已 `$pull`、不可能再重放）后才解保护、才可淘汰 → 淘汰永远安全。
- 保护集是**内存态**，每次登录由 `replayOffline` 从 `offline_messages` 重建（滞留消息每次登录都重新加载→重新保护），无需新增持久字段；落库的 `Ops` 因「受保护→未被淘汰」天然携带 opID 前进。
- 有界性：受保护 opID 数 = 当前滞留离线消息数（极小，个位数），环上界 = 128 + |protected|，仍有界。淘汰退化仅在「全部受保护」（需 128 条滞留消息，荒谬）时暂不淘汰——安全不无界。

**被否方案**：①无界 ops（永不淘汰）→ 玩家文档随生命周期无界增长 → Mongo 16MB 上限破裂；②单纯调大 bound（128→N）→ 只挪边界不消除残窗，对活跃玩家仍可越界。二者对正确性严格更差。

## 3. 范围与非目标

### 范围内
- `opDedup`：增 `protected` 集 + `protect/unprotect` + 淘汰跳过保护项（`remember`/`unprotect` 复用同一淘汰逻辑）。
- `Currency` / `Bag` 组件：暴露 `ProtectOp(opID)` / `UnprotectOp(opID)`（委托各自 `opDedup`）。
- `Runtime.replayOffline`：加载消息后对每个 opID 在 Currency+Bag 上 `ProtectOp`；`Ack` 成功回调内 `UnprotectOp`。

### 非目标
- **mail-claim 的 mailID 同类残窗**：mail 重领是客户端驱动（非登录自动重放），且未领邮件留存于 mailbox、客户端重领窗口短（秒级，128 ops 间隔极不可达），跨会话残窗比离线低一阶。本切片**只修离线重放**（memory ⑤ 明确范围），mail 同类问题记 Follow-up。
- 不改持久 schema（`Ops []string` 不变；保护态纯内存）。
- 不引入跨 collection 事务（`src/common/mongo` 无事务，本就是 ops 环存在的原因）。

## 4. 修法落地

`opDedup`（`op_dedup.go`）：
```go
type opDedup struct {
	seenSet   map[string]struct{}
	order     []string
	protected map[string]struct{} // 受保护 opID：滞留重放源未消费前不淘汰
	max       int
}

// remember：入环后按需淘汰
func (o *opDedup) remember(opID string) {
	if opID == "" { return }
	if _, ok := o.seenSet[opID]; ok { return }
	o.seenSet[opID] = struct{}{}
	o.order = append(o.order, opID)
	o.evict()
}

// evict：环超界时淘汰最旧的【非保护】opID；全保护则暂不淘汰（保护数远小于 max，不会无界）
func (o *opDedup) evict() {
	for len(o.order) > o.max {
		idx := -1
		for i, id := range o.order {
			if _, prot := o.protected[id]; !prot { idx = i; break }
		}
		if idx < 0 { return }
		old := o.order[idx]
		o.order = append(o.order[:idx], o.order[idx+1:]...)
		delete(o.seenSet, old)
	}
}

func (o *opDedup) protect(opID string) {
	if opID == "" { return }
	if o.protected == nil { o.protected = make(map[string]struct{}) }
	o.protected[opID] = struct{}{}
}

func (o *opDedup) unprotect(opID string) {
	if opID == "" { return }
	delete(o.protected, opID)
	o.evict() // 解保护后可能需补淘汰
}
```
（`loadFrom` 内 `o.remember` 复用新 `evict`；`snapshot` 不变。）

`Currency` / `Bag`：各加
```go
func (c *Currency) ProtectOp(opID string)   { c.ops.protect(opID) }
func (c *Currency) UnprotectOp(opID string) { c.ops.unprotect(opID) }
```

`Runtime.replayOffline`（在已有结构上加保护/解保护）：加载到 `msgs` 后，对每个 `m.OpID` 调 `p.Currency().ProtectOp(m.OpID)` + `p.Bag().ProtectOp(m.OpID)`（保护对未持有该 opID 的组件是无害 no-op-ish）；`Ack` 成功回调内对这些 opID `UnprotectOp`（两组件）。`Ack` 失败则不解保护（消息滞留、保护续存）。

## 5. 验证（纯沙箱单元测试）

- **opDedup 保护淘汰**：`max=2`，`protect("G")` 后 `remember` 多个新 opID（>max）；断言 `seen("G")` 恒 true（G 不被淘汰），非保护最旧被淘汰；`unprotect("G")` 后再 `remember` 触发淘汰，断言 G 可被淘汰。全保护时不无界、不 panic。
- **double-apply 闭合（Runtime 级，复现 §1 场景）**：fake offlineStore 让 `Ack` 失败（消息滞留）；replay 一次 msg{G}（应用 Spend+Add）；模拟 >128 次其他 op 使非保护项淘汰；持久化→重建 Player（loadFrom 持久 ops）→ 再 replay 同 msg{G}；断言**只应用一次**（余额/背包不翻倍）。对照：去掉保护则该测试应失败（变异验证）。
- **正常路径不回归**：`Ack` 成功后 `unprotect`，opID 恢复可淘汰；既有重放/结算/购买测试全绿。
- 全量 `go build`/`go vet`/`go vet -tags integration`/`go test ./... -race`（lobby 包 `-count` 稳）。

## 6. 评审力度
正确性切片：核心 Task（opDedup 保护淘汰、replayOffline 接线）走 **spec+质量双评审**；整支 `-race`。比 B1+B7 窄，单独整支终审可并入质量评审的端到端核查（跨会话 funnel 恰一次）。

## 7. 交付物清单（供 impl-plan 拆 Task）
1. `op_dedup.go`：`protected` + `protect/unprotect` + `evict`（重构 `remember`）。
2. `component_currency.go` / `component_bag.go`：`ProtectOp/UnprotectOp`。
3. `runtime.go` `replayOffline`：保护/解保护接线。
4. 测试：`op_dedup_test.go`（保护淘汰）+ runtime 级 double-apply 闭合测试。
