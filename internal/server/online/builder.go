package online

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

func NewOnlineBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	onlineConfig := mustLoadOnlineConfig(opts.OnlineConfigPath)
	SetCommonConfigEntry(commonConfig)
	SetOnlineConfigEntry(onlineConfig)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, onlineConfig.Get().LoggerGroup)

	baseBuilder := app.NewBaseBuilder(nil)
	baseBuilder.SetDaemon(opts.Daemon)
	baseBuilder.SetPprof(opts.Pprof, opts.PprofAddr)
	baseBuilder.SetNodeID(opts.NodeID)
	baseBuilder.AddModule(NewModule())
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

func mustLoadOnlineConfig(path string) *OnlineConfigEntry {
	entry, err := configgen.NewOnlineConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load online config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *logger.LoggerGroup {
	group, err := logger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init online logger: %w", err))
	}
	return group
}
