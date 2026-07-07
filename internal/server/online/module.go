package online

import (
	"context"
	"fmt"
	"sync"
	"time"

	"project/internal/core/app"
	"project/internal/core/ragent/sdk"
	ragentwire "project/internal/core/ragent/wire"
	onlineinternal "project/internal/server/online/internal"
	"project/pkg/timewheel"
)

const moduleName = "online"

type Module struct {
	app.BaseModule
	ready    *app.Ready
	cfg      *OnlineConfigEntry
	client   *sdk.Client
	tw       *timewheel.TimeWheel
	dir      *onlineinternal.Directory
	stopOnce sync.Once
}

func NewModule() *Module {
	return &Module{ready: app.NewReady()}
}

func (m *Module) Name() string { return moduleName }

func (m *Module) Init() error {
	entry := onlineConfigEntry
	if entry == nil {
		return fmt.Errorf("online config entry is nil")
	}
	m.cfg = entry
	cfg := entry.Get()
	if cfg == nil {
		return fmt.Errorf("online config is nil")
	}
	poster, ok := m.App().(app.Poster)
	if !ok {
		return fmt.Errorf("online app does not implement poster")
	}
	m.client = sdk.NewClient(m.App().NodeIDUint32(), cfg.RouteragentSockPath, poster, m.handleRagentFrame)
	m.tw = timewheel.New(100*time.Millisecond, 512)
	m.dir = onlineinternal.NewDirectory(m.tw, 30*time.Second)
	return nil
}

func (m *Module) AfterInit() error {
	if err := m.client.Connect(); err != nil {
		m.ready.Fail(err)
		return err
	}
	m.tw.Start()
	m.ready.Done()
	return nil
}

func (m *Module) WaitReady(ctx context.Context) error { return m.ready.WaitReady(ctx) }

func (m *Module) BeforeShutdown() {
	m.stopOnce.Do(func() {
		if m.client != nil {
			_ = m.client.Close()
		}
		if m.tw != nil {
			m.tw.Close()
		}
	})
}

func (m *Module) Shutdown() {}

func (m *Module) handleRagentFrame(frame ragentwire.Frame) {
	// TODO: register RPC/notify routes for onlinesvr when service protocols are added.
}
