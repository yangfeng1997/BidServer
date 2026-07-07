package event

import (
	"runtime/debug"
	"sync/atomic"

	"project/src/common/logger"
)

// Bus 进程内同步事件总线，泛型类型安全。
// 非协程安全，适合在单一 goroutine（如帧驱动主循环）内使用。
//
// 用法：
//
//	type PlayerLoginEvent struct { UID int64; IP string }
//
//	bus := event.NewBus[PlayerLoginEvent]()
//
//	token := bus.Subscribe(func(e PlayerLoginEvent) {
//	    logger.Info("login", logger.Int64("uid", e.UID))
//	})
//
//	bus.Publish(PlayerLoginEvent{UID: 10001, IP: "1.2.3.4"})
//
//	bus.Unsubscribe(token) // 不再需要时取消
type Bus[T any] struct {
	nextID   atomic.Uint64
	handlers map[uint64]func(T)
}

// Token 订阅凭证，用于取消订阅
type Token uint64

// NewBus 创建事件总线
func NewBus[T any]() *Bus[T] {
	return &Bus[T]{handlers: make(map[uint64]func(T))}
}

// Subscribe 注册事件处理函数，返回 Token 用于取消订阅
func (b *Bus[T]) Subscribe(fn func(T)) Token {
	id := b.nextID.Add(1)
	b.handlers[id] = fn
	return Token(id)
}

// Unsubscribe 取消订阅，token 无效时静默忽略
func (b *Bus[T]) Unsubscribe(token Token) {
	delete(b.handlers, uint64(token))
}

// Publish 同步发布事件，依次调用所有 handler。
// 单个 handler panic 会被隔离并记录，不影响其余 handler，
// 避免某个订阅者的 bug 中断整个发布流程（如帧驱动主循环）。
func (b *Bus[T]) Publish(event T) {
	for _, fn := range b.handlers {
		safePublish(fn, event)
	}
}

func safePublish[T any](fn func(T), event T) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Error("event: handler panic",
				logger.String("stack", string(debug.Stack())))
		}
	}()
	fn(event)
}

// Len 返回当前订阅数量
func (b *Bus[T]) Len() int { return len(b.handlers) }
