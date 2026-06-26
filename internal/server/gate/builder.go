package gate

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	config "project/internal/core/config"
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
	commonEntry := mustLoadCommonConfig()
	gateEntry := mustLoadGateConfig()
	initLogger()

	base := app.NewBaseBuilder(nil)
	cfg := gateEntry.Get()

	listenAddr := cfg.Gate.ListenTcp
	if opts.ListenAddr != "" {
		listenAddr = opts.ListenAddr
	}

	base.AddModule("logger", NewLoggerModule(nil))
	base.AddModule("config", NewConfigModule(commonEntry, gateEntry))
	base.AddModule("gate.session", NewSessionModule(int(cfg.Gate.MaxConn)))
	base.AddModule("gate.acceptor", NewAcceptorModule(listenAddr))

	return &Builder{BaseBuilder: base}
}

func mustLoadCommonConfig() *config.ConfigEntry[configgen.CommonConfig] {
	entry, err := configgen.NewCommonConfigEntry(commonConfigPath)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	return entry
}

func mustLoadGateConfig() *config.ConfigEntry[configgen.GateConfig] {
	entry, err := configgen.NewGateConfigEntry(gateConfigPath)
	if err != nil {
		panic(fmt.Errorf("load gate config: %w", err))
	}
	return entry
}

func initLogger() {
	log, err := logger.NewZapDevelopment()
	if err != nil {
		panic(fmt.Errorf("init gate logger: %w", err))
	}
	logger.SetGlobal(log)
}
