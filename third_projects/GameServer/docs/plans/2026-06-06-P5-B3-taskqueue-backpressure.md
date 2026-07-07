# P5 B3 taskqueue 满策略：背压（阻塞 Enqueue）设计 Spec

> 关联：[`2026-06-02-implementation-roadmap-P2-P5.md`](2026-06-02-implementation-roadmap-P2-P5.md) §9.4 框架风险 backlog B3。

## 1. 背景与动机

`src/common/taskqueue/taskqueue.go` 的 `Queue.Enqueue` 在队列满时**静默丢任务**（仅 `logger.Warn("dispatcher queue full, task dropped")`）：

```go
func (q *Queue) Enqueue(fn func()) {
	select {
	case q.ch <- fn:
	default:
		logger.Warn("dispatcher queue full, task dropped")
	}
}
```

`Queue` 是帧驱动/单主循环服务（lobby / room / match）的跨 goroutine 任务队列：off-loop goroutine（NATS RPC 回调、`mongo.runAsync` 的异步 IO 回调、各 `*ViaRouter` 编排 goroutine）经 `Enqueue` 把**续延**投递回主循环串行执行。

**关键事实：每个投递的任务都是「必达续延」**——不是可丢的尽力日志，而是：
- 客户端响应续延（丢失 → 客户端永远收不到回包）；
- **`mongo.runAsync` 的 `done(err)` 续延**（`mongo.go:56` `d.Enqueue(func(){ done(err) })`）——丢失 → `done` 永不触发 → flush 的 `after(ok)` 不触发 / `inflight` 不递减 / ack 不发出 → **脏内存态卡住、停机 drain 永不收敛、结算/重连续延丢失**。

因此「静默丢」不是优雅降级，而是**把偶发过载升级成静默正确性损坏**。「丢弃 + metric」只是让损坏可见，并不修复（续延仍丢）。**用户已敲定策略 = 背压（阻塞 Enqueue）**：满时阻塞投递者直到主循环腾出空位，零丢失、必达、平滑突发。

## 2. 范围与非目标

### 范围内
- `taskqueue.Queue.Enqueue` 由「非阻塞 + 满则丢」改为**阻塞**（`q.ch <- fn`，去 `select/default/Warn`）。
- 删随之未用的 `logger` import。
- 在 `taskqueue` 包文档注释中固化**无 on-loop 自投递不变式**（见 §4）。
- 单测：阻塞语义（满 → 阻塞 → 消费腾位 → 解阻塞）、零丢失（竞争下全部任务必达）。

### 非目标（明确划出）
- **不改 `Dispatcher` 接口签名**（仍 `Enqueue(fn func())`，无 error 返回）——方案 A 不需要 fail-fast。
- **不引入无界队列**（方案 B 未选）、**不保留丢弃 + metric**（方案 D 未选）。
- **不动 `matchsvr` 的 `matchQueue`**（`rt.queue`，等待者队列，独立类型，自带 `Enqueue/Requeue/FormTable/clearPending`，与 `taskqueue.Queue` 无关）。
- **不引入 ctx-aware/close-on-stop 的 Enqueue 变体**——见 §6 停机边界分析，当前停机路径下纯阻塞已安全；如未来需要再评估（§7 follow-up）。

## 3. 设计

唯一改动（`src/common/taskqueue/taskqueue.go`）：

```go
// Enqueue 投递任务，队列满时阻塞调用方直到主循环腾出空位（背压，零丢失）。
//
// 不变式（调用方必须遵守，否则阻塞会自锁）：Enqueue 只能从 off-loop goroutine 调用，
// 主循环内运行的任务（Flush/C() 消费的 fn）绝不可同步 Enqueue 回本队列——它已在循环上，
// 直接调用目标函数即可。当前全仓 Submit/Enqueue 调用点均在 off-loop go func / NATS 回调内
// （已审计），满足此不变式。
func (q *Queue) Enqueue(fn func()) {
	q.ch <- fn
}
```

`Flush` / `Len` / `C` / `New` / `Dispatcher` 接口均不变。

## 4. 死锁不变式与审计结论

阻塞 `Enqueue` 的唯一死锁风险：**主循环上运行的任务同步 `Enqueue` 回本队列且队列已满** → 主循环阻塞在自己的队列上、无人消费腾位 → 自锁。

**审计结论（本 Spec 据此判定方案 A 安全）**：全仓 `taskqueue` 的 `Submit`/`Enqueue` 调用点逐一核查，**全部在 off-loop goroutine 内**：
- lobby `runtime.go`：所有 `*ViaRouter` 经 `go func`；`tryReconnect` 的 `Submit`（663）在 `rejoinRoom` 的 off-loop done 回调内；唯二 `Submit`（550/663）均 off-loop。
- room `runtime.go`：`broadcastState`/`settle` 的 `Submit`（168/208）在各自 `go func` 内。
- match `runtime.go`：`orchestrate`/`reapExpired` 的 `Submit`（184/193/214/264）在 `go func` 内；`rt.queue.Enqueue` 是 `matchQueue` 非本队列。
- `mongo.runAsync`：`d.Enqueue` 在 `go func` 内（off-loop IO 回调）。
- 后端节点 `asyncDispatch=false`，NATS 订阅 goroutine 上的 handler `Submit` 是相对主循环的 off-loop goroutine（不同 goroutine），满时阻塞 → 对 NATS 施加背压、主循环消费后解阻塞，无死锁。

主循环消费方（`for { select { case fn := <-tq.C(): fn() ... } }` / room `Flush`）与投递者是不同 goroutine，故背压只跨 goroutine 等待，不自锁。

**不变式固化**：在 `Enqueue` 文档注释写明「只能 off-loop 调用」（§3）。未来若新增 on-loop 自投递将违反不变式——靠本注释 + code review 守护。

## 5. 验证（纯沙箱单元测试）

- **阻塞语义**：`New(1)`，`Enqueue` 一个填满；起 goroutine `Enqueue` 第二个并标记完成；短暂确认其**未**完成（仍阻塞）；主循环侧消费一个（`<-q.C()`）；确认第二个随后完成。用 channel/同步原语确定性检测，避免 sleep-竞态。
- **零丢失/必达**：N 个 producer goroutine 各 `Enqueue` 若干任务到小容量队列（强制阻塞），一个 consumer 持续 `Flush`/消费；断言计数器最终等于投递总数（无丢失）。`-race`。
- **既有回归**：`TestQueue_C_ReceivesEnqueued` 不依赖丢弃语义，应继续通过。
- 全量 `go build ./...`、`go vet ./...`、`go vet -tags integration ./...`、`go test ./... -race`（taskqueue 包 `-count` 压测稳；lobby/room/match 包回归绿）。

## 6. 风险与停机边界

- **持续过载 goroutine 堆积**：背压下，投递者阻塞 = goroutine 挂起。持续过载（producer 长期快于主循环）会让阻塞 goroutine 数随入站速率 × 处理滞后增长——属容量问题（任何策略在持续过载下都退化；背压至少把信号反传给上游 NATS/调用方而非静默丢）。监控/扩容是运维侧课题，不在本切片。
- **停机阻塞投递者**：各服务 `drain()`（如 lobby `for rt.inflight.Load()>0 { select{ case fn:=<-tq.C(): fn(); case <-deadline: return } }`）在 Stop 时**持续消费队列直到 inflight 归零或 drainTimeout**。故停机时阻塞的投递者会被 drain 消费、解阻塞，inflight 递减、收敛。`drainTimeout` 兜底极端卡死。drain 超时返回后若仍有投递者阻塞，进程正在退出（`Stop` 是终态、`main` 随后返回），残留阻塞 goroutine 随进程退出回收——良性。
- **无死锁**：见 §4 审计。

## 7. Follow-up（不在本期）
- 若未来出现「主循环上需投递」的合法场景，或需要在非终态 Stop 后让投递者快速失败而非阻塞，可引入 `EnqueueCtx(ctx)` 或 close-on-stop 语义。当前停机为终态，不需要。

## 8. 交付物清单（供 impl-plan 拆 Task）
1. `src/common/taskqueue/taskqueue.go`：`Enqueue` 改阻塞 + 删 `logger` import + 文档注释固化不变式。
2. `src/common/taskqueue/taskqueue_test.go`：新增阻塞语义测试 + 零丢失/必达测试。
