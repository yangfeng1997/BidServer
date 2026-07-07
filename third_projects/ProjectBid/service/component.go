// Package service 定义组件的反射扫描、管道处理与消息分发。
package service

type Options struct {
	Name     string
	NameFunc func(string) string
}

// Option 用于配置 Handler 组件的注册行为。
type Option func(*Options)

// WithName 自定义组件名称（默认使用结构体类型名）。
func WithName(name string) Option {
	return func(opt *Options) {
		opt.Name = name
	}
}

// WithNameFunc 自定义 handler 方法名转换函数。
func WithNameFunc(fn func(string) string) Option {
	return func(opt *Options) {
		opt.NameFunc = fn
	}
}
