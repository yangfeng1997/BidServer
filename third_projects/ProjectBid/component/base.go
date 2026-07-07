package component

import "context"

// Base 提供 Component 接口的空实现，供嵌入使用。
// 嵌入 Base 后只需覆写你关心的方法，其余方法自动获得空实现。
//
// 用法：
//
//	type MyComponent struct {
//	    component.Base
//	    // 你的字段
//	}
//
//	func (c *MyComponent) Name() string { return "my-component" }
//	func (c *MyComponent) Init(ctx context.Context) error { ... }
type Base struct{}

func (Base) Init(ctx context.Context) error           { return nil }
func (Base) AfterInit(ctx context.Context) error      { return nil }
func (Base) BeforeShutdown(ctx context.Context) error { return nil }
func (Base) Shutdown(ctx context.Context) error       { return nil }
