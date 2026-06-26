package lobby

import (
	"project/internal/core/app"
	"project/pkg/logger"
)

type LoggerModule struct {
	app.BaseModule
	closer *logger.LogCloser
}

func NewLoggerModule(closer *logger.LogCloser) *LoggerModule {
	return &LoggerModule{closer: closer}
}

func (module *LoggerModule) Init(app.App) error {
	logger.Info("lobby logger module initialized")
	return nil
}

func (module *LoggerModule) Stop() {
	if module.closer != nil {
		if err := module.closer.Close(); err != nil {
			logger.Error("close lobby logger failed", logger.Err(err))
		}
	}
}
