package taskqueue

const defaultSize = 256

// Dispatcher 任务投递接口，凡是能接收 func() 并在合适时机执行的都可实现此接口。
// cluster.WithDispatch 接受此接口，解耦具体实现。
type Dispatcher interface {
	Enqueue(fn func())
}

// Queue 帧驱动服务的跨 goroutine 任务队列，实现 Dispatcher 接口。
// 外部 goroutine（如 NATS RPC 回调）通过 Enqueue 投递任务，
// 主循环每帧调用 Flush 排空队列，保证任务在主循环 goroutine 串行执行。
type Queue struct {
	ch chan func()
}

// New 创建 Queue，size 为队列容量，建议与业务并发 RPC 数匹配
func New(size int) *Queue {
	if size <= 0 {
		size = defaultSize
	}
	return &Queue{ch: make(chan func(), size)}
}

var _ Dispatcher = (*Queue)(nil)

// Enqueue 投递任务，队列满时阻塞调用方直到主循环腾出空位（背压，零丢失）。
//
// 不变式（调用方必须遵守，否则阻塞会自锁）：Enqueue 只能从 off-loop goroutine 调用，
// 主循环内运行的任务（Flush/C() 消费的 fn）绝不可同步 Enqueue 回本队列——它已在循环上，
// 直接调用目标函数即可。当前全仓 Submit/Enqueue 调用点均在 off-loop go func / NATS 回调内
// （已审计，见设计 Spec §4），满足此不变式。
//
// 停机良性边界：主循环停止消费（drain 完成/超时后 loop 退出）之后到达的投递会永久阻塞在此，
// 仅泄漏一个 goroutine 至进程退出——不卡住已返回的 Stop、不损坏状态。停机时序应先让投递者
// （NATS 订阅 / JetStream 消费）静默再停主循环消费即可完全避免。
func (q *Queue) Enqueue(fn func()) {
	q.ch <- fn
}

// Flush 排空队列，在主循环每帧开始时调用
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

// Len 返回当前队列中待处理任务数，可用于监控
func (q *Queue) Len() int { return len(q.ch) }

// C 返回底层任务 channel，供事件驱动主循环 select（与 Flush 二选一使用）
func (q *Queue) C() <-chan func() { return q.ch }
