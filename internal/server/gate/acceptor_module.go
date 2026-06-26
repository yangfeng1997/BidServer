package gate

import (
	"project/internal/core/app"
	"project/pkg/logger"
)

type AcceptorModule struct {
	app.BaseModule
	addr string
}

func NewAcceptorModule(addr string) *AcceptorModule {
	return &AcceptorModule{addr: addr}
}

func (module *AcceptorModule) Init(app.App) error {
	logger.Info("gate acceptor initialized", logger.String("addr", module.addr))
	return nil
}

func (module *AcceptorModule) Stop() {
	logger.Info("gate acceptor stopped", logger.String("addr", module.addr))
}
