package pipeline

import "context"

// BeforeFunc Before 中间件函数签名
// ctx 和 data 可被改写后传递给下一环节，返回 error 时链式中断
type BeforeFunc func(ctx context.Context, data []byte) (context.Context, []byte, error)

// AfterFunc After 中间件函数签名
// 接收 handler 的 err，可改写（如统一错误格式、日志、metrics）
// After 链全部执行，不因 error 中断，err 随链传递
type AfterFunc func(ctx context.Context, err error) error

// Pipeline Before + After 双向中间件链
type Pipeline struct {
	befores []BeforeFunc
	afters  []AfterFunc
}

func New() *Pipeline {
	return &Pipeline{}
}

// UseBefore 追加 Before 中间件
func (p *Pipeline) UseBefore(fns ...BeforeFunc) {
	p.befores = append(p.befores, fns...)
}

// UseAfter 追加 After 中间件
func (p *Pipeline) UseAfter(fns ...AfterFunc) {
	p.afters = append(p.afters, fns...)
}

// ExecuteBefore 顺序执行 Before 链，任一返回 error 立即短路
func (p *Pipeline) ExecuteBefore(ctx context.Context, data []byte) (context.Context, []byte, error) {
	var err error
	for _, fn := range p.befores {
		ctx, data, err = fn(ctx, data)
		if err != nil {
			return ctx, data, err
		}
	}
	return ctx, data, nil
}

// ExecuteAfter 顺序执行 After 链，全部执行，err 随链传递
func (p *Pipeline) ExecuteAfter(ctx context.Context, err error) error {
	for _, fn := range p.afters {
		err = fn(ctx, err)
	}
	return err
}
