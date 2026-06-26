package gate

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	"project/pkg/logger"
)

const (
	commonConfigPath = "run/common/conf/common.yaml"
	gateConfigPath   = "run/gatesvr/conf/gate.yaml"
)

type Builder struct {
	*app.BaseBuilder
}

func NewBuilder(opts Options) *Builder {
	commonEntry, err := configgen.NewCommonConfigEntry(commonConfigPath)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	gateEntry, err := configgen.NewGateConfigEntry(gateConfigPath)
	if err != nil {
		panic(fmt.Errorf("load gate config: %w", err))
	}
	log, err := logger.NewZapDevelopment()
	if err != nil {
		panic(fmt.Errorf("init gate logger: %w", err))
	}
	logger.SetGlobal(log)

	base := app.NewBaseBuilder(nil)
	cfg := gateEntry.Get()

	listenAddr := cfg.Gate.ListenTcp
	if opts.Addr != "" {
		listenAddr = opts.Addr
	}

	base.AddModule("logger", NewLoggerModule(nil))
	base.AddModule("config", NewConfigModule(commonEntry, gateEntry))
	base.AddModule("gate.session", NewSessionModule(int(cfg.Gate.MaxConn)))
	base.AddModule("gate.acceptor", NewAcceptorModule(listenAddr))

	return &Builder{BaseBuilder: base}
}
