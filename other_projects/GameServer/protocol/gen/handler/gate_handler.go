package handler

import (
	"project/internal/core/dispatcher"
)

// gate 自身处理的客户端入口消息接口
type GateHandler interface{}

// 注册路由
func RegisterGateHandler(d *dispatcher.Dispatcher, srv GateHandler) {
	_ = d
	_ = srv
}
