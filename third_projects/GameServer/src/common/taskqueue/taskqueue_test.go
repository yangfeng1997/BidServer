package taskqueue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueue_C_ReceivesEnqueued(t *testing.T) {
	q := New(4)
	q.Enqueue(func() {})
	select {
	case fn := <-q.C():
		fn()
	default:
		t.Fatal("C() did not expose enqueued task")
	}
}

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
