package roomsvr

import (
	"project/internal/core/app"
	"project/internal/core/dispatcher"
	corerpc "project/internal/core/rpc"
	genhandler "project/protocol/gen/handler"
	genservice "project/protocol/gen/service"
	handler "project/protocol/handler"
	service "project/protocol/service"
)

// Module 是 room 业务模块
type Module struct {
	app.BaseModule
	poster      app.Poster
	HandlerImpl HandlerImpl
	ServiceImpl ServiceImpl
}

func NewModule() *Module {
	m := &Module{}
	m.HandlerImpl.module = m
	m.ServiceImpl.module = m
	return m
}

func (m *Module) Init(a *app.App) error { m.poster = a; return nil }

func (m *Module) AfterInit() error {
	d := dispatcher.New(3)
	genhandler.RegisterRoomHandler(d, &m.HandlerImpl)
	genservice.RegisterRoomService(d, &m.ServiceImpl)
	return nil
}

func (m *Module) BeforeStop() {}
func (m *Module) Fini()       {}

type HandlerImpl struct{ module *Module }

func (h *HandlerImpl) JoinRoom(ctx corerpc.Ctx, req *handler.CS_JoinRoom_Req, reply corerpc.Reply[*handler.SC_JoinRoom_Rsp]) {
	reply(&handler.SC_JoinRoom_Rsp{}, nil)
}

func (h *HandlerImpl) LeaveRoom(ctx corerpc.Ctx, ntf *handler.CS_LeaveRoom_Ntf) {}

type ServiceImpl struct{ module *Module }

func (s *ServiceImpl) JoinRoom(ctx corerpc.Ctx, req *service.RPC_JoinRoom_Req, reply corerpc.Reply[*service.RPC_JoinRoom_Rsp]) {
	reply(&service.RPC_JoinRoom_Rsp{}, nil)
}

func (s *ServiceImpl) LeaveRoom(ctx corerpc.Ctx, ntf *service.RPC_LeaveRoom_Ntf) {}
