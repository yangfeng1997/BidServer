package lobby

import (
	"fmt"

	configgen "project/config/gen"
	"project/internal/core/app"
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
	commonEntry, err := configgen.NewCommonConfigEntry(commonConfigPath)
	if err != nil {
		panic(fmt.Errorf("load common config: %w", err))
	}
	lobbyEntry, err := configgen.NewLobbyConfigEntry(lobbyConfigPath)
	if err != nil {
		panic(fmt.Errorf("load lobby config: %w", err))
	}
	log, err := logger.NewZapDevelopment()
	if err != nil {
		panic(fmt.Errorf("init lobby logger: %w", err))
	}
	logger.SetGlobal(log)

	base := app.NewBaseBuilder(nil)
	cfg := lobbyEntry.Get()

	base.AddModule("logger", NewLoggerModule(nil))
	base.AddModule("config", NewConfigModule(commonEntry, lobbyEntry))
	base.AddModule("lobby.session", NewSessionModule(int(cfg.Lobby.MaxPlayer)))
	base.AddModule("lobby.acceptor", NewAcceptorModule(opts.Addr))

	return &Builder{BaseBuilder: base}
}
