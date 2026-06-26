package lobby

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
	config "project/internal/core/config"
	"project/pkg/logger"
)

const (
	commonConfigPath = "run/common/conf/common.yaml"
	lobbyConfigPath  = "run/lobbysvr/conf/lobby.yaml"
)

type Builder struct {
	*app.BaseBuilder
}

func NewBuilder(opts Options) *Builder {
	commonEntry := mustLoadCommonConfig()
	lobbyEntry := mustLoadLobbyConfig()
	initLogger()

	base := app.NewBaseBuilder(nil)
	cfg := lobbyEntry.Get()

	base.AddModule("logger", NewLoggerModule(nil))
	base.AddModule("config", NewConfigModule(commonEntry, lobbyEntry))
	base.AddModule("lobby.session", NewSessionModule(int(cfg.Lobby.MaxPlayer)))
	base.AddModule("lobby.acceptor", NewAcceptorModule(opts.ListenAddr))

	return &Builder{BaseBuilder: base}
}

func mustLoadCommonConfig() *config.ConfigEntry[configgen.CommonConfig] {
	entry, err := configgen.NewCommonConfigEntry(commonConfigPath)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	return entry
}

func mustLoadLobbyConfig() *config.ConfigEntry[configgen.LobbyConfig] {
	entry, err := configgen.NewLobbyConfigEntry(lobbyConfigPath)
	if err != nil {
		panic(fmt.Errorf("load lobby config: %w", err))
	}
	return entry
}

func initLogger() {
	log, err := logger.NewZapDevelopment()
	if err != nil {
		panic(fmt.Errorf("init lobby logger: %w", err))
	}
	logger.SetGlobal(log)
}
