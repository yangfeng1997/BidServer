package matchqueue

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
)

// MemoryQueue 单测用内存实现：Publish 入内存并立即投递给已注册 handler（同进程）；
// Redeliver 手动重投，验消费侧幂等。非并发安全设计目标——仅供单测主循环驱动。
type MemoryQueue struct {
	mu        sync.Mutex
	published [][]byte
	handler   func(context.Context, []byte) error
}

func NewMemoryQueue() *MemoryQueue { return &MemoryQueue{} }

func (q *MemoryQueue) Publish(ctx context.Context, _ string, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	q.mu.Lock()
	q.published = append(q.published, data)
	h := q.handler
	q.mu.Unlock()
	if h != nil {
		return h(ctx, data)
	}
	return nil
}

func (q *MemoryQueue) Consume(_ context.Context, _ string, handler func(context.Context, []byte) error) error {
	q.mu.Lock()
	q.handler = handler
	q.mu.Unlock()
	return nil
}

// Published 返回已发布消息字节副本（测试断言用）。
func (q *MemoryQueue) Published() [][]byte {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([][]byte, len(q.published))
	copy(out, q.published)
	return out
}

// Redeliver 把第 i 条已发布消息再投递一次给 handler（验重投幂等）。
func (q *MemoryQueue) Redeliver(ctx context.Context, i int) error {
	q.mu.Lock()
	if i < 0 || i >= len(q.published) {
		q.mu.Unlock()
		return fmt.Errorf("matchqueue: redeliver index %d out of range", i)
	}
	data := q.published[i]
	h := q.handler
	q.mu.Unlock()
	if h != nil {
		return h(ctx, data)
	}
	return nil
}

func (q *MemoryQueue) Close() error { return nil }

var _ MatchQueue = (*MemoryQueue)(nil)
