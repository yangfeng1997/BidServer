// Package pipeline 定义 Handler 的前置与后置钩子管道（中间件模式）。
package pipeline

import "context"

// HandlerTempl 是 Handler 方法的前置钩子函数签名。
// 接收 context 和反序列化后的请求参数，返回修改后的 context、修改后的参数和错误。
// 若返回错误，管道中止，Handler 不会执行。
type HandlerTempl func(ctx context.Context, in interface{}) (context.Context, interface{}, error)

// AfterHandlerTempl 是 Handler 方法的后置钩子函数签名。
// 接收 context、Handler 响应和错误，返回修改后的响应和错误。
type AfterHandlerTempl func(ctx context.Context, out interface{}, err error) (interface{}, error)

// Channel 包含前置钩子列表。
type Channel struct {
	Handlers []HandlerTempl
}

// AfterChannel 包含后置钩子列表。
type AfterChannel struct {
	Handlers []AfterHandlerTempl
}

// Hooks 包含前置与后置管道。HandlerHooks 与 RemoteHooks 均嵌入此结构体。
type Hooks struct {
	BeforeHandler *Channel
	AfterHandler  *AfterChannel
}

// HandlerHooks 包含本地客户端消息的前后置钩子管道。
type HandlerHooks struct {
	Hooks
}

// RemoteHooks 包含跨服 RPC 消息的前后置钩子管道。
type RemoteHooks struct {
	Hooks
}

// NewHandlerHooks 创建空的本地管道。
func NewHandlerHooks() *HandlerHooks {
	return &HandlerHooks{
		Hooks: Hooks{
			BeforeHandler: NewChannel(),
			AfterHandler:  NewAfterChannel(),
		},
	}
}

// NewRemoteHooks 创建空的远程管道。
func NewRemoteHooks() *RemoteHooks {
	return &RemoteHooks{
		Hooks: Hooks{
			BeforeHandler: NewChannel(),
			AfterHandler:  NewAfterChannel(),
		},
	}
}

// NewChannel 创建前置管道。
func NewChannel() *Channel {
	return &Channel{Handlers: make([]HandlerTempl, 0)}
}

// NewAfterChannel 创建后置管道。
func NewAfterChannel() *AfterChannel {
	return &AfterChannel{Handlers: make([]AfterHandlerTempl, 0)}
}

// ExecuteBeforePipeline 按注册顺序执行前置钩子。
func (c *Channel) ExecuteBeforePipeline(ctx context.Context, data interface{}) (context.Context, interface{}, error) {
	if c == nil || len(c.Handlers) == 0 {
		return ctx, data, nil
	}
	var err error
	for _, h := range c.Handlers {
		ctx, data, err = h(ctx, data)
		if err != nil {
			return ctx, data, err
		}
	}
	return ctx, data, nil
}

// ExecuteAfterPipeline 按注册顺序执行后置钩子。
func (c *AfterChannel) ExecuteAfterPipeline(ctx context.Context, res interface{}, err error) (interface{}, error) {
	if c == nil || len(c.Handlers) == 0 {
		return res, err
	}
	for _, h := range c.Handlers {
		res, err = h(ctx, res, err)
	}
	return res, err
}

// PushFront 在管道头部插入钩子。
func (c *Channel) PushFront(h HandlerTempl) {
	c.Handlers = append([]HandlerTempl{h}, c.Handlers...)
}

// PushBack 在管道尾部追加钩子。
func (c *Channel) PushBack(h HandlerTempl) {
	c.Handlers = append(c.Handlers, h)
}

// Clear 清空管道。
func (c *Channel) Clear() {
	c.Handlers = c.Handlers[:0]
}

// PushFront 在管道头部插入钩子。
func (c *AfterChannel) PushFront(h AfterHandlerTempl) {
	c.Handlers = append([]AfterHandlerTempl{h}, c.Handlers...)
}

// PushBack 在管道尾部追加钩子。
func (c *AfterChannel) PushBack(h AfterHandlerTempl) {
	c.Handlers = append(c.Handlers, h)
}

// Clear 清空管道。
func (c *AfterChannel) Clear() {
	c.Handlers = c.Handlers[:0]
}
