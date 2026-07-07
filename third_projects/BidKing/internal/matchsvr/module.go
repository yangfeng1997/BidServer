package matchsvr

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
	d := dispatcher.New(4)
	genhandler.RegisterMatchHandler(d, &m.HandlerImpl)
	genservice.RegisterMatchService(d, &m.ServiceImpl)
	return nil
}

func (m *Module) BeforeStop() {}
func (m *Module) Fini()       {}

type HandlerImpl struct{ module *Module }

func (h *HandlerImpl) StartMatch(ctx corerpc.Ctx, req *handler.CS_StartMatch_Req, reply corerpc.Reply[*handler.SC_StartMatch_Rsp]) {
	reply(&handler.SC_StartMatch_Rsp{}, nil)
}

func (h *HandlerImpl) CancelMatch(ctx corerpc.Ctx, ntf *handler.CS_CancelMatch_Ntf) {}

type ServiceImpl struct{ module *Module }

func (s *ServiceImpl) StartMatch(ctx corerpc.Ctx, req *service.RPC_StartMatch_Req, reply corerpc.Reply[*service.RPC_StartMatch_Rsp]) {
	reply(&service.RPC_StartMatch_Rsp{}, nil)
}

func (s *ServiceImpl) CancelMatch(ctx corerpc.Ctx, ntf *service.RPC_CancelMatch_Ntf) {}
