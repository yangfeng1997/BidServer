package rpc

import "time"

// Span 表示轻量链路跨度
type Span interface {
	Child(name string) Span
	Finish()
}

type nopSpan struct{}

func (nopSpan) Child(string) Span { return nopSpan{} }
func (nopSpan) Finish()           {}

// 跨跳传播上下文
type Ctx struct {
	deadline   time.Time
	span       Span
	fromNode   uint32
	clientMeta any
	stale      func() bool
}

// Background 创建空上下文
func Background() Ctx { return Ctx{span: nopSpan{}} }

// WithDeadline 设置截止时间
func (c Ctx) WithDeadline(d time.Duration) Ctx {
	if d > 0 {
		c.deadline = time.Now().Add(d)
	}
	return c
}

// WithSpan 设置跨度
func (c Ctx) WithSpan(s Span) Ctx {
	if s == nil {
		s = nopSpan{}
	}
	c.span = s
	return c
}

// WithFromNode 设置来源节点
func (c Ctx) WithFromNode(nodeID uint32) Ctx {
	c.fromNode = nodeID
	return c
}

// WithClientMeta 设置客户端元信息
func (c Ctx) WithClientMeta(meta any) Ctx {
	c.clientMeta = meta
	return c
}

// WithStaleGuard 设置失效检查
func (c Ctx) WithStaleGuard(fn func() bool) Ctx {
	c.stale = fn
	return c
}

// Remaining 返回剩余时间
func (c Ctx) Remaining() time.Duration {
	if c.deadline.IsZero() {
		return 0
	}
	return time.Until(c.deadline)
}

// Span 返回当前跨度
func (c Ctx) Span() Span {
	if c.span == nil {
		return nopSpan{}
	}
	return c.span
}

// Stale 判断实体是否已失效
func (c Ctx) Stale() bool {
	if c.stale == nil {
		return false
	}
	return c.stale()
}

// FromNodeID 返回来源节点
func (c Ctx) FromNodeID() uint32 { return c.fromNode }

// ClientMeta 返回客户端元信息
func (c Ctx) ClientMeta() any { return c.clientMeta }
