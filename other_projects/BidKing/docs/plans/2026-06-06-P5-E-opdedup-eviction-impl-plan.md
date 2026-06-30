# P5 ⑤ 持久 op-dedup 环淘汰边界 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。步骤用 checkbox 跟踪。
> 关联设计 Spec：[`2026-06-06-P5-E-opdedup-eviction.md`](2026-06-06-P5-E-opdedup-eviction.md)。

**Goal:** 让滞留离线消息的 opID 在 `$pull`（Ack）成功前免于 FIFO 淘汰，关闭「离线 ack-fail + >128 ops → 再登 double-apply」窄窗。

**Architecture:** `opDedup` 增受保护集（淘汰跳过受保护项）；`replayOffline` 重放时保护各 opID、Ack 成功才解保护。保护态纯内存，每次登录从 `offline_messages` 重建；持久 `Ops` 因「受保护→不被淘汰」天然携带 opID 跨会话。

**Tech Stack:** Go、lobby `internal` 包（单主循环 Runtime + EC 组件 + 持久 opDedup 环）。

---

### Task 1: opDedup 受保护集 + 淘汰跳过

**Files:**
- Modify: `src/servers/lobbysvr/internal/op_dedup.go`
- Test: `src/servers/lobbysvr/internal/op_dedup_test.go`

- [ ] **Step 1: 写失败测试**（追加到 `op_dedup_test.go`，保留既有测试）

```go
func TestOpDedup_ProtectedSurvivesEviction(t *testing.T) {
	o := newOpDedup(2)
	o.remember("g") // 受保护的重放 opID
	o.protect("g")
	// 灌入远超 max 的新 opID，正常会把 "g" 挤出
	for i := 0; i < 10; i++ {
		o.remember(string(rune('A' + i)))
	}
	if !o.seen("g") {
		t.Fatal("protected opID must NOT be evicted")
	}
	// 非保护项按容量淘汰：最旧的非保护项应已淘汰
	if o.seen("A") {
		t.Fatal("oldest non-protected opID should have been evicted")
	}
	// 快照应含受保护 opID（落库后跨会话仍能去重）
	snap := o.snapshot()
	found := false
	for _, id := range snap {
		if id == "g" {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot must retain protected opID")
	}
}

func TestOpDedup_UnprotectAllowsEviction(t *testing.T) {
	o := newOpDedup(2)
	o.remember("g")
	o.protect("g")
	for i := 0; i < 5; i++ {
		o.remember(string(rune('A' + i)))
	}
	if !o.seen("g") {
		t.Fatal("protected before unprotect")
	}
	o.unprotect("g") // 解保护后应立即按容量补淘汰（g 是最旧）
	if o.seen("g") {
		t.Fatal("after unprotect, g should become evictable and be evicted")
	}
}

func TestOpDedup_AllProtectedDoesNotPanicOrUnbound(t *testing.T) {
	o := newOpDedup(1)
	o.remember("a")
	o.protect("a")
	o.remember("b")
	o.protect("b") // 全保护，超界但不应淘汰受保护项、不 panic
	if !o.seen("a") || !o.seen("b") {
		t.Fatal("all-protected entries must be retained")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup_Protected -v`
Expected: 编译失败 `o.protect undefined`。

- [ ] **Step 3: 实现**

`op_dedup.go`：结构体加 `protected map[string]struct{}`；`remember` 末尾的内联淘汰抽到 `evict()`；新增 `protect`/`unprotect`。完整改后文件（保留既有方法签名 `seen`/`snapshot`/`loadFrom`）：

```go
// src/servers/lobbysvr/internal/op_dedup.go
package internal

// opDedup 有界 op-id 去重环：seen/remember 两步分离，调用方决定何时 remember
// （如「仅操作成功才记」），空 opID 永不去重。仅主循环用，零锁。
// 受保护 opID（protect）免于 FIFO 淘汰——用于「滞留重放源未消费前 opID 不可丢」（⑤）。
type opDedup struct {
	seenSet   map[string]struct{}
	order     []string
	protected map[string]struct{}
	max       int
}

func newOpDedup(max int) *opDedup {
	if max <= 0 {
		max = 128
	}
	return &opDedup{seenSet: make(map[string]struct{}), max: max}
}

// seen 报告 opID 是否已记录（空 opID 恒 false）。
func (o *opDedup) seen(opID string) bool {
	if opID == "" {
		return false
	}
	_, ok := o.seenSet[opID]
	return ok
}

// remember 记录 opID 并维护有界环（超界淘汰最旧的非保护项）；空或重复 opID 是 no-op。
func (o *opDedup) remember(opID string) {
	if opID == "" {
		return
	}
	if _, ok := o.seenSet[opID]; ok {
		return
	}
	o.seenSet[opID] = struct{}{}
	o.order = append(o.order, opID)
	o.evict()
}

// evict 在超界时淘汰最旧的【非保护】opID；全部受保护则暂不淘汰
// （受保护数 = 滞留重放源数，远小于 max，不会无界）。
func (o *opDedup) evict() {
	for len(o.order) > o.max {
		idx := -1
		for i, id := range o.order {
			if _, prot := o.protected[id]; !prot {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		old := o.order[idx]
		o.order = append(o.order[:idx], o.order[idx+1:]...)
		delete(o.seenSet, old)
	}
}

// protect 标记 opID 免于淘汰（重放源消费前调用）；空 opID no-op。
func (o *opDedup) protect(opID string) {
	if opID == "" {
		return
	}
	if o.protected == nil {
		o.protected = make(map[string]struct{})
	}
	o.protected[opID] = struct{}{}
}

// unprotect 解除保护并按需补淘汰（重放源已消费/$pull 成功后调用）；空 opID no-op。
func (o *opDedup) unprotect(opID string) {
	if opID == "" {
		return
	}
	delete(o.protected, opID)
	o.evict()
}

// snapshot 返回 op-id 环的有序快照（落库用，值拷贝）。
func (o *opDedup) snapshot() []string {
	out := make([]string, len(o.order))
	copy(out, o.order)
	return out
}

// loadFrom 用持久化的 op-id 序列重建去重环（覆盖现状，维持有界淘汰）。
func (o *opDedup) loadFrom(ops []string) {
	o.seenSet = make(map[string]struct{}, len(ops))
	o.order = o.order[:0]
	for _, id := range ops {
		o.remember(id)
	}
}
```

- [ ] **Step 4: 运行确认通过 + 既有 opDedup 测试回归**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestOpDedup -race -v`
Expected: 新 3 个 + 既有 6 个全 PASS。

- [ ] **Step 5: Commit**

```bash
git add src/servers/lobbysvr/internal/op_dedup.go src/servers/lobbysvr/internal/op_dedup_test.go
git commit -m "feat(lobby): op-dedup 环增受保护集，淘汰跳过受保护 opID（⑤ 前置）"
```

---

### Task 2: Currency/Bag 暴露 ProtectOp + replayOffline 接线 + double-apply 闭合测试

**Files:**
- Modify: `src/servers/lobbysvr/internal/component_currency.go`、`component_bag.go`
- Modify: `src/servers/lobbysvr/internal/runtime.go`（`replayOffline`）
- Test: `src/servers/lobbysvr/internal/offline_store_test.go`（给 `fakeOfflineStore` 加 `failAck`）+ 新建/追加 runtime 级测试

- [ ] **Step 1: 写失败测试**

① 给 `fakeOfflineStore`（`offline_store_test.go`）加可控 Ack 失败（保留既有行为，默认成功）：

```go
type fakeOfflineStore struct {
	docs    map[int64][]OfflineMsg
	failAck bool // true 时 Ack 返回错误且不删消息（模拟 $pull 失败、消息滞留）
}
```
并在 `Ack` 开头加：
```go
func (s *fakeOfflineStore) Ack(d taskqueue.Dispatcher, uid int64, opIDs []string, done func(error)) {
	if s.failAck {
		d.Enqueue(func() { done(errors.New("ack failed")) })
		return
	}
	// ……既有删除逻辑不变……
}
```
（import 增 `"errors"`。）

② 新增 runtime 级 double-apply 闭合测试（追加到 lobby 的 runtime 测试文件，如 `runtime_test.go`；用既有 harness `newTestRuntimeWithStore(newFakeStore())` 风格构造 Runtime，并把 `rt.offlineStore` 设为 failAck 的 fake）。测试意图与断言（实现者按既有 harness 适配 Player 构造 / 主循环驱动 / 持久重建）：

```
TestReplayOffline_NoDoubleApplyAfterEviction:
  1. 构造 Runtime；offlineStore = &fakeOfflineStore{docs: {uid: [{Type:settle, OpID:"G", Price:50, Currency:"gold", ItemID:7}]}, failAck:true}
  2. 取得/建档 Player p（带 gold 余额 >= 50，Bag 空）
  3. 驱动 rt.replayOffline(uid, p, func(){})（经主循环 Submit + 排空，使 Load/apply/flush/Ack 回调跑完）
  4. 记录 gold 余额 bal1、item7 数量 cnt1（= 初始 - 50 / +1）
  5. 模拟 >maxCurrencyOps(128) 次非保护 op：循环对 p.Currency() 与 p.Bag() 用 129 个互异 opID 各 Spend/Add 一次（金额/数量随意，余额够）
  6. 断言 "G" 仍在 p.Currency().Persist().Ops 与 p.Bag().Persist().Ops 中（受保护未被淘汰）—— 这是 ⑤ 修复的直接证据
  7. 持久→重建：用 p 的 CurrencyState/BagState 经 loadFrom 重建一个新 Player p2（模拟再次登录）；断言 p2.Currency().ops.seen("G") 与 Bag 同（G 跨会话存活）
  8. 对 p2 再次 rt.replayOffline（消息仍滞留，failAck）→ 断言 gold 余额未再 -50、item7 未再 +1（恰一次，无 double-apply）
```
> 变异验证：若移除 Step-3 的 ProtectOp 接线，步骤 6/8 应失败（G 被淘汰 → 再扣再发）。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestReplayOffline_NoDoubleApply -race -v`
Expected: FAIL —— 当前无 ProtectOp 接线，"G" 在 129 ops 后被淘汰，step 6 断言失败（或 step 8 double-apply）。

- [ ] **Step 3: 实现 Currency/Bag 暴露 + replayOffline 接线**

`component_currency.go` 加：
```go
// ProtectOp 标记 opID 免于 op-dedup 环淘汰（滞留重放源未消费前）。
func (c *Currency) ProtectOp(opID string) { c.ops.protect(opID) }

// UnprotectOp 解除保护（重放源已 $pull 成功后）。
func (c *Currency) UnprotectOp(opID string) { c.ops.unprotect(opID) }
```
`component_bag.go` 同样加（受体 `b *Bag`，委托 `b.ops`）。

`runtime.go` 的 `replayOffline` 改为（在既有结构上加保护/解保护）：
```go
func (rt *Runtime) replayOffline(uid int64, p *Player, after func()) {
	if rt.offlineStore == nil {
		after()
		return
	}
	rt.offlineStore.Load(rt.tq, uid, func(msgs []OfflineMsg, err error) {
		if err != nil {
			logger.Warn("replay offline: load failed", logger.Int64("uid", uid), logger.Err(err))
			after()
			return
		}
		if len(msgs) == 0 {
			after()
			return
		}
		opIDs := make([]string, 0, len(msgs))
		for _, m := range msgs {
			// 保护该 opID 免于 FIFO 淘汰，直到 Ack($pull) 成功——防滞留消息再登 double-apply（⑤）。
			p.Currency().ProtectOp(m.OpID)
			p.Bag().ProtectOp(m.OpID)
			rt.applyOfflineMsg(uid, p, m)
			opIDs = append(opIDs, m.OpID)
		}
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok { // 持久落库成功才 $pull（§6.6）
				rt.offlineStore.Ack(rt.tq, uid, opIDs, func(aerr error) {
					if aerr != nil {
						logger.Warn("replay offline: ack failed", logger.Int64("uid", uid), logger.Err(aerr))
						return // Ack 失败：保留保护，消息滞留，下次登录重放仍由持久环去重
					}
					// $pull 成功，消息不可能再重放，解保护使 opID 恢复可淘汰
					for _, id := range opIDs {
						p.Currency().UnprotectOp(id)
						p.Bag().UnprotectOp(id)
					}
				})
			}
			after() // 续登录：grants 内存可见，失败时消息留存下次登录重放（opID 幂等收敛）
		})
	})
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestReplayOffline|TestOpDedup' -race -v`
Expected: 全 PASS。

- [ ] **Step 5: 全量回归**

Run: `go build ./... && go vet ./... && go vet -tags integration ./... && go test ./... -race`
Expected: 全绿（21 包）；lobby 包 `-count=3` 稳。
`"$(go env GOROOT)/bin/gofmt" -l` 触及文件无输出。

- [ ] **Step 6: Commit**

```bash
git add src/servers/lobbysvr/internal/component_currency.go src/servers/lobbysvr/internal/component_bag.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/offline_store_test.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "fix(lobby): 离线重放保护 opID 至 $pull 成功，闭合 ack-fail+淘汰 double-apply 窄窗（⑤）"
```

---

## 验证清单（评审前自检）
- [ ] 受保护 opID 永不被淘汰、留在 snapshot（落库）；解保护后恢复可淘汰；全保护不无界/不 panic。
- [ ] `replayOffline` 保护每条离线 opID（Currency+Bag），仅 Ack($pull) 成功才解保护；Ack 失败保留保护。
- [ ] double-apply 闭合：ack-fail + >128 ops + 重建重放 = 恰一次。变异（去接线）该测试失败。
- [ ] 既有 opDedup/replay/结算/购买测试全绿；持久 schema（`Ops`）不变。
- [ ] 全量 `-race` 绿；gofmt 干净。

## 评审力度
- Task 1（opDedup 保护淘汰）、Task 2（接线 + double-apply 闭合）走 **spec+质量双评审**；质量评审含端到端跨会话恰一次核查（替代独立整支终审，本切片窄）。全程 `-race`。

## 交付物清单
见 Spec §7（op_dedup.go / component_{currency,bag}.go / runtime.go replayOffline / 测试）。
