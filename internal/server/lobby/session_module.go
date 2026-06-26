package lobby

import (
	"project/internal/core/app"
	"project/pkg/logger"
)

type SessionModule struct {
	app.BaseModule
	maxPlayer int
}

func NewSessionModule(maxPlayer int) *SessionModule {
	return &SessionModule{maxPlayer: maxPlayer}
}

func (module *SessionModule) Init(app.App) error {
	logger.Info("lobby session module initialized", logger.Int("max_player", module.maxPlayer))
	return nil
}

func (module *SessionModule) BeforeStop() {
	logger.Info("lobby session module draining")
}
