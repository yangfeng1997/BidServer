package gate

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	"project/internal/core/logger"
	opt "project/internal/core/options"
)

type Builder struct {
	*app.BaseBuilder
}

func NewGateBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	gateConfig := mustLoadGateConfig(opts.GateConfigPath)
	SetCommonConfigEntry(commonConfig)
	SetGateConfigEntry(gateConfig)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, gateConfig.Get().LoggerGroup)

	baseBuilder := app.NewBaseBuilder(nil)
	baseBuilder.SetDaemon(opts.Daemon)
	baseBuilder.SetPprof(opts.Pprof, opts.PprofAddr)
	baseBuilder.AddShutdownHook(loggerGroup.Shutdown)
	baseBuilder.AddReloadHook(ReloadConfig)

	return &Builder{BaseBuilder: baseBuilder}
}

func mustLoadCommonConfig(path string) *CommonConfigEntry {
	entry, err := configgen.NewCommonConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	return entry
}

func mustLoadGateConfig(path string) *GateConfigEntry {
	entry, err := configgen.NewGateConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load gate config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *logger.LoggerGroup {
	group, err := logger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init gate logger: %w", err))
	}
	return group
}
