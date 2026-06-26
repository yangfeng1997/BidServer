package app

// Module 是 App 管理的生命周期单元。
type Module interface {
	Init(App) error
	AfterInit() error
	BeforeStop()
	Stop()
}

type Reloadable interface {
	Reload() error
}

// ModuleRegistration 描述一个待注册模块。
type ModuleRegistration struct {
	Name   string
	Module Module
}

type moduleWrapper struct {
	module Module
	name   string
}

// BaseModule 提供生命周期方法空实现。
type BaseModule struct{}

func (b *BaseModule) Init(App) error   { return nil }
func (b *BaseModule) AfterInit() error { return nil }
func (b *BaseModule) BeforeStop()      {}
func (b *BaseModule) Stop()            {}
