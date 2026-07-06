package routeragent

import (
	"context"
	"fmt"

	"project/internal/core/app"
	ragentagent "project/internal/core/ragent/agent"
)

const moduleName = "routeragent"

// Module is the App lifecycle adapter for the RouterAgent runtime.
type Module struct {
	app.BaseModule
	runtime *ragentagent.Runtime
}

func NewModule(runtime *ragentagent.Runtime) *Module {
	if runtime == nil {
		runtime = ragentagent.NewRuntime()
	}
	return &Module{runtime: runtime}
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	if m.runtime == nil {
		return fmt.Errorf("routeragent runtime is nil")
	}
	if poster, ok := m.App().(app.Poster); ok {
		m.runtime.SetPoster(poster)
	}
	m.runtime.SetNodeID(m.App().NodeID(), m.App().NodeIDUint32())
	return m.runtime.Init()
}

func (m *Module) AfterInit() error {
	if m.runtime == nil {
		return fmt.Errorf("routeragent runtime is nil")
	}
	return m.runtime.AfterInit()
}

func (m *Module) WaitReady(ctx context.Context) error {
	if m.runtime == nil {
		return fmt.Errorf("routeragent runtime is nil")
	}
	return m.runtime.WaitReady(ctx)
}

func (m *Module) BeforeShutdown() {
	if m.runtime != nil {
		m.runtime.BeforeShutdown()
	}
}

func (m *Module) Shutdown() {
	if m.runtime != nil {
		m.runtime.Shutdown()
	}
}

func (m *Module) Runtime() *ragentagent.Runtime { return m.runtime }
