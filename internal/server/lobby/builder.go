package lobby

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

func NewLobbyBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	lobbyConfig := mustLoadLobbyConfig(opts.LobbyConfigPath)
	SetCommonConfigEntry(commonConfig)
	SetLobbyConfigEntry(lobbyConfig)

	// 2. 创建LoggerGroup，依赖Option和配置
	loggerGroup := newLoggerGroup(opts.BaseOptions, lobbyConfig.Get().LoggerGroup)

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

func mustLoadLobbyConfig(path string) *LobbyConfigEntry {
	entry, err := configgen.NewLobbyConfigEntry(path)
	if err != nil {
		panic(fmt.Errorf("load lobby config: %w", err))
	}
	return entry
}

func newLoggerGroup(opts opt.BaseOptions, cfg configgen.LoggerGroupConfig) *logger.LoggerGroup {
	group, err := logger.NewLoggerGroup(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init lobby logger: %w", err))
	}
	return group
}
