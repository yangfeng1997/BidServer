package app

import (
	"context"
	"time"
)

// Module 是 App 管理的生命周期单元
type Module interface {
	Init(a *App) error
	AfterInit() error
	BeforeStop()
	Fini()
}

// BaseModule 提供生命周期方法空实现
type BaseModule struct{}

func (b *BaseModule) Init(*App) error  { return nil }
func (b *BaseModule) AfterInit() error { return nil }
func (b *BaseModule) BeforeStop()      {}
func (b *BaseModule) Fini()            {}

// Updater 由 App 主循环 tick 驱动
type Updater interface {
	Update(dt time.Duration)
}

// ReadyWaiter 等待模块异步就绪
type ReadyWaiter interface {
	WaitReady(ctx context.Context) error
}
