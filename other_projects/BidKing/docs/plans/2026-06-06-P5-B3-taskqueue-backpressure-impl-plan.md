# P5 B3 taskqueue 背压 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development。步骤用 checkbox（`- [ ]`）跟踪。
> 关联设计 Spec：[`2026-06-06-P5-B3-taskqueue-backpressure.md`](2026-06-06-P5-B3-taskqueue-backpressure.md)。

**Goal:** `taskqueue.Queue.Enqueue` 由「满则静默丢任务」改为阻塞背压（零丢失、必达），并固化「只能 off-loop 调用」不变式。

**Architecture:** 单文件改动（去 `select/default/Warn`，`q.ch <- fn` 阻塞）+ 删未用 `logger` import + 两个并发单测（阻塞语义 + 零丢失）。死锁安全性由 Spec §4 审计保证（全仓无 on-loop 自投递）。

**Tech Stack:** Go、buffered channel、`sync`/`sync/atomic`/`time`（测试）。

---

### Task 1: Enqueue 改阻塞背压 + 不变式注释 + 删 logger import

**Files:**
- Modify: `src/common/taskqueue/taskqueue.go`
- Test: `src/common/taskqueue/taskqueue_test.go`

- [ ] **Step 1: 写失败测试**

在 `src/common/taskqueue/taskqueue_test.go` 增加（import 块改为 `import ("sync"; "sync/atomic"; "testing"; "time")`）：

```go
func TestEnqueue_BlocksWhenFullUntilDrained(t *testing.T) {
	q := New(1)
	q.Enqueue(func() {}) // 填满容量

	enqueued := make(chan struct{})
	go func() {
		q.Enqueue(func() {}) // 队列满 → 应阻塞
		close(enqueued)
	}()

	// 第二个 Enqueue 应仍阻塞（未完成）
	select {
	case <-enqueued:
		t.Fatal("Enqueue should block when queue is full")
	case <-time.After(50 * time.Millisecond):
	}

	// 消费一个，腾出空位
	<-q.C()

	// 第二个 Enqueue 现应解阻塞完成
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("Enqueue should unblock after a slot frees up")
	}
}

func TestEnqueue_NoTaskLostUnderContention(t *testing.T) {
	const producers = 8
	const perProducer = 100
	total := producers * perProducer

	q := New(4) // 小容量强制频繁阻塞，放大丢失风险
	var ran atomic.Int64
	done := make(chan struct{})

	go func() { // consumer 持续消费直到收齐
		for i := 0; i < total; i++ {
			fn := <-q.C()
			fn()
		}
		close(done)
	}()

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Enqueue(func() { ran.Add(1) })
			}
		}()
	}
	wg.Wait()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("consumer did not receive all tasks; ran=%d want=%d", ran.Load(), total)
	}
	if ran.Load() != int64(total) {
		t.Fatalf("task loss: ran=%d want=%d", ran.Load(), total)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./src/common/taskqueue/ -run 'TestEnqueue_' -race -v`
Expected: `TestEnqueue_BlocksWhenFullUntilDrained` FAIL —— 当前 `Enqueue` 满时走 `default` 立即返回（丢任务、不阻塞），故第二个 `Enqueue` 不阻塞、`enqueued` 立即关闭、首个 select 命中 `<-enqueued` 触发 `t.Fatal`。（`TestEnqueue_NoTaskLostUnderContention` 当前也会因丢任务而计数不足/consumer 收不齐超时。）

- [ ] **Step 3: 实现**

`src/common/taskqueue/taskqueue.go`：把 `Enqueue` 改为阻塞，并删掉文件顶部未再使用的 `import "project/src/common/logger"`。

改后的 `Enqueue`：

```go
// Enqueue 投递任务，队列满时阻塞调用方直到主循环腾出空位（背压，零丢失）。
//
// 不变式（调用方必须遵守，否则阻塞会自锁）：Enqueue 只能从 off-loop goroutine 调用，
// 主循环内运行的任务（Flush/C() 消费的 fn）绝不可同步 Enqueue 回本队列——它已在循环上，
// 直接调用目标函数即可。当前全仓 Submit/Enqueue 调用点均在 off-loop go func / NATS 回调内
// （已审计，见设计 Spec §4），满足此不变式。
func (q *Queue) Enqueue(fn func()) {
	q.ch <- fn
}
```

删除后，文件应不再 import `logger`（`taskqueue.go` 中 `logger` 仅 Enqueue 的 Warn 用到）。删除整个 `import "project/src/common/logger"` 语句。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./src/common/taskqueue/ -run 'TestEnqueue_' -race -v`
Expected: 两个新测试 + 既有 `TestQueue_C_ReceivesEnqueued` 全 PASS。

- [ ] **Step 5: 全量回归**

Run: `go build ./... && go vet ./... && go vet -tags integration ./... && go test ./... -race`
Expected: 全绿（21 包；lobby/room/match 等用 taskqueue 的包回归通过）。再跑 `go test ./src/common/taskqueue/ -race -count=20` 确认无偶发。
另跑 `"$(go env GOROOT)/bin/gofmt" -l src/common/taskqueue/taskqueue.go src/common/taskqueue/taskqueue_test.go`（应无输出）。

- [ ] **Step 6: Commit**

```bash
git add src/common/taskqueue/taskqueue.go src/common/taskqueue/taskqueue_test.go
git commit -m "fix(framework): taskqueue.Enqueue 改阻塞背压，消除满队列静默丢任务（B3）"
```

---

## 验证清单（评审前自检）
- [ ] `Enqueue` 满时阻塞、消费腾位后解阻塞；零丢失（竞争下任务全部执行）。
- [ ] `logger` import 已删，无未用 import；`Flush`/`Len`/`C`/`New`/`Dispatcher` 接口不变。
- [ ] 死锁不变式注释已写入；与 Spec §4 审计一致（全仓无 on-loop 自投递）。
- [ ] 全量 `go build`/`go vet`/`go vet -tags integration`/`go test ./... -race` 绿；taskqueue `-count=20` 稳；gofmt 干净。

## 评审力度
- 单 Task（改共享原语 + 并发语义），走 **spec + 质量双评审**子代理（重点：阻塞正确性、死锁不变式、测试非时间脆弱）；
- 全量 `-race`（taskqueue `-count` 压测）。本切片小而集中，无需独立整支终审。
