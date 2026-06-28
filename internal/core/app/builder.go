package app

type Builder interface {
	Build() (App, error)
	AddModule(module Module)
	AddShutdownHook(hook func())
	AddReloadHook(hook func() error)
	SetDaemon(daemon bool)
	SetPprof(enabled bool, addr string)
}

type BaseBuilder struct {
	dieChan             chan bool
	daemon              bool
	pprof               bool
	pprofAddr           string
	moduleRegistrations []moduleWrapper
	shutdownHooks       []func()
	reloadHooks         []func() error
}

func NewBaseBuilder(dieChan chan bool) *BaseBuilder {
	if dieChan == nil {
		dieChan = make(chan bool)
	}

	return &BaseBuilder{
		dieChan: dieChan,
	}
}

func (builder *BaseBuilder) SetDaemon(daemon bool) {
	builder.daemon = daemon
}

func (builder *BaseBuilder) SetPprof(enabled bool, addr string) {
	builder.pprof = enabled
	builder.pprofAddr = addr
}

func (builder *BaseBuilder) AddModule(module Module) {
	builder.moduleRegistrations = append(builder.moduleRegistrations, moduleWrapper{
		name:   module.Name(),
		module: module,
	})
}

func (builder *BaseBuilder) AddShutdownHook(hook func()) {
	builder.shutdownHooks = append(builder.shutdownHooks, hook)
}

func (builder *BaseBuilder) AddReloadHook(hook func() error) {
	builder.reloadHooks = append(builder.reloadHooks, hook)
}

func (builder *BaseBuilder) Build() (App, error) {
	app := NewBaseApp(builder.dieChan, builder.daemon, builder.pprof, builder.pprofAddr, builder.shutdownHooks, builder.reloadHooks)

	for _, registration := range builder.moduleRegistrations {
		if err := app.RegisterModule(registration.module); err != nil {
			return nil, err
		}
	}

	return app, nil
}
