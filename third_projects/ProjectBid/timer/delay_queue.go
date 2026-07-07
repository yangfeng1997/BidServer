package timer

import (
	"container/heap"
	"sync"
	"sync/atomic"
	"time"
)

// ——— 优先队列（最小堆）———

type item struct {
	Value    interface{}
	Priority int64
	Index    int
}

type priorityQueue []*item

func newPriorityQueue(capacity int) priorityQueue {
	return make(priorityQueue, 0, capacity)
}

func (pq priorityQueue) Len() int           { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool { return pq[i].Priority < pq[j].Priority }

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	c := cap(*pq)
	if n+1 > c {
		npq := make(priorityQueue, n, c*2)
		copy(npq, *pq)
		*pq = npq
	}
	*pq = (*pq)[0 : n+1]
	value := x.(*item)
	value.Index = n
	(*pq)[n] = value
}

func (pq *priorityQueue) Pop() interface{} {
	n := len(*pq)
	c := cap(*pq)
	if n < c/2 && c > 25 {
		npq := make(priorityQueue, n, c/2)
		copy(npq, *pq)
		*pq = npq
	}
	value := (*pq)[n-1]
	value.Index = -1
	*pq = (*pq)[0 : n-1]
	return value
}

func (pq *priorityQueue) peekAndShift(maxValue int64) (*item, int64) {
	if pq.Len() == 0 {
		return nil, 0
	}
	value := (*pq)[0]
	if value.Priority > maxValue {
		return nil, value.Priority - maxValue
	}
	heap.Remove(pq, 0)
	return value, 0
}

// ——— 延迟队列 ——— //

// DelayQueue 是一个无界延迟阻塞队列，只有到期的元素才能取出。
type DelayQueue struct {
	C       chan interface{}
	mu      sync.Mutex
	pq      priorityQueue
	sleeping int32
	wakeupC  chan struct{}
}

// NewDelayQueue 创建指定容量的延迟队列。
func NewDelayQueue(size int) *DelayQueue {
	return &DelayQueue{
		C:       make(chan interface{}),
		pq:      newPriorityQueue(size),
		wakeupC: make(chan struct{}),
	}
}

// Offer 将元素插入延迟队列。
func (dq *DelayQueue) Offer(elem interface{}, expiration int64) {
	value := &item{Value: elem, Priority: expiration}

	dq.mu.Lock()
	heap.Push(&dq.pq, value)
	index := value.Index
	dq.mu.Unlock()

	if index == 0 {
		if atomic.CompareAndSwapInt32(&dq.sleeping, 1, 0) {
			dq.wakeupC <- struct{}{}
		}
	}
}

// Poll 启动无限循环，持续等待元素到期并发送到 channel C。
func (dq *DelayQueue) Poll(exitC chan struct{}, nowF func() int64) {
	for {
		now := nowF()

		dq.mu.Lock()
		value, delta := dq.pq.peekAndShift(now)
		if value == nil {
			atomic.StoreInt32(&dq.sleeping, 1)
		}
		dq.mu.Unlock()

		if value == nil {
			if delta == 0 {
				select {
				case <-dq.wakeupC:
					continue
				case <-exitC:
					goto exit
				}
			} else if delta > 0 {
				select {
				case <-dq.wakeupC:
					continue
				case <-time.After(time.Duration(delta) * time.Millisecond):
					if atomic.SwapInt32(&dq.sleeping, 0) == 0 {
						<-dq.wakeupC
					}
					continue
				case <-exitC:
					goto exit
				}
			}
		}

		select {
		case dq.C <- value.Value:
		case <-exitC:
			goto exit
		}
	}

exit:
	atomic.StoreInt32(&dq.sleeping, 0)
}
