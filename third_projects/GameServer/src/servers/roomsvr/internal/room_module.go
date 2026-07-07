package internal

import (
	"project/src/common/logger"
	"project/src/framework/module"
)

// RoomModule roomsvr 模块：持主循环 Runtime，Init 启动、OnStop 停止。
type RoomModule struct {
	module.BaseModule
	rt *Runtime
}

// NewRoomModule 构造 RoomModule
func NewRoomModule(rt *Runtime) *RoomModule { return &RoomModule{rt: rt} }

// Name 模块唯一标识
func (m *RoomModule) Name() string { return "room" }

// Init 启动主循环
func (m *RoomModule) Init() {
	m.rt.Start()
	logger.Info("room module initialized")
}

// OnStop 停止主循环
func (m *RoomModule) OnStop() {
	m.rt.Stop()
	logger.Info("room module stopped")
}
