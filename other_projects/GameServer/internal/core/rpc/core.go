package rpc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"project/internal/core/errcode"
)

// 底层传输层
type Transport interface {
	SendFrame(target Target, header Header, body []byte) error
}

type inflight struct {
	onResult func([]byte, errcode.ErrCode)
	timer    *time.Timer
	span     Span
}

// seq 和 pending 管理
type Core struct {
	transport Transport
	poster    Poster
	timeout   time.Duration
	seq       atomic.Uint64
	mu        sync.Mutex
	pending   map[uint64]*inflight
}

// Option 用于配置 Core
type Option func(*Core)

// WithPoster 设置回调投递器
func WithPoster(p Poster) Option {
	return func(c *Core) { c.poster = p }
}

// WithDefaultTimeout 设置默认超时
func WithDefaultTimeout(d time.Duration) Option {
	return func(c *Core) { c.timeout = d }
}

// New 创建 RPC 引擎
func New(transport Transport, opts ...Option) *Core {
	c := &Core{
		transport: transport,
		pending:   make(map[uint64]*inflight),
		timeout:   3 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// 发起请求并登记回调
func (c *Core) Call(t Target, route string, body []byte, ctx Ctx, on func([]byte, errcode.ErrCode)) {
	if on == nil {
		return
	}
	seq := c.seq.Add(1)
	span := ctx.Span().Child(route)
	reqTimeout := ctx.Remaining()
	if reqTimeout <= 0 {
		reqTimeout = c.timeout
	}
	// 使用 Target.Deadline 覆盖单次调用超时
	if t.Deadline > 0 && t.Deadline < reqTimeout {
		reqTimeout = t.Deadline
	}
	// 先插入 pending 再启动定时器，避免条目插入前触发导致泄露
	c.mu.Lock()
	c.pending[seq] = &inflight{onResult: on, span: span}
	c.mu.Unlock()
	if reqTimeout > 0 {
		c.mu.Lock()
		if f, ok := c.pending[seq]; ok {
			f.timer = time.AfterFunc(reqTimeout, func() {
				c.dispatch(func() { c.onTimeout(seq) })
			})
		}
		c.mu.Unlock()
	}

	head := Header{
		SeqID:       seq,
		Route:       route,
		DeadlineMs:  int64(reqTimeout / time.Millisecond),
		FromNodeID:  ctx.FromNodeID(),
		RoutingMode: t.Mode,
		RoutingKey:  t.Key,
		ServerType:  t.ServerType,
	}
	_ = c.transport.SendFrame(t, head, body)
}

// 发起单向通知
func (c *Core) Send(t Target, route string, body []byte, ctx Ctx) {
	head := Header{
		Route:       route,
		FromNodeID:  ctx.FromNodeID(),
		RoutingMode: t.Mode,
		RoutingKey:  t.Key,
		ServerType:  t.ServerType,
	}
	_ = c.transport.SendFrame(t, head, body)
}

// 处理回包
func (c *Core) OnResponse(seq uint64, payload []byte, code errcode.ErrCode) {
	c.mu.Lock()
	f := c.pending[seq]
	if f != nil {
		delete(c.pending, seq)
	}
	c.mu.Unlock()
	if f == nil {
		return
	}
	if f.timer != nil {
		f.timer.Stop()
	}
	if f.span != nil {
		f.span.Finish()
	}
	c.dispatch(func() { f.onResult(payload, code) })
}

func (c *Core) onTimeout(seq uint64) {
	c.mu.Lock()
	f := c.pending[seq]
	if f != nil {
		delete(c.pending, seq)
	}
	c.mu.Unlock()
	if f == nil {
		return
	}
	if f.span != nil {
		f.span.Finish()
	}
	c.dispatch(func() { f.onResult(nil, errcode.ERR_TIMEOUT) })
}

func (c *Core) dispatch(fn func()) {
	if c.poster != nil {
		c.poster.Post(fn)
		return
	}
	fn()
}

// 返回在途请求数
func (c *Core) PendingLen() int {
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	return n
}

// 返回调试信息
func (c *Core) String() string {
	return fmt.Sprintf("rpc.Core{pending=%d}", c.PendingLen())
}
