package routeragent

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

func NewRouteragentBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	routeragentConfig := mustLoadRouteragentConfig(opts.RouteragentConfigPath)
	SetCommonConfigEntry(commonConfig)
	SetRouteragentConfigEntry(routeragentConfig)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, routeragentConfig.Get().LoggerGroup)

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

func mustLoadRouteragentConfig(path string) *RouteragentConfigEntry {
	entry, err := configgen.NewRouteragentConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load routeragent config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *logger.LoggerGroup {
	group, err := logger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init routeragent logger: %w", err))
	}
	return group
}
