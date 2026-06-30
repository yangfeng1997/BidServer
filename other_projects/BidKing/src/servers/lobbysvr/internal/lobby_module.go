package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// LobbyModule lobby 服务模块：持有主循环 Runtime，Init 启动、OnStop 停止
type LobbyModule struct {
	module.BaseModule
	rt *Runtime
}

func NewLobbyModule(rt *Runtime) *LobbyModule { return &LobbyModule{rt: rt} }

func (l *LobbyModule) Name() string { return "lobby" }

func (l *LobbyModule) Init() {
	l.rt.Start()
	logger.Info("lobby module initialized")
}

func (l *LobbyModule) OnStop() {
	l.rt.Stop()
	logger.Info("lobby module stopped")
}
