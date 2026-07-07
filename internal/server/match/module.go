package match

import (
	"context"
	"fmt"
	"sync"

	"project/internal/core/app"
	"project/internal/core/ragent/sdk"
	ragentwire "project/internal/core/ragent/wire"
)

const moduleName = "match"

type Module struct {
	app.BaseModule
	ready    *app.Ready
	cfg      *MatchConfigEntry
	client   *sdk.Client
	stopOnce sync.Once
}

func NewModule() *Module {
	return &Module{ready: app.NewReady()}
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	entry := matchConfigEntry
	if entry == nil {
		return fmt.Errorf("match config entry is nil")
	}
	m.cfg = entry
	cfg := entry.Get()
	if cfg == nil {
		return fmt.Errorf("match config is nil")
	}
	poster, ok := m.App().(app.Poster)
	if !ok {
		return fmt.Errorf("match app does not implement poster")
	}
	m.client = sdk.NewClient(m.App().NodeIDUint32(), cfg.RouteragentSockPath, poster, m.handleRagentFrame)
	return nil
}

func (m *Module) AfterInit() error {
	if err := m.client.Connect(); err != nil {
		m.ready.Fail(err)
		return err
	}
	m.ready.Done()
	return nil
}

func (m *Module) WaitReady(ctx context.Context) error { return m.ready.WaitReady(ctx) }

func (m *Module) BeforeShutdown() {
	m.stopOnce.Do(func() {
		if m.client != nil {
			_ = m.client.Close()
		}
	})
}

func (m *Module) Shutdown() {}

func (m *Module) handleRagentFrame(frame ragentwire.Frame) {
	// TODO: register RPC/notify routes for matchsvr when service protocols are added.
}
