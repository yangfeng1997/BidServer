package module

// BaseModule 提供 Module 所有方法的空实现，嵌入后只需覆盖需要的方法
type BaseModule struct{}

func (b *BaseModule) Init()         {}
func (b *BaseModule) OnAfterInit()  {}
func (b *BaseModule) OnBeforeStop() {}
func (b *BaseModule) OnStop()       {}
