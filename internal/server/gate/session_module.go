package gate

import (
	"project/internal/core/app"
	"project/pkg/logger"
)

type SessionModule struct {
	app.BaseModule
	maxConn int
}

func NewSessionModule(maxConn int) *SessionModule {
	return &SessionModule{maxConn: maxConn}
}

func (module *SessionModule) Init(app.App) error {
	logger.Info("gate session module initialized", logger.Int("max_conn", module.maxConn))
	return nil
}

func (module *SessionModule) BeforeStop() {
	logger.Info("gate session module draining")
}
