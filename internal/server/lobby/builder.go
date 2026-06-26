package lobby

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	corelogger "project/internal/core/logger"
	opt "project/internal/core/options"
)

type Builder struct {
	*app.BaseBuilder
}

func NewLobbyBuilder(opts Options) *Builder {
	// 1. 必须先加载配置
	commonConfig := mustLoadCommonConfig(opts.CommonConfigPath)
	lobbyConfig := mustLoadLobbyConfig(opts.LobbyConfigPath)
	// 2. 创建Logger模块，依赖Option和配置
	loggerModule := newLoggerModule(opts.BaseOptions, lobbyConfig.Get().LogGroup)
	// 3. 创建Config模块
	configModule := NewConfigModule(commonConfig, lobbyConfig)

	baseBuilder := app.NewBaseBuilder(nil)
	baseBuilder.AddModule("logger", loggerModule)
	baseBuilder.AddModule("config", configModule)

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

func newLoggerModule(opts opt.BaseOptions, cfg configgen.LogGroupConfig) *corelogger.LoggerModule {
	module, err := corelogger.NewLoggerModule(opts, cfg)
	if err != nil {
		panic(fmt.Errorf("init lobby logger: %w", err))
	}
	return module
}
