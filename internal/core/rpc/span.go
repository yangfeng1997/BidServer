package rpc

import (
	"sync"
	"time"
)

// Span 接口实现
type traceSpan struct {
	mu       sync.Mutex
	name     string
	startAt  time.Time
	endAt    time.Time
	children []*traceSpan
	finished bool
}

// 创建 Span
func NewSpan(name string) Span {
	return &traceSpan{
		name:    name,
		startAt: time.Now(),
	}
}

func (s *traceSpan) Child(name string) Span {
	s.mu.Lock()
	defer s.mu.Unlock()
	child := &traceSpan{
		name:    name,
		startAt: time.Now(),
	}
	s.children = append(s.children, child)
	return child
}

func (s *traceSpan) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endAt = time.Now()
	s.finished = true
}

// Elapsed 返回耗时
func (s *traceSpan) Elapsed() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return s.endAt.Sub(s.startAt)
	}
	return time.Since(s.startAt)
}

// Name 返回 span 名称
func (s *traceSpan) Name() string { return s.name }

// TreeString 返回 span 树的文本表示
func (s *traceSpan) TreeString(indent string) string {
	s.mu.Lock()
	elapsed := s.endAt.Sub(s.startAt)
	finished := s.finished
	children := make([]*traceSpan, len(s.children))
	copy(children, s.children)
	s.mu.Unlock()

	result := indent + s.name
	if finished {
		result += " [" + elapsed.String() + "]"
	} else {
		result += " [running]"
	}
	result += "\n"
	for _, child := range children {
		result += child.TreeString(indent + "  ")
	}
	return result
}

// 创建可追踪的 Ctx
func TraceCtx(name string) Ctx {
	span := NewSpan(name)
	return Background().WithSpan(span)
}

// 从 Ctx 提取根 Span
func RootSpan(ctx Ctx) Span {
	sp := ctx.Span()
	if sp == nil {
		return nopSpan{}
	}

	type rooted interface {
		Root() Span
	}
	for {
		if r, ok := sp.(rooted); ok {
			sp = r.Root()
		} else {
			break
		}
	}
	return sp
}

// Ensure nopSpan still implements Span
var _ Span = nopSpan{}
