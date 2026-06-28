package app

// Module 是 App 管理的生命周期单元。
type Module interface {
	Name() string
	App() App
	Set(app App)
	Init() error
	AfterInit() error
	BeforeShutdown()
	Shutdown()
}

type moduleWrapper struct {
	module Module
	name   string
}

// BaseModule 提供生命周期方法空实现。
type BaseModule struct {
	app App
}

func (b *BaseModule) Name() string     { return "" }
func (b *BaseModule) App() App         { return b.app }
func (b *BaseModule) Set(app App)      { b.app = app }
func (b *BaseModule) Init() error      { return nil }
func (b *BaseModule) AfterInit() error { return nil }
func (b *BaseModule) BeforeShutdown()  {}
func (b *BaseModule) Shutdown()        {}
