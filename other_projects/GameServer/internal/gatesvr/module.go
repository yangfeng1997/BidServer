package gatesvr

import (
	"context"
	"fmt"
	"sync"

	"project/internal/core/acceptor"
	"project/internal/core/app"
	"project/internal/core/dispatcher"
	"project/internal/core/session"
	genroutes "project/protocol/gen"
)

// gatesvr 业务模块
type Module struct {
	app.BaseModule
	poster     app.Poster
	sessions   *session.SessionManager
	dispatcher *dispatcher.GateDispatcher
	acceptor   *acceptor.TCPAcceptor
	pending    *PendingMap
	listenAddr string
	ready      *app.Ready
	stopOnce   sync.Once
	stopCh     chan struct{}
}

// NewModule 创建 gatesvr 模块
func NewModule(listenAddr string) *Module {
	if listenAddr == "" {
		listenAddr = "0.0.0.0:7001"
	}
	return &Module{
		listenAddr: listenAddr,
		ready:      app.NewReady(),
		stopCh:     make(chan struct{}),
	}
}

// 保存主循环投递器
func (m *Module) Init(a *app.App) error {
	m.poster = a
	return nil
}

// 初始化监听器、分发器、中间件和握手
func (m *Module) AfterInit() error {
	m.sessions = session.NewSessionManager()
	m.pending = NewPendingMap()
	m.dispatcher = dispatcher.NewGateDispatcher(1, m.sessions) // ST_GATESVR=1
	for cmdID, entry := range genroutes.RouteTable {
		m.dispatcher.RegisterRoute(cmdID, dispatcher.RouteEntry{
			CmdID:      cmdID,
			ServerType: entry.ServerType,
			Route:      entry.Route,
			RspCmdID:   entry.RspCmdID,
		})
	}
	m.dispatcher.SetForward(m.forwardToBackend)
	m.dispatcher.Use(dispatcher.RecoverMiddleware())
	m.dispatcher.Use(dispatcher.AuthMiddleware(genroutes.AuthWhitelist))
	m.dispatcher.SetHandshakeHandler(m.handleHandshake)
	m.acceptor = acceptor.NewTCPAcceptor(m.listenAddr)
	if err := m.acceptor.Listen(); err != nil {
		return err
	}
	m.ready.Done()
	go m.acceptLoop()
	return nil
}

// 等待 gatesvr 就绪
func (m *Module) WaitReady(ctx context.Context) error {
	return m.ready.WaitReady(ctx)
}

// 停止监听和后台循环
func (m *Module) BeforeStop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	if m.acceptor != nil {
		_ = m.acceptor.Close()
	}
}

// 预留
func (m *Module) Fini() {}

// 返回模块状态
func (m *Module) DebugString() string {
	return fmt.Sprintf("gatesvr(listen=%s)", m.listenAddr)
}
