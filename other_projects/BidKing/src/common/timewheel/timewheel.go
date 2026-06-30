// Package timewheel 提供单层 hashed timing wheel（哈希时间轮），
// 作为框架统一的定时设施，供业务层（buff/CD/超时/倒计时等）使用。
//
// 相比为每个定时需求各起一个 time.Timer/time.Ticker（每个都占用 Go runtime
// 时间堆的一个节点），时间轮把海量定时任务摊到固定数量的槽里，
// 添加/取消/推进均为 O(1)，海量短定时场景下显著降低 runtime 开销。
//
// 两种驱动方式：
//
//   - 帧驱动（推荐用于 roomsvr 等主循环服务）：每帧调用 Advance()，
//     定时回调在主循环 goroutine 内串行执行，天然无锁，与 dispatcher 一致。
//
//     tw := timewheel.New(100*time.Millisecond, 512)
//     func (rt *Runtime) tick() { tw.Advance() }   // 每帧推进一格
//
//   - 自驱动（用于非帧驱动服务）：Start() 起一个内部 goroutine 按 tick 自动推进，
//     回调在该 goroutine 执行，业务需自行保证回调线程安全。
//
//     tw := timewheel.New(100*time.Millisecond, 512)
//     tw.Start()
//     defer tw.Close()
package timewheel

import (
	"sync"
	"sync/atomic"
	"time"
)

// task 时间轮中的一个待触发任务
type task struct {
	id       uint64
	slot     int           // 所在槽，便于 Stop 时 O(1) 定位
	rounds   int           // 剩余圈数，>0 时每次扫到递减，归零才触发
	fn       func()        // 到期回调
	interval time.Duration // 周期任务的间隔；一次性任务为 0
}

// Timer 定时任务句柄，传给 Stop 取消
type Timer struct {
	id uint64
}

// TimeWheel 单层哈希时间轮，并发安全
type TimeWheel struct {
	tick  time.Duration
	slots int

	mu      sync.Mutex
	buckets []map[uint64]*task // 每槽一个任务集合
	taskMap map[uint64]*task   // id → task，支持 O(1) 取消
	cur     int                // 当前槽指针

	nextID atomic.Uint64

	closeOnce sync.Once
	closeCh   chan struct{}
}

// New 创建时间轮：tick 为每格时长（定时最小粒度），slots 为格数（tick*slots 为单圈覆盖时长）。
// 参数非法时回退到默认值 100ms × 512（单圈约 51.2s）。
func New(tick time.Duration, slots int) *TimeWheel {
	if tick <= 0 {
		tick = 100 * time.Millisecond
	}
	if slots <= 0 {
		slots = 512
	}
	buckets := make([]map[uint64]*task, slots)
	for i := range buckets {
		buckets[i] = make(map[uint64]*task)
	}
	return &TimeWheel{
		tick:    tick,
		slots:   slots,
		buckets: buckets,
		taskMap: make(map[uint64]*task),
		closeCh: make(chan struct{}),
	}
}

// AfterFunc 注册一次性定时任务，d 后触发 fn，返回句柄用于取消。
func (tw *TimeWheel) AfterFunc(d time.Duration, fn func()) *Timer {
	return tw.add(d, 0, fn)
}

// Tick 注册周期性定时任务，每隔 d 触发一次 fn，返回句柄用于取消。
func (tw *TimeWheel) Tick(d time.Duration, fn func()) *Timer {
	if d <= 0 {
		d = tw.tick
	}
	return tw.add(d, d, fn)
}

// add 计算槽位与圈数并插入任务。interval>0 为周期任务。
func (tw *TimeWheel) add(d, interval time.Duration, fn func()) *Timer {
	id := tw.nextID.Add(1)
	tw.mu.Lock()
	tw.schedule(&task{id: id, fn: fn, interval: interval}, d)
	tw.mu.Unlock()
	return &Timer{id: id}
}

// schedule 在持锁状态下把 t 放入对应槽。需在 tw.mu 保护下调用。
//
// 圈数法不变式（cur 在第 N 次 Advance 后指向第 N 格）：
//
//	ticks  = ceil(d/tick)，至少 1（不能放回当前格立即触发）
//	rounds = (ticks-1) / slots
//	slot   = (cur + ticks) % slots
func (tw *TimeWheel) schedule(t *task, d time.Duration) {
	ticks := int((d + tw.tick - 1) / tw.tick)
	if ticks < 1 {
		ticks = 1
	}
	t.rounds = (ticks - 1) / tw.slots
	t.slot = (tw.cur + ticks) % tw.slots
	tw.buckets[t.slot][t.id] = t
	tw.taskMap[t.id] = t
}

// Stop 取消定时任务，幂等。已触发的一次性任务再 Stop 无副作用。
func (tw *TimeWheel) Stop(t *Timer) {
	if t == nil {
		return
	}
	tw.mu.Lock()
	if existing, ok := tw.taskMap[t.id]; ok {
		delete(tw.buckets[existing.slot], existing.id)
		delete(tw.taskMap, existing.id)
	}
	tw.mu.Unlock()
}

// Advance 推进一格，触发当前格中圈数归零的任务。
// 帧驱动服务每帧调用一次。回调在调用方 goroutine 内执行（先释放锁，
// 允许回调内安全地再次 AfterFunc/Stop，不会重入死锁）。
func (tw *TimeWheel) Advance() {
	tw.mu.Lock()
	tw.cur = (tw.cur + 1) % tw.slots
	bucket := tw.buckets[tw.cur]

	var expired []*task
	for id, t := range bucket {
		if t.rounds > 0 {
			t.rounds--
			continue
		}
		expired = append(expired, t)
		delete(bucket, id)
		if t.interval == 0 {
			delete(tw.taskMap, id)
		}
	}
	// 遍历结束后再 reschedule 周期任务：避免在遍历当前 bucket 的同时向它写入
	// （周期间隔恰为单圈整数倍时会落回同一 bucket，遍历中写 map 行为未定义）。
	for _, t := range expired {
		if t.interval > 0 {
			tw.schedule(t, t.interval)
		}
	}
	tw.mu.Unlock()

	for _, t := range expired {
		t.fn()
	}
}

// Start 启动自驱动 goroutine，按 tick 自动 Advance。回调在内部 goroutine 执行。
// 与帧驱动 Advance 二选一，不要同时使用。
func (tw *TimeWheel) Start() {
	go func() {
		ticker := time.NewTicker(tw.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				tw.Advance()
			case <-tw.closeCh:
				return
			}
		}
	}()
}

// Close 停止自驱动 goroutine，幂等。仅对 Start 启动的时间轮有意义。
func (tw *TimeWheel) Close() {
	tw.closeOnce.Do(func() { close(tw.closeCh) })
}

// Len 返回当前待触发任务数，可用于监控。
func (tw *TimeWheel) Len() int {
	tw.mu.Lock()
	n := len(tw.taskMap)
	tw.mu.Unlock()
	return n
}
