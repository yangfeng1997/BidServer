package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// MatchModule matchsvr 模块：持主循环 Runtime，Init 启动、OnStop 停止。
// JetStream 消费在 main.go 于 cls.Init() 后启动（编排需 discovery 就绪）。
type MatchModule struct {
	module.BaseModule
	rt *Runtime
}

// NewMatchModule 构造 MatchModule
func NewMatchModule(rt *Runtime) *MatchModule { return &MatchModule{rt: rt} }

// Name 模块唯一标识
func (m *MatchModule) Name() string { return "match" }

// Init 启动主循环
func (m *MatchModule) Init() {
	m.rt.Start()
	logger.Info("match module initialized")
}

// OnStop 停止主循环
func (m *MatchModule) OnStop() {
	m.rt.Stop()
	logger.Info("match module stopped")
}
