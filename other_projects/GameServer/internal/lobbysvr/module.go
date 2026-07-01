package lobbysvr

import (
	"project/internal/core/app"
	"project/internal/core/dispatcher"
	corerpc "project/internal/core/rpc"
	genhandler "project/protocol/gen/handler"
	genservice "project/protocol/gen/service"
	handler "project/protocol/handler"
	service "project/protocol/service"
)

// Module 是 lobby 业务模块
type Module struct {
	app.BaseModule
	poster      app.Poster
	HandlerImpl HandlerImpl
	ServiceImpl ServiceImpl
}

// NewModule 创建 lobby 模块
func NewModule() *Module {
	m := &Module{}
	m.HandlerImpl.module = m
	m.ServiceImpl.module = m
	return m
}

func (m *Module) Init(a *app.App) error { m.poster = a; return nil }

func (m *Module) AfterInit() error {
	d := dispatcher.New(2) // ST_LOBBYSVR = 2
	genhandler.RegisterLobbyHandler(d, &m.HandlerImpl)
	genservice.RegisterLobbyService(d, &m.ServiceImpl)
	return nil
}

func (m *Module) BeforeStop() {}
func (m *Module) Fini()       {}

// 实现 LobbyHandler
type HandlerImpl struct{ module *Module }

func (h *HandlerImpl) ClaimReward(ctx corerpc.Ctx, req *handler.CS_ClaimReward_Req, reply corerpc.Reply[*handler.SC_ClaimReward_Rsp]) {
	reply(&handler.SC_ClaimReward_Rsp{}, nil)
}

func (h *HandlerImpl) SyncPos(ctx corerpc.Ctx, ntf *handler.CS_SyncPos_Ntf) {}

// 实现 LobbyService
type ServiceImpl struct{ module *Module }

func (s *ServiceImpl) Login(ctx corerpc.Ctx, req *service.RPC_Login_Req, reply corerpc.Reply[*service.RPC_Login_Rsp]) {
	reply(&service.RPC_Login_Rsp{}, nil)
}

func (s *ServiceImpl) PlayerDisconnect(ctx corerpc.Ctx, ntf *service.RPC_PlayerDisconnect_Ntf) {}
