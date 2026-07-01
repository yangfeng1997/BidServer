package timewheel

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultSlots      = 512
	defaultLevelCount = 4
)

// Package timewheel 提供多层高精度时间轮，适合主循环驱动或独立驱动的定时任务管理
//
// 特点
// - 多层级联，覆盖范围远大于单层 wheel
// - 一次性任务 / 周期任务统一处理
// - 既可由主循环手动 Advance，也可 Start 自驱动
// - 全部修改都在 mutex 保护下完成，适合并发注册/取消
//
// 约定
// - 帧驱动服务优先使用 Advance
// - 非帧驱动服务可使用 Start/Close
// // 回调在调用 Advance 或内部驱动 goroutine 中执行

type task struct {
	id           uint64
	expireTick   uint64
	level        int
	slot         int
	intervalTick uint64
	fn           func()
}

type wheelLevel struct {
	buckets []map[uint64]*task
}

// Timer 是定时任务句柄，用于 Stop 取消任务
type Timer struct{ id uint64 }

// TimeWheel 多层哈希时间轮
type TimeWheel struct {
	tick  time.Duration
	slots int

	levels      []wheelLevel
	granularity []uint64

	mu          sync.Mutex
	currentTick uint64
	taskMap     map[uint64]*task
	nextID      atomic.Uint64
	closeOnce   sync.Once
	closeCh     chan struct{}
}

// New 创建时间轮。非法参数会回退到默认值 100ms × 512，默认 4 层
func New(tick time.Duration, slots int) *TimeWheel {
	return NewWithLevelCount(tick, slots, defaultLevelCount)
}

// NewWithLevelCount 创建时间轮并指定层数。levelCount <= 0 时回退到默认层数
func NewWithLevelCount(tick time.Duration, slots, levelCount int) *TimeWheel {
	if tick <= 0 {
		tick = 100 * time.Millisecond
	}
	if slots <= 0 {
		slots = defaultSlots
	}
	if levelCount <= 0 {
		levelCount = defaultLevelCount
	}

	levels := make([]wheelLevel, levelCount)
	for i := range levels {
		levels[i].buckets = make([]map[uint64]*task, slots)
		for j := range levels[i].buckets {
			levels[i].buckets[j] = make(map[uint64]*task)
		}
	}

	granularity := make([]uint64, levelCount)
	granularity[0] = 1
	for i := 1; i < levelCount; i++ {
		granularity[i] = mulClamp(granularity[i-1], uint64(slots))
	}

	return &TimeWheel{
		tick:        tick,
		slots:       slots,
		levels:      levels,
		granularity: granularity,
		taskMap:     make(map[uint64]*task),
		closeCh:     make(chan struct{}),
	}
}

// AfterFunc 注册一次性任务，d <= 0 时会被提升为 1 tick
func (tw *TimeWheel) AfterFunc(d time.Duration, fn func()) *Timer { return tw.add(d, 0, fn) }

// Tick 注册周期性任务，d <= 0 时回退到基础 tick
func (tw *TimeWheel) Tick(d time.Duration, fn func()) *Timer {
	if d <= 0 {
		d = tw.tick
	}
	return tw.add(d, d, fn)
}

func (tw *TimeWheel) add(d, interval time.Duration, fn func()) *Timer {
	id := tw.nextID.Add(1)
	tw.mu.Lock()
	defer tw.mu.Unlock()

	ticks := durationToTicks(d, tw.tick)
	if ticks < 1 {
		ticks = 1
	}

	t := &task{
		id:           id,
		expireTick:   tw.currentTick + ticks,
		intervalTick: durationToTicks(interval, tw.tick),
		fn:           fn,
	}
	tw.insertLocked(t)
	return &Timer{id: id}
}

// Stop 取消定时任务，幂等
func (tw *TimeWheel) Stop(t *Timer) {
	if t == nil {
		return
	}
	tw.mu.Lock()
	if existing, ok := tw.taskMap[t.id]; ok {
		delete(tw.levels[existing.level].buckets[existing.slot], existing.id)
		delete(tw.taskMap, existing.id)
	}
	tw.mu.Unlock()
}

// Advance 推进一格并触发到期任务
func (tw *TimeWheel) Advance() {
	tw.mu.Lock()
	tw.currentTick++
	currentTick := tw.currentTick

	for level := len(tw.levels) - 1; level >= 1; level-- {
		if currentTick%tw.granularity[level] != 0 {
			continue
		}
		tw.cascadeLocked(level)
	}

	currentSlot := tw.currentSlotLocked(0)
	bucket := tw.levels[0].buckets[currentSlot]
	ready := tw.drainReadyLocked(bucket)
	tw.mu.Unlock()

	for _, t := range ready {
		t.fn()
	}
}

// Start 启动自驱动 goroutine
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

// Close 停止自驱动 goroutine
func (tw *TimeWheel) Close() { tw.closeOnce.Do(func() { close(tw.closeCh) }) }

// Len 返回当前待触发任务数
func (tw *TimeWheel) Len() int {
	tw.mu.Lock()
	n := len(tw.taskMap)
	tw.mu.Unlock()
	return n
}

// Cap 返回每层槽数
func (tw *TimeWheel) Cap() int { return tw.slots }

func (tw *TimeWheel) currentSlotLocked(level int) int {
	return int((tw.currentTick / tw.granularity[level]) % uint64(tw.slots))
}

func (tw *TimeWheel) pickLevel(delayTicks uint64) int {
	level := 0
	last := len(tw.levels) - 1
	for level < last {
		limit := mulClamp(tw.granularity[level], uint64(tw.slots))
		if limit == 0 || delayTicks < limit {
			break
		}
		level++
	}
	return level
}

func (tw *TimeWheel) insertLocked(t *task) {
	delayTicks := uint64(0)
	if t.expireTick > tw.currentTick {
		delayTicks = t.expireTick - tw.currentTick
	}

	level := 0
	slot := tw.currentSlotLocked(0)
	if delayTicks > 0 {
		level = tw.pickLevel(delayTicks)
		slot = int((t.expireTick / tw.granularity[level]) % uint64(tw.slots))
	}

	t.level = level
	t.slot = slot
	tw.levels[level].buckets[slot][t.id] = t
	tw.taskMap[t.id] = t
}

func (tw *TimeWheel) cascadeLocked(level int) {
	idx := tw.currentSlotLocked(level)
	bucket := tw.levels[level].buckets[idx]
	if len(bucket) == 0 {
		return
	}

	pending := make([]*task, 0, len(bucket))
	for id, t := range bucket {
		delete(bucket, id)
		pending = append(pending, t)
	}

	for _, t := range pending {
		tw.insertLocked(t)
	}
}

func (tw *TimeWheel) drainReadyLocked(bucket map[uint64]*task) []*task {
	if len(bucket) == 0 {
		return nil
	}

	ready := make([]*task, 0, len(bucket))
	for id, t := range bucket {
		if t.expireTick > tw.currentTick {
			delete(bucket, id)
			tw.insertLocked(t)
			continue
		}
		delete(bucket, id)
		delete(tw.taskMap, id)
		ready = append(ready, t)
		if t.intervalTick > 0 {
			t.expireTick = tw.currentTick + t.intervalTick
			tw.insertLocked(t)
		}
	}
	return ready
}

func durationToTicks(d, tick time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return uint64((d + tick - 1) / tick)
}

func mulClamp(a, b uint64) uint64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > ^uint64(0)/b {
		return ^uint64(0)
	}
	return a * b
}
