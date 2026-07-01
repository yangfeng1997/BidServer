package app

import (
	"context"
	"sync"
)

// Ready 是 ReadyWaiter 的便捷封装，适合异步连接/加载场景
type Ready struct {
	once sync.Once
	ch   chan struct{}
	err  error
}

// NewReady 创建一个新的就绪原语
func NewReady() *Ready {
	return &Ready{ch: make(chan struct{})}
}

// Done 表示模块已就绪
func (r *Ready) Done() {
	r.once.Do(func() { close(r.ch) })
}

// Fail 表示模块就绪失败
func (r *Ready) Fail(err error) {
	r.once.Do(func() {
		r.err = err
		close(r.ch)
	})
}

// WaitReady 等待 Done 或 Fail，或等待上下文超时
func (r *Ready) WaitReady(ctx context.Context) error {
	select {
	case <-r.ch:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
