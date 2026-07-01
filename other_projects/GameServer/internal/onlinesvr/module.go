package onlinesvr

import (
	"project/internal/core/app"
	"project/internal/core/dispatcher"
	corerpc "project/internal/core/rpc"
	genhandler "project/protocol/gen/handler"
	genservice "project/protocol/gen/service"
	handler "project/protocol/handler"
	service "project/protocol/service"
)

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
	d := dispatcher.New(5)
	genhandler.RegisterOnlineHandler(d, &m.HandlerImpl)
	genservice.RegisterOnlineService(d, &m.ServiceImpl)
	return nil
}

func (m *Module) BeforeStop() {}
func (m *Module) Fini()       {}

type HandlerImpl struct{ module *Module }

func (h *HandlerImpl) QueryOnline(ctx corerpc.Ctx, req *handler.CS_QueryOnline_Req, reply corerpc.Reply[*handler.SC_QueryOnline_Rsp]) {
	reply(&handler.SC_QueryOnline_Rsp{}, nil)
}

func (h *HandlerImpl) Ping(ctx corerpc.Ctx, ntf *handler.CS_Ping_Ntf) {}

type ServiceImpl struct{ module *Module }

func (s *ServiceImpl) QueryOnline(ctx corerpc.Ctx, req *service.RPC_QueryOnline_Req, reply corerpc.Reply[*service.RPC_QueryOnline_Rsp]) {
	reply(&service.RPC_QueryOnline_Rsp{}, nil)
}

func (s *ServiceImpl) Ping(ctx corerpc.Ctx, ntf *service.RPC_Ping_Ntf) {}
