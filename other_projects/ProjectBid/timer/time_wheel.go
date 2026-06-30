// Package timer 提供基于分层时间轮的定时器系统。
// 核心时间轮算法来自 RussellLuo/timingwheel。
package timer

import (
	"sync"
	"sync/atomic"
	"time"
)

// Scheduler 决定任务的下一次执行时间。
type Scheduler interface {
	Next(time.Time) time.Time
}

// EverySchedule 按固定时间间隔调度。
type EverySchedule struct {
	Interval time.Duration
}

// Next 返回 prev 之后的下一个整倍数时间点。
func (s *EverySchedule) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

// Condition 表示一个判断条件，用于决定 cron 定时任务是否执行。
type Condition interface {
	Check(now time.Time) bool
}

// Func 是定时器要执行的回调函数。
type Func func()

// LoopForever 表示定时任务无限循环。
const LoopForever = -1

var (
	// GlobalTicker 全局滴答器，所有 cron 任务都在此驱动。
	GlobalTicker *time.Ticker

	// Precision 全局定时精度，默认 1 秒。
	Precision = time.Second
)

// TimeWheel 分层时间轮实现。
type TimeWheel struct {
	tick         int64
	wheelSize    int64
	interval     int64
	currentTime  int64
	buckets      []*Bucket
	queue        *DelayQueue
	overflowWheel atomic.Pointer[TimeWheel]
	exitC        chan struct{}
	wg           sync.WaitGroup
}

// NewTimeWheel 创建时间轮。
// tick 是基本滴答单位，wheelSize 是每层的槽数。
func NewTimeWheel(tick time.Duration, wheelSize int64) *TimeWheel {
	tickMs := int64(tick / time.Millisecond)
	if tickMs <= 0 {
		return nil
	}

	startMs := timeToMs(time.Now().UTC())
	return newTimingWheel(tickMs, wheelSize, startMs, NewDelayQueue(int(wheelSize)))
}

func newTimingWheel(tickMs int64, wheelSize int64, startMs int64, queue *DelayQueue) *TimeWheel {
	buckets := make([]*Bucket, wheelSize)
	for i := range buckets {
		buckets[i] = newBucket()
	}

	return &TimeWheel{
		tick:        tickMs,
		wheelSize:   wheelSize,
		currentTime: truncate(startMs, tickMs),
		interval:    tickMs * wheelSize,
		buckets:     buckets,
		queue:       queue,
		exitC:       make(chan struct{}),
	}
}

func (tw *TimeWheel) add(t *Timer) bool {
	currentTime := atomic.LoadInt64(&tw.currentTime)
	if t.expiration < currentTime+tw.tick {
		return false
	}

	if t.expiration < currentTime+tw.interval {
		virtualID := t.expiration / tw.tick
		b := tw.buckets[virtualID%tw.wheelSize]
		b.Add(t)

		if b.SetExpiration(virtualID * tw.tick) {
			tw.queue.Offer(b, b.Expiration())
		}
		return true
	}

	overflowWheel := tw.overflowWheel.Load()
	if overflowWheel == nil {
		overflowWheel = newTimingWheel(tw.interval, tw.wheelSize, currentTime, tw.queue)
		if !tw.overflowWheel.CompareAndSwap(nil, overflowWheel) {
			overflowWheel = tw.overflowWheel.Load()
		}
	}
	return overflowWheel.add(t)
}

func (tw *TimeWheel) addOrRun(t *Timer) {
	if !tw.add(t) {
		if t.isAsync {
			go t.task()
		} else {
			t.task()
		}
	}
}

func (tw *TimeWheel) advanceClock(expiration int64) {
	currentTime := atomic.LoadInt64(&tw.currentTime)
	if expiration >= currentTime+tw.tick {
		currentTime = truncate(expiration, tw.tick)
		atomic.StoreInt64(&tw.currentTime, currentTime)

		if overflowWheel := tw.overflowWheel.Load(); overflowWheel != nil {
			overflowWheel.advanceClock(currentTime)
		}
	}
}

// Start 启动时间轮，阻塞调用方直到 Stop 被调用。
func (tw *TimeWheel) Start() {
	tw.wg.Add(1)
	go func() {
		defer tw.wg.Done()
		tw.queue.Poll(tw.exitC, func() int64 {
			return timeToMs(time.Now().UTC())
		})
	}()

	tw.wg.Add(1)
	go func() {
		defer tw.wg.Done()
		for {
			select {
			case elem := <-tw.queue.C:
				b := elem.(*Bucket)
				tw.advanceClock(b.Expiration())
				b.Flush(tw.addOrRun)
			case <-tw.exitC:
				return
			}
		}
	}()
}

// Stop 停止时间轮，不等待正在执行的任务完成。
func (tw *TimeWheel) Stop() {
	close(tw.exitC)
	tw.wg.Wait()
}

// AfterFunc 在 d 时间后调用 f，返回可取消的 Timer。
func (tw *TimeWheel) AfterFunc(d time.Duration, f func()) *Timer {
	t := &Timer{
		id:         NextID(),
		expiration: timeToMs(time.Now().UTC().Add(d)),
		task:       f,
	}
	tw.addOrRun(t)
	return t
}

// AddEveryFunc 每隔 d 时间重复调用 f，返回可取消的 Timer。
func (tw *TimeWheel) AddEveryFunc(d time.Duration, f func()) *Timer {
	return tw.ScheduleFunc(&EverySchedule{Interval: d}, f)
}

// ScheduleFunc 根据 Scheduler 周期性调用 f，返回可取消的 Timer。
func (tw *TimeWheel) ScheduleFunc(s Scheduler, f func()) *Timer {
	expiration := s.Next(time.Now())
	if expiration.IsZero() {
		return nil
	}

	t := &Timer{
		id:         NextID(),
		expiration: timeToMs(expiration),
	}

	t.task = func() {
		nextExpiration := s.Next(msToTime(t.expiration))
		if !nextExpiration.IsZero() {
			t.expiration = timeToMs(nextExpiration)
			tw.addOrRun(t)
		}

		// 安全调用用户函数
		func() {
			defer func() {
				if err := recover(); err != nil {
					// panic 恢复但不中断时间轮
				}
			}()
			f()
		}()
	}

	tw.addOrRun(t)
	return t
}

// ——— 辅助函数 ———

func truncate(x, m int64) int64 {
	if m <= 0 {
		return x
	}
	return x - x%m
}

func timeToMs(t time.Time) int64 {
	return t.UnixNano() / int64(time.Millisecond)
}

func msToTime(t int64) time.Time {
	return time.Unix(0, t*int64(time.Millisecond))
}

var _nextID atomic.Uint64

// NextID 返回下一个全局唯一定时器 ID。
func NextID() uint64 {
	return _nextID.Add(1)
}
