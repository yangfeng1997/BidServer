package taskqueue

import (
	"log"
	"sync/atomic"
	"time"
)

const defaultSize = 256

// Queue 是单主循环场景下的跨 goroutine 任务队列
//
// 约定
// - 生产者 goroutine 通过 Post 投递任务
// - 消费者只在主循环 goroutine 中执行任务
// - 队列满时阻塞生产者，提供背压，避免丢任务
type Queue struct {
	ch          chan func()
	lastFullLog atomic.Int64
	fullHits    atomic.Uint64
}

// New 创建队列。size <= 0 时回退到默认容量
func New(size int) *Queue {
	if size <= 0 {
		size = defaultSize
	}
	return &Queue{ch: make(chan func(), size)}
}

// Post 投递任务；队列满时阻塞，直到主循环消费出空间
func (q *Queue) Post(fn func()) {
	select {
	case q.ch <- fn:
	default:
		now := time.Now().UnixNano()
		last := q.lastFullLog.Load()
		if now-last > int64(time.Second) && q.lastFullLog.CompareAndSwap(last, now) {
			log.Printf("taskqueue full, blocking cap=%d total_full_hits=%d", cap(q.ch), q.fullHits.Load())
		}
		q.fullHits.Add(1)
		q.ch <- fn
	}
}

// Enqueue 是 Post 的兼容别名，保留给旧调用点
func (q *Queue) Enqueue(fn func()) { q.Post(fn) }

// C 暴露底层任务 channel，供主循环 select 消费
func (q *Queue) C() <-chan func() { return q.ch }

// Flush 排空队列，在主循环的单次 tick 内批量消费
func (q *Queue) Flush() {
	for {
		select {
		case fn := <-q.ch:
			fn()
		default:
			return
		}
	}
}

// Len 返回当前积压任务数
func (q *Queue) Len() int { return len(q.ch) }

// Cap 返回队列容量
func (q *Queue) Cap() int { return cap(q.ch) }
