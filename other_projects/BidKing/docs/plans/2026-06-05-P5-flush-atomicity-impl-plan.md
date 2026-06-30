# P5-① 跨字段 flush 原子性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 lobby `flushPlayer` 改为「同一次 flush 的全部脏组件折成一个原子 `$set`」+「`after` 仅在落库成功时触发」，消除 currency/bag 文档内瞬时不一致并补齐 §6.6 在失败路的违背。

**Architecture:** 先做行为保持的 folding 重构（`DocStore.FlushField`→`FlushFields(map[string]any)`，`flushPlayer` 单批量写，删 `pending`/`afterDone`），再叠加 done 门控（`after func()`→`func(ok bool)`），逐站点（Disconnect/Settle/replayOffline/Mailclaim）接失败语义。失败路一律退化为「自然重试 + opID 幂等」。

**Tech Stack:** Go；`src/servers/lobbysvr/internal`（单主循环零锁，回调经 taskqueue 回主循环）；`src/common/mongo`（单文档 `$set` 原子，无事务）；单元测试 + 故障注入 fake `DocStore`；沙箱无 Docker（集成测试仅编译）。

**Spec:** `docs/plans/2026-06-05-P5-flush-atomicity.md`

---

## 关键现状（动手前已核实，main @ `021e790`）
- `flushPlayer`（`runtime.go:267`）逐脏组件各发一次 `FlushField`；`pending==0` 即 `fireAfter`，**失败路仍触发**。
- `DocStore.FlushField`（`store.go:29`）+ `mongoStore.FlushField`（`store.go:49-50`，一次单字段 `$set`）。
- 四个 `after` 站点：Settle（`runtime.go:750`，`done(0)`）、replayOffline（`runtime.go:619`，`Ack`+续登录）、Disconnect（`runtime.go:235`，`delete(players)`）、Mailclaim（`lobby_handler.go:250`，`MarkClaimed`+reply）。
- `done(code)` 经 `LobbyHandler.Settle`（`lobby_handler.go:473`）→ `RPC_Settle_Rsp{Code:code}`；roomsvr `settleViaRouter`（`runtime.go:232`）对 **任意 `Code!=0`** 返回 error → 重投（`settleRetries`）。⇒ **`done(1)` 即触发 room 重投，安全。**
- `Component`（`player.go:5`）：`Name/Field/Snapshot/Dirty/ClearDirty/MarkDirty`。`Field()` 返回 `CurrencyField="currency"`/`BagField="bag"`/`FriendField="friend"`/`RatingField="rating"`。
- **三个 test fake DocStore 均实现 `FlushField`，须一并迁移**：`fakeStore`（`store_test.go:11`，同步，`flushed map[string]any` keyed `"uid:field"`）、`gatedStore`（`runtime_test.go:187`，异步 pending，`flushed map[string]bool`）、`failFieldStore`（`runtime_test.go:265`，按字段注错 + `flushes` 计数）。
- **`.FlushField(` 生产代码仅 `runtime.go:285` 一处调用**；无测试直接调用。
- `TestFlushPlayer_PartialFailureRemarksDirtyAndFiresAfterOnce`（`runtime_test.go:326`）当前编码「per-field 部分失败」语义——**新原子语义下须改写为「整批失败→全部重标脏」**（spec 驱动，非迁就实现）。
- offline fake：`fakeOfflineStore`（`offline_store_test.go:10`，`Ack` 按 opID `$pull`）；mail fake：`seedMailWithAttachment(rt, to, Attachment) string`（`mailbox_store_test.go:81`）。

## File Structure（本计划触及的文件与职责）
- `src/servers/lobbysvr/internal/store.go` — `DocStore` 接口 + `mongoStore`：`FlushField`→`FlushFields`。
- `src/servers/lobbysvr/internal/runtime.go` — `flushPlayer` folding + 门控签名；Disconnect/replayOffline/Settle 失败语义。
- `src/servers/lobbysvr/internal/lobby_handler.go` — Mailclaim 失败语义。
- `src/servers/lobbysvr/internal/store_test.go` — `fakeStore` 迁移。
- `src/servers/lobbysvr/internal/runtime_test.go` — `gatedStore`/`failFieldStore` 迁移；原子测试改写；Disconnect/Settle/replay 新测试。
- `src/servers/lobbysvr/internal/lobby_handler_test.go` — Mailclaim 新测试。
- 文档：`architecture.md` / `development.md`（flush 语义段，预计注释级）。

## 命令速查
- 单测试：`go test ./src/servers/lobbysvr/internal/ -run <Name> -v`
- 全量：`go build ./...`、`go vet ./...`、`go vet -tags integration ./...`、`go test ./... -race`
- gofmt：`$(go env GOROOT)/bin/gofmt -w <file>`

---

## Stage A — 折单原子写（行为保持的重构）

### Task A1: `FlushField`→`FlushFields` + 三 fake 迁移 + `flushPlayer` folding（保留 `after func()`）

> **核心 Task（改动原子性临界测试）**：走 spec+质量双评审。
> 这是接口重命名重构：生产与测试须同改才编译，TDD 以「改写后的原子测试通过 + 全量绿」为验收。

**Files:**
- Modify: `src/servers/lobbysvr/internal/store.go:29`（接口）、`:49-51`（mongoStore）
- Modify: `src/servers/lobbysvr/internal/runtime.go:267-301`（flushPlayer）
- Modify: `src/servers/lobbysvr/internal/store_test.go:28-31`（fakeStore）
- Modify: `src/servers/lobbysvr/internal/runtime_test.go:205-214`（gatedStore）、`:265-282`（failFieldStore）、`:326-354`（原子测试改写）

- [ ] **Step 1: 改 `DocStore` 接口 + `mongoStore`（store.go）**

`store.go:29` 接口方法替换：
```go
	FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error))
```
`store.go:49-51` 实现替换：
```go
func (s *mongoStore) FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	set := make(bson.M, len(fields))
	for field, state := range fields {
		set[field] = state
	}
	s.c.UpsertSetByID(d, playersColl, uid, set, done) // 单文档多字段 $set 原子
}
```
（`bson` 已 import；`playersColl` 常量已存在。）

- [ ] **Step 2: `flushPlayer` folding（runtime.go，保留 `after func()`）**

`runtime.go:262-301` 整体替换（删 `pending`/`afterDone`/`fireAfter`）：
```go
// flushPlayer 异步落库脏组件：主循环内取全部脏组件快照 + 乐观清脏，
// 折成一个原子 FlushFields（单文档多字段 $set）；写失败则全批重标脏、下次重试。
// after 在该批写完成后调用（本任务保留无门控语义，done 门控见 Stage B）。
// 全部回调均在主循环 goroutine 串行执行，故无需加锁。
func (rt *Runtime) flushPlayer(uid int64, p *Player, after func()) {
	fields := make(map[string]any)
	var dirty []Component
	for _, c := range p.Components() {
		if !c.Dirty() {
			continue
		}
		fields[c.Field()] = c.Snapshot()
		c.ClearDirty()
		dirty = append(dirty, c)
	}
	if len(fields) == 0 { // 无脏：视为成功，无需落库
		if after != nil {
			after()
		}
		return
	}
	rt.inflight.Add(1)
	rt.store.FlushFields(rt.tq, uid, fields, func(err error) {
		defer rt.inflight.Add(-1)
		if err != nil {
			for _, c := range dirty {
				c.MarkDirty() // 整批失败：全部重标脏，下次重试
			}
			logger.Warn("lobby flush failed", logger.Int64("uid", uid), logger.Err(err))
		}
		if after != nil {
			after()
		}
	})
}
```

- [ ] **Step 3: 迁移三个 test fake 到 `FlushFields`**

`store_test.go:28-31`（fakeStore）替换：
```go
func (f *fakeStore) FlushFields(_ taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	for field, state := range fields {
		f.flushed[strconv.FormatInt(uid, 10)+":"+field] = state
	}
	done(nil)
}
```
`runtime_test.go:205-214`（gatedStore）替换：
```go
func (g *gatedStore) FlushFields(d taskqueue.Dispatcher, uid int64, fields map[string]any, done func(error)) {
	g.mu.Lock()
	g.pending = append(g.pending, func() {
		g.mu.Lock()
		for field := range fields {
			g.flushed[strconv.FormatInt(uid, 10)+":"+field] = true
		}
		g.mu.Unlock()
		d.Enqueue(func() { done(nil) })
	})
	g.mu.Unlock()
}
```
`runtime_test.go:265-282`（failFieldStore）替换为带 `failsLeft` 的原子注错版（`failsLeft` 供 Stage B 收敛测试复用）：
```go
// failFieldStore：批中含 failField 且 failsLeft>0 时整批失败（原子，模拟单 $set 全不落）并自减；
// 否则成功。flushes 计数批量写次数。
type failFieldStore struct {
	docs      map[int64]*PlayerDoc
	failField string
	failsLeft int
	flushes   int
}

func (s *failFieldStore) Load(_ taskqueue.Dispatcher, uid int64, done func(*PlayerDoc, bool, error)) {
	d, ok := s.docs[uid]
	done(d, ok, nil)
}
func (s *failFieldStore) FlushFields(_ taskqueue.Dispatcher, _ int64, fields map[string]any, done func(error)) {
	s.flushes++
	if _, hit := fields[s.failField]; hit && s.failsLeft > 0 {
		s.failsLeft--
		done(errFlush) // 原子失败：整批不落
		return
	}
	done(nil)
}
```
（删除旧 `failFieldStore.Load`/`FlushField` 原定义；`errFlush`、`var _ DocStore = (*failFieldStore)(nil)` 保留不动。）

- [ ] **Step 4: 改写原子测试（runtime.go:326-354）**

把 `TestFlushPlayer_PartialFailureRemarksDirtyAndFiresAfterOnce` 整体替换为：
```go
func TestFlushPlayer_AtomicBatchFailureRemarksAllDirtyAndFiresAfterOnce(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	afterCount := 0
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		p.Bag().Add("o1", 1, 1)            // 脏
		p.Currency().Gain("o2", "gold", 5) // 脏（批含 currency → 整批失败）
		p.Friend().Add(2)                  // 脏
		rt.flushPlayer(uid, p, func() { afterCount++ })
	})
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		if !p.Currency().Dirty() || !p.Bag().Dirty() || !p.Friend().Dirty() {
			t.Fatal("原子批失败必须把全部组件重标脏（无半落）")
		}
		if store.flushes != 1 {
			t.Fatalf("应为一次批量 FlushFields，实得 %d", store.flushes)
		}
	})
	if afterCount != 1 {
		t.Fatalf("after 必须恰触发一次，实得 %d", afterCount)
	}
}
```

- [ ] **Step 5: 编译 + 全量回归**

Run: `go build ./... && go test ./src/servers/lobbysvr/internal/ -run 'AtomicBatch|FlushSoon|Stop|Login|Replay|Bag' -v`
Expected: PASS（原子测试 + 既有 flush/coalesce/drain/login/replay 全绿）。
Run: `$(go env GOROOT)/bin/gofmt -l src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/store_test.go src/servers/lobbysvr/internal/runtime_test.go`
Expected: 无输出（gofmt 干净）。

- [ ] **Step 6: 全量 `-race` + Commit**

Run: `go test ./src/servers/lobbysvr/... -race`
Expected: PASS。
```bash
git add src/servers/lobbysvr/internal/store.go src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/store_test.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "refactor(lobby): flushPlayer 折单原子 FlushFields（DocStore 接口迁移，行为保持）

- DocStore.FlushField→FlushFields(map[string]any)；mongoStore 一次 UpsertSetByID 单文档多字段 \$set 原子
- flushPlayer 折全部脏组件成一批写，删 pending/afterDone（单回调天然 once）
- 三 test fake(fakeStore/gatedStore/failFieldStore) 迁移；failFieldStore 加 failsLeft 原子注错
- 原子测试改写：整批失败→全部重标脏 + 单次批量写（替 per-field 部分失败语义）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Stage B — done 门控（`after func(ok bool)` + 逐站点失败语义）

### Task B1: `flushPlayer after func(ok bool)` + Disconnect 门控（其余站点 ok-忽略 shim）

> **核心 Task（并发回调 + 签名变更）**：走 spec+质量双评审。
> 签名变更原子触及全部调用点；本任务给 Settle/replayOffline/Mailclaim 上 **ok-忽略 shim** 保持行为（B2/B3/B4 各自接真失败语义），保持 build 绿、每步聚焦。

**Files:**
- Modify: `runtime.go`（flushPlayer 签名 + Disconnect + Settle/replay shim、flushAllDirty/coalesce 不变）
- Modify: `lobby_handler.go:250`（Mailclaim shim）
- Modify: `runtime_test.go`（原子测试 ok 断言 + 新 Disconnect 测试）

- [ ] **Step 1: 写失败测试 — Disconnect 落库失败不剔除**

`runtime_test.go` 追加：
```go
func TestDisconnect_FlushFailureKeepsPlayerForRetry(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: BagField, failsLeft: 1 << 30}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	runOnLoop(t, rt, func() { rt.Player(uid).Bag().Add("o1", 1, 1) }) // 脏（flush 必失败）
	disconnectSync(t, rt, uid)
	runOnLoop(t, rt, func() {
		if rt.players[uid] == nil {
			t.Fatal("落库失败必须不剔除玩家（防丢脏内存态）")
		}
		if !rt.players[uid].Bag().Dirty() {
			t.Fatal("失败的 flush 必须保留脏以便重试")
		}
	})
}
```

- [ ] **Step 2: 运行，确认失败（编译错）**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestDisconnect_FlushFailureKeepsPlayerForRetry`
Expected: 编译失败 / 或断言失败（当前 Disconnect 无条件剔除）。

- [ ] **Step 3: flushPlayer 签名 → `after func(ok bool)`**

`runtime.go` flushPlayer 中 `after func()` 改为 `after func(ok bool)`，回调体改门控：
```go
func (rt *Runtime) flushPlayer(uid int64, p *Player, after func(ok bool)) {
	fields := make(map[string]any)
	var dirty []Component
	for _, c := range p.Components() {
		if !c.Dirty() {
			continue
		}
		fields[c.Field()] = c.Snapshot()
		c.ClearDirty()
		dirty = append(dirty, c)
	}
	if len(fields) == 0 {
		if after != nil {
			after(true) // 无脏视为成功
		}
		return
	}
	rt.inflight.Add(1)
	rt.store.FlushFields(rt.tq, uid, fields, func(err error) {
		defer rt.inflight.Add(-1)
		ok := err == nil
		if !ok {
			for _, c := range dirty {
				c.MarkDirty()
			}
			logger.Warn("lobby flush failed", logger.Int64("uid", uid), logger.Err(err))
		}
		if after != nil {
			after(ok)
		}
	})
}
```
（同时更新注释「after 在该批写完成后调用」→「after(ok) 仅据落库成败回调，门控由各站点据 ok 处理」。）

- [ ] **Step 4: 更新全部调用点**

- `flushAllDirty`（`runtime.go:258`）、`coalesceFlush`（`runtime.go:317`）：保持 `rt.flushPlayer(uid, p, nil)`（nil 合法，无需改）。
- **Disconnect**（`runtime.go:235`）门控：
```go
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok {
				delete(rt.players, uid) // 仅落库成功才剔除；失败保留待重连复用/coalesce/drain
			}
		})
```
- **Settle shim**（`runtime.go:750`）：
```go
		rt.flushPlayer(uid, p, func(ok bool) { done(0) }) // shim：B2 接 done(1) 失败语义
```
- **replayOffline shim**（`runtime.go:619`）签名改 `func(ok bool)`，函数体不变（保留 Ack + after）：
```go
		rt.flushPlayer(uid, p, func(ok bool) { // shim：B3 接「失败跳 $pull」语义
			rt.offlineStore.Ack(rt.tq, uid, opIDs, func(aerr error) {
				if aerr != nil {
					logger.Warn("replay offline: ack failed", logger.Int64("uid", uid), logger.Err(aerr))
				}
			})
			after()
		})
```
- **Mailclaim shim**（`lobby_handler.go:250`）签名改 `func(ok bool)`，函数体不变。

- [ ] **Step 5: 原子测试改门控断言**

把 Task A1 的 `TestFlushPlayer_AtomicBatchFailureRemarksAllDirtyAndFiresAfterOnce` 中
`afterCount := 0` / `func() { afterCount++ }` / 末尾断言，改为捕获 ok：
```go
	afterCount, gotOK := 0, true
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		p.Bag().Add("o1", 1, 1)
		p.Currency().Gain("o2", "gold", 5)
		p.Friend().Add(2)
		rt.flushPlayer(uid, p, func(ok bool) { afterCount++; gotOK = ok })
	})
```
末尾追加：
```go
	if gotOK {
		t.Fatal("批失败时 after(ok) 必须收到 ok=false")
	}
```

- [ ] **Step 6: 运行测试通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestDisconnect_FlushFailureKeepsPlayerForRetry|AtomicBatch' -v`
Expected: PASS。
Run: `go test ./src/servers/lobbysvr/... -race`
Expected: PASS（既有 Disconnect/drain/replay 仍绿——shim 保持行为）。

- [ ] **Step 7: gofmt + Commit**

```bash
$(go env GOROOT)/bin/gofmt -w src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime_test.go
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): flushPlayer done 门控 after(ok bool) + Disconnect 失败不剔除

- after func()→func(ok bool)，ok=落库成败；Disconnect 仅 ok 时剔除（修「失败仍 delete 丢脏态」隐患）
- Settle/replayOffline/Mailclaim 暂以 ok-忽略 shim 保持行为（B2/B3/B4 接真失败语义）
- 原子测试断言 after 收到 ok=false

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task B2: Settle 门控（在线赢家落库失败 → `done(1)` room 重投）

> **核心 Task（结算正确性）**：走 spec+质量双评审。

**Files:**
- Modify: `runtime.go:750`（Settle 在线赢家 flush after）
- Modify: `runtime_test.go`（新增 settle 失败+收敛测试）

- [ ] **Step 1: 写失败测试 — flush 失败 done(1)，重试 done(0) 且恰一次扣发**

`runtime_test.go` 追加：
```go
func TestSettle_OnlineWinner_FlushFailure_ThenRetryExactlyOnce(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1}
	store.docs[1] = func() *PlayerDoc {
		d := NewPlayerDoc(1)
		d.Currency = CurrencyState{Balances: map[string]int64{"gold": 1000}}
		return d
	}()
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(1)
	loadPlayerSync(t, rt, uid)
	var codes []int32
	runOnLoop(t, rt, func() {
		rt.Settle(uid, "g1", uid, 60, "gold", 5, func(code int32) { codes = append(codes, code) }) // winner=uid
	})
	runOnLoop(t, rt, func() {
		rt.Settle(uid, "g1", uid, 60, "gold", 5, func(code int32) { codes = append(codes, code) }) // room 重投
	})
	runOnLoop(t, rt, func() {
		p := rt.Player(uid)
		if p.Currency().Balance("gold") != 940 {
			t.Fatalf("重投必须经 opID=gameId 去重，恰扣一次，bal=%d", p.Currency().Balance("gold"))
		}
		if p.Bag().Count(5) != 1 {
			t.Fatalf("奖品恰发一次，count=%d", p.Bag().Count(5))
		}
	})
	if len(codes) != 2 || codes[0] != 1 || codes[1] != 0 {
		t.Fatalf("期望 [1,0]（首发落库失败 room 重投，重投成功 ack），实得 %v", codes)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestSettle_OnlineWinner_FlushFailure_ThenRetryExactlyOnce`
Expected: FAIL（当前 shim 恒 `done(0)` → codes=[0,0]）。

- [ ] **Step 3: Settle 门控落地**

`runtime.go:750` 替换 shim：
```go
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok {
				done(0) // 持久落库后才 ack（§6.6）
			} else {
				done(1) // 落库失败：令 room 重投（opID=gameId 持久幂等保恰一次）
			}
		})
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestSettle_OnlineWinner_FlushFailure_ThenRetryExactlyOnce -v`
Expected: PASS。
Run: `go test ./src/servers/lobbysvr/internal/ -run Settle -race`
Expected: PASS（既有 settle 测试仍绿）。

- [ ] **Step 5: gofmt + Commit**

```bash
$(go env GOROOT)/bin/gofmt -w src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): Settle 在线赢家落库失败 done(1) 令 room 重投（opID 幂等保恰一次）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task B3: replayOffline 门控（落库失败跳 `$pull`、仍续登录）

> **核心 Task（登录链 + 离线一致性）**：走 spec+质量双评审。

**Files:**
- Modify: `runtime.go:619`（replayOffline flush after）
- Modify: `runtime_test.go`（新增 replay 失败测试）

- [ ] **Step 1: 写失败测试 — replay 落库失败不 `$pull` 但登录成功**

`runtime_test.go` 追加：
```go
func TestReplay_FlushFailure_KeepsInboxAndContinuesLogin(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1 << 30}
	store.docs[30005] = func() *PlayerDoc {
		d := NewPlayerDoc(30005)
		d.Currency = CurrencyState{Balances: map[string]int64{"gold": 200}}
		return d
	}()
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	fos := newFakeOfflineStore()
	runOnLoop(t, rt, func() {
		rt.offlineStore = fos
		fos.docs[30005] = []OfflineMsg{{Type: OfflineMsgSettle, OpID: "g5", Price: 60, Currency: "gold", ItemID: 5}}
	})
	var code int32 = -1
	done := make(chan struct{})
	rt.Submit(func() {
		rt.Login(30005, "0.2.1", func(rsp *lobbypb.RPC_Login_Rsp, _ error) {
			if rsp != nil {
				code = rsp.Code
			}
			close(done)
		})
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("login timeout")
	}
	runOnLoop(t, rt, func() {}) // barrier
	if code != 0 {
		t.Fatalf("落库失败不应阻塞登录，reply code=%d", code)
	}
	runOnLoop(t, rt, func() {
		if len(fos.docs[30005]) != 1 {
			t.Fatalf("落库失败必须不 $pull，inbox=%d", len(fos.docs[30005]))
		}
		p := rt.players[30005]
		if p == nil || p.Currency().Balance("gold") != 140 || p.Bag().Count(5) != 1 {
			t.Fatal("重放后内存应已应用 grants（下次登录靠 opID 去重收敛）")
		}
	})
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestReplay_FlushFailure_KeepsInboxAndContinuesLogin`
Expected: FAIL（当前 shim 恒 Ack → inbox 被 $pull 为 0）。

- [ ] **Step 3: replayOffline 门控落地**

`runtime.go:619` 替换 shim：
```go
		rt.flushPlayer(uid, p, func(ok bool) {
			if ok { // 持久落库成功才 $pull（§6.6）
				rt.offlineStore.Ack(rt.tq, uid, opIDs, func(aerr error) {
					if aerr != nil {
						logger.Warn("replay offline: ack failed", logger.Int64("uid", uid), logger.Err(aerr))
					}
				})
			}
			after() // 续登录：grants 内存可见，失败时消息留存下次登录重放（opID 幂等收敛）
		})
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestReplay_FlushFailure_KeepsInboxAndContinuesLogin|OfflineReplay' -v`
Expected: PASS（既有 `TestRuntime_OfflineReplayOnLogin`/`SkipsAlreadyApplied` 成功路仍绿）。

- [ ] **Step 5: gofmt + Commit**

```bash
$(go env GOROOT)/bin/gofmt -w src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git add src/servers/lobbysvr/internal/runtime.go src/servers/lobbysvr/internal/runtime_test.go
git commit -m "feat(lobby): replayOffline 落库失败跳 \$pull 仍续登录（消息留存下次重放，opID 幂等）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task B4: Mailclaim 门控（落库失败 reply `Code:1`）

> **核心 Task（发奖幂等边界）**：走 spec+质量双评审。

**Files:**
- Modify: `lobby_handler.go:250`（Mailclaim flush after）
- Modify: `lobby_handler_test.go`（新增 mailclaim 失败测试）

- [ ] **Step 1: 写失败测试 — grant 落库失败 reply Code:1 且邮件未标领取**

`lobby_handler_test.go` 追加（注意：需用 failFieldStore + fakeMailStore 构造 rt，并直接 seed store.docs/mail）：
```go
func TestMailclaim_FlushFailure_RepliesRetryAndDoesNotMarkClaimed(t *testing.T) {
	store := &failFieldStore{docs: map[int64]*PlayerDoc{}, failField: CurrencyField, failsLeft: 1 << 30}
	rt := NewRuntime(RuntimeConfig{Store: store, MailStore: newFakeMailStore(), Tick: 10 * time.Millisecond, FlushInterval: time.Hour})
	rt.onlineRegister, rt.onlineUnregister, rt.onlineTouch = func(int64, string) {}, func(int64) {}, func(int64) {}
	rt.Start()
	defer rt.Stop()
	uid := int64(12001)
	loadPlayerSync(t, rt, uid)
	mid := seedMailWithAttachment(rt, uid, Attachment{Kind: "gold", ID: 0, Count: 50}) // gold→currency 组件，flush 必失败
	rsp := mailClaimSync(t, rt, uid, mid)
	if rsp.Code != 1 {
		t.Fatalf("落库失败必须 reply Code:1（客户端重领），实得 %d", rsp.Code)
	}
	runOnLoop(t, rt, func() {
		ms := rt.mailStore.(*fakeMailStore)
		id, _ := primitive.ObjectIDFromHex(mid)
		ms.Get(rt.tq, id, uid, func(ok bool, m *MailDoc, _ error) {
			if !ok || m.Claimed {
				t.Fatal("落库失败时邮件不得被标记领取")
			}
		})
	})
}
```
（`primitive` 已在 `lobby_handler_test.go` import；若无则补 `go.mongodb.org/mongo-driver/bson/primitive`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/servers/lobbysvr/internal/ -run TestMailclaim_FlushFailure_RepliesRetryAndDoesNotMarkClaimed`
Expected: FAIL（当前 shim 恒 reply Code:0 + MarkClaimed）。

- [ ] **Step 3: Mailclaim 门控落地**

`lobby_handler.go:250-262` 替换 shim：
```go
			h.rt.flushPlayer(uid, p, func(ok bool) {
				if !ok {
					replyProto(replier, &lobbypb.SC_MailClaim{Code: 1}, nil) // 落库失败：客户端重领（opID=mailID 持久幂等）
					return
				}
				p.Mail().MarkClaimed(h.rt.tq, id, func(merr error) {
					if merr != nil {
						logger.Warn("mailclaim: mark claimed failed (grant 已落，最终一致)",
							logger.Int64("uid", uid), logger.String("mailId", opID), logger.Err(merr))
					}
				})
				rsp := &lobbypb.SC_MailClaim{Code: 0}
				for _, a := range atts {
					rsp.Granted = append(rsp.Granted, &lobbypb.Attachment{Kind: a.Kind, Id: a.ID, Count: a.Count})
				}
				replyProto(replier, rsp, nil)
			})
```

- [ ] **Step 4: 运行通过**

Run: `go test ./src/servers/lobbysvr/internal/ -run 'TestMailclaim_FlushFailure|MailClaim' -v`
Expected: PASS（既有 `TestMailClaim_GrantsAttachments`/`MultiSameComponentAttachments` 成功路仍绿）。

- [ ] **Step 5: gofmt + Commit**

```bash
$(go env GOROOT)/bin/gofmt -w src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git add src/servers/lobbysvr/internal/lobby_handler.go src/servers/lobbysvr/internal/lobby_handler_test.go
git commit -m "feat(lobby): Mailclaim 落库失败 reply Code:1（客户端重领，opID=mailID 幂等）

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Stage C — 文档 + 全量回归

### Task C1: 文档同步 + 全量 build/vet/test -race

> **机械 Task**：轻评审。

**Files:**
- Modify: `architecture.md` / `development.md`（flush 语义段，若有）

- [ ] **Step 1: 定位并更新文档中 flush 语义描述**

Run: `grep -rn "flushPlayer\|FlushField\|pending==0\|单字段.*\$set\|逐组件.*flush" architecture.md development.md cluster.md`
对命中处更新为：flushPlayer 折单原子 `$set`（全部脏组件一批写）+ `after(ok bool)` done 门控（落库成功才 ack/`$pull`/剔除/MarkClaimed/reply）。若无命中则在 architecture.md 的 lobby flush 段补一句说明（保持注释级，勿大改）。

- [ ] **Step 2: 全量回归**

Run: `go build ./... && go vet ./... && go vet -tags integration ./... && go test ./... -race`
Expected: 全 PASS（沙箱无 Docker，集成测试 `//go:build integration` 仅编译）。

- [ ] **Step 3: 核心包压测**

Run: `go test ./src/servers/lobbysvr/... -race -count=3`
Expected: PASS（并发回调稳定）。

- [ ] **Step 4: gofmt 全量核验**

Run: `$(go env GOROOT)/bin/gofmt -l src/servers/lobbysvr/internal/`
Expected: 仅 `online_module.go`/`router_handler_test.go` 等 **既有 pre-existing dirt**（若出现），本期改动文件须无输出。

- [ ] **Step 5: Commit**

```bash
git add architecture.md development.md
git commit -m "docs: 同步 P5-① flush 折单原子 \$set + done 门控语义

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review（writing-plans 自检）

**1. Spec 覆盖：**
- §4.1 折单原子 `$set` → Task A1（接口+mongoStore+flushPlayer folding）。✓
- §4.2 done 门控 `after(ok bool)` → Task B1（签名）。✓
- §4.3 失败语义表：Settle→B2、replayOffline→B3、Disconnect→B1、Mailclaim→B4。✓
- §6.1 flushPlayer 伪码 → A1 Step2 / B1 Step3。✓
- §6.3 失败路自然重试+opID 幂等（恰一次）→ B2 收敛测试、B3 inbox 留存、B4 不标领取。✓
- §6.4 续登录/不剔除正确性 → B1 Disconnect 测试、B3 续登录测试。✓
- §7 接口变更 → A1。✓
- §10 测试策略（原子性/门控/重试收敛/简化/`-race`）→ A1 原子测试、B1-B4 门控测试、B2 收敛、C1 `-race`。✓
- §9 `done(1)` 复用核实 → 已在「关键现状」确认 roomsvr `Code!=0` 即重投。✓

**2. Placeholder 扫描：** 无 TBD/TODO；每步含完整代码或确切命令+期望。✓

**3. 类型一致性：** `FlushFields(d, uid, fields map[string]any, done func(error))` 在接口/mongoStore/三 fake 一致；`after func(ok bool)` 在 flushPlayer 及全部站点一致；`failFieldStore{docs,failField,failsLeft,flushes}` 跨 A1/B1/B2/B3/B4 一致；`done(int32)`→`RPC_Settle_Rsp.Code` 一致。✓

## 执行约定
- `subagent-driven-development` 逐 Task TDD；A1/B1/B2/B3/B4 走 spec+质量双评审，C1 轻评审；整支 **opus 终审** + 全量 `go build`/`go vet`/`go vet -tags integration`/`go test ./... -race`。
- feat 分支 `feat/p5-flush-atomicity` 单独 code PR；合 PR 前 `git fetch && git rebase origin/main`（执行期 main 可能前进）。
- 沙箱：无 Docker（集成测试仅编译）、无 gh CLI（GitHub API 建/合 PR）、gofmt 用 `$(go env GOROOT)/bin/gofmt`；既有 pre-existing gofmt dirt 不动。
