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
	deadline time.Time
	span     Span
}

// seq 和 pending 管理
type Core struct {
	transport    Transport
	poster       Poster
	timeout      time.Duration
	scanInterval time.Duration
	seq          atomic.Uint64
	mu           sync.Mutex
	pending      map[uint64]*inflight
	stopCh       chan struct{}
	scanWG       sync.WaitGroup
	closeOnce    sync.Once
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

// WithScanInterval 设置超时扫描间隔
func WithScanInterval(d time.Duration) Option {
	return func(c *Core) { c.scanInterval = d }
}

// New 创建 RPC 引擎
func New(transport Transport, opts ...Option) *Core {
	c := &Core{
		transport:    transport,
		pending:      make(map[uint64]*inflight),
		timeout:      3 * time.Second,
		scanInterval: 100 * time.Millisecond,
		stopCh:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.scanInterval <= 0 {
		c.scanInterval = 100 * time.Millisecond
	}
	c.scanWG.Add(1)
	go c.scanLoop()
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
	f := &inflight{onResult: on, span: span}
	if reqTimeout > 0 {
		f.deadline = time.Now().Add(reqTimeout)
	}
	c.mu.Lock()
	c.pending[seq] = f
	c.mu.Unlock()

	head := Header{
		SeqID:       seq,
		Route:       route,
		DeadlineMs:  int64(reqTimeout / time.Millisecond),
		SrcNodeID:   ctx.FromNodeID(),
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
		SrcNodeID:   ctx.FromNodeID(),
		RoutingMode: t.Mode,
		RoutingKey:  t.Key,
		ServerType:  t.ServerType,
	}
	_ = c.transport.SendFrame(t, head, body)
}

// 处理回包
func (c *Core) OnResponse(seq uint64, payload []byte, code errcode.ErrCode) {
	c.OnResponseWithRelease(seq, payload, code, nil)
}

// OnResponseWithRelease 处理回包，并在回调执行完成后释放 payload 所属资源。
func (c *Core) OnResponseWithRelease(seq uint64, payload []byte, code errcode.ErrCode, release func()) {
	c.mu.Lock()
	f := c.pending[seq]
	if f != nil {
		delete(c.pending, seq)
	}
	c.mu.Unlock()
	if f == nil {
		if release != nil {
			release()
		}
		return
	}
	if f.span != nil {
		f.span.Finish()
	}
	c.dispatch(func() {
		if release != nil {
			defer release()
		}
		f.onResult(payload, code)
	})
}

func (c *Core) scanLoop() {
	defer c.scanWG.Done()
	ticker := time.NewTicker(c.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.scanExpired()
		}
	}
}

func (c *Core) scanExpired() {
	now := time.Now()
	var expired []*inflight
	c.mu.Lock()
	for seq, f := range c.pending {
		if !f.deadline.IsZero() && now.After(f.deadline) {
			delete(c.pending, seq)
			expired = append(expired, f)
		}
	}
	c.mu.Unlock()
	for _, f := range expired {
		if f.span != nil {
			f.span.Finish()
		}
		c.dispatch(func() { f.onResult(nil, errcode.ERR_TIMEOUT) })
	}
}

// Close 停止超时扫描 goroutine
func (c *Core) Close() {
	c.closeOnce.Do(func() {
		close(c.stopCh)
		c.scanWG.Wait()
	})
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
