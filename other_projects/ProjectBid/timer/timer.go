package timer

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// Timer 表示一个定时事件，到期时执行 task。
type Timer struct {
	id         uint64
	expiration int64
	task       func()
	b          atomic.Pointer[Bucket]
	element    *list.Element
	isAsync    bool
}

// ID 返回定时器 ID。
func (t *Timer) ID() uint64 { return t.id }

func (t *Timer) getBucket() *Bucket {
	return t.b.Load()
}

func (t *Timer) setBucket(b *Bucket) {
	t.b.Store(b)
}

// Stop 取消定时器，返回 true 表示取消成功，false 表示定时器已到期或已取消。
func (t *Timer) Stop() bool {
	stopped := false
	for b := t.getBucket(); b != nil; b = t.getBucket() {
		stopped = b.Remove(t)
	}
	return stopped
}

// Bucket 是时间轮的一个槽位，持有该槽位上的所有定时器链表。
type Bucket struct {
	expiration int64
	mu         sync.Mutex
	timers     *list.List
}

func newBucket() *Bucket {
	return &Bucket{
		timers:     list.New(),
		expiration: -1,
	}
}

// Expiration 返回桶的过期时间（毫秒）。
func (b *Bucket) Expiration() int64 {
	return atomic.LoadInt64(&b.expiration)
}

// SetExpiration 设置过期时间，若值改变返回 true。
func (b *Bucket) SetExpiration(expiration int64) bool {
	return atomic.SwapInt64(&b.expiration, expiration) != expiration
}

// Add 将定时器加入桶中。
func (b *Bucket) Add(t *Timer) {
	b.mu.Lock()
	e := b.timers.PushBack(t)
	t.setBucket(b)
	t.element = e
	b.mu.Unlock()
}

func (b *Bucket) remove(t *Timer) bool {
	if t.getBucket() != b {
		return false
	}
	b.timers.Remove(t.element)
	t.setBucket(nil)
	t.element = nil
	return true
}

// Remove 将定时器从桶中移除。
func (b *Bucket) Remove(t *Timer) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.remove(t)
}

// Flush 清空桶中所有定时器，并通过 reinsert 回调重新插入时间轮。
func (b *Bucket) Flush(reinsert func(*Timer)) {
	b.mu.Lock()
	e := b.timers.Front()
	for e != nil {
		next := e.Next()
		t := e.Value.(*Timer)
		b.remove(t)
		reinsert(t)
		e = next
	}
	b.mu.Unlock()
	b.SetExpiration(-1)
}
